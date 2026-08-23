package event

import (
	"testing"
	"time"
)

func TestKindFamily(t *testing.T) {
	cases := map[Kind]string{
		KindLinkDown:            "link",
		KindARPBindingChanged:   "l2",
		KindDefaultRouteChanged: "l3",
		KindSystemGap:           "system",
		Kind("bare"):            "bare",
	}
	for k, want := range cases {
		if got := k.Family(); got != want {
			t.Errorf("Kind(%q).Family() = %q, want %q", k, got, want)
		}
	}
}

func TestBuilderProducesValidEvents(t *testing.T) {
	b := NewBuilder("observer-1", nil)
	e := b.New(SourceNetlink, KindLinkDown, SevWarn, Iface("eth1", 3))

	if err := e.Validate(); err != nil {
		t.Fatalf("freshly built event is invalid: %v", err)
	}
	if e.Confidence != 100 {
		t.Errorf("directly observed event should default to full confidence, got %d", e.Confidence)
	}
	if e.Count != 1 {
		t.Errorf("Count = %d, want 1", e.Count)
	}
	if e.SchemaV != SchemaVersion {
		t.Errorf("SchemaV = %d, want %d", e.SchemaV, SchemaVersion)
	}
	if e.Subject.Attrs["ifindex"] != "3" {
		t.Errorf("ifindex attr = %q, want %q", e.Subject.Attrs["ifindex"], "3")
	}
}

func TestValidateRejectsIncompleteEvents(t *testing.T) {
	b := NewBuilder("observer-1", nil)
	base := func() *Event { return b.New(SourceNetlink, KindLinkUp, SevInfo, Iface("eth0", 2)) }

	cases := map[string]func(*Event){
		"no id":         func(e *Event) { e.ID = "" },
		"no kind":       func(e *Event) { e.Kind = "" },
		"no source":     func(e *Event) { e.Source = "" },
		"no observer":   func(e *Event) { e.ObserverID = "" },
		"no subject":    func(e *Event) { e.Subject.Kind = "" },
		"no wall clock": func(e *Event) { e.TSWall = 0 },
		"bad certainty": func(e *Event) { e.Confidence = 101 },
	}
	for name, break_ := range cases {
		e := base()
		break_(e)
		if err := e.Validate(); err == nil {
			t.Errorf("%s: Validate() accepted an invalid event", name)
		}
	}
}

func TestEventChaining(t *testing.T) {
	b := NewBuilder("obs", nil)
	e := b.New(SourceNetlink, KindLinkDown, SevWarn, Iface("eth1", 3)).
		WithAttr("cause", "carrier").
		WithEvidence("raw_flags", 0x1003).
		WithRelated(Observer("obs")).
		WithDedup("link.down|eth1").
		WithConfidence(80)

	if e.Attrs["cause"] != "carrier" {
		t.Errorf("attr not set: %v", e.Attrs)
	}
	if e.Evidence["raw_flags"] != 0x1003 {
		t.Errorf("evidence not set: %v", e.Evidence)
	}
	if len(e.Related) != 1 || e.Related[0].Kind != EntityObserver {
		t.Errorf("related not set: %v", e.Related)
	}
	if e.DedupKey != "link.down|eth1" {
		t.Errorf("dedup key = %q", e.DedupKey)
	}
	if e.Confidence != 80 {
		t.Errorf("confidence = %d, want 80", e.Confidence)
	}
}

func TestClockMonotonicAdvancesAndReportsNoFalseSteps(t *testing.T) {
	c := NewClock()

	_, mono1 := c.Now()
	time.Sleep(2 * time.Millisecond)
	wall2, mono2 := c.Now()

	if mono2 <= mono1 {
		t.Errorf("monotonic clock did not advance: %d then %d", mono1, mono2)
	}
	if wall2 == 0 {
		t.Error("wall clock is zero")
	}
	if _, stepped := c.TakeStep(); stepped {
		t.Error("ordinary elapsed time was misreported as a clock step")
	}
}

func TestClockTakeStepClears(t *testing.T) {
	c := NewClock()
	// Simulate the wall clock having jumped forward a minute between readings
	// without the monotonic clock seeing it, which is what NTP correction and
	// VM resume look like from in here.
	c.Now()
	c.mu.Lock()
	c.lastWall -= int64(time.Minute)
	c.mu.Unlock()
	c.Now()

	ns, stepped := c.TakeStep()
	if !stepped {
		t.Fatal("a one minute wall-clock jump was not detected")
	}
	if ns < int64(50*time.Second) {
		t.Errorf("step reported as %v, want roughly a minute", time.Duration(ns))
	}
	if _, again := c.TakeStep(); again {
		t.Error("TakeStep did not clear the pending step")
	}
}
