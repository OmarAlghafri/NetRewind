package iphelper

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"unsafe"

	"github.com/OmarAlghafri/netrewind/internal/analyze"
	"github.com/OmarAlghafri/netrewind/internal/collect"
	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/ports"
	"golang.org/x/sys/windows"
)

// RouteCollector records the routing table's winners changing. Decisions
// live in analyze.RouteAnalyzer; this type reads the Windows forwarding
// table and decides only which rows are worth looking at.
type RouteCollector struct {
	b        *event.Builder
	log      *slog.Logger
	analyzer *analyze.RouteAnalyzer
	// priorities remembers the effective metric last seen for each route
	// identity, because a delete notification carries only the key fields
	// and the analyzer removes entries by (gateway, interface, priority).
	priorities map[string]int
	// metrics caches each interface's own metric, which Windows adds to a
	// route's metric to rank it; a route's row alone does not say where it
	// stands.
	metrics map[uint64]int
}

// NewRouteCollector returns a collector that has not read the table yet.
func NewRouteCollector(b *event.Builder, log *slog.Logger) *RouteCollector {
	return &RouteCollector{
		b: b, log: log,
		analyzer:   analyze.NewRouteAnalyzer(b).WithSource(event.SourceIPHelper),
		priorities: make(map[string]int),
		metrics:    make(map[uint64]int),
	}
}

// Name implements collect.Collector.
func (c *RouteCollector) Name() string { return "iphelper.route" }

// Run seeds the table, then reports every change to a destination's best
// route until ctx is done.
func (c *RouteCollector) Run(ctx context.Context, out chan<- *event.Event) error {
	if err := c.seed(); err != nil {
		return err
	}
	sub, err := subscribeRoutes()
	if err != nil {
		return err
	}
	defer sub.close()
	c.log.Info("watching the routing table", "collector", c.Name(), "seeded", c.analyzer.Known())

	for {
		select {
		case <-ctx.Done():
			return nil
		case n := <-sub.ch:
			if !reportDrops(ctx, out, c.b, c.log, c.Name(), sub) {
				return nil
			}
			for _, e := range c.handle(n) {
				if !collect.Emit(ctx, out, e) {
					return nil
				}
			}
		}
	}
}

func (c *RouteCollector) seed() error {
	var table *windows.MibIpForwardTable2
	if err := windows.GetIpForwardTable2(windows.AF_UNSPEC, &table); err != nil {
		return fmt.Errorf("iphelper: GetIpForwardTable2: %w", err)
	}
	defer windows.FreeMibTable(unsafe.Pointer(table))
	for _, row := range unsafe.Slice(&table.Table[0], table.NumEntries) {
		if obs, ok := c.observation(&row, false, false); ok {
			c.analyzer.Seed(obs)
		}
	}
	return nil
}

// handle turns one notification into events. Adds and parameter changes
// re-read the row (the notification carries only its key); a delete is
// applied from the key and the remembered priority, since the row is gone.
func (c *RouteCollector) handle(n notification) []*event.Event {
	key := routeKey(n.prefix, n.nextHop, n.index)
	if n.kind == windows.MibDeleteInstance {
		prio, known := c.priorities[key]
		if !known {
			return nil // never seen as interesting; nothing to remove
		}
		delete(c.priorities, key)
		_, bits, _ := net.ParseCIDR(n.prefix)
		ones, _ := bits.Mask.Size()
		return c.analyzer.Observe(ports.RouteObservation{
			Prefix: n.prefix, IsDefault: ones == 0, Gateway: n.nextHop,
			LinkIndex: int(n.index), Priority: prio, Removed: true,
		})
	}

	row, ok, err := routeRow(n)
	if err != nil {
		c.log.Debug("route lookup failed", "collector", c.Name(), "prefix", n.prefix, "err", err)
		return nil
	}
	if !ok {
		// Added and already gone, or changed and gone: treat as a removal
		// of whatever was known.
		return c.handle(notification{kind: windows.MibDeleteInstance, prefix: n.prefix, nextHop: n.nextHop, index: n.index})
	}
	obs, interesting := c.observation(&row, false, n.kind == windows.MibParameterNotification)
	if !interesting {
		return nil
	}
	return c.analyzer.Observe(obs)
}

