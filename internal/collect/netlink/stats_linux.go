//go:build linux

package netlink

import (
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
	nl "github.com/vishvananda/netlink"
)

// readStats records the current counters without comparing them.
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
		st, known := c.state[a.Index]
		if !known {
			continue
		}
		st.stats = countersFrom(a, now)
		c.state[a.Index] = st
	}
}

// compareStats reads the counters again and reports what changed.
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
		st, known := c.state[a.Index]
		if !known {
			continue
		}
		cur := countersFrom(a, now)
		prev := st.stats
		st.stats = cur
		c.state[a.Index] = st

		if prev.at.IsZero() {
			continue // nothing to compare against yet
		}

		// Counters reset when an interface is recreated, and a negative delta
		// read as an unsigned number becomes an enormous positive one.
		if cur.rxPackets < prev.rxPackets || cur.txPackets < prev.txPackets {
			continue
		}

		packets := (cur.rxPackets - prev.rxPackets) + (cur.txPackets - prev.txPackets)
		errors := (cur.rxErrors - prev.rxErrors) + (cur.txErrors - prev.txErrors)
		dropped := (cur.rxDropped - prev.rxDropped) + (cur.txDropped - prev.txDropped)
		bad := errors + dropped

		if packets < minPacketsForRate || bad == 0 {
			continue
		}
		rate := float64(bad) / float64(packets+bad)
		if rate < errorRateThreshold {
			continue
		}

		events = append(events,
			c.b.New(event.SourceNetlink, event.KindLinkErrorRate, event.SevWarn,
				event.Iface(a.Name, a.Index)).
				WithAttr("ifname", a.Name).
				WithAttr("error_rate_percent", round2(rate*100)).
				WithAttr("errors", errors).
				WithAttr("dropped", dropped).
				WithAttr("packets", packets).
				WithAttr("window_seconds", int(now.Sub(prev.at).Seconds())).
				WithDedup("link.error_rate|"+a.Name).
				WithEvidence("threshold_percent", errorRateThreshold*100))
	}
	return events
}

func countersFrom(a *nl.LinkAttrs, now time.Time) linkCounters {
	s := a.Statistics
	return linkCounters{
		rxPackets: s.RxPackets, txPackets: s.TxPackets,
		rxErrors: s.RxErrors, txErrors: s.TxErrors,
		rxDropped: s.RxDropped, txDropped: s.TxDropped,
		at: now,
	}
}

func trimBefore(times []time.Time, cutoff time.Time) []time.Time {
	kept := times[:0]
	for _, t := range times {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	return kept
}

func round2(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}
