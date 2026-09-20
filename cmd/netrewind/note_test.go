package main

import (
	"context"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	apiv1 "github.com/OmarAlghafri/netrewind/internal/api/v1"
	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/incident"
	"github.com/OmarAlghafri/netrewind/internal/ipc"
	"github.com/OmarAlghafri/netrewind/internal/notes"
	"github.com/OmarAlghafri/netrewind/internal/store"
	"github.com/oklog/ulid/v2"
)

// testEndpoint picks a unique local-IPC address for one test: a temp-file
// socket path on Linux, a uniquely-named pipe on Windows (ipc.ListenWith
// accepts either directly - see internal/ipc's own tests for the same
// per-platform split).
func testEndpoint(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return `\\.\pipe\netrewind-cli-test-` + strings.ReplaceAll(t.Name(), "/", "-")
	}
	return filepath.Join(t.TempDir(), "netrewind.sock")
}

// startTestAPI serves srv over a fresh local IPC endpoint and returns it,
// so the CLI commands under test talk to a real listener exactly like a
// running recorder, rather than a fake HTTP transport.
func startTestAPI(t *testing.T, srv *apiv1.Server) string {
	t.Helper()
	endpoint := testEndpoint(t)
	l, err := ipc.ListenWith(endpoint, ipc.Options{})
	if err != nil {
		t.Fatalf("ListenWith: %v", err)
	}
	httpSrv := &http.Server{Handler: srv.Handler()}
	go httpSrv.Serve(l)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		httpSrv.Shutdown(ctx)
	})
	return endpoint
}

// seedIncident writes one incident straight to a fresh event store and
// returns its id - enough for newNoteCmd to resolve the rule/root-cause
// fields it sends to the API.
func seedIncident(t *testing.T, dbPath, ruleID, kind, entity string) string {
	t.Helper()
	st, err := store.OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	id := ulid.Make().String()
	inc := &incident.Incident{
		ID: id, OpenedAt: time.Now().UnixNano(), Status: incident.StatusClosed,
		Title: "test incident", RuleID: ruleID,
		RootCause: incident.RootCause{Kind: event.Kind(kind), Entity: entity, Confidence: 80},
	}
	if err := st.AppendIncidents(context.Background(), inc); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestNoteRoundTripsThroughTheAPI(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "events.db")
	id := seedIncident(t, dbPath, "gateway-hijack", "l2.arp_binding_changed", "10.99.0.1")

	n, err := notes.OpenSQLite(filepath.Join(t.TempDir(), "notes.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	endpoint := startTestAPI(t, &apiv1.Server{Notes: n})

	out, err := run(t, "note", id, "--db", dbPath, "--endpoint", endpoint,
		"--outcome", "confirmed", "--cause", "bad switch port")
	if err != nil {
		t.Fatalf("note: %v\n%s", err, out)
	}
	if !strings.Contains(out, "confirmed") {
		t.Errorf("output does not confirm the outcome:\n%s", out)
	}

	got, err := n.GetAnnotation(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("no annotation was stored")
	}
	if got.Outcome != notes.OutcomeConfirmed {
		t.Errorf("outcome = %q, want confirmed", got.Outcome)
	}
	if got.RuleID != "gateway-hijack" || got.RootCauseKind != "l2.arp_binding_changed" || got.RootCauseEntity != "10.99.0.1" {
		t.Errorf("the note was not filed against the incident's own rule/root-cause: %+v", got)
	}
	if got.CauseNote != "bad switch port" {
		t.Errorf("cause_note = %q, want the flag's value", got.CauseNote)
	}
}

func TestNoteRejectsAnInvalidOutcome(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "events.db")
	id := seedIncident(t, dbPath, "r", "k", "e")

	_, err := run(t, "note", id, "--db", dbPath, "--outcome", "maybe")
	if err == nil {
		t.Fatal("an invalid --outcome was accepted")
	}
	if !strings.Contains(err.Error(), "confirmed, false-positive, or unresolved") {
		t.Errorf("the error does not show the valid choices: %v", err)
	}
}

func TestNoteFailsWhenTheIncidentDoesNotExist(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "events.db")
	seedIncident(t, dbPath, "r", "k", "e") // some other incident, unrelated

	_, err := run(t, "note", ulid.Make().String(), "--db", dbPath, "--outcome", "confirmed")
	if err == nil {
		t.Fatal("a note for a nonexistent incident id was accepted")
	}
	if !strings.Contains(err.Error(), "no incident with id") {
		t.Errorf("the error does not explain the problem: %v", err)
	}
}

func TestNotesListsSimilarIncidentsAndRequiresRuleAndKind(t *testing.T) {
	n, err := notes.OpenSQLite(filepath.Join(t.TempDir(), "notes.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	if err := n.PutAnnotation(context.Background(), notes.Annotation{
		IncidentID: "inc-old", RuleID: "gateway-hijack", RootCauseKind: "l2.arp_binding_changed",
		RootCauseEntity: "10.99.0.1", Outcome: notes.OutcomeConfirmed, CauseNote: "same switch as before",
	}); err != nil {
		t.Fatal(err)
	}
	endpoint := startTestAPI(t, &apiv1.Server{Notes: n})

	out, err := run(t, "notes", "--endpoint", endpoint,
		"--rule", "gateway-hijack", "--kind", "l2.arp_binding_changed", "--entity", "10.99.0.1")
	if err != nil {
		t.Fatalf("notes: %v\n%s", err, out)
	}
	if !strings.Contains(out, "inc-old") || !strings.Contains(out, "same switch as before") {
		t.Errorf("the listing does not show the prior note:\n%s", out)
	}

	if _, err := run(t, "notes", "--endpoint", endpoint, "--entity", "10.99.0.1"); err == nil {
		t.Error("notes without --rule and --kind was accepted")
	}
}
