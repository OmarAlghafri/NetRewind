// Package analyze holds the deterministic logic that turns ports
// observations into NetRewind events.
//
// PRODUCT_RELEASE_PLAN_AR.md §4.1: platform adapters translate their native
// notifications into internal/ports observations; everything here decides
// whether a difference is worth an event, and what kind - and does so with no
// import of any OS-specific package, so it is testable on every platform and
// against every adapter with the same fixtures.
package analyze

import (
	"fmt"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/ports"
)

const (
	// FlapWindow is how long a run of transitions is examined over.
	FlapWindow = 5 * time.Minute
	// FlapTransitions is how many down transitions inside FlapWindow turn a
	// run of outages into a flap finding. One down is an event; a run of
	// them is a fault in the link itself, and restarting the interface does
	// not fix a marginal cable.
	FlapTransitions = 3
	// ErrorRateThreshold is the fraction of packets in error or dropped that
	// stops being ordinary. Well below this is normal on any copper link.
	ErrorRateThreshold = 0.01
	// MinPacketsForRate avoids reporting "50% errors" from one bad packet out
	// of two on an idle interface.
	MinPacketsForRate = 1000
)

type interfaceState struct {
	obs      ports.InterfaceObservation
	lastDown time.Time
	// downs is when this interface last went down, trimmed to FlapWindow.
	downs []time.Time
	// flapped keeps one flapping interface from producing a finding on every
	// further transition. Cleared once it has settled for a full window.
	flapped   bool
	counters  ports.InterfaceCounters
	sampledAt time.Time
}

// InterfaceAnalyzer turns internal/ports.InterfaceObservation values into
// NetRewind link.* events. It holds no reference to any OS-specific type, and
// no wall-clock of its own: every method that needs "now" takes it as a
// parameter, so a test can drive years of flapping in a few lines with no
// sleeping and no flakiness.
type InterfaceAnalyzer struct {
	b      *event.Builder
	source event.Source
	state  map[int]*interfaceState
}

// NewInterfaceAnalyzer returns an analyzer with no interfaces known yet.
func NewInterfaceAnalyzer(b *event.Builder) *InterfaceAnalyzer {
	return &InterfaceAnalyzer{b: b, source: event.SourceNetlink, state: make(map[int]*interfaceState)}
}

// WithSource sets the event source stamped on everything this analyzer
// emits. The default is netlink; a Windows adapter passes event.SourceIPHelper
// so a reader always knows which platform API a fact came through.
func (a *InterfaceAnalyzer) WithSource(s event.Source) *InterfaceAnalyzer {
	a.source = s
	return a
}

// Seed records an interface's current state without emitting anything. An
// adapter calls this once per interface at startup, from its own equivalent
// of a link listing, so the first real notification is compared against
// reality rather than read as a change that never happened.
func (a *InterfaceAnalyzer) Seed(obs ports.InterfaceObservation) {
	a.state[obs.Index] = &interfaceState{obs: obs}
}

// Known reports how many interfaces have been seeded or observed, for a
// collector's own startup logging.
func (a *InterfaceAnalyzer) Known() int { return len(a.state) }

