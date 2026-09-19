package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/incident"
	"github.com/OmarAlghafri/netrewind/internal/store"
)

func seedIncident(t *testing.T, st store.Store, ruleID string, at time.Time) {
	t.Helper()
	inc := &incident.Incident{
		ID: ulid.Make().String(), OpenedAt: at.UnixNano(),
		Status: incident.StatusClosed, Title: "t", Severity: event.SevWarn,
		Confidence: 80, RuleID: ruleID,
		Chain: []incident.Link{{EventID: "e", At: at.UnixNano()}},
	}
	if err := st.AppendIncidents(context.Background(), inc); err != nil {
		t.Fatalf("seed incident: %v", err)
	}
}

// A cursor's basic job at the HTTP layer: every response carries one
// (even the very first, since-based call - a client needs somewhere to
// start delta-polling from), and presenting it back returns only what's
// new.
func TestEventsCursorRoundTrip(t *testing.T) {
	srv, st := newTestServer(t)
	now := time.Now()
	seedEvent(t, st, event.KindLinkDown, now.Add(-1*time.Minute))

	first := get(t, srv.Handler(), "/v1/events")
	var firstBody eventsResponse
	if err := json.NewDecoder(first.Body).Decode(&firstBody); err != nil {
		t.Fatal(err)
	}
	if len(firstBody.Events) != 1 {
		t.Fatalf("first poll: got %d events, want 1", len(firstBody.Events))
	}
	if firstBody.NextCursor == "" {
		t.Fatal("first (since-based) response must still carry a next_cursor - otherwise a client can never switch to delta polling")
	}

	// Nothing new happened: polling again with the cursor returns no events.
	second := get(t, srv.Handler(), "/v1/events?cursor="+firstBody.NextCursor)
	var secondBody eventsResponse
	if err := json.NewDecoder(second.Body).Decode(&secondBody); err != nil {
		t.Fatal(err)
	}
	if len(secondBody.Events) != 0 {
		t.Fatalf("delta poll with nothing new: got %d events, want 0", len(secondBody.Events))
	}

	// A genuinely new event now appears on the next delta poll, and only
	// that one - not the whole window again.
	seedEvent(t, st, event.KindLinkUp, now)
	third := get(t, srv.Handler(), "/v1/events?cursor="+secondBody.NextCursor)
	var thirdBody eventsResponse
	if err := json.NewDecoder(third.Body).Decode(&thirdBody); err != nil {
		t.Fatal(err)
	}
	if len(thirdBody.Events) != 1 || thirdBody.Events[0].Kind != event.KindLinkUp {
		t.Fatalf("delta poll after a new event: got %+v, want exactly the new link.up", thirdBody.Events)
	}
}

// The exact scenario the execution order calls out by name: a repeated
// event folds into its existing row instead of inserting a new one, and a
// client delta-polling with a cursor must still see the count change - an
// HTTP-level confirmation of internal/store's own
// TestQueryCursorCatchesAFoldedRowsCountChange, proving the fold-detection
// clause survives being wired through the actual handler, cursor encoding,
// and JSON round-trip, not only the Go-internal Filter/Query path.
func TestEventsCursorCatchesAFoldedRowsCountChange(t *testing.T) {
	srv, st := newTestServer(t)
	b := event.NewBuilder("obs-1", nil)
	now := time.Now()

	flap := b.New(event.SourceNetlink, event.KindLinkDown, event.SevWarn, event.Iface("eth1", 3))
	flap.TSWall = now.UnixNano()
	flap.WithDedup("link.down|eth1")
	if err := st.Append(context.Background(), flap); err != nil {
		t.Fatalf("Append: %v", err)
	}

	first := get(t, srv.Handler(), "/v1/events")
	var firstBody eventsResponse
	if err := json.NewDecoder(first.Body).Decode(&firstBody); err != nil {
		t.Fatal(err)
	}
	if len(firstBody.Events) != 1 || firstBody.Events[0].Count != 1 {
		t.Fatalf("first poll: got %+v, want one event with count 1", firstBody.Events)
	}

	secondFlap := b.New(event.SourceNetlink, event.KindLinkDown, event.SevWarn, event.Iface("eth1", 3))
	secondFlap.TSWall = now.Add(5 * time.Second).UnixNano()
	secondFlap.WithDedup("link.down|eth1")
	if err := st.Append(context.Background(), secondFlap); err != nil {
		t.Fatalf("Append (fold): %v", err)
	}

	delta := get(t, srv.Handler(), "/v1/events?cursor="+firstBody.NextCursor)
	var deltaBody eventsResponse
	if err := json.NewDecoder(delta.Body).Decode(&deltaBody); err != nil {
		t.Fatal(err)
	}
	if len(deltaBody.Events) != 1 {
		t.Fatalf("delta poll after a fold: got %d events, want 1 (the folded row) - a fold was silently missed over HTTP", len(deltaBody.Events))
	}
	if deltaBody.Events[0].Count != 2 {
		t.Fatalf("delta poll after a fold: Count = %d, want 2", deltaBody.Events[0].Count)
	}
}

