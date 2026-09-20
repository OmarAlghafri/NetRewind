package ai

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/OmarAlghafri/netrewind/ai/eval/schema"
	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/incident"
	"github.com/OmarAlghafri/netrewind/internal/registry"
)

func testEvent(id, kind string, tsWall int64, attrs map[string]any) map[string]any {
	e := map[string]any{"event_id": id, "kind": kind, "ts_wall": tsWall}
	if attrs != nil {
		e["attrs"] = attrs
	}
	return e
}

func testIncident(ruleID, rootKind, rootEntity, rootEventID string, confidence uint8, chain []incident.Link) *incident.Incident {
	return &incident.Incident{
		ID: "inc-1", OpenedAt: 1000 * int64(1e9), Confidence: confidence, RuleID: ruleID,
		RootCause: incident.RootCause{Kind: event.Kind(rootKind), Entity: rootEntity, EventID: rootEventID, Confidence: confidence},
		Chain:     chain,
	}
}

func TestGuardrailProceedsUncappedWithNoIncident(t *testing.T) {
	res := Guardrail(Request{})
	if res.Refuse {
		t.Error("a request with no incident at all was refused; a general question must be answerable")
	}
	if res.Ceiling != 100 {
		t.Errorf("ceiling = %d, want 100 for no incident", res.Ceiling)
	}
}

func TestGuardrailRefusesBlindnessRuleIDs(t *testing.T) {
	// Deliberately NOT a system.gap/system.drop/system.collector_down root
	// cause: the real blind_gap/collector_down corpus incidents do use
	// those kinds as their own root cause, so rules 4/5 (gap/drop/
	// collector-down in window) independently refuse them too, which would
	// mask a broken rule-3 check entirely. An ordinary kind here isolates
	// rule 3 on its own - proven necessary when this exact mistake let a
	// disabled rule 3 pass unnoticed (caught and fixed during review).
	for _, ruleID := range []string{"recorder-was-blind", "collector-not-watching"} {
		t.Run(ruleID, func(t *testing.T) {
			inc := testIncident(ruleID, "l2.arp_binding_changed", "Dell", "e1", 100, []incident.Link{{EventID: "e1", Kind: "l2.arp_binding_changed", At: 1000 * int64(1e9)}})
			res := Guardrail(Request{Incident: inc, Events: []map[string]any{testEvent("e1", "l2.arp_binding_changed", 1000*int64(1e9), nil)}})
			if !res.Refuse {
				t.Errorf("rule_id %s was not refused", ruleID)
			}
			if !hasReasonCode(res.Reasons, "insufficient_evidence") {
				t.Errorf("reasons = %+v, want insufficient_evidence", res.Reasons)
			}
			if res.Ceiling != 100 {
				t.Errorf("ceiling = %d, want the incident's own confidence (100) even though refused", res.Ceiling)
			}
		})
	}
}

func TestGuardrailRefusesGapInWindow(t *testing.T) {
	opened := int64(1000) * int64(1e9)
	inc := testIncident("gateway-hijack", "l2.arp_binding_changed", "10.0.0.1", "e-root", 80,
		[]incident.Link{{EventID: "e-root", Kind: "l2.arp_binding_changed", At: opened}})
	events := []map[string]any{
		testEvent("e-root", "l2.arp_binding_changed", opened, nil),
		testEvent("e-gap", "system.gap", opened+30*int64(1e9), nil), // 30s after open, inside the ±60s pad
	}
	res := Guardrail(Request{Incident: inc, Events: events})
	if !res.Refuse {
		t.Error("a system.gap inside the incident's window was not refused")
	}
	if !hasReasonCode(res.Reasons, "gap_in_window") {
		t.Errorf("reasons = %+v, want gap_in_window", res.Reasons)
	}
}

func TestGuardrailRefusesDropInWindow(t *testing.T) {
	opened := int64(1000) * int64(1e9)
	inc := testIncident("gateway-hijack", "l2.arp_binding_changed", "10.0.0.1", "e-root", 80,
		[]incident.Link{{EventID: "e-root", Kind: "l2.arp_binding_changed", At: opened}})
	events := []map[string]any{
		testEvent("e-root", "l2.arp_binding_changed", opened, nil),
		testEvent("e-drop", "system.drop", opened-40*int64(1e9), nil), // 40s before open, inside pad
	}
	res := Guardrail(Request{Incident: inc, Events: events})
	if !res.Refuse || !hasReasonCode(res.Reasons, "drop_in_window") {
		t.Errorf("a system.drop just before the window was not refused with drop_in_window: %+v", res)
	}
}

