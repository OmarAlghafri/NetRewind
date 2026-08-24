//go:build linux

// Package flow observes TCP connections from inside the kernel.
//
// It exists to answer the question netlink cannot: not "is the link up" but
// "can these two machines still talk". A filtering change, a service that died,
// and a route that stopped working are indistinguishable at layer 3 and obvious
// here.
//
// This file is only the plumbing - load, attach, read, parse. Every judgement
// about what a transition means lives in the platform-neutral Tracker, so it
// can be tested without a kernel.
package flow

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/collect"
	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/ringbuf"
	"github.com/cilium/ebpf/rlimit"
)

// flowProgram is compiled by clang, not by the Go toolchain, and committed so
// this package still builds where no BPF toolchain exists. Rebuild with
// `make bpf` after editing the C.
//
//go:embed bpf/flow.bpf.o
var flowProgram []byte

// eventSize must match struct flow_event in the C, and evState/evReset must
// match the EV_ constants there. A mismatch here reads the wrong bytes and
// reports confident nonsense, so there is a test that checks the size against
// the compiled object.
const (
	eventSize = 28
	evState   = 0
	evReset   = 1
)

// Collector attaches the eBPF program and feeds what it reports to a Tracker.
type Collector struct {
	log         *slog.Logger
	tracker     *Tracker
	observerID  string
	lastDropped uint64
}

// NewCollector returns a collector for TCP connection activity.
func NewCollector(b *event.Builder, log *slog.Logger) *Collector {
	return &Collector{log: log, tracker: NewTracker(b, nil), observerID: b.ObserverID}
}

// Name implements collect.Collector.
func (c *Collector) Name() string { return "ebpf.flow" }

// Run loads the program, attaches it and reports until ctx is done.
func (c *Collector) Run(ctx context.Context, out chan<- *event.Event) error {
	// eBPF maps are accounted against locked memory on older kernels.
	if err := rlimit.RemoveMemlock(); err != nil {
		return fmt.Errorf("flow: raise memlock limit: %w", err)
	}

	spec, err := ebpf.LoadCollectionSpecFromReader(bytes.NewReader(flowProgram))
	if err != nil {
		return fmt.Errorf("flow: load program: %w", err)
	}
	coll, err := ebpf.NewCollection(spec)
	if err != nil {
		// The verifier's complaint is the only useful diagnostic when a
		// program will not load, so it is passed through rather than wrapped
		// into something shorter.
		var ve *ebpf.VerifierError
		if errors.As(err, &ve) {
			return fmt.Errorf("flow: verifier rejected the program: %+v", ve)
		}
		return fmt.Errorf("flow: create collection: %w", err)
	}
	defer coll.Close()

	// Two tracepoints. The state one carries the connection's life; the reset
	// one is the only thing that distinguishes a peer refusing from a path
	// disappearing, since both end ESTABLISHED -> CLOSE.
	attachments := []struct{ group, name, prog string }{
		{"sock", "inet_sock_set_state", "trace_inet_sock_set_state"},
		{"tcp", "tcp_receive_reset", "trace_tcp_receive_reset"},
	}
	for _, a := range attachments {
		prog, ok := coll.Programs[a.prog]
		if !ok {
			return fmt.Errorf("flow: compiled object has no %s program", a.prog)
		}
		tp, err := link.Tracepoint(a.group, a.name, prog, nil)
		if err != nil {
			// Entering a network namespace gets a fresh mount namespace with
			// /sys remounted, which hides tracefs. Say so, because the bare
			// error does not suggest the fix.
			return fmt.Errorf(
				"flow: attach %s/%s (is tracefs mounted in this mount namespace?): %w",
				a.group, a.name, err)
		}
		defer tp.Close()
	}

	events, ok := coll.Maps["events"]
	if !ok {
		return errors.New("flow: compiled object has no events map")
	}
	reader, err := ringbuf.NewReader(events)
	if err != nil {
		return fmt.Errorf("flow: open ring buffer: %w", err)
	}
	defer reader.Close()

	c.log.Info("watching TCP connections", "collector", c.Name())

	// Closing the reader is what unblocks Read; the context alone cannot.
	go func() {
		<-ctx.Done()
		reader.Close()
	}()

	ticker := time.NewTicker(RollupInterval)
	defer ticker.Stop()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if e := c.tracker.Rollup(); e != nil {
					if !collect.Emit(ctx, out, e) {
						return
					}
				}
				if e := c.checkDropped(coll); e != nil {
					if !collect.Emit(ctx, out, e) {
						return
					}
				}
			}
		}
	}()

	for {
		record, err := reader.Read()
		if err != nil {
			if errors.Is(err, ringbuf.ErrClosed) || ctx.Err() != nil {
				<-done
				return nil
			}
			c.log.Warn("ring buffer read failed", "err", err)
			continue
		}
		if len(record.RawSample) < eventSize {
			continue
		}
		raw := record.RawSample
		var (
			saddr = binary.LittleEndian.Uint32(raw[8:12])
			daddr = binary.LittleEndian.Uint32(raw[12:16])
			sport = binary.LittleEndian.Uint16(raw[16:18])
			dport = binary.LittleEndian.Uint16(raw[18:20])
		)

		// raw[24] is the kind: which hook produced this.
		if raw[24] == evReset {
			c.tracker.Reset(saddr, daddr, sport, dport)
			continue
		}

		e := c.tracker.Observe(saddr, daddr, sport, dport, raw[20], raw[21])
		if e != nil && !collect.Emit(ctx, out, e) {
			<-done
			return nil
		}
	}
}

// checkDropped reads the kernel-side loss counter.
//
// This is the honesty valve for the whole eBPF path. If the ring buffer
// overflows and nobody says so, the record shows a quiet network, and a quiet
// network is what an operator concludes when nothing is wrong.
func (c *Collector) checkDropped(coll *ebpf.Collection) *event.Event {
	m, ok := coll.Maps["dropped"]
	if !ok {
		return nil
	}
	var total uint64
	var key uint32
	if err := m.Lookup(&key, &total); err != nil {
		return nil
	}
	delta := total - c.lastDropped
	c.lastDropped = total
	if delta == 0 {
		return nil
	}
	c.log.Warn("kernel dropped events", "count", delta)
	return c.tracker.b.New(event.SourceEBPF, event.KindSystemDrop, event.SevWarn,
		event.Observer(c.observerID)).
		WithAttr("dropped", delta).
		WithAttr("total_dropped", total).
		WithAttr("source", "ringbuf").
		WithEvidence("reason", "ring buffer full: transitions arrived faster than they could be read")
}

var _ collect.Collector = (*Collector)(nil)
