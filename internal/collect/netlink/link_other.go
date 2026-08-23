//go:build !linux

// Package netlink is a Linux-only source. This stub exists so the rest of the
// tree still builds, vets and tests on a developer's Windows or macOS machine;
// only the collectors themselves need the target kernel.
package netlink

import (
	"context"
	"errors"
	"log/slog"

	"github.com/OmarAlghafri/netrewind/internal/collect"
	"github.com/OmarAlghafri/netrewind/internal/event"
)

// ErrUnsupported is returned by every collector in this package off Linux.
var ErrUnsupported = errors.New("netlink: collectors require Linux")

// LinkCollector is the non-Linux placeholder for the interface-state collector.
type LinkCollector struct{}

// NewLinkCollector returns a collector that will refuse to run on this platform.
func NewLinkCollector(_ *event.Builder, _ *slog.Logger) *LinkCollector { return &LinkCollector{} }

// Name implements collect.Collector.
func (c *LinkCollector) Name() string { return "netlink.link" }

// Run always fails: there is no netlink socket to read here.
func (c *LinkCollector) Run(_ context.Context, _ chan<- *event.Event) error { return ErrUnsupported }

var _ collect.Collector = (*LinkCollector)(nil)
