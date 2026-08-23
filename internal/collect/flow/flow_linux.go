//go:build linux

// Package flow observes TCP connections from inside the kernel.
//
// It exists to answer the question netlink cannot: not "is the link up" but
// "can these two machines still talk". A filtering change, a service that died,
// and a route that stopped working are indistinguishable at layer 3 and obvious
// here.
package flow

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
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

// TCP states, from the kernel's tcp_states.h. They are an ABI.
const (
	tcpEstablished = 1
	tcpSynSent     = 2
	tcpSynRecv     = 3
	tcpClose       = 7
	tcpListen      = 10
)

const (
	// rollupInterval is how often ordinary connection activity is summarised.
	//
	// Individual connections are not events. A busy segment opens thousands a
	// second, and recording each would fill the store in a day while telling an
	// operator nothing they could not get from a counter. Only the anomalies -
	// a handshake that never completed - are worth a row of their own.
	rollupInterval = 10 * time.Second
	// maxTracked bounds the connection table. Beyond it, durations stop being
	// measured and the shortfall is reported rather than hidden.
	maxTracked = 65536
	// eventSize must match struct flow_event in the C.
	eventSize = 24
)

// Collector watches TCP state transitions.
type Collector struct {
	b   *event.Builder
	log *slog.Logger

	mu          sync.Mutex
	established map[flowKey]time.Time
	stats       rollup
	untracked   uint64
	lastDropped uint64
}

type flowKey struct {
	saddr, daddr uint32
	sport, dport uint16
}

// rollup accumulates the ordinary activity between two summaries.
type rollup struct {
	opened   int
	closed   int
	failed   int
	refused  int
	peers    map[uint32]struct{}
	totalDur time.Duration
}

func (r *rollup) reset() {
	*r = rollup{peers: make(map[uint32]struct{})}
}

// NewCollector returns a collector for TCP connection activity.
func NewCollector(b *event.Builder, log *slog.Logger) *Collector {
	c := &Collector{b: b, log: log, established: make(map[flowKey]time.Time)}
	c.stats.reset()
	return c
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

	prog, ok := coll.Programs["trace_inet_sock_set_state"]
	if !ok {
		return errors.New("flow: compiled object has no trace_inet_sock_set_state program")
	}
	tp, err := link.Tracepoint("sock", "inet_sock_set_state", prog, nil)
	if err != nil {
		return fmt.Errorf("flow: attach tracepoint: %w", err)
	}
	defer tp.Close()

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

	ticker := time.NewTicker(rollupInterval)
	defer ticker.Stop()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				for _, e := range c.summarise(coll) {
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
		if e := c.handle(record.RawSample); e != nil {
			if !collect.Emit(ctx, out, e) {
				<-done
				return nil
			}
		}
	}
}

// handle turns one state transition into an event, or into a tally.
func (c *Collector) handle(raw []byte) *event.Event {
	saddr := binary.LittleEndian.Uint32(raw[8:12])
	daddr := binary.LittleEndian.Uint32(raw[12:16])
	sport := binary.LittleEndian.Uint16(raw[16:18])
	dport := binary.LittleEndian.Uint16(raw[18:20])
	oldstate := raw[20]
	newstate := raw[21]

	// A socket entering or leaving LISTEN is a service starting or stopping,
	// not a connection opening or closing. Counting it as one made a server
	// shutting down look like a client disconnecting, and put a closed
	// connection in the rollup that was never opened.
	if oldstate == tcpListen || newstate == tcpListen {
		return nil
	}

	key := flowKey{saddr: saddr, daddr: daddr, sport: sport, dport: dport}

	c.mu.Lock()
	defer c.mu.Unlock()

	switch {
	case newstate == tcpEstablished:
		c.stats.opened++
		c.stats.peers[daddr] = struct{}{}
		if len(c.established) < maxTracked {
			c.established[key] = time.Now()
		} else {
			c.untracked++
		}
		return nil

	case newstate == tcpClose && oldstate == tcpSynSent:
		// A connection that went straight from the opening SYN to closed never
		// got an answer. Something between here and there refused it, dropped
		// it, or was not listening - and which of those it was is exactly what
		// the timeline around it will say.
		c.stats.failed++
		c.stats.peers[daddr] = struct{}{}
		delete(c.established, key)
		return c.handshakeFailed(saddr, daddr, sport, dport)

	case newstate == tcpClose:
		if openedAt, ok := c.established[key]; ok {
			c.stats.totalDur += time.Since(openedAt)
			delete(c.established, key)
		}
		if oldstate == tcpSynRecv {
			// An inbound connection abandoned after the SYN was answered.
			c.stats.refused++
		}
		c.stats.closed++
		return nil
	}
	return nil
}

func (c *Collector) handshakeFailed(saddr, daddr uint32, sport, dport uint16) *event.Event {
	dst := ipString(daddr)
	return c.b.New(event.SourceEBPF, event.KindFlowHandshakeFail, event.SevNotice,
		event.Host(dst, "")).
		WithAttr("src", ipString(saddr)).
		WithAttr("dst", dst).
		WithAttr("dport", int(dport)).
		WithAttr("sport", int(sport)).
		// One unanswered connection is ordinary; a run of them to the same
		// service is not, so they fold together and the count carries the
		// weight. Folding on the destination and port also keeps a port scan
		// from filling the store with one row per port.
		WithDedup(fmt.Sprintf("flow.handshake_fail|%s|%d", dst, dport)).
		WithEvidence("transition", "SYN_SENT -> CLOSE")
}

// summarise emits the periodic rollup and reports anything the kernel had to
// throw away.
func (c *Collector) summarise(coll *ebpf.Collection) []*event.Event {
	c.mu.Lock()
	stats := c.stats
	untracked := c.untracked
	tracked := len(c.established)
	c.stats.reset()
	c.untracked = 0
	c.mu.Unlock()

	var out []*event.Event

	if stats.opened+stats.closed+stats.failed > 0 {
		e := c.b.New(event.SourceEBPF, event.KindFlowRollup, event.SevInfo,
			event.Observer(c.b.ObserverID)).
			WithAttr("opened", stats.opened).
			WithAttr("closed", stats.closed).
			WithAttr("handshake_failures", stats.failed).
			WithAttr("distinct_peers", len(stats.peers)).
			WithAttr("window_seconds", int(rollupInterval.Seconds())).
			WithAttr("tracked", tracked)
		if stats.closed > 0 {
			e.WithAttr("mean_duration_ms", (stats.totalDur / time.Duration(stats.closed)).Milliseconds())
		}
		if untracked > 0 {
			// Say so rather than quietly reporting fewer connections.
			e.WithAttr("untracked", untracked)
			e.Severity = event.SevNotice
		}
		out = append(out, e)
	}

	if e := c.checkDropped(coll); e != nil {
		out = append(out, e)
	}
	return out
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
	c.mu.Lock()
	delta := total - c.lastDropped
	c.lastDropped = total
	c.mu.Unlock()

	if delta == 0 {
		return nil
	}
	c.log.Warn("kernel dropped events", "count", delta)
	return c.b.New(event.SourceEBPF, event.KindSystemDrop, event.SevWarn,
		event.Observer(c.b.ObserverID)).
		WithAttr("dropped", delta).
		WithAttr("total_dropped", total).
		WithAttr("source", "ringbuf").
		WithEvidence("reason", "ring buffer full: transitions arrived faster than they could be read")
}

func ipString(addr uint32) string {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], addr)
	return net.IP(b[:]).String()
}

var _ collect.Collector = (*Collector)(nil)
