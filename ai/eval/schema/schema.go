// Package schema is the shared shape of one ai/eval case, used by both the
// generator (ai/eval/gen) that produces cases from corpus/v1/ and the
// harness (ai/eval/harness) that grades a model's answer against one.
package schema

// Case is one graded prompt: input events, a question in both languages,
// and expected-answer criteria a grader checks a model's constrained JSON
// output against - never free-text comparison.
type Case struct {
	ID         string   `json:"id"`
	Scenario   string   `json:"scenario"`
	Kind       string   `json:"kind"` // "positive", "negative", "ambiguous", "adversarial"
	QuestionAr string   `json:"question_ar"`
	QuestionEn string   `json:"question_en"`
	EventIDs   []string `json:"input_event_ids"`
	Expected   Expected `json:"expected"`
	Note       string   `json:"note,omitempty"`
	// EventsOverride, when set, is a repo-root-relative path to an events
	// JSON file to load INSTEAD of corpus/v1/<Scenario>/events.json - used
	// only by a synthetic adversarial case that needs one field of a real
	// event modified to test something the real, unmodified lab run never
	// actually captured (ai/eval/synthetic/, never corpus/v1/, which stays
	// "every number here is real" per its own README). Scenario is kept
	// even then, naming which real scenario the synthetic variant is based
	// on, for lineage - it is documentation in that case, not a load path.
	EventsOverride string `json:"events_override,omitempty"`
}

// Expected is what a passing answer must satisfy.
type Expected struct {
	// RefusalExpected is true when the correct behaviour is to decline a
	// cause entirely (a blind spot, a collector down, or genuinely nothing
	// concluded) - PRODUCT_RELEASE_PLAN_AR.md §6.2: "ارفض الاستنتاج في
	// gap/collector-down."
	RefusalExpected bool `json:"refusal_expected"`
	// MustCiteEventIDs are real event IDs from the actual incident chain,
	// never invented - what a passing answer's evidence_handles should
	// resolve to (execution order §4.10's handle contract: the model cites
	// handles like "E3", never a raw event ID; harness.Grade resolves them
	// back to real IDs via the case's HandleMap before comparing here).
	MustCiteEventIDs []string `json:"must_cite_event_ids,omitempty"`
	RootCauseKind    string   `json:"root_cause_kind,omitempty"`
	RootCauseEntity  string   `json:"root_cause_entity,omitempty"`
	// MaxConfidence bounds a hypothesis's confidence: the AI must never
	// report higher confidence than the deterministic engine itself reached
	// for the same conclusion (§6.2: "لا تقول 'سبب مؤكد' فوق confidence
	// الدليل").
	MaxConfidence int `json:"max_confidence,omitempty"`
	// RelationMustInclude names relation words (causes/correlates/precedes)
	// a correct explanation should not contradict.
	RelationMustInclude []string `json:"relation_must_include,omitempty"`
	RuleID              string   `json:"rule_id,omitempty"`
}
