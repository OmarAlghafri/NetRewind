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

Supports conditional GET: the response carries an `ETag`. A poll that
sends it back as `If-None-Match` gets `304 Not Modified` with no body when
nothing changed - this rarely does, unlike events, so a client polling it
on a timer should do this rather than re-fetching and re-parsing the same
list every time.

`status` is one of `up`, `down` (started and failed; `reason` is the failure
in the collector's own words, the same text as its `system.collector_down`
event), `unsupported` (this platform cannot run it) or `unknown` (registered,
not reported yet).

### `GET /v1/events`

Query parameters: `since`, `until` (RFC 3339; default window is the last
hour ending now), `limit` (positive integer, capped server-side regardless
of what is asked for), `order` (`asc` or `desc`, default `asc` - oldest
first), `kind` (exact event kind, e.g. `link.down`), `family` (kind prefix,
e.g. `l3`), `subject` (subject label, e.g. an address), `cursor` (opaque,
from a previous response's `next_cursor` - see below). Returns
`{ "events": [...], "next_cursor": "...", "has_more": bool }`, `events`
never `null`.

`cursor`, when present, replaces `since` as the lower bound: the response
contains only events after the cursor's position. Every response - even
the first, `since`-based call - carries a `next_cursor` to present on the
next poll, so a client can switch from a full window fetch to a delta poll
(only what's new) without a separate mode. A cursor also catches a
*folded* event (a repeat of the same fact within the fold window updates
its existing row's count in place, without moving its timestamp) - the
delta correctly reports the updated count on the next poll rather than
missing the update the way a plain "since the last timestamp I saw" filter
would. `has_more` is true when the response was cut off by `limit`, a hint
to page further rather than a guarantee of exactly how much more there is.

### `GET /v1/incidents`

Same window, `limit`, and `cursor` parameters (see `/v1/events` above)
plus `rule` (rule id) and `min_severity` (`info|notice|warn|error`).
Returns `{ "incidents": [...], "next_cursor": "...", "has_more": bool }`.
Always ordered oldest first; there is no `order` parameter here.

### `GET /v1/rules`

The correlation catalogue the recorder loaded: id, title, severity,
confidence, window, root cause kind and advice for every rule. Match clauses
are not exposed; they are the engine's business. Supports conditional GET
the same way `/v1/capabilities` does (see above).

### `GET /v1/bundle`

An evidence bundle (`application/gzip`, see [bundles](desktop.md#evidence-bundles))
of the window given by `since`/`until` (default: the last hour). DNS names
are redacted unless `include_secrets=true`. Still a read: nothing is written
anywhere by serving it. The `limit` parameter caps events and incidents;
without it the bundle's own, much larger default applies and the manifest's
`truncated` flag says whether it was hit.

### `GET /v1/what-happened`

Answers exactly what `netrewind what-happened --host <addr> --at <time>
--window <dur>` answers from a terminal - reconstructing what happened
around one moment, for one machine or for all of them - over the API
instead of reading the store file directly.

Query parameters: `host` (an address, MAC, or hostname; omitted searches
every subject in the window, matching the CLI's own behaviour with no
`--host`), `at` (RFC 3339, default now), `window` (a duration, e.g. `5m`,
default `5m` - how far either side of `at` to look). Unlike `/v1/events`
this is a single bounded reconstruction, not a paginated feed: there is no
`limit` or `cursor` parameter here.

A `host` is expanded to every identity label it answered to during the
window - asking about an address a machine picked up five minutes ago
still finds what was recorded while it held its previous one. `system.*`
events inside the window are always included regardless of the host
filter, so a recording gap is never silently indistinguishable from
nothing happening.

Returns:

```json
{
  "from": "2026-09-19T10:15:00Z",
  "to": "2026-09-19T10:25:00Z",
  "observed_labels": ["10.0.0.5"],
  "events": [ ... ],
  "incidents": [ ... ]
}
```

`observed_labels` lists the host's *other* addresses (what the CLI prints
as "also answered to: ..."), not the one given in the query.

## Using it from a shell

```bash
netrewind status                      # health + capabilities, formatted
netrewind status -o json              # the raw documents
netrewind status --endpoint /tmp/x.sock
```

Any HTTP client that can dial a Unix socket works on Linux, for example
`curl --unix-socket /run/netrewind/api.sock http://netrewind/v1/health`.
