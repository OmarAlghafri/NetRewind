package analyze

import (
	"testing"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/ports"
)

// This file is the test coverage internal/collect/netlink's link diff logic
// never had: that logic lived inside LinkCollector.diff, coupled to
// vishvananda/netlink types, and could only be exercised end to end through
// the real fault-injection lab (docs/evidence/04-full-lab-gate.log) or a real
// kernel. Extracting it behind ports.InterfaceObservation is what makes cases
// like "exactly three transitions" or "an MTU change with nothing else
// different" testable in isolation at all.

func at(seconds int) time.Time {
	return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(seconds) * time.Second)
}

func newAnalyzer() *InterfaceAnalyzer {
	return NewInterfaceAnalyzer(event.NewBuilder("test", nil))
}

func kinds(events []*event.Event) []event.Kind {
	out := make([]event.Kind, len(events))
	for i, e := range events {
		out[i] = e.Kind
	}
	return out
}

func TestASeededInterfaceProducesNoEventOnItsFirstRealNotification(t *testing.T) {
	a := newAnalyzer()
	a.Seed(ports.InterfaceObservation{Index: 1, Name: "eth0", AdminUp: true, OperUp: true, MTU: 1500})

	// Netlink re-announces interfaces for reasons that are not state changes;
	// an identical notification must produce nothing.
	got := a.Observe(ports.InterfaceObservation{Index: 1, Name: "eth0", AdminUp: true, OperUp: true, MTU: 1500}, at(1))
	if len(got) != 0 {
		t.Errorf("got %d events for an unchanged interface, want 0: %v", len(got), kinds(got))
	}
}

func TestAnUnseededInterfacesFirstSightingProducesNoEvent(t *testing.T) {
	a := newAnalyzer()
	// No Seed call at all: this is what a genuinely new interface looks like.
	got := a.Observe(ports.InterfaceObservation{Index: 7, Name: "veth-new", AdminUp: true, OperUp: true, MTU: 1500}, at(1))
	if len(got) != 0 {
		t.Errorf("got %d events for a never-before-seen index, want 0 (a new interface is not modelled as a change): %v", len(got), kinds(got))
	}
}

func TestAdministrativeDownIsDistinguishedFromCarrierLoss(t *testing.T) {
	a := newAnalyzer()
	a.Seed(ports.InterfaceObservation{Index: 1, Name: "eth0", AdminUp: true, OperUp: true, MTU: 1500})

	got := a.Observe(ports.InterfaceObservation{Index: 1, Name: "eth0", AdminUp: false, OperUp: false, MTU: 1500}, at(1))
	if len(got) != 1 || got[0].Kind != event.KindLinkDown {
		t.Fatalf("kinds = %v, want exactly one link.down", kinds(got))
	}
	if got[0].Attrs["cause"] != "administrative" {
		t.Errorf("cause = %v, want administrative (admin_up went false)", got[0].Attrs["cause"])
	}
}

func TestCarrierLossWithAdminStillUpIsCauseCarrier(t *testing.T) {
	a := newAnalyzer()
	a.Seed(ports.InterfaceObservation{Index: 1, Name: "eth0", AdminUp: true, OperUp: true, MTU: 1500})

	got := a.Observe(ports.InterfaceObservation{Index: 1, Name: "eth0", AdminUp: true, OperUp: false, MTU: 1500}, at(1))
	if len(got) != 1 || got[0].Kind != event.KindLinkDown {
		t.Fatalf("kinds = %v, want exactly one link.down", kinds(got))
	}
	if got[0].Attrs["cause"] != "carrier" {
		t.Errorf("cause = %v, want carrier (admin_up stayed true)", got[0].Attrs["cause"])
	}
}

