# The local API

`netrewindd` serves a versioned JSON API to processes on the same machine. It
is what the desktop application and `netrewind status` read. It is never
reachable from the network: the transport is a Unix domain socket on Linux
and a named pipe on Windows, and there is no option to bind a TCP port.

Every route on the record itself is `GET`: the event store cannot be
changed through this interface (see [Requests](#requests)). The one
exception is `/v1/notes/*` (see [Notes](#notes-writable)), a completely
separate database for the operator's own annotations - never the record,
never evidence.

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

Plain HTTP/1.1 over the stream. Every route on the record is `GET`; any other
method there is a 405 (`/v1/notes/*` registers `PUT`/`POST`/`DELETE` routes
of its own - see below). The `Host` header is ignored. Times are RFC 3339;
event timestamps in bodies are nanoseconds since the epoch, as in
[the schema](schema.md).

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
    "coverage": ["flow.*"], "status": "unsupported", "reason": "requires linux",
    "reason_code": "requires_platform", "reason_params": {"platform": "linux"}, ... }
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

`reason_code`/`reason_params`, when present, are a structured form of
`reason` a client can translate (see
[rules.md](rules.md#an-arabic-translation-if-you-have-one) for the same
idea applied to rule text) - `reason` itself is always present alongside
them, unchanged, for a client that has no translation for the code or
predates this field.

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
confidence, window, root cause kind and advice for every rule, plus `i18n`
(a translation of title/advice/each clause's `why`, keyed by language code -
see [rules.md](rules.md#an-arabic-translation-if-you-have-one)) when the
rule file has one. Match clauses are not exposed; they are the engine's
business. Supports conditional GET
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

## Notes (writable)

`internal/notes` (see ADR 0008) is a second, completely separate database
next to the event store: the operator's own conclusions about an incident,
feedback on a local-AI answer, and (opt-in only) follow-up-question
history. It is never merged into the record, never treated as evidence, and
never read by anything that also touches `events.db`. `netrewind note` and
`netrewind notes` use it (see below) instead of any direct file access.

These routes exist only when the recorder's configuration has `notes.enabled`
(on by default - see [releasing.md](releasing.md) or the daemon's own
`--help`); when it is off, every path under `/v1/notes` is unregistered and
answers 404, the same as any other route this build does not have.

Body limit: 64 KiB. Malformed or oversized JSON is a 400. Errors use the
same `{ "error": { "code", "message" } }` shape as the rest of the API.

### `GET/PUT/DELETE /v1/notes/incidents/{id}`

`GET` returns the stored annotation or 404 if there is none. `PUT` creates
or replaces it:

```json
{ "rule_id": "gateway-hijack", "root_cause_kind": "l2.arp_binding_changed",
  "root_cause_entity": "10.99.0.1", "opened_at_ns": 1758270000000000000,
  "outcome": "confirmed", "cause_note": "…", "resolution_note": "…" }
```

`outcome` must be `confirmed`, `false_positive`, or `unresolved`. `DELETE`
removes it; both return `204` except `PUT`, which returns the stored
annotation as confirmation.

### `GET /v1/notes/similar`

Query parameters `rule_id` and `root_cause_kind` (both required), `entity`,
`exclude` (an incident id to leave out), `limit` (default 3). Returns prior
annotated incidents for the same rule and root cause - same rule+kind+entity
first, then same rule+kind, newest first within each tier - as
`{ "incident_id", "outcome", "cause_note", ... }` (the same annotation shape
`GET /v1/notes/incidents/{id}` returns, in an array). Never `null`.

### `POST /v1/notes/feedback`

Local-only helpful/not-helpful signal on a local-AI answer:

```json
{ "answer_id": "…", "incident_id": "…", "profile": "balanced",
  "model_id": "…", "helpful": true, "rule_id": "…",
  "root_cause_kind": "…", "root_cause_entity": "…" }
```

Returns `204`. The oldest entry is dropped once more than 500 are stored;
this is never sent anywhere.

### `GET/POST /v1/notes/threads/{id}`

Opt-in follow-up-question history for one incident. `GET` returns the
stored turns (never `null`). `POST` appends one:

```json
{ "answer_id": "…", "question_redacted": "…", "summary_redacted": "…" }
```

Refuses with `409 history_disabled` when history is not opted into - either
the per-installation setting (`GET/PUT /v1/notes/settings`, below) or the
operator's own `notes.threads: false` in the daemon's configuration, which
overrides the per-installation setting regardless of what it says. At most
20 turns are kept per incident; the oldest is dropped once exceeded.

### `GET/PUT /v1/notes/settings`

The per-installation history opt-in: `{ "history_opt_in": true|false }`.
Turning it off deletes every stored thread immediately as part of the same
request - off means forgotten, not merely "stop adding more".

### `GET /v1/notes/stats`

Counts for the desktop's Diagnostics page: `{ "annotations", "feedback",
"threads", "bytes" }` (`threads` counts incidents with at least one stored
turn; `bytes` is the notes database's on-disk size). Deliberately nothing
more specific than that - never a path, which on this OS carries a Windows
username.

### `DELETE /v1/notes`

Forgets everything: every annotation, every feedback entry, every thread.
Does not change the history opt-in setting itself. Returns `204`.

## Using it from a shell

```bash
netrewind status                      # health + capabilities, formatted
netrewind status -o json              # the raw documents
netrewind status --endpoint /tmp/x.sock

netrewind note 01J8ZQXK7X8VN5T4R6E9W1C2D3 --outcome confirmed --cause "bad switch port"
netrewind notes --rule gateway-hijack --kind l2.arp_binding_changed --entity 10.99.0.1
```

Any HTTP client that can dial a Unix socket works on Linux, for example
`curl --unix-socket /run/netrewind/api.sock http://netrewind/v1/health`.