// observation translates a forwarding row into the platform-neutral shape,
// reporting false for rows that are not a route in the sense the record
// cares about: loopback entries, multicast and link-local prefixes, and
// on-link host routes (a /32 or /128 with no gateway), which Windows adds
// and removes for every address the machine itself holds.
func (c *RouteCollector) observation(row *windows.MibIpForwardRow2, removed, replace bool) (ports.RouteObservation, bool) {
	if row.Loopback != 0 {
		return ports.RouteObservation{}, false
	}
	prefix := prefixString(row.DestinationPrefix)
	ip := sockaddrIP(row.DestinationPrefix.Prefix.Family, unsafe.Pointer(&row.DestinationPrefix.Prefix))
	if ip == nil || ip.IsMulticast() || ip.IsLinkLocalUnicast() {
		return ports.RouteObservation{}, false
	}
	gw := nextHopString(row.NextHop)
	plen := int(row.DestinationPrefix.PrefixLength)
	hostRoute := (len(ip) == net.IPv4len && plen == 32) || (len(ip) == net.IPv6len && plen == 128)
	if hostRoute && gw == "" {
		return ports.RouteObservation{}, false
	}
	prio := int(row.Metric) + c.interfaceMetric(row.InterfaceLuid, row.DestinationPrefix.Prefix.Family)
	key := routeKey(prefix, gw, row.InterfaceIndex)
	if removed {
		delete(c.priorities, key)
	} else {
		c.priorities[key] = prio
	}
	return ports.RouteObservation{
		Prefix:    prefix,
		IsDefault: plen == 0,
		Gateway:   gw,
		LinkIndex: int(row.InterfaceIndex),
		Priority:  prio,
		Removed:   removed,
		Replace:   replace,
	}, true
}

// interfaceMetric reads (and caches) the per-family interface metric
// Windows adds to every route on that interface.
func (c *RouteCollector) interfaceMetric(luid uint64, family uint16) int {
	cacheKey := luid<<1 | uint64(family&1)
	if m, ok := c.metrics[cacheKey]; ok {
		return m
	}
	var row windows.MibIpInterfaceRow
	row.Family = family
	row.InterfaceLuid = luid
	if err := windows.GetIpInterfaceEntry(&row); err != nil {
		return 0
	}
	c.metrics[cacheKey] = int(row.Metric)
	return int(row.Metric)
}

func routeKey(prefix, gw string, index uint32) string {
	return fmt.Sprintf("%s|%s|%d", prefix, gw, index)
}

// routeRow re-reads a forwarding row by its key. ok is false when the
// route no longer exists.
func routeRow(n notification) (row windows.MibIpForwardRow2, ok bool, err error) {
	ip, bits, err := net.ParseCIDR(n.prefix)
	if err != nil {
		return row, false, fmt.Errorf("iphelper: route prefix %q: %w", n.prefix, err)
	}
	ones, _ := bits.Mask.Size()
	row.InterfaceLuid = n.luid
	row.InterfaceIndex = n.index
	row.DestinationPrefix.PrefixLength = uint8(ones)
	setSockaddr(&row.DestinationPrefix.Prefix, ip)
	if n.nextHop != "" {
		setSockaddr(&row.NextHop, net.ParseIP(n.nextHop))
	} else {
		row.NextHop.Family = row.DestinationPrefix.Prefix.Family
	}
	err = windows.GetIpForwardEntry2(&row)
	switch {
	case err == nil:
		return row, true, nil
	case err == windows.ERROR_NOT_FOUND || err == windows.ERROR_FILE_NOT_FOUND:
		return row, false, nil
	}
	return row, false, err
}

// setSockaddr fills a SOCKADDR_INET union from a net.IP.
func setSockaddr(s *windows.RawSockaddrInet, ip net.IP) {
	if v4 := ip.To4(); v4 != nil {
		s.Family = windows.AF_INET
		a := (*windows.RawSockaddrInet4)(unsafe.Pointer(s))
		copy(a.Addr[:], v4)
		return
	}
	s.Family = windows.AF_INET6
	a := (*windows.RawSockaddrInet6)(unsafe.Pointer(s))
	copy(a.Addr[:], ip.To16())
}

// AddrCollector records addresses being added to and removed from
// interfaces. An address counts as added once duplicate address detection
// has finished and it is preferred - before that it cannot be used, and a
// DAD failure means it never will be.
type AddrCollector struct {
	b   *event.Builder
	log *slog.Logger
	// announced holds every address currently recorded as present (seeded
	// or added), keyed by address and interface, so a removal is reported
	// only for an address the record knows about, and a tentative address
	// that never became preferred is not reported as removed.
	announced map[string]ports.AddressObservation
}

// NewAddrCollector returns a collector that has not looked at anything yet.
func NewAddrCollector(b *event.Builder, log *slog.Logger) *AddrCollector {
	return &AddrCollector{b: b, log: log, announced: make(map[string]ports.AddressObservation)}
}

// Name implements collect.Collector.
func (c *AddrCollector) Name() string { return "iphelper.addr" }

