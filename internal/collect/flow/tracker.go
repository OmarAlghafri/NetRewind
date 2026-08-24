package flow

import (
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
)

// TCP states, from the kernel's tcp_states.h. They are an ABI.
const (
	tcpEstablished = 1
	tcpSynSent     = 2
	tcpSynRecv     = 3
	tcpClose       = 7
	tcpListen      = 10
)

const (
	// RollupInterval is how often ordinary connection activity is summarised.
	//
	// Individual connections are not events. A busy segment opens thousands a
	// second, and recording each would fill the store in a day while telling an
	// operator nothing they could not get from a counter. Only the anomalies
	// are worth a row of their own.
	RollupInterval = 10 * time.Second
	// maxTracked bounds the open-connection table. Beyond it, durations stop
	// being measured and the shortfall is reported rather than hidden.
	maxTracked = 65536
	// maxPairs bounds the memory of which pairs have ever worked.
	maxPairs = 16384
	// pairMemory is how long a past success stays relevant. A service that
	// worked yesterday and fails today is worth reporting; one that last worked
	// a month ago is not evidence of anything having changed.
	pairMemory = 24 * time.Hour
	// resetMemory is how long a reset waits for its state change. The two
	// arrive microseconds apart; anything older is a reset whose close was
	// missed.
	resetMemory = 5 * time.Second
)

// flowKey identifies one connection.
type flowKey struct {
	saddr, daddr uint32
	sport, dport uint16
}

// pairKey identifies a client talking to a service, without the ephemeral
// source port - it is the relationship that matters, not the socket.
type pairKey struct {
	saddr, daddr uint32
	dport        uint16
}

type pairState struct {
	lastSuccess time.Time
	// reported keeps a broken pair from producing the same finding on every
	// retry. It is cleared the moment the pair works again, so a fault that
	// recurs after a recovery is reported afresh.
	reported bool
}

// rollup accumulates ordinary activity between two summaries.
type rollup struct {
	opened    int
	closed    int
	failed    int
	abandoned int
	peers     map[uint32]struct{}
	totalDur  time.Duration
}

func (r *rollup) reset() { *r = rollup{peers: make(map[uint32]struct{})} }

// Tracker turns TCP state transitions into events.
//
// It is deliberately free of anything kernel-specific so that the state machine
// - which is where the judgement lives - can be tested on any platform. The
// eBPF side does nothing but parse bytes and hand them here.
type Tracker struct {
	b   *event.Builder
	now func() time.Time

	mu          sync.Mutex
	established map[flowKey]time.Time
	worked      map[pairKey]pairState
	// resets holds connections a reset arrived for, awaiting the state change
	// that turns it into an event.
	resets    map[flowKey]time.Time
	stats     rollup
	untracked uint64
}

// NewTracker returns a tracker. A nil clock means time.Now.
func NewTracker(b *event.Builder, now func() time.Time) *Tracker {
	if now == nil {
		now = time.Now
	}
	t := &Tracker{
		b:           b,
		now:         now,
		established: make(map[flowKey]time.Time),
		worked:      make(map[pairKey]pairState),
		resets:      make(map[flowKey]time.Time),
	}
	t.stats.reset()
	return t
}

// Observe folds one state transition in and returns the event it justifies, if
// any. Most transitions justify none: they become part of the next rollup.
func (t *Tracker) Observe(saddr, daddr uint32, sport, dport uint16, oldstate, newstate uint8) *event.Event {
	// A socket entering or leaving LISTEN is a service starting or stopping,
	// not a connection opening or closing. Counting it as one made a server
	// shutting down look like a client disconnecting.
	if oldstate == tcpListen || newstate == tcpListen {
		return nil
	}

	key := flowKey{saddr: saddr, daddr: daddr, sport: sport, dport: dport}
	pk := pairKey{saddr: saddr, daddr: daddr, dport: dport}
	now := t.now()

	t.mu.Lock()
	defer t.mu.Unlock()

	switch {
	case newstate == tcpEstablished:
		t.stats.opened++
		t.stats.peers[daddr] = struct{}{}
		if len(t.established) < maxTracked {
			t.established[key] = now
		} else {
			t.untracked++
		}
		t.remember(pk, now)
		return nil

	case newstate == tcpClose && oldstate == tcpSynSent:
		// A connection that went from the opening SYN straight to closed was
		// never answered. Something refused it, dropped it, or was not
		// listening.
		t.stats.failed++
		t.stats.peers[daddr] = struct{}{}
		delete(t.established, key)
		return t.failure(pk, saddr, daddr, sport, dport, now)

	case newstate == tcpClose:
		var lifetime time.Duration
		if openedAt, ok := t.established[key]; ok {
			lifetime = now.Sub(openedAt)
			t.stats.totalDur += lifetime
			delete(t.established, key)
		}
		if oldstate == tcpSynRecv {
			// An inbound connection abandoned after the SYN was answered.
			t.stats.abandoned++
		}
		t.stats.closed++

		// A connection going from ESTABLISHED straight to CLOSE did not shut
		// down: it was killed. Either the peer sent a reset or the stack gave
		// up retransmitting, and those are different faults - "they refused"
		// against "the path disappeared". The reset tracepoint is what tells
		// them apart; without a matching reset, it timed out.
		if oldstate == tcpEstablished {
			if _, reset := t.resets[key]; reset {
				delete(t.resets, key)
				return t.abortive(event.KindFlowReset, event.SevNotice,
					saddr, daddr, sport, dport, lifetime, "a reset arrived")
			}
			return t.abortive(event.KindFlowTimeoutNoClose, event.SevWarn,
				saddr, daddr, sport, dport, lifetime,
				"no reset and no orderly shutdown: the stack gave up")
		}
		return nil
	}
	return nil
}

