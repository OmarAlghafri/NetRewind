// Package identity answers the question the rest of the system depends on:
// when two observations, minutes or days apart, are about the same machine.
//
// It cannot be answered with an address. An IP moves between machines. A MAC is
// forged, and modern phones randomise it per network. A hostname may not exist.
// So a host here is a stable identifier, and everything observable about it is a
// *binding* with a lifetime: this MAC belonged to this host from then until now.
package identity

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

// Attribute types. MAC is treated as the strongest of them - not because it is
// trustworthy, but because it changes least often among the things we can see.
const (
	AttrMAC      = "mac"
	AttrIPv4     = "ipv4"
	AttrIPv6     = "ipv6"
	AttrHostname = "hostname"
)

// Attr is one observed fact about a machine.
type Attr struct {
	Type  string
	Value string
}

func (a Attr) key() string { return a.Type + "|" + a.Value }

// Binding is an attribute belonging to a host over a period of time. ValidTo is
// zero while the binding is current.
type Binding struct {
	HostID     string
	AttrType   string
	AttrValue  string
	ValidFrom  int64
	ValidTo    int64
	Confidence uint8
}

// Current reports whether the binding has not been superseded.
func (b Binding) Current() bool { return b.ValidTo == 0 }

// Repo persists bindings. It is defined here rather than in the store package
// so that identity stays the owner of its own vocabulary; the SQLite store
// implements it.
type Repo interface {
	// LoadCurrent returns every binding that has not been closed.
	LoadCurrent(ctx context.Context) ([]Binding, error)
	// Open records a new binding as current from the given instant.
	Open(ctx context.Context, b Binding) error
	// CloseBinding ends the current binding of an attribute.
	CloseBinding(ctx context.Context, attrType, attrValue string, at int64) error
	// ResolveAt returns the host an attribute belonged to at an instant, which
	// is not necessarily the host it belongs to now.
	ResolveAt(ctx context.Context, attrType, value string, at int64) (string, bool, error)
	// LabelsFor returns every attribute value bound to a host during a window,
	// so a query for one address can find events recorded under another.
	LabelsFor(ctx context.Context, hostID string, from, to int64) ([]string, error)
}

// Resolver maps observed attributes onto stable host identities, keeping the
// current picture in memory and the full history in the repository.
type Resolver struct {
	mu     sync.RWMutex
	repo   Repo
	byAttr map[string]string // attr key -> host id, current bindings only
	// hostAttrs is the reverse index: what each host currently answers to. It
	// is what makes it possible to notice that an address has changed hands
	// rather than that a known machine has acquired another name.
	hostAttrs map[string]map[string]string // host id -> attr type -> value
}

// New loads the current bindings and returns a ready resolver.
func New(ctx context.Context, repo Repo) (*Resolver, error) {
	r := &Resolver{
		repo:      repo,
		byAttr:    make(map[string]string),
		hostAttrs: make(map[string]map[string]string),
	}
	current, err := repo.LoadCurrent(ctx)
	if err != nil {
		return nil, fmt.Errorf("identity: load current bindings: %w", err)
	}
	for _, b := range current {
		r.bindLocked(b.HostID, Attr{b.AttrType, b.AttrValue})
	}
	return r, nil
}

// bindLocked records a binding in both indexes, detaching the attribute from
// whichever host used to hold it.
func (r *Resolver) bindLocked(hostID string, a Attr) {
	if prev, ok := r.byAttr[a.key()]; ok && prev != hostID {
		if attrs := r.hostAttrs[prev]; attrs != nil && attrs[a.Type] == a.Value {
			delete(attrs, a.Type)
			if len(attrs) == 0 {
				delete(r.hostAttrs, prev)
			}
		}
	}
	r.byAttr[a.key()] = hostID
	if r.hostAttrs[hostID] == nil {
		r.hostAttrs[hostID] = make(map[string]string, 2)
	}
	r.hostAttrs[hostID][a.Type] = a.Value
}

// Observation is what a collector saw together at one instant: an address and
// the hardware address answering for it, for example.
type Observation struct {
	At    time.Time
	Attrs []Attr
}

// Result describes what the resolver made of an observation.
type Result struct {
	HostID string
	// New is true when this observation created a host we had never seen.
	New bool
	// Moved lists attributes that used to belong to a different host. An
	// address moving between machines is not an error - DHCP does it all day -
	// but it is exactly the kind of thing an incident turns on, so it is
	// reported rather than silently absorbed.
	Moved []Attr
	// Displaced is true when a known address turned up behind a hardware
	// address that is not the one currently bound to it. The address changed
	// hands; the machine behind it is a different machine.
	Displaced bool
}

