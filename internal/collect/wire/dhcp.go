package wire

import (
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
)

// DHCP ports and the fixed part of the message, from RFC 2131.
const (
	dhcpServerPort = 67
	dhcpClientPort = 68
	dhcpMinLen     = 240 // fixed header plus the magic cookie
	dhcpMagic      = 0x63825363

	optPad         = 0
	optEnd         = 255
	optMessageType = 53
	optServerID    = 54
	optLeaseTime   = 51
	optClientID    = 61
	optRequestedIP = 50
	optRouter      = 3
	optDNSServer   = 6
)

// DHCP message types worth naming.
const (
	dhcpDiscover = 1
	dhcpOffer    = 2
	dhcpRequest  = 3
	dhcpDecline  = 4
	dhcpAck      = 5
	dhcpNak      = 6
	dhcpRelease  = 7
)

func dhcpTypeName(t byte) string {
	switch t {
	case dhcpDiscover:
		return "discover"
	case dhcpOffer:
		return "offer"
	case dhcpRequest:
		return "request"
	case dhcpDecline:
		return "decline"
	case dhcpAck:
		return "ack"
	case dhcpNak:
		return "nak"
	case dhcpRelease:
		return "release"
	default:
		return fmt.Sprintf("type(%d)", t)
	}
}

// DHCPMessage is the metadata of one DHCP exchange. No option is kept that is
// not needed to answer a question about addressing.
type DHCPMessage struct {
	Type        byte
	ClientMAC   net.HardwareAddr
	YourIP      net.IP // the address being offered or confirmed
	ServerID    net.IP // which server is speaking
	Router      net.IP // the gateway it is handing out
	DNS         []net.IP
	LeaseTime   time.Duration
	RequestedIP net.IP
}

// ParseDHCP decodes the fixed header and the options that matter.
func ParseDHCP(payload []byte) (DHCPMessage, error) {
	var m DHCPMessage
	if len(payload) < dhcpMinLen {
		return m, ErrNotForUs
	}
	if binary.BigEndian.Uint32(payload[236:240]) != dhcpMagic {
		return m, ErrNotForUs
	}

	hlen := int(payload[2])
	if hlen == 6 {
		m.ClientMAC = net.HardwareAddr(append([]byte(nil), payload[28:34]...))
	}
	m.YourIP = net.IP(append([]byte(nil), payload[16:20]...))

	// Options are length-prefixed and attacker-controlled. Every read below is
	// bounded, and a malformed option ends the walk rather than the process.
	for i := dhcpMinLen; i < len(payload); {
		code := payload[i]
		if code == optPad {
			i++
			continue
		}
		if code == optEnd {
			break
		}
		if i+1 >= len(payload) {
			break
		}
		length := int(payload[i+1])
		start := i + 2
		if start+length > len(payload) {
			break
		}
		value := payload[start : start+length]

		switch code {
		case optMessageType:
			if length >= 1 {
				m.Type = value[0]
			}
		case optServerID:
			if length == 4 {
				m.ServerID = net.IP(append([]byte(nil), value...))
			}
		case optRouter:
			if length >= 4 {
				m.Router = net.IP(append([]byte(nil), value[:4]...))
			}
		case optDNSServer:
			for off := 0; off+4 <= length; off += 4 {
				m.DNS = append(m.DNS, net.IP(append([]byte(nil), value[off:off+4]...)))
			}
		case optLeaseTime:
			if length == 4 {
				m.LeaseTime = time.Duration(binary.BigEndian.Uint32(value)) * time.Second
			}
		case optRequestedIP:
			if length == 4 {
				m.RequestedIP = net.IP(append([]byte(nil), value...))
			}
		}
		i = start + length
	}
	return m, nil
}

/* ------------------------------------------------------------------ */
/* Watcher                                                            */
/* ------------------------------------------------------------------ */

// serverMemory is how long a DHCP server stays "known". A server that has not
// spoken for a day and then does is worth reporting again: on most networks
// there is exactly one, and a second appearing is the fault this watcher
// exists to catch.
const serverMemory = 24 * time.Hour

// leaseMemory bounds how many clients are remembered for lease-change
// detection.
const maxLeases = 8192

type serverState struct {
	firstSeen time.Time
	lastSeen  time.Time
	mac       string
}

// DHCPWatcher turns DHCP messages into events.
//
// The event it exists for is dhcp.server_seen: a server nobody expected,
// answering on a segment that already had one. That is what a rogue DHCP server
// looks like from the wire, it is one of the most common ways a network breaks
// for everyone at once, and no amount of watching the local kernel will reveal
// it - it is a conversation between other machines.
type DHCPWatcher struct {
	b   *event.Builder
	now func() time.Time

	mu      sync.Mutex
	servers map[string]*serverState
	leases  map[string]string // client MAC -> address it last held
}

// NewDHCPWatcher returns a watcher. A nil clock means time.Now.
func NewDHCPWatcher(b *event.Builder, now func() time.Time) *DHCPWatcher {
	if now == nil {
		now = time.Now
	}
	return &DHCPWatcher{
		b:       b,
		now:     now,
		servers: make(map[string]*serverState),
		leases:  make(map[string]string),
	}
}

