//! The local-AI sidecar's lifecycle: verifying the staged llama.cpp runtime
//! and model file against their locks before every launch, picking a free
//! loopback port and a per-session token, spawning `llama-server`, and
//! waiting for it to answer `/health` - never trusting a file just because
//! it once verified (docs/product/threat-model.md: the model and runtime
//! are re-hashed on every launch, not only after download).
//!
//! `#![allow(dead_code)]`: these are exercised by this module's own tests
//! today; wiring them into `lib.rs`'s `ai_runtime_start`/`ai_status`
//! Tauri commands is the very next step in this same phase, at which
//! point a plain (non-test) build uses them too - the same transitional
//! reasoning `agent::codes` documents on itself.
#![allow(dead_code)]

use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::collections::HashMap;
use std::io;
use std::path::Path;
use std::time::Duration;

/// `runtime.lock.json`: one entry per Rust target triple, naming the
/// asset this build's CI fetched the runtime from and every extracted
/// file's own hash - `internal/ai/cmd/fetchruntime` (Go) populates this
/// file for real; nothing here ever downloads anything itself.
#[derive(Deserialize, Serialize, Debug, Clone, PartialEq)]
pub struct RuntimeLock {
    pub targets: HashMap<String, TargetEntry>,
}

#[derive(Deserialize, Serialize, Debug, Clone, PartialEq)]
pub struct TargetEntry {
    pub asset_url: String,
    pub asset_sha256: String,
    /// file name (relative to the staged runtime directory) -> its own
    /// sha256, hex-encoded lowercase.
    pub files: HashMap<String, String>,
    #[serde(default)]
    pub glibc_min: Option<String>,
    #[serde(default)]
    pub system_libs: Vec<String>,
    pub server_args: Vec<String>,
}

/// Mirrors `internal/aimodel.Meta`'s JSON shape (Go) exactly - written
/// there as `<file>.meta.json` next to a verified download, read here to
/// re-verify the same file before every launch.
#[derive(Deserialize, Serialize, Debug, Clone, PartialEq)]
pub struct ModelMeta {
    pub profile: String,
    pub id: String,
    pub revision: String,
    pub sha256: String,
    pub size_bytes: i64,
    pub downloaded_at: String,
}

fn sha256_hex(path: &Path) -> io::Result<String> {
    let mut file = std::fs::File::open(path)?;
    let mut hasher = Sha256::new();
    io::copy(&mut file, &mut hasher)?;
    Ok(hex::encode(hasher.finalize()))
}

/// Verifies every file `entry` lists is present in `dir` and hashes to
/// exactly the value the lock recorded. Returns the first mismatch or
/// missing file's name and an error code from `super::codes`.
pub fn verify_runtime_files(dir: &Path, entry: &TargetEntry) -> Result<(), (String, String)> {
    let mut names: Vec<&String> = entry.files.keys().collect();
    names.sort(); // deterministic order: the first failure reported is stable across runs
    for name in names {
        let want = &entry.files[name];
        let path = dir.join(name);
        let got = match sha256_hex(&path) {
            Ok(h) => h,
            Err(_) => return Err((name.clone(), super::codes::RUNTIME_MISSING.to_string())),
        };
        if !got.eq_ignore_ascii_case(want) {
            return Err((
                name.clone(),
                super::codes::RUNTIME_HASH_MISMATCH.to_string(),
            ));
        }
    }
    Ok(())
}

/// Verifies a downloaded model file still matches its own `meta.json` -
/// called on every launch, not only right after a download completed.
pub fn verify_model_file(model_path: &Path, meta: &ModelMeta) -> Result<(), String> {
    let metadata =
        std::fs::metadata(model_path).map_err(|_| super::codes::MODEL_MISSING.to_string())?;
    if metadata.len() != meta.size_bytes as u64 {
        return Err(super::codes::MODEL_HASH_MISMATCH.to_string());
    }
    let got = sha256_hex(model_path).map_err(|_| super::codes::MODEL_MISSING.to_string())?;
    if !got.eq_ignore_ascii_case(&meta.sha256) {
        return Err(super::codes::MODEL_HASH_MISMATCH.to_string());
    }
    Ok(())
}

