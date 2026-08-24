package probe

import (
	"testing"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newAnalyser() *Analyser {
	return NewAnalyser(event.NewBuilder("obs", nil), (&clock{t: time.Now()}).now)
}

func round(target string, sent, recv int, rtt time.Duration) Result {
	r := Result{Target: target, Sent: sent, Recv: recv}
	for i := 0; i < recv; i++ {
		r.RTTs = append(r.RTTs, rtt)
	}
	return r
}

// A healthy round says nothing. A collector that reports on health is a
// collector nobody reads by the second week.
func TestAHealthyRoundIsSilent(t *testing.T) {
	a := newAnalyser()
	if got := a.Observe(round("10.0.0.1", 5, 5, 2*time.Millisecond)); len(got) != 0 {
		t.Errorf("a healthy round produced %d events", len(got))
	}
}

func TestPacketLossIsReported(t *testing.T) {
	a := newAnalyser()
	got := a.Observe(round("10.0.0.1", 5, 3, 2*time.Millisecond)) // 40% loss

	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	e := got[0]
	if e.Kind != event.KindMetricAnomaly {
		t.Errorf("kind = %s", e.Kind)
	}
	if event.Str(e, "metric") != "packet_loss" {
		t.Errorf("metric = %q", event.Str(e, "metric"))
	}
	if e.Severity != event.SevWarn {
		t.Errorf("severity = %s, want warn", e.Severity)
	}
}

// One unlucky packet is not a fault. Reporting it as one is how a monitoring
// tool trains its operator to ignore it.
func TestOneLostPacketIsNotAFault(t *testing.T) {
	a := newAnalyser()
	if got := a.Observe(round("10.0.0.1", 10, 9, 2*time.Millisecond)); len(got) != 0 {
		t.Errorf("10%% loss was reported as a fault: %d events", len(got))
	}
}

// A target that stays down must not produce an event every ten seconds for the
// rest of the outage.
func TestAnOutageIsReportedOnceAndRecoveryIsReportedToo(t *testing.T) {
	a := newAnalyser()

	first := a.Observe(round("10.0.0.1", 5, 0, 0))
	if len(first) != 1 || event.Str(first[0], "metric") != "unreachable" {
		t.Fatalf("the outage was not reported: %v", first)
	}
	if first[0].Severity != event.SevError {
		t.Errorf("an unreachable target is severity %s, want error", first[0].Severity)
	}

	for i := 0; i < 5; i++ {
		if got := a.Observe(round("10.0.0.1", 5, 0, 0)); len(got) != 0 {
			t.Fatalf("round %d repeated the outage", i+2)
		}
	}

	// It comes back. The timeline needs to know when it stopped, not only when
	// it started.
	recovered := a.Observe(round("10.0.0.1", 5, 5, 2*time.Millisecond))
	if len(recovered) != 1 || event.Str(recovered[0], "metric") != "recovered" {
		t.Fatalf("recovery was not reported: %v", recovered)
	}

	// And a fresh outage after a recovery is a fresh finding.
	again := a.Observe(round("10.0.0.1", 5, 0, 0))
	if len(again) != 1 {
		t.Error("an outage after a recovery was swallowed")
	}
}

// Latency is judged against the link's own history, not against a number
// somebody guessed - a satellite link at 600ms is healthy and a LAN at 60ms is
// not.
func TestLatencyIsJudgedAgainstABaseline(t *testing.T) {
	a := newAnalyser()

	// Establish what normal looks like.
	for i := 0; i < baselineRounds+1; i++ {
		if got := a.Observe(round("10.0.0.1", 5, 5, 30*time.Millisecond)); len(got) != 0 {
			t.Fatalf("baseline round %d produced an event: %v", i, got)
		}
	}

	spike := a.Observe(round("10.0.0.1", 5, 5, 200*time.Millisecond))
	if len(spike) != 1 || event.Str(spike[0], "metric") != "latency" {
		t.Fatalf("a sevenfold rise was not reported: %v", spike)
	}
	if ms, _ := event.Int(spike[0], "baseline_ms"); ms != 30 {
		t.Errorf("baseline_ms = %d, want 30", ms)
	}
}

func TestNoLatencyJudgementWithoutABaseline(t *testing.T) {
	a := newAnalyser()
	// A single very slow round, with no history to compare it against.
	if got := a.Observe(round("10.0.0.1", 5, 5, 900*time.Millisecond)); len(got) != 0 {
		t.Errorf("latency was judged before there was anything to judge it against: %v", got)
	}
}

// Three times almost nothing is still almost nothing.
func TestATinyRiseOnAFastLinkIsNotASpike(t *testing.T) {
	a := newAnalyser()
	for i := 0; i < baselineRounds+1; i++ {
		a.Observe(round("10.0.0.1", 5, 5, 200*time.Microsecond))
	}
	if got := a.Observe(round("10.0.0.1", 5, 5, time.Millisecond)); len(got) != 0 {
		t.Errorf("a 0.8ms rise was reported as a spike: %v", got)
	}
}

// Median, not mean: one retransmitted packet drags a mean far enough to look
// like a fault.
func TestMedianIgnoresOneOutlier(t *testing.T) {
	r := Result{Target: "x", Sent: 5, Recv: 5, RTTs: []time.Duration{
		2 * time.Millisecond, 2 * time.Millisecond, 3 * time.Millisecond,
		2 * time.Millisecond, 900 * time.Millisecond,
	}}
	if got := r.Median(); got != 2*time.Millisecond && got != 3*time.Millisecond {
		t.Errorf("median = %v, want about 2-3ms despite the outlier", got)
	}
}

func TestLossArithmetic(t *testing.T) {
	cases := []struct {
		sent, recv int
		want       float64
	}{
		{5, 5, 0}, {5, 0, 1}, {5, 4, 0.2}, {0, 0, 0},
	}
	for _, c := range cases {
		got := Result{Sent: c.sent, Recv: c.recv}.Loss()
		if got < c.want-0.001 || got > c.want+0.001 {
			t.Errorf("Loss(%d/%d) = %v, want %v", c.recv, c.sent, got, c.want)
		}
	}
}

func TestTargetsAreTrackedSeparately(t *testing.T) {
	a := newAnalyser()

	if got := a.Observe(round("10.0.0.1", 5, 0, 0)); len(got) != 1 {
		t.Fatal("the first target's outage was not reported")
	}
	// A second target going down is its own finding, not a repeat.
	if got := a.Observe(round("10.0.0.2", 5, 0, 0)); len(got) != 1 {
		t.Error("a second target's outage was swallowed by the first")
	}
}

// A measurement is not an observation, and the confidence should say so.
func TestMeasuredEventsAreNotFullyCertain(t *testing.T) {
	a := newAnalyser()
	got := a.Observe(round("10.0.0.1", 5, 0, 0))
	if len(got) == 0 {
		t.Fatal("nothing reported")
	}
	if got[0].Confidence >= 100 {
		t.Errorf("confidence = %d: a probe measures, it does not observe", got[0].Confidence)
	}
}
