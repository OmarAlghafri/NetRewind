//go:build linux

package netlink

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/analyze"
	"github.com/OmarAlghafri/netrewind/internal/collect"
	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/identity"
	"github.com/OmarAlghafri/netrewind/internal/ports"
	nl "github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// gatewayCacheTTL bounds how stale the collector's idea of the default
// gateway may be. Knowing which address is the gateway is what separates a
// routine DHCP churn from a redirected network.
const gatewayCacheTTL = 30 * time.Second

// NeighCollector watches the kernel neighbour table: which hardware address
// is answering for which IP address.
//
// This is where the most valuable single event in the system comes from. An
// ARP binding changing under a running network is how a gateway gets
// hijacked, how a duplicate address announces itself, and how a failover
// quietly happens - and almost nothing records it.
//
// The decision logic - what counts as a change, duplicate detection, MAC
// movement, severity - lives in internal/analyze.NeighborAnalyzer. This
// type's job is translating netlink's vocabulary into
// internal/ports.NeighborObservation, including the one thing only netlink
// plumbing can answer here: whether an address is the current default
// gateway, which needs a route-table read.
type NeighCollector struct {
	b        *event.Builder
	log      *slog.Logger
	analyzer *analyze.NeighborAnalyzer

	mu          sync.Mutex
	gateways    map[string]bool
	gatewayseen time.Time
}

// NewNeighCollector returns a collector for the neighbour table. The
// resolver may be nil, in which case events are recorded without a stable
// host identity.
func NewNeighCollector(b *event.Builder, log *slog.Logger, ids *identity.Resolver) *NeighCollector {
	return &NeighCollector{
		b:        b,
		log:      log,
		analyzer: analyze.NewNeighborAnalyzer(b, log, ids),
		gateways: make(map[string]bool),
	}
}

// Name implements collect.Collector.
func (c *NeighCollector) Name() string { return "netlink.neigh" }

// Run seeds the current neighbour table, then reports every change to it.
func (c *NeighCollector) Run(ctx context.Context, out chan<- *event.Event) error {
	if err := c.seed(); err != nil {
		return err
	}

	updates := make(chan nl.NeighUpdate, 512)
	done := make(chan struct{})
	defer close(done)

	// An overrun here is the kernel throwing away neighbour changes - the
	// exact moment worth recording is when a storm of them is most likely to
	// outrun the socket.
	reporter := newOverflowReporter(c.b, c.log, c.Name())
	reporter.bind(ctx, out)

	opts := nl.NeighSubscribeOptions{ErrorCallback: reporter.callback}
	if err := nl.NeighSubscribeWithOptions(updates, done, opts); err != nil {
		return fmt.Errorf("netlink: subscribe to neighbour updates: %w", err)
	}

	c.log.Info("watching the neighbour table", "collector", c.Name(), "seeded", c.analyzer.Known())

	for {
		select {
		case <-ctx.Done():
			return nil
		case u, ok := <-updates:
			if !ok {
				return fmt.Errorf("netlink: neighbour update channel closed")
			}
			for _, e := range c.diff(ctx, u) {
				if !collect.Emit(ctx, out, e) {
					return nil
				}
			}
		}
	}
}

func (c *NeighCollector) seed() error {
	neighs, err := nl.NeighList(0, unix.AF_UNSPEC)
	if err != nil {
		return fmt.Errorf("netlink: list neighbours: %w", err)
	}
	for _, n := range neighs {
		if !usable(n) {
			continue
		}
		c.analyzer.Seed(ports.NeighborObservation{
			IP: n.IP.String(), MAC: n.HardwareAddr.String(), LinkIndex: n.LinkIndex,
		})
	}
	return nil
}

// diff translates one notification into an observation and asks the
// analyzer what it means. The neighbour table is noisy: entries go stale and
// are revalidated constantly, and most notifications produce nothing.
func (c *NeighCollector) diff(ctx context.Context, u nl.NeighUpdate) []*event.Event {
	if u.IP == nil || u.IP.IsUnspecified() {
		return nil
	}
	ip := u.IP.String()
	now := time.Now()

	// The entry was removed, or the neighbour stopped answering ARP at all.
	// Either way the address is no longer reachable at layer 2.
	if u.Type == unix.RTM_DELNEIGH || u.State&unix.NUD_FAILED != 0 {
		return c.analyzer.Observe(ctx, ports.NeighborObservation{
			IP: ip, Failed: true, NUDState: nudString(u.State),
		}, now)
	}

	if !usable(u.Neigh) {
		return nil
	}
	return c.analyzer.Observe(ctx, ports.NeighborObservation{
		IP: ip, MAC: u.HardwareAddr.String(), LinkIndex: u.LinkIndex,
		IsGateway: c.isGateway(ip), NUDState: nudString(u.State),
	}, now)
}

// isGateway reports whether an address is a current default gateway, caching
// the answer briefly so that a burst of ARP changes does not mean a burst of
// route table reads.
func (c *NeighCollector) isGateway(ip string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if time.Since(c.gatewayseen) > gatewayCacheTTL {
		c.gateways = make(map[string]bool)
		routes, err := nl.RouteList(nil, unix.AF_UNSPEC)
		if err == nil {
			for _, r := range routes {
				if isDefaultRoute(r) && r.Gw != nil {
					c.gateways[r.Gw.String()] = true
				}
			}
			c.gatewayseen = time.Now()
		}
	}
	return c.gateways[ip]
}

// usable filters out neighbour entries that carry no useful binding: entries
// still being resolved, entries for interfaces that do not use ARP, and the
// multicast and broadcast addresses that are not machines at all.
func usable(n nl.Neigh) bool {
	if n.IP == nil || len(n.HardwareAddr) == 0 {
		return false
	}
	if n.State&(unix.NUD_INCOMPLETE|unix.NUD_FAILED|unix.NUD_NOARP) != 0 {
		return false
	}
	if n.IP.IsMulticast() || n.IP.IsLinkLocalMulticast() {
		return false
	}
	// All-zero and broadcast hardware addresses are placeholders, not hosts.
	zero, bcast := true, true
	for _, b := range n.HardwareAddr {
		if b != 0x00 {
			zero = false
		}
		if b != 0xff {
			bcast = false
		}
	}
	return !zero && !bcast
}

func nudString(state int) string {
	switch {
	case state&unix.NUD_REACHABLE != 0:
		return "reachable"
	case state&unix.NUD_STALE != 0:
		return "stale"
	case state&unix.NUD_DELAY != 0:
		return "delay"
	case state&unix.NUD_PROBE != 0:
		return "probe"
	case state&unix.NUD_FAILED != 0:
		return "failed"
	case state&unix.NUD_PERMANENT != 0:
		return "permanent"
	case state&unix.NUD_INCOMPLETE != 0:
		return "incomplete"
	case state&unix.NUD_NOARP != 0:
		return "noarp"
	default:
		return fmt.Sprintf("state(0x%x)", state)
	}
}

var _ collect.Collector = (*NeighCollector)(nil)