/// Binds an OS-assigned loopback port and immediately releases it -
/// `llama-server` is told to bind this exact port a moment later. There is
/// an inherent, accepted TOCTOU gap between the two (nothing on a single-
/// user desktop machine is expected to race this), the same tradeoff any
/// "ask the OS for a free port" approach makes.
pub fn pick_free_port() -> io::Result<u16> {
    let listener = std::net::TcpListener::bind("127.0.0.1:0")?;
    listener.local_addr().map(|a| a.port())
}

/// A 32-byte OS-random token, hex-encoded - sent as `--api-key` to
/// `llama-server` and as the `Authorization: Bearer` header on every
/// request this session makes to it (ADR 0006).
pub fn generate_token() -> String {
    use rand::RngCore;
    let mut bytes = [0u8; 32];
    rand::thread_rng().fill_bytes(&mut bytes);
    hex::encode(bytes)
}

/// `threads = clamp(physical_cores, 1, 8)`, unless the operator overrode
/// it in settings - a background analysis should not visibly compete with
/// whatever else the machine is doing by claiming every core.
pub fn default_threads(override_threads: Option<usize>) -> usize {
    if let Some(t) = override_threads {
        return t.max(1);
    }
    num_cpus::get_physical().clamp(1, 8)
}

/// Builds the exact `llama-server` argument list: host/port/api-key/model
/// path/thread count first, then the lock's own `server_args` (context
/// size, `--no-jinja`, `--no-webui`, `--parallel 1`, `--timeout 600`,
/// etc.) appended verbatim - this crate does not re-decide what those mean,
/// the lock already pins them for the exact runtime build.
pub fn build_server_args(
    port: u16,
    token: &str,
    model_path: &Path,
    threads: usize,
    extra_args: &[String],
) -> Vec<String> {
    let mut args = vec![
        "--host".to_string(),
        "127.0.0.1".to_string(),
        "--port".to_string(),
        port.to_string(),
        "--api-key".to_string(),
        token.to_string(),
        "-m".to_string(),
        model_path.to_string_lossy().into_owned(),
        "--threads".to_string(),
        threads.to_string(),
    ];
    args.extend(extra_args.iter().cloned());
    args
}

/// Polls `GET {base_url}/health` until it answers 200, a non-loading
/// error status, or `timeout` elapses. `503` is llama-server's own "still
/// loading the model" answer and keeps waiting; a connection failure also
/// keeps waiting (the process may not have bound the port yet). Any other
/// status ends the wait immediately - it is a real answer, not "not ready
/// yet".
pub async fn wait_until_ready(
    base_url: &str,
    timeout: Duration,
    poll_interval: Duration,
) -> Result<(), String> {
    let client = reqwest_like_get(base_url);
    let deadline = tokio::time::Instant::now() + timeout;
    loop {
        match client().await {
            Ok(200) => return Ok(()),
            Ok(503) => {}                // still loading
            Ok(_other) => return Ok(()), // an actual (non-loading) answer counts as "the port is live"
            Err(_) => {}                 // not listening yet, or reset mid-handshake - keep trying
        }
        if tokio::time::Instant::now() >= deadline {
            return Err(super::codes::RUNTIME_NOT_READY.to_string());
        }
        tokio::time::sleep(poll_interval).await;
    }
}

/// A tiny, dependency-free `GET {base}/health` returning just the status
/// code - this module already depends on hyper transitively via `agent`,
/// but reuses raw `TcpStream` + a hand-rolled request line here rather than
/// pulling hyper's client into this file too, since all that is needed is
/// one status line.
fn reqwest_like_get(
    base_url: &str,
) -> impl Fn() -> std::pin::Pin<Box<dyn std::future::Future<Output = io::Result<u16>> + Send>> {
    let base_url = base_url.to_string();
    move || {
        let base_url = base_url.clone();
        Box::pin(async move { http_get_status(&base_url, "/health").await })
    }
}

