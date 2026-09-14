// Package registry tracks what NetRewind's collectors are, on this platform,
// right now - not what the documentation says should exist.
//
// It exists for the capability page a desktop UI is required to show
// (PRODUCT_RELEASE_PLAN_AR.md, "لا يجوز تسمية قدرة بأنها 'مدعومة' لمجرد أن
// التطبيق يفتح على النظام"): which sources are watching, which are not, and
// why. cmd/netrewindd still owns constructing the actual collect.Collector
// values, since that part is genuinely platform-specific; this package owns
// only the metadata and live status around them, so a future local API can
// report it without reaching into the daemon's internals.
package registry

import (
	"sync"
	"time"
)

// Status is a collector's live state, as last observed. It is deliberately
// not a bool: "never started" and "started, then failed" read the same to a
// bool but mean different things to an operator deciding whether to wait.
type Status string

const (
	StatusUnknown Status = "unknown" // registered, has not reported in yet
	StatusUp      Status = "up"
	StatusDown    Status = "down"
	// StatusUnsupported means this build cannot run the collector on this
	// platform at all - it was never started and never will be, which a
	// reader must be able to tell apart from one that started and failed.
	StatusUnsupported Status = "unsupported"
)

// Descriptor is what is known about a collector before it ever runs: fixed
// facts, not live state. Kept separate from Status so a capability page can
// explain a collector that is down, or one that was never buildable on this
// platform at all, using the same shape either way.
type Descriptor struct {
	// Name matches collect.Collector.Name() and the collector= label on its
	// system.collector_down events. The two must never drift apart, which is
	// why callers should derive both from one constant rather than typing the
	// string twice.
	Name string `json:"name"`
	// Platform is what this collector needs to do anything at all: "linux",
	// "windows", or "any". A stub compiled in for a platform it does not
	// support belongs here with its real requirement, not "any" - the point
	// of this field is to say why a collector is down, not to hide that it
	// is platform-bound.
	Platform string `json:"platform"`
	// Privilege names the capability or permission the collector needs, in
	// terms an operator can act on: "CAP_NET_ADMIN", "CAP_NET_RAW",
	// "administrator", or "none".
	Privilege string `json:"privilege"`
	// Coverage lists the event kind families this collector is the source
	// of, e.g. "link.*", "l2.*". It is what lets a UI explain an absence:
	// no events of this family were recorded because nothing was watching
	// for them, which reads very differently from nothing having happened.
	Coverage []string `json:"coverage"`
}

type trackedEntry struct {
	Descriptor
	status     Status
	reason     string
	lastChange time.Time
	lastSeen   time.Time
}

// Registry is safe for concurrent use: each collector reports its own status
// from its own goroutine, and a reader (metrics, CLI, a future API) can poll
// it at any time without coordinating with either.
type Registry struct {
	mu      sync.RWMutex
	entries map[string]*trackedEntry
	order   []string // registration order, so reports read consistently
	now     func() time.Time
}

// New returns an empty registry. A nil clock means time.Now; tests pass their
// own so status transitions can be asserted at exact, controlled instants.
func New(now func() time.Time) *Registry {
	if now == nil {
		now = time.Now
	}
	return &Registry{entries: make(map[string]*trackedEntry), now: now}
}

// Register declares a collector's static facts, before anyone has started it.
// Calling it twice for the same name replaces the descriptor and resets live
// state to unknown - this is a declaration of what the collector is, not an
// accumulator of what it has done.
func (r *Registry) Register(d Descriptor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.entries[d.Name]; !exists {
		r.order = append(r.order, d.Name)
	}
	r.entries[d.Name] = &trackedEntry{Descriptor: d, status: StatusUnknown}
}

// Up marks a collector as actively watching, right now.
func (r *Registry) Up(name string) {
	r.mark(name, StatusUp, "")
}

// Down marks a collector as not watching, and names why. reason should be the
// same text that goes on the collector's system.collector_down event, so a
// reader is never given two different explanations for the same fact.
func (r *Registry) Down(name, reason string) {
	r.mark(name, StatusDown, reason)
}

// Unsupported marks a collector as impossible on this platform, with the
// reason a capability report should show (e.g. "requires Linux").
func (r *Registry) Unsupported(name, reason string) {
	r.mark(name, StatusUnsupported, reason)
}

func (r *Registry) mark(name string, s Status, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[name]
	if !ok {
		// A status report for a collector nobody registered is a bug in the
		// caller, not something to drop silently: record it anyway, with an
		// otherwise-empty descriptor that is itself evidence something is
		// unregistered and worth finding.
		e = &trackedEntry{Descriptor: Descriptor{Name: name}}
		r.entries[name] = e
		r.order = append(r.order, name)
	}
	e.status = s
	e.reason = reason
	now := r.now()
	e.lastChange = now
	if s == StatusUp {
		e.lastSeen = now
	}
}

// Snapshot is one collector's descriptor plus its live state, for reporting.
type Snapshot struct {
	Descriptor
	Status     Status    `json:"status"`
	Reason     string    `json:"reason,omitempty"`
	LastChange time.Time `json:"last_change"`
	LastSeen   time.Time `json:"last_seen"`
}

// Snapshot returns every registered collector's current state, in
// registration order. It is a copy: the caller cannot corrupt the registry's
// own state by holding onto or modifying the result.
func (r *Registry) Snapshot() []Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Snapshot, 0, len(r.order))
	for _, name := range r.order {
		e := r.entries[name]
		out = append(out, Snapshot{
			Descriptor: e.Descriptor,
			Status:     e.status,
			Reason:     e.reason,
			LastChange: e.lastChange,
			LastSeen:   e.lastSeen,
		})
	}
	return out
}
