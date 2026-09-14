//go:build linux

package netlink

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"github.com/OmarAlghafri/netrewind/internal/analyze"
	"github.com/OmarAlghafri/netrewind/internal/collect"
	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/ports"
	nl "github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// RouteCollector watches the main routing table.
//
// It deliberately ignores every other table. The local table is rewritten
// constantly by the kernel and would bury the handful of changes that
// matter under thousands that do not - so "is this the main table" is
// filtered here, in the adapter, before internal/analyze.RouteAnalyzer ever
// sees a notification.
//
// The decision logic - which of several routes to a destination currently
// wins, what counts as added/removed/changed - lives in
// internal/analyze.RouteAnalyzer.
type RouteCollector struct {
	b        *event.Builder
	log      *slog.Logger
	analyzer *analyze.RouteAnalyzer
}

// NewRouteCollector returns a collector for routing changes.
func NewRouteCollector(b *event.Builder, log *slog.Logger) *RouteCollector {
	return &RouteCollector{b: b, log: log, analyzer: analyze.NewRouteAnalyzer(b)}
}

// Name implements collect.Collector.
func (c *RouteCollector) Name() string { return "netlink.route" }

// Run seeds the current routing table, then reports every change to it.
func (c *RouteCollector) Run(ctx context.Context, out chan<- *event.Event) error {
	routes, err := nl.RouteList(nil, unix.AF_UNSPEC)
	if err != nil {
		return fmt.Errorf("netlink: list routes: %w", err)
	}
	for _, r := range routes {
		if !isMainTable(r.Table) {
			continue
		}
		c.analyzer.Seed(observationOf(r, false, false))
	}

	updates := make(chan nl.RouteUpdate, 256)
	done := make(chan struct{})
	defer close(done)

	reporter := newOverflowReporter(c.b, c.log, c.Name())
	reporter.bind(ctx, out)

	opts := nl.RouteSubscribeOptions{ErrorCallback: reporter.callback}
	if err := nl.RouteSubscribeWithOptions(updates, done, opts); err != nil {
		return fmt.Errorf("netlink: subscribe to route updates: %w", err)
	}

	c.log.Info("watching the routing table", "collector", c.Name(), "seeded", c.analyzer.Known())

	for {
		select {
		case <-ctx.Done():
			return nil
		case u, ok := <-updates:
			if !ok {
				return fmt.Errorf("netlink: route update channel closed")
			}
			for _, e := range c.diff(u) {
				if !collect.Emit(ctx, out, e) {
					return nil
				}
			}
		}
	}
}

func (c *RouteCollector) diff(u nl.RouteUpdate) []*event.Event {
	if !isMainTable(u.Table) {
		return nil
	}
	removed := u.Type == unix.RTM_DELROUTE
	replace := u.NlFlags&unix.NLM_F_REPLACE != 0
	return c.analyzer.Observe(observationOf(u.Route, removed, replace))
}

// isMainTable reports whether a route belongs to the table this collector
// watches. Netlink reports the main table as either RT_TABLE_MAIN or 0.
func isMainTable(table int) bool {
	return table == unix.RT_TABLE_MAIN || table == 0
}

func observationOf(r nl.Route, removed, replace bool) ports.RouteObservation {
	gw := ""
	if r.Gw != nil {
		gw = r.Gw.String()
	}
	return ports.RouteObservation{
		Prefix:    routeKey(r),
		IsDefault: isDefaultRoute(r),
		Gateway:   gw,
		LinkIndex: r.LinkIndex,
		Priority:  r.Priority,
		Removed:   removed,
		Replace:   replace,
	}
}

// AddrCollector watches addresses being added to and removed from
// interfaces. A DHCP lease changing, a failover moving a virtual address, or
// an interface silently losing its address all appear here first.
type AddrCollector struct {
	b   *event.Builder
	log *slog.Logger
}

// NewAddrCollector returns a collector for interface addresses.
func NewAddrCollector(b *event.Builder, log *slog.Logger) *AddrCollector {
	return &AddrCollector{b: b, log: log}
}

// Name implements collect.Collector.
func (c *AddrCollector) Name() string { return "netlink.addr" }

// Run reports address changes until ctx is done. Stateless, like
// internal/analyze.ObserveAddress: an address either arrived or left, and
// the notification already says which, with nothing to diff against.
func (c *AddrCollector) Run(ctx context.Context, out chan<- *event.Event) error {
	updates := make(chan nl.AddrUpdate, 256)
	done := make(chan struct{})
	defer close(done)

	reporter := newOverflowReporter(c.b, c.log, c.Name())
	reporter.bind(ctx, out)

	opts := nl.AddrSubscribeOptions{ErrorCallback: reporter.callback}
	if err := nl.AddrSubscribeWithOptions(updates, done, opts); err != nil {
		return fmt.Errorf("netlink: subscribe to address updates: %w", err)
	}

	c.log.Info("watching interface addresses", "collector", c.Name())

	for {
		select {
		case <-ctx.Done():
			return nil
		case u, ok := <-updates:
			if !ok {
				return fmt.Errorf("netlink: address update channel closed")
			}
			// Link-local addresses appear and vanish with every interface
			// transition and belong to no machine an operator asks about.
			// Recording them would bury the addressing changes that matter.
			if u.LinkAddress.IP.IsLinkLocalUnicast() || u.LinkAddress.IP.IsLinkLocalMulticast() {
				continue
			}
			e := analyze.ObserveAddress(c.b, ports.AddressObservation{
				IP:        u.LinkAddress.IP.String(),
				CIDR:      u.LinkAddress.String(),
				LinkIndex: u.LinkIndex,
				Removed:   !u.NewAddr,
			})
			if !collect.Emit(ctx, out, e) {
				return nil
			}
		}
	}
}

// routeKey names a route by its destination, which is what an operator asks
// about. The default route is spelled the way `ip route` spells it.
func routeKey(r nl.Route) string {
	if isDefaultRoute(r) {
		return "default"
	}
	return r.Dst.String()
}

// isDefaultRoute reports whether a route covers everything: netlink expresses
// that as either a nil destination or an explicit zero-length prefix, and both
// turn up in practice.
func isDefaultRoute(r nl.Route) bool {
	if r.Dst == nil {
		return true
	}
	ones, _ := r.Dst.Mask.Size()
	return ones == 0 && r.Dst.IP.Equal(net.IPv4zero) || ones == 0 && r.Dst.IP.Equal(net.IPv6zero)
}

var (
	_ collect.Collector = (*RouteCollector)(nil)
	_ collect.Collector = (*AddrCollector)(nil)
)
