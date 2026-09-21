# ADR 0006 — Local AI runtime, model manager, and candidate selection

**Status:** Accepted; runtime/model-manager code implemented; the
evaluation gate (Verification, below) has now been run on real hardware
against two model families - neither is offered, for the reasons the
2026-09-21 amendment records. `AI_FEATURE_ENABLED` stays `false`.
**Date:** 2026-09-19. Amended 2026-09-20, 2026-09-21.

## Context

`ai/eval/` already has a corpus (33 cases from 18 real lab scenarios), a
harness (`ai/eval/harness/`), and a runner
(`ai/eval/run/main.go`) that drives an operator-started `llama-server` over
loopback HTTP. The corrected sweep on record
(`docs/evidence/27-ai-eval-corrected-sweep.log:31-79`) found no shipping
candidate: the best combination (Phi-4-mini-instruct, English) passes 2 of 6
graded cases and Phi-4-mini fabricates an event ID in two others. No
Go/Rust model-manager, sidecar, or download/verification code exists outside
the eval harness. `NOTICE` lists no third-party runtime licenses.
`internal/update` already has a verified-download primitive
(`apply.go:34-94`, `verify.go:104-123` - fetch, SHA-256, optional ed25519
signature, constant-time compare) built for the self-updater.

## Decision

- **Runtime:** a pinned llama.cpp release run as a Tauri sidecar
  (`externalBin`), starting from build `b10948` (the build already used for
  every evaluation run to date) unless a newer pinned release is
  re-evaluated and earns the switch. The binary is fetched at build time by
  a pinned URL and a recorded SHA-256 in a lock file - never `latest`. MIT
  license added to `NOTICE` and the SBOM. Bound to loopback only, a random
  port, a per-session token, hard RAM/thread/context/timeout limits, killed
  on app exit or user cancel.
- **Model delivery:** never bundled in the installer. Opt-in download only,
  driven by a signed manifest reusing `internal/update`'s verification
  primitive rather than a second implementation - naming model ID,
  revision, quantization, URL, size, SHA-256, signature, license, and
  minimum RAM. Resumable to `.partial`; hash and signature verified before
  an atomic rename; a file that fails verification is quarantined, never
  silently deleted.
- **Evidence-handle contract** (the fix for why both measured models failed
  so far - `ai/eval/README.md`'s reported failures are dominated by
  malformed/fabricated event-ID references, not reasoning quality): the
  model is given short closed handles (`E1..En`) mapped to real event IDs by
  the backend, enforced by a per-request JSON-schema/grammar restricting the
  model to exactly the handles it was actually offered. Any handle outside
  that set fails the whole answer closed before it is ever displayed.
- **Deterministic confidence ceiling:** computed by a guardrail from record
  quality/relations/gaps - the model never sets its own ceiling, and a
  required-collector-down or in-window gap forces `insufficient_evidence`
  before inference is even attempted.
- **Candidates for the 1.1.0 gate** (neither of the two already-failed
  models is re-run as a shipping candidate): Qwen3-4B-Instruct GGUF Q4_K_M
  (Apache-2.0) as the primary hypothesis; a Qwen3-1.7B (or comparable)
  low-resource profile; one comparator from a different model family under
  a confirmed-compatible open license, evaluated only after its license
  terms are checked (a Gemma-family model needs the project owner's
  explicit sign-off before shipping to users, even if it is evaluated
  locally).

## Consequences

- This is the first runtime binary and first opt-in network download this
  project ships beyond the recorder/desktop themselves; both get their own
  NOTICE/SBOM entries and their own threat-model note
  (`docs/product/threat-model.md`) once implemented.
- The AI panel UI is built only after the evaluation gate is met - if it is
  not met, this ADR's runtime/manager code may still land (it is needed to
  *run* the gate), but the feature stays off and is reported that way in
  `CHANGELOG.md`, exactly as 1.0.0 already did for the same reason.
- Real evaluation requires real multi-gigabyte downloads and multi-hour CPU
  benchmark runs on real hardware (Windows x64 and Linux CPU) - not
  something a single short session can complete; each run's raw numbers are
  recorded under `docs/evidence/gui-modernization/1.1.0/ai-eval/` regardless
  of outcome.

