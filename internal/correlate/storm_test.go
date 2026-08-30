package correlate

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
)

// A flapping port is the burst these rules exist to recognise, and it used to
// be the burst that stopped the recorder recording.
//
// The engine re-walked every event in its window as a candidate opening on
// every arrival, so the cost of one event grew with the window and the cost of
// a burst grew with its cube: two thousand flaps took thirteen seconds of CPU,
// five thousand took thirty-four, and the writer that calls this is the same
// goroutine that makes events durable. A recorder that stalls under a storm has
// failed at the one thing it exists for, and it fails silently - the queue
// backs up, netlink overruns, and the record has a hole in exactly the period
// somebody will later need.
//
// This pins the shape of the cost rather than a number: doubling the burst must
// not do much worse than double the work. A cubic regression fails it by orders
// of magnitude, so the threshold does not need to be tight to be useful, and a
// loose one does not turn a slow CI runner into a red build.
func TestABurstDoesNotCostMoreThanLinearlyMoreThanItself(t *testing.T) {
	rules := shippedRules(t)
	measure := func(n int) time.Duration {
		eng := NewEngine(rules, discardLog())
		b := event.NewBuilder("obs", nil)
		base := time.Unix(1700000000, 0)
		start := time.Now()
		for i := 0; i < n; i++ {
			kind := event.KindLinkDown
			if i%2 == 1 {
				kind = event.KindLinkUp
			}
			e := b.New(event.SourceNetlink, kind, event.SevWarn, event.Iface("eth0", 1))
			// 200 a second: a link flapping as fast as netlink can report it,
			// with the whole burst inside one rule window.
			e.TSWall = base.Add(time.Duration(i) * 5 * time.Millisecond).UnixNano()
			eng.Offer(e)
		}
		return time.Since(start)
	}

	small := measure(2000)
	large := measure(8000)

	// Four times the events. Linear would be four times the work; the ratio is
	// allowed to reach twelve so that a loaded runner cannot fail this, and a
	// return to the old behaviour would be nearer a hundred.
	const factor = 4
	const tolerated = 12.0
	if small <= 0 {
		small = time.Microsecond
	}
	ratio := float64(large) / float64(small)
	t.Logf("%d events in %s, %d in %s (ratio %.1fx for %dx the events)",
		2000, small.Round(time.Millisecond), 8000, large.Round(time.Millisecond), ratio, factor)
	if ratio > tolerated {
		t.Errorf("cost grew %.1fx for %dx the events; correlation is superlinear in the burst again", ratio, factor)
	}

	// And the absolute cost has to stay somewhere a recorder can live. Eight
	// thousand events is forty seconds of a flapping port; taking longer than
	// that to think about them means the recorder is falling behind the network.
	if large > 40*time.Second {
		t.Errorf("8000 events took %s, which is longer than the period they describe", large)
	}
}

// A burst larger than the window is truncated rather than held, and the engine
// says how much it gave up. Growing without limit would end as the process
// being killed, which takes the recording with it.
func TestTheWindowIsBoundedAndSaysWhatItDropped(t *testing.T) {
	rules := shippedRules(t)
	eng := NewEngine(rules, discardLog())
	b := event.NewBuilder("obs", nil)
	base := time.Unix(1700000000, 0)

	const n = maxWindowEvents + 5000
	for i := 0; i < n; i++ {
		// A kind no shipped rule looks for: the bound is a property of the
		// window, not of what the rules make of it, and matching thirty
		// thousand events against nineteen rules would make this a test of how
		// fast the runner is.
		e := b.New(event.SourceNetlink, event.KindLinkMTUChanged, event.SevInfo, event.Iface("eth0", 1))
		// One millisecond apart, so every one is inside every rule's window and
		// nothing is dropped for age.
		e.TSWall = base.Add(time.Duration(i) * time.Millisecond).UnixNano()
		eng.Offer(e)
	}

	eng.mu.Lock()
	held := len(eng.window)
	eng.mu.Unlock()

	if held > maxWindowEvents {
		t.Errorf("window holds %d events, above the %d limit", held, maxWindowEvents)
	}
	if got := eng.Truncated(); got != n-maxWindowEvents {
		t.Errorf("Truncated() = %d, want %d", got, n-maxWindowEvents)
	}
}

// The engine must not report the same flap over and over. The store folds
// repeats of one fact into one row, and an incident chain cites rows; a rule
// whose whole point is that a run of outages is one fault has to conclude once.
func TestARunOfTheSameFaultIsOneConclusion(t *testing.T) {
	rules := shippedRules(t)
	eng := NewEngine(rules, discardLog())
	b := event.NewBuilder("obs", nil)
	base := time.Unix(1700000000, 0)

	// What the store hands the engine after folding: one row per fact inside
	// the fold window, so the same id arrives repeatedly.
	downID, upID := "01ARZ3NDEKTSV4RRFFQ69G5FAV", "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	incidents := 0
	for i := 0; i < 400; i++ {
		kind, id := event.KindLinkDown, downID
		if i%2 == 1 {
			kind, id = event.KindLinkUp, upID
		}
		e := b.New(event.SourceNetlink, kind, event.SevWarn, event.Iface("eth0", 1))
		e.ID = id
		e.TSWall = base.Add(time.Duration(i) * 50 * time.Millisecond).UnixNano()
		incidents += len(eng.Offer(e))
	}
	if incidents != 1 {
		t.Errorf("one flapping port produced %d incidents; a run of the same fault is one conclusion", incidents)
	}
}

func shippedRules(t *testing.T) []*Rule {
	t.Helper()
	rules, err := LoadRules("../../rules")
	if err != nil {
		t.Fatalf("load the shipped rules: %v", err)
	}
	return rules
}

func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}