func TestRecoveryReportsDownDuration(t *testing.T) {
	a := newAnalyzer()
	a.Seed(ports.InterfaceObservation{Index: 1, Name: "eth0", AdminUp: true, OperUp: true, MTU: 1500})
	a.Observe(ports.InterfaceObservation{Index: 1, Name: "eth0", AdminUp: true, OperUp: false, MTU: 1500}, at(0))

	got := a.Observe(ports.InterfaceObservation{Index: 1, Name: "eth0", AdminUp: true, OperUp: true, MTU: 1500}, at(5))
	if len(got) != 1 || got[0].Kind != event.KindLinkUp {
		t.Fatalf("kinds = %v, want exactly one link.up", kinds(got))
	}
	if ms, _ := got[0].Attrs["down_duration_ms"].(int64); ms != 5000 {
		t.Errorf("down_duration_ms = %v, want 5000", got[0].Attrs["down_duration_ms"])
	}
}

func TestARecoveryWithNoPriorDownCarriesNoDuration(t *testing.T) {
	// isOperUp treats an interface that was never known down as never having
	// a duration to report - this covers a veth pair coming straight up.
	a := newAnalyzer()
	a.Seed(ports.InterfaceObservation{Index: 1, Name: "veth0", AdminUp: false, OperUp: false, MTU: 1500})

	got := a.Observe(ports.InterfaceObservation{Index: 1, Name: "veth0", AdminUp: true, OperUp: true, MTU: 1500}, at(1))
	if len(got) != 1 || got[0].Kind != event.KindLinkUp {
		t.Fatalf("kinds = %v, want exactly one link.up", kinds(got))
	}
	if _, present := got[0].Attrs["down_duration_ms"]; present {
		t.Errorf("down_duration_ms present with no prior down: %v", got[0].Attrs["down_duration_ms"])
	}
}

func TestThreeCarrierLossesInTheWindowIsAFlap(t *testing.T) {
	a := newAnalyzer()
	a.Seed(ports.InterfaceObservation{Index: 1, Name: "eth0", AdminUp: true, OperUp: true, MTU: 1500})

	up := func(sec int) {
		a.Observe(ports.InterfaceObservation{Index: 1, Name: "eth0", AdminUp: true, OperUp: true, MTU: 1500}, at(sec))
	}
	down := func(sec int) []*event.Event {
		return a.Observe(ports.InterfaceObservation{Index: 1, Name: "eth0", AdminUp: true, OperUp: false, MTU: 1500}, at(sec))
	}

	if got := down(10); len(got) != 1 {
		t.Fatalf("down 1: kinds = %v, want just link.down", kinds(got))
	}
	up(11)
	if got := down(20); len(got) != 1 {
		t.Fatalf("down 2: kinds = %v, want just link.down (only 2 transitions so far)", kinds(got))
	}
	up(21)

	got := down(30)
	if len(got) != 2 {
		t.Fatalf("down 3: kinds = %v, want link.down + link.flap", kinds(got))
	}
	if got[1].Kind != event.KindLinkFlap {
		t.Errorf("second event = %s, want %s", got[1].Kind, event.KindLinkFlap)
	}
	if n, _ := got[1].Attrs["transitions"].(int); n != FlapTransitions {
		t.Errorf("transitions = %v, want %d", got[1].Attrs["transitions"], FlapTransitions)
	}
}

func TestAFlapIsReportedOnceNotOnEveryFurtherTransition(t *testing.T) {
	a := newAnalyzer()
	a.Seed(ports.InterfaceObservation{Index: 1, Name: "eth0", AdminUp: true, OperUp: true, MTU: 1500})
	up := func(sec int) {
		a.Observe(ports.InterfaceObservation{Index: 1, Name: "eth0", AdminUp: true, OperUp: true, MTU: 1500}, at(sec))
	}
	down := func(sec int) []*event.Event {
		return a.Observe(ports.InterfaceObservation{Index: 1, Name: "eth0", AdminUp: true, OperUp: false, MTU: 1500}, at(sec))
	}

	down(10)
	up(11)
	down(20)
	up(21)
	if got := down(30); len(got) != 2 {
		t.Fatalf("third down: want link.down+link.flap, got %v", kinds(got))
	}
	up(31)

	got := down(40)
	if len(got) != 1 || got[0].Kind != event.KindLinkDown {
		t.Errorf("fourth down: kinds = %v, want just link.down (already flagged as flapping)", kinds(got))
	}
}

