//! Spawning the staged `netrewind` CLI - the one thing that actually
//! builds a prompt, calls a model, and validates its answer
//! (`internal/ai.Analyze`, Go). This crate never reimplements any of that
//! logic; every AI request in the desktop shell is `netrewind ai analyze`
//! over stdin/stdout, exactly like the CLI's own documented contract.
//!
//! `#![allow(dead_code)]`: wired into `lib.rs`'s Tauri commands in the
//! next step of this same phase - see runtime.rs's identical note.
#![allow(dead_code)]

use std::path::{Path, PathBuf};
use std::process::Stdio;
use std::sync::{Arc, Mutex};
use std::time::Duration;
use tokio::io::{AsyncBufReadExt, AsyncReadExt, AsyncWriteExt, BufReader};
use tokio::process::Command;
use tokio::sync::Notify;

/// The staged CLI's expected file name for this platform.
fn cli_file_name() -> &'static str {
    if cfg!(windows) {
        "netrewind.exe"
    } else {
        "netrewind"
    }
}

/// Looks for the staged CLI at `resource_dir/recorder/<name>` - where the
/// Makefile's `desktop-resources` step places it (Windows always; Linux
/// too, so a bundle-source user with no recorder package installed can
/// still use the AI panel). Returns `None` rather than an error: "not
/// staged" is a normal state the caller reports as `cli_missing`, not a
/// failure of this function.
pub fn locate_cli(resource_dir: &Path) -> Option<PathBuf> {
    let candidate = resource_dir.join("recorder").join(cli_file_name());
    if candidate.is_file() {
        Some(candidate)
    } else {
        None
    }
}

/// Runs `netrewind ai analyze`, writing `stdin_json` to its stdin and
/// returning what it wrote to stdout. A non-zero exit is not itself an
/// error here: the CLI's own contract uses exit codes 2/3/4 for verdict-
/// level and transport-level distinctions the caller (`ai_analyze` in
/// `lib.rs`) still needs the JSON body to make - this function's job is
/// only "did the process run and produce output", not to interpret it.
pub async fn spawn_analyze(
    cli_path: &Path,
    stdin_json: &str,
    timeout: Duration,
) -> Result<CliOutput, String> {
    run_with_stdin(cli_path, &["ai", "analyze"], stdin_json, timeout).await
}

/// Runs `netrewind ai report` - a pure text transform (internal/redact)
/// with no model call and no sidecar involved, unlike spawn_analyze. Used
/// to redact a report composed client-side (engine facts + operator notes
/// + model text) before it ever reaches the clipboard, so this one
/// redaction implementation is shared with the debug-prompt-log and
/// persisted-thread paths instead of a second copy living in Rust.
pub async fn spawn_report(
    cli_path: &Path,
    text: &str,
    timeout: Duration,
) -> Result<CliOutput, String> {
    run_with_stdin(cli_path, &["ai", "report"], text, timeout).await
}

/// `--models-dir <dir>` appended when given, matching every model-manager
/// CLI call below - `models_dir` must be `ai::models::models_dir`'s own
/// answer (lib.rs), the exact directory `ai_runtime_start` later resolves
/// a downloaded model's file name against. Without this, the Go CLI would
/// default to its own per-user cache directory, a different path.
fn with_models_dir(mut args: Vec<String>, models_dir: Option<&Path>) -> Vec<String> {
    if let Some(dir) = models_dir {
        args.push("--models-dir".to_string());
        args.push(dir.to_string_lossy().into_owned());
    }
    args
}

/// Runs `netrewind ai model list -o json` - the embedded-or-configured
/// catalogue plus which profiles are already downloaded, request/response
/// like spawn_analyze (fast; no progress to stream).
pub async fn spawn_model_list(
    cli_path: &Path,
    models_dir: Option<&Path>,
    timeout: Duration,
) -> Result<CliOutput, String> {
    let args = with_models_dir(
        vec![
            "ai".into(),
            "model".into(),
            "list".into(),
            "-o".into(),
            "json".into(),
        ],
        models_dir,
    );
    let arg_refs: Vec<&str> = args.iter().map(String::as_str).collect();
    run_with_stdin(cli_path, &arg_refs, "", timeout).await
}

