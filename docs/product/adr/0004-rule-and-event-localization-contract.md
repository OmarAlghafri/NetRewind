# ADR 0004 — Localization contract for rule and event narrative text

**Status:** Rule i18n contract accepted and implemented, all 19 shipped
rules translated (`internal/correlate/rule.go`, `internal/incident/incident.go`,
`internal/correlate/engine.go`, `internal/api/v1/server.go`, `rules/*.yaml`).
The 49-kind event catalogue is also implemented and rendering in the GUI
(`internal/event/gen`, `desktop/src/i18n/kindCatalogue.ts`,
`IncidentCard.tsx`, `Diagnostics.tsx`). Rust/Go error codes, the generated
`desktop/src/i18n/generated/rules.json` build step, and
`IncidentCard.tsx`'s/`Rules.tsx`'s remaining raw-English fields
(`title`/`why`/`advice`) plus the untagged-Latin scanner are separate,
not-yet-started pieces of this same ADR - tracked below, not silently
folded into "done".
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
- `/v1/rules` returns the `i18n` block. **Superseded during implementation**:
  ADR 0005 built a real `ETag`/conditional-GET on this route instead of a
  same-body hash field - see that ADR's own note on the same substitution.
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

**Rule i18n contract (done):**
- `internal/correlate/i18n_test.go`: a rule with a full `i18n.ar` block
  loads and keeps both languages (`TestARuleWithATranslationBlockLoadsAndKeepsBothLanguages`);
  a rule with none still loads unchanged
  (`TestARuleWithNoTranslationBlockStillLoads` - the actual fallback-safety
  claim, not merely assumed from the field being optional); a translation
  naming a clause that does not exist is rejected the same way `root_cause`
  already is (`TestATranslationForANonexistentClauseIsRejected`); a built
  incident's `Chain[].Clause` names the real matched clause, checked against
  a two-clause rule specifically so an off-by-one would show up as the
  wrong name, not just *a* non-empty one
  (`TestChainLinksNameTheClauseThatMatchedThem`).
- `internal/api/v1/server_test.go`'s `TestRulesIncludesTranslationsWhenTheRuleHasThem`:
  the `i18n` block survives the HTTP/JSON round trip, and is omitted
  entirely (not an empty object) for a rule with none.
- `TestAnUnknownKeyInARuleIsRefused` still passes unmodified - the strict
  decoder's behaviour on a genuine typo is untouched by this addition.
- All 19 shipped rules translated (title, advice, every clause's `why`) and
  reverified with the real validator: `go run ./cmd/netrewind rules --dir
  rules` - 19 rules loaded. Read back literally, not merely checked for
  Unicode, per this execution order's own §1 rule 7.
- `go build ./...`, `CGO_ENABLED=0 go test ./...` (all packages), `gofmt -l`
  all clean.

**Event-kind catalogue (done):**
- `internal/event/gen/main.go`: parses `kinds.go`'s AST for every `Kind`
  constant and the `Families` var, writes sorted JSON to
  `desktop/src/i18n/generated/kinds.json` - the same technique
  `internal/event/coverage_test.go`'s `declaredKinds`/`repoRoot` already
  use to keep `docs/schema.md` honest, applied to the GUI catalogue.
- `internal/event/kind_catalogue_test.go`'s
  `TestTheGeneratedKindCatalogueIsUpToDate` re-parses `kinds.go` directly
  and fails if the checked-in JSON drifts - proven by deliberately
  breaking it and watching it fail before restoring (see
  `docs/evidence/36-...log`).
- `desktop/src/i18n/kindCatalogue.ts`: hand-written `{en, ar}` labels for
  all 49 kinds and 10 families, reusing the rule translations' own
  vocabulary (المسار, العنوان, عنوان العتاد, البوابة, المُحلِّل, "توقف ...
  عن الإجابة") rather than inventing a second one, and the execution
  order's glossary correction (وحدة جمع البيانات, not «جامعة»).
  `labelForKind`/`labelForFamily` return `{name, known}`; `known: false`
  is the "unknown key falls back to the original, never blank, never a
  crash" path this ADR requires, for a kind an older GUI build predates.
- `desktop/src/i18n/kindCatalogue.test.ts`: checks the catalogue's key set
  against the generated JSON exactly (missing or extra both fail), and
  that every label is non-empty and distinct from the raw code. Also
  deliberately broken and watched to fail before restoring.
- `IncidentCard.tsx` (chain links and root cause) and `Diagnostics.tsx`
  ("events by family") switched from a bare technical code to the human
  name plus the code in `TechnicalValue` - verified by actually running
  the app against the demo recording in both languages and reading the
  rendered text, not merely by the test suite passing.

**Not started:** Rust/Go error codes for capability `reason` and shell
connection failures; the `desktop/src/i18n/generated/rules.json` build
step; `IncidentCard.tsx`'s `title`/`link.why`/`incident.advice` and all of
`Rules.tsx` still render the raw English rule fields - confirmed still
true by this session's own screenshots, where kind labels are Arabic but
incident titles and advice are still English; the automated scan for
untagged Latin sentences, which needs that remaining frontend work to
exist before it has anything meaningful to scan.