func TestAdministrativeTransitionsDoNotCountTowardsFlapping(t *testing.T) {
	// An engineer taking a port down and up three times while working on it
	// must not be reported as a faulty cable.
	a := newAnalyzer()
	a.Seed(ports.InterfaceObservation{Index: 1, Name: "eth0", AdminUp: true, OperUp: true, MTU: 1500})
	admin := func(sec int, up bool) []*event.Event {
		return a.Observe(ports.InterfaceObservation{Index: 1, Name: "eth0", AdminUp: up, OperUp: up, MTU: 1500}, at(sec))
	}

	for i, sec := range []int{10, 20, 30} {
		if got := admin(sec, false); len(got) != 1 || got[0].Attrs["cause"] != "administrative" {
			t.Fatalf("down %d: %v", i, kinds(got))
		}
		admin(sec+1, true)
	}
	// A fourth administrative down must still be an ordinary link.down, never
	// a flap - the three prior ones must not have accumulated in cur.downs.
	got := admin(40, false)
	if len(got) != 1 || got[0].Kind != event.KindLinkDown {
		t.Errorf("kinds = %v, want a single link.down, never link.flap for administrative transitions", kinds(got))
	}
}

func TestAnMTUChangeIsReportedAloneWhenNothingElseChanged(t *testing.T) {
	a := newAnalyzer()
	a.Seed(ports.InterfaceObservation{Index: 1, Name: "eth0", AdminUp: true, OperUp: true, MTU: 1500})

	got := a.Observe(ports.InterfaceObservation{Index: 1, Name: "eth0", AdminUp: true, OperUp: true, MTU: 1400}, at(1))
	if len(got) != 1 || got[0].Kind != event.KindLinkMTUChanged {
		t.Fatalf("kinds = %v, want exactly one link.mtu_changed", kinds(got))
	}
	if got[0].Attrs["mtu_old"] != 1500 || got[0].Attrs["mtu_new"] != 1400 {
		t.Errorf("mtu_old/new = %v/%v, want 1500/1400", got[0].Attrs["mtu_old"], got[0].Attrs["mtu_new"])
	}
}

func TestAnMTUChangeAlongsideADownIsReportedAsBoth(t *testing.T) {
	a := newAnalyzer()
	a.Seed(ports.InterfaceObservation{Index: 1, Name: "eth0", AdminUp: true, OperUp: true, MTU: 1500})

	got := a.Observe(ports.InterfaceObservation{Index: 1, Name: "eth0", AdminUp: false, OperUp: false, MTU: 1400}, at(1))
	if len(got) != 2 {
		t.Fatalf("kinds = %v, want link.down and link.mtu_changed together", kinds(got))
	}
}

func TestTheFirstReadingNeverClaimsAnMTUChange(t *testing.T) {
	// prev.MTU is the zero value on the very first Seed if a caller could not
	// read it; that must never be read as "changed to 1500".
	a := newAnalyzer()
	a.Seed(ports.InterfaceObservation{Index: 1, Name: "eth0", AdminUp: true, OperUp: true, MTU: 0})

	got := a.Observe(ports.InterfaceObservation{Index: 1, Name: "eth0", AdminUp: true, OperUp: true, MTU: 1500}, at(1))
	if len(got) != 0 {
		t.Errorf("kinds = %v, want none (MTU baseline was unknown, not zero)", kinds(got))
	}
}

func TestARemovedInterfaceReportsDownWithCauseRemoved(t *testing.T) {
	a := newAnalyzer()
	a.Seed(ports.InterfaceObservation{Index: 9, Name: "veth-temp", AdminUp: true, OperUp: true, MTU: 1500})

	got := a.Observe(ports.InterfaceObservation{Index: 9, Name: "veth-temp", Removed: true}, at(1))
	if len(got) != 1 || got[0].Kind != event.KindLinkDown || got[0].Attrs["cause"] != "removed" {
		t.Fatalf("kinds/cause = %v/%v, want [link.down]/removed", kinds(got), got[0].Attrs["cause"])
	}
	if a.Known() != 0 {
		t.Errorf("Known() = %d after removal, want 0", a.Known())
	}
}

