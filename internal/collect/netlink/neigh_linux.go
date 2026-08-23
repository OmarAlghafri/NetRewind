//go:build linux

package netlink

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/collect"
	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/identity"
	nl "github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const (
	// duplicateWindow is how long a run of binding changes is examined for the
	// flip-flopping that means two machines are claiming one address.
	duplicateWindow = 60 * time.Second
	// duplicateChanges is how many changes inside that window it takes.
	duplicateChanges = 3
	// gatewayCacheTTL bounds how stale the collector's idea of the default
	// gateway may be. Knowing which address is the gateway is what separates a
	// routine DHCP churn from a redirected network.
	gatewayCacheTTL = 30 * time.Second
)

// NeighCollector watches the kernel neighbour table: which hardware address is
// answering for which IP address.
//
// This is where the most valuable single event in the system comes from. An ARP
// binding changing under a running network is how a gateway gets hijacked, how
// a duplicate address announces itself, and how a failover quietly happens - and
// almost nothing records it.
type NeighCollector struct {
	b   *event.Builder
	log *slog.Logger
	ids *identity.Resolver

	bindings map[string]*neighState // keyed by IP

	mu          sync.Mutex
	gateways    map[string]bool
	gatewayseen time.Time
}

type neighState struct {
	mac       string
	linkIndex int
	changes   []change
}

type change struct {
	at  time.Time
	mac string
}

// NewNeighCollector returns a collector for the neighbour table. The resolver
// may be nil, in which case events are recorded without a stable host identity.
func NewNeighCollector(b *event.Builder, log *slog.Logger, ids *identity.Resolver) *NeighCollector {
	return &NeighCollector{
		b:        b,
		log:      log,
		ids:      ids,
		bindings: make(map[string]*neighState),
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

	opts := nl.NeighSubscribeOptions{
		ErrorCallback: func(err error) {
			c.log.Warn("netlink neighbour subscription error", "err", err)
		},
	}
	if err := nl.NeighSubscribeWithOptions(updates, done, opts); err != nil {
		return fmt.Errorf("netlink: subscribe to neighbour updates: %w", err)
	}

	c.log.Info("watching the neighbour table", "collector", c.Name(), "seeded", len(c.bindings))

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
		c.bindings[n.IP.String()] = &neighState{
			mac:       n.HardwareAddr.String(),
			linkIndex: n.LinkIndex,
		}
	}
	return nil
}

