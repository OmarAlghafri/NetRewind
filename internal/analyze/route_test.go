package analyze

import (
	"testing"

	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/ports"
)

func newRouteAnalyzer() *RouteAnalyzer {
	return NewRouteAnalyzer(event.NewBuilder("test", nil))
}

func TestANewRouteToAFreshDestinationIsRouteAdded(t *testing.T) {
	a := newRouteAnalyzer()
	got := a.Observe(ports.RouteObservation{Prefix: "10.0.0.0/24", Gateway: "10.0.0.1", LinkIndex: 2, Priority: 100})
	if len(got) != 1 || got[0].Kind != event.KindRouteAdded {
		t.Fatalf("kinds = %v, want route_added", kinds(got))
	}
}

func TestALowerMetricAlternativeDoesNotChangeTheWinner(t *testing.T) {
	a := newRouteAnalyzer()
	a.Seed(ports.RouteObservation{Prefix: "10.0.0.0/24", Gateway: "10.0.0.1", LinkIndex: 2, Priority: 100})

	// A standby route at a WORSE (higher) metric must not be reported: the
	// traffic does not move.
	got := a.Observe(ports.RouteObservation{Prefix: "10.0.0.0/24", Gateway: "10.0.0.2", LinkIndex: 3, Priority: 200})
	if len(got) != 0 {
		t.Fatalf("kinds = %v, want none - the better route still wins", kinds(got))
	}
}

func TestLosingTheWinningRouteToAStandbyIsRouteChanged(t *testing.T) {
	a := newRouteAnalyzer()
	a.Seed(ports.RouteObservation{Prefix: "10.0.0.0/24", Gateway: "10.0.0.1", LinkIndex: 2, Priority: 100})
	a.Observe(ports.RouteObservation{Prefix: "10.0.0.0/24", Gateway: "10.0.0.2", LinkIndex: 3, Priority: 200})

	// The winning route is withdrawn; the standby now wins.
	got := a.Observe(ports.RouteObservation{Prefix: "10.0.0.0/24", Gateway: "10.0.0.1", LinkIndex: 2, Priority: 100, Removed: true})
	if len(got) != 1 || got[0].Kind != event.KindRouteChanged {
		t.Fatalf("kinds = %v, want route_changed (the standby took over)", kinds(got))
	}
	if got[0].Attrs["gateway_new"] != "10.0.0.2" {
		t.Errorf("gateway_new = %v, want 10.0.0.2", got[0].Attrs["gateway_new"])
	}
}

func TestWithdrawingTheOnlyRouteIsRouteRemoved(t *testing.T) {
	a := newRouteAnalyzer()
	a.Seed(ports.RouteObservation{Prefix: "10.0.0.0/24", Gateway: "10.0.0.1", LinkIndex: 2, Priority: 100})
	got := a.Observe(ports.RouteObservation{Prefix: "10.0.0.0/24", Gateway: "10.0.0.1", LinkIndex: 2, Priority: 100, Removed: true})
	if len(got) != 1 || got[0].Kind != event.KindRouteRemoved {
		t.Fatalf("kinds = %v, want route_removed", kinds(got))
	}
}

func TestLosingTheDefaultRouteIsSeverityError(t *testing.T) {
	a := newRouteAnalyzer()
	a.Seed(ports.RouteObservation{Prefix: "default", IsDefault: true, Gateway: "10.0.0.1", Priority: 100})
	got := a.Observe(ports.RouteObservation{Prefix: "default", IsDefault: true, Gateway: "10.0.0.1", Priority: 100, Removed: true})
	if len(got) != 1 || got[0].Severity != event.SevError {
		t.Fatalf("severity = %v, want error - losing the default route loses everything beyond the local segment", got)
	}
}

func TestLosingAnOrdinaryRouteIsOnlyNotice(t *testing.T) {
	a := newRouteAnalyzer()
	a.Seed(ports.RouteObservation{Prefix: "10.0.0.0/24", Gateway: "10.0.0.1", Priority: 100})
	got := a.Observe(ports.RouteObservation{Prefix: "10.0.0.0/24", Gateway: "10.0.0.1", Priority: 100, Removed: true})
	if len(got) != 1 || got[0].Severity != event.SevNotice {
		t.Fatalf("severity = %v, want notice for a non-default route", got)
	}
}

