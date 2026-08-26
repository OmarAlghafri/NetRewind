package otel

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/incident"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func sampleEvent() *event.Event {
	b := event.NewBuilder("recorder-1", nil)
	e := b.New(event.SourceNetlink, event.KindARPBindingChanged, event.SevError,
		event.Host("10.1.10.1", "c2:03:12:2c:00:01"))
	e.TSWall = 1787589873180757237 // a real nanosecond timestamp, deliberately large
	e.WithAttr("ip", "10.1.10.1").
		WithAttr("is_gateway", true).
		WithAttr("changes_in_window", 3).
		WithEvidence("nud_state", "reachable")
	return e
}

/* ------------------------------------------------------------------ */
/* Encoding                                                           */
/* ------------------------------------------------------------------ */

func TestThePayloadHasTheShapeOTLPRequires(t *testing.T) {
	body, err := Encode("recorder-1", "0.8.0", []*event.Event{sampleEvent()}, nil)
	if err != nil {
		t.Fatal(err)
	}

	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("the payload is not valid JSON: %v", err)
	}

	rl, ok := doc["resourceLogs"].([]any)
	if !ok || len(rl) != 1 {
		t.Fatalf("resourceLogs missing or wrong: %v", doc)
	}
	first := rl[0].(map[string]any)
	if _, ok := first["resource"]; !ok {
		t.Error("no resource, so the receiver cannot attribute these records to anything")
	}
	sl, ok := first["scopeLogs"].([]any)
	if !ok || len(sl) != 1 {
		t.Fatalf("scopeLogs missing or wrong")
	}
	records, ok := sl[0].(map[string]any)["logRecords"].([]any)
	if !ok || len(records) != 1 {
		t.Fatalf("expected one log record, got %v", sl[0])
	}
}

// A nanosecond timestamp does not survive being a JSON number: it is a uint64,
// and most decoders make numbers float64, which silently rounds it. The
// specification allows a decimal string for this reason and this checks the
// exact value comes back.
func TestNanosecondTimestampsSurviveTheEncoding(t *testing.T) {
	e := sampleEvent()
	body, err := Encode("recorder-1", "0.8.0", []*event.Event{e}, nil)
	if err != nil {
		t.Fatal(err)
	}

	rec := firstRecord(t, body)
	got, ok := rec["timeUnixNano"].(string)
	if !ok {
		t.Fatalf("timeUnixNano is %T, not a string; large values would round", rec["timeUnixNano"])
	}
	if got != strconv.FormatInt(e.TSWall, 10) {
		t.Errorf("timeUnixNano = %s, want %d", got, e.TSWall)
	}
	// The round trip a receiver would do.
	back, err := strconv.ParseInt(got, 10, 64)
	if err != nil || back != e.TSWall {
		t.Errorf("the timestamp does not survive a round trip: %v %v", back, err)
	}
}

func TestSeverityMapsOntoTheLogsDataModel(t *testing.T) {
	cases := []struct {
		sev  event.Severity
		want int
	}{
		{event.SevInfo, 9},
		{event.SevNotice, 11},
		{event.SevWarn, 13},
		{event.SevError, 17},
	}
	for _, tc := range cases {
		if got := severityNumber(tc.sev); got != tc.want {
			t.Errorf("severityNumber(%s) = %d, want %d", tc.sev, got, tc.want)
		}
	}
}