// Reset records that a reset arrived for a connection. The state change that
// follows is what turns it into an event; on its own a reset says nothing that
// the close does not say better.
func (t *Tracker) Reset(saddr, daddr uint32, sport, dport uint16) {
	key := flowKey{saddr: saddr, daddr: daddr, sport: sport, dport: dport}
	now := t.now()

	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.resets) >= maxTracked {
		// Rather than grow without bound, forget the oldest pending resets.
		// The consequence is a reset reported as a timeout, which is a wrong
		// label on one event - better than a map that eats the machine.
		for k, at := range t.resets {
			if now.Sub(at) > resetMemory {
				delete(t.resets, k)
			}
		}
		if len(t.resets) >= maxTracked {
			return
		}
	}
	t.resets[key] = now
}

func (t *Tracker) abortive(kind event.Kind, sev event.Severity,
	saddr, daddr uint32, sport, dport uint16, lifetime time.Duration, why string) *event.Event {
	src, dst := ipString(saddr), ipString(daddr)
	e := t.b.New(event.SourceEBPF, kind, sev, event.Host(dst, "")).
		WithAttr("src", src).
		WithAttr("dst", dst).
		WithAttr("dport", int(dport)).
		WithDedup(fmt.Sprintf("%s|%s|%d", kind, dst, dport)).
		WithEvidence("reason", why)
	if lifetime > 0 {
		e.WithAttr("lifetime_ms", lifetime.Milliseconds())
	}
	return e
}

// remember records that a pair completed a handshake, and re-arms it so a
// failure after a recovery is reported again.
func (t *Tracker) remember(pk pairKey, now time.Time) {
	if len(t.worked) >= maxPairs {
		t.forgetStale(now)
	}
	if len(t.worked) >= maxPairs {
		return // still full: stop remembering rather than grow without bound
	}
	t.worked[pk] = pairState{lastSuccess: now}
}

func (t *Tracker) forgetStale(now time.Time) {
	for k, v := range t.worked {
		if now.Sub(v.lastSuccess) > pairMemory {
			delete(t.worked, k)
		}
	}
}

// failure decides which of two findings an unanswered connection is.
//
// An unanswered connection on its own is ordinary: closed ports are closed. The
// finding worth waking someone for is an unanswered connection between two
// machines that *were talking a moment ago*, because that means something
// changed - a filtering rule, an ACL, a route, a service - and it is the
// single strongest signal in the whole system that a change just broke
// something.
func (t *Tracker) failure(pk pairKey, saddr, daddr uint32, sport, dport uint16, now time.Time) *event.Event {
	src, dst := ipString(saddr), ipString(daddr)

	if ps, ok := t.worked[pk]; ok && !ps.reported {
		if since := now.Sub(ps.lastSuccess); since <= pairMemory {
			ps.reported = true
			t.worked[pk] = ps
			return t.b.New(event.SourceEBPF, event.KindFlowFirstFailureForPair, event.SevWarn,
				event.Host(dst, "")).
				WithAttr("src", src).
				WithAttr("dst", dst).
				WithAttr("dport", int(dport)).
				WithAttr("last_success_ago_ms", since.Milliseconds()).
				WithDedup(fmt.Sprintf("flow.first_failure_for_pair|%s|%s|%d", src, dst, dport)).
				WithEvidence("transition", "SYN_SENT -> CLOSE").
				WithEvidence("previously_established", ps.lastSuccess.UTC().Format(time.RFC3339))
		}
	}

	return t.b.New(event.SourceEBPF, event.KindFlowHandshakeFail, event.SevNotice,
		event.Host(dst, "")).
		WithAttr("src", src).
		WithAttr("dst", dst).
		WithAttr("dport", int(dport)).
		WithAttr("sport", int(sport)).
		// Folding on destination and port keeps a port scan from filling the
		// store with one row per port.
		WithDedup(fmt.Sprintf("flow.handshake_fail|%s|%d", dst, dport)).
		WithEvidence("transition", "SYN_SENT -> CLOSE")
}

// Rollup returns the periodic summary, or nil if nothing happened.
func (t *Tracker) Rollup() *event.Event {
	t.mu.Lock()
	stats := t.stats
	untracked := t.untracked
	tracked := len(t.established)
	pairs := len(t.worked)
	t.stats.reset()
	t.untracked = 0
	t.mu.Unlock()

	if stats.opened+stats.closed+stats.failed == 0 {
		return nil
	}

	e := t.b.New(event.SourceEBPF, event.KindFlowRollup, event.SevInfo,
		event.Observer(t.b.ObserverID)).
		WithAttr("opened", stats.opened).
		WithAttr("closed", stats.closed).
		WithAttr("handshake_failures", stats.failed).
		WithAttr("distinct_peers", len(stats.peers)).
		WithAttr("window_seconds", int(RollupInterval.Seconds())).
		WithAttr("tracked", tracked).
		WithAttr("known_pairs", pairs)
	if stats.closed > 0 {
		e.WithAttr("mean_duration_ms", (stats.totalDur / time.Duration(stats.closed)).Milliseconds())
	}
	if stats.abandoned > 0 {
		e.WithAttr("abandoned_inbound", stats.abandoned)
	}
	if untracked > 0 {
		// Say so rather than quietly reporting fewer connections.
		e.WithAttr("untracked", untracked)
		e.Severity = event.SevNotice
	}
	return e
}

func ipString(addr uint32) string {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], addr)
	return net.IP(b[:]).String()
}
