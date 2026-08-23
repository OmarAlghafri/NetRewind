package flow

import (
	"net"
	"testing"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
)

func ip(s string) uint32 {
	b := net.ParseIP(s).To4()
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestTracker(t *testing.T) (*Tracker, *fakeClock) {
	t.Helper()
	clk := &fakeClock{t: time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)}
	return NewTracker(event.NewBuilder("obs", nil), clk.now), clk
}

// connect drives a successful handshake and then a close.
func connect(tr *Tracker, src, dst string, sport, dport uint16) {
	tr.Observe(ip(src), ip(dst), sport, dport, tcpClose, tcpSynSent)
	tr.Observe(ip(src), ip(dst), sport, dport, tcpSynSent, tcpEstablished)
	tr.Observe(ip(src), ip(dst), sport, dport, tcpEstablished, tcpClose)
}

// refused drives a connection that is never answered.
func refused(tr *Tracker, src, dst string, sport, dport uint16) *event.Event {
	tr.Observe(ip(src), ip(dst), sport, dport, tcpClose, tcpSynSent)
	return tr.Observe(ip(src), ip(dst), sport, dport, tcpSynSent, tcpClose)
}

func TestUnansweredConnectionIsOrdinaryOnItsOwn(t *testing.T) {
	tr, _ := newTestTracker(t)

	e := refused(tr, "10.0.0.5", "10.0.0.9", 40000, 9999)
	if e == nil {
		t.Fatal("an unanswered connection produced nothing")
	}
	if e.Kind != event.KindFlowHandshakeFail {
		t.Errorf("kind = %s, want %s", e.Kind, event.KindFlowHandshakeFail)
	}
	if e.Severity != event.SevNotice {
		t.Errorf("severity = %s: a closed port is ordinary, not a warning", e.Severity)
	}
}

// The finding worth waking someone for: two machines that were talking a moment
// ago no longer can.
func TestAPairThatStopsWorkingIsReported(t *testing.T) {
	tr, clk := newTestTracker(t)

	connect(tr, "10.0.0.5", "10.0.0.9", 40000, 443)
	clk.advance(30 * time.Second)

	e := refused(tr, "10.0.0.5", "10.0.0.9", 40001, 443)
	if e == nil {
		t.Fatal("nothing reported")
	}
	if e.Kind != event.KindFlowFirstFailureForPair {
		t.Fatalf("kind = %s, want %s", e.Kind, event.KindFlowFirstFailureForPair)
	}
	if e.Severity != event.SevWarn {
		t.Errorf("severity = %s, want warn", e.Severity)
	}
	if got, _ := event.Int(e, "last_success_ago_ms"); got != 30000 {
		t.Errorf("last_success_ago_ms = %d, want 30000", got)
	}
}

// A broken pair must not produce the same finding on every retry, or the
// operator learns to ignore it.
func TestABrokenPairIsReportedOnce(t *testing.T) {
	tr, clk := newTestTracker(t)

	connect(tr, "10.0.0.5", "10.0.0.9", 40000, 443)
	clk.advance(time.Second)

	first := refused(tr, "10.0.0.5", "10.0.0.9", 40001, 443)
	if first.Kind != event.KindFlowFirstFailureForPair {
		t.Fatalf("first failure not reported: %s", first.Kind)
	}
	for i := 0; i < 3; i++ {
		clk.advance(time.Second)
		again := refused(tr, "10.0.0.5", "10.0.0.9", uint16(40002+i), 443)
		if again.Kind == event.KindFlowFirstFailureForPair {
			t.Fatalf("retry %d reported the same break again", i)
		}
	}
}

