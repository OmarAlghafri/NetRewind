//go:build !linux && !windows

package main

import (
	"log/slog"

	"github.com/OmarAlghafri/netrewind/internal/collect"
	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/identity"
)

// platformCollectors is empty off Linux and Windows: the recorder still
// starts, serves its API and keeps its record, and every collector is
// reported as unsupported rather than pretended to run.
func platformCollectors(config, *event.Builder, *slog.Logger, *identity.Resolver) []collect.Collector {
	return nil
}

func runService(*slog.Logger, config) (bool, error) { return false, nil }

// serviceCommand reports that there is no service subcommand on this
// platform: systemd (or any supervisor) runs the plain process.
func serviceCommand([]string) (bool, error) { return false, nil }
