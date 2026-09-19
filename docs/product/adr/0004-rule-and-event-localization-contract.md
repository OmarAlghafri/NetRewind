# ADR 0004 — Localization contract for rule and event narrative text

**Status:** Proposed.
**Date:** 2026-09-19.

## Context

The Arabic UI translates chrome only. Every semantic sentence an operator
actually reads while investigating - `incident.title`, `link.why`,
`incident.advice` (all copied verbatim from `rules/*.yaml` in
`internal/correlate/engine.go:636,640,657-660`), the `event.Describe`
fallback templates (`internal/event/describe.go:17-258`), and capability
`reason` strings (`cmd/netrewindd/main.go:235`) - is English-only, with no
localization mechanism in Go at all. `desktop/src/components/IncidentCard.tsx`
renders `incident.title`/`link.why`/`incident.advice` directly; `Rules.tsx`
renders `r.title` directly; `CapabilityTable.tsx` wraps `c.reason` in
`ltr-field`, which forces monospace/LTR onto what is usually an English
sentence, not a technical token.

The rule loader (`internal/correlate/rule.go:246`,
`yaml.Decoder.KnownFields(true)`) rejects any YAML key the `Rule`/`Clause`
structs do not declare - confirmed by reading `rule.go:31-86` and
`TestAnUnknownKeyInARuleIsRefused` (`internal/correlate/rule_test.go:15-39`).
A localization field cannot be added to the YAML files before it exists on
the Go struct, or every one of the 19 shipped rules fails to load.

## Decision

- Add an optional `i18n` block to `Rule` and `Clause`:
  `i18n.ar.title`, `i18n.ar.advice` on `Rule`; `i18n.ar.clauses.<as>` (a map
  keyed by the clause's `as` name) on the rule for per-clause `why` text.
  Absent block -> English fallback, never an error.
- Add a `Clause string` field to `incident.Link`
  (`internal/incident/incident.go:41-52`, currently `Seq, EventID, Kind, At,
  Subject, Relation, Why, Evidence` with no clause identifier) carrying the
  matched clause's `as`, so the GUI can look up `rule_id + clause` in the
  Arabic catalogue even when `Why` fell back to `event.Describe`.
- `/v1/rules` returns the `i18n` block plus a content `hash` of the loaded
  catalogue (see ADR 0005 for why - the same hash serves cache-invalidation
  for both concerns).
- A build step generates `desktop/src/i18n/generated/rules.json` from
  `rules/*.yaml`, so demo mode and bundle mode (neither of which talk to a
  live API) also render Arabic rule text.
- A generated event-kind catalogue mirrors `internal/event/kinds.go`'s 49
  kinds (pinned to `docs/schema.md` today by
  `internal/event/schema_doc_test.go:27-73`) into a GUI-side table giving
  each kind a human name in both languages with the technical code shown
  beneath - generated, not hand-maintained, so a new Go kind cannot silently
  ship without a matching GUI entry.
- Rust connection errors (`desktop/src-tauri/src/agent.rs:79-121`, currently
  hard-coded English sentences) and the Go capability `reason` become
  `{code, params, technical_detail}`; the GUI shows a translated message
  with the raw detail available on demand.
- An unknown or newer localization key falls back to the English original,
  tagged "original text" in Arabic mode - never a crash, never blank text.

## Consequences

- This is the first Go-side i18n of any kind in the project; `rule.go`,
  `incident.go`, and the API response types all gain fields, all additively.
- Every existing rule file keeps working unmodified (the block is optional);
  translating all 19 files' `title`/`advice`/`why` text is the actual
  content work this ADR unblocks, tracked separately per rule.
- A pre-existing rule fixture without the `i18n` block is kept permanently
  in the test suite specifically to prove the fallback path never regresses.

## Verification

Pending - closed by the Phase 4 gate: every kind in `internal/event/kinds.go`
has both an `ar` and `en` catalogue entry (build fails otherwise); a rule
file without `i18n` still loads and renders with the English-fallback tag;
`TestAnUnknownKeyInARuleIsRefused` still passes unmodified; an automated scan
of the rendered Arabic app finds no Latin-script sentence outside a
`<bdi dir="ltr">`/`TechnicalValue` wrapper.