// Observe compares one observation against what was last known for the same
// index, at moment at, and returns the events the difference justifies.
//
// Most notifications produce nothing: netlink (and, expected of any future
// adapter) re-announces interfaces for reasons that are not state changes.
func (a *InterfaceAnalyzer) Observe(obs ports.InterfaceObservation, at time.Time) []*event.Event {
	subject := event.Iface(obs.Name, obs.Index)
	prevState, known := a.state[obs.Index]

	// The interface went away entirely: a VLAN was deleted, a container's
	// veth was torn down, a USB adapter was pulled. This is its own case
	// rather than a kind of "down", because a removed interface cannot flap,
	// cannot regain carrier, and has no MTU to compare next time.
	if obs.Removed {
		delete(a.state, obs.Index)
		if !known {
			return nil
		}
		return []*event.Event{
			a.b.New(a.source, event.KindLinkDown, event.SevWarn, subject).
				WithAttr("ifname", obs.Name).
				WithAttr("cause", "removed").
				WithDedup("link.down|"+obs.Name).
				WithEvidence("removed", true),
		}
	}

	// First time this index has been seen: record it, but do not claim it
	// just changed. An interface appearing for the first time is its own
	// event, not modelled by this analyzer.
	if !known {
		a.state[obs.Index] = &interfaceState{obs: obs}
		return nil
	}

	prev := prevState.obs
	cur := interfaceState{obs: obs, lastDown: prevState.lastDown, downs: prevState.downs, flapped: prevState.flapped,
		counters: prevState.counters, sampledAt: prevState.sampledAt}

	var events []*event.Event

	// An interface only actually carries traffic when it is both
	// administratively enabled and has carrier - either one being false is
	// enough to make it unusable, so "up" has to mean both, not either.
	//
	// The port-level tests caught a real defect in the collector this
	// analyzer replaces (internal/collect/netlink.LinkCollector.diff): its
	// down-transition required *both* signals to already be false
	// (!cur.adminUp && !curOperUp), so a plain physical carrier loss on a
	// real NIC - admin stays up, only carrier drops - could never satisfy it
	// and no link.down was ever produced. Every lab scenario and every prior
	// test toggles interfaces with "ip link set ... down/up", which changes
	// admin state too, so nothing ever exercised the case where only carrier
	// moves. Fixed here with symmetric logic: down is prev-up -> not-up,
	// exactly mirrored for up, so either signal alone is sufficient either
	// direction.
	prevUp := prev.AdminUp && prev.OperUp
	curUp := obs.AdminUp && obs.OperUp

	switch {
	case prevUp && !curUp:
		cur.lastDown = at
		// Administrative takes narrative priority: a human or a script
		// asked for this, and that is more useful to say than "carrier"
		// even if the carrier also happens to be gone as a consequence.
		cause := "carrier"
		if !obs.AdminUp {
			cause = "administrative"
		}
		events = append(events,
			a.b.New(a.source, event.KindLinkDown, event.SevWarn, subject).
				WithAttr("ifname", obs.Name).
				WithAttr("cause", cause).
				WithAttr("admin_up", obs.AdminUp).
				WithDedup("link.down|"+obs.Name).
				WithEvidence("oper_up_before", prev.OperUp))

		// A run of outages is a different finding from one outage, and an
		// administrative shutdown is somebody working rather than a link
		// failing - counting those as flapping would blame the cable for
		// the engineer.
		if cause == "carrier" {
			cur.downs = trimBefore(append(cur.downs, at), at.Add(-FlapWindow))
			if len(cur.downs) >= FlapTransitions && !cur.flapped {
				cur.flapped = true
				events = append(events,
					a.b.New(a.source, event.KindLinkFlap, event.SevError, subject).
						WithAttr("ifname", obs.Name).
						WithAttr("transitions", len(cur.downs)).
						WithAttr("window_seconds", int(FlapWindow.Seconds())).
						WithDedup("link.flap|"+obs.Name).
						WithEvidence("first_in_window", cur.downs[0].UTC().Format(time.RFC3339)))
			}
		}

	case !prevUp && curUp:
		// It settled. If it flaps again after this, that is a fresh finding.
		if cur.flapped && len(cur.downs) > 0 && at.Sub(cur.downs[len(cur.downs)-1]) > FlapWindow {
			cur.flapped = false
			cur.downs = nil
		}
		e := a.b.New(a.source, event.KindLinkUp, event.SevNotice, subject).
			WithAttr("ifname", obs.Name).
			WithAttr("admin_up", obs.AdminUp).
			WithDedup("link.up|" + obs.Name)
		// How long it was down is the first thing anyone asks, and it is the
		// input to flap detection.
		if !prevState.lastDown.IsZero() {
			e.WithAttr("down_duration_ms", at.Sub(prevState.lastDown).Milliseconds())
		}
		events = append(events, e)
	}

	// An MTU change is silent, survives reboots, and breaks large transfers
	// while leaving ping working - a classic multi-day outage. prev.MTU != 0
	// guards the very first reading, which is a baseline, not a change.
	if prev.MTU != obs.MTU && prev.MTU != 0 {
		events = append(events,
			a.b.New(a.source, event.KindLinkMTUChanged, event.SevNotice, subject).
				WithAttr("ifname", obs.Name).
				WithAttr("mtu_old", prev.MTU).
				WithAttr("mtu_new", obs.MTU).
				WithDedup(fmt.Sprintf("link.mtu|%s|%d", obs.Name, obs.MTU)))
	}

	a.state[obs.Index] = &cur
	return events
}

// CompareCounters reads a fresh counters sample for one interface and reports
// whether the error/drop rate since the last sample has crossed
// ErrorRateThreshold. Sampling, not pushing, because the kernel (and any
// platform this is ported to) does not notify on a counter changing, and a
// link degrading is a slope rather than an instant.
func (a *InterfaceAnalyzer) CompareCounters(index int, name string, counters ports.InterfaceCounters, at time.Time) []*event.Event {
	st, known := a.state[index]
	if !known {
		return nil
	}
	prev, prevAt := st.counters, st.sampledAt
	st.counters, st.sampledAt = counters, at

	if prevAt.IsZero() {
		return nil // nothing to compare against yet
	}

	// Counters reset when an interface is recreated, and a negative delta
	// read as an unsigned number becomes an enormous positive one.
	if counters.RxPackets < prev.RxPackets || counters.TxPackets < prev.TxPackets {
		return nil
	}

	packets := (counters.RxPackets - prev.RxPackets) + (counters.TxPackets - prev.TxPackets)
	errs := (counters.RxErrors - prev.RxErrors) + (counters.TxErrors - prev.TxErrors)
	dropped := (counters.RxDropped - prev.RxDropped) + (counters.TxDropped - prev.TxDropped)
	bad := errs + dropped

	if packets < MinPacketsForRate || bad == 0 {
		return nil
	}
	rate := float64(bad) / float64(packets+bad)
	if rate < ErrorRateThreshold {
		return nil
	}

	return []*event.Event{
		a.b.New(a.source, event.KindLinkErrorRate, event.SevWarn, event.Iface(name, index)).
			WithAttr("ifname", name).
			WithAttr("error_rate_percent", round2(rate*100)).
			WithAttr("errors", errs).
			WithAttr("dropped", dropped).
			WithAttr("packets", packets).
			WithAttr("window_seconds", int(at.Sub(prevAt).Seconds())).
			WithDedup("link.error_rate|"+name).
			WithEvidence("threshold_percent", ErrorRateThreshold*100),
	}
}

func trimBefore(times []time.Time, cutoff time.Time) []time.Time {
	kept := times[:0]
	for _, t := range times {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	return kept
}

func round2(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}