func TestGuardrailDoesNotRefuseAGapFarOutsideTheWindow(t *testing.T) {
	opened := int64(1000) * int64(1e9)
	inc := testIncident("gateway-hijack", "l2.arp_binding_changed", "10.0.0.1", "e-root", 80,
		[]incident.Link{{EventID: "e-root", Kind: "l2.arp_binding_changed", At: opened}})
	events := []map[string]any{
		testEvent("e-root", "l2.arp_binding_changed", opened, nil),
		testEvent("e-gap", "system.gap", opened-3600*int64(1e9), nil), // an hour before - well outside the pad
	}
	res := Guardrail(Request{Incident: inc, Events: events})
	if res.Refuse {
		t.Errorf("a gap an hour outside the window incorrectly refused the incident: %+v", res)
	}
}

func TestGuardrailRefusesCollectorDownInWindow(t *testing.T) {
	opened := int64(1000) * int64(1e9)
	inc := testIncident("gateway-hijack", "l2.arp_binding_changed", "10.0.0.1", "e-root", 80,
		[]incident.Link{{EventID: "e-root", Kind: "l2.arp_binding_changed", At: opened}})
	events := []map[string]any{
		testEvent("e-root", "l2.arp_binding_changed", opened, nil),
		testEvent("e-down", "system.collector_down", opened+10*int64(1e9), map[string]any{"collector": "netlink.neigh"}),
	}
	res := Guardrail(Request{Incident: inc, Events: events})
	if !res.Refuse || !hasReasonCode(res.Reasons, "collector_down_in_window") {
		t.Errorf("a collector_down inside the window was not refused: %+v", res)
	}
}

func TestGuardrailRefusesWhenARequiredCollectorIsCurrentlyDown(t *testing.T) {
	opened := int64(1000) * int64(1e9)
	inc := testIncident("gateway-hijack", "l2.arp_binding_changed", "10.0.0.1", "e-root", 80,
		[]incident.Link{{EventID: "e-root", Kind: "l2.arp_binding_changed", At: opened}})
	events := []map[string]any{testEvent("e-root", "l2.arp_binding_changed", opened, nil)}
	caps := []registry.Snapshot{
		{Descriptor: registry.Descriptor{Name: "netlink.neigh", Coverage: []string{"l2.arp_binding_changed", "l2.mac_moved"}}, Status: registry.StatusDown},
	}
	res := Guardrail(Request{Incident: inc, Events: events, Capabilities: caps})
	if !res.Refuse || !hasReasonCode(res.Reasons, "required_collector_down") {
		t.Errorf("a down collector covering the root cause kind was not refused: %+v", res)
	}
}

func TestGuardrailProceedsWithBlindFamiliesForAnUnrelatedDownCollector(t *testing.T) {
	opened := int64(1000) * int64(1e9)
	inc := testIncident("gateway-hijack", "l2.arp_binding_changed", "10.0.0.1", "e-root", 80,
		[]incident.Link{{EventID: "e-root", Kind: "l2.arp_binding_changed", At: opened}})
	events := []map[string]any{testEvent("e-root", "l2.arp_binding_changed", opened, nil)}
	caps := []registry.Snapshot{
		{Descriptor: registry.Descriptor{Name: "ebpf.flow", Coverage: []string{"flow.*"}}, Status: registry.StatusDown},
	}
	res := Guardrail(Request{Incident: inc, Events: events, Capabilities: caps})
	if res.Refuse {
		t.Errorf("an unrelated down collector (flow.*, incident is l2) incorrectly caused a refusal: %+v", res)
	}
	if len(res.BlindFamilies) != 1 || res.BlindFamilies[0] != "flow" {
		t.Errorf("BlindFamilies = %v, want [flow]", res.BlindFamilies)
	}
}

func TestGuardrailIgnoresCapabilitiesWhenNil(t *testing.T) {
	opened := int64(1000) * int64(1e9)
	inc := testIncident("gateway-hijack", "l2.arp_binding_changed", "10.0.0.1", "e-root", 80,
		[]incident.Link{{EventID: "e-root", Kind: "l2.arp_binding_changed", At: opened}})
	res := Guardrail(Request{Incident: inc, Events: []map[string]any{testEvent("e-root", "l2.arp_binding_changed", opened, nil)}, Capabilities: nil})
	if res.Refuse {
		t.Errorf("nil Capabilities (unknown, as in eval mode) incorrectly triggered a capabilities-based refusal: %+v", res)
	}
}