// After a recovery, a fresh break is a fresh finding.
func TestRecoveryReArmsTheFinding(t *testing.T) {
	tr, clk := newTestTracker(t)

	connect(tr, "10.0.0.5", "10.0.0.9", 40000, 443)
	clk.advance(time.Second)
	refused(tr, "10.0.0.5", "10.0.0.9", 40001, 443)

	clk.advance(time.Second)
	connect(tr, "10.0.0.5", "10.0.0.9", 40002, 443) // it works again
	clk.advance(time.Second)

	e := refused(tr, "10.0.0.5", "10.0.0.9", 40003, 443)
	if e.Kind != event.KindFlowFirstFailureForPair {
		t.Errorf("a break after a recovery was not reported: %s", e.Kind)
	}
}

// A pair that last worked long ago is not evidence that anything just changed.
func TestAncientSuccessIsNotEvidence(t *testing.T) {
	tr, clk := newTestTracker(t)

	connect(tr, "10.0.0.5", "10.0.0.9", 40000, 443)
	clk.advance(pairMemory + time.Hour)

	e := refused(tr, "10.0.0.5", "10.0.0.9", 40001, 443)
	if e.Kind != event.KindFlowHandshakeFail {
		t.Errorf("kind = %s: a success a day ago should not imply a recent change", e.Kind)
	}
}

// The port is part of the relationship: a working web server says nothing about
// a database port on the same host.
func TestPairsAreDistinguishedByPort(t *testing.T) {
	tr, clk := newTestTracker(t)

	connect(tr, "10.0.0.5", "10.0.0.9", 40000, 443)
	clk.advance(time.Second)

	e := refused(tr, "10.0.0.5", "10.0.0.9", 40001, 5432)
	if e.Kind != event.KindFlowHandshakeFail {
		t.Errorf("kind = %s: a different port is a different relationship", e.Kind)
	}
}

// A service starting or stopping is not a connection opening or closing.
func TestListenTransitionsAreIgnored(t *testing.T) {
	tr, _ := newTestTracker(t)

	if e := tr.Observe(ip("0.0.0.0"), ip("0.0.0.0"), 9100, 0, tcpClose, tcpListen); e != nil {
		t.Error("a service starting produced an event")
	}
	if e := tr.Observe(ip("0.0.0.0"), ip("0.0.0.0"), 9100, 0, tcpListen, tcpClose); e != nil {
		t.Error("a service stopping produced an event")
	}
	if r := tr.Rollup(); r != nil {
		t.Errorf("a listening socket was counted as connection activity: %v", r.Attrs)
	}
}

func TestRollupCountsAndClears(t *testing.T) {
	tr, clk := newTestTracker(t)

	connect(tr, "10.0.0.5", "10.0.0.9", 40000, 443)
	clk.advance(50 * time.Millisecond)
	connect(tr, "10.0.0.5", "10.0.0.10", 40001, 443)
	refused(tr, "10.0.0.5", "10.0.0.11", 40002, 9999)

	r := tr.Rollup()
	if r == nil {
		t.Fatal("no rollup produced")
	}
	if got, _ := event.Int(r, "opened"); got != 2 {
		t.Errorf("opened = %d, want 2", got)
	}
	if got, _ := event.Int(r, "closed"); got != 2 {
		t.Errorf("closed = %d, want 2", got)
	}
	if got, _ := event.Int(r, "handshake_failures"); got != 1 {
		t.Errorf("handshake_failures = %d, want 1", got)
	}
	if got, _ := event.Int(r, "distinct_peers"); got != 3 {
		t.Errorf("distinct_peers = %d, want 3", got)
	}
	if tr.Rollup() != nil {
		t.Error("the second rollup reported activity that was already reported")
	}
}

func TestConnectionDurationIsMeasured(t *testing.T) {
	tr, clk := newTestTracker(t)

	tr.Observe(ip("10.0.0.5"), ip("10.0.0.9"), 40000, 443, tcpSynSent, tcpEstablished)
	clk.advance(250 * time.Millisecond)
	tr.Observe(ip("10.0.0.5"), ip("10.0.0.9"), 40000, 443, tcpEstablished, tcpClose)

	r := tr.Rollup()
	if got, _ := event.Int(r, "mean_duration_ms"); got != 250 {
		t.Errorf("mean_duration_ms = %d, want 250", got)
	}
}
