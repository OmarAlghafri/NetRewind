package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/identity"
	"github.com/OmarAlghafri/netrewind/internal/incident"
	"github.com/OmarAlghafri/netrewind/internal/store"
)

func newTestServer(t *testing.T) (*Server, *store.SQLite) {
	t.Helper()
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	s, err := New(st, slog.New(slog.NewTextHandler(io.Discard, nil)), "test-observer")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s, st
}

func get(t *testing.T, s *Server, path string) (*http.Response, string) {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	return w.Result(), w.Body.String()
}

func seedEvent(t *testing.T, st *store.SQLite, kind event.Kind, sev event.Severity, subject string, at time.Time, attrs map[string]any) *event.Event {
	t.Helper()
	b := event.NewBuilder("test-observer", nil)
	e := b.New(event.SourceNetlink, kind, sev, event.Host(subject, ""))
	e.TSWall = at.UnixNano()
	for k, v := range attrs {
		e.WithAttr(k, v)
	}
	if err := st.Append(context.Background(), e); err != nil {
		t.Fatalf("Append: %v", err)
	}
	return e
}

func TestEveryPageRenders(t *testing.T) {
	s, _ := newTestServer(t)
	for _, path := range []string{"/", "/incidents", "/timeline", "/host", "/healthz"} {
		res, body := get(t, s, path)
		if res.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d", path, res.StatusCode)
		}
		if path != "/healthz" && !strings.Contains(body, "test-observer") {
			t.Errorf("GET %s did not render the observer name", path)
		}
	}
}

// An empty window must not read as a clean bill of health.
func TestAnEmptyWindowSaysWhatItDoesNotKnow(t *testing.T) {
	s, _ := newTestServer(t)
	_, body := get(t, s, "/timeline")

	if !strings.Contains(body, "Nothing was recorded") {
		t.Error("an empty timeline did not say so plainly")
	}
	// Compared with whitespace collapsed: a sentence that wraps in the template
	// is still the same sentence, and a test that cares where the line breaks
	// fall would make the templates unformattable.
	if !strings.Contains(flat(body), "No record is not the same as nothing happening") {
		t.Errorf("an empty timeline reads as a clean bill of health:\n%s", excerpt(body))
	}
}

// flat collapses runs of whitespace so prose can be asserted on without the
// test caring how the template is wrapped.
func flat(s string) string { return strings.Join(strings.Fields(s), " ") }

// The banner has to be on every page, above everything else. Making the reader
// remember to check for holes guarantees that one day nobody does.
func TestGapsAreSurfacedOnEveryPage(t *testing.T) {
	s, st := newTestServer(t)
	seedEvent(t, st, event.KindSystemGap, event.SevWarn, "test-observer", time.Now().Add(-5*time.Minute),
		map[string]any{"gap_duration_ms": 42000})

	for _, path := range []string{"/", "/incidents", "/timeline", "/host"} {
		_, body := get(t, s, path)
		if !strings.Contains(body, "The record has 1 hole in this window") {
			t.Errorf("GET %s does not surface the gap", path)
		}
		if !strings.Contains(body, "not watching for 42s") {
			t.Errorf("GET %s does not say how long the recorder was blind", path)
		}
	}
}

func TestEventsAppearWithTheirNarrationAndMarker(t *testing.T) {
	s, st := newTestServer(t)
	seedEvent(t, st, event.KindLinkDown, event.SevWarn, "eth1", time.Now().Add(-time.Minute),
		map[string]any{"cause": "administrative"})

	_, body := get(t, s, "/timeline")
	if !strings.Contains(body, "was shut down administratively") {
		t.Errorf("the event was not narrated:\n%s", excerpt(body))
	}
	if !strings.Contains(body, "link.down") {
		t.Error("the event kind is not shown")
	}
	if !strings.Contains(body, "sev-warn") {
		t.Error("severity is not carried into the markup")
	}
}

// The relation between two links is the engine's central claim, and rendering
// two of them the same way would let a reader assume causation everywhere.
func TestTheThreeRelationsRenderDifferently(t *testing.T) {
	s, st := newTestServer(t)
	now := time.Now()

	inc := &incident.Incident{
		ID: "01WEBINCIDENT00000000000000", OpenedAt: now.Add(-2 * time.Minute).UnixNano(),
		ClosedAt: now.UnixNano(), Status: incident.StatusClosed,
		Title: "A path that was working stopped working", Severity: event.SevError,
		Confidence: 88, RuleID: "change-broke-a-path",
		RootCause: incident.RootCause{
			Kind: event.KindARPBindingChanged, Entity: "10.99.1.11", Confidence: 88,
		},
		Chain: []incident.Link{
			{Seq: 0, EventID: "e1", Kind: event.KindARPBindingChanged, At: now.Add(-2 * time.Minute).UnixNano(),
				Subject: "10.99.1.11", Why: "the last thing that changed"},
			{Seq: 1, EventID: "e2", Kind: event.KindFlowFirstFailureForPair, At: now.Add(-time.Minute).UnixNano(),
				Subject: "10.99.1.11", Relation: incident.RelCauses, Why: "they can no longer connect"},
			{Seq: 2, EventID: "e3", Kind: event.KindNeighborFailed, At: now.UnixNano(),
				Subject: "10.99.1.11", Relation: incident.RelCorrelates, Why: "others failed too"},
		},
		Victims: []string{"10.99.1.11"},
		Advice:  "confirm against your change record",
	}
	if err := st.AppendIncidents(context.Background(), inc); err != nil {
		t.Fatalf("AppendIncidents: %v", err)
	}

	_, body := get(t, s, "/incidents")

	for _, want := range []string{
		"which caused",         // the causal claim, spelled out
		"and at the same time", // co-occurrence, spelled out
		"rel-causes",           // and given different lines
		"rel-correlates",
		"change-broke-a-path",
		"confidence 88%",
		"confirm against your change record",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("incident page is missing %q", want)
		}
	}
	// Nothing may shorten a non-causal relation into an arrow.
	if strings.Contains(body, "&rarr;</span>") {
		t.Error("a relation was rendered as an arrow")
	}
}

