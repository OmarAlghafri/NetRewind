//go:build linux

package wire

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/collect"
	"github.com/OmarAlghafri/netrewind/internal/event"
	"golang.org/x/net/bpf"
	"golang.org/x/sys/unix"
)

const (
	// snapLen is how much of each frame is read. Enough for the headers, the
	// DHCP options that matter and a DNS question - and deliberately not
	// enough to capture anything anyone would object to being captured.
	snapLen = 1024
	// sweepInterval prunes the pending-query and resolver tables.
	sweepInterval = 30 * time.Second
)

// Collector reads DHCP, DNS and ICMP metadata from a raw socket.
//
// The socket is opened in promiscuous-free mode: no interface is put into
// promiscuous mode, so this sees broadcast, multicast and traffic addressed to
// this host. DHCP is broadcast, so a rogue server is visible from any port on
// the segment - which is the point, and why this does not need a mirror port to
// be useful.
type Collector struct {
	log  *slog.Logger
	dhcp *DHCPWatcher
	dns  *DNSWatcher
	icmp *ICMPWatcher
	// Iface restricts capture to one interface. Empty means every interface,
	// which is what an appliance with one segment wants.
	Iface string
}

// NewCollector returns a wire collector. recordDNSNames controls whether
// queried names are stored at all.
func NewCollector(b *event.Builder, log *slog.Logger, iface string, recordDNSNames bool) *Collector {
	return &Collector{
		log:   log,
		dhcp:  NewDHCPWatcher(b, nil),
		dns:   NewDNSWatcher(b, nil, recordDNSNames),
		icmp:  NewICMPWatcher(b, nil),
		Iface: iface,
	}
}

// Name implements collect.Collector.
func (c *Collector) Name() string { return "wire" }

// Run opens the socket and reports until ctx is done.
func (c *Collector) Run(ctx context.Context, out chan<- *event.Event) error {
	fd, err := openSocket(c.Iface)
	if err != nil {
		return err
	}
	defer unix.Close(fd)

	c.log.Info("watching DHCP, DNS and ICMP on the wire",
		"collector", c.Name(), "iface", ifaceOrAll(c.Iface))

	// A read timeout is what lets the loop notice cancellation. Without it the
	// goroutine blocks in recvfrom until a packet arrives, which on a quiet
	// segment can be a very long time after shutdown was requested.
	tv := unix.Timeval{Sec: 1}
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv); err != nil {
		return fmt.Errorf("wire: set read timeout: %w", err)
	}

	sweep := time.NewTicker(sweepInterval)
	defer sweep.Stop()

	buf := make([]byte, snapLen)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-sweep.C:
			c.dns.Sweep()
		default:
		}

		n, _, err := unix.Recvfrom(fd, buf, 0)
		if err != nil {
			if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) ||
				errors.Is(err, unix.EINTR) {
				continue // the timeout fired, or a signal arrived
			}
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("wire: read: %w", err)
		}
		if n <= 0 {
			continue
		}

		for _, e := range c.handle(buf[:n]) {
			if !collect.Emit(ctx, out, e) {
				return nil
			}
		}
	}
}

// handle routes one frame to the watcher that understands it.
func (c *Collector) handle(frame []byte) []*event.Event {
	p, err := Parse(frame)
	if err != nil {
		return nil
	}

	switch {
	case p.Proto == protoICMP:
		m, err := ParseICMP(p.Payload)
		if err != nil {
			return nil
		}
		return c.icmp.Observe(p, m)

	case p.Proto == protoUDP &&
		(p.SrcPort == dhcpServerPort || p.DstPort == dhcpServerPort ||
			p.SrcPort == dhcpClientPort || p.DstPort == dhcpClientPort):
		m, err := ParseDHCP(p.Payload)
		if err != nil {
			return nil
		}
		return c.dhcp.Observe(p, m)

	case p.Proto == protoUDP && (p.SrcPort == dnsPort || p.DstPort == dnsPort):
		m, err := ParseDNS(p.Payload)
		if err != nil {
			return nil
		}
		return c.dns.Observe(p, m)
	}
	return nil
}

// openSocket opens AF_PACKET with a kernel-side filter attached.
//
// Filtering in the kernel rather than in Go is not an optimisation detail. An
// unfiltered raw socket wakes this process for every frame on the segment, and
// on a busy one that is a recorder that spends its day discarding video
// traffic instead of watching for the four things it cares about.
func openSocket(iface string) (int, error) {
	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW, int(htons(unix.ETH_P_ALL)))
	if err != nil {
		return -1, fmt.Errorf("wire: open packet socket (needs CAP_NET_RAW): %w", err)
	}

	filter, err := bpf.Assemble(captureFilter())
	if err != nil {
		unix.Close(fd)
		return -1, fmt.Errorf("wire: assemble filter: %w", err)
	}
	raw := make([]unix.SockFilter, len(filter))
	for i, ins := range filter {
		raw[i] = unix.SockFilter{Code: ins.Op, Jt: ins.Jt, Jf: ins.Jf, K: ins.K}
	}
	prog := unix.SockFprog{Len: uint16(len(raw)), Filter: &raw[0]}
	if err := unix.SetsockoptSockFprog(fd, unix.SOL_SOCKET, unix.SO_ATTACH_FILTER, &prog); err != nil {
		unix.Close(fd)
		return -1, fmt.Errorf("wire: attach filter: %w", err)
	}

	if iface != "" {
		idx, err := interfaceIndex(iface)
		if err != nil {
			unix.Close(fd)
			return -1, err
		}
		addr := unix.SockaddrLinklayer{Protocol: htons(unix.ETH_P_ALL), Ifindex: idx}
		if err := unix.Bind(fd, &addr); err != nil {
			unix.Close(fd)
			return -1, fmt.Errorf("wire: bind to %s: %w", iface, err)
		}
	}
	return fd, nil
}

func htons(v uint16) uint16 { return v<<8 | v>>8 }

func ifaceOrAll(name string) string {
	if name == "" {
		return "all"
	}
	return name
}

var _ collect.Collector = (*Collector)(nil)
