# ADR 0007 — `/v1/what-happened`: investigation parity with the CLI

**Status:** Accepted, implemented (`internal/api/v1/whathappened.go`).
**Date:** 2026-09-19.

## Context

`netrewind what-happened --host <addr> --at <time> --window <dur>`
(`cmd/netrewind/timeline.go:52-124`) answers the question this project
exists for - "what happened around this moment, to this machine" - by
expanding a host to every identity label it answered to
(`labelsToSearch`, using `store.SQLite`'s `ResolveAt`/`LabelsFor`
directly), querying each, and always folding in `system.*` events for the
window so a blind spot is never silently absent. Before this ADR, the
local API had no equivalent: a desktop GUI's Investigation page could only
approximate this with `/v1/events?subject=<label>`, which does not expand
identity at all - asking about `10.0.0.9` would miss everything recorded
while the same machine held `10.0.0.5` a minute earlier, and would not
tell the caller anything else that host had answered to.

`ResolveAt`/`LabelsFor` were `*store.SQLite`-only methods
(`internal/store/identity_sqlite.go:147,168`); `internal/api/v1.Server`
holds the `store.Store` *interface*, which did not declare them.

## Decision

- Promote `ResolveAt` and `LabelsFor` onto the `store.Store` interface
  itself. `*SQLite` is the only implementation today, so this is additive
  in practice; the one existing test double
  (`cmd/netrewindd/writer_test.go`'s `brokenStore`) gained two
  never-called stub methods to keep satisfying the interface.
- Add `GET /v1/what-happened?host=&at=&window=`, deliberately re-implementing
  `labelsToSearch`'s loop inside `internal/api/v1` rather than sharing code
  with `cmd/netrewind` - the loop itself is six lines; the real shared
  surface is the store interface promotion above, and a shared package for
  six lines would be more indirection than the duplication it removes. A
  future change to this logic must land in both places, and a mismatch
  between them is exactly what this endpoint's own parity tests
  (`internal/api/v1/whathappened_test.go`) and Phase 3's planned E2E
  scenario (comparing this endpoint's output to a real CLI invocation) are
  meant to catch - not something silently trusted to stay in sync by hand.
- Not a paginated/cursor endpoint like `/v1/events` (ADR 0005): this is a
  single bounded reconstruction around one moment, matching the CLI's own
  `Limit: 2000`-per-query, non-paginated behaviour exactly. Adding cursor
  semantics here would be scope beyond what investigation parity actually
  needs.
- Response shape: `{from, to, observed_labels[], events[], incidents[]}` -
  `observed_labels` is what the CLI prints as "also answered to: ..."
  (the host's other addresses, not the query label itself).

## Consequences

- The GUI's Investigation page (Phase 3, frontend) calls this endpoint
  instead of trying to reconstruct identity expansion client-side, which
  it has no access to at all (identity resolution happens store-side).
- `at` and `window` use RFC3339 and Go duration syntax respectively,
  matching the rest of the API's time conventions - not the CLI's more
  flexible `-2h`/`HH:MM`/`now` shorthand, which is a terminal-ergonomics
  feature an API client does not need (it can compute an absolute
  timestamp itself).

## Verification

- `internal/api/v1/whathappened_test.go`: no-host searches the whole
  window (matching the CLI's own behaviour when `--host` is omitted); a
  host is correctly expanded to an address it held earlier via a real
  `identity_binding` row (not a mocked resolver) and an unrelated host's
  events are confirmed absent; `system.gap` is confirmed always included
  regardless of the host filter; malformed `at`/`window` are rejected with
  `bad_param`.
- `go build ./...`, `go test ./...`, `gofmt -l`, and `go vet` for
  linux/windows/darwin all clean after the interface change.
- Not yet done: an actual CLI-vs-API E2E comparison (running
  `netrewind what-happened` and this endpoint against the same store and
  diffing the results) - planned for Phase 3's E2E scenario 6, once the
  GUI's own Investigation page exists to be the thing under test.
