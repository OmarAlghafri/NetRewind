# ADR 0008 — Operator notes: a second store beside the record, not in it

**Status:** Accepted, implemented (`internal/notes`, `internal/api/v1/notes.go`).
**Date:** 2026-09-20.

## Context

The 1.2.0 local-AI assistant needs memory: an operator's own conclusion on
an incident (confirmed cause / false positive / unresolved, plus free
text), automatic retrieval of similar past incidents, local-only
helpful/not-helpful feedback on a model answer, and (opt-in only)
persisted follow-up-question threads.

The recorder's own record (`events.db`) is read-only over the local API by
construction - `internal/api/v1/server_test.go`'s `TestWriteMethodsAreNotRouted`
pins that every route on it is `GET`, and that guarantee is a real part of
this project's threat model (a compromised or careless local client cannot
alter recorded evidence). Operator notes are not evidence: they are the
operator's own annotation, they are never treated as if the deterministic
Isnad engine produced them, and they must never contaminate a bundle's
evidentiary content. Writing them into `events.db`, or exposing them
through the record's existing read-only routes, would blur exactly that
line. The owner's own decision (recorded in the 1.2.0 plan) was explicit:
memory lives inside the recorder, in a *separate* database, behind a
deliberately small write surface - accepting that this means the record's
"the API is read-only" rule now has one narrow, clearly-scoped exception
rather than remaining absolute.

## Decision

- **A second SQLite file, `notes.db`,** opened next to `events.db`
  (`store.DataDir()`), by the daemon only, with its own migration/version
  scheme (`internal/notes/sqlite.go`) - never the same connection, schema,
  or Go type as `store.Store`. `internal/notes.Store` is a distinct
  interface; nothing that implements it can also satisfy `store.Store`,
  and no handler in `internal/api/v1/notes.go` is ever given an
  `s.Store` (`store.Store`) reference - only `s.Notes` (`notes.Store`).
  `TestNotesHandlersNeverTouchTheRecord` (`internal/api/v1/notes_test.go`)
  pins this at the handler level: every notes route is exercised against a
  `Server` whose `Store` field panics on first use, so a future change that
  accidentally reached the record fails loudly in CI, not silently in
  production.
- **A small, explicit write surface**, `/v1/notes/*`, registered only when
  the daemon's `notes: enabled` config (on by default) is true and a
  `notes.Store` is actually configured
  (`TestNotesRoutesAreAbsentWhenNotesIsNil`): `GET/PUT/DELETE
  /v1/notes/incidents/{id}` (one annotation per incident), `GET
  /v1/notes/similar` (ranked prior annotated incidents for the same rule
  and root cause), `POST /v1/notes/feedback` (helpful/not-helpful, capped
  at 500 entries, oldest dropped), `GET/POST /v1/notes/threads/{id}`
  (opt-in follow-up history, capped at 20 turns per incident, refuses with
  `409 history_disabled` when the per-installation opt-in or the
  operator's own `notes: threads: false` policy says no), `GET/PUT
  /v1/notes/settings` (the opt-in itself - turning it off deletes every
  stored thread immediately, "off" means forgotten), `GET /v1/notes/stats`
  (counts and an on-disk byte size for Diagnostics - deliberately nothing
  more specific, never a path, which on Windows carries a username), and
  `DELETE /v1/notes` (forget everything). Every write is bounded (64 KiB
  body, 2000 chars per free-text field) - this is a place for a few
  sentences of operator judgement, not an unbounded local key-value store.
- **Identity by fingerprint, not by incident ID.** An incident's own ID is
  a fresh ULID minted on every firing (`internal/correlate/engine.go`'s
  `build()`), so "the same conclusion, on a re-fired incident, across a
  daemon restart" cannot be keyed on it. `ai.Fingerprint(rule_id,
  root_cause_kind, root_cause_entity)` (sha256 of the three) is what
  `Similar` actually matches on; `incident_id` is kept alongside it only as
  "the exact row a specific analysis session was looking at."
- **A note is never evidence and never merged from a bundle.** A local-AI
  answer's own validation (`internal/ai/validate.go`) never reads
  `notes.Store` at all - offered annotations reach the model only as
  explicitly-labelled `A1..` handles the caller chose to include, marked
  "untrusted" in the prompt. Importing a foreign bundle must not let that
  bundle's author inject a false "confirmed cause" into a local
  operator's own record of their own network - so import never writes
  `notes.db` (unimplemented as of this ADR: exporting a bundle's own
  in-window notes as a read-only `notes.json` member, so a *recipient* can
  at least see what the *sender* had annotated, remains open - see
  Consequences).

## Consequences

- The record's own "every route is GET" guarantee (ADR 0005's pagination
  work, `TestWriteMethodsAreNotRouted`) is now scoped to `store.Store`'s
  routes specifically, not literally every route this API serves -
  `TestWriteMethodsAreNotRouted` was narrowed accordingly, not deleted, so
  it keeps meaning exactly what it says about the record.
- A local process running as the allowed user can now write four kinds of
  small, clearly-labelled local records it previously could not write at
  all. `docs/product/threat-model.md` needs (and does not yet have,
  tracked separately) the corresponding delta: this is a strictly smaller
  capability than "can modify the record," but it is not nothing, and the
  threat model should say so explicitly rather than by omission.
- **Known gap, not yet implemented:** the 1.2.0 plan's bundle-export member
  (`notes.json`, read-only, labelled "from this bundle" on import) does not
  exist yet - `internal/bundle` has no notes-aware export path today. A
  recipient of an exported evidence bundle sees only the deterministic
  record; the sender's own operator notes stay local until this lands.
- `netrewind note <incident-id> --outcome ... --cause ...` and `netrewind
  notes --rule --kind --entity` (`cmd/netrewind/note.go`) are the CLI's own
  client of this same write surface - like the desktop panel, over the API,
  never a direct `notes.db` file open.

## Verification

- `internal/notes/sqlite_test.go`: migration/version handling, annotation
  round-trip, `Similar`'s ranking (same rule+kind+entity before same
  rule+kind, newest first within each tier), feedback cap (oldest dropped
  past 500), thread cap (oldest dropped past 20), `SetHistoryOptIn(false)`
  deleting every stored thread in the same call, `Stats` reflecting real
  writes.
- `internal/api/v1/notes_test.go`: routes absent when `Notes` is nil;
  `TestNotesHandlersNeverTouchTheRecord`'s full read/write/delete cycle
  against a store that panics on any `store.Store` method call; invalid
  outcome and oversized-body rejection; 404 for an unannotated incident;
  thread-append refusal both from the per-installation opt-in and from the
  operator's own policy override; `GET /v1/notes/stats` reflecting real
  counts, not a hard-coded value.
- `go build ./... && go vet ./... && CGO_ENABLED=0 go test ./... && gofmt -l cmd internal ai` clean.
