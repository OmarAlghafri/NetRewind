//! The NetRewind desktop shell: a viewer for the recorder's local API and
//! for evidence bundles. It never modifies the record - every command here
//! reads.

mod agent;
mod ai;
mod bundle;

use agent::AgentError;
use serde::Serialize;
use std::collections::HashMap;
use std::sync::{Arc, Mutex};
use std::time::Duration;
use tauri::State;
use tokio::sync::Notify;

/// A request `agent_cancel` was asked to stop fails with this exact code
/// (`agent::codes::CANCELLED`) - the frontend checks for it specifically,
/// so an intentional cancellation (the investigation moved on before the
/// poll finished) never shows the user a scary "the recorder is
/// unreachable" message the way a real transport failure should.
fn cancelled_error() -> AgentError {
    AgentError::new(agent::codes::CANCELLED, &[], "cancelled")
}

/// One `Notify` per in-flight request that was given a `request_id`,
/// keyed by that id so `agent_cancel` can find and wake the specific
/// request being superseded - not every in-flight request at once, which
/// would cancel a poll for a different page's data along with the stale
/// one. Cleaned up (removed) the moment its request finishes, cancelled or
/// not, so a long session does not accumulate finished entries.
#[derive(Default)]
struct AgentState {
    inflight: Mutex<HashMap<String, Arc<Notify>>>,
}

/// Command-line options the viewer was started with, applied by the
/// frontend on top of its saved settings for this run only (nothing here
/// is persisted). `netrewind-desktop --source live --page incidents` opens
/// straight onto the live incidents; `--bundle <file>` opens a bundle.
#[derive(Serialize, Default)]
struct LaunchOptions {
    source: Option<String>,
    endpoint: Option<String>,
    bundle: Option<String>,
    page: Option<String>,
    lang: Option<String>,
    no_wizard: bool,
}

fn parse_launch_options(args: impl Iterator<Item = String>) -> LaunchOptions {
    let mut opts = LaunchOptions::default();
    let mut args = args.peekable();
    while let Some(arg) = args.next() {
        let mut value = |opts_field: &mut Option<String>| {
            if let Some(v) = args.next() {
                *opts_field = Some(v);
            }
        };
        match arg.as_str() {
            "--source" => value(&mut opts.source),
            "--endpoint" => value(&mut opts.endpoint),
            "--bundle" => value(&mut opts.bundle),
            "--page" => value(&mut opts.page),
            "--lang" => value(&mut opts.lang),
            "--no-wizard" => opts.no_wizard = true,
            _ => {}
        }
    }
    if opts.bundle.is_some() && opts.source.is_none() {
        opts.source = Some("bundle".to_string());
    }
    opts
}

#[tauri::command]
fn launch_options() -> LaunchOptions {
    parse_launch_options(std::env::args().skip(1))
}

#[derive(Serialize)]
struct AgentResponse {
    status: u16,
    /// The response body as text (the API is JSON).
    body: String,
    /// Lower-cased header names ("etag", not "ETag") - what
    /// /v1/rules and /v1/capabilities' conditional-GET support (ADR 0005)
    /// needs read back on the frontend.
    headers: HashMap<String, String>,
}

/// The endpoint the recorder listens on by default on this platform.
#[tauri::command]
fn agent_default_endpoint() -> String {
    agent::default_endpoint()
}

/// Races `future` against `notify` (when given) being triggered, favouring
/// whichever finishes first the way `tokio::select!` always does. `None`
/// runs `future` to completion with no cancellation path at all - the
/// no-`request_id` case, kept as a real code path rather than always
/// registering a `Notify` nothing will ever fire.
///
/// A free function taking `Option<&Notify>` instead of `agent_get`'s own
/// `Option<Arc<Notify>>`/`State` so it can be unit tested directly against
/// a fake slow future, without spinning up a Tauri app just to exercise
/// the one part of this command that has real logic worth getting wrong.
async fn race_cancellable<F, T, E>(future: F, notify: Option<&Notify>, cancelled: E) -> Result<T, E>
where
    F: std::future::Future<Output = Result<T, E>>,
{
    match notify {
        Some(n) => {
            tokio::select! {
                r = future => r,
                _ = n.notified() => Err(cancelled),
            }
        }
        None => future.await,
    }
}

