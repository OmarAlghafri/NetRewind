package main

import (
	"log/slog"

	"github.com/OmarAlghafri/netrewind/internal/collect"
	"github.com/OmarAlghafri/netrewind/internal/collect/iphelper"
	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/identity"
)

// platformCollectors is every source this build can watch on Windows: the
// IP Helper API's interface, address and route change notifications and a
// polled neighbour table. Flows, filtering policy, on-the-wire DHCP/DNS and
// active probes have no Windows source in this release and are reported as
// unsupported by the registry rather than run as stubs.
func platformCollectors(cfg config, b *event.Builder, log *slog.Logger, ids *identity.Resolver) []collect.Collector {
	return []collect.Collector{
		iphelper.NewLinkCollector(b, log),
		iphelper.NewAddrCollector(b, log),
		iphelper.NewRouteCollector(b, log),
		iphelper.NewNeighCollector(b, log, ids),
	}
}
