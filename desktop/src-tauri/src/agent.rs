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
use serde::Serialize;
use std::collections::HashMap;
use std::time::Duration;

/// How long one request may take end to end: connect, send, read. A local
/// daemon answers in milliseconds; anything longer is a recorder that is
/// wedged, and the UI should say so rather than hang.
const REQUEST_TIMEOUT: Duration = Duration::from_secs(10);

/// A connection/transport failure, structured so the GUI can show a
/// translated sentence instead of this crate's own English wording, with
/// `technical_detail` (this crate's original sentence, including whatever
/// the OS or hyper said) available for a reader who wants it.
///
/// ADR 0004 / execution order §4.5: "Rust connection errors ... become
/// {code, params, technical_detail}; the GUI shows a translated message
/// with the raw detail available on demand." `code` is one of a small,
/// closed set (see desktop/src/i18n/agentErrorCatalogue.ts, which every
/// code here must have an entry in - checked by that file's own test); an
/// older frontend build that predates a given code, or a params key it
/// does not recognise, is expected to fall back to `technical_detail`
/// rather than fail to render at all.
#[derive(Serialize, Debug, Clone, PartialEq, Eq)]
pub struct AgentError {
    pub code: String,
    pub params: HashMap<String, String>,
    pub technical_detail: String,
}

impl AgentError {
    pub fn new(code: &str, params: &[(&str, &str)], technical_detail: impl Into<String>) -> Self {
        AgentError {
            code: code.to_string(),
            params: params
                .iter()
                .map(|(k, v)| (k.to_string(), v.to_string()))
                .collect(),
            technical_detail: technical_detail.into(),
        }
    }
}

/// Every `AgentError.code` this build can produce, named rather than typed
/// as a string literal at each call site - a typo in a raw literal
/// ("hadshake_failed") would silently produce an untranslatable code no
/// test could catch without either this or a generated list; a typo in a
/// constant name is a compile error instead. `lib.rs` uses `INVALID_PATH`
/// and `CANCELLED` from here too, so this is the one place both files'
/// codes are declared.
///
/// desktop/src/i18n/agentErrorCatalogue.ts must have a template for every
/// one of these - `codes::all_codes_have_no_duplicates` here proves this
/// list is internally consistent (compiled, deduplicated); that TS file's
/// own test proves its catalogue matches this same list, hand-kept in
/// sync on both sides (documented there, not a generated cross-language
/// bridge - proportionate to twelve stable, rarely-changing strings).
///
/// `#[allow(dead_code)]`: `ACCESS_DENIED_WINDOWS`/`ACCESS_DENIED_UNIX` are
/// each used by only one platform's `connect()` (`#[cfg(windows)]` /
/// `#[cfg(not(windows))]`), so a single-platform build always sees exactly
/// one of the two as unused; `ALL` is read only from this module's own
/// `#[cfg(test)]` tests and from `lib.rs`'s tests, so a plain `cargo
/// build` (as opposed to `cargo test`) sees it as unused too. Both are
/// real uses on the platform/build that exercises them, not actual dead
/// code.
#[allow(dead_code)]
pub mod codes {
    pub const TIMEOUT: &str = "timeout";
    pub const HANDSHAKE_FAILED: &str = "handshake_failed";
    pub const REQUEST_BUILD_FAILED: &str = "request_build_failed";
    pub const REQUEST_FAILED: &str = "request_failed";
    pub const RESPONSE_READ_FAILED: &str = "response_read_failed";
    pub const NOT_LISTENING: &str = "not_listening";
    pub const ACCESS_DENIED_WINDOWS: &str = "access_denied_windows";
    pub const ACCESS_DENIED_UNIX: &str = "access_denied_unix";
    pub const OPEN_FAILED: &str = "open_failed";
    pub const PIPE_BUSY: &str = "pipe_busy";
    pub const INVALID_PATH: &str = "invalid_path";
    pub const CANCELLED: &str = "cancelled";

    pub const ALL: &[&str] = &[
        TIMEOUT,
        HANDSHAKE_FAILED,
        REQUEST_BUILD_FAILED,
        REQUEST_FAILED,
        RESPONSE_READ_FAILED,
        NOT_LISTENING,
        ACCESS_DENIED_WINDOWS,
        ACCESS_DENIED_UNIX,
        OPEN_FAILED,
        PIPE_BUSY,
        INVALID_PATH,
        CANCELLED,
    ];
}

/// Lets `?` still work at a call site that has not adopted the structured
/// shape (`agent_export_bundle`'s own `Result<_, String>`) - falls back to
/// exactly the sentence this crate always showed.
impl From<AgentError> for String {
    fn from(e: AgentError) -> String {
        e.technical_detail
    }
}

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
pub async fn get(
    endpoint: &str,
    path: &str,
    extra_headers: &[(String, String)],
) -> Result<Response, AgentError> {
    tokio::time::timeout(REQUEST_TIMEOUT, get_inner(endpoint, path, extra_headers))
        .await
        .map_err(|_| {
            AgentError::new(
                codes::TIMEOUT,
                &[
                    ("endpoint", endpoint),
                    ("seconds", &REQUEST_TIMEOUT.as_secs().to_string()),
                ],
                format!(
                    "the recorder at {endpoint} did not answer within {}s",
                    REQUEST_TIMEOUT.as_secs()
                ),
            )
        })?
}