/// GET `path` (for example `/v1/events?limit=200`) from the recorder at
/// `endpoint` (empty means the platform default). `headers` lets the
/// frontend send `If-None-Match` for a conditional GET.
///
/// `request_id`, when given, registers this call so a later `agent_cancel`
/// with the same id can interrupt it - real cancellation of the
/// in-flight named-pipe/socket read, not merely the frontend choosing to
/// ignore whatever answer eventually arrives (which `useRecord.ts`'s
/// `generation` counter already handled; this is the piece that also
/// stops the wasted work itself, addressing the wasted work a superseded
/// poll leaves running otherwise).
#[tauri::command]
async fn agent_get(
    endpoint: String,
    path: String,
    headers: Option<HashMap<String, String>>,
    request_id: Option<String>,
    state: State<'_, AgentState>,
) -> Result<AgentResponse, AgentError> {
    if !path.starts_with("/v1/") {
        return Err(AgentError::new(
            agent::codes::INVALID_PATH,
            &[],
            "only /v1/ paths are served",
        ));
    }
    let endpoint = if endpoint.trim().is_empty() {
        agent::default_endpoint()
    } else {
        endpoint
    };
    let extra_headers: Vec<(String, String)> = headers.unwrap_or_default().into_iter().collect();

    let notify = request_id.as_ref().map(|id| {
        let n = Arc::new(Notify::new());
        state.inflight.lock().unwrap().insert(id.clone(), n.clone());
        n
    });

    let result = race_cancellable(
        agent::get(&endpoint, &path, &extra_headers),
        notify.as_deref(),
        cancelled_error(),
    )
    .await;

    if let Some(id) = &request_id {
        state.inflight.lock().unwrap().remove(id);
    }

    let resp = result?;
    Ok(AgentResponse {
        status: resp.status,
        body: String::from_utf8_lossy(&resp.body).into_owned(),
        headers: resp.headers.into_iter().collect(),
    })
}

/// Whether `path` is inside the one write surface the record's own
/// read-only API has (`/v1/notes/*` - internal/api/v1/notes.go). A plain
/// prefix check, factored out of `agent_request` so this security
/// boundary is directly unit-testable rather than only reachable through
/// a full Tauri command invocation.
fn is_notes_path(path: &str) -> bool {
    path == "/v1/notes" || path.starts_with("/v1/notes/") || path.starts_with("/v1/notes?")
}

/// `method path` (PUT/POST/DELETE) against the recorder at `endpoint`,
/// restricted to `/v1/notes/*` - the one write surface the record's own
/// read-only API has (internal/api/v1/notes.go; the record itself,
/// events.db, has none). `agent_get` stays the only way to reach every
/// other route: this command exists specifically for the operator's own
/// annotations/feedback/history settings, never widened to an arbitrary
/// path the way that would blur the record's read-only guarantee this
/// shell has always relied on.
#[tauri::command]
async fn agent_request(
    endpoint: String,
    method: String,
    path: String,
    headers: Option<HashMap<String, String>>,
    body: Option<String>,
    request_id: Option<String>,
    state: State<'_, AgentState>,
) -> Result<AgentResponse, AgentError> {
    if !is_notes_path(&path) {
        return Err(AgentError::new(
            agent::codes::INVALID_PATH,
            &[],
            "only /v1/notes paths accept a write",
        ));
    }
    let endpoint = if endpoint.trim().is_empty() {
        agent::default_endpoint()
    } else {
        endpoint
    };
    let extra_headers: Vec<(String, String)> = headers.unwrap_or_default().into_iter().collect();
    let body_bytes = body.map(|b| b.into_bytes());

    let notify = request_id.as_ref().map(|id| {
        let n = Arc::new(Notify::new());
        state.inflight.lock().unwrap().insert(id.clone(), n.clone());
        n
    });

    let result = race_cancellable(
        agent::request(&endpoint, &method, &path, &extra_headers, body_bytes),
        notify.as_deref(),
        cancelled_error(),
    )
    .await;

    if let Some(id) = &request_id {
        state.inflight.lock().unwrap().remove(id);
    }

    let resp = result?;
    Ok(AgentResponse {
        status: resp.status,
        body: String::from_utf8_lossy(&resp.body).into_owned(),
        headers: resp.headers.into_iter().collect(),
    })
}