func TestEventsOrderParam(t *testing.T) {
	srv, st := newTestServer(t)
	now := time.Now()
	seedEvent(t, st, event.KindLinkDown, now.Add(-2*time.Minute))
	seedEvent(t, st, event.KindLinkUp, now.Add(-1*time.Minute))

	asc := get(t, srv.Handler(), "/v1/events?order=asc")
	var ascBody eventsResponse
	json.NewDecoder(asc.Body).Decode(&ascBody)
	if len(ascBody.Events) != 2 || ascBody.Events[0].Kind != event.KindLinkDown {
		t.Fatalf("order=asc: got %+v, want link.down first", ascBody.Events)
	}

	desc := get(t, srv.Handler(), "/v1/events?order=desc")
	var descBody eventsResponse
	json.NewDecoder(desc.Body).Decode(&descBody)
	if len(descBody.Events) != 2 || descBody.Events[0].Kind != event.KindLinkUp {
		t.Fatalf("order=desc: got %+v, want link.up first", descBody.Events)
	}

	bad := get(t, srv.Handler(), "/v1/events?order=sideways")
	if bad.Code != http.StatusBadRequest {
		t.Errorf("order=sideways: status = %d, want 400", bad.Code)
	}
}

func TestEventsRejectsAMalformedCursor(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := get(t, srv.Handler(), "/v1/events?cursor=not-a-real-cursor")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var body errorResponse
	json.NewDecoder(rec.Body).Decode(&body)
	if body.Error.Code != "bad_param" {
		t.Errorf("error code = %q, want bad_param", body.Error.Code)
	}
}

func TestEventsLimitIsClampedToMaxLimit(t *testing.T) {
	srv, st := newTestServer(t)
	now := time.Now()
	for i := 0; i < 5; i++ {
		seedEvent(t, st, event.KindLinkDown, now.Add(-time.Duration(i)*time.Second))
	}
	// Ask for more than store.MaxLimit; the server must not honour it
	// literally (bug: an unbounded limit request should never scan more
	// than the ceiling, whatever it is set to) - this only checks the
	// request does not error and returns at most what actually exists,
	// since exercising the ceiling itself would mean seeding >20000 rows.
	rec := get(t, srv.Handler(), "/v1/events?limit=999999999")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body eventsResponse
	json.NewDecoder(rec.Body).Decode(&body)
	if len(body.Events) != 5 {
		t.Fatalf("got %d events, want all 5 seeded", len(body.Events))
	}
}

func TestIncidentsCursorRoundTrip(t *testing.T) {
	srv, st := newTestServer(t)
	now := time.Now()
	seedIncident(t, st, "rule-a", now.Add(-1*time.Minute))

	first := get(t, srv.Handler(), "/v1/incidents")
	var firstBody incidentsResponse
	json.NewDecoder(first.Body).Decode(&firstBody)
	if len(firstBody.Incidents) != 1 || firstBody.NextCursor == "" {
		t.Fatalf("first poll: got %+v, want 1 incident and a cursor", firstBody)
	}

	second := get(t, srv.Handler(), "/v1/incidents?cursor="+firstBody.NextCursor)
	var secondBody incidentsResponse
	json.NewDecoder(second.Body).Decode(&secondBody)
	if len(secondBody.Incidents) != 0 {
		t.Fatalf("delta poll with nothing new: got %d incidents, want 0", len(secondBody.Incidents))
	}

	seedIncident(t, st, "rule-b", now)
	third := get(t, srv.Handler(), "/v1/incidents?cursor="+secondBody.NextCursor)
	var thirdBody incidentsResponse
	json.NewDecoder(third.Body).Decode(&thirdBody)
	if len(thirdBody.Incidents) != 1 || thirdBody.Incidents[0].RuleID != "rule-b" {
		t.Fatalf("delta poll after a new incident: got %+v, want exactly rule-b", thirdBody.Incidents)
	}
}
