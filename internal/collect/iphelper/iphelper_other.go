//go:build !windows

package iphelper

import (
	"context"
	"errors"
	"log/slog"

	"github.com/OmarAlghafri/netrewind/internal/collect"
	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/identity"
)

// ErrUnsupported is returned by every collector in this package off Windows.
var ErrUnsupported = errors.New("iphelper: collectors require Windows")

// LinkCollector is the non-Windows placeholder for the interface collector.
type LinkCollector struct{}

// NewLinkCollector returns a collector that will refuse to run here.
func NewLinkCollector(*event.Builder, *slog.Logger) *LinkCollector { return &LinkCollector{} }

// Name implements collect.Collector.
func (c *LinkCollector) Name() string { return "iphelper.link" }

// Run always fails: there is no IP Helper API here.
func (c *LinkCollector) Run(context.Context, chan<- *event.Event) error { return ErrUnsupported }

// RouteCollector is the non-Windows placeholder for the route collector.
type RouteCollector struct{}

// NewRouteCollector returns a collector that will refuse to run here.
func NewRouteCollector(*event.Builder, *slog.Logger) *RouteCollector { return &RouteCollector{} }

// Name implements collect.Collector.
func (c *RouteCollector) Name() string { return "iphelper.route" }

// Run always fails: there is no IP Helper API here.
func (c *RouteCollector) Run(context.Context, chan<- *event.Event) error { return ErrUnsupported }

// AddrCollector is the non-Windows placeholder for the address collector.
type AddrCollector struct{}

// NewAddrCollector returns a collector that will refuse to run here.
func NewAddrCollector(*event.Builder, *slog.Logger) *AddrCollector { return &AddrCollector{} }

// Name implements collect.Collector.
func (c *AddrCollector) Name() string { return "iphelper.addr" }

// Run always fails: there is no IP Helper API here.
func (c *AddrCollector) Run(context.Context, chan<- *event.Event) error { return ErrUnsupported }

// NeighCollector is the non-Windows placeholder for the neighbour collector.
type NeighCollector struct{}

// NewNeighCollector returns a collector that will refuse to run here.
func NewNeighCollector(*event.Builder, *slog.Logger, *identity.Resolver) *NeighCollector {
	return &NeighCollector{}
}

// Name implements collect.Collector.
func (c *NeighCollector) Name() string { return "iphelper.neigh" }

// Run always fails: there is no IP Helper API here.
func (c *NeighCollector) Run(context.Context, chan<- *event.Event) error { return ErrUnsupported }

var (
	_ collect.Collector = (*LinkCollector)(nil)
	_ collect.Collector = (*RouteCollector)(nil)
	_ collect.Collector = (*AddrCollector)(nil)
	_ collect.Collector = (*NeighCollector)(nil)
)