/// Interrupts the in-flight `agent_get` call registered under
/// `request_id`, if it is still running. A no-op (not an error) if the
/// request already finished - cancellation racing completion is normal,
/// not a bug to report.
#[tauri::command]
fn agent_cancel(request_id: String, state: State<'_, AgentState>) {
    if let Some(n) = state.inflight.lock().unwrap().get(&request_id) {
        n.notify_one();
    }
}

#[derive(Serialize)]
struct ExportResult {
    bytes: u64,
    path: String,
}

/// Asks the recorder for an evidence bundle of the given window and writes
/// it to `dest` (a path the user chose in a save dialog).
#[tauri::command]
async fn agent_export_bundle(
    endpoint: String,
    query: String,
    dest: String,
) -> Result<ExportResult, String> {
    let endpoint = if endpoint.trim().is_empty() {
        agent::default_endpoint()
    } else {
        endpoint
    };
    let path = if query.is_empty() {
        "/v1/bundle".to_string()
    } else {
        format!("/v1/bundle?{query}")
    };
    let resp = agent::get(&endpoint, &path, &[]).await?;
    if resp.status != 200 {
        return Err(format!(
            "the recorder refused the export ({}): {}",
            resp.status,
            String::from_utf8_lossy(&resp.body)
        ));
    }
    // Written to a temporary name and renamed, so a half-written file never
    // sits at the chosen path.
    let tmp = format!("{dest}.part");
    std::fs::write(&tmp, &resp.body).map_err(|e| format!("cannot write {tmp}: {e}"))?;
    std::fs::rename(&tmp, &dest)
        .map_err(|e| format!("cannot move the bundle into place at {dest}: {e}"))?;
    Ok(ExportResult {
        bytes: resp.body.len() as u64,
        path: dest,
    })
}

/// Verifies and parses an evidence bundle file for viewing. `public_key`,
/// when set, makes a valid signature mandatory.
#[tauri::command]
fn bundle_open(path: String, public_key: Option<String>) -> Result<bundle::Contents, String> {
    bundle::open(&path, public_key.as_deref())
}

/// The running sidecar, if `ai_runtime_start` has ever succeeded and
/// `ai_runtime_stop` (or app exit) has not since stopped it - the
/// port/token every `ai_analyze` call in the meantime must use. Dropping
/// `child` (replacing it with `None`, or the whole `AiState` on app exit)
/// kills the process (`kill_on_drop`, set where it is spawned) rather
/// than leaving an orphaned llama-server behind.
struct Sidecar {
    child: tokio::process::Child,
    port: u16,
    token: String,
}

/// One local-AI sidecar for the whole app session - never more than one
/// llama-server at a time, matching there being exactly one model
/// profile active at once in the plan's own design. `download` holds the
/// same one-at-a-time model for a download in progress: the shared
/// progress state and the cancel signal `ai::cli::spawn_model_download`'s
/// background task reads, so a later command can poll or cancel it
/// without needing the `Child` handle itself.
#[derive(Default)]
struct AiState {
    sidecar: tokio::sync::Mutex<Option<Sidecar>>,
    download: tokio::sync::Mutex<Option<DownloadHandle>>,
}

struct DownloadHandle {
    progress: Arc<Mutex<ai::cli::ModelDownloadStatus>>,
    cancel: Arc<Notify>,
}

#[derive(Serialize)]
struct AiStatusResponse {
    running: bool,
    port: Option<u16>,
}

/// Whether a sidecar is currently running, and on which port - enough for
/// the frontend to decide whether `ai_analyze` can be called yet, without
/// exposing the token (that stays entirely server-side, attached by
/// `ai_analyze` itself, never sent to the frontend to relay back).
#[tauri::command]
async fn ai_status(state: State<'_, AiState>) -> Result<AiStatusResponse, String> {
    let mut guard = state.sidecar.lock().await;
    // try_wait() is Ok(Some(_)) once the child has actually exited (a
    // crash, or llama-server refusing its own arguments) - reported as
    // "not running" rather than as whatever state this struct was left in
    // right after spawn, which kill_on_drop alone would not catch until
    // this whole AiState is dropped.
    if let Some(sidecar) = guard.as_mut() {
        if matches!(sidecar.child.try_wait(), Ok(Some(_))) {
            *guard = None;
        }
    }
    Ok(AiStatusResponse {
        running: guard.is_some(),
        port: guard.as_ref().map(|s| s.port),
    })
}

