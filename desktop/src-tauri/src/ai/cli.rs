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
use std::time::Duration;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::process::Command;

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
        let out = repo_root
            .join("desktop")
            .join("src-tauri")
            .join("target")
            .join("netrewind-cli-test.exe");
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
}
