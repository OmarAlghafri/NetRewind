package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/OmarAlghafri/netrewind/internal/notes"
	"github.com/OmarAlghafri/netrewind/internal/store"
)

func newTestServerWithNotes(t *testing.T) (*Server, notes.Store) {
	t.Helper()
	srv, _ := newTestServer(t)
	n, err := notes.OpenSQLite(filepath.Join(t.TempDir(), "notes.db"))
	if err != nil {
		t.Fatalf("notes.OpenSQLite: %v", err)
	}
	t.Cleanup(func() { n.Close() })
	srv.Notes = n
	return srv, n
}

func doJSON(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r = httptest.NewRequest(method, path, bytes.NewReader(b))
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

// TestNotesRoutesAreAbsentWhenNotesIsNil is the "disabled by default"
// half of the design: a daemon that never configures notes must not expose
// the surface at all, not merely refuse writes to it.
func TestNotesRoutesAreAbsentWhenNotesIsNil(t *testing.T) {
	srv, _ := newTestServer(t) // Notes left nil
	rec := doJSON(t, srv.Handler(), "GET", "/v1/notes/incidents/x", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (Go's ServeMux default for an unregistered pattern) when Notes is nil", rec.Code)
	}
}

// TestNotesHandlersNeverTouchTheRecord is the pin the plan requires:
// every /v1/notes/* request must be answerable, and must actually work
// end-to-end, on a Server whose Store field would panic on first use -
// proving the notes code path genuinely never dereferences it, not merely
// that nobody happened to call it in these particular tests.
func TestNotesHandlersNeverTouchTheRecord(t *testing.T) {
	_, n := newTestServerWithNotes(t)
	srv := &Server{Store: panicOnUseStore{}, Notes: n}

	// A full read/write/delete cycle across every notes route, against a
	// Store that panics if any handler ever calls one of its methods.
	rec := doJSON(t, srv.Handler(), "PUT", "/v1/notes/incidents/inc-1", notesPutRequest{
		RuleID: "gateway-hijack", RootCauseKind: "l2.arp_binding_changed", RootCauseEntity: "10.99.0.1",
		Outcome: notes.OutcomeConfirmed,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body = %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, srv.Handler(), "GET", "/v1/notes/incidents/inc-1", nil); rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body = %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, srv.Handler(), "GET", "/v1/notes/similar?rule_id=gateway-hijack&root_cause_kind=l2.arp_binding_changed&entity=10.99.0.1", nil); rec.Code != http.StatusOK {
		t.Fatalf("similar status = %d, body = %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, srv.Handler(), "POST", "/v1/notes/feedback", notesFeedbackRequest{AnswerID: "a1", IncidentID: "inc-1", Helpful: true}); rec.Code != http.StatusNoContent {
		t.Fatalf("feedback status = %d, body = %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, srv.Handler(), "PUT", "/v1/notes/settings", map[string]bool{"history_opt_in": true}); rec.Code != http.StatusNoContent {
		t.Fatalf("settings PUT status = %d, body = %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, srv.Handler(), "POST", "/v1/notes/threads/inc-1", notesThreadAppendRequest{AnswerID: "a1", QuestionRedacted: "q", SummaryRedacted: "s"}); rec.Code != http.StatusNoContent {
		t.Fatalf("thread append status = %d, body = %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, srv.Handler(), "GET", "/v1/notes/threads/inc-1", nil); rec.Code != http.StatusOK {
		t.Fatalf("thread get status = %d, body = %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, srv.Handler(), "DELETE", "/v1/notes/incidents/inc-1", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete one status = %d, body = %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, srv.Handler(), "DELETE", "/v1/notes", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("forget all status = %d, body = %s", rec.Code, rec.Body)
	}
	// If any handler had called srv.Store, the panic below would have
	// already failed this test with a stack trace naming the offending
	// call, rather than a plain assertion failure - that is deliberate.
}

// panicOnUseStore implements store.Store by panicking on every method -
// see TestNotesHandlersNeverTouchTheRecord.
type panicOnUseStore struct{ store.Store }

func TestNotesPutRejectsAnInvalidOutcome(t *testing.T) {
	srv, _ := newTestServerWithNotes(t)
	rec := doJSON(t, srv.Handler(), "PUT", "/v1/notes/incidents/x", notesPutRequest{
		RuleID: "r", RootCauseKind: "k", Outcome: "not-a-real-outcome",
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for an invalid outcome", rec.Code)
	}
}

func TestNotesPutRejectsAnOversizedBody(t *testing.T) {
	srv, _ := newTestServerWithNotes(t)
	huge := notesPutRequest{RuleID: "r", RootCauseKind: "k", Outcome: notes.OutcomeConfirmed, CauseNote: string(make([]byte, notesMaxBody+1))}
	rec := doJSON(t, srv.Handler(), "PUT", "/v1/notes/incidents/x", huge)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a body over notesMaxBody", rec.Code)
	}
}

func TestNotesGetForUnknownIncidentReturns404(t *testing.T) {
	srv, _ := newTestServerWithNotes(t)
	rec := doJSON(t, srv.Handler(), "GET", "/v1/notes/incidents/never-annotated", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// TestNotesThreadAppendRefusesWithoutOptIn proves the API surfaces
// notes.ErrHistoryDisabled as a real, distinguishable error rather than a
// generic 500 or - worse - silently succeeding.
func TestNotesThreadAppendRefusesWithoutOptIn(t *testing.T) {
	srv, _ := newTestServerWithNotes(t)
	rec := doJSON(t, srv.Handler(), "POST", "/v1/notes/threads/inc-1", notesThreadAppendRequest{AnswerID: "a", QuestionRedacted: "q", SummaryRedacted: "s"})
	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 when history is not opted into", rec.Code)
	}
	var body errorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "history_disabled" {
		t.Errorf("error code = %q, want history_disabled", body.Error.Code)
	}
}

// TestNotesThreadAppendRefusesWhenOperatorPolicyDisablesThreads proves the
// server-level notes.threads override (config's notes.threads: false) is
// enforced even when the store's own per-installation history opt-in is on.
func TestNotesThreadAppendRefusesWhenOperatorPolicyDisablesThreads(t *testing.T) {
	srv, n := newTestServerWithNotes(t)
	if err := n.SetHistoryOptIn(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	srv.NotesThreadsDisabled = true
	rec := doJSON(t, srv.Handler(), "POST", "/v1/notes/threads/inc-1", notesThreadAppendRequest{AnswerID: "a", QuestionRedacted: "q", SummaryRedacted: "s"})
	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 when the operator policy disables threads even though history_opt_in is on", rec.Code)
	}
}

// TestNotesWriteMethodsWorkOnlyOnTheNotesNamespace re-confirms, from this
// file, that the record's own write-refusal (TestWriteMethodsAreNotRouted
// in server_test.go) is untouched by adding these routes: a POST to
// /v1/events must still fail even though POST now succeeds elsewhere.
func TestNotesWriteMethodsWorkOnlyOnTheNotesNamespace(t *testing.T) {
	srv, _ := newTestServerWithNotes(t)
	rec := doJSON(t, srv.Handler(), "POST", "/v1/events", nil)
	if rec.Code == http.StatusOK {
		t.Error("POST /v1/events succeeded; the record must stay read-only even with notes enabled")
	}
}
