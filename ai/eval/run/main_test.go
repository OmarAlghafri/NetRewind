package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestExtractModelOutputIgnoresNestedBraces is a regression test for a real
// bug found while reading docs/evidence/20-ai-eval-first-benchmark.log's raw
// transcripts by hand: the first version of extractModelOutput used
// strings.LastIndex(raw, "{"), which finds the LAST '{' character anywhere -
// including one nested inside the model's own ranked_hypotheses array - and
// silently extracted just that small nested object instead of the real
// top-level answer. It never returned an error (the nested object is valid
// JSON, just the wrong shape), so every case in that first run was graded
// against an accidentally-empty ModelOutput regardless of what the model
// actually said.
func TestExtractModelOutputIgnoresNestedBraces(t *testing.T) {
	raw := `some banner text {"ignored": "echoed event object, not the answer"} more noise
{
  "summary": "a real answer",
  "ranked_hypotheses": [
    {"cause": "l2.arp_binding_changed", "entity": "10.99.0.201", "confidence": 90}
  ],
  "evidence_event_ids": ["ev1"],
  "counter_evidence": [],
  "unknowns": [],
  "confidence_ceiling": 90,
  "next_checks": []
}
[ Prompt: 1.0 t/s ]`

	out, err := extractModelOutput(raw)
	if err != nil {
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
	if _, err := extractModelOutput("no braces here at all"); err == nil {
		t.Error("expected an error when no JSON object is present")
	}
}

// TestBuildDeidentifyTableMatchesGeneratorOrder proves this runner's table
// construction produces the identical <HOST_N> assignment ai/eval/gen would
// for the same real scenario - built from the actual gateway_hijack corpus
// events (a real, singular 10.99.0.201 address), not synthetic data. If
// this ever disagreed with gen/main.go's own deidentify table, a
// "-deidentified" case's context would show a DIFFERENT placeholder than
// the one already baked into that case's QuestionEn/Expected.RootCauseEntity,
// silently breaking every deidentified case's grading.
func TestBuildDeidentifyTableMatchesGeneratorOrder(t *testing.T) {
	events := []map[string]any{
		{"subject": map[string]any{"kind": "observer", "label": "Dell"}},
		{"subject": map[string]any{"kind": "host", "label": "10.99.0.201"}},
		{"subject": map[string]any{"kind": "host", "label": "10.99.0.201"}}, // repeat: must not get a second placeholder
		{"subject": map[string]any{"kind": "host", "label": "10.99.1.11"}},
		{"nosubject": true}, // malformed/missing subject must not panic
	}
	table := buildDeidentifyTable(events)
	if table["10.99.0.201"] != "<HOST_1>" {
		t.Errorf("10.99.0.201 = %q, want <HOST_1> (first-seen order)", table["10.99.0.201"])
	}
	if table["10.99.1.11"] != "<HOST_2>" {
		t.Errorf("10.99.1.11 = %q, want <HOST_2>", table["10.99.1.11"])
	}
	if len(table) != 2 {
		t.Errorf("table has %d entries, want 2 (Dell is not a 10.99.x.x address and must not be table)", len(table))
	}

	text := `event about 10.99.0.201 and again 10.99.0.201, also 10.99.1.11`
	got := deidentify(text, table)
	want := `event about <HOST_1> and again <HOST_1>, also <HOST_2>`
	if got != want {
		t.Errorf("deidentify() = %q, want %q", got, want)
	}
}

func TestExtractModelOutputHandlesStrayClosingBrace(t *testing.T) {
	raw := `} {"summary": "ok", "ranked_hypotheses": [], "evidence_event_ids": [], "counter_evidence": [], "unknowns": ["x"], "confidence_ceiling": 0, "next_checks": []}`
	out, err := extractModelOutput(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Summary != "ok" {
		t.Errorf("Summary = %q, want %q", out.Summary, "ok")
	}
}

// TestRequestDisablesThePromptCache pins the reproducibility fix: every
// request tells llama-server not to serve the prompt from its KV cache, so
// a rerun of the same case evaluates the same tokens the same way.
func TestRequestDisablesThePromptCache(t *testing.T) {
	body, err := json.Marshal(chatCompletionRequest{Temperature: 0, MaxTokens: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"cache_prompt":false`) {
		t.Errorf("request body = %s, want cache_prompt:false present", body)
	}
}

// TestSystemPromptNamesTheAnswerLanguage pins that the prompt asks for the
// answer in the question's language, so -lang ar measures Arabic output
// and not only Arabic comprehension.
func TestSystemPromptNamesTheAnswerLanguage(t *testing.T) {
	if !strings.Contains(systemPrompt, "same language as the question") {
		t.Errorf("system prompt no longer instructs the answer language")
	}
}
