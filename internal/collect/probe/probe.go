// Package probe measures reachability rather than waiting to be told about it.
//
// Every other collector in this project is passive: it reports what the kernel
// or the wire happened to say. That leaves a gap the README opens with -
// "34% packet loss outbound" - because nothing announces packet loss. Loss is
// the absence of something, and an absence has to be measured.
//
// This is the only part of NetRewind that puts a packet on the network. It
// sends a handful of small probes a minute to targets it was told about or
// discovered from the routing table, and reports when the answer stops coming
// back or starts taking much longer than it used to.
package probe

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
)

const (
	// Interval is how often each target is probed.
	Interval = 10 * time.Second
	// probesPerRound is how many packets go out per target per round. Enough
	// to distinguish loss from a single unlucky packet, few enough that the
	// recorder is not itself traffic worth noticing.
	probesPerRound = 5
	// Timeout bounds one probe.
	Timeout = 2 * time.Second
	// baselineRounds is how many rounds are needed before latency is judged
	// against history rather than against nothing.
	baselineRounds = 6
	// lossThreshold is the fraction of probes that must go missing before a
	// round counts as lossy. One packet in five is 20%.
	lossThreshold = 0.2
	// latencyFactor is how many times the established median a round has to
	// take before it is a spike rather than jitter.
	latencyFactor = 3.0
	// minLatencyRise stops a spike being reported on targets that answer in
	// microseconds, where three times nothing is still nothing.
	minLatencyRise = 20 * time.Millisecond
)

// Result is the outcome of one round against one target.
type Result struct {
	Target string
	Sent   int
	Recv   int
	// RTTs holds the round-trip time of each reply, in order.
	RTTs []time.Duration
}

// Loss is the fraction of probes that did not come back.
func (r Result) Loss() float64 {
	if r.Sent == 0 {
		return 0
	}
	return float64(r.Sent-r.Recv) / float64(r.Sent)
}

// Median returns the middle round-trip time, or zero if nothing came back.
//
// Median rather than mean on purpose: one retransmitted packet drags a mean
// far enough to look like a fault, and a fault that reports itself on healthy
// networks stops being read.
func (r Result) Median() time.Duration {
	if len(r.RTTs) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), r.RTTs...)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	return sorted[len(sorted)/2]
}

type targetState struct {
	// history holds recent medians, for a baseline that adapts to the link
	// rather than to a number somebody guessed.
	history []time.Duration
	lossy   bool
	slow    bool
	down    bool
}

// Analyser turns probe results into events.
//
// It is separate from anything that sends a packet so the judgement - what
// counts as loss, what counts as a spike, when to stop repeating yourself - is
// testable without a network.
type Analyser struct {
	b   *event.Builder
	now func() time.Time

	mu      sync.Mutex
	targets map[string]*targetState
}

// NewAnalyser returns an analyser. A nil clock means time.Now.
func NewAnalyser(b *event.Builder, now func() time.Time) *Analyser {
	if now == nil {
		now = time.Now
	}
	return &Analyser{b: b, now: now, targets: make(map[string]*targetState)}
}

// Observe folds one round in and returns the events it justifies.
func (a *Analyser) Observe(r Result) []*event.Event {
	a.mu.Lock()
	defer a.mu.Unlock()

	st, known := a.targets[r.Target]
	if !known {
		st = &targetState{}
		a.targets[r.Target] = st
	}

	var out []*event.Event

	switch {
	case r.Recv == 0 && r.Sent > 0:
		// Nothing came back at all. Reported once, and again only after it has
		// recovered - a target that stays down must not produce an event every
		// ten seconds for the rest of the outage.
		if !st.down {
			st.down = true
			out = append(out, a.anomaly(r, "unreachable", event.SevError,
				fmt.Sprintf("%d of %d probes went unanswered", r.Sent, r.Sent)).
				WithAttr("loss_percent", 100.0))
		}
		st.lossy = true
		return out

	case r.Loss() >= lossThreshold:
		if !st.lossy {
			st.lossy = true
			out = append(out, a.anomaly(r, "packet_loss", event.SevWarn,
				fmt.Sprintf("%d of %d probes went unanswered", r.Sent-r.Recv, r.Sent)).
				WithAttr("loss_percent", round1(r.Loss()*100)))
		}

	default:
		// Recovered. Say so: an operator reading the timeline needs to know
		// when it stopped, not only when it started.
		if st.down || st.lossy {
			was := "packet loss"
			if st.down {
				was = "unreachability"
			}
			out = append(out, a.anomaly(r, "recovered", event.SevNotice,
				fmt.Sprintf("%s ended; all %d probes answered", was, r.Sent)))
		}
		st.down, st.lossy = false, false
	}

	// Latency is only judged once there is enough history to judge against.
	median := r.Median()
	if median > 0 {
		if len(st.history) >= baselineRounds {
			baseline := medianOf(st.history)
			if baseline > 0 && float64(median) > float64(baseline)*latencyFactor &&
				median-baseline > minLatencyRise {
				if !st.slow {
					st.slow = true
					out = append(out, a.anomaly(r, "latency", event.SevWarn,
						fmt.Sprintf("round trip rose from %s to %s",
							baseline.Round(time.Millisecond), median.Round(time.Millisecond))).
						WithAttr("rtt_ms", median.Milliseconds()).
						WithAttr("baseline_ms", baseline.Milliseconds()))
				}
			} else {
				st.slow = false
			}
		}
		st.history = append(st.history, median)
		if len(st.history) > 30 {
			st.history = st.history[1:]
		}
	}

	return out
}

func (a *Analyser) anomaly(r Result, kind string, sev event.Severity, why string) *event.Event {
	return a.b.New(event.SourceProbe, event.KindMetricAnomaly, sev,
		event.Host(r.Target, "")).
		WithAttr("target", r.Target).
		WithAttr("metric", kind).
		WithAttr("probes", r.Sent).
		WithAttr("answered", r.Recv).
		// Measured rather than observed, so it does not claim the certainty of
		// something the kernel reported.
		WithConfidence(90).
		WithDedup(fmt.Sprintf("metric.anomaly|%s|%s", r.Target, kind)).
		WithEvidence("summary", why)
}

func medianOf(in []time.Duration) time.Duration {
	if len(in) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), in...)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	return sorted[len(sorted)/2]
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// ParseTargets splits the comma-separated address list the daemon is given.
//
// Empty entries are ignored so a trailing comma is not a reason to refuse to
// start. It lives here rather than in each platform's collector because the
// daemon validates the same list before either of them is constructed, and
// three copies of one split is three chances for them to disagree.
func ParseTargets(s string) []string {
	var out []string
	for _, t := range strings.Split(s, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}
