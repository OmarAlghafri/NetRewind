//go:build linux

package netlink

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"github.com/OmarAlghafri/netrewind/internal/collect"
	"github.com/OmarAlghafri/netrewind/internal/event"
	nl "github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// RouteCollector watches the main routing table.
//
// It deliberately ignores every other table. The local table is rewritten
// constantly by the kernel and would bury the handful of changes that matter
// under thousands that do not.
type RouteCollector struct {
	b   *event.Builder
	log *slog.Logger
	// routes holds every route to a destination, not just one.
	//
	// A destination routinely has several routes with different metrics -
	// that is how failover and multipath are expressed - and what an operator
	// actually cares about is which of them currently wins. Collapsing them to
	// one entry would report a standby route being installed as though the
	// traffic had moved, and miss it when the traffic really did.
	routes map[string][]routeState
}

type routeState struct {
	gw        string
	linkIndex int
	priority  int
}

// id distinguishes two routes to the same destination.
func (r routeState) id() string {
	return fmt.Sprintf("%s|%d|%d", r.gw, r.linkIndex, r.priority)
}

// NewRouteCollector returns a collector for routing changes.
func NewRouteCollector(b *event.Builder, log *slog.Logger) *RouteCollector {
	return &RouteCollector{b: b, log: log, routes: make(map[string][]routeState)}
}

// winner returns the route the kernel will actually use for a destination: the
// one with the lowest metric.
func winner(routes []routeState) (routeState, bool) {
	if len(routes) == 0 {
		return routeState{}, false
	}
	best := routes[0]
	for _, r := range routes[1:] {
		if r.priority < best.priority {
			best = r
		}
	}
	return best, true
}

func upsert(routes []routeState, r routeState) []routeState {
	for i, existing := range routes {
		if existing.id() == r.id() {
			routes[i] = r
			return routes
		}
	}
	return append(routes, r)
}

// supersede drops the routes a replace makes obsolete.
//
// The kernel identifies a route for replacement by destination, table and
// metric - not by where it points. Re-pointing a prefix at a different next hop
// or a different interface is precisely the common case, so matching on the
// interface here would leave the old path in place and the change unreported.
func supersede(routes []routeState, r routeState) []routeState {
	out := routes[:0]
	for _, existing := range routes {
		if existing.priority != r.priority {
			out = append(out, existing)
		}
	}
	return out
}

func remove(routes []routeState, r routeState) []routeState {
	out := routes[:0]
	for _, existing := range routes {
		if existing.id() != r.id() {
			out = append(out, existing)
		}
	}
	return out
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
		if r.Table != unix.RT_TABLE_MAIN && r.Table != 0 {
			continue
		}
		key := routeKey(r)
		c.routes[key] = upsert(c.routes[key], stateOf(r))
	}

	updates := make(chan nl.RouteUpdate, 256)
	done := make(chan struct{})
	defer close(done)

	opts := nl.RouteSubscribeOptions{
		ErrorCallback: func(err error) {
			c.log.Warn("netlink route subscription error", "err", err)
		},
	}
	if err := nl.RouteSubscribeWithOptions(updates, done, opts); err != nil {
		return fmt.Errorf("netlink: subscribe to route updates: %w", err)
	}

	c.log.Info("watching the routing table", "collector", c.Name(), "seeded", len(c.routes))

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
	if u.Table != unix.RT_TABLE_MAIN && u.Table != 0 {
		return nil
	}
	key := routeKey(u.Route)
	def := isDefaultRoute(u.Route)
	subject := event.Subnet(key)
	cur := stateOf(u.Route)

	before, hadAny := winner(c.routes[key])

	switch {
	case u.Type == unix.RTM_DELROUTE:
		c.routes[key] = remove(c.routes[key], cur)
	case u.NlFlags&unix.NLM_F_REPLACE != 0:
		// A replace supersedes the route with the same destination and metric
		// rather than joining it. Routing daemons replace constantly - it is
		// how FRR installs a recomputed path - so treating it as an addition
		// would leave the collector holding paths that no longer exist.
		c.routes[key] = upsert(supersede(c.routes[key], cur), cur)
	default:
		c.routes[key] = upsert(c.routes[key], cur)
	}
	after, hasAny := winner(c.routes[key])

	switch {
	case !hadAny && hasAny:
		return []*event.Event{
			c.b.New(event.SourceNetlink, event.KindRouteAdded, event.SevInfo, subject).
				WithAttr("prefix", key).
				WithAttr("gateway", after.gw).
				WithAttr("ifindex", after.linkIndex).
				WithAttr("is_default", def).
				WithDedup("l3.route_added|" + key),
		}

	case hadAny && !hasAny:
		delete(c.routes, key)
		// Losing the default route is losing everything beyond the local
		// segment, which is worth more than a notice.
		sev := event.SevNotice
		if def {
			sev = event.SevError
		}
		return []*event.Event{
			c.b.New(event.SourceNetlink, event.KindRouteRemoved, sev, subject).
				WithAttr("prefix", key).
				WithAttr("gateway", before.gw).
				WithAttr("is_default", def).
				WithDedup("l3.route_removed|" + key),
		}

	case hadAny && hasAny && before != after:
		kind, sev := event.KindRouteChanged, event.SevNotice
		if def {
			// Where the default route points decides where all outbound
			// traffic goes. A silent change here is how traffic ends up
			// somewhere it should not be.
			kind, sev = event.KindDefaultRouteChanged, event.SevError
		}
		return []*event.Event{
			c.b.New(event.SourceNetlink, kind, sev, subject).
				WithAttr("prefix", key).
				WithAttr("gateway_old", before.gw).
				WithAttr("gateway_new", after.gw).
				WithAttr("ifindex_old", before.linkIndex).
				WithAttr("ifindex_new", after.linkIndex).
				WithAttr("is_default", def).
				WithAttr("alternatives", len(c.routes[key])).
				WithEvidence("metric_old", before.priority).
				WithEvidence("metric_new", after.priority),
		}
	}

	// A standby route was installed or withdrawn without changing where the
	// traffic goes. Worth knowing eventually, not worth an event now.
	return nil
}

// AddrCollector watches addresses being added to and removed from interfaces.
// A DHCP lease changing, a failover moving a virtual address, or an interface
// silently losing its address all appear here first.
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

// Run reports address changes until ctx is done.
func (c *AddrCollector) Run(ctx context.Context, out chan<- *event.Event) error {
	updates := make(chan nl.AddrUpdate, 256)
	done := make(chan struct{})
	defer close(done)

	opts := nl.AddrSubscribeOptions{
		ErrorCallback: func(err error) {
			c.log.Warn("netlink address subscription error", "err", err)
		},
	}
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
			addr := u.LinkAddress.String()
			kind, sev := event.KindAddrRemoved, event.SevNotice
			if u.NewAddr {
				kind, sev = event.KindAddrAdded, event.SevInfo
			}
			e := c.b.New(event.SourceNetlink, kind, sev, event.Host(u.LinkAddress.IP.String(), "")).
				WithAttr("address", addr).
				WithAttr("ifindex", u.LinkIndex).
				WithDedup(fmt.Sprintf("%s|%s", kind, addr))
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

func stateOf(r nl.Route) routeState {
	gw := ""
	if r.Gw != nil {
		gw = r.Gw.String()
	}
	return routeState{gw: gw, linkIndex: r.LinkIndex, priority: r.Priority}
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
