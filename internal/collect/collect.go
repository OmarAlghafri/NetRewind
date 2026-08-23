// Package collect holds the sources NetRewind draws observations from.
//
// Every source is a Collector: it watches one part of the kernel or the wire,
// and turns what it sees into events on a shared channel. Collectors never
// touch the store and never talk to each other - correlation happens later, on
// the timeline, where it can be explained.
package collect

import (
	"context"

	"github.com/OmarAlghafri/netrewind/internal/event"
)

// Collector watches one source and emits events until its context is done.
//
// Run must return promptly when ctx is cancelled, and must not close out: the
// daemon owns that channel and several collectors share it.
type Collector interface {
	// Name identifies the collector in logs and in system.* events.
	Name() string
	// Run watches until ctx is done. Returning a non-nil error means the
	// source failed, which is itself a gap in the record.
	Run(ctx context.Context, out chan<- *event.Event) error
}

// Emit sends an event unless the context is already done. Collectors use it so
// that a full channel during shutdown cannot wedge them.
func Emit(ctx context.Context, out chan<- *event.Event, e *event.Event) bool {
	select {
	case out <- e:
		return true
	case <-ctx.Done():
		return false
	}
}
