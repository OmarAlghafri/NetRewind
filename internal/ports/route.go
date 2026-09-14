package ports

// RouteObservation is one route entry, as reported by a single native
// notification - not the winner among several routes to the same
// destination, which is exactly the judgment the analyzer that consumes
// this makes for itself (see internal/analyze.RouteAnalyzer): a destination
// routinely has several routes at different priorities (failover, multipath),
// and collapsing them here would lose the "which one currently wins"
// question this whole family exists to answer.
type RouteObservation struct {
	// Prefix names the destination the way an operator would ask about it -
	// "default" for the default route, or the CIDR string otherwise. The
	// adapter decides this: recognising "this route covers everything" is a
	// native-table judgment call (netlink expresses it as a nil destination
	// or an explicit zero-length prefix; a future adapter may express it
	// differently), but the concept itself is universal.
	Prefix    string
	IsDefault bool
	Gateway   string
	LinkIndex int
	Priority  int
	// Removed is true when this specific entry (this Prefix at this
	// Priority) was withdrawn.
	Removed bool
	// Replace is true when this notification replaces whatever entry
	// already exists at the same Prefix+Priority, rather than adding a new
	// alternative alongside it. Netlink's NLM_F_REPLACE flag is exactly
	// this; a routing daemon recomputing a path replaces constantly, and
	// treating that as an addition would leave stale alternatives in place
	// forever.
	Replace bool
}

// AddressObservation is one interface address appearing or disappearing.
type AddressObservation struct {
	IP        string
	CIDR      string // the full address string, e.g. "192.168.1.5/24"
	LinkIndex int
	Removed   bool
}