## Verification

Pending - closed by the Phase 6 gate (execution order §4.10/§9): 100% valid
JSON, 100% correct handles (zero fabricated references), correct refusal on
every gap/collector-down case, zero confidence-ceiling violations, zero
tool/network/command content in any model output, measured RAM/latency
within the budget documented before the run. Thresholds are fixed before
the run and never lowered after seeing a result.

## Amendment (2026-09-20)

Written after the runtime, model manager, and desktop UI described below
were actually built, against the 1.2.0 plan this ADR's Decision predates.
Recorded here rather than by silently rewriting the Decision above, so the
original reasoning stays legible.

- **Governing change: one Go core, not per-consumer logic.** `internal/ai`
  (handle resolution, prompt/schema construction, the guardrail table,
  post-answer validation, similarity ranking, the chat client) was
  extracted from `ai/eval/harness` into a shared library. `ai/eval/run`,
  `cmd/netrewind/ai.go` (`netrewind ai analyze`/`explain`/`report`), and the
  desktop shell (which spawns the same CLI as a subprocess, never
  reimplementing any of this in Rust or TypeScript) all call the identical
  code path - "what was measured is what ships" holds by construction, not
  by convention.
- **Not `externalBin`.** Tauri's `externalBin` copies exactly one binary
  into the bundle; llama.cpp's own release archives ship the server
  alongside shared libraries (`ggml*`, `llama.*`) it needs beside it at
  runtime, which `externalBin` cannot carry. The actual implementation
  (`desktop/src-tauri/src/ai/runtime.rs`) stages the whole runtime
  directory as a bundled resource instead, and verifies every staged
  file's SHA-256 against `runtime.lock.json` on every launch (not only at
  install time) - the risk `externalBin` would not have covered any better
  than this does, since a single-file copy still needs its own integrity
  check.
- **Model manager landed as designed**, with one implementation detail the
  original Decision left open now settled: downloads run in Go
  (`internal/aimodel`, resumable to `.partial`, quarantine-on-mismatch,
  never silently deleted) driven by a signed manifest, and the Rust shell
  spawns the staged `netrewind` CLI for both the download and the analyze
  path rather than carrying its own TLS/HTTP stack - `Cargo.toml` gained no
  networking crate for this feature.
- **Candidate list superseded.** The Qwen3-4B-Instruct/Phi-4-mini/Gemma
  list above is the pre-1.2.0-plan sweep's result, not the current
  candidate set. The 1.2.0 plan replaced it with a tiered catalogue -
  Small/Balanced/Full profiles (Qwen3.5 0.8B/2B/4B as the current
  candidates, Full already pre-registered) plus an optional Falcon-H1
  comparator pending its own licence sign-off - selected per tier by gate
  result on that tier's hardware, never by size or popularity. See the
  1.2.0 plan document for the full table and the excluded-with-reason
  list (Gemma, Llama, LFM2, Ministral, SmolLM2/3, Jais).
- **Operator notes / memory** (annotations, retrieval, feedback, opt-in
  follow-up threads) is a related but separate decision - see ADR 0008,
  not folded into this one because it is about the recorder's own write
  surface, not the runtime or model selection.
