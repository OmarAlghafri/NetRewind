//! HTTP over the recorder's local-only transport.
//!
//! `netrewindd` serves its versioned API (`/v1/...`) on a Unix domain socket
//! (Linux) or a named pipe (Windows) - never on a TCP port. This module
//! speaks HTTP/1.1 over that stream, one request per connection, which is
//! all a viewer polling a local daemon needs.

use bytes::Bytes;
use http_body_util::{BodyExt, Empty};
use hyper::Request;
use hyper_util::rt::TokioIo;
use std::time::Duration;

/// How long one request may take end to end: connect, send, read. A local
/// daemon answers in milliseconds; anything longer is a recorder that is
/// wedged, and the UI should say so rather than hang.
const REQUEST_TIMEOUT: Duration = Duration::from_secs(10);

/// The platform's default endpoint, matching `internal/ipc.DefaultPath`.
pub fn default_endpoint() -> String {
    #[cfg(windows)]
    {
        r"\\.\pipe\netrewind-api".to_string()
    }
    #[cfg(not(windows))]
    {
        "/run/netrewind/api.sock".to_string()
    }
}

/// One answer from the recorder.
pub struct Response {
    pub status: u16,
    pub body: Vec<u8>,
    /// Header names are lower-cased (HTTP header names are case-insensitive;
    /// a caller looking for "etag" should not have to also check "ETag").
    /// Only ever populated from what the recorder itself sent, never
    /// invented here.
    pub headers: Vec<(String, String)>,
}

/// Performs `GET path` (path includes any query string) against the API at
/// `endpoint`, with `extra_headers` added to the request (empty is fine -
/// this is how a client sends `If-None-Match` for a conditional GET). A
/// transport failure (no such pipe or socket, access denied) is an `Err`;
/// an HTTP error status is an `Ok` with that status, since the body then
/// carries the API's own error shape.
pub async fn get(endpoint: &str, path: &str, extra_headers: &[(String, String)]) -> Result<Response, String> {
    tokio::time::timeout(REQUEST_TIMEOUT, get_inner(endpoint, path, extra_headers))
        .await
        .map_err(|_| format!("the recorder at {endpoint} did not answer within {}s", REQUEST_TIMEOUT.as_secs()))?
}

async fn get_inner(endpoint: &str, path: &str, extra_headers: &[(String, String)]) -> Result<Response, String> {
    let stream = connect(endpoint).await?;
    let (mut sender, conn) = hyper::client::conn::http1::handshake(TokioIo::new(stream))
        .await
        .map_err(|e| format!("HTTP handshake with the recorder failed: {e}"))?;
    tokio::spawn(async move {
        // The connection task ends when the response has been read; its
        // outcome is reflected in the response future below.
        let _ = conn.await;
    });
    let mut builder = Request::builder()
        .method("GET")
        .uri(path)
        .header("Host", "netrewind")
        .header("Accept", "application/json, application/gzip");
    for (name, value) in extra_headers {
        builder = builder.header(name.as_str(), value.as_str());
    }
    let req = builder
        .body(Empty::<Bytes>::new())
        .map_err(|e| format!("could not build the request: {e}"))?;
    let resp = sender
        .send_request(req)
        .await
        .map_err(|e| format!("request to the recorder failed: {e}"))?;
    let status = resp.status().as_u16();
    let headers = resp
        .headers()
        .iter()
        .filter_map(|(name, value)| value.to_str().ok().map(|v| (name.as_str().to_ascii_lowercase(), v.to_string())))
        .collect();
    let body = resp
        .into_body()
        .collect()
        .await
        .map_err(|e| format!("reading the recorder's answer failed: {e}"))?
        .to_bytes()
        .to_vec();
    Ok(Response { status, body, headers })
}

#[cfg(windows)]
async fn connect(endpoint: &str) -> Result<impl tokio::io::AsyncRead + tokio::io::AsyncWrite + Unpin, String> {
    use tokio::net::windows::named_pipe::ClientOptions;
    const ERROR_PIPE_BUSY: i32 = 231;
    const ERROR_FILE_NOT_FOUND: i32 = 2;
    const ERROR_ACCESS_DENIED: i32 = 5;
    // A busy pipe means every server instance is mid-connection; it frees
    // within milliseconds. Not-found and access-denied are final answers.
    for attempt in 0..20 {
        match ClientOptions::new().open(endpoint) {
            Ok(pipe) => return Ok(pipe),
            Err(e) if e.raw_os_error() == Some(ERROR_PIPE_BUSY) && attempt < 19 => {
                tokio::time::sleep(Duration::from_millis(50)).await;
            }
            Err(e) if e.raw_os_error() == Some(ERROR_FILE_NOT_FOUND) => {
                return Err(format!(
                    "no recorder is listening at {endpoint} (is the netrewindd service running?)"
                ));
            }
            Err(e) if e.raw_os_error() == Some(ERROR_ACCESS_DENIED) => {
                return Err(format!(
                    "the recorder at {endpoint} refused this user: add this account to api.allow_users in netrewindd.yaml"
                ));
            }
            Err(e) => return Err(format!("could not open {endpoint}: {e}")),
        }
    }
    Err(format!("the recorder at {endpoint} stayed busy"))
}

#[cfg(not(windows))]
async fn connect(endpoint: &str) -> Result<impl tokio::io::AsyncRead + tokio::io::AsyncWrite + Unpin, String> {
    use tokio::net::UnixStream;
    UnixStream::connect(endpoint).await.map_err(|e| match e.kind() {
        std::io::ErrorKind::NotFound => format!(
            "no recorder is listening at {endpoint} (is the netrewindd service running?)"
        ),
        std::io::ErrorKind::PermissionDenied => format!(
            "the recorder at {endpoint} refused this user: add this user to the group named by api.group in netrewindd.yaml"
        ),
        _ => format!("could not open {endpoint}: {e}"),
    })
}
