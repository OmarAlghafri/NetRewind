# ADR 0005 — Keyset cursor pagination and fold-aware delta polling

**Status:** Proposed.
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
  not on a timer.
- Client rewrite: first load fetches the selected range; every subsequent
  poll fetches only the delta via cursor; every request carries an
  `AbortController` wired to the existing `generation` ref so a superseded
  request is actually canceled; exponential backoff with jitter on failure;
  a persistent stale-data notice instead of silently going quiet.

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

Pending - closed by the Phase 3 gate: a regression test that folds a repeat
event past the point a naive `ts_wall`-only delta would miss it, and asserts
the new delta query returns the updated row; the GUI's Investigation page
matches `netrewind what-happened` output for identical inputs; the network
log during a live session shows no full 24h/5000-row refetch after the
first load and no overlapping in-flight requests.
