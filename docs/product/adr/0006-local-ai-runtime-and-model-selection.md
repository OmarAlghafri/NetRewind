# ADR 0006 — Local AI runtime, model manager, and candidate selection

**Status:** Proposed.
**Date:** 2026-09-19.

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
