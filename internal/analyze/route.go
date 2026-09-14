package analyze

import (
	"fmt"

	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/ports"
)

type routeEntry struct {
	gw        string
	linkIndex int
	priority  int
}

// id distinguishes two entries to the same destination.
func (r routeEntry) id() string {
	return fmt.Sprintf("%s|%d|%d", r.gw, r.linkIndex, r.priority)
}

// RouteAnalyzer watches the winner among possibly several routes to each
// destination and turns a change of winner into an l3.* event. It holds one
// route table (keyed by prefix); an adapter is responsible for filtering to
// whichever table it considers "the routes that matter" before calling
// Observe - on Linux that is the main table, and that filtering stays in
// internal/collect/netlink since "which table is main" is a Linux concept.
type RouteAnalyzer struct {
	b      *event.Builder
	routes map[string][]routeEntry
}

// NewRouteAnalyzer returns an analyzer with no routes known yet.
func NewRouteAnalyzer(b *event.Builder) *RouteAnalyzer {
	return &RouteAnalyzer{b: b, routes: make(map[string][]routeEntry)}
}

// Seed records a route entry as part of the table's initial state, without
// emitting anything.
func (a *RouteAnalyzer) Seed(obs ports.RouteObservation) {
	key := obs.Prefix
	a.routes[key] = upsertRoute(a.routes[key], entryOf(obs))
}

// Known reports how many distinct destinations are tracked, for a
// collector's own startup logging.
func (a *RouteAnalyzer) Known() int { return len(a.routes) }

// Observe compares one route notification against the current winner for its
// destination and returns the events the difference justifies.
func (a *RouteAnalyzer) Observe(obs ports.RouteObservation) []*event.Event {
	key := obs.Prefix
	subject := event.Subnet(key)
	cur := entryOf(obs)

	before, hadAny := winner(a.routes[key])

	switch {
	case obs.Removed:
		a.routes[key] = removeRoute(a.routes[key], cur)
	case obs.Replace:
		// A replace supersedes the entry with the same destination and
		// metric rather than joining it - see the Replace field's own doc
		// comment in internal/ports/route.go.
		a.routes[key] = upsertRoute(supersedeRoute(a.routes[key], cur), cur)
	default:
		a.routes[key] = upsertRoute(a.routes[key], cur)
	}
	after, hasAny := winner(a.routes[key])

	switch {
	case !hadAny && hasAny:
		return []*event.Event{
			a.b.New(event.SourceNetlink, event.KindRouteAdded, event.SevInfo, subject).
				WithAttr("prefix", key).
				WithAttr("gateway", after.gw).
				WithAttr("ifindex", after.linkIndex).
				WithAttr("is_default", obs.IsDefault).
				WithDedup("l3.route_added|" + key),
		}

	case hadAny && !hasAny:
		delete(a.routes, key)
		// Losing the default route is losing everything beyond the local
		// segment, which is worth more than a notice.
		sev := event.SevNotice
		if obs.IsDefault {
			sev = event.SevError
		}
		return []*event.Event{
			a.b.New(event.SourceNetlink, event.KindRouteRemoved, sev, subject).
				WithAttr("prefix", key).
				WithAttr("gateway", before.gw).
				WithAttr("is_default", obs.IsDefault).
				WithDedup("l3.route_removed|" + key),
		}

	case hadAny && hasAny && before != after:
		kind, sev := event.KindRouteChanged, event.SevNotice
		if obs.IsDefault {
			// Where the default route points decides where all outbound
			// traffic goes. A silent change here is how traffic ends up
			// somewhere it should not be.
			kind, sev = event.KindDefaultRouteChanged, event.SevError
		}
		return []*event.Event{
			a.b.New(event.SourceNetlink, kind, sev, subject).
				WithAttr("prefix", key).
				WithAttr("gateway_old", before.gw).
				WithAttr("gateway_new", after.gw).
				WithAttr("ifindex_old", before.linkIndex).
				WithAttr("ifindex_new", after.linkIndex).
				WithAttr("is_default", obs.IsDefault).
				WithAttr("alternatives", len(a.routes[key])).
				WithEvidence("metric_old", before.priority).
				WithEvidence("metric_new", after.priority),
		}
	}

	// A standby route was installed or withdrawn without changing where the
	// traffic goes. Worth knowing eventually, not worth an event now.
	return nil
}

func entryOf(obs ports.RouteObservation) routeEntry {
	return routeEntry{gw: obs.Gateway, linkIndex: obs.LinkIndex, priority: obs.Priority}
}

// winner returns the entry that would actually carry traffic: the one with
// the lowest priority (metric).
func winner(entries []routeEntry) (routeEntry, bool) {
	if len(entries) == 0 {
		return routeEntry{}, false
	}
	best := entries[0]
	for _, e := range entries[1:] {
		if e.priority < best.priority {
			best = e
		}
	}
	return best, true
}

func upsertRoute(entries []routeEntry, e routeEntry) []routeEntry {
	for i, existing := range entries {
		if existing.id() == e.id() {
			entries[i] = e
			return entries
		}
	}
	return append(entries, e)
}

// supersedeRoute drops the entries a replace makes obsolete: matched by
// priority alone, not by where the route points, since re-pointing a prefix
// at a different next hop (or interface) at the same metric is precisely the
// common case a replace expresses.
func supersedeRoute(entries []routeEntry, e routeEntry) []routeEntry {
	out := entries[:0]
	for _, existing := range entries {
		if existing.priority != e.priority {
			out = append(out, existing)
		}
	}
	return out
}

func removeRoute(entries []routeEntry, e routeEntry) []routeEntry {
	out := entries[:0]
	for _, existing := range entries {
		if existing.id() != e.id() {
			out = append(out, existing)
		}
	}
	return out
}

// ObserveAddress turns one address-appeared-or-disappeared observation into
// an event. Stateless: unlike routes and neighbours, there is nothing to
// diff against - an address either arrived or left, and the notification
// already says which.
func ObserveAddress(b *event.Builder, obs ports.AddressObservation) *event.Event {
	kind, sev := event.KindAddrRemoved, event.SevNotice
	if !obs.Removed {
		kind, sev = event.KindAddrAdded, event.SevInfo
	}
	return b.New(event.SourceNetlink, kind, sev, event.Host(obs.IP, "")).
		WithAttr("address", obs.CIDR).
		WithAttr("ifindex", obs.LinkIndex).
		WithDedup(fmt.Sprintf("%s|%s", kind, obs.CIDR))
}