func TestGuardrailRefusesAnEmptyChainOrZeroConfidence(t *testing.T) {
	empty := testIncident("some-rule", "l2.arp_binding_changed", "e", "e1", 80, nil)
	if res := Guardrail(Request{Incident: empty, Events: []map[string]any{testEvent("e1", "l2.arp_binding_changed", 0, nil)}}); !res.Refuse || !hasReasonCode(res.Reasons, "no_chain") {
		t.Errorf("an empty chain was not refused with no_chain: %+v", res)
	}
	zero := testIncident("some-rule", "l2.arp_binding_changed", "e", "e1", 0, []incident.Link{{EventID: "e1", Kind: "l2.arp_binding_changed"}})
	if res := Guardrail(Request{Incident: zero, Events: []map[string]any{testEvent("e1", "l2.arp_binding_changed", 0, nil)}}); !res.Refuse || !hasReasonCode(res.Reasons, "no_chain") {
		t.Errorf("zero confidence was not refused with no_chain: %+v", res)
	}
}

func TestMissingEvidenceReportsRootCauseAndChainGaps(t *testing.T) {
	inc := testIncident("r", "k", "e", "e-root", 80, []incident.Link{
		{EventID: "e-root", Kind: "k"}, {EventID: "e-chain-2", Kind: "k"},
	})
	missing := MissingEvidence(Request{Incident: inc, Events: []map[string]any{testEvent("e-root", "k", 0, nil)}})
	if len(missing) != 1 || missing[0] != "e-chain-2" {
		t.Errorf("MissingEvidence = %v, want [e-chain-2]", missing)
	}
	if got := MissingEvidence(Request{Incident: nil}); got != nil {
		t.Errorf("MissingEvidence with no incident = %v, want nil", got)
	}
}

func hasReasonCode(reasons []Reason, code string) bool {
	for _, r := range reasons {
		if r.Code == code {
			return true
		}
	}
	return false
}

/* ------------------------------------------------------------------ */
/* Corpus agreement: the same rule_id-based refusals the eval corpus's */
/* own generator (ai/eval/gen/main.go) already encodes must still hold, */
/* and the ceiling must still match the deterministic engine's own      */
/* confidence, now that Guardrail also runs the newer gap/drop/         */
/* collector-down/no-chain checks over the same real lab data.          */
/* ------------------------------------------------------------------ */

func repoRootForTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatalf("could not find repo root (go.mod) from %s", dir)
	return ""
}

func readTestJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

func TestGuardrailAgreesWithTheRealCorpus(t *testing.T) {
	root := repoRootForTest(t)
	casesDir := filepath.Join(root, "ai", "eval", "cases")
	entries, err := os.ReadDir(casesDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no eval cases found; is ai/eval/cases populated?")
	}

	checked := 0
	for _, entry := range entries {
		var c schema.Case
		readTestJSON(t, filepath.Join(casesDir, entry.Name()), &c)

		eventsPath := filepath.Join(root, "corpus", "v1", c.Scenario, "events.json")
		if c.EventsOverride != "" {
			eventsPath = filepath.Join(root, filepath.FromSlash(c.EventsOverride))
		}
		var rawEvents []map[string]any
		readTestJSON(t, eventsPath, &rawEvents)

		var inc *incident.Incident
		if c.Expected.RuleID != "" {
			var incidents []incident.Incident
			readTestJSON(t, filepath.Join(root, "corpus", "v1", c.Scenario, "incidents.json"), &incidents)
			for i := range incidents {
				if incidents[i].RuleID == c.Expected.RuleID {
					inc = &incidents[i]
					break
				}
			}
			if inc == nil {
				t.Fatalf("%s: no incident with rule_id %q in %s/incidents.json", c.ID, c.Expected.RuleID, c.Scenario)
			}
		}

		req := Request{Incident: inc, Events: rawEvents}
		if missing := MissingEvidence(req); len(missing) != 0 {
			t.Errorf("%s: MissingEvidence found real corpus data incomplete: %v", c.ID, missing)
		}

		res := Guardrail(req)
		wantRefuse := blindnessRuleIDs[c.Expected.RuleID]
		if res.Refuse != wantRefuse {
			t.Errorf("%s: Guardrail.Refuse = %v, want %v (rule_id %q) - reasons: %+v",
				c.ID, res.Refuse, wantRefuse, c.Expected.RuleID, res.Reasons)
		}
		if inc != nil && res.Ceiling != c.Expected.MaxConfidence {
			t.Errorf("%s: Ceiling = %d, want %d (the incident's own confidence)", c.ID, res.Ceiling, c.Expected.MaxConfidence)
		}
		checked++
	}
	if checked != 37 {
		t.Errorf("checked %d cases, want the full 37-case corpus (a case was skipped or the corpus grew without updating this test)", checked)
	}
}
