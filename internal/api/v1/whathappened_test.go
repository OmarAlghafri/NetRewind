package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/identity"
	"github.com/OmarAlghafri/netrewind/internal/store"
)

// bindIdentity is this file's equivalent of seedEvent - writes a real
// identity_binding row directly through the concrete *store.SQLite (the
// only implementation newTestServer ever hands back), the same way
// production identity resolution does, rather than faking ResolveAt's
// answer.
func bindIdentity(t *testing.T, st store.Store, hostID, attrType, attrValue string, from, to time.Time) {
	t.Helper()
	sql, ok := st.(*store.SQLite)
	if !ok {
		t.Fatalf("test store is %T, want *store.SQLite", st)
	}
	var validTo int64
	if !to.IsZero() {
		validTo = to.UnixNano()
	}
	err := sql.Open(context.Background(), identity.Binding{
		HostID: hostID, AttrType: attrType, AttrValue: attrValue,
		ValidFrom: from.UnixNano(), ValidTo: validTo, Confidence: 100,
	})
	if err != nil {
		t.Fatalf("bind identity: %v", err)
	}
}

// The plain case: no host given, searches the whole window, always folds
// in system.* events - matching cmd/netrewind/timeline.go's own behaviour
// when --host is omitted.
func TestWhatHappenedWithNoHostSearchesTheWholeWindow(t *testing.T) {
	srv, st := newTestServer(t)
	now := time.Now()
	seedEvent(t, st, event.KindLinkDown, now)
	seedEvent(t, st, event.KindLinkUp, now.Add(-10*time.Minute)) // outside a 5m window

	rec := get(t, srv.Handler(), "/v1/what-happened?at="+now.UTC().Format(time.RFC3339))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body whatHappenedResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Events) != 1 || body.Events[0].Kind != event.KindLinkDown {
		t.Fatalf("events = %+v, want exactly the one inside the default 5m window", body.Events)
	}
}

// The point of the identity table: asking about an address finds events
// recorded while the same host was known by a *different* address too,
// and does not drag in events belonging to whichever other machine holds
// that address today - the exact scenario labelsToSearch exists for.
func TestWhatHappenedExpandsAHostToEveryLabelItAnsweredTo(t *testing.T) {
	srv, st := newTestServer(t)
	now := time.Now()

	// This host held 10.0.0.5 up to 1 minute ago, then moved to 10.0.0.9.
	bindIdentity(t, st, "host-1", identity.AttrIPv4, "10.0.0.5", now.Add(-2*time.Hour), now.Add(-1*time.Minute))
	bindIdentity(t, st, "host-1", identity.AttrIPv4, "10.0.0.9", now.Add(-1*time.Minute), time.Time{})

	// An event recorded against the OLD address, inside the window.
	b := event.NewBuilder("obs-1", nil)
	oldAddrEvent := b.New(event.SourceNetlink, event.KindLinkDown, event.SevWarn, event.Iface("eth0", 2))
	oldAddrEvent.Subject.Label = "10.0.0.5"
	oldAddrEvent.TSWall = now.Add(-90 * time.Second).UnixNano()
	if err := st.Append(context.Background(), oldAddrEvent); err != nil {
		t.Fatalf("Append: %v", err)
	}
	// An unrelated event on a different host entirely, holding an address
	// this host never had - must NOT be dragged in.
	unrelated := b.New(event.SourceNetlink, event.KindLinkUp, event.SevWarn, event.Iface("eth0", 2))
	unrelated.Subject.Label = "10.0.0.200"
	unrelated.TSWall = now.UnixNano()
	if err := st.Append(context.Background(), unrelated); err != nil {
		t.Fatalf("Append: %v", err)
	}

	url := "/v1/what-happened?host=10.0.0.9&at=" + now.UTC().Format(time.RFC3339) + "&window=10m"
	rec := get(t, srv.Handler(), url)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body whatHappenedResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	foundOld := false
	for _, e := range body.Events {
		if e.Subject.Label == "10.0.0.200" {
			t.Fatalf("event for an unrelated host (10.0.0.200) leaked into the result: %+v", e)
		}
		if e.Subject.Label == "10.0.0.5" {
			foundOld = true
		}
	}
	if !foundOld {
		t.Fatalf("expected the event recorded under the host's OLD address (10.0.0.5) to be found via identity expansion; got %+v", body.Events)
	}
	found9 := false
	for _, l := range body.ObservedLabels {
		if l == "10.0.0.5" {
			found9 = true
		}
	}
	if !found9 {
		t.Errorf("observed_labels = %v, want it to include the host's other address 10.0.0.5", body.ObservedLabels)
	}
}

// A blind spot must never be silently absent: system.gap/collector_down
// inside the window is always included, host filter or not - the same
// guarantee cmd/netrewind/timeline.go states explicitly.
func TestWhatHappenedAlwaysIncludesSystemEvents(t *testing.T) {
	srv, st := newTestServer(t)
	now := time.Now()
	b := event.NewBuilder("obs-1", nil)

	gap := b.New(event.SourceInternal, event.KindSystemGap, event.SevWarn, event.EntityRef{Kind: event.EntityObserver, Label: "obs-1"})
	gap.TSWall = now.UnixNano()
	if err := st.Append(context.Background(), gap); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// Ask about a host that has no relation to the gap event at all - the
	// gap must still appear, because "no record" must never look
	// indistinguishable from "nothing happened".
	url := "/v1/what-happened?host=192.0.2.1&at=" + now.UTC().Format(time.RFC3339)
	rec := get(t, srv.Handler(), url)
	var body whatHappenedResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	foundGap := false
	for _, e := range body.Events {
		if e.Kind == event.KindSystemGap {
			foundGap = true
		}
	}
	if !foundGap {
		t.Fatalf("system.gap was not included even though it fell inside the window; got %+v", body.Events)
	}
}

func TestWhatHappenedRejectsBadParams(t *testing.T) {
	srv, _ := newTestServer(t)
	for _, url := range []string{
		"/v1/what-happened?at=not-a-timestamp",
		"/v1/what-happened?window=not-a-duration",
		"/v1/what-happened?window=-5m",
	} {
		rec := get(t, srv.Handler(), url)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", url, rec.Code)
		}
	}
}
