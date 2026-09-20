package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/incident"
)

// fakeLlamaServer answers /v1/chat/completions with a canned sequence of
// assistant message bodies, one per call - the same shape a real
// llama-server response takes, so Analyze exercises its real HTTP/JSON
// path rather than a mocked-out client.
func fakeLlamaServer(t *testing.T, bodies ...string) *httptest.Server {
	t.Helper()
	call := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if call >= len(bodies) {
			t.Fatalf("fakeLlamaServer received more requests (%d) than canned bodies (%d)", call+1, len(bodies))
		}
		content := bodies[call]
		call++
		resp := map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": content}}},
			"timings": map[string]float64{"prompt_ms": 10, "predicted_ms": 20},
		}
		json.NewEncoder(w).Encode(resp)
	}))
}

func testAnalyzeIncident() *incident.Incident {
	return &incident.Incident{
		ID: "inc-1", RuleID: "gateway-hijack", OpenedAt: 1000 * int64(1e9), Confidence: 75,
		RootCause: incident.RootCause{Kind: event.Kind("l2.arp_binding_changed"), Entity: "10.0.0.1", EventID: "e-root"},
		Chain:     []incident.Link{{EventID: "e-root", Kind: "l2.arp_binding_changed", At: 1000 * int64(1e9)}},
	}
}

func testAnalyzeEvents() []map[string]any {
	return []map[string]any{{"event_id": "e-root", "kind": "l2.arp_binding_changed", "ts_wall": 1000 * int64(1e9)}}
}

