package wire

import (
	"encoding/binary"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
)

const dnsPort = 53

// DNS response codes worth naming. Anything else is reported by number.
const (
	rcodeNoError  = 0
	rcodeFormErr  = 1
	rcodeServFail = 2
	rcodeNXDomain = 3
	rcodeRefused  = 5
)

func rcodeName(c uint8) string {
	switch c {
	case rcodeNoError:
		return "noerror"
	case rcodeFormErr:
		return "formerr"
	case rcodeServFail:
		return "servfail"
	case rcodeNXDomain:
		return "nxdomain"
	case rcodeRefused:
		return "refused"
	default:
		return fmt.Sprintf("rcode(%d)", c)
	}
}

// DNSMessage is the metadata of one DNS message. The answer section is never
// read: what was asked and whether it worked is the whole of what a network
// recorder needs, and the rest is the user's business.
type DNSMessage struct {
	ID       uint16
	Response bool
	Rcode    uint8
	Question string
	QType    uint16
}

// ParseDNS decodes the header and the first question.
func ParseDNS(payload []byte) (DNSMessage, error) {
	var m DNSMessage
	if len(payload) < 12 {
		return m, ErrNotForUs
	}
	m.ID = binary.BigEndian.Uint16(payload[0:2])
	flags := binary.BigEndian.Uint16(payload[2:4])
	m.Response = flags&0x8000 != 0
	m.Rcode = uint8(flags & 0x000f)

	qdCount := binary.BigEndian.Uint16(payload[4:6])
	if qdCount == 0 {
		return m, nil
	}

	name, next, err := readName(payload, 12)
	if err != nil {
		// A malformed question is not a reason to give up on the header, which
		// already carries the rcode - the part that says whether it worked.
		return m, nil
	}
	m.Question = name
	if next+4 <= len(payload) {
		m.QType = binary.BigEndian.Uint16(payload[next : next+2])
	}
	return m, nil
}

// readName walks the label sequence, following at most a few compression
// pointers.
//
// The pointer limit is not politeness. A crafted packet can point a label at
// itself, and a parser that follows without counting loops forever on data
// anyone on the segment can send.
func readName(buf []byte, off int) (string, int, error) {
	var parts []string
	jumps := 0
	next := -1

	for {
		if off >= len(buf) {
			return "", 0, ErrNotForUs
		}
		length := int(buf[off])

		if length == 0 {
			off++
			if next < 0 {
				next = off
			}
			break
		}

		if length&0xc0 == 0xc0 { // compression pointer
			if off+1 >= len(buf) {
				return "", 0, ErrNotForUs
			}
			if jumps++; jumps > 8 {
				return "", 0, ErrNotForUs
			}
			if next < 0 {
				next = off + 2
			}
			off = int(binary.BigEndian.Uint16(buf[off:off+2]) & 0x3fff)
			continue
		}

		if length > 63 || off+1+length > len(buf) {
			return "", 0, ErrNotForUs
		}
		parts = append(parts, string(buf[off+1:off+1+length]))
		off += 1 + length

		if len(parts) > 127 {
			return "", 0, ErrNotForUs
		}
	}

	return strings.Join(parts, "."), next, nil
}

/* ------------------------------------------------------------------ */
/* Watcher                                                            */
/* ------------------------------------------------------------------ */

const (
	// resolverMemory bounds how long a client's resolver choice is remembered.
	resolverMemory = 24 * time.Hour
	// maxClients bounds the resolver table.
	maxClients = 8192
	// maxPending bounds outstanding queries awaiting a response, for latency.
	maxPending = 4096
	// slowQuery is when a resolver's latency stops being ordinary.
	slowQuery = 750 * time.Millisecond
)

type pendingQuery struct {
	at       time.Time
	resolver string
	name     string
}

// DNSWatcher turns DNS metadata into events.
//
// The one that matters is dns.resolver_changed: a client that was asking one
// resolver is now asking another. That is what DHCP poisoning, a hijacked
// gateway and a misconfigured VPN all look like from the wire, and it is
// invisible from the local kernel because it is someone else's traffic.
type DNSWatcher struct {
	b   *event.Builder
	now func() time.Time
	// RecordNames controls whether query names are stored. There are networks
	// where recording them is not permitted, so this is a switch that turns
	// them off entirely rather than a setting someone has to remember.
	RecordNames bool

	mu        sync.Mutex
	resolvers map[string]resolverChoice // client IP -> resolver it uses
	pending   map[uint32]pendingQuery
}

type resolverChoice struct {
	resolver string
	at       time.Time
}

