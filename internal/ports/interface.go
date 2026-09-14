// Package ports defines the observations a platform adapter must be able to
// report, independent of the mechanism (netlink, eBPF, Windows IP Helper,
// WFP, ...) that produced them.
//
// PRODUCT_RELEASE_PLAN_AR.md §4.1: "يترجم adapter النظام بياناته إلى
// observations مشتركة؛ المحلل الحتمي فقط يحولها إلى أحداث NetRewind." An
// adapter's only job is the translation into these types; everything that
// decides whether a change is worth an event, and what kind, lives in
// internal/analyze instead, where it can be tested without a kernel.
//
// These types describe what changed, in vocabulary from docs/schema.md - not
// how it was observed. A field only belongs here if a NetRewind event or
// analyzer decision actually depends on it today; this package tracks real
// collectors, not a guess at ones that do not exist yet (netlink is the only
// implementation as of this package's introduction - see internal/collect/netlink).
package ports

// InterfaceObservation is one interface's layer-1 state at a point in time.
//
// AdminUp and OperUp are kept separate deliberately: administrative state
// (someone ran "ip link set down", or a switch port was shut) and operational
// state (the carrier dropped) have completely different causes and
// completely different fixes. Collapsing them into one "is it up" bool is
// exactly what docs/schema.md's link.* family exists to stop doing.
type InterfaceObservation struct {
	// Index is the adapter's own stable identifier for the interface
	// (ifindex on Linux, an interface LUID on Windows). Adapters use it as
	// the map key that survives a rename; NetRewind events key on Name.
	Index int
	Name  string

	AdminUp bool
	OperUp  bool

	// MTU is 0 when the adapter could not read it, which callers must treat
	// as "unknown", not "changed to zero".
	MTU int

	// Removed is true when the interface no longer exists at all (deleted,
	// unplugged and torn down, a VLAN removed) - not merely down. It is its
	// own field rather than a sentinel state combination because "does this
	// interface still exist" is a different question from "is it carrying",
	// and conflating them was what made the original diff logic read the
	// deletion case as just another kind of down.
	Removed bool
}

// InterfaceCounters is the traffic counters worth watching for a link
// degrading silently - errors and drops against the traffic that produced
// them, sampled at a point in time. The timestamp is carried by the caller
// (internal/analyze.InterfaceAnalyzer), not this type, because a bare
// snapshot has no meaning without knowing when the previous one was taken.
type InterfaceCounters struct {
	RxPackets, TxPackets uint64
	RxErrors, TxErrors   uint64
	RxDropped, TxDropped uint64
}
