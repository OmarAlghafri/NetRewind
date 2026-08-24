//go:build !linux

package wire

import (
	"context"
	"errors"
	"log/slog"

	"github.com/OmarAlghafri/netrewind/internal/collect"
	"github.com/OmarAlghafri/netrewind/internal/event"
)

// ErrUnsupported is returned everywhere but Linux. The parsing and the
// watchers are platform-neutral and fully tested here; only the socket needs a
// kernel.
var ErrUnsupported = errors.New("wire: raw packet capture requires Linux")

// Collector is the non-Linux placeholder.
type Collector struct{ Iface string }

// NewCollector returns a collector that will refuse to run on this platform.
func NewCollector(_ *event.Builder, _ *slog.Logger, iface string, _ bool) *Collector {
	return &Collector{Iface: iface}
}

// Name implements collect.Collector.
func (c *Collector) Name() string { return "wire" }

// Run always fails: there is no packet socket here.
func (c *Collector) Run(_ context.Context, _ chan<- *event.Event) error { return ErrUnsupported }

var _ collect.Collector = (*Collector)(nil)
