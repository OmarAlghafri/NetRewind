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

	"github.com/OmarAlghafri/netrewind/internal/analyze"
	"github.com/OmarAlghafri/netrewind/internal/collect"
	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/ports"
	nl "github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// statsInterval is how often interface counters are read.
const statsInterval = 30 * time.Second

// LinkCollector watches layer-1 state: which interfaces exist, whether they
// carry, and at what MTU.
//
// It distinguishes administrative state (someone ran "ip link set down", or a
// switch port was shut) from operational state (the carrier dropped). Reporting
// them as the same "interface down" is what makes existing tools useless during
// an incident, because the two have completely different causes.
//
// The decision logic - what counts as a change, what counts as flapping, what
// an error rate crosses - lives in internal/analyze.InterfaceAnalyzer, not
// here. This type's only job is translating netlink's vocabulary
// (nl.LinkUpdate, nl.LinkOperState, RTM_DELLINK) into
// internal/ports.InterfaceObservation and handing it to the analyzer, per
// PRODUCT_RELEASE_PLAN_AR.md §4.1.
type LinkCollector struct {
	b        *event.Builder
	log      *slog.Logger
	analyzer *analyze.InterfaceAnalyzer
}

// NewLinkCollector returns a collector for interface state.
func NewLinkCollector(b *event.Builder, log *slog.Logger) *LinkCollector {
	return &LinkCollector{b: b, log: log, analyzer: analyze.NewInterfaceAnalyzer(b)}
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

	// An overrun here is the kernel throwing away changes, which is a hole in
	// the record and has to be recorded as one.
	reporter := newOverflowReporter(c.b, c.log, c.Name())
	reporter.bind(ctx, out)

	opts := nl.LinkSubscribeOptions{ErrorCallback: reporter.callback}
	if err := nl.LinkSubscribeWithOptions(updates, done, opts); err != nil {
		return fmt.Errorf("netlink: subscribe to link updates: %w", err)
	}

	c.log.Info("watching interface state", "collector", c.Name(), "seeded", c.analyzer.Known())

	// Counters are polled rather than pushed: the kernel does not notify on a
	// statistic changing, and a link degrading is a slope rather than an
	// instant.
	statsTicker := time.NewTicker(statsInterval)
	defer statsTicker.Stop()
	c.readStats(time.Now())

	// operBefore remembers the raw operstate string per index across one
	// notification to the next, purely so an emitted event can cite what the
	// kernel actually reported (evidence), the way it always has - the
	// analyzer only ever sees the boolean OperUp the analysis needs.
	operBefore := make(map[int]string)

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
			for _, e := range c.diff(u, operBefore) {
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
		c.analyzer.Seed(observationFrom(a))
	}
	return nil
}

// observationFrom translates netlink's view of an interface into the
// platform-neutral shape internal/analyze decides on. isOperUp's OperUnknown
// exception belongs here, at the boundary, rather than in the analyzer: many
// virtual interfaces - veth, tun, most of what a lab topology is made of -
// never report anything but OperUnknown, and that is a netlink quirk, not a
// fact about what "up" means in general.
func observationFrom(a *nl.LinkAttrs) ports.InterfaceObservation {
	return ports.InterfaceObservation{
		Index:   a.Index,
		Name:    a.Name,
		AdminUp: a.Flags&net.FlagUp != 0,
		OperUp:  isOperUp(a.OperState),
		MTU:     a.MTU,
	}
}

// diff translates one notification into an observation, asks the analyzer
// what it means, and enriches whatever events come back with the netlink-
// specific evidence (the raw operstate string, the raw flags word) that the
// analyzer itself has no vocabulary for, since it must mean the same thing
// for an adapter that is not netlink at all.
func (c *LinkCollector) diff(u nl.LinkUpdate, operBefore map[int]string) []*event.Event {
	if u.Link == nil {
		return nil
	}
	a := u.Link.Attrs()
	if a == nil {
		return nil
	}

	before := operBefore[a.Index]
	operBefore[a.Index] = a.OperState.String()

	if u.Header.Type == unix.RTM_DELLINK {
		delete(operBefore, a.Index)
		events := c.analyzer.Observe(ports.InterfaceObservation{
			Index: a.Index, Name: a.Name, Removed: true,
		}, time.Now())
		for _, e := range events {
			e.WithEvidence("netlink_type", "RTM_DELLINK")
		}
		return events
	}

	obs := observationFrom(a)
	events := c.analyzer.Observe(obs, time.Now())
	for _, e := range events {
		if e.Kind == event.KindLinkDown || e.Kind == event.KindLinkUp {
			e.WithAttr("oper_state", a.OperState.String()).
				WithEvidence("oper_state_before", before).
				WithEvidence("raw_flags", a.RawFlags)
		}
	}
	return events
}

// isOperUp treats OperUnknown as up. Many virtual interfaces - veth, tun, and
// most of what a lab topology is made of - never report anything else, and
// calling them all down would drown the timeline in false outages.
func isOperUp(s nl.LinkOperState) bool {
	return s == nl.OperUp || s == nl.OperUnknown
}

var _ collect.Collector = (*LinkCollector)(nil)