func TestTheDefaultRouteMovingIsDefaultRouteChanged(t *testing.T) {
	a := newRouteAnalyzer()
	a.Seed(ports.RouteObservation{Prefix: "default", IsDefault: true, Gateway: "10.0.0.1", Priority: 100})
	got := a.Observe(ports.RouteObservation{Prefix: "default", IsDefault: true, Gateway: "10.0.0.2", Priority: 100, Replace: true})
	if len(got) != 1 || got[0].Kind != event.KindDefaultRouteChanged {
		t.Fatalf("kinds = %v, want default_route_changed", kinds(got))
	}
	if got[0].Severity != event.SevError {
		t.Errorf("severity = %s, want error for the default route", got[0].Severity)
	}
}

func TestAnOrdinaryRouteRepointedIsRouteChangedNotDefault(t *testing.T) {
	a := newRouteAnalyzer()
	a.Seed(ports.RouteObservation{Prefix: "10.0.0.0/24", Gateway: "10.0.0.1", Priority: 100})
	got := a.Observe(ports.RouteObservation{Prefix: "10.0.0.0/24", Gateway: "10.0.0.2", Priority: 100, Replace: true})
	if len(got) != 1 || got[0].Kind != event.KindRouteChanged {
		t.Fatalf("kinds = %v, want route_changed", kinds(got))
	}
}

func TestAReplaceAtADifferentMetricDoesNotSupersedeTheOriginal(t *testing.T) {
	// supersede matches on priority alone: a replace at a NEW metric adds an
	// alternative rather than replacing the existing winner.
	a := newRouteAnalyzer()
	a.Seed(ports.RouteObservation{Prefix: "10.0.0.0/24", Gateway: "10.0.0.1", Priority: 100})
	got := a.Observe(ports.RouteObservation{Prefix: "10.0.0.0/24", Gateway: "10.0.0.2", Priority: 50, Replace: true})
	if len(got) != 1 || got[0].Kind != event.KindRouteChanged {
		t.Fatalf("kinds = %v, want route_changed (the new, better metric wins)", kinds(got))
	}
	if got[0].Attrs["alternatives"] != 2 {
		t.Errorf("alternatives = %v, want 2 - the old route at metric 100 should still be tracked as a standby", got[0].Attrs["alternatives"])
	}
}

func TestAnUnchangedWinnerProducesNoEvent(t *testing.T) {
	a := newRouteAnalyzer()
	a.Seed(ports.RouteObservation{Prefix: "10.0.0.0/24", Gateway: "10.0.0.1", LinkIndex: 2, Priority: 100})
	got := a.Observe(ports.RouteObservation{Prefix: "10.0.0.0/24", Gateway: "10.0.0.1", LinkIndex: 2, Priority: 100})
	if len(got) != 0 {
		t.Errorf("kinds = %v, want none for a re-announcement of the same winning route", kinds(got))
	}
}

func TestObserveAddressAdded(t *testing.T) {
	b := event.NewBuilder("test", nil)
	e := ObserveAddress(b, ports.AddressObservation{IP: "192.168.1.5", CIDR: "192.168.1.5/24", LinkIndex: 2})
	if e.Kind != event.KindAddrAdded || e.Severity != event.SevInfo {
		t.Errorf("kind/severity = %s/%s, want addr_added/info", e.Kind, e.Severity)
	}
}

func TestObserveAddressRemoved(t *testing.T) {
	b := event.NewBuilder("test", nil)
	e := ObserveAddress(b, ports.AddressObservation{IP: "192.168.1.5", CIDR: "192.168.1.5/24", LinkIndex: 2, Removed: true})
	if e.Kind != event.KindAddrRemoved || e.Severity != event.SevNotice {
		t.Errorf("kind/severity = %s/%s, want addr_removed/notice", e.Kind, e.Severity)
	}
}