// NewDNSWatcher returns a watcher. A nil clock means time.Now.
func NewDNSWatcher(b *event.Builder, now func() time.Time, recordNames bool) *DNSWatcher {
	if now == nil {
		now = time.Now
	}
	return &DNSWatcher{
		b:           b,
		now:         now,
		RecordNames: recordNames,
		resolvers:   make(map[string]resolverChoice),
		pending:     make(map[uint32]pendingQuery),
	}
}

// Observe folds one DNS message in and returns the events it justifies.
func (w *DNSWatcher) Observe(p Packet, m DNSMessage) []*event.Event {
	now := w.now()
	w.mu.Lock()
	defer w.mu.Unlock()

	if !m.Response {
		return w.query(p, m, now)
	}
	return w.response(p, m, now)
}

func (w *DNSWatcher) query(p Packet, m DNSMessage, now time.Time) []*event.Event {
	client, resolver := p.SrcIP.String(), p.DstIP.String()

	if len(w.pending) < maxPending {
		w.pending[pendingKey(p.SrcIP, m.ID)] = pendingQuery{at: now, resolver: resolver, name: m.Question}
	}

	prev, known := w.resolvers[client]
	if len(w.resolvers) >= maxClients && !known {
		return nil
	}
	w.resolvers[client] = resolverChoice{resolver: resolver, at: now}

	if !known || prev.resolver == resolver || now.Sub(prev.at) > resolverMemory {
		return nil
	}
	return []*event.Event{
		w.b.New(event.SourceDNS, event.KindDNSResolverChanged, event.SevWarn,
			event.Host(client, "")).
			WithAttr("client", client).
			WithAttr("resolver_old", prev.resolver).
			WithAttr("resolver_new", resolver).
			WithDedup("dns.resolver_changed|"+client).
			WithEvidence("last_used", prev.at.UTC().Format(time.RFC3339)),
	}
}

func (w *DNSWatcher) response(p Packet, m DNSMessage, now time.Time) []*event.Event {
	key := pendingKey(p.DstIP, m.ID)
	q, waiting := w.pending[key]
	if waiting {
		delete(w.pending, key)
	}

	var out []*event.Event

	// SERVFAIL and REFUSED mean the resolver failed. NXDOMAIN means it worked
	// and the name does not exist, which is an application's problem and not a
	// network fault - recording it would bury the two that are.
	if m.Rcode == rcodeServFail || m.Rcode == rcodeRefused || m.Rcode == rcodeFormErr {
		resolver := p.SrcIP.String()
		e := w.b.New(event.SourceDNS, event.KindDNSQueryFail, event.SevNotice,
			event.Host(resolver, "")).
			WithAttr("resolver", resolver).
			WithAttr("client", p.DstIP.String()).
			WithAttr("rcode", rcodeName(m.Rcode)).
			WithDedup(fmt.Sprintf("dns.query_fail|%s|%s", resolver, rcodeName(m.Rcode)))
		w.attachName(e, m.Question, q.name)
		out = append(out, e)
	}

	if waiting {
		if took := now.Sub(q.at); took > slowQuery {
			e := w.b.New(event.SourceDNS, event.KindDNSLatencySpike, event.SevNotice,
				event.Host(q.resolver, "")).
				WithAttr("resolver", q.resolver).
				WithAttr("client", p.DstIP.String()).
				WithAttr("took_ms", took.Milliseconds()).
				WithDedup("dns.latency_spike|" + q.resolver)
			w.attachName(e, m.Question, q.name)
			out = append(out, e)
		}
	}
	return out
}

// attachName adds the queried name only when recording names is permitted.
func (w *DNSWatcher) attachName(e *event.Event, names ...string) {
	if !w.RecordNames {
		return
	}
	for _, n := range names {
		if n != "" {
			e.WithAttr("name", n)
			return
		}
	}
}

// Sweep drops queries that were never answered, so the pending table cannot
// grow on a resolver that has stopped responding entirely.
func (w *DNSWatcher) Sweep() {
	now := w.now()
	w.mu.Lock()
	defer w.mu.Unlock()
	for k, q := range w.pending {
		if now.Sub(q.at) > 30*time.Second {
			delete(w.pending, k)
		}
	}
	for k, r := range w.resolvers {
		if now.Sub(r.at) > resolverMemory {
			delete(w.resolvers, k)
		}
	}
}

// pendingKey pairs a client with a transaction id. The id alone collides
// constantly - it is sixteen bits and every client picks its own.
func pendingKey(client net.IP, id uint16) uint32 {
	v4 := client.To4()
	if v4 == nil {
		return uint32(id)
	}
	return (uint32(v4[2])<<24 | uint32(v4[3])<<16) | uint32(id)
}
