package analyze

import (
	"context"
	"testing"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/ports"
)

func newNeighborAnalyzer() *NeighborAnalyzer {
	return NewNeighborAnalyzer(event.NewBuilder("test", nil), nil, nil)
}

func TestANewBindingIsReportedAsArpBindingNew(t *testing.T) {
	a := newNeighborAnalyzer()
	got := a.Observe(context.Background(), ports.NeighborObservation{IP: "10.0.0.5", MAC: "aa:bb", NUDState: "reachable"}, at(0))
	if len(got) != 1 || got[0].Kind != event.KindARPBindingNew {
		t.Fatalf("kinds = %v, want one arp_binding_new", kinds(got))
	}
	if got[0].Severity != event.SevInfo {
		t.Errorf("severity = %s, want info for a genuine first sighting", got[0].Severity)
	}
}

func TestAFirstSightingOnTheGatewayIsStillInfo(t *testing.T) {
	a := newNeighborAnalyzer()
	got := a.Observe(context.Background(), ports.NeighborObservation{IP: "10.0.0.1", MAC: "aa:bb", IsGateway: true}, at(0))
	if len(got) != 1 || got[0].Severity != event.SevInfo {
		t.Fatalf("severity = %v, want info - the recorder starting up meets its gateway, nothing changed", got)
	}
	if got[0].Attrs["is_gateway"] != true {
		t.Errorf("is_gateway attr = %v, want true", got[0].Attrs["is_gateway"])
	}
}

func TestAChangedBindingIsWarnUnlessItIsTheGateway(t *testing.T) {
	a := newNeighborAnalyzer()
	a.Seed(ports.NeighborObservation{IP: "10.0.0.5", MAC: "aa:aa"})

	got := a.Observe(context.Background(), ports.NeighborObservation{IP: "10.0.0.5", MAC: "bb:bb"}, at(1))
	if len(got) != 1 || got[0].Kind != event.KindARPBindingChanged {
		t.Fatalf("kinds = %v, want arp_binding_changed", kinds(got))
	}
	if got[0].Severity != event.SevWarn {
		t.Errorf("severity = %s, want warn for an ordinary host", got[0].Severity)
	}
	if got[0].Attrs["mac_old"] != "aa:aa" || got[0].Attrs["mac_new"] != "bb:bb" {
		t.Errorf("mac_old/new = %v/%v", got[0].Attrs["mac_old"], got[0].Attrs["mac_new"])
	}
}

func TestAChangedGatewayBindingIsError(t *testing.T) {
	a := newNeighborAnalyzer()
	a.Seed(ports.NeighborObservation{IP: "10.0.0.1", MAC: "aa:aa"})

	got := a.Observe(context.Background(), ports.NeighborObservation{IP: "10.0.0.1", MAC: "bb:bb", IsGateway: true}, at(1))
	if len(got) != 1 || got[0].Severity != event.SevError {
		t.Fatalf("severity = %v, want error - every host's outbound traffic now goes to a different machine", got)
	}
}

func TestThreeFlipsBetweenTheSameMACsIsADuplicateIP(t *testing.T) {
	a := newNeighborAnalyzer()
	a.Seed(ports.NeighborObservation{IP: "10.0.0.5", MAC: "aa:aa"})

	change := func(sec int, mac string) []*event.Event {
		return a.Observe(context.Background(), ports.NeighborObservation{IP: "10.0.0.5", MAC: mac}, at(sec))
	}
	if got := change(1, "bb:bb"); len(got) != 1 {
		t.Fatalf("change 1: kinds = %v, want just arp_binding_changed", kinds(got))
	}
	if got := change(2, "aa:aa"); len(got) != 1 {
		t.Fatalf("change 2: kinds = %v, want just arp_binding_changed (2 flips so far)", kinds(got))
	}
	got := change(3, "bb:bb")
	if len(got) != 2 {
		t.Fatalf("change 3: kinds = %v, want arp_binding_changed + duplicate_ip", kinds(got))
	}
	if got[1].Kind != event.KindDuplicateIP {
		t.Errorf("second event = %s, want %s", got[1].Kind, event.KindDuplicateIP)
	}
	if got[1].Confidence != 80 {
		t.Errorf("confidence = %d, want 80", got[1].Confidence)
	}
}

