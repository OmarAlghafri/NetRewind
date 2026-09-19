# ADR 0005 — Keyset cursor pagination and fold-aware delta polling

**Status:** Accepted and implemented, backend and frontend
(`internal/store/{store,sqlite,incident_sqlite}.go`,
`internal/api/v1/server.go`, `desktop/src/data/{useRecord,tauri}.ts`,
`desktop/src-tauri/src/{agent,lib}.rs`).
**Date:** 2026-09-19.

## Context

`desktop/src/data/useRecord.ts:66-86` (`fetchLive`) fetches
`/v1/health` sequentially, then `/v1/capabilities`, `/v1/events?since=<now
minus 24h>&limit=5000`, `/v1/incidents?since=...&limit=5000`, `/v1/rules` in
parallel - on *every* poll (default every `max(2, refreshSeconds)` seconds,
`useRecord.ts:154`), with no cursor, no `AbortController`, and capabilities
and rules re-fetched even though they rarely change.

`internal/api/v1/server.go`'s `parseWindow` (`:105-130`) enforces no maximum
`limit` - any positive integer is accepted - and the response wrapper types
`eventsResponse`/`incidentsResponse` were already shaped as JSON objects
rather than bare arrays specifically so a future top-level field could be
added without breaking existing decoders (`server.go:330`'s own comment).

The one real hazard in adding a naive `since=<last event's ts_wall>` delta:
folding. A repeated event (the same `dedup_key` within `FoldWindow`) updates
`ts_last` and `count` **in place** on the existing row
(`internal/store/sqlite.go:157-160`) without changing `ts_wall`. A delta
query filtered purely on `ts_wall > cursor` will never see that a folded
row's count just grew - a flapping port's fifth flap would silently vanish
from a delta-polling client's view of that row.

## Decision

- `/v1/events` and `/v1/incidents` gain, additively: `order=asc|desc`
  (existing default - oldest-first, contradicting `docs/api.md:80`'s
  "newest first" claim - is preserved when the param is omitted; the doc is
  corrected in the same commit); a keyset `cursor` over `(ts_wall,
  event_id)`; `next_cursor` and `has_more` as new top-level response fields;
  a server-side `MaxLimit` the client cannot exceed regardless of what it
  requests.
- Fold-aware delta: a poll after the first load queries
  **`ts_wall > cursor.ts_wall OR (ts_wall = cursor.ts_wall AND event_id >
  cursor.event_id) OR ts_last > cursor.polled_at`** - the third clause is
  what catches a folded row whose count grew without moving `ts_wall`. This
  is Option (a) from the execution order (`updated_since` on `ts_last`,
  unioned with the keyset cursor) chosen over re-querying the whole
  fold-window each poll (Option (b)), because it needs no knowledge of
  `FoldWindow`'s value on the client and stays correct even if that constant
  changes server-side.
- `/v1/rules` and `/v1/capabilities` gain a content `hash` (SHA-256 of the
  canonical JSON); the client refetches either only when its hash changes,
  not on a timer. **Superseded during implementation**: built as standard
  HTTP conditional GET (`ETag`/`If-None-Match`/304) instead - a real
  bandwidth/decode saving on a match, not a same-body field the client
  still has to download to read. See this ADR's own Verification section.
- Client rewrite: first load fetches the selected range; every subsequent
  poll fetches only the delta via cursor; every request carries an
  `AbortController` wired to the existing `generation` ref so a superseded
  request is actually canceled; exponential backoff with jitter on failure;
  a persistent stale-data notice instead of silently going quiet.
  **Superseded during implementation**: the live path goes through Tauri's
  `invoke()` to a Rust command, never `fetch()` - there is no in-browser
  request for a Web `AbortController` to abort. A working equivalent needed
  a real Rust-side mechanism instead: `agent_get` takes an optional
  `request_id`, registers a `tokio::sync::Notify` for it, and races the
  actual named-pipe/socket read against that notification
  (`desktop/src-tauri/src/lib.rs`'s `race_cancellable`); `agent_cancel`
  wakes the notification for a superseded poll's ids. Achieves the same
  intent (interrupting the real in-flight I/O, not just discarding the
  eventual answer) through the primitive this architecture actually has.

## Consequences

- `store.Filter` (`internal/store/store.go:20-38`) needs a cursor-aware
  query path alongside its existing `Since/Until/Limit/Descending` fields;
  this is additive to the struct, not a replacement.
- Existing CLI callers of `Store.Query` (`cmd/netrewind/timeline.go` and
  others) are unaffected - they do not pass a cursor and get today's
  behavior unchanged.
- A client written against the pre-cursor API keeps working: `next_cursor`/
  `has_more` are new fields it can ignore, and omitting `cursor`/`order`
  reproduces today's exact query.

## Verification

**Backend (done):**
- `internal/store/cursor_test.go`:
  `TestQueryCursorCatchesAFoldedRowsCountChange` is exactly the regression
  test this ADR called for - it folds a repeat event past the point a
  naive `ts_wall`-only delta would miss it (constructs the cursor from a
  poll *before* the fold, then asserts the delta poll after the fold
  returns the updated row with the grown count), plus a control case
  proving a cursor taken *after* the fold correctly sees nothing further.
  `TestQueryCursorExcludesAlreadySeenRows` and the incident equivalent
  cover the ordinary keyset-advance case.
- `internal/api/v1/cursor_test.go`: the same fold scenario end-to-end
  through the HTTP handler, cursor encoding, and JSON round-trip
  (`TestEventsCursorCatchesAFoldedRowsCountChange`), plus `order=asc|desc`,
  a malformed cursor rejected as `bad_param`, and `MaxLimit` clamping.
- `docs/api.md:80`'s "newest first" claim corrected to match the actual
  default (oldest-first) in the same change.
- `go build ./...`, `go test ./...` (all packages), `gofmt -l`, `go vet`
  for linux/windows/darwin, and `govulncheck ./...` all clean.
- `/v1/rules`/`/v1/capabilities` now support standard HTTP conditional GET
  (`ETag`/`If-None-Match`/`304 Not Modified`,
  `internal/api/v1/server.go`'s `writeJSONWithETag`) rather than a
  same-body hash field - a real bandwidth/decode saving when nothing
  changed, not merely a hint the client could act on. Tested
  (`internal/api/v1/etag_test.go`): a matching `If-None-Match` gets 304
  with an empty body; a stale one gets the real body back.

**Frontend (done):**
- `desktop/src-tauri/src/agent.rs`/`lib.rs`: `agent_get` threads request
  headers (`If-None-Match`) and returns response headers (`ETag`) neither
  direction previously existed for at all; `race_cancellable` (unit tested
  directly against a `std::future::pending()` future, ruling out a false
  pass - `desktop/src-tauri/src/lib.rs`'s test module) plus `agent_cancel`
  give real, working cancellation.
- `desktop/src/data/tauri.ts`: `agentGetRaw` (headers in, 304-aware,
  cancellation-aware) alongside the existing simple `agentGet`.
- `desktop/src/data/useRecord.ts`: `fetchLivePoll` replaces the old
  `fetchLive` internally - `since=` on the first load, `cursor=` after;
  `mergeById` updates a folded event/incident in place instead of
  duplicating or dropping it; `If-None-Match` sent for rules/capabilities
  once an ETag is known, with a 304 keeping the existing list rather than
  clearing it; a previous, now-superseded poll's five sub-requests are
  actually cancelled (`cancelPoll`) before a new one starts; exponential
  backoff (`consecutiveFailures`, a ref - a real stale-closure bug was
  found and fixed reading this from `state.status` instead, which a
  `setTimeout` closure never saw update) replaces the fixed interval once
  polls start failing.
- Proven end-to-end at the frontend level, not just assumed from the
  backend tests passing (`desktop/src/data/useRecord.cursor.test.tsx`,
  new): a fake recorder that behaves like the real API (changing
  `next_cursor` each call, a real 304 on a matching `If-None-Match`)
  confirms the first call uses `since=`, the second uses the exact
  `cursor=` value the first returned, a folded event's grown count
  replaces the old entry instead of appending a duplicate, rules/
  capabilities survive a 304 unchanged, and `agent_cancel` is actually
  invoked when settings change mid-poll.
- Regression: `npm test` 26/26 (7 existing useRecord + 3 existing
  component + 3 new cursor + 8 routing + 5 formatter... see evidence 34
  for the exact breakdown), `npx playwright test` 32/32, `cargo test --lib`
  10/10, `tsc --noEmit` clean, `cargo clippy --lib` 0 warnings.
- Not verifiable in this environment: a live session against a real
  netrewindd through the actual compiled Tauri window (this sandbox has no
  way to drive a native OS window, only browser tabs) - deferred to
  Phase 7's live-hardware testing, which already covers exactly this.

Investigation page calling `/v1/what-happened` is separate work (ADR 0007
covers the endpoint; no page consumes it yet - Phase 3/5's remaining page
rebuilds).
