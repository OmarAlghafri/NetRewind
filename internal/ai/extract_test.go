package ai

import "testing"

// TestExtractModelOutputIgnoresNestedBraces is a regression test for a real
// bug found while reading docs/evidence/20-ai-eval-first-benchmark.log's raw
// transcripts by hand: the first version of this extractor used
// strings.LastIndex(raw, "{"), which finds the LAST '{' character anywhere -
// including one nested inside the model's own ranked_hypotheses array - and
// silently extracted just that small nested object instead of the real
// top-level answer. It never returned an error (the nested object is valid
// JSON, just the wrong shape), so every case in that first run was graded
// against an accidentally-empty output regardless of what the model
// actually said.
func TestExtractModelOutputIgnoresNestedBraces(t *testing.T) {
	raw := `some banner text {"ignored": "echoed event object, not the answer"} more noise
{
  "summary": "a real answer",
  "ranked_hypotheses": [
    {"cause": "l2.arp_binding_changed", "entity": "10.99.0.201", "confidence": 90}
  ],
  "evidence_handles": ["E1"],
  "counter_evidence": [],
  "unknowns": [],
  "confidence_ceiling": 90,
  "next_checks": []
}
[ Prompt: 1.0 t/s ]`

	var out struct {
		Summary          string `json:"summary"`
		RankedHypotheses []struct {
			Cause string `json:"cause"`
		} `json:"ranked_hypotheses"`
		ConfidenceCeiling int `json:"confidence_ceiling"`
	}
	if err := ExtractModelOutput(raw, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Summary != "a real answer" {
		t.Errorf("Summary = %q, want %q (the extractor picked the wrong JSON object)", out.Summary, "a real answer")
	}
	if len(out.RankedHypotheses) != 1 || out.RankedHypotheses[0].Cause != "l2.arp_binding_changed" {
		t.Errorf("RankedHypotheses = %+v, want one l2.arp_binding_changed entry", out.RankedHypotheses)
	}
	if out.ConfidenceCeiling != 90 {
		t.Errorf("ConfidenceCeiling = %d, want 90", out.ConfidenceCeiling)
	}
}

func TestExtractModelOutputNoObjectFound(t *testing.T) {
	var out map[string]any
	if err := ExtractModelOutput("no braces here at all", &out); err == nil {
		t.Error("expected an error when no JSON object is present")
	}
}

func TestExtractModelOutputHandlesStrayClosingBrace(t *testing.T) {
	raw := `} {"summary": "ok", "ranked_hypotheses": [], "evidence_handles": [], "counter_evidence": [], "unknowns": ["x"], "confidence_ceiling": 0, "next_checks": []}`
	var out struct {
		Summary string `json:"summary"`
	}
	if err := ExtractModelOutput(raw, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Summary != "ok" {
		t.Errorf("Summary = %q, want %q", out.Summary, "ok")
	}
}