func TestASingleChangeIsNotADuplicate(t *testing.T) {
	a := newNeighborAnalyzer()
	a.Seed(ports.NeighborObservation{IP: "10.0.0.5", MAC: "aa:aa"})
	got := a.Observe(context.Background(), ports.NeighborObservation{IP: "10.0.0.5", MAC: "bb:bb"}, at(1))
	if len(got) != 1 {
		t.Fatalf("kinds = %v, want just arp_binding_changed for one handover", kinds(got))
	}
}

func TestChangesOutsideTheWindowDoNotAccumulateTowardsDuplicate(t *testing.T) {
	a := newNeighborAnalyzer()
	a.Seed(ports.NeighborObservation{IP: "10.0.0.5", MAC: "aa:aa"})
	a.Observe(context.Background(), ports.NeighborObservation{IP: "10.0.0.5", MAC: "bb:bb"}, at(0))
	a.Observe(context.Background(), ports.NeighborObservation{IP: "10.0.0.5", MAC: "aa:aa"}, at(1))

	// Far outside DuplicateWindow (60s): the earlier two changes must have
	// aged out, so this third one alone must not trigger duplicate_ip.
	got := a.Observe(context.Background(), ports.NeighborObservation{IP: "10.0.0.5", MAC: "bb:bb"}, at(int((DuplicateWindow + 10*time.Second).Seconds())))
	if len(got) != 1 {
		t.Fatalf("kinds = %v, want just arp_binding_changed - earlier changes should have aged out of the window", kinds(got))
	}
}

func TestTheSameMACOnADifferentInterfaceIsMacMoved(t *testing.T) {
	a := newNeighborAnalyzer()
	a.Seed(ports.NeighborObservation{IP: "10.0.0.5", MAC: "aa:aa", LinkIndex: 2})
	got := a.Observe(context.Background(), ports.NeighborObservation{IP: "10.0.0.5", MAC: "aa:aa", LinkIndex: 3}, at(1))
	if len(got) != 1 || got[0].Kind != event.KindMACMoved {
		t.Fatalf("kinds = %v, want mac_moved", kinds(got))
	}
}

func TestAFailedEntryReportsNeighborFailed(t *testing.T) {
	a := newNeighborAnalyzer()
	a.Seed(ports.NeighborObservation{IP: "10.0.0.5", MAC: "aa:aa"})
	got := a.Observe(context.Background(), ports.NeighborObservation{IP: "10.0.0.5", Failed: true, NUDState: "failed"}, at(1))
	if len(got) != 1 || got[0].Kind != event.KindNeighborFailed {
		t.Fatalf("kinds = %v, want neighbor_failed", kinds(got))
	}
	if a.Known() != 0 {
		t.Errorf("Known() = %d after a failure, want 0", a.Known())
	}
}

func TestAFailureForAnUnknownAddressProducesNoEvent(t *testing.T) {
	a := newNeighborAnalyzer()
	got := a.Observe(context.Background(), ports.NeighborObservation{IP: "10.0.0.9", Failed: true}, at(0))
	if len(got) != 0 {
		t.Errorf("kinds = %v, want none - nothing was ever known about this address", kinds(got))
	}
}

func TestAnUnchangedObservationProducesNoEvent(t *testing.T) {
	a := newNeighborAnalyzer()
	a.Seed(ports.NeighborObservation{IP: "10.0.0.5", MAC: "aa:aa", LinkIndex: 2})
	got := a.Observe(context.Background(), ports.NeighborObservation{IP: "10.0.0.5", MAC: "aa:aa", LinkIndex: 2}, at(1))
	if len(got) != 0 {
		t.Errorf("kinds = %v, want none for an identical re-announcement", kinds(got))
	}
}