/// Runs `netrewind ai model remove <profile>` - request/response, same
/// reasoning as spawn_model_list.
pub async fn spawn_model_remove(
    cli_path: &Path,
    profile: &str,
    models_dir: Option<&Path>,
    timeout: Duration,
) -> Result<CliOutput, String> {
    let args = with_models_dir(
        vec![
            "ai".into(),
            "model".into(),
            "remove".into(),
            profile.to_string(),
        ],
        models_dir,
    );
    let arg_refs: Vec<&str> = args.iter().map(String::as_str).collect();
    run_with_stdin(cli_path, &arg_refs, "", timeout).await
}

/// One model download's live state, polled by `ai_model_download_status`
/// (lib.rs) the same way the frontend already polls `ai_status` for the
/// sidecar - a download can take minutes, so there is no single
/// request/response call that could return it.
#[derive(Clone, Debug, Default, serde::Serialize)]
pub struct ModelDownloadStatus {
    pub profile: String,
    pub downloaded: u64,
    pub total: u64,
    pub done: bool,
    pub cancelled: bool,
    pub error: Option<String>,
}

/// Spawns `netrewind ai model download <profile> --json` and drives it in
/// the background, updating `progress` as JSON progress lines arrive on
/// its stdout - unlike every other spawn_* here, this returns immediately
/// rather than awaiting the child; `lib.rs` stores `progress` and `cancel`
/// in `AiState` so the status/cancel commands can reach the same download
/// without needing the `Child` handle itself, which `tokio::process::Child`
/// has no way to share between two independent Tauri command calls.
pub fn spawn_model_download(
    cli_path: PathBuf,
    profile: String,
    models_dir: Option<PathBuf>,
    progress: Arc<Mutex<ModelDownloadStatus>>,
    cancel: Arc<Notify>,
) {
    tokio::spawn(async move {
        let args = with_models_dir(
            vec![
                "ai".into(),
                "model".into(),
                "download".into(),
                profile,
                "--json".into(),
            ],
            models_dir.as_deref(),
        );
        let arg_refs: Vec<&str> = args.iter().map(String::as_str).collect();

        let result = run_streaming_with_cancel(&cli_path, &arg_refs, &cancel, |line| {
            apply_download_progress_line(&progress, line);
        })
        .await;

        let Ok(mut p) = progress.lock() else { return };
        match result {
            Ok(true) => p.cancelled = true,
            Ok(false) => p.done = true,
            Err(e) => p.error = Some(e),
        }
    });
}