/// Stops the running sidecar, if any - a no-op, not an error, if none is
/// running (stopping something already stopped is not a failure).
#[tauri::command]
async fn ai_runtime_stop(state: State<'_, AiState>) -> Result<(), String> {
    *state.sidecar.lock().await = None;
    Ok(())
}

/// Verifies the staged runtime and model files against their own locks,
/// picks a free loopback port and a fresh per-session token, and spawns
/// `llama-server`, replacing any sidecar already running. Returns once
/// `/health` answers - the frontend does not poll for readiness itself.
///
/// `model_file_name` names the model already downloaded under
/// `ai::models::models_dir` (by `netrewind ai model download`, spawned
/// separately - this command never downloads anything). `extra_args` are
/// the runtime lock's own `server_args` for this platform, read by the
/// frontend from the staged `runtime.lock.json` alongside this call, kept
/// as a parameter rather than this command re-reading that file itself so
/// there is exactly one place (the frontend's own settings/status flow)
/// that decides which runtime build is in use.
#[tauri::command]
async fn ai_runtime_start(
    app: tauri::AppHandle,
    model_file_name: String,
    extra_args: Vec<String>,
    threads: Option<usize>,
    state: State<'_, AiState>,
) -> Result<AiStatusResponse, String> {
    use tauri::Manager;

    ai::require_enabled()?;

    let resource_dir = app
        .path()
        .resource_dir()
        .map_err(|e| format!("{}: {e}", ai::codes::RUNTIME_MISSING))?;
    let data_dir = app
        .path()
        .app_local_data_dir()
        .map_err(|e| format!("{}: {e}", ai::codes::MODEL_MISSING))?;

    let runtime_dir = resource_dir.join("llama-runtime");
    let lock_path = runtime_dir.join("runtime.lock.json");
    let lock_data = std::fs::read(&lock_path)
        .map_err(|e| format!("{}: {lock_path:?}: {e}", ai::codes::RUNTIME_MISSING))?;
    let lock: ai::runtime::RuntimeLock = serde_json::from_slice(&lock_data)
        .map_err(|e| format!("{}: runtime.lock.json: {e}", ai::codes::RUNTIME_MISSING))?;
    let target = env!("TARGET");
    let entry = lock.targets.get(target).ok_or_else(|| {
        format!(
            "{}: no runtime.lock.json entry for {target}",
            ai::codes::RUNTIME_MISSING
        )
    })?;
    ai::runtime::verify_runtime_files(&runtime_dir, entry)
        .map_err(|(name, code)| format!("{code}: {name}"))?;

    let models_dir = ai::models::models_dir(&data_dir);
    let model_path = models_dir.join(&model_file_name);
    let meta_path = models_dir.join(format!("{model_file_name}.meta.json"));
    let meta_data = std::fs::read(&meta_path)
        .map_err(|e| format!("{}: {meta_path:?}: {e}", ai::codes::MODEL_MISSING))?;
    let meta: ai::runtime::ModelMeta = serde_json::from_slice(&meta_data).map_err(|e| {
        format!(
            "{}: {model_file_name}.meta.json: {e}",
            ai::codes::MODEL_MISSING
        )
    })?;
    ai::runtime::verify_model_file(&model_path, &meta)?;

    let port = ai::runtime::pick_free_port()
        .map_err(|e| format!("{}: {e}", ai::codes::RUNTIME_PORT_UNAVAILABLE))?;
    let token = ai::runtime::generate_token();
    let thread_count = ai::runtime::default_threads(threads);
    let mut args =
        ai::runtime::build_server_args(port, &token, &model_path, thread_count, &entry.server_args);
    args.extend(extra_args);

    let exe = runtime_dir.join(if cfg!(windows) {
        "llama-server.exe"
    } else {
        "llama-server"
    });
    let mut command = tokio::process::Command::new(&exe);
    command.args(&args).kill_on_drop(true);
    #[cfg(windows)]
    {
        // tokio::process::Command exposes this Windows builder method
        // directly (no std::os::windows trait import needed).
        const CREATE_NO_WINDOW: u32 = 0x0800_0000;
        const BELOW_NORMAL_PRIORITY_CLASS: u32 = 0x0000_4000;
        command.creation_flags(CREATE_NO_WINDOW | BELOW_NORMAL_PRIORITY_CLASS);
    }
    let child = command
        .spawn()
        .map_err(|e| format!("{}: {exe:?}: {e}", ai::codes::RUNTIME_SPAWN_FAILED))?;

    let mut guard = state.sidecar.lock().await;
    *guard = Some(Sidecar {
        child,
        port,
        token: token.clone(),
    });
    drop(guard);

    ai::runtime::wait_until_ready(
        &format!("http://127.0.0.1:{port}"),
        Duration::from_secs(120),
        Duration::from_millis(250),
    )
    .await?;

    Ok(AiStatusResponse {
        running: true,
        port: Some(port),
    })
}

