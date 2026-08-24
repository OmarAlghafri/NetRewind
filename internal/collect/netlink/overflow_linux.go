//go:build linux

package netlink

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"

	"github.com/OmarAlghafri/netrewind/internal/collect"
	"github.com/OmarAlghafri/netrewind/internal/event"
	"golang.org/x/sys/unix"
)

// overflowReporter turns a netlink socket overrun into a recorded event.
//
// This is the failure the whole project is built against, arriving through the
// back door. When a burst outruns the socket buffer the kernel returns ENOBUFS
// and *drops the messages* - so a storm of ARP changes, the exact moment worth
// recording, is when the recorder is most likely to miss some. Logging that and
// carrying on would leave a timeline that looks calm because the recorder went
// deaf, which is indistinguishable from a network that was fine.
//
// One reporter per collector, shared with its subscription's error callback.
type overflowReporter struct {
	b    *event.Builder
	log  *slog.Logger
	name string

	// events is the collector's own output channel. Writing to it from the
	// callback is safe: collect.Emit gives up if the context is done, so a
	// callback firing during shutdown cannot wedge the netlink goroutine.
	events chan<- *event.Event
	ctx    context.Context

	dropped atomic.Uint64
}

func newOverflowReporter(b *event.Builder, log *slog.Logger, name string) *overflowReporter {
	return &overflowReporter{b: b, log: log, name: name}
}

// bind attaches the reporter to a running collector's context and channel. It
// is called once, before the subscription is created.
func (r *overflowReporter) bind(ctx context.Context, out chan<- *event.Event) {
	r.ctx = ctx
	r.events = out
}

// callback is what netlink subscriptions are given.
func (r *overflowReporter) callback(err error) {
	if !isOverflow(err) {
		// Closing the socket on shutdown makes the pending read fail, and
		// every collector would report that as a problem on every clean stop.
		// Four warnings that always appear are four warnings nobody reads, and
		// they would sit alongside the ones that matter.
		if r.stopping(err) {
			return
		}
		// Any other error is a fault in the socket rather than a hole in the
		// record. Logged, and left to the read loop to fail on if it is fatal.
		r.log.Warn("netlink subscription error", "collector", r.name, "err", err)
		return
	}

	n := r.dropped.Add(1)
	r.log.Warn("netlink buffer overran; messages were lost",
		"collector", r.name, "overruns", n)

	if r.events == nil || r.ctx == nil {
		return
	}
	e := r.b.New(event.SourceInternal, event.KindSystemDrop, event.SevWarn,
		event.Observer(r.b.ObserverID)).
		WithAttr("dropped", 1).
		WithAttr("total_dropped", n).
		WithAttr("source", r.name).
		WithDedup("system.drop|"+r.name).
		WithEvidence("reason",
			"the netlink socket buffer overran: changes arrived faster than they could be read, "+
				"and the kernel discarded some without saying which")
	collect.Emit(r.ctx, r.events, e)
}

// isOverflow reports whether an error means the kernel threw messages away.
//
// The string comparison is not paranoia. vishvananda/netlink reports receive
// failures for the link, address and route subscriptions as
// fmt.Errorf("Receive failed: %v", err) - %v, not %w - which discards the
// wrapped errno and makes errors.Is blind. Only the neighbour subscription
// passes the error through intact. Relying on errors.Is alone would mean this
// detected overruns on one collector out of four and silently missed the rest,
// which is the failure it exists to prevent.
//
// The comparison is against the errno's own text rather than the library's
// message, so it does not depend on a wording this project does not control.
func isOverflow(err error) bool {
	return matchesErrno(err, unix.ENOBUFS, unix.ENOMEM)
}

// matchesErrno reports whether err is one of these errnos, whether it was
// wrapped properly or flattened into a string.
func matchesErrno(err error, errnos ...unix.Errno) bool {
	if err == nil {
		return false
	}
	for _, e := range errnos {
		if errors.Is(err, e) {
			return true
		}
	}
	msg := err.Error()
	for _, e := range errnos {
		if strings.Contains(msg, e.Error()) {
			return true
		}
	}
	return false
}

// stopping reports whether an error is only the consequence of shutting down.
//
// The netlink library closes the socket when the done channel is closed, and
// the read already blocked on it fails with EAGAIN or a closed-file error. Both
// are expected, and only when the context has actually been cancelled: the same
// errors arriving while the recorder is meant to be running are real.
func (r *overflowReporter) stopping(err error) bool {
	if r.ctx == nil || r.ctx.Err() == nil {
		return false
	}
	return matchesErrno(err, unix.EAGAIN, unix.EBADF) ||
		errors.Is(err, os.ErrClosed) ||
		errors.Is(err, context.Canceled)
}
