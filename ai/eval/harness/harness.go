// Package harness grades one AI answer against one ai/eval case.
//
// This package does not call a model. It exists so the grading rules
// themselves - what counts as a violation, what counts as a score - are
// settled and tested before any model exists to produce something to grade,
// exactly as PRODUCT_RELEASE_PLAN_AR.md requires the eval corpus to exist
// before the AI interface does (§6.4). A grader nobody has tried to fool
// with a bad answer is not yet a grader.
package harness

import (
	"github.com/OmarAlghafri/netrewind/ai/eval/schema"
	"github.com/OmarAlghafri/netrewind/internal/ai"
)

// Expected is an alias onto the shared schema package - see
// ai/eval/schema.Expected for field-by-field documentation.
type Expected = schema.Expected

// ModelOutput and Hypothesis are aliases onto internal/ai - see their own
// doc comments there. Moved out of this package so Validate
// (production-time safety checks, internal/ai) and Grade (this package's
// eval-time accuracy checks against known-correct answers) share one
// definition of what a model's answer looks like on the wire, rather than
// two structurally-identical types that could quietly drift apart.
type ModelOutput = ai.ModelOutput
type Hypothesis = ai.Hypothesis

// Result is one graded answer. Violations are the hard,
// release-blocking failures from §6.4's "معايير منع الإصدار" - any one of
// them fails the case outright. The remaining fields are scored metrics,
// tracked per model candidate/quantization over time rather than pass/fail
// on their own, matching §6.4's "لكل مرشح... سجّل... citation precision،
// Top-1/Top-3 cause coverage".
type Result struct {
	CaseID     string
	Violations []string

	Top1CauseHit      bool
	Top3CauseHit      bool
	CitationPrecision float64 // fraction of cited event IDs that were actually expected
	RefusedCorrectly  bool    // only meaningful when the case expects refusal
}

// Pass reports whether the answer cleared every hard gate. A case can Pass
// with imperfect scores (citation precision below 1.0, no Top-1 hit) - those
// are tracked, not blocking - but never with a Violation present.
func (r Result) Pass() bool { return len(r.Violations) == 0 }

// Grade checks out against what case expects. hm is the exact HandleMap
// built from the case's own input events (harness.BuildHandles) - the set
// of handles that were actually offered to the model for this case.
//
// A handle outside that set (execution order §4.10's "any handle outside
// that set fails the whole answer closed") is the model inventing evidence
// - the single most dangerous failure mode this whole design exists to
// prevent (§6.2: "لا تخترع حدثاً"), now structurally harder to trigger
// than a fabricated ULID ever was, but still checked explicitly rather than
// assumed impossible: a server not actually enforcing the per-request
// grammar (a misconfiguration, not a model failure) must still be caught
// here, not silently trusted.
func Grade(caseID string, expected Expected, hm HandleMap, out ModelOutput) Result {
	r := Result{CaseID: caseID}

	citedIDs, unknownHandles := hm.ResolveHandles(out.EvidenceHandles)
	for _, h := range unknownHandles {
		r.Violations = append(r.Violations, "handle not in the offered evidence set: "+h)
	}

	if expected.RefusalExpected {
		gradeRefusal(&r, expected, out)
		return r
	}

	if len(out.RankedHypotheses) == 0 {
		r.Violations = append(r.Violations, "no hypothesis offered for a case that has a real, findable cause")
		return r
	}

	// No hypothesis may claim more confidence than the deterministic engine
	// itself reached for the same conclusion (§6.2: "لا تقول 'سبب مؤكد' فوق
	// confidence الدليل").
	for _, h := range out.RankedHypotheses {
		if expected.MaxConfidence > 0 && h.Confidence > expected.MaxConfidence {
			r.Violations = append(r.Violations,
				"hypothesis confidence exceeds the evidence's own confidence ceiling")
		}
	}
	if expected.MaxConfidence > 0 && out.ConfidenceCeiling > expected.MaxConfidence {
		r.Violations = append(r.Violations, "confidence_ceiling exceeds the evidence's own confidence")
	}

	for i, h := range out.RankedHypotheses {
		hit := h.Cause == expected.RootCauseKind && h.Entity == expected.RootCauseEntity
		if hit && i == 0 {
			r.Top1CauseHit = true
		}
		if hit && i < 3 {
			r.Top3CauseHit = true
		}
	}

	r.CitationPrecision = precision(citedIDs, expected.MustCiteEventIDs)
	return r
}

func gradeRefusal(r *Result, expected Expected, out ModelOutput) {
	// A confident hypothesis when the correct answer is "the record cannot
	// say" is false-causality - exactly what §6.4's benchmark table tracks
	// as "false-causality" and what §6.2 forbids outright for a gap or a
	// downed collector.
	if len(out.RankedHypotheses) > 0 {
		r.Violations = append(r.Violations,
			"offered a confident hypothesis for a case where refusal was the correct answer")
		return
	}
	if len(out.Unknowns) == 0 {
		r.Violations = append(r.Violations,
			"refused without naming what is unknown - a bare refusal is not more useful than a wrong answer")
		return
	}
	r.RefusedCorrectly = true
}

// precision is the fraction of cited IDs that were actually among the ones
// expected. Undefined (reported as 0) when nothing was expected and nothing
// was cited would be a division by zero rather than a meaningful score, so
// that case reports 1.0: citing nothing when nothing was required is not a
// precision failure.
func precision(cited, expected []string) float64 {
	if len(expected) == 0 {
		if len(cited) == 0 {
			return 1
		}
		return 0
	}
	want := make(map[string]bool, len(expected))
	for _, id := range expected {
		want[id] = true
	}
	if len(cited) == 0 {
		return 0
	}
	hits := 0
	for _, id := range cited {
		if want[id] {
			hits++
		}
	}
	return float64(hits) / float64(len(cited))
}
