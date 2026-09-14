package iphelper

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"time"
	"unsafe"

	"github.com/OmarAlghafri/netrewind/internal/analyze"
	"github.com/OmarAlghafri/netrewind/internal/collect"
	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/identity"
	"github.com/OmarAlghafri/netrewind/internal/ports"
	"golang.org/x/sys/windows"
)

// neighInterval is the polling period for the neighbour table. Windows has
// no change notification for it, so a binding that changes and changes
// back inside one interval is invisible - the reason this stays short.
const neighInterval = 2 * time.Second

// gatewayCacheTTL bounds how stale the collector's idea of the default
// gateways may be, exactly as in the Linux collector: knowing which
// neighbour is the gateway is what separates a curiosity from a hijack.
const gatewayCacheTTL = 30 * time.Second

var (
	modiphlpapi        = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetIpNetTable2 = modiphlpapi.NewProc("GetIpNetTable2")
)

// mibIpNetRow2 mirrors MIB_IPNET_ROW2 (netioapi.h), which
// golang.org/x/sys/windows does not define. Field order and padding follow
// the C layout exactly; the SOCKADDR_INET union is 28 bytes, as in
// windows.MibUnicastIpAddressRow.
type mibIpNetRow2 struct {
	Address               windows.RawSockaddrInet6
	InterfaceIndex        uint32
	InterfaceLuid         uint64
	PhysicalAddress       [32]byte
	PhysicalAddressLength uint32
	State                 uint32
	Flags                 uint8
	_                     [3]byte
	ReachabilityTime      uint32
}

type mibIpNetTable2 struct {
	NumEntries uint32
	Table      [1]mibIpNetRow2
}

// NL_NEIGHBOR_STATE values (nldef.h).
const (
	nlnsUnreachable uint32 = 0
	nlnsIncomplete  uint32 = 1
	nlnsProbe       uint32 = 2
	nlnsDelay       uint32 = 3
	nlnsStale       uint32 = 4
	nlnsReachable   uint32 = 5
	nlnsPermanent   uint32 = 6
)

func neighborStateString(s uint32) string {
	switch s {
	case nlnsReachable:
		return "reachable"
	case nlnsStale:
		return "stale"
	case nlnsDelay:
		return "delay"
	case nlnsProbe:
		return "probe"
	case nlnsPermanent:
		return "permanent"
	case nlnsIncomplete:
		return "incomplete"
	case nlnsUnreachable:
		return "unreachable"
	}
	return fmt.Sprintf("state(%d)", s)
}

func getIpNetTable2(family uint16) ([]mibIpNetRow2, error) {
	var table *mibIpNetTable2
	r, _, _ := procGetIpNetTable2.Call(uintptr(family), uintptr(unsafe.Pointer(&table)))
	if r != 0 {
		return nil, fmt.Errorf("iphelper: GetIpNetTable2: %w", windows.Errno(r))
	}
	defer windows.FreeMibTable(unsafe.Pointer(table))
	rows := unsafe.Slice(&table.Table[0], table.NumEntries)
	out := make([]mibIpNetRow2, len(rows))
	copy(out, rows)
	return out, nil
}

// neighbor is one usable binding read from the table.
type neighbor struct {
	ip    string
	mac   string
	index int
	state uint32
}

// NeighCollector records ARP/ND bindings appearing, changing and failing,
// by polling the neighbour table and diffing it. Decisions live in
// analyze.NeighborAnalyzer, shared with the Linux collector, so a gateway
// hijack looks the same in the record whichever platform saw it.
type NeighCollector struct {
	b        *event.Builder
	log      *slog.Logger
	analyzer *analyze.NeighborAnalyzer
	last     map[string]neighbor // keyed by IP
	gateways map[string]bool
	gwSeen   time.Time
}

// NewNeighCollector returns a collector that has not read the table yet.
func NewNeighCollector(b *event.Builder, log *slog.Logger, ids *identity.Resolver) *NeighCollector {
	return &NeighCollector{
		b: b, log: log,
		analyzer: analyze.NewNeighborAnalyzer(b, log, ids).WithSource(event.SourceIPHelper),
		last:     make(map[string]neighbor),
		gateways: make(map[string]bool),
	}
}

// Name implements collect.Collector.
func (c *NeighCollector) Name() string { return "iphelper.neigh" }

