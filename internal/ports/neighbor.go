package ports

// NeighborObservation is one neighbour-table entry: which hardware address
// currently answers for one IP address, on which interface.
//
// IsGateway is decided by the adapter, not this package or the analyzer that
// consumes it: finding the current default gateway needs a route-table read
// (netlink on Linux; something else on any future adapter), and the whole
// point of the ports split is that decision logic never reaches into a
// platform-specific table itself. An adapter that cannot cheaply answer
// "is this the gateway" should say false rather than guess - a missed
// gateway-hijack detection is a smaller failure than a wrong one.
type NeighborObservation struct {
	IP        string
	MAC       string
	LinkIndex int
	// Failed is true when the entry is gone, or the kernel could not resolve
	// it at all - the address no longer answers at layer 2, which is a
	// different fact from a fresh binding never having existed.
	Failed    bool
	IsGateway bool
	// NUDState is a human-readable neighbour-state string for evidence only
	// ("reachable", "stale", ...) - it is never decision input, only cited
	// on an event so a human can see what the kernel actually reported.
	NUDState string
}