func TestAnalyzeReturnsInsufficientEvidenceWithoutCallingTheModel(t *testing.T) {
	srv := fakeLlamaServer(t) // zero canned bodies - a call would fail the test
	defer srv.Close()

	inc := testAnalyzeIncident()
	inc.RuleID = "recorder-was-blind"
	req := Request{Incident: inc, Events: testAnalyzeEvents()}

	result, err := Analyze(context.Background(), NewHTTPClient(5*time.Second), srv.URL, req, nil, "what happened?", "en", Policy{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if result.Verdict != VerdictInsufficientEvidence {
		t.Errorf("verdict = %s, want insufficient_evidence", result.Verdict)
	}
	if !result.Guardrail.Refuse {
		t.Error("Guardrail.Refuse = false, want true")
	}
}

func TestAnalyzeReturnsAnsweredForACleanFirstAttempt(t *testing.T) {
	clean := `{"summary":"The address changed hands, based on E1.","ranked_hypotheses":[{"cause":"l2.arp_binding_changed","entity":"10.0.0.1","confidence":70}],"evidence_handles":["E1"],"counter_evidence":[],"unknowns":[],"confidence_ceiling":70,"next_checks":["Check the switch port history for E1."]}`
	srv := fakeLlamaServer(t, clean)
	defer srv.Close()

	req := Request{Incident: testAnalyzeIncident(), Events: testAnalyzeEvents()}
	result, err := Analyze(context.Background(), NewHTTPClient(5*time.Second), srv.URL, req, nil, "what happened?", "en", Policy{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if result.Verdict != VerdictAnswered {
		t.Fatalf("verdict = %s, want answered - violations: %+v", result.Verdict, result.Violations)
	}
	if !result.FirstAttemptValid || result.Retried {
		t.Errorf("FirstAttemptValid=%v Retried=%v, want true/false for a clean first attempt", result.FirstAttemptValid, result.Retried)
	}
	if result.Guardrail.Ceiling != 75 {
		t.Errorf("Ceiling = %d, want the incident's own confidence 75", result.Guardrail.Ceiling)
	}
}

func TestAnalyzeRetriesOnceOnAnUnknownHandleThenSucceeds(t *testing.T) {
	bad := `{"summary":"S","ranked_hypotheses":[{"cause":"l2.arp_binding_changed","entity":"10.0.0.1","confidence":70}],"evidence_handles":["E1","E99"],"counter_evidence":[],"unknowns":[],"confidence_ceiling":70,"next_checks":[]}`
	fixed := `{"summary":"S","ranked_hypotheses":[{"cause":"l2.arp_binding_changed","entity":"10.0.0.1","confidence":70}],"evidence_handles":["E1"],"counter_evidence":[],"unknowns":[],"confidence_ceiling":70,"next_checks":[]}`
	srv := fakeLlamaServer(t, bad, fixed)
	defer srv.Close()

	req := Request{Incident: testAnalyzeIncident(), Events: testAnalyzeEvents()}
	result, err := Analyze(context.Background(), NewHTTPClient(5*time.Second), srv.URL, req, nil, "what happened?", "en", Policy{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if !result.Retried {
		t.Error("Retried = false, want true (unknown_handle is retry-eligible)")
	}
	if result.FirstAttemptValid {
		t.Error("FirstAttemptValid = true, want false (the first attempt cited an unoffered handle)")
	}
	if result.Verdict != VerdictAnswered {
		t.Fatalf("verdict = %s, want answered after the retry fixed it - violations: %+v", result.Verdict, result.Violations)
	}
}

// TestAnalyzeDoesNotRetryOnANonRetryEligibleViolation proves the "exactly
// one retry, only on S/H/C" rule: a command-syntax violation (not
// retry-eligible) must fail closed on the first attempt rather than
// spending a second, useless inference on content a resend cannot fix.
func TestAnalyzeDoesNotRetryOnANonRetryEligibleViolation(t *testing.T) {
	commandInjection := `{"summary":"Run ping -t 10.0.0.1 to confirm.","ranked_hypotheses":[{"cause":"l2.arp_binding_changed","entity":"10.0.0.1","confidence":70}],"evidence_handles":["E1"],"counter_evidence":[],"unknowns":[],"confidence_ceiling":70,"next_checks":[]}`
	srv := fakeLlamaServer(t, commandInjection) // a second call would fail the test
	defer srv.Close()

	req := Request{Incident: testAnalyzeIncident(), Events: testAnalyzeEvents()}
	result, err := Analyze(context.Background(), NewHTTPClient(5*time.Second), srv.URL, req, nil, "what happened?", "en", Policy{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if result.Retried {
		t.Error("Retried = true, want false (command_syntax is not retry-eligible)")
	}
	if result.Verdict != VerdictInvalid {
		t.Fatalf("verdict = %s, want invalid", result.Verdict)
	}
}

func TestAnalyzeReturnsRefusedByModelForAProperRefusal(t *testing.T) {
	refusal := `{"summary":"","ranked_hypotheses":[],"evidence_handles":[],"counter_evidence":[],"unknowns":["The recorder has no coverage for this window."],"confidence_ceiling":0,"next_checks":[]}`
	srv := fakeLlamaServer(t, refusal)
	defer srv.Close()

	req := Request{Incident: testAnalyzeIncident(), Events: testAnalyzeEvents()}
	result, err := Analyze(context.Background(), NewHTTPClient(5*time.Second), srv.URL, req, nil, "what happened?", "en", Policy{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if result.Verdict != VerdictRefusedByModel {
		t.Fatalf("verdict = %s, want refused_by_model - violations: %+v", result.Verdict, result.Violations)
	}
}

func TestAnalyzeRejectsARequestMissingChainEvidenceBeforeCallingTheModel(t *testing.T) {
	srv := fakeLlamaServer(t) // zero canned bodies - a call would fail the test
	defer srv.Close()

	req := Request{Incident: testAnalyzeIncident(), Events: nil} // e-root never offered
	_, err := Analyze(context.Background(), NewHTTPClient(5*time.Second), srv.URL, req, nil, "what happened?", "en", Policy{})
	if err == nil {
		t.Fatal("a request missing its own chain event was accepted")
	}
	var invalid *InvalidRequestError
	if !errors.As(err, &invalid) {
		t.Fatalf("err = %v (%T), want an *InvalidRequestError a caller can distinguish from a network failure", err, err)
	}
	if len(invalid.MissingEventIDs) != 1 || invalid.MissingEventIDs[0] != "e-root" {
		t.Errorf("MissingEventIDs = %v, want [e-root]", invalid.MissingEventIDs)
	}
}

// TestAnalyzeOffersRankedHistoryAsHandles proves RankSimilar's output
// actually reaches the model as H-handles, end to end.
func TestAnalyzeOffersRankedHistoryAsHandles(t *testing.T) {
	prior := &incident.Incident{
		ID: "inc-old", RuleID: "gateway-hijack", OpenedAt: 500 * int64(1e9), Confidence: 75,
		RootCause: incident.RootCause{Kind: event.Kind("l2.arp_binding_changed"), Entity: "10.0.0.1"},
	}
	answer := `{"summary":"Similar to H1.","ranked_hypotheses":[{"cause":"l2.arp_binding_changed","entity":"10.0.0.1","confidence":70}],"evidence_handles":["E1","H1"],"counter_evidence":[],"unknowns":[],"confidence_ceiling":70,"next_checks":[]}`
	srv := fakeLlamaServer(t, answer)
	defer srv.Close()

	req := Request{Incident: testAnalyzeIncident(), Events: testAnalyzeEvents(), History: []*incident.Incident{prior}}
	result, err := Analyze(context.Background(), NewHTTPClient(5*time.Second), srv.URL, req, nil, "what happened?", "en", Policy{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(result.History) != 1 || result.History[0].ID != "inc-old" {
		t.Fatalf("History = %v, want [inc-old]", result.History)
	}
	if result.Handles.Kind("H1") != "history" {
		t.Errorf("H1 was not assigned as a history handle: %+v", result.Handles)
	}
	if result.Verdict != VerdictAnswered {
		t.Fatalf("verdict = %s, want answered (H1 was validly offered) - violations: %+v", result.Verdict, result.Violations)
	}
}