async fn http_get_status(base_url: &str, path: &str) -> io::Result<u16> {
    use tokio::io::{AsyncReadExt, AsyncWriteExt};
    use tokio::net::TcpStream;

    let rest = base_url.strip_prefix("http://").ok_or_else(|| {
        io::Error::new(
            io::ErrorKind::InvalidInput,
            "base_url must start with http://",
        )
    })?;
    let mut stream = TcpStream::connect(rest).await?;
    let request = format!("GET {path} HTTP/1.1\r\nHost: {rest}\r\nConnection: close\r\n\r\n");
    stream.write_all(request.as_bytes()).await?;
    let mut buf = Vec::new();
    stream.read_to_end(&mut buf).await?;
    let text = String::from_utf8_lossy(&buf);
    let status_line = text.lines().next().unwrap_or("");
    let status = status_line
        .split_whitespace()
        .nth(1)
        .and_then(|s| s.parse::<u16>().ok())
        .ok_or_else(|| {
            io::Error::new(
                io::ErrorKind::InvalidData,
                format!("no status line in response: {status_line:?}"),
            )
        })?;
    Ok(status)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;
    use tokio::io::AsyncWriteExt as _;
    use tokio::net::TcpListener;

    fn write_file(dir: &Path, name: &str, content: &[u8]) -> String {
        let path = dir.join(name);
        std::fs::File::create(&path)
            .unwrap()
            .write_all(content)
            .unwrap();
        hex::encode(Sha256::digest(content))
    }

    #[test]
    fn verify_runtime_files_accepts_correct_hashes() {
        let dir = tempdir();
        let sum = write_file(dir.path(), "llama-server.exe", b"real bytes");
        let entry = TargetEntry {
            asset_url: "x".into(),
            asset_sha256: "x".into(),
            files: HashMap::from([("llama-server.exe".to_string(), sum)]),
            glibc_min: None,
            system_libs: vec![],
            server_args: vec![],
        };
        assert!(verify_runtime_files(dir.path(), &entry).is_ok());
    }

    #[test]
    fn verify_runtime_files_rejects_a_tampered_file() {
        let dir = tempdir();
        write_file(dir.path(), "llama-server.exe", b"tampered bytes");
        let entry = TargetEntry {
            asset_url: "x".into(),
            asset_sha256: "x".into(),
            files: HashMap::from([("llama-server.exe".to_string(), "0".repeat(64))]),
            glibc_min: None,
            system_libs: vec![],
            server_args: vec![],
        };
        let err = verify_runtime_files(dir.path(), &entry).unwrap_err();
        assert_eq!(err.1, super::super::codes::RUNTIME_HASH_MISMATCH);
    }

    #[test]
    fn verify_runtime_files_reports_a_missing_file() {
        let dir = tempdir();
        let entry = TargetEntry {
            asset_url: "x".into(),
            asset_sha256: "x".into(),
            files: HashMap::from([("ghost.dll".to_string(), "0".repeat(64))]),
            glibc_min: None,
            system_libs: vec![],
            server_args: vec![],
        };
        let err = verify_runtime_files(dir.path(), &entry).unwrap_err();
        assert_eq!(err.1, super::super::codes::RUNTIME_MISSING);
    }

    #[test]
    fn verify_model_file_accepts_a_correct_file() {
        let dir = tempdir();
        let content = b"a fake model file, small enough for a test";
        let sum = write_file(dir.path(), "model.gguf", content);
        let meta = ModelMeta {
            profile: "small".into(),
            id: "x".into(),
            revision: "x".into(),
            sha256: sum,
            size_bytes: content.len() as i64,
            downloaded_at: "now".into(),
        };
        assert!(verify_model_file(&dir.path().join("model.gguf"), &meta).is_ok());
    }

    #[test]
    fn verify_model_file_rejects_a_size_mismatch_before_even_hashing() {
        let dir = tempdir();
        let content = b"content";
        let sum = write_file(dir.path(), "model.gguf", content);
        let meta = ModelMeta {
            profile: "small".into(),
            id: "x".into(),
            revision: "x".into(),
            sha256: sum,
            size_bytes: 999999,
            downloaded_at: "now".into(),
        };
        let err = verify_model_file(&dir.path().join("model.gguf"), &meta).unwrap_err();
        assert_eq!(err, crate::ai::codes::MODEL_HASH_MISMATCH);
    }

    #[test]
    fn verify_model_file_rejects_a_tampered_file_of_the_right_size() {
        let dir = tempdir();
        let real = b"AAAAAAAAAA";
        let tampered = b"BBBBBBBBBB"; // same length, different content
        let sum = hex::encode(Sha256::digest(real));
        write_file(dir.path(), "model.gguf", tampered);
        let meta = ModelMeta {
            profile: "small".into(),
            id: "x".into(),
            revision: "x".into(),
            sha256: sum,
            size_bytes: tampered.len() as i64,
            downloaded_at: "now".into(),
        };
        let err = verify_model_file(&dir.path().join("model.gguf"), &meta).unwrap_err();
        assert_eq!(err, crate::ai::codes::MODEL_HASH_MISMATCH);
    }

    #[test]
    fn pick_free_port_returns_a_usable_nonzero_port() {
        let port = pick_free_port().unwrap();
        assert_ne!(port, 0);
        // Immediately usable: nothing else should have grabbed it yet on a
        // single-threaded test machine, and binding it again here proves
        // it really was released.
        assert!(std::net::TcpListener::bind(("127.0.0.1", port)).is_ok());
    }

    #[test]
    fn generate_token_is_32_bytes_of_hex_and_not_constant() {
        let a = generate_token();
        let b = generate_token();
        assert_eq!(a.len(), 64); // 32 bytes, hex-encoded
        assert!(a.chars().all(|c| c.is_ascii_hexdigit()));
        assert_ne!(a, b, "two calls produced the same token");
    }

    #[test]
    fn default_threads_respects_an_explicit_override() {
        assert_eq!(default_threads(Some(3)), 3);
        assert_eq!(
            default_threads(Some(0)),
            1,
            "an override of 0 must not spawn llama-server with zero threads"
        );
    }

    #[test]
    fn default_threads_without_an_override_is_clamped_between_one_and_eight() {
        let t = default_threads(None);
        assert!(
            (1..=8).contains(&t),
            "default_threads() = {t}, want it clamped to [1, 8]"
        );
    }

    #[test]
    fn build_server_args_places_the_model_and_lock_args_correctly() {
        let args = build_server_args(
            4321,
            "tok123",
            Path::new("/models/small.gguf"),
            4,
            &[
                "--no-jinja".to_string(),
                "--ctx-size".to_string(),
                "8192".to_string(),
            ],
        );
        assert_eq!(
            args,
            vec![
                "--host",
                "127.0.0.1",
                "--port",
                "4321",
                "--api-key",
                "tok123",
                "-m",
                "/models/small.gguf",
                "--threads",
                "4",
                "--no-jinja",
                "--ctx-size",
                "8192",
            ]
        );
    }

    /// A minimal in-process HTTP/1.1 server answering one fixed status
    /// line per connection - the "self-exe fake server" the plan calls
    /// for, without needing an actual second compiled binary: what
    /// `wait_until_ready` depends on is exactly this status-line protocol,
    /// not anything llama-server itself does beyond serving it.
    async fn serve_fixed_status(status_sequence: Vec<u16>) -> String {
        use tokio::io::AsyncReadExt as _;

        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        tokio::spawn(async move {
            for status in status_sequence {
                let (mut socket, _) = listener.accept().await.unwrap();
                // The client's request must be read (at least drained)
                // before this end closes the connection: closing a socket
                // on Windows while the peer's request bytes still sit
                // unread in the receive buffer sends an RST instead of a
                // clean FIN, which the client sees as ConnectionReset
                // rather than a completed response - not a real llama-
                // server behavior this test should be exercising at all.
                let mut buf = [0u8; 1024];
                let _ = socket.read(&mut buf).await;
                let body = "ok";
                let response = format!(
                    "HTTP/1.1 {status} X\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}",
                    body.len()
                );
                let _ = socket.write_all(response.as_bytes()).await;
                let _ = socket.shutdown().await;
            }
        });
        format!("http://{addr}")
    }

    #[tokio::test]
    async fn wait_until_ready_returns_ok_immediately_on_a_200() {
        let base = serve_fixed_status(vec![200]).await;
        let result =
            wait_until_ready(&base, Duration::from_secs(2), Duration::from_millis(10)).await;
        assert!(result.is_ok(), "{result:?}");
    }

    #[tokio::test]
    async fn wait_until_ready_keeps_polling_through_a_503_then_succeeds() {
        let base = serve_fixed_status(vec![503, 503, 200]).await;
        let result =
            wait_until_ready(&base, Duration::from_secs(2), Duration::from_millis(10)).await;
        assert!(result.is_ok(), "{result:?}");
    }

    /// TestAnalyzeReturnsAnsweredForACleanFirstAttempt-style isolation:
    /// `is_ok()` alone does not prove 503 is treated any differently from
    /// 200, since both eventually make wait_until_ready return Ok - a
    /// version that returned Ok on the very first 503 would pass the test
    /// above too. This test can only pass if a lone, repeated 503 is
    /// never treated as ready - it must time out.
    #[tokio::test]
    async fn wait_until_ready_never_treats_a_503_alone_as_ready() {
        let base = serve_fixed_status(vec![503; 20]).await;
        let result =
            wait_until_ready(&base, Duration::from_millis(300), Duration::from_millis(10)).await;
        assert_eq!(
            result,
            Err(super::super::codes::RUNTIME_NOT_READY.to_string())
        );
    }

    #[tokio::test]
    async fn wait_until_ready_times_out_if_the_server_never_answers() {
        // Nothing is listening on this port at all.
        let base = "http://127.0.0.1:1";
        let result =
            wait_until_ready(base, Duration::from_millis(200), Duration::from_millis(20)).await;
        assert_eq!(
            result,
            Err(super::super::codes::RUNTIME_NOT_READY.to_string())
        );
    }

    #[test]
    fn runtime_lock_parses_a_real_shaped_document() {
        let json = r#"{
            "targets": {
                "x86_64-pc-windows-msvc": {
                    "asset_url": "https://example.com/llama-b10948-win-x64.zip",
                    "asset_sha256": "aa",
                    "files": {"llama-server.exe": "bb", "ggml.dll": "cc"},
                    "glibc_min": null,
                    "system_libs": [],
                    "server_args": ["--ctx-size", "8192", "--no-jinja"]
                },
                "x86_64-unknown-linux-gnu": {
                    "asset_url": "https://example.com/llama-b10948-linux-x64.zip",
                    "asset_sha256": "dd",
                    "files": {"llama-server": "ee"},
                    "glibc_min": "2.35",
                    "system_libs": ["libgomp1"],
                    "server_args": ["--ctx-size", "8192"]
                }
            }
        }"#;
        let lock: RuntimeLock = serde_json::from_str(json).expect("valid runtime.lock.json");
        assert_eq!(lock.targets.len(), 2);
        let win = &lock.targets["x86_64-pc-windows-msvc"];
        assert_eq!(win.files["llama-server.exe"], "bb");
        assert!(win.glibc_min.is_none());
        let linux = &lock.targets["x86_64-unknown-linux-gnu"];
        assert_eq!(linux.glibc_min.as_deref(), Some("2.35"));
        assert_eq!(linux.system_libs, vec!["libgomp1".to_string()]);
    }

    fn tempdir() -> tempfile_shim::TempDir {
        tempfile_shim::TempDir::new()
    }

    /// A tiny stand-in for the `tempfile` crate (not a dependency of this
    /// crate) - a directory under the OS temp dir, unique per call,
    /// removed when dropped. Good enough for these tests' needs (a place
    /// to write a couple of files) without adding a new dependency for it.
    mod tempfile_shim {
        use std::path::{Path, PathBuf};

        pub struct TempDir(PathBuf);
        impl TempDir {
            pub fn new() -> Self {
                let mut dir = std::env::temp_dir();
                dir.push(format!(
                    "netrewind-ai-test-{}",
                    crate::ai::runtime::generate_token()
                ));
                std::fs::create_dir_all(&dir).unwrap();
                TempDir(dir)
            }
            pub fn path(&self) -> &Path {
                &self.0
            }
        }
        impl Drop for TempDir {
            fn drop(&mut self) {
                let _ = std::fs::remove_dir_all(&self.0);
            }
        }
    }
}
