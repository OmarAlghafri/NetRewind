//! The NetRewind desktop shell: a viewer for the recorder's local API and
//! for evidence bundles. It never modifies the record - every command here
//! reads.

mod agent;
mod bundle;

use serde::Serialize;

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
}

/// The endpoint the recorder listens on by default on this platform.
#[tauri::command]
fn agent_default_endpoint() -> String {
    agent::default_endpoint()
}

/// GET `path` (for example `/v1/events?limit=200`) from the recorder at
/// `endpoint` (empty means the platform default).
#[tauri::command]
async fn agent_get(endpoint: String, path: String) -> Result<AgentResponse, String> {
    if !path.starts_with("/v1/") {
        return Err("only /v1/ paths are served".to_string());
    }
    let endpoint = if endpoint.trim().is_empty() { agent::default_endpoint() } else { endpoint };
    let resp = agent::get(&endpoint, &path).await?;
    Ok(AgentResponse { status: resp.status, body: String::from_utf8_lossy(&resp.body).into_owned() })
}

#[derive(Serialize)]
struct ExportResult {
    bytes: u64,
    path: String,
}

/// Asks the recorder for an evidence bundle of the given window and writes
/// it to `dest` (a path the user chose in a save dialog).
#[tauri::command]
async fn agent_export_bundle(endpoint: String, query: String, dest: String) -> Result<ExportResult, String> {
    let endpoint = if endpoint.trim().is_empty() { agent::default_endpoint() } else { endpoint };
    let path = if query.is_empty() { "/v1/bundle".to_string() } else { format!("/v1/bundle?{query}") };
    let resp = agent::get(&endpoint, &path).await?;
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
    std::fs::rename(&tmp, &dest).map_err(|e| format!("cannot move the bundle into place at {dest}: {e}"))?;
    Ok(ExportResult { bytes: resp.body.len() as u64, path: dest })
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
        .invoke_handler(tauri::generate_handler![
            launch_options,
            agent_default_endpoint,
            agent_get,
            agent_export_bundle,
            bundle_open
        ])
        .run(tauri::generate_context!())
        .expect("error while running the NetRewind desktop application");
}

#[cfg(test)]
mod tests {
    use super::parse_launch_options;

    #[test]
    fn parses_the_documented_options_and_ignores_the_rest() {
        let o = parse_launch_options(
            ["--source", "live", "--page", "incidents", "--lang", "en", "--no-wizard", "--unknown", "x"]
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
}
