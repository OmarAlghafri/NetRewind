//go:build !linux

package probe

import (
	"context"
	"errors"
	"log/slog"

	"github.com/OmarAlghafri/netrewind/internal/collect"
	"github.com/OmarAlghafri/netrewind/internal/event"
)

// ErrUnsupported is returned everywhere but Linux. The judgement - what counts
// as loss, what counts as a spike - is in the platform-neutral Analyser and is
// fully tested here.
var ErrUnsupported = errors.New("probe: ICMP probing requires Linux")

// Collector is the non-Linux placeholder.
type Collector struct{ Targets []string }

// NewCollector returns a collector that will refuse to run on this platform.
func NewCollector(_ *event.Builder, _ *slog.Logger, targets string) *Collector {
	return &Collector{Targets: ParseTargets(targets)}
}

// Name implements collect.Collector.
func (c *Collector) Name() string { return "probe.icmp" }

// Run always fails: there is no ICMP socket here.
func (c *Collector) Run(_ context.Context, _ chan<- *event.Event) error { return ErrUnsupported }

var _ collect.Collector = (*Collector)(nil)
