//go:build linux

package netlink

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"

	"github.com/OmarAlghafri/netrewind/internal/event"
	"golang.org/x/sys/unix"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// The library flattens the errno into a string for three of the four
// subscriptions. If this stops being true the plain errors.Is path still
// works, but until then an overrun on link, addr or route reaches the reporter
// looking like nothing at all.
func TestOverrunIsRecognisedThroughTheLibrarysWrapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"the errno itself", unix.ENOBUFS, true},
		{"properly wrapped", fmt.Errorf("subscribe: %w", unix.ENOBUFS), true},
		{
			// Exactly what link_linux.go, addr_linux.go and route_linux.go
			// hand to the error callback.
			name: "flattened the way the library flattens it",
			err:  fmt.Errorf("Receive failed: %v", unix.ENOBUFS),
			want: true,
		},
		{"out of memory counts too", fmt.Errorf("Receive failed: %v", unix.ENOMEM), true},
		{"an unrelated failure does not", fmt.Errorf("Receive failed: %v", unix.EINVAL), false},
		{"nothing is not an overrun", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isOverflow(tc.err); got != tc.want {
				t.Errorf("isOverflow(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestAnOverrunBecomesARecordedEvent(t *testing.T) {
	out := make(chan *event.Event, 4)
	r := newOverflowReporter(event.NewBuilder("obs-1", nil), quietLogger(), "netlink.link")
	r.bind(context.Background(), out)

	r.callback(fmt.Errorf("Receive failed: %v", unix.ENOBUFS))

	select {
	case e := <-out:
		if e.Kind != event.KindSystemDrop {
			t.Errorf("kind = %s, want %s", e.Kind, event.KindSystemDrop)
		}
		if got := e.Attrs["source"]; got != "netlink.link" {
			t.Errorf("source = %v, want the collector that lost the messages", got)
		}
		if e.DedupKey == "" {
			t.Error("no dedup key: a sustained overrun would flood the store")
		}
	default:
		t.Fatal("the kernel discarded messages and nothing was recorded")
	}
}

func TestAnUnrelatedErrorIsNotRecordedAsALostMessage(t *testing.T) {
	out := make(chan *event.Event, 4)
	r := newOverflowReporter(event.NewBuilder("obs-1", nil), quietLogger(), "netlink.link")
	r.bind(context.Background(), out)

	r.callback(fmt.Errorf("Wrong sender portid 42, expected 0"))

	select {
	case e := <-out:
		t.Fatalf("recorded %s for an error that lost nothing", e.Kind)
	default:
	}
}

// Shutting down closes the socket underneath a blocked read. Every collector
// would otherwise report that as a fault on every clean stop.
func TestShutdownErrorsAreNotReported(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan *event.Event, 4)
	r := newOverflowReporter(event.NewBuilder("obs-1", nil), quietLogger(), "netlink.link")
	r.bind(ctx, out)
	cancel()

	// The exact error a stopped subscription produces.
	if !r.stopping(fmt.Errorf("Receive failed: %v", unix.EAGAIN)) {
		t.Error("a read interrupted by shutdown is treated as a fault")
	}
	if !r.stopping(fmt.Errorf("Receive failed: %v", unix.EBADF)) {
		t.Error("a closed socket during shutdown is treated as a fault")
	}
}

// The same error while the recorder is meant to be running is real, and
// suppressing it would hide a socket that has stopped working.
func TestTheSameErrorWhileRunningIsNotSuppressed(t *testing.T) {
	out := make(chan *event.Event, 4)
	r := newOverflowReporter(event.NewBuilder("obs-1", nil), quietLogger(), "netlink.link")
	r.bind(context.Background(), out)

	if r.stopping(fmt.Errorf("Receive failed: %v", unix.EAGAIN)) {
		t.Error("an error during normal operation was written off as shutdown")
	}
}

// An overrun during shutdown is still a hole in the record and must survive
// the shutdown suppression.
func TestAnOverrunDuringShutdownIsStillCounted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make(chan *event.Event, 4)
	r := newOverflowReporter(event.NewBuilder("obs-1", nil), quietLogger(), "netlink.link")
	r.bind(ctx, out)

	r.callback(fmt.Errorf("Receive failed: %v", unix.ENOBUFS))
	if n := r.dropped.Load(); n != 1 {
		t.Errorf("overruns counted = %d, want 1", n)
	}
}
