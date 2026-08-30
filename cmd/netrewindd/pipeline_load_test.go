package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/correlate"
	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/incident"
	"github.com/OmarAlghafri/netrewind/internal/store"
)

// What one goroutine has to get through, end to end.
//
// The store's own load tests measure the store. This measures what the writer
// actually does with a batch: make it durable, then offer every event to
// correlation, on the one goroutine that does both. That is the number that
// decides whether the queue drains, and correlation - not the store - is the
// slower half of it, so measuring the store alone answers the wrong question.
//
// Three shapes, because they cost very different amounts:
//
//   - a quiet segment, where nothing repeats
//   - a broadcast storm, where every event is about a different machine
//   - a flapping port, where every event is the same fact about the same
//     interface, which is the shape the rules are built to recognise and the
//     shape that used to bring the recorder to a halt
func TestThePipelineKeepsUpWithABurst(t *testing.T) {
	requireLoadTest(t)

	rules, err := correlate.LoadRules(filepath.Join("..", "..", "rules"))
	if err != nil {
		t.Fatalf("load rules: %v", err)
	}

	shapes := []struct {
		name string
		// The lowest rate this shape must sustain, in events a second. Set an
		// order of magnitude under what a developer machine measures, so this
		// fails on a regression rather than on a busy CI runner.
		floor int
		make  func(b *event.Builder, i int, base time.Time) *event.Event
	}{
		{
			name:  "a quiet segment",
			floor: 2000,
			make: func(b *event.Builder, i int, base time.Time) *event.Event {
				e := b.New(event.SourceNetlink, event.KindARPBindingNew, event.SevInfo,
					event.Host(fmt.Sprintf("10.0.%d.%d", i/256%256, i%256), ""))
				e.TSWall = base.Add(time.Duration(i) * 10 * time.Millisecond).UnixNano()
				return e
			},
		},
		{
			name:  "a broadcast storm",
			floor: 2000,
			make: func(b *event.Builder, i int, base time.Time) *event.Event {
				ip := fmt.Sprintf("10.0.%d.%d", i/256%256, i%256)
				e := b.New(event.SourceNetlink, event.KindARPBindingChanged, event.SevWarn,
					event.Host(ip, ""))
				e.TSWall = base.Add(time.Duration(i) * time.Millisecond).UnixNano()
				e.WithAttr("mac_new", fmt.Sprintf("02:00:00:00:%02x:%02x", i/256%256, i%256)).
					WithDedup("l2.arp_binding_changed|" + ip)
				return e
			},
		},
		{
			name:  "one port flapping",
			floor: 1000,
			make: func(b *event.Builder, i int, base time.Time) *event.Event {
				kind := event.KindLinkDown
				if i%2 == 1 {
					kind = event.KindLinkUp
				}
				e := b.New(event.SourceNetlink, kind, event.SevWarn, event.Iface("eth0", 1))
				e.TSWall = base.Add(time.Duration(i) * 5 * time.Millisecond).UnixNano()
				e.WithDedup(string(kind) + "|eth0")
				return e
			},
		},
	}

	const total = 20000
	for _, shape := range shapes {
		t.Run(shape.name, func(t *testing.T) {
			st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "events.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()

			engine := correlate.NewEngine(rules, slog.New(
				slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})))
			b := event.NewBuilder("obs", nil)
			base := time.Now().Add(-time.Hour)
			ctx := context.Background()

			// Built before the clock starts, and the first batch is thrown
			// away: what is wanted is the steady-state rate of a recorder that
			// has been running for hours, not the cost of a cold binary.
			events := make([]*event.Event, total)
			for i := range events {
				events[i] = shape.make(b, i, base)
			}
			if err := st.Append(ctx, events[:flushSize]...); err != nil {
				t.Fatal(err)
			}
			for _, e := range events[:flushSize] {
				engine.Offer(e)
			}

			incidents := 0
			start := time.Now()
			for i := flushSize; i < total; i += flushSize {
				end := i + flushSize
				if end > total {
					end = total
				}
				batch := events[i:end]
				if err := st.Append(ctx, batch...); err != nil {
					t.Fatalf("append: %v", err)
				}
				var found []*incident.Incident
				for _, e := range batch {
					found = append(found, engine.Offer(e)...)
				}
				if len(found) > 0 {
					if err := st.AppendIncidents(ctx, found...); err != nil {
						t.Fatalf("append incidents: %v", err)
					}
					incidents += len(found)
				}
			}
			took := time.Since(start)
			rate := float64(total-flushSize) / took.Seconds()

			held, err := st.CountEvents(ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%6.0f events/s  (%d events through store and correlation in %s; "+
				"%d rows after folding, %d incidents, %d dropped from the correlation window)",
				rate, total-flushSize, took.Round(time.Millisecond), held, incidents, engine.Truncated())

			if rate < float64(shape.floor) {
				t.Errorf("%0.f events/s is below the %d/s this shape has to sustain; "+
					"the writer is a single goroutine, so below this the queue does not drain "+
					"and the record starts losing exactly what it was deployed to catch",
					rate, shape.floor)
			}
		})
	}
}

func requireLoadTest(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("load test")
	}
	if os.Getenv("NETREWIND_LOAD_TEST") == "" {
		t.Skip("set NETREWIND_LOAD_TEST=1, or run `make load`, to measure throughput")
	}
}
