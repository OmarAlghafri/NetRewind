package otel

import (
	"context"
	"log/slog"
	"sync"

	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/incident"
)

// queueDepth is how many batches may be waiting to ship.
//
// Small on purpose. The store already has everything, so a deep queue would
// only buy the ability to hold more stale copies in memory while a collector is
// down - and the memory belongs to a recorder that has to survive the
// conditions it is recording.
const queueDepth = 32

// Shipper sends batches to a collector without ever making the writer wait.
//
// Exporting is a courtesy to somebody else's pipeline. Recording is the job.
// So the writer hands a batch over and moves on, and if the collector is slow
// or gone, batches are dropped here rather than allowed to push back onto the
// thing that is writing the actual record.
type Shipper struct {
	ex    *Exporter
	log   *slog.Logger
	queue chan batch
	done  chan struct{}
	once  sync.Once
}

type batch struct {
	events    []*event.Event
	incidents []*incident.Incident
}

// NewShipper starts the goroutine that drains the queue. Stop it with Close.
func NewShipper(ex *Exporter, log *slog.Logger) *Shipper {
	s := &Shipper{
		ex:    ex,
		log:   log,
		queue: make(chan batch, queueDepth),
		done:  make(chan struct{}),
	}
	go s.run()
	return s
}

func (s *Shipper) run() {
	defer close(s.done)
	for b := range s.queue {
		// Not the daemon's context: a batch already accepted should get its
		// chance to ship even as the recorder is stopping. The exporter's own
		// timeout bounds how long that can take.
		if err := s.ex.Export(context.Background(), b.events, b.incidents); err != nil {
			s.log.Debug("otlp export failed", "err", err)
		}
	}
}

// Ship queues a batch. It never blocks.
//
// A full queue means the collector cannot keep up with the recorder. The batch
// is dropped and counted, because the alternative is the exporter deciding how
// fast the recorder may write, which is the wrong way round.
func (s *Shipper) Ship(events []*event.Event, incidents []*incident.Incident) {
	if len(events) == 0 && len(incidents) == 0 {
		return
	}
	// The writer reuses its batch slice, so what is queued has to be a copy.
	// Without this the shipper would encode whatever the writer had moved on
	// to, which is the sort of bug that produces a plausible-looking export of
	// the wrong events.
	b := batch{
		events:    append([]*event.Event(nil), events...),
		incidents: append([]*incident.Incident(nil), incidents...),
	}
	select {
	case s.queue <- b:
	default:
		s.ex.note(len(b.events) + len(b.incidents))
	}
}

// Dropped is how many records never reached the collector, whether because the
// queue was full or the collector refused them.
func (s *Shipper) Dropped() uint64 { return s.ex.Dropped() }

// Close drains what is queued and stops the goroutine.
func (s *Shipper) Close() {
	s.once.Do(func() {
		close(s.queue)
		<-s.done
	})
}
