# The local API

`netrewindd` serves a read-only, versioned JSON API to processes on the same
machine. It is what the desktop application and `netrewind status` read. It
is never reachable from the network: the transport is a Unix domain socket on
Linux and a named pipe on Windows, and there is no option to bind a TCP port.

## Endpoint

| Platform | Default | Configured by |
|---|---|---|
| Linux | `/run/netrewind/api.sock` (mode `0660`, group `netrewind`) | `api.path`, `api.group` |
| Windows | `\\.\pipe\netrewind-api` (DACL: the service account plus `api.allow_users`) | `api.path`, `api.allow_users` |

A process that is not the recorder's user and not in the group (Linux) or
the allow list (Windows) gets a permission error at connect time. Nothing is
checked inside the API itself: reaching the socket is the authorisation.

Turn it off with `api.enabled: false` or `--api off`; the recorder then
records and answers nobody. If the API cannot be served (for example the
configured group does not exist) the recorder logs the reason and keeps
recording without it.

## Requests

Plain HTTP/1.1 over the stream. Every route is `GET`; anything else is a 405.
The `Host` header is ignored. Times are RFC 3339; event timestamps in bodies
are nanoseconds since the epoch, as in [the schema](schema.md).

Errors share one shape:

```json
{ "error": { "code": "bad_param", "message": "since: must be RFC3339" } }
```

### `GET /v1/health`

```json
{
  "status": "ok",
  "version": "1.0.0",
  "schema_version": 1,
  "api_version": 1,
  "observer_id": "workstation",
  "started_at": "2026-09-14T06:53:49Z",
  "uptime_seconds": 412,
  "store": { "path": "/var/lib/netrewind/events.db", "events": 1832 },
  "collectors": { "up": 4, "down": 0, "unsupported": 8 }
}
```

`status` is `ok` whenever the API answers; a store that cannot be counted is
reported in `store.error`, so a reachable but unhealthy recorder is
distinguishable from an absent one.

### `GET /v1/capabilities`

The recorder's own capability report: every collector this build knows
about, whether it is watching, and — when it is not — why.

```json
{ "capabilities": [
  { "name": "iphelper.link", "platform": "windows", "privilege": "none",
    "coverage": ["link.*"], "status": "up",
    "last_change": "2026-09-14T06:53:49Z", "last_seen": "2026-09-14T06:53:49Z" },
  { "name": "ebpf.flow", "platform": "linux", "privilege": "CAP_BPF (or root) + kernel BTF",
    "coverage": ["flow.*"], "status": "unsupported", "reason": "requires linux", ... }
] }
```

`status` is one of `up`, `down` (started and failed; `reason` is the failure
in the collector's own words, the same text as its `system.collector_down`
event), `unsupported` (this platform cannot run it) or `unknown` (registered,
not reported yet).

### `GET /v1/events`

Query parameters: `since`, `until` (RFC 3339; default window is the last
hour ending now), `limit` (positive integer), `kind` (exact event kind, e.g.
`link.down`), `family` (kind prefix, e.g. `l3`), `subject` (subject label,
e.g. an address). Returns `{ "events": [...] }`, newest first, never `null`.

### `GET /v1/incidents`

Same window parameters plus `rule` (rule id) and `min_severity`
(`info|notice|warn|error`). Returns `{ "incidents": [...] }`.

### `GET /v1/rules`

The correlation catalogue the recorder loaded: id, title, severity,
confidence, window, root cause kind and advice for every rule. Match clauses
are not exposed; they are the engine's business.

### `GET /v1/bundle`

An evidence bundle (`application/gzip`, see [bundles](desktop.md#evidence-bundles))
of the window given by `since`/`until` (default: the last hour). DNS names
are redacted unless `include_secrets=true`. Still a read: nothing is written
anywhere by serving it. The `limit` parameter caps events and incidents;
without it the bundle's own, much larger default applies and the manifest's
`truncated` flag says whether it was hit.

## Using it from a shell

```bash
netrewind status                      # health + capabilities, formatted
netrewind status -o json              # the raw documents
netrewind status --endpoint /tmp/x.sock
```

Any HTTP client that can dial a Unix socket works on Linux, for example
`curl --unix-socket /run/netrewind/api.sock http://netrewind/v1/health`.