func TestRemovingAnUnknownInterfaceProducesNoEvent(t *testing.T) {
	a := newAnalyzer()
	got := a.Observe(ports.InterfaceObservation{Index: 9, Name: "ghost", Removed: true}, at(1))
	if len(got) != 0 {
		t.Errorf("kinds = %v, want none - nothing was ever known about this interface", kinds(got))
	}
}

func TestARemovedInterfaceCanReappearAsNew(t *testing.T) {
	a := newAnalyzer()
	a.Seed(ports.InterfaceObservation{Index: 9, Name: "veth-temp", AdminUp: true, OperUp: true, MTU: 1500})
	a.Observe(ports.InterfaceObservation{Index: 9, Name: "veth-temp", Removed: true}, at(1))

	// The same index reused for a new interface must be treated as new, not
	// diffed against the deleted one's stale state.
	got := a.Observe(ports.InterfaceObservation{Index: 9, Name: "veth-temp2", AdminUp: true, OperUp: true, MTU: 1500}, at(2))
	if len(got) != 0 {
		t.Errorf("kinds = %v, want none (this index's first sighting after removal)", kinds(got))
	}
}

func TestErrorRateBelowThresholdProducesNoEvent(t *testing.T) {
	a := newAnalyzer()
	a.Seed(ports.InterfaceObservation{Index: 1, Name: "eth0"})
	a.CompareCounters(1, "eth0", ports.InterfaceCounters{RxPackets: 100000, TxPackets: 100000}, at(0))

	got := a.CompareCounters(1, "eth0", ports.InterfaceCounters{
		RxPackets: 200000, TxPackets: 200000, RxErrors: 1, // far under 1% of 200,000
	}, at(30))
	if len(got) != 0 {
		t.Errorf("kinds = %v, want none for a negligible error rate", kinds(got))
	}
}

func TestErrorRateAboveThresholdIsReported(t *testing.T) {
	a := newAnalyzer()
	a.Seed(ports.InterfaceObservation{Index: 1, Name: "eth0"})
	a.CompareCounters(1, "eth0", ports.InterfaceCounters{RxPackets: 0, TxPackets: 0}, at(0))

	got := a.CompareCounters(1, "eth0", ports.InterfaceCounters{
		RxPackets: 5000, TxPackets: 0, RxErrors: 100, // 100 / (5000+100) ~= 1.96%
	}, at(30))
	if len(got) != 1 || got[0].Kind != event.KindLinkErrorRate {
		t.Fatalf("kinds = %v, want exactly one link.error_rate_high", kinds(got))
	}
}

func TestFewerThanMinPacketsNeverReportsARate(t *testing.T) {
	// One error out of two packets on an idle interface is not "50% errors".
	a := newAnalyzer()
	a.Seed(ports.InterfaceObservation{Index: 1, Name: "eth0"})
	a.CompareCounters(1, "eth0", ports.InterfaceCounters{RxPackets: 0}, at(0))

	got := a.CompareCounters(1, "eth0", ports.InterfaceCounters{RxPackets: 2, RxErrors: 1}, at(30))
	if len(got) != 0 {
		t.Errorf("kinds = %v, want none (below MinPacketsForRate)", kinds(got))
	}
}

func TestACounterResetIsNotReadAsAHugeUnsignedDelta(t *testing.T) {
	a := newAnalyzer()
	a.Seed(ports.InterfaceObservation{Index: 1, Name: "eth0"})
	a.CompareCounters(1, "eth0", ports.InterfaceCounters{RxPackets: 500000}, at(0))

	// The interface was recreated and its counters reset to near zero.
	got := a.CompareCounters(1, "eth0", ports.InterfaceCounters{RxPackets: 10}, at(30))
	if len(got) != 0 {
		t.Errorf("kinds = %v, want none - a decreasing counter must be treated as a reset, not billions of errors", kinds(got))
	}
}

func TestCompareCountersOnAnUnknownInterfaceIsIgnored(t *testing.T) {
	a := newAnalyzer()
	got := a.CompareCounters(99, "ghost", ports.InterfaceCounters{RxPackets: 100}, at(0))
	if len(got) != 0 {
		t.Errorf("kinds = %v, want none for an interface never seeded", kinds(got))
	}
}
