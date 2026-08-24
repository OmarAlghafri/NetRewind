package wire

import (
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
)

// sweepInterval is how often the pending-query table is pruned and the kernel's
// drop counters are drained.
const sweepInterval = 30 * time.Second

// dropShareSerious is the fraction of lost frames past which the conclusions
// drawn from a window stop being trustworthy rather than merely incomplete.
const dropShareSerious = 0.05

// dropEvent reports frames the kernel threw away before this process saw them.
//
// This is the failure mode the whole project is built against. A recorder that
// quietly misses frames while presenting an unbroken timeline is worse than no
// recorder, because the timeline is then evidence for a conclusion it does not
// support. Anyone reading the record has to be able to see the hole, which is
// why this is an event in the same stream as everything else and not a line in
// a log nobody reads.
//
// The policy lives here rather than beside the syscall because deciding how
// serious a loss is has nothing to do with the platform, and this way it is
// tested everywhere rather than only where a packet socket exists.
func dropEvent(b *event.Builder, name, iface string, received, dropped uint32) *event.Event {
	total := uint64(received) + uint64(dropped)
	var share float64
	if total > 0 {
		share = float64(dropped) / float64(total)
	}

	sev := event.SevWarn
	if share > dropShareSerious {
		sev = event.SevError
	}
	return b.New(event.SourceInternal, event.KindSystemDrop, sev, event.Observer(name)).
		WithAttr("component", name).
		WithAttr("dropped", int64(dropped)).
		WithAttr("received", int64(received)).
		WithAttr("iface", ifaceOrAll(iface)).
		WithAttr("reason", "packet socket buffer full").
		WithDedup("system.drop|"+name).
		WithEvidence("drop_share", share).
		WithEvidence("window_seconds", int(sweepInterval.Seconds()))
}

func ifaceOrAll(name string) string {
	if name == "" {
		return "all"
	}
	return name
}
