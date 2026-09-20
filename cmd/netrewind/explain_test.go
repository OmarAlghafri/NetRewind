package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apiv1 "github.com/OmarAlghafri/netrewind/internal/api/v1"
	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/incident"
	"github.com/OmarAlghafri/netrewind/internal/notes"
	"github.com/OmarAlghafri/netrewind/internal/store"
)

func fakeLlamaServerForExplain(t *testing.T, content string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": content}}},
			"timings": map[string]float64{"prompt_ms": 5, "predicted_ms": 15},
		}
		json.NewEncoder(w).Encode(resp)
	}))
}

// seedIncidentWithEvent writes one incident whose sole chain link is a
// real event in the store, mirroring what correlation actually produces -
// unlike seedIncident (note_test.go), this leaves the underlying event
// present too, since findIncidentByID + explainWindowEvents both need it.
func seedIncidentWithEvent(t *testing.T, dbPath string) (incidentID, eventID string) {
	t.Helper()
	st, err := store.OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	b := event.NewBuilder("obs-1", nil)
	now := time.Now()
	e := b.New(event.SourceNetlink, event.KindARPBindingChanged, event.SevError,
		event.Host("10.0.0.1", "aa:bb:cc:dd:ee:ff"))
	e.TSWall = now.UnixNano()
	if err := st.Append(context.Background(), e); err != nil {
		t.Fatal(err)
	}

	incID := "01M2EXPLAINTESTINCIDENT01"
	inc := &incident.Incident{
		ID: incID, OpenedAt: e.TSWall, ClosedAt: e.TSWall, Status: incident.StatusClosed,
		Title: "test incident", RuleID: "gateway-hijack", Confidence: 75,
		RootCause: incident.RootCause{Kind: event.KindARPBindingChanged, Entity: "10.0.0.1", EventID: e.ID, Confidence: 75},
		Chain:     []incident.Link{{EventID: e.ID, Kind: event.KindARPBindingChanged, At: e.TSWall}},
	}
	if err := st.AppendIncidents(context.Background(), inc); err != nil {
		t.Fatal(err)
	}
	return incID, e.ID
}

func TestExplainPrintsAnAnsweredHypothesis(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "events.db")
	incID, _ := seedIncidentWithEvent(t, dbPath)

	answer := `{"summary":"The address changed hands.","ranked_hypotheses":[{"cause":"l2.arp_binding_changed","entity":"10.0.0.1","confidence":70}],"evidence_handles":["E1"],"counter_evidence":[],"unknowns":[],"confidence_ceiling":70,"next_checks":["Check the switch port."]}`
	srv := fakeLlamaServerForExplain(t, answer)
	defer srv.Close()

	out, err := run(t, "explain", incID, "--db", dbPath, "--server", srv.URL)
	if err != nil {
		t.Fatalf("explain: %v\n%s", err, out)
	}
	if !strings.Contains(out, "hypothesis") {
		t.Errorf("output does not carry the hypothesis-not-evidence banner:\n%s", out)
	}
	if !strings.Contains(out, "changed hands") {
		t.Errorf("output does not include the model's summary:\n%s", out)
	}
	if !strings.Contains(out, "Check the switch port") {
		t.Errorf("output does not include the suggested next check:\n%s", out)
	}
}

// TestExplainRequiresAServerFlag checks the exact upfront message, not
// just "some error happened": an empty --server still reaches
// ai.Analyze and fails there too (an empty URL is not a valid request),
// which would make a looser "err != nil" assertion pass even if the
// deliberate early check were deleted - a clean, immediate message beats
// a confusing HTTP-layer failure several calls later.
func TestExplainRequiresAServerFlag(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "events.db")
	incID, _ := seedIncidentWithEvent(t, dbPath)

	_, err := run(t, "explain", incID, "--db", dbPath)
	if err == nil {
		t.Fatal("explain without --server was accepted")
	}
	if !strings.Contains(err.Error(), "--server is required") {
		t.Errorf("err = %v, want the explicit --server-is-required message, not a downstream failure", err)
	}
}

func TestExplainReportsInsufficientEvidenceWithoutCallingAnUnreachableServer(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "events.db")
	st, err := store.OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	b := event.NewBuilder("obs-1", nil)
	now := time.Now()
	e := b.New(event.SourceInternal, event.KindSystemGap, event.SevWarn, event.Observer("obs-1"))
	e.TSWall = now.UnixNano()
	if err := st.Append(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	incID := "01M2EXPLAINBLINDTESTINC01"
	inc := &incident.Incident{
		ID: incID, OpenedAt: e.TSWall, ClosedAt: e.TSWall, Status: incident.StatusClosed,
		RuleID: "recorder-was-blind", Confidence: 100,
		RootCause: incident.RootCause{Kind: event.KindSystemGap, Entity: "obs-1", EventID: e.ID, Confidence: 100},
		Chain:     []incident.Link{{EventID: e.ID, Kind: event.KindSystemGap, At: e.TSWall}},
	}
	if err := st.AppendIncidents(context.Background(), inc); err != nil {
		t.Fatal(err)
	}
	st.Close()

	// A server address nobody is listening on - if Guardrail's refusal
	// were broken, this would hang or error instead of answering cleanly.
	out, err := run(t, "explain", incID, "--db", dbPath, "--server", "http://127.0.0.1:1", "--timeout", "2s")
	if err != nil {
		t.Fatalf("explain: %v\n%s", err, out)
	}
	if !strings.Contains(out, "not enough evidence") {
		t.Errorf("output does not report insufficient evidence:\n%s", out)
	}
}

func TestExplainIncludesTheIncidentsOwnOperatorNoteWhenReachable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "events.db")
	incID, _ := seedIncidentWithEvent(t, dbPath)

	n, err := notes.OpenSQLite(filepath.Join(t.TempDir(), "notes.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	if err := n.PutAnnotation(context.Background(), notes.Annotation{
		IncidentID: incID, RuleID: "gateway-hijack", RootCauseKind: "l2.arp_binding_changed",
		RootCauseEntity: "10.0.0.1", Outcome: notes.OutcomeConfirmed, CauseNote: "known bad switch port",
	}); err != nil {
		t.Fatal(err)
	}
	endpoint := startTestAPI(t, &apiv1.Server{Notes: n})

	// The model echoes the annotation text back in its summary, proving
	// the annotation actually reached the prompt as an A-handle.
	answer := `{"summary":"Consistent with A1: known bad switch port.","ranked_hypotheses":[{"cause":"l2.arp_binding_changed","entity":"10.0.0.1","confidence":70}],"evidence_handles":["E1","A1"],"counter_evidence":[],"unknowns":[],"confidence_ceiling":70,"next_checks":[]}`
	srv := fakeLlamaServerForExplain(t, answer)
	defer srv.Close()

	out, err := run(t, "explain", incID, "--db", dbPath, "--server", srv.URL, "--endpoint", endpoint)
	if err != nil {
		t.Fatalf("explain: %v\n%s", err, out)
	}
	if !strings.Contains(out, "known bad switch port") {
		t.Errorf("output does not reflect the operator's own note:\n%s", out)
	}
}
