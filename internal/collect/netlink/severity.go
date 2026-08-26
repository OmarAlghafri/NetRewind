package netlink

import "github.com/OmarAlghafri/netrewind/internal/event"

// firstSightingSeverity decides how loudly to report an address appearing.
//
// A genuine first sighting is ordinary: a machine joined the segment. But an
// address the identity table has seen before, now answered by hardware that has
// never held it, only looks like a first sighting - it is a substitution that
// happened while the old binding was absent. Real routers produce exactly this
// when an address moves between them, because the old neighbour entry fails
// before the new one arrives. A GNS3 topology showed it; veth pairs never do,
// since there the old binding is still present and the change is reported as a
// change.
//
// On the default gateway it means every host on the segment is now sending its
// outbound traffic to a different machine. Reported at info, as it was, nobody
// filtering the record by severity would ever see it.
//
// The policy lives apart from the netlink plumbing so it can be tested without
// a kernel, and so the reasoning above sits next to the decision rather than
// buried in a diff of neighbour updates.
func firstSightingSeverity(changedHands, isGateway bool) event.Severity {
	switch {
	case changedHands && isGateway:
		return event.SevError
	case changedHands:
		return event.SevWarn
	default:
		return event.SevInfo
	}
}
