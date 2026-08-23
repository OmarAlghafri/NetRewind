//go:build !linux

package policy

import (
	"context"
	"errors"
	"log/slog"

	"github.com/OmarAlghafri/netrewind/internal/collect"
	"github.com/OmarAlghafri/netrewind/internal/event"
)

// ErrUnsupported is returned everywhere but Linux.
var ErrUnsupported = errors.New("policy: nftables is a Linux facility")

// Collector is the non-Linux placeholder.
type Collector struct{}

// NewCollector returns a collector that will refuse to run on this platform.
func NewCollector(_ *event.Builder, _ *slog.Logger) *Collector { return &Collector{} }

// Name implements collect.Collector.
func (c *Collector) Name() string { return "policy.nftables" }

// Run always fails: there is no nftables here.
func (c *Collector) Run(_ context.Context, _ chan<- *event.Event) error { return ErrUnsupported }

var _ collect.Collector = (*Collector)(nil)