/// Spawns `cmd` with `args`, calling `on_line` for every line of stdout as
/// it arrives - unlike `run_with_stdin`, which only returns once the whole
/// process has exited, this is what lets `spawn_model_download` report
/// progress during a download that can take minutes. Watches `cancel`
/// concurrently with reading: a signal kills the child and returns
/// `Ok(true)` without waiting to collect stderr (there is nothing useful
/// to report about a process this call itself asked to die). Otherwise
/// `Ok(false)` on a clean exit, or `Err` (the process's own stderr) on a
/// non-zero exit or a spawn/wait failure - the same shape `run_with_stdin`
/// reports errors in, minus the exit code (a cancelled or successful
/// caller never needs it).
///
/// A free function taking `&Notify` (not a `State`/`AiState` type) for the
/// same reason `run_with_stdin` is free-standing: a test can drive it
/// directly against a real hung/echoing process, exactly like
/// `spawn_analyze_times_out_on_a_hung_process` already does for the
/// request/response primitive.
async fn run_streaming_with_cancel<F: FnMut(&str)>(
    cmd: &Path,
    args: &[&str],
    cancel: &Notify,
    mut on_line: F,
) -> Result<bool, String> {
    let mut child = Command::new(cmd)
        .args(args)
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .kill_on_drop(true)
        .spawn()
        .map_err(|e| format!("spawn {cmd:?}: {e}"))?;

    let stdout = child.stdout.take().expect("stdout was piped");
    let mut stderr = child.stderr.take().expect("stderr was piped");
    let mut lines = BufReader::new(stdout).lines();
    let stderr_task = tokio::spawn(async move {
        let mut buf = Vec::new();
        let _ = stderr.read_to_end(&mut buf).await;
        buf
    });

    loop {
        tokio::select! {
            line = lines.next_line() => {
                match line {
                    Ok(Some(l)) => on_line(&l),
                    _ => break,
                }
            }
            _ = cancel.notified() => {
                let _ = child.start_kill();
                return Ok(true);
            }
        }
    }

    let status = child
        .wait()
        .await
        .map_err(|e| format!("wait for {cmd:?}: {e}"))?;
    let stderr_bytes = stderr_task.await.unwrap_or_default();
    if status.success() {
        Ok(false)
    } else {
        Err(format!(
            "exit code {}: {}",
            status.code().unwrap_or(-1),
            String::from_utf8_lossy(&stderr_bytes)
        ))
    }
}

/// One line of the Go CLI's `--json` progress output is either the literal
/// text `done` or a `{"downloaded":N,"total":N}` object
/// (cmd/netrewind/ai_model.go's own progress closure) - anything else
/// (should not happen) is silently ignored rather than treated as an
/// error, since a stray blank line is not worth failing a whole download
/// over.
fn apply_download_progress_line(progress: &Arc<Mutex<ModelDownloadStatus>>, line: &str) {
    let line = line.trim();
    if line.is_empty() {
        return;
    }
    let Ok(mut p) = progress.lock() else { return };
    if line == "done" {
        p.done = true;
        return;
    }
    if let Ok(v) = serde_json::from_str::<serde_json::Value>(line) {
        if let Some(d) = v.get("downloaded").and_then(|x| x.as_u64()) {
            p.downloaded = d;
        }
        if let Some(t) = v.get("total").and_then(|x| x.as_u64()) {
            p.total = t;
        }
    }
}

/// The actual spawn/write-stdin/read/timeout/kill-on-drop mechanism,
/// generic over the command and its arguments so a test can exercise it
/// against a process built specifically to hang - the "cancel mid-
/// analysis kills the sidecar" scenario - without duplicating this logic
/// into a second, untested copy. `spawn_analyze` is the only real caller;
/// this is not a general-purpose process runner meant for other uses.
async fn run_with_stdin(
    cmd: &Path,
    args: &[&str],
    stdin_data: &str,
    timeout: Duration,
) -> Result<CliOutput, String> {
    let mut child = Command::new(cmd)
        .args(args)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .kill_on_drop(true)
        .spawn()
        .map_err(|e| format!("spawn {cmd:?}: {e}"))?;

    if let Some(mut stdin) = child.stdin.take() {
        stdin
            .write_all(stdin_data.as_bytes())
            .await
            .map_err(|e| format!("write to {cmd:?} stdin: {e}"))?;
        // Explicit shutdown, not just dropping the handle: the CLI reads
        // stdin to EOF before it does anything, so it never sees "done"
        // unless the write side is actually closed.
        stdin
            .shutdown()
            .await
            .map_err(|e| format!("close {cmd:?} stdin: {e}"))?;
    }

    let wait = async {
        let mut stdout = Vec::new();
        let mut stderr = Vec::new();
        if let Some(mut s) = child.stdout.take() {
            let _ = s.read_to_end(&mut stdout).await;
        }
        if let Some(mut s) = child.stderr.take() {
            let _ = s.read_to_end(&mut stderr).await;
        }
        let status = child
            .wait()
            .await
            .map_err(|e| format!("wait for {cmd:?}: {e}"))?;
        Ok::<_, String>(CliOutput {
            exit_code: status.code().unwrap_or(-1),
            stdout: String::from_utf8_lossy(&stdout).into_owned(),
            stderr: String::from_utf8_lossy(&stderr).into_owned(),
        })
    };

    // kill_on_drop above is what actually stops the child once `child` is
    // dropped - `tokio::time::timeout` racing `wait` means that drop
    // happens exactly when the timeout wins, not merely when this
    // function eventually returns.
    match tokio::time::timeout(timeout, wait).await {
        Ok(result) => result,
        Err(_) => Err(format!(
            "{cmd:?} did not finish within {}s",
            timeout.as_secs()
        )),
    }
}