// diff turns one neighbour notification into the events it justifies.
//
// The neighbour table is noisy: entries go stale and are revalidated
// constantly. Only an actual change of which hardware address answers for an
// address, or an address giving up entirely, is worth recording.
func (c *NeighCollector) diff(ctx context.Context, u nl.NeighUpdate) []*event.Event {
	if u.IP == nil || u.IP.IsUnspecified() {
		return nil
	}
	ip := u.IP.String()

	// The entry was removed, or the neighbour stopped answering ARP at all.
	// Either way the address is no longer reachable at layer 2.
	if u.Type == unix.RTM_DELNEIGH || u.State&unix.NUD_FAILED != 0 {
		prev, known := c.bindings[ip]
		if !known {
			return nil
		}
		delete(c.bindings, ip)
		return []*event.Event{
			c.b.New(event.SourceNetlink, event.KindNeighborFailed, event.SevNotice,
				event.Host(ip, prev.mac)).
				WithAttr("ip", ip).
				WithAttr("mac", prev.mac).
				WithDedup("l2.neighbor_failed|"+ip).
				WithEvidence("nud_state", nudString(u.State)),
		}
	}

	if !usable(u.Neigh) {
		return nil
	}
	mac := u.HardwareAddr.String()

	prev, known := c.bindings[ip]
	if !known {
		c.bindings[ip] = &neighState{mac: mac, linkIndex: u.LinkIndex}
		e := c.b.New(event.SourceNetlink, event.KindARPBindingNew, event.SevInfo,
			event.Host(ip, mac)).
			WithAttr("ip", ip).
			WithAttr("mac", mac).
			WithAttr("ifindex", u.LinkIndex).
			WithDedup("l2.arp_binding_new|" + ip)
		c.stampIdentity(ctx, e, ip, mac)
		return []*event.Event{e}
	}

	var events []*event.Event

	if prev.mac != mac {
		gw := c.isGateway(ip)
		sev := event.SevWarn
		if gw {
			// The gateway's hardware address changing under a live network is
			// either a failover or an attack. Both need looking at now.
			sev = event.SevError
		}
		e := c.b.New(event.SourceNetlink, event.KindARPBindingChanged, sev,
			event.Host(ip, mac)).
			WithAttr("ip", ip).
			WithAttr("mac_old", prev.mac).
			WithAttr("mac_new", mac).
			WithAttr("is_gateway", gw).
			WithEvidence("nud_state", nudString(u.State)).
			WithEvidence("ifindex", u.LinkIndex)
		c.stampIdentity(ctx, e, ip, mac)
		events = append(events, e)

		prev.changes = appendChange(prev.changes, change{at: time.Now(), mac: mac})
		if dup, macs := looksDuplicated(prev.changes); dup {
			events = append(events,
				c.b.New(event.SourceNetlink, event.KindDuplicateIP, event.SevError,
					event.Host(ip, mac)).
					WithAttr("ip", ip).
					WithAttr("claimants", macs).
					WithAttr("changes_in_window", len(prev.changes)).
					WithConfidence(80).
					WithDedup("l2.duplicate_ip|"+ip).
					WithEvidence("window_seconds", int(duplicateWindow.Seconds())))
		}
	}

	// The same hardware address answering from behind a different interface
	// means the machine moved, or something is impersonating it.
	if prev.mac == mac && prev.linkIndex != u.LinkIndex && prev.linkIndex != 0 {
		events = append(events,
			c.b.New(event.SourceNetlink, event.KindMACMoved, event.SevWarn,
				event.Host(ip, mac)).
				WithAttr("mac", mac).
				WithAttr("ifindex_old", prev.linkIndex).
				WithAttr("ifindex_new", u.LinkIndex).
				WithDedup(fmt.Sprintf("l2.mac_moved|%s|%d", mac, u.LinkIndex)))
	}

	prev.mac = mac
	prev.linkIndex = u.LinkIndex
	return events
}

// stampIdentity attaches the stable host this observation belongs to, so the
// event can still be found after the address moves elsewhere.
func (c *NeighCollector) stampIdentity(ctx context.Context, e *event.Event, ip, mac string) {
	if c.ids == nil {
		return
	}
	attrType := identity.AttrIPv4
	if net.ParseIP(ip).To4() == nil {
		attrType = identity.AttrIPv6
	}
	res, err := c.ids.Observe(ctx, identity.Observation{
		At: e.WallTime(),
		Attrs: []identity.Attr{
			{Type: identity.AttrMAC, Value: mac},
			{Type: attrType, Value: ip},
		},
	})
	if err != nil {
		c.log.Warn("identity resolution failed", "ip", ip, "mac", mac, "err", err)
		return
	}
	e.Subject.ID = res.HostID
	if res.Displaced {
		e.WithAttr("address_changed_hands", true)
	}
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

func appendChange(history []change, c change) []change {
	history = append(history, c)
	cutoff := c.at.Add(-duplicateWindow)
	kept := history[:0]
	for _, h := range history {
		if h.at.After(cutoff) {
			kept = append(kept, h)
		}
	}
	return kept
}

// looksDuplicated reports whether a run of binding changes is the flip-flopping
// that two machines claiming one address produces. One change is a handover;
// several between the same few hardware addresses is a conflict.
func looksDuplicated(history []change) (bool, []string) {
	if len(history) < duplicateChanges {
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
