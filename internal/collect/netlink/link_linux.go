//go:build linux

// Package netlink turns kernel netlink notifications into NetRewind events.
//
// This is the cheapest useful source in the system and the reason eBPF is not
// needed on day one: the kernel already broadcasts every interface, neighbour
// and route change, and those three cover the large majority of what actually
// breaks a local network.
package netlink

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/collect"
	"github.com/OmarAlghafri/netrewind/internal/event"
	nl "github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// LinkCollector watches layer-1 state: which interfaces exist, whether they
// carry, and at what MTU.
//
// It distinguishes administrative state (someone ran "ip link set down", or a
// switch port was shut) from operational state (the carrier dropped). Reporting
// them as the same "interface down" is what makes existing tools useless during
// an incident, because the two have completely different causes.
type LinkCollector struct {
	b     *event.Builder
	log   *slog.Logger
	state map[int]linkState
}

const (
	// flapWindow is how long a run of transitions is examined over.
	flapWindow = 5 * time.Minute
	// flapTransitions is how many it takes before a link is flapping rather
	// than having had an outage. One down is an event; a run of them is a
	// fault in the link itself, and no amount of restarting the interface
	// fixes a marginal cable.
	flapTransitions = 3
	// statsInterval is how often interface counters are read.
	statsInterval = 30 * time.Second
	// errorRateThreshold is the fraction of packets in error that stops being
	// ordinary. Well below this is normal on any copper link.
	errorRateThreshold = 0.01
	// minPacketsForRate avoids reporting "50% errors" from one bad packet out
	// of two on an idle interface.
	minPacketsForRate = 1000
)

type linkState struct {
	name     string
	adminUp  bool
	oper     nl.LinkOperState
	mtu      int
	lastDown time.Time
	// downs is when this interface last went down, trimmed to flapWindow.
	downs []time.Time
	// flapped keeps one flapping interface from producing a finding on every
	// further transition. Cleared once it settles.
	flapped bool
	stats   linkCounters
}

// linkCounters is the subset of interface statistics worth watching: errors
// and drops against the traffic that produced them.
type linkCounters struct {
	rxPackets, txPackets uint64
	rxErrors, txErrors   uint64
	rxDropped, txDropped uint64
	at                   time.Time
}

// NewLinkCollector returns a collector for interface state.
func NewLinkCollector(b *event.Builder, log *slog.Logger) *LinkCollector {
	return &LinkCollector{b: b, log: log, state: make(map[int]linkState)}
}

// Name implements collect.Collector.
func (c *LinkCollector) Name() string { return "netlink.link" }

// Run seeds the current interface state, then reports every transition until
// ctx is done.
//
// Seeding matters: without it the first notification for an interface looks
// like a change, and a restart would manufacture events that never happened.
func (c *LinkCollector) Run(ctx context.Context, out chan<- *event.Event) error {
	if err := c.seed(); err != nil {
		return err
	}

	updates := make(chan nl.LinkUpdate, 256)
	done := make(chan struct{})
	defer close(done)

	opts := nl.LinkSubscribeOptions{
		ErrorCallback: func(err error) {
			// A netlink read error is a hole in the record, not a fatal
			// condition. It is logged here and turned into system.drop by the
			// health collector once that exists.
			c.log.Warn("netlink link subscription error", "err", err)
		},
	}
	if err := nl.LinkSubscribeWithOptions(updates, done, opts); err != nil {
		return fmt.Errorf("netlink: subscribe to link updates: %w", err)
	}

	c.log.Info("watching interface state", "collector", c.Name(), "seeded", len(c.state))

	// Counters are polled rather than pushed: the kernel does not notify on a
	// statistic changing, and a link degrading is a slope rather than an
	// instant. Same goroutine, so the state map needs no lock.
	statsTicker := time.NewTicker(statsInterval)
	defer statsTicker.Stop()
	c.readStats(time.Now())

	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-statsTicker.C:
			for _, e := range c.compareStats(now) {
				if !collect.Emit(ctx, out, e) {
					return nil
				}
			}
		case u, ok := <-updates:
			if !ok {
				return fmt.Errorf("netlink: link update channel closed")
			}
			for _, e := range c.diff(u) {
				if !collect.Emit(ctx, out, e) {
					return nil
				}
			}
		}
	}
}

// seed records the state of every interface as it is right now, without
// emitting anything.
func (c *LinkCollector) seed() error {
	links, err := nl.LinkList()
	if err != nil {
		return fmt.Errorf("netlink: list links: %w", err)
	}
	for _, l := range links {
		a := l.Attrs()
		c.state[a.Index] = linkState{
			name:    a.Name,
			adminUp: a.Flags&net.FlagUp != 0,
			oper:    a.OperState,
			mtu:     a.MTU,
		}
	}
	return nil
}