pub struct CliOutput {
    pub exit_code: i32,
    pub stdout: String,
    pub stderr: String,
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn locate_cli_finds_a_staged_binary() {
        let dir = std::env::temp_dir().join(format!(
            "netrewind-cli-locate-test-{}",
            super::super::runtime::generate_token()
        ));
        std::fs::create_dir_all(dir.join("recorder")).unwrap();
        std::fs::write(dir.join("recorder").join(cli_file_name()), b"fake").unwrap();
        assert!(locate_cli(&dir).is_some());
        std::fs::remove_dir_all(&dir).ok();
    }

    #[test]
    fn locate_cli_returns_none_when_nothing_is_staged() {
        let dir = std::env::temp_dir().join(format!(
            "netrewind-cli-locate-test-empty-{}",
            super::super::runtime::generate_token()
        ));
        std::fs::create_dir_all(&dir).unwrap();
        assert!(locate_cli(&dir).is_none());
        std::fs::remove_dir_all(&dir).ok();
    }

    /// Builds the real Go `netrewind` binary (this repo already requires
    /// the Go toolchain for the exact same reason the plan's own
    /// `desktop-resources` build step does: the staged CLI IS that
    /// binary) and spawns it for real - not a fake stand-in - to prove
    /// spawn_analyze's stdin/stdout/timeout plumbing against the actual
    /// process it will run in production. Skips (rather than failing) if
    /// `go` is not on PATH, so this test does not block a Rust-only
    /// development environment.
    async fn build_real_netrewind_cli() -> Option<PathBuf> {
        let repo_root = std::path::Path::new(env!("CARGO_MANIFEST_DIR"))
            .join("..")
            .join("..");
        // A unique name per call, not a shared "netrewind-cli-test.exe" -
        // this test suite runs with multiple threads by default, and two
        // tests building and then spawning/removing the same path at the
        // same time raced each other (one test's cleanup deleting the
        // binary a second test was mid-spawn against) until this was
        // fixed.
        let out = repo_root
            .join("desktop")
            .join("src-tauri")
            .join("target")
            .join(format!(
                "netrewind-cli-test-{}.exe",
                super::super::runtime::generate_token()
            ));
        let status = tokio::process::Command::new("go")
            .args(["build", "-o"])
            .arg(&out)
            .arg("./cmd/netrewind")
            .current_dir(&repo_root)
            .status()
            .await;
        match status {
            Ok(s) if s.success() => Some(out),
            _ => None,
        }
    }

    /// Proves the Rust-side timeout wrapper (not the Go CLI's own policy
    /// timeout - a genuinely hung process, the "cancel mid-analysis kills
    /// the sidecar" scenario the plan's own live-verification list names)
    /// actually returns rather than hanging forever. Calls run_with_stdin
    /// directly (the same function spawn_analyze itself calls) against a
    /// real hanging process, rather than reimplementing spawn logic in a
    /// parallel test-only copy that a regression in the real function
    /// could not fail.
    #[tokio::test]
    async fn spawn_analyze_times_out_on_a_hung_process() {
        // Windows' own `timeout.exe` refuses to run at all under
        // redirected stdin ("Input redirection is not supported"), which
        // this call always sets up - `ping` has no such restriction and
        // reliably takes ~30s either way.
        let (cmd, args): (&str, &[&str]) = if cfg!(windows) {
            ("ping.exe", &["-n", "31", "127.0.0.1"])
        } else {
            ("/bin/sh", &["-c", "sleep 30"])
        };
        let start = std::time::Instant::now();
        let result = run_with_stdin(Path::new(cmd), args, "", Duration::from_millis(300)).await;
        assert!(
            result.is_err(),
            "a hung process was not reported as a timeout"
        );
        assert!(
            start.elapsed() < Duration::from_secs(5),
            "took {:?}, want it to return promptly once its own timeout elapsed, not wait for the 30s sleep",
            start.elapsed()
        );
    }

