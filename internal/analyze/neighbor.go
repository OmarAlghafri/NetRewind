package analyze

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/identity"
	"github.com/OmarAlghafri/netrewind/internal/ports"
)

const (
	// DuplicateWindow is how long a run of binding changes is examined for
	// the flip-flopping that means two machines are claiming one address.
	DuplicateWindow = 60 * time.Second
	// DuplicateChanges is how many changes inside that window it takes.
	DuplicateChanges = 3
)

type neighborState struct {
	mac       string
	linkIndex int
	changes   []neighborChange
}

type neighborChange struct {
	at  time.Time
	mac string
}

// NeighborAnalyzer turns ports.NeighborObservation into the l2.* events:
// arp_binding_new, arp_binding_changed, mac_moved, duplicate_ip,
// neighbor_failed. This is the single highest-value family in the real lab
// evidence (docs/evidence/) - almost every headline incident
// (gateway-hijack, contested-address, change-broke-a-path) starts here.
//
// Identity resolution (internal/identity) is consulted directly rather than
// abstracted behind a port: it is already platform-neutral, so there is
// nothing here for an adapter to translate. A nil resolver means events are
// recorded without a stable host identity, matching the original collector.
type NeighborAnalyzer struct {
	b   *event.Builder
	log *slog.Logger
	ids *identity.Resolver

	state map[string]*neighborState // keyed by IP; single-goroutine access, same as the collector this replaces
}

// NewNeighborAnalyzer returns an analyzer with no bindings known yet.
func NewNeighborAnalyzer(b *event.Builder, log *slog.Logger, ids *identity.Resolver) *NeighborAnalyzer {
	return &NeighborAnalyzer{b: b, log: log, ids: ids, state: make(map[string]*neighborState)}
}

// Seed records a binding's current state without emitting anything.
func (a *NeighborAnalyzer) Seed(obs ports.NeighborObservation) {
	a.state[obs.IP] = &neighborState{mac: obs.MAC, linkIndex: obs.LinkIndex}
}

// Known reports how many bindings have been seeded or observed.
func (a *NeighborAnalyzer) Known() int { return len(a.state) }