// diff compares one notification against what we last knew and returns the
// events that the difference justifies. Netlink re-announces interfaces for
// reasons that are not state changes, so most notifications produce nothing.
func (c *LinkCollector) diff(u nl.LinkUpdate) []*event.Event {
	if u.Link == nil {
		return nil
	}
	a := u.Link.Attrs()
	if a == nil {
		return nil
	}

	prev, known := c.state[a.Index]
	subject := event.Iface(a.Name, a.Index)

	// The interface went away entirely: a VLAN was deleted, a container's veth
	// was torn down, a USB adapter was pulled.
	if u.Header.Type == unix.RTM_DELLINK {
		delete(c.state, a.Index)
		if !known {
			return nil
		}
		return []*event.Event{
			c.b.New(event.SourceNetlink, event.KindLinkDown, event.SevWarn, subject).
				WithAttr("ifname", a.Name).
				WithAttr("cause", "removed").
				WithDedup("link.down|"+a.Name).
				WithEvidence("netlink_type", "RTM_DELLINK"),
		}
	}

	cur := linkState{
		name:     a.Name,
		adminUp:  a.Flags&net.FlagUp != 0,
		oper:     a.OperState,
		mtu:      a.MTU,
		lastDown: prev.lastDown,
	}

	// First time we have seen this index: record it, but do not claim it just
	// changed. A new interface appearing is its own event, added in M1.
	if !known {
		c.state[a.Index] = cur
		return nil
	}

	var events []*event.Event

	prevOperUp := isOperUp(prev.oper)
	curOperUp := isOperUp(cur.oper)

	switch {
	case (prev.adminUp || prevOperUp) && !cur.adminUp && !curOperUp:
		now := time.Now()
		cur.lastDown = now
		cause := "carrier"
		if prev.adminUp && !cur.adminUp {
			cause = "administrative"
		}
		events = append(events,
			c.b.New(event.SourceNetlink, event.KindLinkDown, event.SevWarn, subject).
				WithAttr("ifname", a.Name).
				WithAttr("cause", cause).
				WithAttr("oper_state", cur.oper.String()).
				WithAttr("admin_up", cur.adminUp).
				WithDedup("link.down|"+a.Name).
				WithEvidence("oper_state_before", prev.oper.String()).
				WithEvidence("raw_flags", a.RawFlags))

		// A run of outages is a different finding from one outage, and an
		// administrative shutdown is somebody working rather than a link
		// failing - counting those as flapping would blame the cable for the
		// engineer.
		if cause == "carrier" {
			cur.downs = trimBefore(append(cur.downs, now), now.Add(-flapWindow))
			if len(cur.downs) >= flapTransitions && !cur.flapped {
				cur.flapped = true
				events = append(events,
					c.b.New(event.SourceNetlink, event.KindLinkFlap, event.SevError, subject).
						WithAttr("ifname", a.Name).
						WithAttr("transitions", len(cur.downs)).
						WithAttr("window_seconds", int(flapWindow.Seconds())).
						WithDedup("link.flap|"+a.Name).
						WithEvidence("first_in_window", cur.downs[0].UTC().Format(time.RFC3339)))
			}
		}

	case !prevOperUp && curOperUp:
		// It settled. If it flaps again after this, that is a fresh finding.
		if cur.flapped && len(cur.downs) > 0 &&
			time.Since(cur.downs[len(cur.downs)-1]) > flapWindow {
			cur.flapped = false
			cur.downs = nil
		}
		e := c.b.New(event.SourceNetlink, event.KindLinkUp, event.SevNotice, subject).
			WithAttr("ifname", a.Name).
			WithAttr("oper_state", cur.oper.String()).
			WithAttr("admin_up", cur.adminUp).
			WithDedup("link.up|"+a.Name).
			WithEvidence("oper_state_before", prev.oper.String())
		// How long it was down is the first thing anyone asks, and it is the
		// input to flap detection in M1.
		if !prev.lastDown.IsZero() {
			e.WithAttr("down_duration_ms", time.Since(prev.lastDown).Milliseconds())
		}
		events = append(events, e)
	}

	// An MTU change is silent, survives reboots and breaks large transfers
	// while leaving ping working - a classic multi-day outage.
	if prev.mtu != cur.mtu && prev.mtu != 0 {
		events = append(events,
			c.b.New(event.SourceNetlink, event.KindLinkMTUChanged, event.SevNotice, subject).
				WithAttr("ifname", a.Name).
				WithAttr("mtu_old", prev.mtu).
				WithAttr("mtu_new", cur.mtu).
				WithDedup(fmt.Sprintf("link.mtu|%s|%d", a.Name, cur.mtu)))
	}

	c.state[a.Index] = cur
	return events
}

// isOperUp treats OperUnknown as up. Many virtual interfaces - veth, tun, and
// most of what a lab topology is made of - never report anything else, and
// calling them all down would drown the timeline in false outages.
func isOperUp(s nl.LinkOperState) bool {
	return s == nl.OperUp || s == nl.OperUnknown
}

var _ collect.Collector = (*LinkCollector)(nil)
