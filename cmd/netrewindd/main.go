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
	"github.com/OmarAlghafri/netrewind/internal/metrics"
	"github.com/OmarAlghafri/netrewind/internal/otel"
	"github.com/OmarAlghafri/netrewind/internal/registry"
	"github.com/OmarAlghafri/netrewind/internal/store"
	"github.com/OmarAlghafri/netrewind/internal/update"
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
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	cfg, err := loadConfig(fs, os.Args[1:])
	if err != nil {
		// Configuration errors go to stderr rather than through the logger:
		// the log level is one of the things that might be wrong.
		fmt.Fprintf(os.Stderr, "netrewindd: %v\n", err)
		os.Exit(2)
	}

	if cfg.ShowVersion {
		fmt.Printf("netrewindd %s (schema v%d)\n", version, event.SchemaVersion)
		return
	}

	if cfg.CheckOnly {
		fmt.Printf("configuration is usable: store %s, %s of history, observer %s\n",
			cfg.DBPath, cfg.Retention, cfg.ObserverID)
		return
	}

	log := newLogger(cfg.LogLevel)
	if err := run(log, cfg); err != nil {
		log.Error("netrewindd stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger, cfg config) error {
	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		return fmt.Errorf("create store directory: %w", err)
	}
	st, err := store.OpenSQLite(cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()

	dbPath, observerID := cfg.DBPath, cfg.ObserverID
	retention, gapAfter := time.Duration(cfg.Retention), time.Duration(cfg.GapAfter)
	clock := event.NewClock()
	builder := event.NewBuilder(observerID, clock)

	// Correlation runs beside recording rather than after it, so an incident is
	// available while it is still happening. It is optional: a recorder with no
	// rules still records everything, and the rules can be replayed over stored
	// history later.
	var engine *correlate.Engine
	if cfg.RulesDir != "" {
		rules, err := correlate.LoadRules(cfg.RulesDir)
		if err != nil {
			log.Warn("correlation disabled", "err", err)
		} else {
			engine = correlate.NewEngine(rules, log)
			log.Info("correlation enabled", "rules", len(rules), "from", cfg.RulesDir)
		}
	}

	// Metrics exist whether or not anyone is scraping them, so the CLI and the
	// logs can report the same numbers as a dashboard would.
	meter := metrics.NewRecorder(version, observerID)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if cfg.MetricsAddr != "" {
		srv, err := meter.Serve(cfg.MetricsAddr, log)
		if err != nil {
			return err
		}
		go func() {
			log.Info("serving metrics", "addr", cfg.MetricsAddr, "path", "/metrics")
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

	// Copying the record to somebody else's pipeline is off unless asked for.
	// An appliance should not open a connection nobody requested.
	var shipper *otel.Shipper
	if cfg.OTLPEndpoint != "" {
		exporter := otel.New(cfg.OTLPEndpoint, observerID, version, cfg.OTLPHeaders, log)
		shipper = otel.NewShipper(exporter, log)
		defer shipper.Close()
		log.Info("copying the record to an OpenTelemetry collector", "endpoint", exporter.Endpoint())
	}

	queue := make(chan *event.Event, queueDepth)

	var wg sync.WaitGroup
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		writer(context.WithoutCancel(ctx), st, engine, meter, builder, shipper, queue, log)
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
		wire.NewCollector(builder, log, cfg.WireIface, cfg.RecordDNSNames),
		probe.NewCollector(builder, log, cfg.ProbeTargets),
		update.New(update.Config{
			Check:     cfg.Update.Check,
			Apply:     cfg.Update.Apply,
			Interval:  time.Duration(cfg.Update.Every),
			Repo:      cfg.Update.Repo,
			Token:     cfg.Update.Token,
			PublicKey: cfg.Update.PublicKey,
			RulesDir:  cfg.RulesDir,
		}, version, builder, log, stop),
	}

	// The registry is a capability report waiting to be read (by the CLI, and
	// later by a local API), not something the collectors themselves consult -
	// it is fed the same up/down facts metrics already gets, alongside it
	// rather than instead of it, so nothing already relying on
	// netrewind_collector_up changes shape.
	reg := registry.New(nil)
	for _, d := range collectorDescriptors {
		reg.Register(d)
	}

	for _, c := range collectors {
		wg.Add(1)
		meter.SetCollector(c.Name(), true)
		reg.Up(c.Name())
		go func(c collect.Collector) {
			defer wg.Done()
			// A collector that stops is a source the record no longer has, so
			// it is marked down whether it failed or was simply asked to stop.
			defer meter.SetCollector(c.Name(), false)
			if err := c.Run(ctx, queue); err != nil && !errors.Is(err, context.Canceled) {
				log.Error("collector failed", "collector", c.Name(), "err", err)
				reg.Down(c.Name(), err.Error())
				// The log is not the record. A source that never started, or
				// that died, leaves a whole family of events missing from the
				// timeline, and someone reading it later would see the absence
				// and conclude nothing of that kind happened. The record has to
				// say it was not looking.
				collect.Emit(ctx, queue, builder.New(
					event.SourceInternal, event.KindCollectorDown, event.SevError,
					event.Observer(observerID)).
					WithAttr("collector", c.Name()).
					WithAttr("reason", err.Error()).
					WithDedup("system.collector_down|"+c.Name()))
				return
			}
			reg.Down(c.Name(), "stopped")
		}(c)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		heartbeat(ctx, st, builder, meter, shipper, queue, log, retention)
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
func heartbeat(ctx context.Context, st store.Store, b *event.Builder, meter *metrics.Recorder, ship *otel.Shipper, queue chan<- *event.Event, log *slog.Logger, retention time.Duration) {
	beat := time.NewTicker(heartbeatInterval)
	defer beat.Stop()
	prune := time.NewTicker(time.Hour)
	defer prune.Stop()

	mark := func() {
		err := st.SetMeta(ctx, store.MetaLastHeartbeat, strconv.FormatInt(time.Now().UnixNano(), 10))
		// A heartbeat that fails because the recorder is stopping is the
		// shutdown working, not a fault. Reporting it would put a warning on
		// every clean stop and teach whoever reads the log to ignore them.
		if err != nil && ctx.Err() == nil {
			log.Warn("heartbeat failed", "err", err)
		}
	}
	// The gauges an operator watches are refreshed on the same beat. Declaring
	// netrewind_stored_events without ever setting it left it with no sample at
	// all, so the series a scraper would alert on did not exist.
	gauges := func() {
		if n, err := st.CountEvents(ctx); err == nil {
			meter.SetStoredEvents(n)
		} else if ctx.Err() == nil {
			log.Debug("could not count stored events", "err", err)
		}
		if ship != nil {
			meter.SetOTLPDropped(ship.Dropped())
		}
	}

	mark()
	gauges()

	for {
		select {
		case <-ctx.Done():
			mark()
			return
		case <-beat.C:
			mark()
			gauges()
			// A wall-clock jump reorders the timeline for anyone reading it
			// later, so it is recorded as an event in its own right.
			if ns, stepped := b.Clock.TakeStep(); stepped {
				collect.Emit(ctx, queue,
					b.New(event.SourceInternal, event.KindSystemClockStep, event.SevWarn,
						event.Observer(b.ObserverID)).
						WithAttr("step_ms", ns/int64(time.Millisecond)))
			}
		case <-prune.C:
			trim(ctx, st, log, retention)
		}
	}
}

// incidentRetentionFactor is how much longer a conclusion is kept than the
// evidence under it.
//
// Incidents are small, they are the answer rather than the raw material, and
// they are what somebody comes back to months later - so a week of events is a
// month of incidents. Every link in a chain carries its own description
// precisely so that an incident still reads once the events it cites have gone.
//
// Derived from the configured retention rather than being a setting of its own:
// an operator who has already said how much history they want should not have
// to answer a second, subtler question about how much of the conclusion drawn
// from it to keep.
const incidentRetentionFactor = 4

// trim removes what has aged out of all three kinds of history the store holds.
//
// All three, because for a long time it was one. Retention was documented as
// how much history to keep and it bounded the events table alone: the
// conclusions and the identity bindings grew without limit, so a recorder on a
// segment with any churn filled its disk however retention was set. On an
// appliance that is a recorder which one day stops recording, having been
// configured exactly as the documentation said.
func trim(ctx context.Context, st store.Store, log *slog.Logger, retention time.Duration) {
	now := time.Now()
	events, err := st.Prune(ctx, now.Add(-retention))
	if err != nil {
		log.Warn("prune failed", "what", "events", "err", err)
		return
	}
	// Bindings that ended before the oldest event we still hold can no longer
	// be needed: they exist to say which machine held an address at a moment,
	// and there are no moments left to ask about.
	bindings, err := st.PruneIdentity(ctx, now.Add(-retention).UnixNano())
	if err != nil {
		log.Warn("prune failed", "what", "identity bindings", "err", err)
		return
	}
	incidents, err := st.PruneIncidents(ctx, now.Add(-retention*incidentRetentionFactor))
	if err != nil {
		log.Warn("prune failed", "what", "incidents", "err", err)
		return
	}
	if events > 0 || bindings > 0 || incidents > 0 {
		log.Info("pruned expired history",
			"events", events, "identity_bindings", bindings, "incidents", incidents,
			"retention", retention, "incident_retention", retention*incidentRetentionFactor)
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