// Observe folds an observation into the identity table and returns the host it
// belongs to.
//
// The rule is that the strongest attribute present wins. If a MAC we already
// know appears with a new address, the address joins that host. If an address
// we know appears with a new MAC, the machine behind the address has changed
// and that is reported as a move.
func (r *Resolver) Observe(ctx context.Context, obs Observation) (Result, error) {
	if len(obs.Attrs) == 0 {
		return Result{}, fmt.Errorf("identity: observation with no attributes")
	}
	at := obs.At.UnixNano()

	r.mu.Lock()
	defer r.mu.Unlock()

	// Prefer the host the strongest present attribute already points at.
	sorted := append([]Attr(nil), obs.Attrs...)
	sort.SliceStable(sorted, func(i, j int) bool { return strength(sorted[i].Type) > strength(sorted[j].Type) })

	var res Result
	matchedVia := ""
	for _, a := range sorted {
		if host, ok := r.byAttr[a.key()]; ok {
			res.HostID = host
			matchedVia = a.Type
			break
		}
	}

	// We recognised the address but not the machine behind it. If the host that
	// address belongs to is currently bound to a different hardware address,
	// the address has changed hands - and the new occupant is a different
	// machine, not a new alias for the old one.
	//
	// Getting this wrong is how an identity table quietly merges an attacker
	// with its victim, or two machines fighting over one address into a single
	// host that appears to have three hardware addresses.
	if res.HostID != "" && matchedVia != AttrMAC {
		if newMAC, ok := attrOf(obs.Attrs, AttrMAC); ok {
			if cur, bound := r.hostAttrs[res.HostID][AttrMAC]; bound && cur != newMAC {
				res.HostID = ""
				res.Displaced = true
			}
		}
	}

	if res.HostID == "" {
		res.HostID = ulid.Make().String()
		res.New = true
	}

	for _, a := range obs.Attrs {
		prev, known := r.byAttr[a.key()]
		if known && prev == res.HostID {
			continue // already ours, nothing changed
		}
		if known {
			// The attribute is being taken from another host.
			if err := r.repo.CloseBinding(ctx, a.Type, a.Value, at); err != nil {
				return res, err
			}
			res.Moved = append(res.Moved, a)
		}
		b := Binding{
			HostID:     res.HostID,
			AttrType:   a.Type,
			AttrValue:  a.Value,
			ValidFrom:  at,
			Confidence: confidenceFor(a.Type),
		}
		if err := r.repo.Open(ctx, b); err != nil {
			return res, err
		}
		r.bindLocked(res.HostID, a)
	}
	return res, nil
}

func attrOf(attrs []Attr, attrType string) (string, bool) {
	for _, a := range attrs {
		if a.Type == attrType {
			return a.Value, true
		}
	}
	return "", false
}

// Resolve returns the host an attribute currently belongs to.
func (r *Resolver) Resolve(a Attr) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	host, ok := r.byAttr[a.key()]
	return host, ok
}

// ResolveAt returns the host an attribute belonged to at a past instant. This
// is the one that matters during an investigation: the address being asked
// about may since have moved to a different machine entirely.
func (r *Resolver) ResolveAt(ctx context.Context, a Attr, at time.Time) (string, bool, error) {
	return r.repo.ResolveAt(ctx, a.Type, a.Value, at.UnixNano())
}

// LabelsFor returns every address and name a host answered to during a window.
func (r *Resolver) LabelsFor(ctx context.Context, hostID string, from, to time.Time) ([]string, error) {
	return r.repo.LabelsFor(ctx, hostID, from.UnixNano(), to.UnixNano())
}

// strength orders attribute types by how stable they are in practice.
func strength(attrType string) int {
	switch attrType {
	case AttrMAC:
		return 3
	case AttrHostname:
		return 2
	case AttrIPv4, AttrIPv6:
		return 1
	default:
		return 0
	}
}

// confidenceFor records how much a binding of this kind should be trusted.
// A MAC is the most stable thing we observe and still only earns 90: MAC
// randomisation and spoofing are both ordinary, and a number that claimed
// certainty here would be lying.
func confidenceFor(attrType string) uint8 {
	switch attrType {
	case AttrMAC:
		return 90
	case AttrHostname:
		return 70
	default:
		return 60
	}
}
