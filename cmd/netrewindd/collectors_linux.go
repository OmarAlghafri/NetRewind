package main

import (
	"log/slog"

	"github.com/OmarAlghafri/netrewind/internal/collect"
	"github.com/OmarAlghafri/netrewind/internal/collect/flow"
	"github.com/OmarAlghafri/netrewind/internal/collect/netlink"
	"github.com/OmarAlghafri/netrewind/internal/collect/policy"
	"github.com/OmarAlghafri/netrewind/internal/collect/probe"
	"github.com/OmarAlghafri/netrewind/internal/collect/wire"
	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/identity"
)

// platformCollectors is every source this build can watch on Linux: the
// kernel's own netlink notifications for links, neighbours, routes and
// addresses; eBPF for flows; nftables for policy; raw sockets for what is
// on the wire; and active reachability probes.
func platformCollectors(cfg config, b *event.Builder, log *slog.Logger, ids *identity.Resolver) []collect.Collector {
	return []collect.Collector{
		netlink.NewLinkCollector(b, log),
		netlink.NewNeighCollector(b, log, ids),
		netlink.NewRouteCollector(b, log),
		netlink.NewAddrCollector(b, log),
		flow.NewCollector(b, log),
		policy.NewCollector(b, log),
		wire.NewCollector(b, log, cfg.WireIface, cfg.RecordDNSNames),
		probe.NewCollector(b, log, cfg.ProbeTargets),
	}
}

// runService reports that no service manager owns this process: on Linux
// systemd supervises the plain process and stops it with a signal.
func runService(*slog.Logger, config) (bool, error) { return false, nil }

// serviceCommand reports that there is no service subcommand on this
// platform: systemd (or any supervisor) runs the plain process.
func serviceCommand([]string) (bool, error) { return false, nil }