    #[tokio::test]
    async fn spawn_analyze_runs_the_real_cli_end_to_end_against_a_fake_model_server() {
        let Some(cli) = build_real_netrewind_cli().await else {
            eprintln!("skipping: `go` not available to build the real netrewind CLI for this test");
            return;
        };

        // A minimal fake llama-server, same shape as internal/ai's own
        // Go-side tests use: one canned chat-completion response.
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        tokio::spawn(async move {
            let (mut socket, _) = listener.accept().await.unwrap();
            let mut buf = [0u8; 4096];
            let _ = socket.read(&mut buf).await;
            let body = serde_json::json!({
                "choices": [{"message": {"role": "assistant", "content": "{\"summary\":\"ok\",\"ranked_hypotheses\":[],\"evidence_handles\":[],\"counter_evidence\":[],\"unknowns\":[\"nothing conclusive\"],\"confidence_ceiling\":0,\"next_checks\":[]}"}}],
                "timings": {"prompt_ms": 1.0, "predicted_ms": 2.0}
            }).to_string();
            let response = format!(
                "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{}",
                body.len(),
                body
            );
            let _ = socket.write_all(response.as_bytes()).await;
            let _ = socket.shutdown().await;
        });

        let request = serde_json::json!({
            "question": "what happened?",
            "lang": "en",
            "server": {"url": format!("http://{addr}")}
        })
        .to_string();

        let result = spawn_analyze(&cli, &request, Duration::from_secs(10))
            .await
            .unwrap();
        assert_eq!(result.exit_code, 0, "stderr: {}", result.stderr);
        assert!(
            result.stdout.contains("\"verdict\""),
            "stdout does not look like the analyze response: {}",
            result.stdout
        );
        let _ = std::fs::remove_file(&cli);
    }

    #[tokio::test]
    async fn spawn_report_runs_the_real_cli_and_redacts_a_private_address() {
        let Some(cli) = build_real_netrewind_cli().await else {
            eprintln!("skipping: `go` not available to build the real netrewind CLI for this test");
            return;
        };

        let result = spawn_report(
            &cli,
            "gateway at 10.99.0.1 changed hands",
            Duration::from_secs(10),
        )
        .await
        .unwrap();
        assert_eq!(result.exit_code, 0, "stderr: {}", result.stderr);
        assert!(
            !result.stdout.contains("10.99.0.1"),
            "stdout still contains the real address: {}",
            result.stdout
        );
        assert!(
            result.stdout.contains("<HOST_1>"),
            "stdout does not contain the expected placeholder: {}",
            result.stdout
        );
        let _ = std::fs::remove_file(&cli);
    }

    #[tokio::test]
    async fn spawn_model_list_runs_the_real_cli_against_the_embedded_catalogue() {
        let Some(cli) = build_real_netrewind_cli().await else {
            eprintln!("skipping: `go` not available to build the real netrewind CLI for this test");
            return;
        };

        let dir = std::env::temp_dir().join(format!(
            "netrewind-model-list-test-{}",
            super::super::runtime::generate_token()
        ));
        std::fs::create_dir_all(&dir).unwrap();

        let result = spawn_model_list(&cli, Some(&dir), Duration::from_secs(15))
            .await
            .unwrap();
        assert_eq!(result.exit_code, 0, "stderr: {}", result.stderr);
        let rows: serde_json::Value = serde_json::from_str(&result.stdout)
            .unwrap_or_else(|e| panic!("stdout is not JSON: {e}\n{}", result.stdout));
        let profiles: Vec<&str> = rows
            .as_array()
            .expect("a JSON array")
            .iter()
            .map(|r| r["profile"].as_str().unwrap())
            .collect();
        assert_eq!(
            profiles,
            vec!["small", "balanced", "full"],
            "the embedded catalogue's own 3 profiles, in order"
        );

        let _ = std::fs::remove_dir_all(&dir);
        let _ = std::fs::remove_file(&cli);
    }