func TestAttributesKeepTheirTypes(t *testing.T) {
	body, err := Encode("recorder-1", "0.8.0", []*event.Event{sampleEvent()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	attrs := attrsOf(firstRecord(t, body))

	if v := attrs["netrewind.attr.is_gateway"]; v == nil {
		t.Fatal("is_gateway is missing")
	} else if _, ok := v.(map[string]any)["boolValue"]; !ok {
		t.Errorf("is_gateway is not a boolValue: %v", v)
	}
	if v := attrs["netrewind.attr.changes_in_window"]; v == nil {
		t.Fatal("changes_in_window is missing")
	} else if _, ok := v.(map[string]any)["intValue"]; !ok {
		t.Errorf("a count was not exported as intValue: %v", v)
	}
	if attrs["netrewind.evidence.nud_state"] == nil {
		t.Error("evidence was dropped; it is the reason to believe the event")
	}
}

// The same attribute arriving from a live collector and from the store must
// land on the same wire type, or a query that works on one breaks on the other.
func TestNumbersFromTheStoreEncodeLikeNumbersFromACollector(t *testing.T) {
	live := value(3)
	replayed := value(json.Number("3"))
	if live.Int == nil || replayed.Int == nil {
		t.Fatalf("one of them is not an int: live=%+v replayed=%+v", live, replayed)
	}
	if *live.Int != *replayed.Int {
		t.Errorf("live %s vs replayed %s", *live.Int, *replayed.Int)
	}
	// And a float that happens to be whole is still a count.
	if whole := value(float64(3)); whole.Int == nil || *whole.Int != "3" {
		t.Errorf("a whole float was not exported as an integer: %+v", whole)
	}
}

func TestAFoldedEventCarriesItsOccurrenceCount(t *testing.T) {
	e := sampleEvent()
	e.Count = 47
	body, err := Encode("recorder-1", "0.8.0", []*event.Event{e}, nil)
	if err != nil {
		t.Fatal(err)
	}
	attrs := attrsOf(firstRecord(t, body))
	v, ok := attrs["netrewind.count"]
	if !ok {
		t.Fatal("a folded event was exported as one occurrence, which under-reports exactly when things are worst")
	}
	if got := v.(map[string]any)["intValue"]; got != "47" {
		t.Errorf("count = %v, want 47", got)
	}
}

func TestAnIncidentCarriesItsWholeChain(t *testing.T) {
	inc := &incident.Incident{
		ID: "inc-1", RuleID: "gateway-hijack", Title: "The gateway moved",
		Severity: event.SevError, Confidence: 90, OpenedAt: time.Now().UnixNano(),
		Advice: "check the switch port",
		RootCause: incident.RootCause{
			Kind: event.KindARPBindingChanged, Entity: "10.1.10.1", EventID: "ev-1",
		},
		Chain: []incident.Link{
			{Seq: 0, EventID: "ev-1", Kind: event.KindARPBindingChanged,
				Relation: incident.RelCauses, Why: "the gateway's hardware changed", Subject: "10.1.10.1"},
			{Seq: 1, EventID: "ev-2", Kind: event.KindDefaultRouteChanged,
				Relation: incident.RelCorrelates, Why: "routing followed", Subject: "default"},
		},
	}
	body, err := Encode("recorder-1", "0.8.0", nil, []*incident.Incident{inc})
	if err != nil {
		t.Fatal(err)
	}
	attrs := attrsOf(firstRecord(t, body))

	for _, want := range []string{
		"netrewind.rule", "netrewind.advice", "netrewind.root_cause.entity",
		"netrewind.chain.0.why", "netrewind.chain.1.relation",
	} {
		if attrs[want] == nil {
			t.Errorf("%s is missing; the incident arrives without its evidence", want)
		}
	}
	// The relation is the one thing that must not be lost: it is the difference
	// between a claim about cause and an observation of co-occurrence.
	rel := attrs["netrewind.chain.1.relation"].(map[string]any)["stringValue"]
	if rel != string(incident.RelCorrelates) {
		t.Errorf("relation = %v, want %s", rel, incident.RelCorrelates)
	}
}

// Two exports of the same batch must be byte-identical, so a diff means
// something actually changed.
func TestEncodingIsStable(t *testing.T) {
	e := sampleEvent()
	first, err := Encode("recorder-1", "0.8.0", []*event.Event{e}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		again, err := Encode("recorder-1", "0.8.0", []*event.Event{e}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if string(again) != string(first) {
			t.Fatal("the same batch encoded differently twice; attribute order is not stable")
		}
	}
}

/* ------------------------------------------------------------------ */
/* Export                                                             */
/* ------------------------------------------------------------------ */

func TestASuccessfulExportPostsToTheLogsPath(t *testing.T) {
	var got struct {
		path        string
		contentType string
		header      string
		body        []byte
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path = r.URL.Path
		got.contentType = r.Header.Get("Content-Type")
		got.header = r.Header.Get("X-Auth")
		got.body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ex := New(srv.URL, "recorder-1", "0.8.0", map[string]string{"X-Auth": "token"}, quiet())
	if err := ex.Export(context.Background(), []*event.Event{sampleEvent()}, nil); err != nil {
		t.Fatalf("export: %v", err)
	}

	if got.path != logsPath {
		t.Errorf("posted to %s, want %s", got.path, logsPath)
	}
	if got.contentType != "application/json" {
		t.Errorf("content type = %q", got.contentType)
	}
	if got.header != "token" {
		t.Error("configured headers were not sent, so an authenticated collector would refuse this")
	}
	if !strings.Contains(string(got.body), "resourceLogs") {
		t.Error("the body is not an OTLP payload")
	}
}

// An endpoint given with the signal path already on it is what half the
// documentation shows, and appending a second one would 404 forever.
func TestTheEndpointIsNotDoubledUp(t *testing.T) {
	for _, given := range []string{
		"http://collector:4318",
		"http://collector:4318/",
		"http://collector:4318/v1/logs",
	} {
		ex := New(given, "r", "v", nil, quiet())
		if got := ex.Endpoint(); got != "http://collector:4318/v1/logs" {
			t.Errorf("New(%q).Endpoint() = %q", given, got)
		}
	}
}

// A collector that is down must not stop the recorder, and must not be silent
// about what it cost.
func TestACollectorThatIsDownIsCountedNotIgnored(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	ex := New(srv.URL, "recorder-1", "0.8.0", nil, quiet())
	events := []*event.Event{sampleEvent(), sampleEvent(), sampleEvent()}

	if err := ex.Export(context.Background(), events, nil); err == nil {
		t.Fatal("a failing collector reported success")
	}
	if got := ex.Dropped(); got != 3 {
		t.Errorf("dropped = %d, want 3: the count is the only trace these records leave", got)
	}
}

// Retrying a rejected payload just delays discovering it is malformed.
func TestARejectedPayloadIsNotRetried(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	ex := New(srv.URL, "recorder-1", "0.8.0", nil, quiet())
	if err := ex.Export(context.Background(), []*event.Event{sampleEvent()}, nil); err == nil {
		t.Fatal("a 400 was reported as success")
	}
	if n := attempts.Load(); n != 1 {
		t.Errorf("a 400 was attempted %d times; it will be a 400 next time too", n)
	}
}

// A collector restarting is the ordinary case, and the batch should survive it.
func TestATransientFailureIsRetried(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ex := New(srv.URL, "recorder-1", "0.8.0", nil, quiet())
	if err := ex.Export(context.Background(), []*event.Event{sampleEvent()}, nil); err != nil {
		t.Fatalf("a collector that came back on the second try still failed: %v", err)
	}
	if ex.Dropped() != 0 {
		t.Errorf("dropped %d after a successful retry", ex.Dropped())
	}
}

func TestAnEmptyBatchIsNotPosted(t *testing.T) {
	var posted atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		posted.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ex := New(srv.URL, "recorder-1", "0.8.0", nil, quiet())
	if err := ex.Export(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
	if posted.Load() {
		t.Error("an empty batch was posted")
	}
}

/* ------------------------------------------------------------------ */

func firstRecord(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	rl := doc["resourceLogs"].([]any)[0].(map[string]any)
	sl := rl["scopeLogs"].([]any)[0].(map[string]any)
	return sl["logRecords"].([]any)[0].(map[string]any)
}

func attrsOf(rec map[string]any) map[string]any {
	out := map[string]any{}
	for _, kv := range rec["attributes"].([]any) {
		m := kv.(map[string]any)
		out[m["key"].(string)] = m["value"]
	}
	return out
}
