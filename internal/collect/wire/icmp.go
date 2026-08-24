package wire

import (
	"encoding/binary"
	"fmt"
	"net"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
)

// ICMP types and the destination-unreachable codes worth naming.
const (
	icmpDestUnreachable = 3

	codeNetUnreachable  = 0
	codeHostUnreachable = 1
	codePortUnreachable = 3
	codeFragNeeded      = 4
	codeNetAdminProhib  = 9
	codeHostAdminProhib = 10
	codeAdminFiltered   = 13
)

func unreachableName(code uint8) string {
	switch code {
	case codeNetUnreachable:
		return "network unreachable"
	case codeHostUnreachable:
		return "host unreachable"
	case codePortUnreachable:
		return "port unreachable"
	case codeFragNeeded:
		return "fragmentation needed"
	case codeNetAdminProhib, codeHostAdminProhib, codeAdminFiltered:
		return "administratively prohibited"
	default:
		return fmt.Sprintf("code(%d)", code)
	}
}

// ICMPMessage is a destination-unreachable report and, where the message
// carries it, the packet that provoked it.
type ICMPMessage struct {
	Type uint8
	Code uint8
	// NextHopMTU is set only for fragmentation-needed.
	NextHopMTU uint16
	// The quoted original packet, if the message included enough of it.
	OrigSrc  net.IP
	OrigDst  net.IP
	OrigPort uint16
}

// ParseICMP decodes an ICMP message and the header it quotes.
func ParseICMP(payload []byte) (ICMPMessage, error) {
	var m ICMPMessage
	if len(payload) < 8 {
		return m, ErrNotForUs
	}
	m.Type = payload[0]
	m.Code = payload[1]
	if m.Type != icmpDestUnreachable {
		return m, ErrNotForUs
	}
	m.NextHopMTU = binary.BigEndian.Uint16(payload[6:8])

	// The quoted packet is at least an IP header, and by RFC 1812 usually more.
	quoted := payload[8:]
	if len(quoted) < 20 {
		return m, nil
	}
	ihl := int(quoted[0]&0x0f) * 4
	if ihl < 20 || len(quoted) < ihl {
		return m, nil
	}
	m.OrigSrc = net.IP(append([]byte(nil), quoted[12:16]...))
	m.OrigDst = net.IP(append([]byte(nil), quoted[16:20]...))
	if len(quoted) >= ihl+4 {
		m.OrigPort = binary.BigEndian.Uint16(quoted[ihl+2 : ihl+4])
	}
	return m, nil
}

// ICMPWatcher turns unreachable reports into events.
//
// The one worth the code is fragmentation-needed. A path whose MTU has dropped
// keeps ping working perfectly and breaks every large transfer, which is why it
// survives for weeks: everything an operator reaches for first says the network
// is fine. The report naming the smaller MTU is the whole diagnosis, and it is
// addressed to somebody else.
type ICMPWatcher struct {
	b   *event.Builder
	now func() time.Time
}

// NewICMPWatcher returns a watcher.
func NewICMPWatcher(b *event.Builder, now func() time.Time) *ICMPWatcher {
	if now == nil {
		now = time.Now
	}
	return &ICMPWatcher{b: b, now: now}
}

// Observe turns one ICMP message into the event it justifies.
func (w *ICMPWatcher) Observe(p Packet, m ICMPMessage) []*event.Event {
	dst := ""
	if m.OrigDst != nil {
		dst = m.OrigDst.String()
	}
	reporter := p.SrcIP.String()

	if m.Code == codeFragNeeded {
		e := w.b.New(event.SourceProbe, event.KindMTUBlackhole, event.SevWarn,
			event.Host(dst, "")).
			WithAttr("reported_by", reporter).
			WithAttr("destination", dst).
			WithAttr("next_hop_mtu", int(m.NextHopMTU)).
			WithDedup(fmt.Sprintf("l3.mtu_blackhole|%s|%d", dst, m.NextHopMTU)).
			WithEvidence("icmp", unreachableName(m.Code))
		if m.OrigSrc != nil {
			e.WithAttr("source", m.OrigSrc.String())
		}
		return []*event.Event{e}
	}

	// Administratively prohibited is a filtering decision made somewhere on
	// the path, reported by the device that made it. That is a stronger
	// statement than a host simply being unreachable.
	sev := event.SevNotice
	if m.Code == codeNetAdminProhib || m.Code == codeHostAdminProhib || m.Code == codeAdminFiltered {
		sev = event.SevWarn
	}

	e := w.b.New(event.SourceProbe, event.KindICMPUnreachable, sev,
		event.Host(dst, "")).
		WithAttr("reported_by", reporter).
		WithAttr("destination", dst).
		WithAttr("reason", unreachableName(m.Code)).
		WithDedup(fmt.Sprintf("l3.icmp_unreachable|%s|%d", dst, m.Code))
	if m.OrigSrc != nil {
		e.WithAttr("source", m.OrigSrc.String())
	}
	if m.OrigPort != 0 {
		e.WithAttr("dport", int(m.OrigPort))
	}
	return []*event.Event{e}
}