/// Runs one analysis: injects the running sidecar's own url/token into
/// `request_json` (never sent from the frontend - the token is this
/// process's own secret) and spawns `netrewind ai analyze` with it,
/// returning its stdout verbatim (the frontend parses the same
/// `aiAnalyzeResponse` shape `netrewind ai analyze` always produces).
/// Fails with `runtime_not_ready` if no sidecar is running yet.
#[tauri::command]
async fn ai_analyze(
    app: tauri::AppHandle,
    request_json: String,
    timeout_seconds: Option<u64>,
    state: State<'_, AiState>,
) -> Result<String, String> {
    use tauri::Manager;

    ai::require_enabled()?;

    let (port, token) = {
        let guard = state.sidecar.lock().await;
        let sidecar = guard
            .as_ref()
            .ok_or_else(|| ai::codes::RUNTIME_NOT_READY.to_string())?;
        (sidecar.port, sidecar.token.clone())
    };

    let mut request: serde_json::Value = serde_json::from_str(&request_json)
        .map_err(|e| format!("{}: request_json: {e}", ai::codes::ANALYSIS_INVALID))?;
    request["server"] =
        serde_json::json!({"url": format!("http://127.0.0.1:{port}"), "token": token});

    let resource_dir = app
        .path()
        .resource_dir()
        .map_err(|e| format!("{}: {e}", ai::codes::CLI_MISSING))?;
    let cli_path =
        ai::cli::locate_cli(&resource_dir).ok_or_else(|| ai::codes::CLI_MISSING.to_string())?;

    let timeout = Duration::from_secs(timeout_seconds.unwrap_or(180));
    let output = ai::cli::spawn_analyze(&cli_path, &request.to_string(), timeout)
        .await
        .map_err(|e| format!("{}: {e}", ai::codes::CLI_FAILED))?;
    if output.stdout.trim().is_empty() {
        return Err(format!("{}: {}", ai::codes::CLI_FAILED, output.stderr));
    }
    Ok(output.stdout)
}

/// Redacts a report the panel composed client-side (engine facts +
/// operator notes + local-model text, already concatenated into one
/// document by the caller) before it reaches the clipboard - the one
/// place this text leaves the device. No sidecar involved: this is a pure
/// text transform (`netrewind ai report`, internal/redact), so unlike
/// `ai_analyze` it needs only the staged CLI, never a running model.
#[tauri::command]
async fn ai_report_redact(app: tauri::AppHandle, text: String) -> Result<String, String> {
    use tauri::Manager;

    ai::require_enabled()?;

    let resource_dir = app
        .path()
        .resource_dir()
        .map_err(|e| format!("{}: {e}", ai::codes::CLI_MISSING))?;
    let cli_path =
        ai::cli::locate_cli(&resource_dir).ok_or_else(|| ai::codes::CLI_MISSING.to_string())?;

    let output = ai::cli::spawn_report(&cli_path, &text, Duration::from_secs(30))
        .await
        .map_err(|e| format!("{}: {e}", ai::codes::CLI_FAILED))?;
    if output.exit_code != 0 {
        return Err(format!("{}: {}", ai::codes::CLI_FAILED, output.stderr));
    }
    Ok(output.stdout)
}

