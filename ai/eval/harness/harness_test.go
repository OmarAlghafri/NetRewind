package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OmarAlghafri/netrewind/ai/eval/schema"
)

func TestAGoodAnswerPassesAPositiveCase(t *testing.T) {
	expected := Expected{
		MustCiteEventIDs: []string{"ev1", "ev2"},
		RootCauseKind:    "l2.arp_binding_changed",
		RootCauseEntity:  "10.99.1.11",
		MaxConfidence:    88,
	}
	known := map[string]bool{"ev1": true, "ev2": true, "ev3": true}

	out := ModelOutput{
		Summary: "the ARP binding changed, breaking a working path",
		RankedHypotheses: []Hypothesis{
			{Cause: "l2.arp_binding_changed", Entity: "10.99.1.11", Confidence: 88},
		},
		EvidenceEventIDs: []string{"ev1", "ev2"},
	}

	r := Grade("t1", expected, known, out)
	if !r.Pass() {
		t.Fatalf("expected a pass, got violations: %v", r.Violations)
	}
	if !r.Top1CauseHit {
		t.Error("expected Top1CauseHit for the correct cause ranked first")
	}
	if r.CitationPrecision != 1.0 {
		t.Errorf("CitationPrecision = %v, want 1.0", r.CitationPrecision)
	}
}

func TestAFabricatedCitationIsAlwaysAViolation(t *testing.T) {
	expected := Expected{RootCauseKind: "link.down", RootCauseEntity: "eth0", MaxConfidence: 80}
	known := map[string]bool{"ev1": true}

	out := ModelOutput{
		RankedHypotheses: []Hypothesis{{Cause: "link.down", Entity: "eth0", Confidence: 50}},
		EvidenceEventIDs: []string{"ev1", "ev-does-not-exist"},
	}

	r := Grade("t2", expected, known, out)
	if r.Pass() {
		t.Fatal("a citation of an event ID absent from the input must never pass")
	}
	found := false
	for _, v := range r.Violations {
		if strings.Contains(v, "ev-does-not-exist") {
			found = true
		}
	}
	if !found {
		t.Errorf("violations do not name the fabricated ID: %v", r.Violations)
	}
}

func TestConfidenceAboveTheCeilingIsAViolation(t *testing.T) {
	expected := Expected{RootCauseKind: "link.down", RootCauseEntity: "eth0", MaxConfidence: 80}
	known := map[string]bool{"ev1": true}

	out := ModelOutput{
		RankedHypotheses: []Hypothesis{{Cause: "link.down", Entity: "eth0", Confidence: 99}},
		EvidenceEventIDs: []string{"ev1"},
	}

	r := Grade("t3", expected, known, out)
	if r.Pass() {
		t.Fatal("a hypothesis more confident than the evidence itself must never pass")
	}
}

func TestNoHypothesisOfferedFailsAPositiveCase(t *testing.T) {
	expected := Expected{RootCauseKind: "link.down", RootCauseEntity: "eth0"}
	r := Grade("t4", expected, map[string]bool{}, ModelOutput{})
	if r.Pass() {
		t.Fatal("offering nothing for a case with a real, findable cause must fail")
	}
}

func TestAConfidentAnswerOnARefusalCaseIsFalseCausality(t *testing.T) {
	expected := Expected{RefusalExpected: true}
	known := map[string]bool{"ev1": true}

	out := ModelOutput{
		RankedHypotheses: []Hypothesis{{Cause: "link.down", Entity: "eth0", Confidence: 40}},
		EvidenceEventIDs: []string{"ev1"},
	}
	r := Grade("t5", expected, known, out)
	if r.Pass() {
		t.Fatal("a confident hypothesis where refusal was correct is exactly the false-causality this harness must catch")
	}
}

func TestACorrectRefusalPasses(t *testing.T) {
	expected := Expected{RefusalExpected: true}
	out := ModelOutput{
		Summary:  "the record has a gap here",
		Unknowns: []string{"whether anything happened during the blind period"},
	}
	r := Grade("t6", expected, map[string]bool{}, out)
	if !r.Pass() {
		t.Fatalf("a correct, honest refusal must pass: %v", r.Violations)
	}
	if !r.RefusedCorrectly {
		t.Error("RefusedCorrectly should be true")
	}
}

