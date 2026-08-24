//go:build linux

package probe

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/collect"
	"github.com/OmarAlghafri/netrewind/internal/event"
	nl "github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// Collector sends ICMP echoes and hands the results to an Analyser.
//
// It prefers a datagram ICMP socket, which needs no privilege where the kernel
// permits it and lets the kernel demultiplex replies - so the recorder cannot
// see, and cannot be accused of seeing, anybody else's ICMP traffic. Where the
// default ping_group_range forbids it, a raw socket is used instead.
type Collector struct {
	log      *slog.Logger
	analyser *Analyser
	// Targets are probed every round. Empty means "whatever the default route
	// points at", rediscovered as it changes.
	Targets []string
}

// NewCollector returns a probe collector. targets is a comma-separated list;
// empty means follow the default gateway.
func NewCollector(b *event.Builder, log *slog.Logger, targets string) *Collector {
	var list []string
	for _, t := range strings.Split(targets, ",") {
		if t = strings.TrimSpace(t); t != "" {
			list = append(list, t)
		}
	}
	return &Collector{log: log, analyser: NewAnalyser(b, nil), Targets: list}
}

// Name implements collect.Collector.
func (c *Collector) Name() string { return "probe.icmp" }

// Run probes until ctx is done.
func (c *Collector) Run(ctx context.Context, out chan<- *event.Event) error {
	// Fail at startup rather than silently measuring nothing.
	fd, _, err := openICMP()
	if err != nil {
		return err
	}
	unix.Close(fd)

	c.log.Info("measuring reachability", "collector", c.Name(),
		"targets", targetsOrGateway(c.Targets), "every", Interval)

	ticker := time.NewTicker(Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			for _, target := range c.resolveTargets() {
				result, err := c.round(ctx, target)
				if err != nil {
					c.log.Debug("probe round failed", "target", target, "err", err)
					continue
				}
				for _, e := range c.analyser.Observe(result) {
					if !collect.Emit(ctx, out, e) {
						return nil
					}
				}
			}
		}
	}
}

// resolveTargets returns what to probe this round.
//
// With no configured targets it follows the default gateway, re-read each
// round. That matters: when the default route moves, the thing worth measuring
// moves with it, and a probe list fixed at startup would keep measuring a
// gateway nobody uses any more.
func (c *Collector) resolveTargets() []string {
	if len(c.Targets) > 0 {
		return c.Targets
	}
	routes, err := nl.RouteList(nil, unix.AF_INET)
	if err != nil {
		return nil
	}
	for _, r := range routes {
		if r.Dst == nil && r.Gw != nil {
			return []string{r.Gw.String()}
		}
	}
	return nil
}

// round sends probesPerRound echoes and collects what comes back.
func (c *Collector) round(ctx context.Context, target string) (Result, error) {
	ip := net.ParseIP(target)
	if ip == nil || ip.To4() == nil {
		return Result{}, fmt.Errorf("probe: %q is not an IPv4 address", target)
	}

	fd, raw, err := openICMP()
	if err != nil {
		return Result{}, err
	}
	defer unix.Close(fd)

	tv := unix.NsecToTimeval(int64(Timeout))
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv); err != nil {
		return Result{}, fmt.Errorf("probe: set timeout: %w", err)
	}

	addr := &unix.SockaddrInet4{}
	copy(addr.Addr[:], ip.To4())

	result := Result{Target: target}
	id := uint16(os.Getpid())
	reply := make([]byte, 128)

	for seq := 0; seq < probesPerRound; seq++ {
		if ctx.Err() != nil {
			break
		}
		packet := echoRequest(id, uint16(seq))
		sent := time.Now()
		if err := unix.Sendto(fd, packet, 0, addr); err != nil {
			// An unreachable network fails here rather than timing out, and
			// that is still a probe that did not come back.
			result.Sent++
			continue
		}
		result.Sent++

		n, _, err := unix.Recvfrom(fd, reply, 0)
		if err != nil {
			continue
		}
		icmp, ok := icmpBody(reply[:n], raw)
		if !ok {
			continue
		}
		// The kernel rewrites the identifier on a datagram socket, so only the
		// sequence number is ours to check. A raw socket also hands us every
		// other ICMP message on the host, which is why the type is checked too.
		if icmp[0] != 0 /* echo reply */ || binary.BigEndian.Uint16(icmp[6:8]) != uint16(seq) {
			continue
		}
		result.Recv++
		result.RTTs = append(result.RTTs, time.Since(sent))
	}
	return result, nil
}

// openICMP opens an ICMP socket, preferring the unprivileged kind.
//
// A datagram ICMP socket needs no capability at all - but only if
// net.ipv4.ping_group_range covers the calling group, and the kernel default
// on most distributions is "1 0", an empty range that excludes even root. So
// the unprivileged path is tried first and a raw socket is the fallback, which
// is the opposite of the usual order and the only one that actually works
// across the distributions this has to run on.
//
// The second return value says which kind was opened, because a raw socket
// hands back the IP header and a datagram one does not.
func openICMP() (int, bool, error) {
	if fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM, unix.IPPROTO_ICMP); err == nil {
		return fd, false, nil
	}
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_RAW, unix.IPPROTO_ICMP)
	if err != nil {
		return -1, false, fmt.Errorf(
			"probe: open ICMP socket: neither a datagram socket (needs a ping_group_range "+
				"covering this user) nor a raw one (needs CAP_NET_RAW) could be opened: %w", err)
	}
	return fd, true, nil
}

func echoRequest(id, seq uint16) []byte {
	p := make([]byte, 16)
	p[0] = 8 // echo request
	binary.BigEndian.PutUint16(p[4:6], id)
	binary.BigEndian.PutUint16(p[6:8], seq)
	binary.BigEndian.PutUint16(p[2:4], checksum(p))
	return p
}

func checksum(b []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(b[i : i+2]))
	}
	if len(b)%2 == 1 {
		sum += uint32(b[len(b)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	return ^uint16(sum)
}

func targetsOrGateway(targets []string) string {
	if len(targets) == 0 {
		return "the default gateway"
	}
	return strings.Join(targets, ", ")
}

var _ collect.Collector = (*Collector)(nil)

// icmpBody returns the ICMP message inside a reply, skipping the IP header
// that a raw socket includes and a datagram socket does not.
func icmpBody(reply []byte, raw bool) ([]byte, bool) {
	if !raw {
		if len(reply) < 8 {
			return nil, false
		}
		return reply, true
	}
	if len(reply) < 20 {
		return nil, false
	}
	ihl := int(reply[0]&0x0f) * 4
	if ihl < 20 || len(reply) < ihl+8 {
		return nil, false
	}
	return reply[ihl:], true
}
