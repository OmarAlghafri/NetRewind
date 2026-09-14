//go:build linux

package netlink

import (
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/ports"
	nl "github.com/vishvananda/netlink"
)

// readStats primes the analyzer's counters baseline without comparing
// anything, so the first real comparison has something to compare against
// rather than reading the first sample as an infinite rate.
func (c *LinkCollector) readStats(now time.Time) {
	links, err := nl.LinkList()
	if err != nil {
		return
	}
	for _, l := range links {
		a := l.Attrs()
		if a == nil || a.Statistics == nil {
			continue
		}
		c.analyzer.CompareCounters(a.Index, a.Name, countersFrom(a), now)
	}
}

// compareStats reads the counters again and reports what changed. The rate
// decision itself lives in internal/analyze.InterfaceAnalyzer.CompareCounters;
// this is only the netlink-specific reading of them.
func (c *LinkCollector) compareStats(now time.Time) []*event.Event {
	links, err := nl.LinkList()
	if err != nil {
		c.log.Warn("could not read interface counters", "err", err)
		return nil
	}

	var events []*event.Event
	for _, l := range links {
		a := l.Attrs()
		if a == nil || a.Statistics == nil {
			continue
		}
		events = append(events, c.analyzer.CompareCounters(a.Index, a.Name, countersFrom(a), now)...)
	}
	return events
}

func countersFrom(a *nl.LinkAttrs) ports.InterfaceCounters {
	s := a.Statistics
	return ports.InterfaceCounters{
		RxPackets: s.RxPackets, TxPackets: s.TxPackets,
		RxErrors: s.RxErrors, TxErrors: s.TxErrors,
		RxDropped: s.RxDropped, TxDropped: s.TxDropped,
	}
}
