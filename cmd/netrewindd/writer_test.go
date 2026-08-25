package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/incident"
	"github.com/OmarAlghafri/netrewind/internal/metrics"
	"github.com/OmarAlghafri/netrewind/internal/store"
)

// brokenStore refuses writes on demand, the way a full disk does.
type brokenStore struct {
	mu       sync.Mutex
	refusing bool
	accepted []*event.Event
	attempts int
}

func (s *brokenStore) Append(_ context.Context, events ...*event.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts++
	if s.refusing {
		return errors.New("disk I/O error: no space left on device")
	}
	s.accepted = append(s.accepted, events...)
	return nil
}

func (s *brokenStore) refuse(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refusing = v
}

func (s *brokenStore) stored() []*event.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*event.Event(nil), s.accepted...)
}

func (s *brokenStore) Query(context.Context, store.Filter) ([]*event.Event, error) { return nil, nil }
func (s *brokenStore) Prune(context.Context, time.Time) (int64, error)             { return 0, nil }
func (s *brokenStore) GetMeta(context.Context, string) (string, error)             { return "", nil }
func (s *brokenStore) SetMeta(context.Context, string, string) error               { return nil }
func (s *brokenStore) Close() error                                                { return nil }
func (s *brokenStore) AppendIncidents(context.Context, ...*incident.Incident) error {
	return nil
}

func (s *brokenStore) QueryIncidents(context.Context, store.IncidentFilter) ([]*incident.Incident, error) {
	return nil, nil
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// A store that refuses writes and then recovers must leave an account of what
// was lost in between. Without it the timeline is unbroken and an arbitrary
// number of events are simply missing from the middle of it.
func TestEventsLostToAFullDiskAreAdmittedOnRecovery(t *testing.T) {
	st := &brokenStore{}
	meter := metrics.NewRecorder("test", "obs-1")
	b := event.NewBuilder("obs-1", nil)
	queue := make(chan *event.Event, 64)

	done := make(chan struct{})
	go func() {
		defer close(done)
		writer(context.Background(), st, nil, meter, b, queue, quiet())
	}()

	// The disk fills.
	st.refuse(true)
	for i := 0; i < 5; i++ {
		queue <- b.New(event.SourceNetlink, event.KindLinkDown, event.SevWarn, event.Iface("eth1", 3))
	}
	// Wait for the gauge itself rather than the attempt count: the counter is
	// incremented inside Append, before the writer has seen the error.
	waitFor(t, func() bool { return gauge(meter, metrics.StoreWritable) == 0 })

	if n := len(st.stored()); n != 0 {
		t.Fatalf("a refusing store accepted %d events", n)
	}

	// Space is freed.
	st.refuse(false)
	queue <- b.New(event.SourceNetlink, event.KindLinkUp, event.SevInfo, event.Iface("eth1", 3))

	waitFor(t, func() bool { return len(st.stored()) > 0 })
	close(queue)
	<-done

	stored := st.stored()
	var admission *event.Event
	for _, e := range stored {
		if e.Kind == event.KindSystemDrop {
			admission = e
		}
	}
	if admission == nil {
		t.Fatal("the store recovered and never admitted what it lost while it was broken")
	}
	if got := admission.Attrs["dropped"]; got != int64(5) {
		t.Errorf("dropped = %v, want the 5 events the store refused", got)
	}
	if got := admission.Attrs["source"]; got != "store" {
		t.Errorf("source = %v, want store", got)
	}
	if admission.Severity != event.SevError {
		t.Errorf("severity = %s, want error: events were lost for good", admission.Severity)
	}
	reason, _ := admission.Evidence["reason"].(string)
	if !strings.Contains(reason, "no space left") {
		t.Errorf("the admission does not carry why the store refused: %q", reason)
	}
	if gauge(meter, metrics.StoreWritable) != 1 {
		t.Error("netrewind_store_writable is still 0 after the store recovered")
	}
}

// The admission must survive a store that is still broken when it is attempted,
// rather than being lost along with the batch it was prepended to.
func TestTheAdmissionIsNotItselfLost(t *testing.T) {
	st := &brokenStore{}
	meter := metrics.NewRecorder("test", "obs-1")
	b := event.NewBuilder("obs-1", nil)
	queue := make(chan *event.Event, 64)

	done := make(chan struct{})
	go func() {
		defer close(done)
		writer(context.Background(), st, nil, meter, b, queue, quiet())
	}()

	st.refuse(true)
	for i := 0; i < 3; i++ {
		queue <- b.New(event.SourceNetlink, event.KindLinkDown, event.SevWarn, event.Iface("eth1", 3))
	}
	waitFor(t, func() bool { return st.attemptCount() >= 1 })

	// A second round, still broken. The first admission was attempted and
	// failed; it must not have been forgotten.
	for i := 0; i < 2; i++ {
		queue <- b.New(event.SourceNetlink, event.KindLinkDown, event.SevWarn, event.Iface("eth1", 3))
	}
	waitFor(t, func() bool { return st.attemptCount() >= 2 })

	st.refuse(false)
	queue <- b.New(event.SourceNetlink, event.KindLinkUp, event.SevInfo, event.Iface("eth1", 3))
	waitFor(t, func() bool { return len(st.stored()) > 0 })
	close(queue)
	<-done

	var total int64
	for _, e := range st.stored() {
		if e.Kind == event.KindSystemDrop {
			if n, ok := e.Attrs["dropped"].(int64); ok {
				total += n
			}
		}
	}
	if total != 5 {
		t.Errorf("the record accounts for %d lost events, want all 5", total)
	}
}

// The ordinary path must not be disturbed by any of the above.
func TestAWorkingStoreRecordsEverythingAndAdmitsNothing(t *testing.T) {
	st := &brokenStore{}
	meter := metrics.NewRecorder("test", "obs-1")
	b := event.NewBuilder("obs-1", nil)
	queue := make(chan *event.Event, 64)

	done := make(chan struct{})
	go func() {
		defer close(done)
		writer(context.Background(), st, nil, meter, b, queue, quiet())
	}()

	for i := 0; i < 10; i++ {
		queue <- b.New(event.SourceNetlink, event.KindLinkDown, event.SevWarn, event.Iface("eth1", 3))
	}
	close(queue)
	<-done

	stored := st.stored()
	if len(stored) != 10 {
		t.Errorf("stored %d of 10 events", len(stored))
	}
	for _, e := range stored {
		if e.Kind == event.KindSystemDrop {
			t.Error("a working store produced a loss admission")
		}
	}
}

func (s *brokenStore) attemptCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attempts
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for the writer")
}

// gauge reads a value out of the exposition, which is what a scraper sees.
// It reports -1 rather than failing, so it can be polled.
func gauge(meter *metrics.Recorder, name string) float64 {
	var sb strings.Builder
	if _, err := meter.Registry().WriteTo(&sb); err != nil {
		return -1
	}
	for _, line := range strings.Split(sb.String(), "\n") {
		if strings.HasPrefix(line, name+" ") {
			if strings.HasSuffix(line, " 1") {
				return 1
			}
			return 0
		}
	}
	return -1
}
