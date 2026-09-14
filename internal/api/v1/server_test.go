package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/incident"
	"github.com/OmarAlghafri/netrewind/internal/registry"
	"github.com/OmarAlghafri/netrewind/internal/store"
)

func newTestServer(t *testing.T) (*Server, store.Store) {
	t.Helper()
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	reg := registry.New(nil)
	reg.Register(registry.Descriptor{Name: "netlink.link", Platform: "linux", Privilege: "CAP_NET_ADMIN", Coverage: []string{"link.*"}})
	reg.Up("netlink.link")
	return &Server{Store: st, Registry: reg}, st
}

func seedEvent(t *testing.T, st store.Store, kind event.Kind, when time.Time) {
	t.Helper()
	b := event.NewBuilder("obs-1", nil)
	e := b.New(event.SourceNetlink, kind, event.SevWarn, event.Iface("eth0", 2))
	e.TSWall = when.UnixNano() // backdated deliberately, to test window filtering
	if err := st.Append(context.Background(), e); err != nil {
		t.Fatalf("seed event: %v", err)
	}
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec
}

func TestHealthReportsOK(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := get(t, srv.Handler(), "/v1/health")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct{ Status string }
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, want ok", body.Status)
	}
}

func TestCapabilitiesReflectsTheRegistry(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := get(t, srv.Handler(), "/v1/capabilities")
	var body capabilitiesResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Capabilities) != 1 || body.Capabilities[0].Name != "netlink.link" {
		t.Fatalf("capabilities = %+v, want one entry for netlink.link", body.Capabilities)
	}
	if body.Capabilities[0].Status != registry.StatusUp {
		t.Errorf("status = %s, want up", body.Capabilities[0].Status)
	}
}

func TestEventsWithinWindow(t *testing.T) {
	srv, st := newTestServer(t)
	now := time.Now()
	seedEvent(t, st, event.KindLinkDown, now.Add(-30*time.Minute))
	seedEvent(t, st, event.KindLinkUp, now.Add(-3*time.Hour)) // outside the default 1h window

	rec := get(t, srv.Handler(), "/v1/events")
	var body eventsResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Events) != 1 || body.Events[0].Kind != event.KindLinkDown {
		t.Fatalf("events = %+v, want exactly the one inside the default window", body.Events)
	}
}

func TestEventsFilteredByKind(t *testing.T) {
	srv, st := newTestServer(t)
	now := time.Now()
	seedEvent(t, st, event.KindLinkDown, now.Add(-time.Minute))
	seedEvent(t, st, event.KindLinkUp, now.Add(-time.Minute))

	rec := get(t, srv.Handler(), "/v1/events?kind=link.down")
	var body eventsResponse
	json.NewDecoder(rec.Body).Decode(&body)
	if len(body.Events) != 1 || body.Events[0].Kind != event.KindLinkDown {
		t.Fatalf("events = %+v, want only link.down", body.Events)
	}
}

func TestEventsRespectExplicitSinceAndUntil(t *testing.T) {
	srv, st := newTestServer(t)
	target := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	seedEvent(t, st, event.KindLinkDown, target)
	seedEvent(t, st, event.KindLinkUp, target.Add(48*time.Hour))

	since := target.Add(-time.Minute).Format(time.RFC3339)
	until := target.Add(time.Minute).Format(time.RFC3339)
	rec := get(t, srv.Handler(), "/v1/events?since="+since+"&until="+until)
	var body eventsResponse
	json.NewDecoder(rec.Body).Decode(&body)
	if len(body.Events) != 1 || body.Events[0].Kind != event.KindLinkDown {
		t.Fatalf("events = %+v, want only the one inside the explicit window", body.Events)
	}
}

func TestEventsRejectsAMalformedSince(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := get(t, srv.Handler(), "/v1/events?since=not-a-timestamp")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var body errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "bad_param" || body.Error.Message == "" {
		t.Errorf("error = %+v, want a typed bad_param error naming the reason", body.Error)
	}
}

func TestEventsRejectsANonPositiveLimit(t *testing.T) {
	srv, _ := newTestServer(t)
	for _, limit := range []string{"0", "-1", "abc"} {
		rec := get(t, srv.Handler(), "/v1/events?limit="+limit)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("limit=%s: status = %d, want 400", limit, rec.Code)
		}
	}
}

func TestIncidentsFilteredByRule(t *testing.T) {
	srv, st := newTestServer(t)
	inc := &incident.Incident{
		ID: "01TESTINCIDENT00000000000", OpenedAt: time.Now().UnixNano(),
		Status: incident.StatusClosed, Title: "t", Severity: event.SevWarn,
		RootCause: incident.RootCause{Kind: "link.down", Entity: "eth0"}, RuleID: "carrier-lost",
	}
	if err := st.AppendIncidents(context.Background(), inc); err != nil {
		t.Fatal(err)
	}

	var matched incidentsResponse
	if err := json.NewDecoder(get(t, srv.Handler(), "/v1/incidents?rule=carrier-lost").Body).Decode(&matched); err != nil {
		t.Fatal(err)
	}
	if len(matched.Incidents) != 1 {
		t.Fatalf("incidents = %+v, want 1", matched.Incidents)
	}

	var unmatched incidentsResponse
	if err := json.NewDecoder(get(t, srv.Handler(), "/v1/incidents?rule=no-such-rule").Body).Decode(&unmatched); err != nil {
		t.Fatal(err)
	}
	if len(unmatched.Incidents) != 0 {
		t.Fatalf("incidents = %+v, want none for an unmatched rule", unmatched.Incidents)
	}
}

func TestEmptyResultsAreAnEmptyArrayNotNull(t *testing.T) {
	// A client's JSON decoder should never have to distinguish "no events"
	// from "the events field was missing" - both must be []·, never null.
	srv, _ := newTestServer(t)
	rec := get(t, srv.Handler(), "/v1/events")
	if got := rec.Body.String(); !jsonHasEmptyArray(got, "events") {
		t.Errorf("body = %s, want an empty array for events, not null", got)
	}
}

func jsonHasEmptyArray(body, key string) bool {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		return false
	}
	return string(m[key]) == "[]"
}

func TestWriteMethodsAreNotRouted(t *testing.T) {
	srv, _ := newTestServer(t)
	for _, method := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		for _, path := range []string{"/v1/events", "/v1/incidents", "/v1/health", "/v1/capabilities"} {
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, httptest.NewRequest(method, path, nil))
			if rec.Code == http.StatusOK {
				t.Errorf("%s %s was served; the API must be read-only", method, path)
			}
		}
	}
}

func TestCapabilitiesWithNoRegistryStillAnswers(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.Registry = nil
	rec := get(t, srv.Handler(), "/v1/capabilities")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 even with no registry configured", rec.Code)
	}
}
