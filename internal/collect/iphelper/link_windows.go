package iphelper

import (
	"context"
	"log/slog"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/analyze"
	"github.com/OmarAlghafri/netrewind/internal/collect"
	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/ports"
)

// statsInterval is how often interface counters are re-read for the error
// rate check; the kernel does not notify on a counter moving.
const statsInterval = 30 * time.Second

// LinkCollector records interfaces appearing, disappearing, and changing
// administrative or operational state, plus error-rate degradation from the
// interface counters. Decisions live in analyze.InterfaceAnalyzer; this type
// only reads Windows and translates.
type LinkCollector struct {
	b        *event.Builder
	log      *slog.Logger
	analyzer *analyze.InterfaceAnalyzer
	// byLUID maps an interface LUID (the key every notification carries) to
	// the index the analyzer knows it by, so an interface that has already
	// been deleted - and can no longer be looked up - can still be reported
	// as removed by index and name.
	byLUID map[uint64]ports.InterfaceObservation
}

// NewLinkCollector returns a collector that has not looked at anything yet.
func NewLinkCollector(b *event.Builder, log *slog.Logger) *LinkCollector {
	return &LinkCollector{
		b: b, log: log,
		analyzer: analyze.NewInterfaceAnalyzer(b).WithSource(event.SourceIPHelper),
		byLUID:   make(map[uint64]ports.InterfaceObservation),
	}
}

// Name implements collect.Collector.
func (c *LinkCollector) Name() string { return "iphelper.link" }

// Run seeds the current interface table, then reports every transition
// until ctx is done.
func (c *LinkCollector) Run(ctx context.Context, out chan<- *event.Event) error {
	if err := c.seed(); err != nil {
		return err
	}
	sub, err := subscribeInterfaces()
	if err != nil {
		return err
	}
	defer sub.close()
	c.log.Info("watching interface state", "collector", c.Name(), "seeded", c.analyzer.Known())

	stats := time.NewTicker(statsInterval)
	defer stats.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-stats.C:
			for _, e := range c.compareStats(now) {
				if !collect.Emit(ctx, out, e) {
					return nil
				}
			}
		case n := <-sub.ch:
			if !reportDrops(ctx, out, c.b, c.log, c.Name(), sub) {
				return nil
			}
			for _, e := range c.handle(n, time.Now()) {
				if !collect.Emit(ctx, out, e) {
					return nil
				}
			}
		}
	}
}

// seed records every observable interface as it is right now, without
// emitting anything, so the first notification is a change and not a
// discovery.
func (c *LinkCollector) seed() error {
	rows, err := listInterfaces()
	if err != nil {
		return err
	}
	now := time.Now()
	for i := range rows {
		row := &rows[i]
		if !observableRow(row) {
			continue
		}
		obs := interfaceObservation(row)
		c.analyzer.Seed(obs)
		c.analyzer.CompareCounters(obs.Index, obs.Name, interfaceCounters(row), now)
		c.byLUID[row.InterfaceLuid] = obs
	}
	return nil
}

// handle turns one notification into events. Windows sends one
// notification per IP family for the same interface; both re-read the same
// row and the analyzer, seeing no change the second time, emits nothing.
func (c *LinkCollector) handle(n notification, at time.Time) []*event.Event {
	row, present, err := interfaceRow(n.luid)
	if err != nil {
		c.log.Debug("interface lookup failed", "collector", c.Name(), "luid", n.luid, "err", err)
		return nil
	}
	known, seen := c.byLUID[n.luid]

	// A delete notification for one IP family while the adapter itself is
	// still present is not the link going away (the other family's row
	// remains); only the adapter no longer existing is.
	if !present {
		if !seen {
			return nil
		}
		delete(c.byLUID, n.luid)
		known.Removed = true
		return c.analyzer.Observe(known, at)
	}
	// The observability filter decides which interfaces to start tracking.
	// It must not apply to one already tracked: an adapter being disabled
	// passes through "not present, MTU 0" - the same shape as never-present
	// scaffolding - and dropping that reading would leave the analyzer
	// believing the link is still up, so neither the down nor the later up
	// would ever be recorded.
	if !seen && !observableRow(&row) {
		return nil
	}
	obs := interfaceObservation(&row)
	if seen && obs.MTU == 0 {
		obs.MTU = known.MTU // "not present" reports no MTU; that is not an MTU change
	}
	c.log.Debug("interface notification", "collector", c.Name(), "ifname", obs.Name, "ifindex", obs.Index,
		"kind", n.kind, "admin_up", obs.AdminUp, "oper_up", obs.OperUp, "oper_status", row.OperStatus,
		"media", row.MediaConnectState, "mtu", obs.MTU, "seen", seen)
	c.byLUID[n.luid] = obs
	return c.analyzer.Observe(obs, at)
}

// compareStats re-reads every known interface's counters and reports an
// error rate the analyzer judges worth recording.
func (c *LinkCollector) compareStats(now time.Time) []*event.Event {
	var events []*event.Event
	for luid, obs := range c.byLUID {
		row, present, err := interfaceRow(luid)
		if err != nil || !present {
			continue
		}
		events = append(events, c.analyzer.CompareCounters(obs.Index, obs.Name, interfaceCounters(&row), now)...)
	}
	return events
}

var _ collect.Collector = (*LinkCollector)(nil)
