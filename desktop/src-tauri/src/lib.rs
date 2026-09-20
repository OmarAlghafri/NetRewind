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

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_dialog::init())
        .manage(AgentState::default())
        .invoke_handler(tauri::generate_handler![
            launch_options,
            agent_default_endpoint,
            agent_get,
            agent_cancel,
            agent_export_bundle,
            bundle_open
        ])
        .run(tauri::generate_context!())
        .expect("error while running the NetRewind desktop application");
}

#[cfg(test)]
mod tests {
    use super::{agent::codes, cancelled_error, parse_launch_options, race_cancellable};
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
}