// Observe folds one DHCP message in and returns the events it justifies.
func (w *DHCPWatcher) Observe(p Packet, m DHCPMessage) []*event.Event {
	now := w.now()
	w.mu.Lock()
	defer w.mu.Unlock()

	var out []*event.Event

	// Only a server sends offer, ack or nak, and only from the server port.
	// Taking the source port into account keeps a client that lies about its
	// message type from registering itself as a server.
	speaking := p.SrcPort == dhcpServerPort &&
		(m.Type == dhcpOffer || m.Type == dhcpAck || m.Type == dhcpNak)

	if speaking {
		id := m.ServerID
		if id == nil {
			id = p.SrcIP
		}
		key := id.String()

		prev, known := w.servers[key]
		if !known || now.Sub(prev.lastSeen) > serverMemory {
			// Severity turns on whether this segment already had a server.
			// One server is infrastructure; a second is a fault, and usually a
			// fault that takes the whole broadcast domain with it.
			others := w.otherLiveServersLocked(key, now)
			sev := event.SevNotice
			if others > 0 {
				sev = event.SevError
			}
			e := w.b.New(event.SourceDHCP, event.KindDHCPServerSeen, sev,
				event.Host(key, p.SrcMAC.String())).
				WithAttr("server", key).
				WithAttr("server_mac", p.SrcMAC.String()).
				WithAttr("other_servers", others).
				WithAttr("first_seen", !known).
				WithDedup("dhcp.server_seen|"+key).
				WithEvidence("message", dhcpTypeName(m.Type))
			if m.Router != nil {
				e.WithAttr("offers_gateway", m.Router.String())
			}
			if p.VLAN != 0 {
				e.WithAttr("vlan", int(p.VLAN))
			}
			out = append(out, e)
		}
		w.servers[key] = &serverState{
			firstSeen: firstSeenOr(prev, now),
			lastSeen:  now,
			mac:       p.SrcMAC.String(),
		}
	}

	switch m.Type {
	case dhcpOffer, dhcpAck:
		if m.ClientMAC == nil || m.YourIP == nil || m.YourIP.IsUnspecified() {
			break
		}
		kind := event.KindDHCPOffer
		if m.Type == dhcpAck {
			kind = event.KindDHCPAck
		}
		e := w.b.New(event.SourceDHCP, kind, event.SevInfo,
			event.Host(m.YourIP.String(), m.ClientMAC.String())).
			WithAttr("client_mac", m.ClientMAC.String()).
			WithAttr("address", m.YourIP.String()).
			WithDedup(fmt.Sprintf("%s|%s|%s", kind, m.ClientMAC, m.YourIP))
		if m.ServerID != nil {
			e.WithAttr("server", m.ServerID.String())
		}
		if m.LeaseTime > 0 {
			e.WithAttr("lease_seconds", int(m.LeaseTime.Seconds()))
		}
		out = append(out, e)

		if m.Type == dhcpAck {
			if ev := w.leaseChangedLocked(m, now); ev != nil {
				out = append(out, ev)
			}
		}

	case dhcpNak:
		e := w.b.New(event.SourceDHCP, event.KindDHCPNak, event.SevWarn,
			event.Host("", macOrEmpty(m.ClientMAC))).
			WithAttr("client_mac", macOrEmpty(m.ClientMAC)).
			WithDedup("dhcp.nak|" + macOrEmpty(m.ClientMAC))
		if m.ServerID != nil {
			e.WithAttr("server", m.ServerID.String())
		}
		if m.RequestedIP != nil {
			e.WithAttr("refused", m.RequestedIP.String())
		}
		out = append(out, e)
	}

	return out
}

// leaseChangedLocked reports a client whose address is not the one it had.
func (w *DHCPWatcher) leaseChangedLocked(m DHCPMessage, now time.Time) *event.Event {
	mac := m.ClientMAC.String()
	addr := m.YourIP.String()

	prev, known := w.leases[mac]
	if len(w.leases) >= maxLeases && !known {
		return nil // bounded rather than growing without limit
	}
	w.leases[mac] = addr

	if !known || prev == addr {
		return nil
	}
	return w.b.New(event.SourceDHCP, event.KindDHCPLeaseChanged, event.SevNotice,
		event.Host(addr, mac)).
		WithAttr("client_mac", mac).
		WithAttr("address_old", prev).
		WithAttr("address_new", addr).
		WithDedup("dhcp.lease_changed|" + mac)
}

// otherLiveServersLocked counts servers other than this one that have spoken
// recently enough to still be considered present.
func (w *DHCPWatcher) otherLiveServersLocked(exclude string, now time.Time) int {
	n := 0
	for key, s := range w.servers {
		if key == exclude {
			continue
		}
		if now.Sub(s.lastSeen) <= serverMemory {
			n++
		}
	}
	return n
}

func firstSeenOr(prev *serverState, now time.Time) time.Time {
	if prev != nil && !prev.firstSeen.IsZero() {
		return prev.firstSeen
	}
	return now
}

func macOrEmpty(mac net.HardwareAddr) string {
	if mac == nil {
		return ""
	}
	return mac.String()
}
