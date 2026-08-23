// Command netrewindd is the NetRewind recorder.
//
// It runs continuously, watches the kernel for network state changes, and
// appends them to a local event store. It answers no questions itself - that is
// the netrewind CLI's job - and it deliberately does the smallest amount of
// thinking possible, because anything it decides at record time is a decision
// that cannot be revisited later when the incident is understood better.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/collect"
	"github.com/OmarAlghafri/netrewind/internal/collect/netlink"
	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/store"
)

const (
	// flushInterval bounds how long an event can sit in memory before it is
	// durable. A recorder that loses the last minute of an outage to its own
	// buffering has failed at the one thing it exists for.
	flushInterval = 250 * time.Millisecond
	// flushSize caps a single transaction.
	flushSize = 128
	// queueDepth absorbs a burst without blocking a collector.
	queueDepth = 4096
	// heartbeatInterval is how often the recorder records that it is alive.
	// It bounds how precisely a gap can be located after a crash.
	heartbeatInterval = 10 * time.Second
	// defaultGapThreshold is how long the recorder must have been absent before
	// the silence is worth reporting as a hole in the record. Below roughly one
	// heartbeat it would fire on every ordinary restart.
	defaultGapThreshold = 30 * time.Second
)

func main() {
	var (
		dbPath     = flag.String("db", store.DefaultPath(), "path to the event store")
		observerID = flag.String("observer-id", defaultObserverID(), "identity of this recorder")
		logLevel   = flag.String("log-level", "info", "debug, info, warn or error")
		retention  = flag.Duration("retention", 7*24*time.Hour, "how much history to keep")
		gapAfter   = flag.Duration("gap-threshold", defaultGapThreshold, "absence longer than this is recorded as a gap in the record")
	)
	flag.Parse()

	log := newLogger(*logLevel)

	if err := run(log, *dbPath, *observerID, *retention, *gapAfter); err != nil {
		log.Error("netrewindd stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger, dbPath, observerID string, retention, gapAfter time.Duration) error {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return fmt.Errorf("create store directory: %w", err)
	}
	st, err := store.OpenSQLite(dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	clock := event.NewClock()
	builder := event.NewBuilder(observerID, clock)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Info("netrewind recorder starting", "db", dbPath, "observer", observerID, "retention", retention)

	// Before recording anything new, account for the time we were not running.
	// A timeline that cannot show its own blind spots is not evidence.
	startupEvents := checkGap(ctx, st, builder, log, gapAfter)

	queue := make(chan *event.Event, queueDepth)

	var wg sync.WaitGroup
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		writer(context.WithoutCancel(ctx), st, queue, log)
	}()

	for _, e := range startupEvents {
		queue <- e
	}

	collectors := []collect.Collector{
		netlink.NewLinkCollector(builder, log),
	}
	for _, c := range collectors {
		wg.Add(1)
		go func(c collect.Collector) {
			defer wg.Done()
			if err := c.Run(ctx, queue); err != nil && !errors.Is(err, context.Canceled) {
				log.Error("collector failed", "collector", c.Name(), "err", err)
			}
		}(c)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		heartbeat(ctx, st, builder, queue, log, retention)
	}()

	<-ctx.Done()
	log.Info("shutting down")

	wg.Wait()

	// The stop marker is what tells a later gap check that the absence was
	// deliberate rather than a crash.
	queue <- builder.New(event.SourceInternal, event.KindSystemStop, event.SevInfo,
		event.Observer(observerID))
	close(queue)
	<-writerDone
	return nil
}

// writer drains the queue into the store in batches.
//
// It runs on a context that is deliberately not cancelled with the rest of the
// daemon: on shutdown the collectors stop first, then the writer flushes what
// they already produced. Dropping buffered events at exit would put an
// unexplained hole at the end of every recording.
func writer(ctx context.Context, st store.Store, queue <-chan *event.Event, log *slog.Logger) {
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	batch := make([]*event.Event, 0, flushSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := st.Append(ctx, batch...); err != nil {
			log.Error("append failed", "events", len(batch), "err", err)
		}
		batch = batch[:0]
	}

	for {
		select {
		case e, ok := <-queue:
			if !ok {
				flush()
				return
			}
			log.Debug("event", "kind", e.Kind, "subject", e.Subject.Label)
			batch = append(batch, e)
			if len(batch) >= flushSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// checkGap compares the last recorded heartbeat with now and, if the recorder
// was away long enough to matter, records exactly how long it was blind.
func checkGap(ctx context.Context, st store.Store, b *event.Builder, log *slog.Logger, threshold time.Duration) []*event.Event {
	events := []*event.Event{
		b.New(event.SourceInternal, event.KindSystemStart, event.SevInfo, event.Observer(b.ObserverID)),
	}

	raw, err := st.GetMeta(ctx, store.MetaLastHeartbeat)
	if err != nil {
		log.Warn("could not read last heartbeat", "err", err)
		return events
	}
	if raw == "" {
		return events // first ever start: there is no gap, only a beginning
	}
	last, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		log.Warn("last heartbeat unreadable", "value", raw, "err", err)
		return events
	}

	absent := time.Since(time.Unix(0, last))
	if absent < threshold {
		return events
	}
	log.Warn("recorder was absent", "duration", absent.Round(time.Second))
	events = append(events,
		b.New(event.SourceInternal, event.KindSystemGap, event.SevWarn, event.Observer(b.ObserverID)).
			WithAttr("gap_start_ns", last).
			WithAttr("gap_duration_ms", absent.Milliseconds()).
			WithEvidence("last_heartbeat", time.Unix(0, last).UTC().Format(time.RFC3339)))
	return events
}

// heartbeat records liveness and trims history on the same timer.
func heartbeat(ctx context.Context, st store.Store, b *event.Builder, queue chan<- *event.Event, log *slog.Logger, retention time.Duration) {
	beat := time.NewTicker(heartbeatInterval)
	defer beat.Stop()
	prune := time.NewTicker(time.Hour)
	defer prune.Stop()

	mark := func() {
		if err := st.SetMeta(ctx, store.MetaLastHeartbeat, strconv.FormatInt(time.Now().UnixNano(), 10)); err != nil {
			log.Warn("heartbeat failed", "err", err)
		}
	}
	mark()

	for {
		select {
		case <-ctx.Done():
			mark()
			return
		case <-beat.C:
			mark()
			// A wall-clock jump reorders the timeline for anyone reading it
			// later, so it is recorded as an event in its own right.
			if ns, stepped := b.Clock.TakeStep(); stepped {
				collect.Emit(ctx, queue,
					b.New(event.SourceInternal, event.KindSystemClockStep, event.SevWarn,
						event.Observer(b.ObserverID)).
						WithAttr("step_ms", ns/int64(time.Millisecond)))
			}
		case <-prune.C:
			n, err := st.Prune(ctx, time.Now().Add(-retention))
			if err != nil {
				log.Warn("prune failed", "err", err)
				continue
			}
			if n > 0 {
				log.Info("pruned expired history", "events", n, "retention", retention)
			}
		}
	}
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		l = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: l}))
}

func defaultObserverID() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "netrewind"
}
