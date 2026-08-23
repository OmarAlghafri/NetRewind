//go:build !linux

// Package flow observes TCP connections from inside the kernel. This stub keeps
// the tree building off Linux, where there is no kernel to observe.
package flow

import (
	"context"
	"errors"
	"log/slog"

	"github.com/OmarAlghafri/netrewind/internal/collect"
	"github.com/OmarAlghafri/netrewind/internal/event"
)

// ErrUnsupported is returned everywhere but Linux.
var ErrUnsupported = errors.New("flow: eBPF connection tracking requires Linux")

// Collector is the non-Linux placeholder.
type Collector struct{}

// NewCollector returns a collector that will refuse to run on this platform.
func NewCollector(_ *event.Builder, _ *slog.Logger) *Collector { return &Collector{} }

// Name implements collect.Collector.
func (c *Collector) Name() string { return "ebpf.flow" }

// Run always fails: there is no kernel here to load a program into.
func (c *Collector) Run(_ context.Context, _ chan<- *event.Event) error { return ErrUnsupported }

var _ collect.Collector = (*Collector)(nil)