// Run seeds the current addresses (without reporting them), then reports
// every arrival and departure until ctx is done.
func (c *AddrCollector) Run(ctx context.Context, out chan<- *event.Event) error {
	if err := c.seed(); err != nil {
		return err
	}
	sub, err := subscribeAddresses()
	if err != nil {
		return err
	}
	defer sub.close()
	c.log.Info("watching interface addresses", "collector", c.Name(), "seeded", len(c.announced))

	for {
		select {
		case <-ctx.Done():
			return nil
		case n := <-sub.ch:
			if !reportDrops(ctx, out, c.b, c.log, c.Name(), sub) {
				return nil
			}
			for _, e := range c.handle(n) {
				if !collect.Emit(ctx, out, e) {
					return nil
				}
			}
		}
	}
}

func (c *AddrCollector) seed() error {
	var table *windows.MibUnicastIpAddressTable
	if err := windows.GetUnicastIpAddressTable(windows.AF_UNSPEC, &table); err != nil {
		return fmt.Errorf("iphelper: GetUnicastIpAddressTable: %w", err)
	}
	defer windows.FreeMibTable(unsafe.Pointer(table))
	for _, row := range unsafe.Slice(&table.Table[0], table.NumEntries) {
		if obs, ok := addressObservation(&row); ok && row.DadState == windows.NldsPreferred {
			c.announced[addrKey(obs)] = obs
		}
	}
	return nil
}

func (c *AddrCollector) handle(n notification) []*event.Event {
	if n.ip == nil {
		return nil
	}
	key := fmt.Sprintf("%s|%d", n.ip, n.index)
	if n.kind == windows.MibDeleteInstance {
		obs, known := c.announced[key]
		if !known {
			return nil
		}
		delete(c.announced, key)
		obs.Removed = true
		return []*event.Event{analyze.ObserveAddressFrom(c.b, event.SourceIPHelper, obs)}
	}

	var row windows.MibUnicastIpAddressRow
	row.InterfaceLuid = n.luid
	row.InterfaceIndex = n.index
	setSockaddr((*windows.RawSockaddrInet)(unsafe.Pointer(&row.Address)), n.ip)
	if err := windows.GetUnicastIpAddressEntry(&row); err != nil {
		if err == windows.ERROR_NOT_FOUND || err == windows.ERROR_FILE_NOT_FOUND {
			return c.handle(notification{kind: windows.MibDeleteInstance, ip: n.ip, index: n.index})
		}
		c.log.Debug("address lookup failed", "collector", c.Name(), "address", n.ip, "err", err)
		return nil
	}
	obs, ok := addressObservation(&row)
	if !ok {
		return nil
	}
	switch row.DadState {
	case windows.NldsPreferred, windows.NldsDeprecated:
		if _, known := c.announced[key]; known {
			return nil
		}
		c.announced[key] = obs
		return []*event.Event{analyze.ObserveAddressFrom(c.b, event.SourceIPHelper, obs)}
	case windows.NldsDuplicate:
		// Duplicate address detection failed: another machine already
		// answers for this address. That is the fact an operator is chasing
		// when "the network is weird", and it is recorded as the conflict it
		// is rather than as an address quietly never appearing.
		return []*event.Event{
			c.b.New(event.SourceIPHelper, event.KindDuplicateIP, event.SevError, event.Host(obs.IP, "")).
				WithAttr("ip", obs.IP).
				WithAttr("ifindex", obs.LinkIndex).
				WithAttr("detected_by", "duplicate_address_detection").
				WithDedup("l2.duplicate_ip|" + obs.IP),
		}
	}
	return nil // tentative: not usable yet; the preferred notification follows
}

// addressObservation translates an address row, reporting false for the
// addresses the record does not track: loopback, link-local (which appear
// and vanish with every interface transition) and anything unparseable.
func addressObservation(row *windows.MibUnicastIpAddressRow) (ports.AddressObservation, bool) {
	ip := sockaddrIP(row.Address.Family, unsafe.Pointer(&row.Address))
	if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return ports.AddressObservation{}, false
	}
	return ports.AddressObservation{
		IP:        ip.String(),
		CIDR:      fmt.Sprintf("%s/%d", ip, row.OnLinkPrefixLength),
		LinkIndex: int(row.InterfaceIndex),
	}, true
}

func addrKey(obs ports.AddressObservation) string {
	return fmt.Sprintf("%s|%d", obs.IP, obs.LinkIndex)
}

var (
	_ collect.Collector = (*RouteCollector)(nil)
	_ collect.Collector = (*AddrCollector)(nil)
)