/// Resolves the staged CLI path and the model storage directory the same
/// way `ai_runtime_start` does - shared by every model-manager command
/// below so a download always lands exactly where `ai_runtime_start` will
/// later look for it.
fn ai_model_paths(
    app: &tauri::AppHandle,
) -> Result<(std::path::PathBuf, std::path::PathBuf), String> {
    use tauri::Manager;
    let resource_dir = app
        .path()
        .resource_dir()
        .map_err(|e| format!("{}: {e}", ai::codes::CLI_MISSING))?;
    let cli_path =
        ai::cli::locate_cli(&resource_dir).ok_or_else(|| ai::codes::CLI_MISSING.to_string())?;
    let data_dir = app
        .path()
        .app_local_data_dir()
        .map_err(|e| format!("{}: {e}", ai::codes::MODEL_MISSING))?;
    let models_dir = ai::models::models_dir(&data_dir);
    Ok((cli_path, models_dir))
}

/// Lists every profile the embedded catalogue offers (see
/// `internal/aimodel.LoadEmbeddedManifest`) and whether it is already
/// downloaded - never gated by `ai::require_enabled()`: downloading a
/// model file to disk runs no model and produces no analysis, unlike
/// `ai_analyze`/`ai_runtime_start`/`ai_report_redact`, which stay gated.
#[tauri::command]
async fn ai_model_list(app: tauri::AppHandle) -> Result<String, String> {
    let (cli_path, models_dir) = ai_model_paths(&app)?;
    let output = ai::cli::spawn_model_list(&cli_path, Some(&models_dir), Duration::from_secs(15))
        .await
        .map_err(|e| format!("{}: {e}", ai::codes::CLI_FAILED))?;
    if output.exit_code != 0 {
        return Err(format!("{}: {}", ai::codes::CLI_FAILED, output.stderr));
    }
    Ok(output.stdout)
}

/// Starts downloading one profile in the background and returns
/// immediately - refuses if another download is already in flight and not
/// yet finished/failed/cancelled, since only one model is ever active at a
/// time. Poll `ai_model_download_status` for progress.
#[tauri::command]
async fn ai_model_download_start(
    app: tauri::AppHandle,
    profile: String,
    state: State<'_, AiState>,
) -> Result<(), String> {
    let (cli_path, models_dir) = ai_model_paths(&app)?;

    let mut guard = state.download.lock().await;
    if let Some(existing) = guard.as_ref() {
        let p = existing
            .progress
            .lock()
            .map_err(|_| ai::codes::DOWNLOAD_FAILED.to_string())?;
        if !p.done && !p.cancelled && p.error.is_none() {
            return Err(format!(
                "{}: a download is already in progress",
                ai::codes::DOWNLOAD_FAILED
            ));
        }
    }

    let progress = Arc::new(Mutex::new(ai::cli::ModelDownloadStatus {
        profile: profile.clone(),
        ..Default::default()
    }));
    let cancel = Arc::new(Notify::new());
    ai::cli::spawn_model_download(
        cli_path,
        profile,
        Some(models_dir),
        progress.clone(),
        cancel.clone(),
    );
    *guard = Some(DownloadHandle { progress, cancel });
    Ok(())
}

/// The in-flight (or just-finished) download's status, or `None` if
/// nothing has ever been started this session - polled the same way the
/// frontend already polls `ai_status`.
#[tauri::command]
async fn ai_model_download_status(
    state: State<'_, AiState>,
) -> Result<Option<ai::cli::ModelDownloadStatus>, String> {
    let guard = state.download.lock().await;
    match guard.as_ref() {
        Some(handle) => {
            let p = handle
                .progress
                .lock()
                .map_err(|_| ai::codes::DOWNLOAD_FAILED.to_string())?;
            Ok(Some(p.clone()))
        }
        None => Ok(None),
    }
}

/// Kills the in-flight download, if any - a no-op if nothing is running
/// (already finished, or nothing was ever started). The Go side's own
/// `.partial` resume means a later `ai_model_download_start` for the same
/// profile picks up where this left off, not from scratch.
#[tauri::command]
async fn ai_model_download_cancel(state: State<'_, AiState>) -> Result<(), String> {
    let guard = state.download.lock().await;
    if let Some(handle) = guard.as_ref() {
        handle.cancel.notify_one();
    }
    Ok(())
}