// Asking about an address has to find events recorded while the machine was on
// a different one - that is the whole point of the identity table.
func TestFollowingAHostSpansItsAddresses(t *testing.T) {
	s, st := newTestServer(t)
	ctx := context.Background()
	now := time.Now()

	// One machine, two addresses over time, bound through its hardware address.
	if err := st.Open(ctx, bindingFor("host-1", "mac", "02:00:00:00:00:11", now.Add(-time.Hour))); err != nil {
		t.Fatal(err)
	}
	if err := st.Open(ctx, bindingFor("host-1", "ipv4", "10.0.0.5", now.Add(-time.Hour))); err != nil {
		t.Fatal(err)
	}
	if err := st.Open(ctx, bindingFor("host-1", "ipv4", "10.0.0.9", now.Add(-30*time.Minute))); err != nil {
		t.Fatal(err)
	}

	seedEvent(t, st, event.KindARPBindingNew, event.SevInfo, "10.0.0.5", now.Add(-50*time.Minute), nil)
	seedEvent(t, st, event.KindNeighborFailed, event.SevNotice, "10.0.0.9", now.Add(-5*time.Minute), nil)

	_, body := get(t, s, "/host?q=10.0.0.9&window=6h")
	if !strings.Contains(body, "stopped answering") {
		t.Errorf("the event on the current address is missing:\n%s", excerpt(body))
	}
	if !strings.Contains(body, "first answered") {
		t.Error("an event recorded while the machine was on its previous address was not found")
	}
}

func TestWindowIsClampedAndDefaulted(t *testing.T) {
	cases := map[string]time.Duration{
		"":         time.Hour,
		"nonsense": time.Hour,
		"-5m":      time.Hour,
		"0s":       time.Hour,
		"15m":      15 * time.Minute,
		"9000h":    maxWindow, // a typo must not become a scan of the whole store
	}
	for in, want := range cases {
		if got := parseWindow(in); got != want {
			t.Errorf("parseWindow(%q) = %v, want %v", in, got, want)
		}
	}
}

// The store is evidence. Nothing here may change it, and nothing here may be
// reached by a method that implies it would.
func TestOnlyReadsAreRouted(t *testing.T) {
	s, _ := newTestServer(t)
	for _, method := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		req := httptest.NewRequest(method, "/timeline", nil)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		if w.Code == http.StatusOK {
			t.Errorf("%s /timeline was accepted", method)
		}
	}
}

func TestPagesAreNotCachedAndNotIndexed(t *testing.T) {
	s, _ := newTestServer(t)
	res, body := get(t, s, "/timeline")

	if got := res.Header.Get("Cache-Control"); got != "no-store" {
		// A cached timeline is a misleading timeline.
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if !strings.Contains(body, `name="robots" content="noindex, nofollow"`) {
		t.Error("the interface does not tell crawlers to stay out")
	}
}

func TestStaticAssetsAreServedFromTheBinary(t *testing.T) {
	s, _ := newTestServer(t)
	res, body := get(t, s, "/static/style.css")

	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /static/style.css = %d", res.StatusCode)
	}
	if !strings.Contains(body, "--ground") {
		t.Error("the stylesheet was not the embedded one")
	}
	// No web font: the appliance may sit on a segment with no route out, and a
	// page that waits on a font CDN is a page that does not render mid-outage.
	if strings.Contains(body, "fonts.googleapis") || strings.Contains(body, "@import url(") {
		t.Error("the stylesheet reaches for an external resource")
	}
}

func excerpt(s string) string {
	if len(s) > 900 {
		return s[:900] + "..."
	}
	return s
}

// bindingFor builds an identity binding, so the host page can be tested against
// a machine that has genuinely changed address.
func bindingFor(hostID, attrType, value string, from time.Time) identity.Binding {
	return identity.Binding{
		HostID: hostID, AttrType: attrType, AttrValue: value,
		ValidFrom: from.UnixNano(), Confidence: 90,
	}
}
