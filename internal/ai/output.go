package ai

// ModelOutput is the constrained JSON shape every AI answer must take,
// starting from PRODUCT_RELEASE_PLAN_AR.md §6.2 point 3: "Output مقيد
// grammar/JSON schema: summary, ranked_hypotheses[], evidence_event_ids[],
// counter_evidence[], unknowns[], confidence_ceiling, next_checks[]" -
// superseded on the evidence field by execution order §4.10's handle
// contract: EvidenceHandles carries short closed handles ("E1", "E2", ...),
// never a raw event_id, so there is nothing shaped like a real ULID for the
// model to fabricate a plausible-but-wrong variant of.
//
// Moved here from ai/eval/harness (which now aliases it) alongside
// Hypothesis, so Validate (production-time safety checks) and Grade
// (eval-time accuracy checks against known-correct answers) share one
// definition of what a model's answer actually looks like on the wire.
type ModelOutput struct {
	Summary           string       `json:"summary"`
	RankedHypotheses  []Hypothesis `json:"ranked_hypotheses"`
	EvidenceHandles   []string     `json:"evidence_handles"`
	CounterEvidence   []string     `json:"counter_evidence"`
	Unknowns          []string     `json:"unknowns"`
	ConfidenceCeiling int          `json:"confidence_ceiling"`
	NextChecks        []string     `json:"next_checks"`
}

// Hypothesis is one ranked cause the model is proposing.
type Hypothesis struct {
	Cause      string `json:"cause"`  // an event kind, e.g. "l2.arp_binding_changed"
	Entity     string `json:"entity"` // the subject the cause is about
	Confidence int    `json:"confidence"`
}