/// Deletes a downloaded profile's file and metadata - request/response,
/// not gated by `ai::require_enabled()` for the same reason
/// `ai_model_list`/`ai_model_download_start` are not.
#[tauri::command]
async fn ai_model_remove(app: tauri::AppHandle, profile: String) -> Result<(), String> {
    let (cli_path, models_dir) = ai_model_paths(&app)?;
    let output = ai::cli::spawn_model_remove(
        &cli_path,
        &profile,
        Some(&models_dir),
        Duration::from_secs(15),
    )
    .await
    .map_err(|e| format!("{}: {e}", ai::codes::CLI_FAILED))?;
    if output.exit_code != 0 {
        return Err(format!("{}: {}", ai::codes::CLI_FAILED, output.stderr));
    }
    Ok(())
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_dialog::init())
        .manage(AgentState::default())
        .manage(AiState::default())
        .invoke_handler(tauri::generate_handler![
            launch_options,
            agent_default_endpoint,
            agent_get,
            agent_request,
            agent_cancel,
            agent_export_bundle,
            bundle_open,
            ai_status,
            ai_runtime_start,
            ai_runtime_stop,
            ai_analyze,
            ai_report_redact,
            ai_model_list,
            ai_model_download_start,
            ai_model_download_status,
            ai_model_download_cancel,
            ai_model_remove
        ])
        .run(tauri::generate_context!())
        .expect("error while running the NetRewind desktop application");
}

#[cfg(test)]
mod tests {
    use super::{
        agent::codes, cancelled_error, is_notes_path, parse_launch_options, race_cancellable,
    };
    use tokio::sync::Notify;

    #[test]
    fn parses_the_documented_options_and_ignores_the_rest() {
        let o = parse_launch_options(
            [
                "--source",
                "live",
                "--page",
                "incidents",
                "--lang",
                "en",
                "--no-wizard",
                "--unknown",
                "x",
            ]
            .into_iter()
            .map(String::from),
        );
        assert_eq!(o.source.as_deref(), Some("live"));
        assert_eq!(o.page.as_deref(), Some("incidents"));
        assert_eq!(o.lang.as_deref(), Some("en"));
        assert!(o.no_wizard);
        assert!(o.bundle.is_none());
    }

    #[test]
    fn a_bundle_path_implies_the_bundle_source() {
        let o = parse_launch_options(["--bundle", "C:/x/b.tar.gz"].into_iter().map(String::from));
        assert_eq!(o.source.as_deref(), Some("bundle"));
        assert_eq!(o.bundle.as_deref(), Some("C:/x/b.tar.gz"));
    }

    #[tokio::test]
    async fn race_cancellable_returns_the_futures_own_result_when_never_cancelled() {
        let notify = Notify::new();
        // Never notified - the future must be allowed to run to completion,
        // Some(&notify) or not.
        let got = race_cancellable(
            async { Ok::<_, String>(42) },
            Some(&notify),
            "cancelled".to_string(),
        )
        .await;
        assert_eq!(got, Ok(42));

        let got_no_notify =
            race_cancellable(async { Ok::<_, String>(7) }, None, "cancelled".to_string()).await;
        assert_eq!(got_no_notify, Ok(7));
    }

    #[tokio::test]
    async fn race_cancellable_returns_the_cancelled_error_when_notified_first() {
        let notify = Notify::new();
        notify.notify_one();
        // std::future::pending() never resolves on its own - if this test
        // returns anything at all, it can only be because the
        // cancellation path actually won the race, not because the
        // "real" work happened to finish first (which is impossible here
        // by construction, ruling out a false pass).
        let got = race_cancellable(
            std::future::pending::<Result<(), _>>(),
            Some(&notify),
            cancelled_error(),
        )
        .await;
        assert_eq!(got, Err(cancelled_error()));
        assert_eq!(got.unwrap_err().code, codes::CANCELLED);
    }

    #[test]
    fn is_notes_path_accepts_the_notes_namespace_and_its_query_strings() {
        for path in [
            "/v1/notes",
            "/v1/notes/incidents/inc-1",
            "/v1/notes/similar?rule_id=x",
            "/v1/notes?foo=bar",
        ] {
            assert!(is_notes_path(path), "{path} should be accepted");
        }
    }

    #[test]
    fn is_notes_path_rejects_the_record_and_lookalike_paths() {
        for path in [
            "/v1/events",
            "/v1/incidents",
            "/v1/health",
            "/v1/notesevil",
            "/v1/",
            "/v1",
            "",
        ] {
            assert!(!is_notes_path(path), "{path} should be rejected");
        }
    }
}
