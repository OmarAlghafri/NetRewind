//go:build !linux

// Package netlink is a Linux-only source. These stubs exist so the rest of the
// tree still builds, vets and tests on a developer's Windows or macOS machine;
// only the collectors themselves need the target kernel.
package netlink

import (
	"context"
	"errors"
	"log/slog"

	"github.com/OmarAlghafri/netrewind/internal/collect"
	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/identity"
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

// NeighCollector is the non-Linux placeholder for the neighbour-table collector.
type NeighCollector struct{}

// NewNeighCollector returns a collector that will refuse to run on this platform.
func NewNeighCollector(_ *event.Builder, _ *slog.Logger, _ *identity.Resolver) *NeighCollector {
	return &NeighCollector{}
}

// Name implements collect.Collector.
func (c *NeighCollector) Name() string { return "netlink.neigh" }

// Run always fails: there is no netlink socket to read here.
func (c *NeighCollector) Run(_ context.Context, _ chan<- *event.Event) error { return ErrUnsupported }

// RouteCollector is the non-Linux placeholder for the routing-table collector.
type RouteCollector struct{}

// NewRouteCollector returns a collector that will refuse to run on this platform.
func NewRouteCollector(_ *event.Builder, _ *slog.Logger) *RouteCollector { return &RouteCollector{} }

// Name implements collect.Collector.
func (c *RouteCollector) Name() string { return "netlink.route" }

// Run always fails: there is no netlink socket to read here.
func (c *RouteCollector) Run(_ context.Context, _ chan<- *event.Event) error { return ErrUnsupported }

// AddrCollector is the non-Linux placeholder for the address collector.
type AddrCollector struct{}

// NewAddrCollector returns a collector that will refuse to run on this platform.
func NewAddrCollector(_ *event.Builder, _ *slog.Logger) *AddrCollector { return &AddrCollector{} }

// Name implements collect.Collector.
func (c *AddrCollector) Name() string { return "netlink.addr" }

// Run always fails: there is no netlink socket to read here.
func (c *AddrCollector) Run(_ context.Context, _ chan<- *event.Event) error { return ErrUnsupported }

var (
	_ collect.Collector = (*LinkCollector)(nil)
	_ collect.Collector = (*NeighCollector)(nil)
	_ collect.Collector = (*RouteCollector)(nil)
	_ collect.Collector = (*AddrCollector)(nil)
)
