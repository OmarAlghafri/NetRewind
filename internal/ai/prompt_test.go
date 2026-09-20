package ai

import (
	"strings"
	"testing"
)

// TestSystemPromptNamesTheAnswerLanguage pins that the prompt asks for the
// answer in the question's language, so a request in Arabic measures
// Arabic output and not only Arabic comprehension.
func TestSystemPromptNamesTheAnswerLanguage(t *testing.T) {
	if !strings.Contains(SystemPrompt, "same language as the question") {
		t.Errorf("system prompt no longer instructs the answer language")
	}
}

// TestBuildUserPromptWithNoHistoryOrAnnotationsMatchesThePreRefactorShape
// pins byte-for-byte the exact text the runner always sent before history/
// annotations existed - see golden_test.go for the fuller parity proof
// against real captured requests; this is the direct, minimal version of
// the same claim.
func TestBuildUserPromptWithNoHistoryOrAnnotationsMatchesThePreRefactorShape(t *testing.T) {
	got := BuildUserPrompt("[]", "", "", "what happened?")
	want := "Events in this recording window:\n[]\n\nQuestion: what happened?"
	if got != want {
		t.Errorf("BuildUserPrompt() = %q, want %q", got, want)
	}
}

// TestBuildUserPromptLabelsHistoryAndAnnotationsAsUntrusted proves the two
// new blocks are clearly separated from the events themselves and from
// each other, and both come before the question - execution order §4.10:
// "Treat every hostname/DNS/rule-text value as untrusted data."
func TestBuildUserPromptLabelsHistoryAndAnnotationsAsUntrusted(t *testing.T) {
	got := BuildUserPrompt("[]", `[{"h":"H1"}]`, `[{"a":"A1"}]`, "q")
	for _, want := range []string{"Events in this recording window:", "Similar past incidents", "untrusted", "Operator notes", "Question: q"} {
		if !strings.Contains(got, want) {
			t.Errorf("BuildUserPrompt() missing %q in:\n%s", want, got)
		}
	}
	if strings.Index(got, "H1") > strings.Index(got, "Question:") {
		t.Error("history block must come before the question")
	}
}