async fn get_inner(
    endpoint: &str,
    path: &str,
    extra_headers: &[(String, String)],
) -> Result<Response, AgentError> {
    let stream = connect(endpoint).await?;
    let (mut sender, conn) = hyper::client::conn::http1::handshake(TokioIo::new(stream))
        .await
        .map_err(|e| {
            AgentError::new(
                codes::HANDSHAKE_FAILED,
                &[],
                format!("HTTP handshake with the recorder failed: {e}"),
            )
        })?;
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
    let req = builder.body(Empty::<Bytes>::new()).map_err(|e| {
        AgentError::new(
            codes::REQUEST_BUILD_FAILED,
            &[],
            format!("could not build the request: {e}"),
        )
    })?;
    let resp = sender.send_request(req).await.map_err(|e| {
        AgentError::new(
            codes::REQUEST_FAILED,
            &[],
            format!("request to the recorder failed: {e}"),
        )
    })?;
    let status = resp.status().as_u16();
    let headers = resp
        .headers()
        .iter()
        .filter_map(|(name, value)| {
            value
                .to_str()
                .ok()
                .map(|v| (name.as_str().to_ascii_lowercase(), v.to_string()))
        })
        .collect();
    let body = resp
        .into_body()
        .collect()
        .await
        .map_err(|e| {
            AgentError::new(
                codes::RESPONSE_READ_FAILED,
                &[],
                format!("reading the recorder's answer failed: {e}"),
            )
        })?
        .to_bytes()
        .to_vec();
    Ok(Response {
        status,
        body,
        headers,
    })
}

#[cfg(windows)]
async fn connect(
    endpoint: &str,
) -> Result<impl tokio::io::AsyncRead + tokio::io::AsyncWrite + Unpin, AgentError> {
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
                return Err(AgentError::new(
                    codes::NOT_LISTENING,
                    &[("endpoint", endpoint)],
                    format!("no recorder is listening at {endpoint} (is the netrewindd service running?)"),
                ));
            }
            Err(e) if e.raw_os_error() == Some(ERROR_ACCESS_DENIED) => {
                return Err(AgentError::new(
                    codes::ACCESS_DENIED_WINDOWS,
                    &[("endpoint", endpoint)],
                    format!(
                        "the recorder at {endpoint} refused this user: add this account to api.allow_users in netrewindd.yaml"
                    ),
                ));
            }
            Err(e) => {
                return Err(AgentError::new(
                    codes::OPEN_FAILED,
                    &[("endpoint", endpoint)],
                    format!("could not open {endpoint}: {e}"),
                ));
            }
        }
    }
    Err(AgentError::new(
        codes::PIPE_BUSY,
        &[("endpoint", endpoint)],
        format!("the recorder at {endpoint} stayed busy"),
    ))
}

#[cfg(not(windows))]
async fn connect(
    endpoint: &str,
) -> Result<impl tokio::io::AsyncRead + tokio::io::AsyncWrite + Unpin, AgentError> {
    use tokio::net::UnixStream;
    UnixStream::connect(endpoint).await.map_err(|e| match e.kind() {
        std::io::ErrorKind::NotFound => AgentError::new(
            codes::NOT_LISTENING,
            &[("endpoint", endpoint)],
            format!("no recorder is listening at {endpoint} (is the netrewindd service running?)"),
        ),
        std::io::ErrorKind::PermissionDenied => AgentError::new(
            codes::ACCESS_DENIED_UNIX,
            &[("endpoint", endpoint)],
            format!(
                "the recorder at {endpoint} refused this user: add this user to the group named by api.group in netrewindd.yaml"
            ),
        ),
        _ => AgentError::new(codes::OPEN_FAILED, &[("endpoint", endpoint)], format!("could not open {endpoint}: {e}")),
    })
}

#[cfg(test)]
mod tests {
    use super::codes;
    use std::collections::HashSet;

    /// `desktop/src/i18n/agentErrorCatalogue.test.ts` hand-keeps a matching
    /// literal list and checks its catalogue against it - this is the
    /// other half: that `codes::ALL` itself has no duplicate and no typo'd
    /// entry, so what the TS side is being kept in sync with is correct in
    /// the first place. Neither test can see the other language's source,
    /// so both check what they can: internal consistency on each side, by
    /// hand kept matching across the boundary (documented on `codes`
    /// itself and in that TS test).
    #[test]
    fn all_codes_have_no_duplicates_and_match_the_expected_count() {
        let unique: HashSet<&str> = codes::ALL.iter().copied().collect();
        assert_eq!(
            unique.len(),
            codes::ALL.len(),
            "codes::ALL has a duplicate entry: {:?}",
            codes::ALL
        );
        // A change to this number is exactly the signal to go update
        // desktop/src/i18n/agentErrorCatalogue.ts's EXPECTED_CODES list too.
        assert_eq!(
            codes::ALL.len(),
            12,
            "a code was added or removed - update agentErrorCatalogue.test.ts too"
        );
    }

    #[test]
    fn every_code_is_a_non_empty_snake_case_identifier() {
        for code in codes::ALL {
            assert!(!code.is_empty());
            assert!(
                code.chars().all(|c| c.is_ascii_lowercase() || c == '_'),
                "{code:?} is not snake_case - it would not match a hand-typed catalogue key on the TS side"
            );
        }
    }
}