func TestABareRefusalWithNoUnknownsNamedFails(t *testing.T) {
	expected := Expected{RefusalExpected: true}
	out := ModelOutput{Summary: "not sure"}
	r := Grade("t7", expected, map[string]bool{}, out)
	if r.Pass() {
		t.Fatal("a refusal that names no unknowns is not more useful than a wrong answer, and must not pass")
	}
}

func TestTop3HitWithoutTop1(t *testing.T) {
	expected := Expected{RootCauseKind: "link.down", RootCauseEntity: "eth0"}
	out := ModelOutput{
		RankedHypotheses: []Hypothesis{
			{Cause: "l3.route_removed", Entity: "default", Confidence: 60},
			{Cause: "link.down", Entity: "eth0", Confidence: 40},
		},
	}
	r := Grade("t8", expected, map[string]bool{}, out)
	if r.Top1CauseHit {
		t.Error("Top1CauseHit should be false - the right cause was ranked second")
	}
	if !r.Top3CauseHit {
		t.Error("Top3CauseHit should be true - the right cause was in the top 3")
	}
}

func TestCitationPrecisionPartialOverlap(t *testing.T) {
	expected := Expected{RootCauseKind: "x", RootCauseEntity: "y", MustCiteEventIDs: []string{"ev1", "ev2"}}
	known := map[string]bool{"ev1": true, "ev2": true, "ev3": true}
	out := ModelOutput{
		RankedHypotheses: []Hypothesis{{Cause: "x", Entity: "y", Confidence: 10}},
		EvidenceEventIDs: []string{"ev1", "ev3"}, // one expected, one not
	}
	r := Grade("t9", expected, known, out)
	if r.CitationPrecision != 0.5 {
		t.Errorf("CitationPrecision = %v, want 0.5", r.CitationPrecision)
	}
}

// TestHarnessRunsAgainstEveryGeneratedCase is the real integration check:
// it loads every case ai/eval/gen actually produced from corpus/v1/ and
// grades a synthetic "perfect" answer built directly from each case's own
// Expected block. Every one must pass - if the harness cannot pass an
// answer that is trivially, exactly correct by construction, the harness
// itself is broken, not any future model.
func TestHarnessRunsAgainstEveryGeneratedCase(t *testing.T) {
	dir := repoCasesDir(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v (run `go run ./ai/eval/gen/` first)", dir, err)
	}
	if len(entries) == 0 {
		t.Fatal("no generated cases found - run `go run ./ai/eval/gen/` first")
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var c schema.Case
		if err := json.Unmarshal(data, &c); err != nil {
			t.Fatalf("%s: %v", entry.Name(), err)
		}

		t.Run(c.ID, func(t *testing.T) {
			known := make(map[string]bool, len(c.EventIDs))
			for _, id := range c.EventIDs {
				known[id] = true
			}

			var out ModelOutput
			if c.Expected.RefusalExpected {
				out = ModelOutput{Summary: "refusing", Unknowns: []string{"the cause cannot be determined from this record"}}
			} else {
				out = ModelOutput{
					Summary: "synthetic perfect answer built from the case's own expected block",
					RankedHypotheses: []Hypothesis{
						{Cause: c.Expected.RootCauseKind, Entity: c.Expected.RootCauseEntity, Confidence: c.Expected.MaxConfidence},
					},
					EvidenceEventIDs: c.Expected.MustCiteEventIDs,
				}
			}

			r := Grade(c.ID, c.Expected, known, out)
			if !r.Pass() {
				t.Errorf("a by-construction-correct answer failed: %v", r.Violations)
			}
			if !c.Expected.RefusalExpected && !r.Top1CauseHit {
				t.Error("the exact expected cause, ranked first, did not register as a Top1CauseHit")
			}
		})
	}
}

func repoCasesDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		candidate := filepath.Join(dir, "ai", "eval", "cases")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not find ai/eval/cases from the test's working directory")
	return ""
}