    #[test]
    fn apply_download_progress_line_reads_a_progress_object() {
        let progress = Arc::new(Mutex::new(ModelDownloadStatus::default()));
        apply_download_progress_line(&progress, r#"{"downloaded":100,"total":1000}"#);
        let p = progress.lock().unwrap();
        assert_eq!(p.downloaded, 100);
        assert_eq!(p.total, 1000);
        assert!(!p.done);
    }

    #[test]
    fn apply_download_progress_line_recognises_the_done_sentinel() {
        let progress = Arc::new(Mutex::new(ModelDownloadStatus::default()));
        apply_download_progress_line(&progress, "done");
        assert!(progress.lock().unwrap().done);
    }

    #[test]
    fn apply_download_progress_line_ignores_garbage_without_panicking() {
        let progress = Arc::new(Mutex::new(ModelDownloadStatus::default()));
        apply_download_progress_line(&progress, "not json at all");
        apply_download_progress_line(&progress, "");
        let p = progress.lock().unwrap();
        assert_eq!(p.downloaded, 0);
        assert!(!p.done);
    }

    /// Proves `on_line` sees every stdout line as it is produced, using a
    /// real (short-lived) process rather than a mock - `cmd.exe /C echo`
    /// on Windows, `printf` elsewhere, matching this file's own convention
    /// of exercising the real spawn/pipe machinery instead of a stand-in.
    #[tokio::test]
    async fn run_streaming_with_cancel_delivers_every_line() {
        let (cmd, args): (&str, &[&str]) = if cfg!(windows) {
            ("cmd.exe", &["/C", "echo one&&echo two"])
        } else {
            ("/bin/sh", &["-c", "printf 'one\\ntwo\\n'"])
        };
        let mut lines = Vec::new();
        let cancel = Notify::new();
        let cancelled = run_streaming_with_cancel(Path::new(cmd), args, &cancel, |l| {
            lines.push(l.trim().to_string())
        })
        .await
        .unwrap();
        assert!(!cancelled);
        assert_eq!(lines, vec!["one", "two"]);
    }

    /// Proves a signal on `cancel` actually kills the child promptly
    /// instead of waiting for it to finish on its own - the same
    /// hung-process shape `spawn_analyze_times_out_on_a_hung_process` uses,
    /// but cancelled explicitly rather than by a timeout.
    #[tokio::test]
    async fn run_streaming_with_cancel_kills_a_hung_process_when_signalled() {
        let (cmd, args): (&str, &[&str]) = if cfg!(windows) {
            ("ping.exe", &["-n", "31", "127.0.0.1"])
        } else {
            ("/bin/sh", &["-c", "sleep 30"])
        };
        let cancel = Arc::new(Notify::new());
        let cancel_for_signal = cancel.clone();
        tokio::spawn(async move {
            tokio::time::sleep(Duration::from_millis(200)).await;
            cancel_for_signal.notify_one();
        });
        let start = std::time::Instant::now();
        let cancelled = run_streaming_with_cancel(Path::new(cmd), args, &cancel, |_| {})
            .await
            .unwrap();
        assert!(cancelled, "expected Ok(true) for a cancelled run");
        assert!(
            start.elapsed() < Duration::from_secs(5),
            "took {:?}, want it to return promptly once cancelled, not wait for the 30s process",
            start.elapsed()
        );
    }
}
