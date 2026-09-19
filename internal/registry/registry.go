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

// Reason codes a caller can pass to DownCoded/UnsupportedCoded, named
// rather than typed as a string literal at each call site for the same
// reason agent.rs's codes module in the desktop shell is - a typo in a
// literal would silently produce an untranslatable code no test could
// catch; a typo in a constant name is a compile error instead.
// desktop/src/i18n/capabilityReasonCatalogue.ts must have an entry for
// each of these; ReasonCodesUsedByThisBuild below is what a test on that
// side is hand-kept matching against.
const (
	ReasonRequiresPlatform = "requires_platform"
	ReasonCollectorStopped = "collector_stopped"
)

// ReasonCodesUsedByThisBuild is every code cmd/netrewindd actually passes
// to DownCoded/UnsupportedCoded - a registry_test.go test proves this list
// has no duplicate, and desktop/src/i18n/capabilityReasonCatalogue.test.ts
// hand-keeps a matching literal list on the TypeScript side.
var ReasonCodesUsedByThisBuild = []string{ReasonRequiresPlatform, ReasonCollectorStopped}

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
	status       Status
	reason       string
	reasonCode   string
	reasonParams map[string]string
	lastChange   time.Time
	lastSeen     time.Time
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
	r.mark(name, StatusUp, "", "", nil)
}

// Down marks a collector as not watching, and names why. reason should be the
// same text that goes on the collector's system.collector_down event, so a
// reader is never given two different explanations for the same fact.
func (r *Registry) Down(name, reason string) {
	r.mark(name, StatusDown, reason, "", nil)
}

// DownCoded is Down plus a structured reason (ADR 0004 §4.5: "the Go
// capability reason become{s} {code, params, technical_detail}") for a
// caller that knows why in a form a GUI can translate, not just a free-text
// sentence. code should be a short, stable identifier
// (desktop/src/i18n/capabilityReasonCatalogue.ts must have an entry for
// it); reason is still required and still goes on the collector_down event,
// exactly as Down's - an unrecognised or absent code always falls back to
// it, so this is additive, never a replacement for the plain sentence.
func (r *Registry) DownCoded(name, code string, params map[string]string, reason string) {
	r.mark(name, StatusDown, reason, code, params)
}

// Unsupported marks a collector as impossible on this platform, with the
// reason a capability report should show (e.g. "requires Linux").
func (r *Registry) Unsupported(name, reason string) {
	r.mark(name, StatusUnsupported, reason, "", nil)
}

// UnsupportedCoded is Unsupported plus a structured reason - see DownCoded.
func (r *Registry) UnsupportedCoded(name, code string, params map[string]string, reason string) {
	r.mark(name, StatusUnsupported, reason, code, params)
}

func (r *Registry) mark(name string, s Status, reason, code string, params map[string]string) {
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
	e.reasonCode = code
	e.reasonParams = params
	now := r.now()
	e.lastChange = now
	if s == StatusUp {
		e.lastSeen = now
	}
}

// Snapshot is one collector's descriptor plus its live state, for reporting.
type Snapshot struct {
	Descriptor
	Status Status `json:"status"`
	Reason string `json:"reason,omitempty"`
	// ReasonCode and ReasonParams are the structured form of Reason (ADR
	// 0004 §4.5), present only when the call that set this status used
	// DownCoded/UnsupportedCoded instead of the plain Down/Unsupported.
	// Absent entirely - never an empty object - when there is none, so a
	// GUI's fallback to Reason itself (an older recorder, or a status set
	// through the uncoded path) is "no code", not "an empty code".
	ReasonCode   string            `json:"reason_code,omitempty"`
	ReasonParams map[string]string `json:"reason_params,omitempty"`
	LastChange   time.Time         `json:"last_change"`
	LastSeen     time.Time         `json:"last_seen"`
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
			Descriptor:   e.Descriptor,
			Status:       e.status,
			Reason:       e.reason,
			ReasonCode:   e.reasonCode,
			ReasonParams: e.reasonParams,
			LastChange:   e.lastChange,
			LastSeen:     e.lastSeen,
		})
	}
	return out
}