- **Still open, in the order they block each other:** no `runtime.lock.json`
  exists in the repo yet - `desktop/src-tauri/src/ai/runtime.rs`'s own
  tests parse a document of the intended shape as a fixture, but nothing
  has staged a real one against actual per-file SHA-256 digests of a
  genuine llama.cpp release (blocked on downloading that release to hash
  it - held pending the standing "no model/asset downloads without an
  explicit URL and size confirmed first" rule); the `make desktop-runtime`
  Makefile target, CI caching, and the arm64 runtime build job do not
  exist yet, and are not useful to write against a lock file that does not
  yet carry real hashes; `models.json` is not yet signed or published.
  **`AI_FEATURE_ENABLED` itself - the compile-time gate this ADR's Decision
  and `ai/mod.rs`'s own doc comment both describe - does not exist as code
  anywhere yet.** Today the panel's only gate is `aiSettings.enabled`, a
  plain runtime setting the operator can already turn on in Settings
  (default off) as long as they have typed in a model file name obtained
  out of band - there is no build-time switch stopping that regardless of
  whether any profile has passed its evaluation gate. Adding the real
  compile-time gate, and having it actually key off a signed manifest's
  `gate.passed` per profile as the plan specifies, remains open and is a
  precondition for `AI_FEATURE_ENABLED` to mean what this ADR says it
  means.

## Amendment (2026-09-21)

Written after the tiered catalogue was actually run through the gate on
the second test machine, twice - the first time this ADR's Verification
section has real numbers behind it rather than a description of what a
future run would check.

- **`AI_FEATURE_ENABLED` now exists** (Rust `ai::AI_FEATURE_ENABLED`,
  TS `data/aiFeature.ts`), checked before `ai_runtime_start`/`ai_analyze`
  and first in the desktop panel's own state machine. It is `false` and
  stays `false` - nothing in this amendment changes that.
- **`models.json` is signed** and ships three profiles (Small/Balanced/
  Full), each `gate.passed: false` with a real, specific evidence string
  naming the exact failing run - not the placeholder empty string the
  2026-09-20 amendment found.
- **The Small/Balanced/Full Qwen3.5 catalogue (0.8B/2B/4B) failed its
  gate outright**: Small on JSON validity (a repetition loop exhausts
  the token budget before the object closes); Balanced and Full both on
  refusal correctness (both confidently answered on both
  insufficient-evidence cases in the test split instead of refusing).
  `docs/evidence/54-ai-eval-three-tier-gate-run.log`.
- **A second family, IBM Granite 4.x, was tried as a replacement** after
  research into models specifically suited to structured-output
  reliability and refusal calibration (Apache-2.0, dense architecture,
  official Arabic support). All three tiers failed their first run too,
  for a different, precisely diagnosed reason: verbose in-string
  reasoning inside a JSON field exhausting the token budget, not
  repetition. `docs/evidence/55-ai-eval-granite-gate-run.log`.
- **The verbosity failure was fixed for real** (`internal/ai/schema.go`
  gained per-field `maxLength`, grammar-enforced by llama-server itself;
  two new system-prompt rules; `--no-reasoning-preserve` on the server -
  the model's own chat template preserves reasoning by default). Granite
  4.2-3B re-run on `test` then produced 9/9 valid JSON and held its 2/2
  correct-refusal record - **the first candidate in this project's
  history to technically meet every one of the six pre-registered safety
  criteria.** `docs/evidence/56-ai-eval-granite-verbosity-fix-final.log`.
- **Not offered anyway, on a usefulness judgment rather than a gate
  failure**: that same run scored 0/9 top-1 cause hits and declined to
  even attempt an answer on 5 of the 6 cases with a real, findable cause.
  The six criteria were written to catch a *dangerous* model (one that
  fabricates, overclaims, or fails to refuse) and were never meant to be
  sufficient on their own - they say nothing about whether a model is
  actually useful. A feature that is safe but almost never finds the
  cause would train operators to stop trying it, which costs the
  product's credibility more than shipping nothing does. This is exactly
  the judgment call this ADR's own Decision always intended a human to
  make on top of a passed gate, not a mechanical override of it.
- **Gap in the gate methodology itself, flagged for the next
  pre-registration, not retrofitted onto this one**: add a seventh,
  usefulness-floor criterion (e.g. a minimum top-3 hit rate on
  positive-kind cases) alongside the six safety criteria, so a
  technically-safe-but-empty model cannot reach this same ambiguous
  position again.
- **Still genuinely open**: `runtime.lock.json` and the shipped desktop
  app's own automatic runtime staging (`make desktop-runtime`,
  `internal/ai/cmd/fetchruntime`, the CI caching and arm64 build job) -
  every gate run to date started `llama-server` manually with a runtime
  downloaded and hashed by hand for that run, which is sufficient for
  evaluation but not for a real end-user's one-click download-and-run
  flow. Not attempted in this amendment: building it for a runtime that
  no approved model would yet use serves no one, and is better done once
  (or if) a candidate actually passes on both safety and usefulness.
  Gemma 4 (Apache-2.0 since April 2026, sizes matching this project's
  tiers, but a documented JSON-strictness weakness of its own) was
  identified as a third candidate and not yet tried.
