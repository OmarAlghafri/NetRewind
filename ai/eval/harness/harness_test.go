package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OmarAlghafri/netrewind/ai/eval/schema"
)

// testHandleMap builds a HandleMap the same way BuildHandles would, from a
// plain list of "real IDs" in order - a small helper so each test below can
// say what handle maps to what real ID without constructing full event
// maps just to get an event_id field read out of them. Delegates to the
// real BuildHandles (via a minimal synthetic event per ID) rather than
// reimplementing the "E%d" numbering separately, so this helper cannot
// silently drift from the actual assignment rule under test.
func testHandleMap(ids ...string) HandleMap {
	events := make([]map[string]any, len(ids))
	for i, id := range ids {
		events[i] = map[string]any{"event_id": id}
	}
	return BuildHandles(events)
}

func TestAGoodAnswerPassesAPositiveCase(t *testing.T) {
	expected := Expected{
		MustCiteEventIDs: []string{"ev1", "ev2"},
		RootCauseKind:    "l2.arp_binding_changed",
		RootCauseEntity:  "10.99.1.11",
		MaxConfidence:    88,
	}
	hm := testHandleMap("ev1", "ev2", "ev3")

	out := ModelOutput{
		Summary: "the ARP binding changed, breaking a working path",
		RankedHypotheses: []Hypothesis{
			{Cause: "l2.arp_binding_changed", Entity: "10.99.1.11", Confidence: 88},
		},
		EvidenceHandles: []string{"E1", "E2"},
	}

	r := Grade("t1", expected, hm, out)
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

// TestAHandleOutsideTheOfferedSetIsAlwaysAViolation is the direct proof of
// execution order §4.10's actual fix: a model can no longer fabricate a
// plausible-looking ULID, but it could still (if the server-side schema
// enforcement were ever misconfigured or bypassed) emit a handle it was
// never offered - e.g. "E99" when only E1 was given. That must still fail
// exactly as fabricating a raw event ID always did.
func TestAHandleOutsideTheOfferedSetIsAlwaysAViolation(t *testing.T) {
	expected := Expected{RootCauseKind: "link.down", RootCauseEntity: "eth0", MaxConfidence: 80}
	hm := testHandleMap("ev1")

	out := ModelOutput{
		RankedHypotheses: []Hypothesis{{Cause: "link.down", Entity: "eth0", Confidence: 50}},
		EvidenceHandles:  []string{"E1", "E99"},
	}

	r := Grade("t2", expected, hm, out)
	if r.Pass() {
		t.Fatal("a handle never offered for this case must never pass")
	}
	found := false
	for _, v := range r.Violations {
		if strings.Contains(v, "E99") {
			found = true
		}
	}
	if !found {
		t.Errorf("violations do not name the out-of-set handle: %v", r.Violations)
	}
}

func TestConfidenceAboveTheCeilingIsAViolation(t *testing.T) {
	expected := Expected{RootCauseKind: "link.down", RootCauseEntity: "eth0", MaxConfidence: 80}
	hm := testHandleMap("ev1")

	out := ModelOutput{
		RankedHypotheses: []Hypothesis{{Cause: "link.down", Entity: "eth0", Confidence: 99}},
		EvidenceHandles:  []string{"E1"},
	}

	r := Grade("t3", expected, hm, out)
	if r.Pass() {
		t.Fatal("a hypothesis more confident than the evidence itself must never pass")
	}
}

func TestNoHypothesisOfferedFailsAPositiveCase(t *testing.T) {
	expected := Expected{RootCauseKind: "link.down", RootCauseEntity: "eth0"}
	r := Grade("t4", expected, HandleMap{}, ModelOutput{})
	if r.Pass() {
		t.Fatal("offering nothing for a case with a real, findable cause must fail")
	}
}

func TestAConfidentAnswerOnARefusalCaseIsFalseCausality(t *testing.T) {
	expected := Expected{RefusalExpected: true}
	hm := testHandleMap("ev1")

	out := ModelOutput{
		RankedHypotheses: []Hypothesis{{Cause: "link.down", Entity: "eth0", Confidence: 40}},
		EvidenceHandles:  []string{"E1"},
	}
	r := Grade("t5", expected, hm, out)
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
	r := Grade("t6", expected, HandleMap{}, out)
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
	r := Grade("t7", expected, HandleMap{}, out)
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
	r := Grade("t8", expected, HandleMap{}, out)
	if r.Top1CauseHit {
		t.Error("Top1CauseHit should be false - the right cause was ranked second")
	}
	if !r.Top3CauseHit {
		t.Error("Top3CauseHit should be true - the right cause was in the top 3")
	}
}

func TestCitationPrecisionPartialOverlap(t *testing.T) {
	expected := Expected{RootCauseKind: "x", RootCauseEntity: "y", MustCiteEventIDs: []string{"ev1", "ev2"}}
	hm := testHandleMap("ev1", "ev2", "ev3")
	out := ModelOutput{
		RankedHypotheses: []Hypothesis{{Cause: "x", Entity: "y", Confidence: 10}},
		EvidenceHandles:  []string{"E1", "E3"}, // one expected (ev1), one not (ev3)
	}
	r := Grade("t9", expected, hm, out)
	if r.CitationPrecision != 0.5 {
		t.Errorf("CitationPrecision = %v, want 0.5", r.CitationPrecision)
	}
}

// TestBuildHandlesAssignsInEventOrderAndSkipsMalformedEntries proves the
// handle-assignment contract directly: sequential E1..En in input order,
// and an event with no usable event_id is skipped rather than silently
// consuming a handle number it could never legitimately be cited by.
func TestBuildHandlesAssignsInEventOrderAndSkipsMalformedEntries(t *testing.T) {
	events := []map[string]any{
		{"event_id": "id-a", "kind": "link.down"},
		{"kind": "malformed, no event_id"},
		{"event_id": "id-b", "kind": "link.up"},
	}
	hm := BuildHandles(events)
	if len(hm.Handles) != 2 {
		t.Fatalf("Handles = %v, want exactly 2 (the malformed entry must be skipped)", hm.Handles)
	}
	if hm.ToHandle["id-a"] != "E1" || hm.ToHandle["id-b"] != "E2" {
		t.Errorf("ToHandle = %v, want id-a->E1, id-b->E2 (event order preserved)", hm.ToHandle)
	}
	if hm.ToID["E1"] != "id-a" || hm.ToID["E2"] != "id-b" {
		t.Errorf("ToID = %v, want the reverse mapping", hm.ToID)
	}
}

// TestRedactEventNeverLeavesTheRealEventIDReachable is the actual anti-
// fabrication guarantee at the source: proves the field the model would
// need to copy to fabricate a citation is not merely renamed but genuinely
// absent from what RedactEvent returns - there is nothing shaped like a
// real ULID anywhere in the model's input to begin with.
func TestRedactEventNeverLeavesTheRealEventIDReachable(t *testing.T) {
	events := []map[string]any{{"event_id": "01M2REALULIDVALUE", "kind": "link.down", "schema_v": 1, "ts_mono": 12345, "severity": "warn"}}
	hm := BuildHandles(events)
	redacted := hm.RedactEvent(events[0])

	data, err := json.Marshal(redacted)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "01M2REALULIDVALUE") {
		t.Errorf("redacted event still contains the real event_id: %s", data)
	}
	if redacted["handle"] != "E1" {
		t.Errorf(`redacted["handle"] = %v, want "E1"`, redacted["handle"])
	}
	if _, present := redacted["schema_v"]; present {
		t.Error("schema_v should be stripped, not just event_id")
	}
	if _, present := redacted["ts_mono"]; present {
		t.Error("ts_mono should be stripped, not just event_id")
	}
	if redacted["severity"] != "warn" {
		t.Error("an unrelated, analytically useful field (severity) must survive redaction unchanged")
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
			hm := testHandleMap(c.EventIDs...)

			var out ModelOutput
			if c.Expected.RefusalExpected {
				out = ModelOutput{Summary: "refusing", Unknowns: []string{"the cause cannot be determined from this record"}}
			} else {
				citeHandles := make([]string, 0, len(c.Expected.MustCiteEventIDs))
				for _, id := range c.Expected.MustCiteEventIDs {
					citeHandles = append(citeHandles, hm.ToHandle[id])
				}
				out = ModelOutput{
					Summary: "synthetic perfect answer built from the case's own expected block",
					RankedHypotheses: []Hypothesis{
						{Cause: c.Expected.RootCauseKind, Entity: c.Expected.RootCauseEntity, Confidence: c.Expected.MaxConfidence},
					},
					EvidenceHandles: citeHandles,
				}
			}

			r := Grade(c.ID, c.Expected, hm, out)
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
