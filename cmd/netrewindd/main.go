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
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/collect"
	"github.com/OmarAlghafri/netrewind/internal/collect/flow"
	"github.com/OmarAlghafri/netrewind/internal/collect/netlink"
	"github.com/OmarAlghafri/netrewind/internal/collect/policy"
	"github.com/OmarAlghafri/netrewind/internal/collect/probe"
	"github.com/OmarAlghafri/netrewind/internal/collect/wire"
	"github.com/OmarAlghafri/netrewind/internal/correlate"
	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/identity"
	"github.com/OmarAlghafri/netrewind/internal/incident"
	"github.com/OmarAlghafri/netrewind/internal/metrics"
	"github.com/OmarAlghafri/netrewind/internal/store"
)

// version is stamped at build time with -ldflags "-X main.version=...".
var version = "dev"

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
		dbPath         = flag.String("db", store.DefaultPath(), "path to the event store")
		observerID     = flag.String("observer-id", defaultObserverID(), "identity of this recorder")
		logLevel       = flag.String("log-level", "info", "debug, info, warn or error")
		retention      = flag.Duration("retention", 7*24*time.Hour, "how much history to keep")
		gapAfter       = flag.Duration("gap-threshold", defaultGapThreshold, "absence longer than this is recorded as a gap in the record")
		rulesDir       = flag.String("rules", "rules", "directory of correlation rules; empty disables correlation")
		metricsAddr    = flag.String("metrics-addr", "", "serve Prometheus metrics on this address, e.g. 127.0.0.1:9464; empty disables it")
		wireIface      = flag.String("wire-iface", "", "capture DHCP, DNS and ICMP on this interface; empty means all")
		recordDNSNames = flag.Bool("record-dns-names", false, "store the names looked up. Off by default: there are networks where recording them is not permitted")
		probeTargets   = flag.String("probe", "", "addresses to measure reachability to; empty follows the default gateway")
	)
	flag.Parse()

	log := newLogger(*logLevel)

	cfg := config{
		dbPath:         *dbPath,
		observerID:     *observerID,
		retention:      *retention,
		gapAfter:       *gapAfter,
		rulesDir:       *rulesDir,
		metricsAddr:    *metricsAddr,
		wireIface:      *wireIface,
		recordDNSNames: *recordDNSNames,
		probeTargets:   *probeTargets,
	}
	if err := run(log, cfg); err != nil {
		log.Error("netrewindd stopped", "err", err)
		os.Exit(1)
	}
}

type config struct {
	dbPath         string
	observerID     string
	rulesDir       string
	metricsAddr    string
	wireIface      string
	recordDNSNames bool
	probeTargets   string
	retention      time.Duration
	gapAfter       time.Duration
}

func run(log *slog.Logger, cfg config) error {
	if err := os.MkdirAll(filepath.Dir(cfg.dbPath), 0o755); err != nil {
		return fmt.Errorf("create store directory: %w", err)
	}
	st, err := store.OpenSQLite(cfg.dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	dbPath, observerID, retention, gapAfter := cfg.dbPath, cfg.observerID, cfg.retention, cfg.gapAfter
	clock := event.NewClock()
	builder := event.NewBuilder(observerID, clock)

	// Correlation runs beside recording rather than after it, so an incident is
	// available while it is still happening. It is optional: a recorder with no
	// rules still records everything, and the rules can be replayed over stored
	// history later.
	var engine *correlate.Engine
	if cfg.rulesDir != "" {
		rules, err := correlate.LoadRules(cfg.rulesDir)
		if err != nil {
			log.Warn("correlation disabled", "err", err)
		} else {
			engine = correlate.NewEngine(rules, log)
			log.Info("correlation enabled", "rules", len(rules), "from", cfg.rulesDir)
		}
	}

	// Metrics exist whether or not anyone is scraping them, so the CLI and the
	// logs can report the same numbers as a dashboard would.
	meter := metrics.NewRecorder(version, observerID)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if cfg.metricsAddr != "" {
		srv, err := meter.Serve(cfg.metricsAddr)
		if err != nil {
			return err
		}
		go func() {
			log.Info("serving metrics", "addr", cfg.metricsAddr, "path", "/metrics")
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("metrics endpoint stopped", "err", err)
			}
		}()
		defer func() {
			shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			srv.Shutdown(shutdown)
		}()
	}

	log.Info("netrewind recorder starting", "db", dbPath, "observer", observerID, "retention", retention)

	// Before recording anything new, account for the time we were not running.
	// A timeline that cannot show its own blind spots is not evidence.
	startupEvents := checkGap(ctx, st, builder, log, gapAfter)

	queue := make(chan *event.Event, queueDepth)

	var wg sync.WaitGroup
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		writer(context.WithoutCancel(ctx), st, engine, meter, queue, log)
	}()

	for _, e := range startupEvents {
		queue <- e
	}

	// Identity resolution runs at ingest so an event records which machine we
	// believed it was about at the time. The temporal table behind the resolver
	// keeps the history, so a query can still follow a machine across an
	// address change even if that belief later needs revising.
	ids, err := identity.New(ctx, st)
	if err != nil {
		return err
	}

	collectors := []collect.Collector{
		netlink.NewLinkCollector(builder, log),
		netlink.NewNeighCollector(builder, log, ids),
		netlink.NewRouteCollector(builder, log),
		netlink.NewAddrCollector(builder, log),
		flow.NewCollector(builder, log),
		policy.NewCollector(builder, log),
		wire.NewCollector(builder, log, cfg.wireIface, cfg.recordDNSNames),
		probe.NewCollector(builder, log, cfg.probeTargets),
	}
	for _, c := range collectors {
		wg.Add(1)
		meter.SetCollector(c.Name(), true)
		go func(c collect.Collector) {
			defer wg.Done()
			// A collector that stops is a source the record no longer has, so
			// it is marked down whether it failed or was simply asked to stop.
			defer meter.SetCollector(c.Name(), false)
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
func writer(ctx context.Context, st store.Store, engine *correlate.Engine, meter *metrics.Recorder, queue <-chan *event.Event, log *slog.Logger) {
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	batch := make([]*event.Event, 0, flushSize)

	// Correlation runs over a batch only once that batch is durable, so an
	// incident can never point at evidence that was never written. The cost is
	// that an incident lags its last event by up to one flush interval, which
	// is a better trade than a conclusion whose evidence is missing.
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := st.Append(ctx, batch...); err != nil {
			log.Error("append failed", "events", len(batch), "err", err)
			batch = batch[:0]
			return
		}
		// Metrics are taken after the write, so a scrape never counts an event
		// the store does not hold.
		for _, e := range batch {
			meter.Observe(e)
		}
		if engine != nil {
			var incidents []*incident.Incident
			for _, e := range batch {
				incidents = append(incidents, engine.Offer(e)...)
			}
			if len(incidents) > 0 {
				if err := st.AppendIncidents(ctx, incidents...); err != nil {
					log.Error("could not store incidents", "err", err)
				}
				for _, inc := range incidents {
					meter.ObserveIncident(inc)
					log.Warn("incident", "title", inc.Title, "rule", inc.RuleID,
						"severity", inc.Severity, "confidence", inc.Confidence, "links", len(inc.Chain))
				}
			}
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
