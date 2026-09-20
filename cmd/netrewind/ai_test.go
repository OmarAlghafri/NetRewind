package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func runWithStdin(t *testing.T, stdin string, args ...string) (stdout string, err error) {
	t.Helper()
	var out bytes.Buffer
	cmd := newRootCmd()
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), err
}

func fakeLlamaServerForCLI(t *testing.T, content string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": content}}},
			"timings": map[string]float64{"prompt_ms": 5, "predicted_ms": 15},
		}
		json.NewEncoder(w).Encode(resp)
	}))
}

func analyzeRequestJSON(t *testing.T, serverURL string) string {
	t.Helper()
	req := map[string]any{
		"incident": map[string]any{
			"incident_id": "inc-1", "opened_at": 1000000000000, "confidence": 75, "rule_id": "gateway-hijack",
			"root_cause": map[string]any{"kind": "l2.arp_binding_changed", "entity": "10.0.0.1", "event_id": "e-root", "confidence": 75},
			"chain":      []map[string]any{{"event_id": "e-root", "kind": "l2.arp_binding_changed", "at": 1000000000000}},
		},
		"events":   []map[string]any{{"event_id": "e-root", "kind": "l2.arp_binding_changed", "ts_wall": 1000000000000}},
		"question": "what happened?", "lang": "en",
		"server": map[string]any{"url": serverURL},
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAIAnalyzeCommandReturnsAnsweredForACleanAnswer(t *testing.T) {
	clean := `{"summary":"The address changed hands, based on E1.","ranked_hypotheses":[{"cause":"l2.arp_binding_changed","entity":"10.0.0.1","confidence":70}],"evidence_handles":["E1"],"counter_evidence":[],"unknowns":[],"confidence_ceiling":70,"next_checks":[]}`
	srv := fakeLlamaServerForCLI(t, clean)
	defer srv.Close()

	out, err := runWithStdin(t, analyzeRequestJSON(t, srv.URL), "ai", "analyze")
	if err != nil {
		t.Fatalf("ai analyze: %v\n%s", err, out)
	}
	var resp aiAnalyzeResponse
	if jerr := json.Unmarshal([]byte(out), &resp); jerr != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", jerr, out)
	}
	if resp.Verdict != "answered" {
		t.Errorf("verdict = %s, want answered", resp.Verdict)
	}
	// err is already nil here (checked above) - main() only calls
	// exitCodeFor inside `if err != nil`, so nil error is itself the "exit
	// 0" case, not a value to pass through exitCodeFor.
}

func TestAIAnalyzeCommandExitsThreeOnMalformedStdin(t *testing.T) {
	_, err := runWithStdin(t, "not json", "ai", "analyze")
	if err == nil {
		t.Fatal("malformed stdin was accepted")
	}
	if code := exitCodeFor(err); code != 3 {
		t.Errorf("exit code = %d, want 3 for a malformed request", code)
	}
}

func TestAIAnalyzeCommandExitsThreeWhenServerURLIsMissing(t *testing.T) {
	_, err := runWithStdin(t, `{"question":"x"}`, "ai", "analyze")
	if err == nil {
		t.Fatal("a request with no server.url was accepted")
	}
	if code := exitCodeFor(err); code != 3 {
		t.Errorf("exit code = %d, want 3", code)
	}
}

func TestAIAnalyzeCommandExitsFourWhenServerIsUnreachable(t *testing.T) {
	_, err := runWithStdin(t, analyzeRequestJSON(t, "http://127.0.0.1:1"), "ai", "analyze")
	if err == nil {
		t.Fatal("an unreachable server was not reported as an error")
	}
	if code := exitCodeFor(err); code != 4 {
		t.Errorf("exit code = %d, want 4 for an unreachable server", code)
	}
}

func TestAIAnalyzeCommandExitsTwoOnAnInvalidAnswer(t *testing.T) {
	commandInjection := `{"summary":"Run ping -t 10.0.0.1 to confirm.","ranked_hypotheses":[{"cause":"l2.arp_binding_changed","entity":"10.0.0.1","confidence":70}],"evidence_handles":["E1"],"counter_evidence":[],"unknowns":[],"confidence_ceiling":70,"next_checks":[]}`
	srv := fakeLlamaServerForCLI(t, commandInjection)
	defer srv.Close()

	out, err := runWithStdin(t, analyzeRequestJSON(t, srv.URL), "ai", "analyze")
	if err == nil {
		t.Fatal("an answer with a command_syntax violation was accepted")
	}
	if code := exitCodeFor(err); code != 2 {
		t.Errorf("exit code = %d, want 2 for an answer that never passed validation", code)
	}
	// The response body is still printed - a caller can inspect why.
	var resp aiAnalyzeResponse
	if jerr := json.Unmarshal([]byte(out), &resp); jerr != nil {
		t.Fatalf("output is not valid JSON even though it should still print the failed result: %v\n%s", jerr, out)
	}
	if resp.Verdict != "invalid" || len(resp.Validation.Violations) == 0 {
		t.Errorf("resp = %+v, want verdict invalid with violations listed", resp)
	}
}

func TestAIAnalyzeCommandReturnsInsufficientEvidenceWithoutCallingTheModel(t *testing.T) {
	req := map[string]any{
		"incident": map[string]any{
			"incident_id": "inc-1", "opened_at": 1000000000000, "confidence": 100, "rule_id": "recorder-was-blind",
			"root_cause": map[string]any{"kind": "system.gap", "entity": "Dell", "event_id": "e-root", "confidence": 100},
			"chain":      []map[string]any{{"event_id": "e-root", "kind": "system.gap", "at": 1000000000000}},
		},
		"events":   []map[string]any{{"event_id": "e-root", "kind": "system.gap", "ts_wall": 1000000000000}},
		"question": "what happened?", "lang": "en",
		"server": map[string]any{"url": "http://127.0.0.1:1"}, // must never be dialed
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}

	out, runErr := runWithStdin(t, string(b), "ai", "analyze")
	if runErr != nil {
		t.Fatalf("ai analyze: %v\n%s", runErr, out)
	}
	var resp aiAnalyzeResponse
	if jerr := json.Unmarshal([]byte(out), &resp); jerr != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", jerr, out)
	}
	if resp.Verdict != "insufficient_evidence" {
		t.Errorf("verdict = %s, want insufficient_evidence", resp.Verdict)
	}
	// runErr == nil (checked above) is itself "exit 0": insufficient_evidence
	// is a correct outcome, not a failure the CLI reports as one.
}
