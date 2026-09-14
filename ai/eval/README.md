# AI evaluation corpus

> Built per `PRODUCT_RELEASE_PLAN_AR.md` §6.4, **before** any AI interface or
> model exists — the plan's own ordering ("أنشئ `ai/eval/` قبل واجهة AI").
> Nothing here downloads, runs, or requires a model. This is the yardstick a
> future candidate model is measured against, built first so the measurement
> cannot be shaped around whatever a chosen model happens to do well.

## What this is, and what it is not

Every case in `cases/*.json` grades a *hypothesis*, not free text. Once the
AI layer exists (§6.2), it will always answer in the constrained JSON shape
the plan specifies (`summary`, `ranked_hypotheses[]`, `evidence_event_ids[]`,
`counter_evidence[]`, `unknowns[]`, `confidence_ceiling`, `next_checks[]`) —
a case's `expected` block is what a grader checks that structured output
against: does it cite real event IDs, does it name the right root-cause
kind/entity, does it stay at or under the deterministic engine's own
confidence, does it correctly refuse when refusal is the right answer.

**Every `expected` value is derived from a real run**, not invented for this
corpus: `cases/*.json` is generated (`ai/eval/gen/`, run with
`go run ./ai/eval/gen/`) directly from `corpus/v1/*/events.json` and
`incidents.json` — the same fixtures Phase 0's work already produced from
actual `lab/inject.sh` runs on this machine (see `corpus/v1/README.md`). The
generator is idempotent and safe to re-run whenever `corpus/v1/` changes;
regenerating overwrites `cases/` and `splits.json` deterministically (sorted
scenario order, not random), so a diff after regenerating shows exactly what
changed and why.

## Coverage (33 cases, from 18 real scenarios)

- **Positive** (18): a real incident was concluded; the case grades whether
  the AI names the same root cause, cites real event IDs from the actual
  chain, and does not exceed the real confidence.
- **Negative** (3): `link_flap`, `route_change`, `normal_traffic` in
  isolation — real scenarios where the deterministic engine concluded
  *nothing* (see `corpus/v1/README.md` §3 for why that is correct behaviour,
  not a gap). The correct AI answer is to say so, not invent a cause.
- **Ambiguous** (3): `blind_gap` (2 real incidents: `recorder-was-blind`,
  `collector-not-watching`) and `collector_down` — cases where a real
  incident exists but the correct behaviour is still refusal for the
  underlying question, because the incident itself *is* "the record cannot
  answer this" (§6.2: "ارفض الاستنتاج في gap/collector-down").
- **Adversarial** (1): `malicious_dns_name` — the real prompt-injection
  fixture from Phase 0 (a crafted string sent as a live DNS query, see
  `corpus/v1/README.md` §4), graded on treating the string as data to
  report, never as an instruction.
- **De-identified** (9): a duplicate of each scenario's first positive case
  with the lab's own `10.99.x.x` addresses replaced by `<HOST_N>` tokens,
  proving the pipeline handles anonymised input without a separate code
  path — not because the synthetic lab addresses were ever real user data.
- **Arabic and English**: every case carries both `question_ar` and
  `question_en` for the same underlying grading criteria.

## Splits (`splits.json`)

Assigned by **whole scenario**, never by individual case — splitting cases
from the same scenario across train/dev/test would leak that scenario's own
event IDs (i.e. the answer) between the set used to tune a prompt and the
set used to score it honestly. Deterministic assignment (sorted scenario
name, `i % 4`), not random, so it is reproducible and auditable, not just
repeatable.

## Honest limitations

- **Only one adversarial scenario exists**, and after deterministic
  splitting it landed in `train`. A real red-team set — several
  prompt-injection variants, held out from anything used to tune the
  prompt — is explicit future work before Phase 6's model-selection gate
  can be trusted, not something this corpus already provides.
- **English and Arabic questions grade the same criteria**, but neither has
  been reviewed by a native technical reviewer for naturalness — they were
  written directly, not adapted from real operator language in either
  language.
- **All 18 scenarios come from one synthetic lab topology** (network
  namespaces + veth pairs, see `docs/dev-environment.md`). A model
  benchmarked only against this corpus has not been tested against a real,
  messier network's event stream — the plan's own §6.4 gate criteria
  (Top-1/Top-3 coverage, false-causality rate, citation precision) should be
  read as measured against *this* corpus specifically until a real-world
  corpus exists, not as a general claim about the model's behaviour anywhere.
- **A grading harness exists** (`ai/eval/harness/`, see
  `docs/evidence/17-ai-eval-harness.log`) and **two real models have now
  been benchmarked against it, in both languages, including the
  de-identified cases** (`ai/eval/run/`, see
  `docs/evidence/20-ai-eval-first-benchmark.log` for the first pass and
  `docs/evidence/23-ai-eval-full-comparison.log` for the full picture):
  - **microsoft/Phi-4-mini-instruct** (MIT-licensed, 3.8B) - English:
    2/6 pass, 2/6 top-1, mean citation precision 0.30. Arabic (same 6
    cases): 3/5 graded pass, mean citation precision 0.75 (one case
    genuinely truncated before completion, not a parsing bug).
  - **Qwen2.5-1.5B-Instruct** (Apache-2.0, 1.5B) - English: 1/5 graded
    pass, mean citation precision 0.04. Chosen over both §6.1's Qwen3-4B
    hypothesis and Phi-4-mini-instruct as a genuinely smaller,
    different-family second candidate; underperforms Phi-4-mini-instruct on
    every tracked metric in this run, consistent with its smaller size.
    Arabic (same 6 cases, `docs/evidence/24-ai-eval-qwen-arabic.log`):
    0/3 graded pass, 3/6 invalid - all three invalid outputs are
    grammar-constrained greedy **repetition loops** (the same event ID or
    the same Arabic sentence repeated until the token cap), not budget
    truncations; a second pass at `-n-predict 1500` changed no outcome.
    Both refusal cases were among the invalid ones, so no refusal number
    exists for this combination. Worse than Phi-4-mini-instruct in Arabic
    on every metric and worse than its own English run.
  - Both measurement defects those runs exposed are fixed
    (`docs/evidence/27-ai-eval-corrected-sweep.log`): the runner sends
    `cache_prompt: false` so a long-lived `llama-server` evaluates every
    prompt from scratch (its prompt cache had made `temperature: 0` runs
    non-reproducible), and the system prompt requires answers in the
    question's language. The corrected sweep of both models in both
    languages is the table that stands: no candidate meets the release
    gate - the best combination passes 2 of 6 cases and Phi-4-mini
    fabricates an event ID in two others - so **no model ships and no AI
    feature is enabled in 1.0.0**, per the plan's own rule that a bad result
    keeps AI off rather than lowering the bar.
  - `ai/eval/run` now supports `-lang {en,ar}` and correctly replicates
    `ai/eval/gen`'s address-substitution table for `-deidentified` cases
    (verified against the real `<HOST_1>` token already in
    `gateway_hijack-positive-en-deidentified.json`, not just argued).
  - Real, honest numbers throughout, not a selection decision - two
    candidates, one prompt, `dev` split only (by design; `test` stays
    held out). Three real bugs found and fixed along the way across both
    evidence logs (a llama.cpp grammar/template interaction, a genuine
    JSON-extraction bug in this repo's own runner caught by manually
    reading raw transcripts, and the deidentify-table gap above) - see the
    logs for what each one looked like and how it was caught.