// Observe compares one observation against what was last known for the same
// IP, at moment at, and returns the events the difference justifies.
func (a *NeighborAnalyzer) Observe(ctx context.Context, obs ports.NeighborObservation, at time.Time) []*event.Event {
	if obs.Failed {
		prev, known := a.state[obs.IP]
		if !known {
			return nil
		}
		delete(a.state, obs.IP)
		return []*event.Event{
			a.b.New(event.SourceNetlink, event.KindNeighborFailed, event.SevNotice,
				event.Host(obs.IP, prev.mac)).
				WithAttr("ip", obs.IP).
				WithAttr("mac", prev.mac).
				WithDedup("l2.neighbor_failed|"+obs.IP).
				WithEvidence("nud_state", obs.NUDState),
		}
	}

	prev, known := a.state[obs.IP]
	if !known {
		a.state[obs.IP] = &neighborState{mac: obs.MAC, linkIndex: obs.LinkIndex}
		e := a.b.New(event.SourceNetlink, event.KindARPBindingNew, event.SevInfo,
			event.Host(obs.IP, obs.MAC)).
			WithAttr("ip", obs.IP).
			WithAttr("mac", obs.MAC).
			WithAttr("ifindex", obs.LinkIndex).
			WithAttr("is_gateway", obs.IsGateway).
			WithDedup("l2.arp_binding_new|" + obs.IP)
		a.stampIdentity(ctx, e, obs.IP, obs.MAC, at)

		// Severity is decided after the identity table has been consulted,
		// because whether this address has been seen before on other
		// hardware is the whole question. See FirstSightingSeverity.
		changed, _ := e.Attrs["address_changed_hands"].(bool)
		e.Severity = FirstSightingSeverity(changed, obs.IsGateway)
		return []*event.Event{e}
	}

	var events []*event.Event

	if prev.mac != obs.MAC {
		sev := event.SevWarn
		if obs.IsGateway {
			// The gateway's hardware address changing under a live network
			// is either a failover or an attack. Both need looking at now.
			sev = event.SevError
		}
		e := a.b.New(event.SourceNetlink, event.KindARPBindingChanged, sev,
			event.Host(obs.IP, obs.MAC)).
			WithAttr("ip", obs.IP).
			WithAttr("mac_old", prev.mac).
			WithAttr("mac_new", obs.MAC).
			WithAttr("is_gateway", obs.IsGateway).
			WithEvidence("nud_state", obs.NUDState).
			WithEvidence("ifindex", obs.LinkIndex)
		a.stampIdentity(ctx, e, obs.IP, obs.MAC, at)
		events = append(events, e)

		prev.changes = appendNeighborChange(prev.changes, neighborChange{at: at, mac: obs.MAC})
		if dup, macs := looksDuplicated(prev.changes); dup {
			events = append(events,
				a.b.New(event.SourceNetlink, event.KindDuplicateIP, event.SevError,
					event.Host(obs.IP, obs.MAC)).
					WithAttr("ip", obs.IP).
					WithAttr("claimants", macs).
					WithAttr("changes_in_window", len(prev.changes)).
					WithConfidence(80).
					WithDedup("l2.duplicate_ip|"+obs.IP).
					WithEvidence("window_seconds", int(DuplicateWindow.Seconds())))
		}
	}

	// The same hardware address answering from behind a different interface
	// means the machine moved, or something is impersonating it.
	if prev.mac == obs.MAC && prev.linkIndex != obs.LinkIndex && prev.linkIndex != 0 {
		events = append(events,
			a.b.New(event.SourceNetlink, event.KindMACMoved, event.SevWarn,
				event.Host(obs.IP, obs.MAC)).
				WithAttr("mac", obs.MAC).
				WithAttr("ifindex_old", prev.linkIndex).
				WithAttr("ifindex_new", obs.LinkIndex).
				WithDedup(fmt.Sprintf("l2.mac_moved|%s|%d", obs.MAC, obs.LinkIndex)))
	}

	prev.mac = obs.MAC
	prev.linkIndex = obs.LinkIndex
	return events
}

// stampIdentity attaches the stable host this observation belongs to, so the
// event can still be found after the address moves elsewhere. A resolution
// failure is logged and otherwise ignored - the event is still worth keeping
// without a resolved identity, matching the collector this replaces.
func (a *NeighborAnalyzer) stampIdentity(ctx context.Context, e *event.Event, ip, mac string, at time.Time) {
	if a.ids == nil {
		return
	}
	attrType := identity.AttrIPv4
	if net.ParseIP(ip).To4() == nil {
		attrType = identity.AttrIPv6
	}
	res, err := a.ids.Observe(ctx, identity.Observation{
		At: at,
		Attrs: []identity.Attr{
			{Type: identity.AttrMAC, Value: mac},
			{Type: attrType, Value: ip},
		},
	})
	if err != nil {
		if a.log != nil {
			a.log.Warn("identity resolution failed", "ip", ip, "mac", mac, "err", err)
		}
		return
	}
	e.Subject.ID = res.HostID
	if res.Displaced {
		e.WithAttr("address_changed_hands", true)
	}
}

func appendNeighborChange(history []neighborChange, c neighborChange) []neighborChange {
	history = append(history, c)
	cutoff := c.at.Add(-DuplicateWindow)
	kept := history[:0]
	for _, h := range history {
		if h.at.After(cutoff) {
			kept = append(kept, h)
		}
	}
	return kept
}

// looksDuplicated reports whether a run of binding changes is the
// flip-flopping that two machines claiming one address produces. One change
// is a handover; several between the same few hardware addresses is a
// conflict.
func looksDuplicated(history []neighborChange) (bool, []string) {
	if len(history) < DuplicateChanges {
		return false, nil
	}
	seen := make(map[string]bool, len(history))
	macs := make([]string, 0, len(history))
	for _, h := range history {
		if !seen[h.mac] {
			seen[h.mac] = true
			macs = append(macs, h.mac)
		}
	}
	return len(macs) >= 2, macs
}