// Run seeds the table, then polls it until ctx is done.
func (c *NeighCollector) Run(ctx context.Context, out chan<- *event.Event) error {
	current, err := c.read()
	if err != nil {
		return err
	}
	for ip, n := range current {
		c.analyzer.Seed(ports.NeighborObservation{IP: ip, MAC: n.mac, LinkIndex: n.index,
			IsGateway: c.isGateway(ip), NUDState: neighborStateString(n.state)})
	}
	c.last = current
	c.log.Info("watching the neighbour table", "collector", c.Name(), "seeded", c.analyzer.Known(), "every", neighInterval)

	tick := time.NewTicker(neighInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-tick.C:
			current, err := c.read()
			if err != nil {
				c.log.Warn("neighbour table read failed", "collector", c.Name(), "err", err)
				continue
			}
			for _, e := range c.diff(ctx, current, now) {
				if !collect.Emit(ctx, out, e) {
					return nil
				}
			}
			c.last = current
		}
	}
}

// read returns the usable bindings in the table right now, plus the
// addresses currently marked unreachable (with no MAC), which diff reports
// as failures.
func (c *NeighCollector) read() (map[string]neighbor, error) {
	rows, err := getIpNetTable2(windows.AF_UNSPEC)
	if err != nil {
		return nil, err
	}
	out := make(map[string]neighbor, len(rows))
	for i := range rows {
		row := &rows[i]
		ip := sockaddrIP(row.Address.Family, unsafe.Pointer(&row.Address))
		if ip == nil || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLoopback() {
			continue
		}
		if len(ip) == net.IPv4len && ip.Equal(net.IPv4bcast) {
			continue
		}
		n := neighbor{ip: ip.String(), index: int(row.InterfaceIndex), state: row.State}
		if row.PhysicalAddressLength > 0 && row.PhysicalAddressLength <= 32 {
			mac := net.HardwareAddr(row.PhysicalAddress[:row.PhysicalAddressLength])
			if !placeholderMAC(mac) {
				n.mac = mac.String()
			}
		}
		switch row.State {
		case nlnsUnreachable, nlnsIncomplete:
			n.mac = "" // not a binding; kept so a lost neighbour is reported
		}
		if n.mac == "" && row.State != nlnsUnreachable {
			continue // no binding and not a failure: nothing to say
		}
		out[n.ip] = n
	}
	return out, nil
}

// diff compares two readings and asks the analyzer about every binding
// that appeared, changed MAC, or stopped answering.
func (c *NeighCollector) diff(ctx context.Context, current map[string]neighbor, now time.Time) []*event.Event {
	var events []*event.Event
	for ip, n := range current {
		prev, seen := c.last[ip]
		switch {
		case n.mac == "":
			// Unreachable now; only worth reporting if it was a binding
			// before, otherwise it is the table remembering a host that was
			// never there.
			if seen && prev.mac != "" {
				events = append(events, c.analyzer.Observe(ctx, ports.NeighborObservation{
					IP: ip, Failed: true, NUDState: neighborStateString(n.state)}, now)...)
			}
		case !seen || prev.mac != n.mac:
			events = append(events, c.analyzer.Observe(ctx, ports.NeighborObservation{
				IP: ip, MAC: n.mac, LinkIndex: n.index,
				IsGateway: c.isGateway(ip), NUDState: neighborStateString(n.state)}, now)...)
		}
	}
	for ip, prev := range c.last {
		if _, still := current[ip]; !still && prev.mac != "" {
			events = append(events, c.analyzer.Observe(ctx, ports.NeighborObservation{
				IP: ip, Failed: true, NUDState: "removed"}, now)...)
		}
	}
	return events
}

// isGateway reports whether an address is a current default gateway,
// re-reading the forwarding table at most every gatewayCacheTTL.
func (c *NeighCollector) isGateway(ip string) bool {
	if time.Since(c.gwSeen) > gatewayCacheTTL {
		c.gateways = make(map[string]bool)
		var table *windows.MibIpForwardTable2
		if err := windows.GetIpForwardTable2(windows.AF_UNSPEC, &table); err == nil {
			for _, row := range unsafe.Slice(&table.Table[0], table.NumEntries) {
				if row.DestinationPrefix.PrefixLength == 0 {
					if gw := nextHopString(row.NextHop); gw != "" {
						c.gateways[gw] = true
					}
				}
			}
			windows.FreeMibTable(unsafe.Pointer(table))
		}
		c.gwSeen = time.Now()
	}
	return c.gateways[ip]
}

// placeholderMAC reports the all-zero and broadcast hardware addresses the
// table uses for entries that are not hosts.
func placeholderMAC(mac net.HardwareAddr) bool {
	zero, bcast := true, true
	for _, b := range mac {
		if b != 0x00 {
			zero = false
		}
		if b != 0xff {
			bcast = false
		}
	}
	return zero || bcast
}

var _ collect.Collector = (*NeighCollector)(nil)
