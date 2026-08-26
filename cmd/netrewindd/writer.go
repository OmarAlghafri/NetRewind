package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/correlate"
	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/incident"
	"github.com/OmarAlghafri/netrewind/internal/metrics"
	"github.com/OmarAlghafri/netrewind/internal/otel"
	"github.com/OmarAlghafri/netrewind/internal/store"
)

// storeLoss remembers what the store refused, so it can be recorded once the
// store accepts writes again.
//
// A full disk is the ordinary way a recorder that has been running for months
// dies, and the failure is silent by nature: the events are gone, and the
// record cannot be told about it because the record is the thing that is
// broken. Logging and moving on would leave an unbroken timeline with an
// arbitrary amount missing from the middle - which is precisely the situation
// this project exists to make impossible.
//
// So the count is kept in memory, and the first write that succeeds carries the
// admission with it.
type storeLoss struct {
	events uint64
	since  time.Time
	reason string
}

// note records a batch the store would not take.
func (l *storeLoss) note(n int, err error, at time.Time) {
	if l.events == 0 {
		l.since = at
	}
	l.events += uint64(n)
	if err != nil {
		l.reason = err.Error()
	}
}

func (l *storeLoss) any() bool { return l.events > 0 }

// admission returns the event describing the loss so far.
//
// It is prepended to the batch that proves the store is writable again, so it
// lands in the same transaction as the recovery rather than in a later one that
// might not happen. It deliberately does not forget the loss: the write it is
// part of may fail too, and a counter reset before the account is durable is
// how the account goes missing. Only a successful write clears it.
func (l *storeLoss) admission(b *event.Builder, at time.Time) *event.Event {
	if l.events == 0 {
		return nil
	}
	e := b.New(event.SourceInternal, event.KindSystemDrop, event.SevError,
		event.Observer(b.ObserverID)).
		WithAttr("dropped", int64(l.events)).
		WithAttr("source", "store").
		WithAttr("blind_from", l.since.UTC().Format(time.RFC3339)).
		WithAttr("blind_ms", at.Sub(l.since).Milliseconds()).
		WithDedup("system.drop|store").
		WithEvidence("reason",
			"the event store would not accept writes, so these events were lost before "+
				"they were ever durable: "+l.reason)
	return e
}

// writer drains the queue into the store in batches.
//
// It runs on a context that is deliberately not cancelled with the rest of the
// daemon: on shutdown the collectors stop first, then the writer flushes what
// they already produced. Dropping buffered events at exit would put an
// unexplained hole at the end of every recording.
func writer(ctx context.Context, st store.Store, engine *correlate.Engine, meter *metrics.Recorder, b *event.Builder, ship *otel.Shipper, queue <-chan *event.Event, log *slog.Logger) {
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	batch := make([]*event.Event, 0, flushSize)
	var loss storeLoss

	// Correlation runs over a batch only once that batch is durable, so an
	// incident can never point at evidence that was never written. The cost is
	// that an incident lags its last event by up to one flush interval, which
	// is a better trade than a conclusion whose evidence is missing.
	flush := func() {
		if len(batch) == 0 && !loss.any() {
			return
		}
		now := time.Now()

		// Count only the real events against a failure: the admission is not
		// one of them, and counting it would inflate the number it reports.
		lost := len(batch)
		if e := loss.admission(b, now); e != nil {
			batch = append([]*event.Event{e}, batch...)
		}

		if err := st.Append(ctx, batch...); err != nil {
			// The admission, if there was one, is folded back in rather than
			// lost with the batch - the store is still broken and still owes an
			// account of it.
			loss.note(lost, err, now)
			log.Error("the store would not accept writes; these events are lost",
				"events", lost, "err", err)
			meter.SetStoreWritable(false)
			batch = batch[:0]
			return
		}
		// Durable at last: the account is safe to forget only now.
		loss = storeLoss{}
		meter.SetStoreWritable(true)

		// Metrics are taken after the write, so a scrape never counts an event
		// the store does not hold.
		for _, e := range batch {
			meter.Observe(e)
		}
		var incidents []*incident.Incident
		if engine != nil {
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

		// Only after everything is durable. A copy in somebody else's pipeline
		// of an event the store does not hold would be a record that disagrees
		// with itself, and the store is the one that has to be right.
		if ship != nil {
			ship.Ship(batch, incidents)
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
