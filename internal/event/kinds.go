package event

import "strings"

// Source names where an observation came from. It is recorded separately from
// Kind because the same kind of fact can arrive by different routes, and how we
// learned something bounds how much we should trust it.
type Source string

const (
	SourceNetlink   Source = "netlink"
	SourceEBPF      Source = "ebpf"
	SourceConntrack Source = "conntrack"
	SourceNftables  Source = "nftables"
	SourceDHCP      Source = "dhcp"
	SourceDNS       Source = "dns"
	SourceLLDP      Source = "lldp"
	SourceSNMP      Source = "snmp"
	SourceProbe     Source = "probe"
	SourceConfig    Source = "config"
	SourceInternal  Source = "internal"
)

// Severity is the recorder's own opinion of how much an event matters on its
// own. Correlation may raise or lower the severity of the incident it lands in.
type Severity string

const (
	SevInfo   Severity = "info"
	SevNotice Severity = "notice"
	SevWarn   Severity = "warn"
	SevError  Severity = "error"
)

// Kind is "family.action". The family determines which layer of the network the
// event describes; the action is the state change that occurred.
type Kind string

// Family returns the part before the dot, e.g. "l2" for "l2.arp_binding_changed".
func (k Kind) Family() string {
	if i := strings.IndexByte(string(k), '.'); i >= 0 {
		return string(k)[:i]
	}
	return string(k)
}

// link.* - layer 1. Sourced from netlink.
const (
	KindLinkUp         Kind = "link.up"
	KindLinkDown       Kind = "link.down"
	KindLinkFlap       Kind = "link.flap"
	KindLinkMTUChanged Kind = "link.mtu_changed"
	KindLinkErrorRate  Kind = "link.error_rate_high"
)

// l2.* - layer 2. Sourced from netlink neighbour tables, eBPF and LLDP.
const (
	KindARPBindingNew     Kind = "l2.arp_binding_new"
	KindARPBindingChanged Kind = "l2.arp_binding_changed"
	KindMACMoved          Kind = "l2.mac_moved"
	KindDuplicateIP       Kind = "l2.duplicate_ip"
	KindNeighborFailed    Kind = "l2.neighbor_failed"
	KindLLDPNeighborChged Kind = "l2.lldp_neighbor_changed"
	KindVLANSeen          Kind = "l2.vlan_seen"
)

// l3.* - layer 3. Sourced from netlink routes and ICMP.
const (
	KindRouteAdded          Kind = "l3.route_added"
	KindRouteRemoved        Kind = "l3.route_removed"
	KindRouteChanged        Kind = "l3.route_changed"
	KindDefaultRouteChanged Kind = "l3.default_route_changed"
	KindAddrAdded           Kind = "l3.addr_added"
	KindAddrRemoved         Kind = "l3.addr_removed"
	KindICMPUnreachable     Kind = "l3.icmp_unreachable"
	KindMTUBlackhole        Kind = "l3.mtu_blackhole"
)

// flow.* - layer 4, from eBPF.
//
// There is deliberately no flow.open or flow.close. Individual connections are
// not events: a busy segment opens thousands a second, and recording each would
// fill the store in a day while telling an operator nothing a counter could
// not. Ordinary activity becomes a flow.rollup; only the outcomes below earn a
// row of their own.
const (
	KindFlowReset           Kind = "flow.reset"
	KindFlowTimeoutNoClose  Kind = "flow.timeout_no_close"
	KindFlowRetransmitSpike Kind = "flow.retransmit_spike"
	KindFlowHandshakeFail   Kind = "flow.handshake_fail"
	// KindFlowFirstFailureForPair is the strongest single signal in the system:
	// two machines that were talking a moment ago can no longer connect, which
	// means something changed - a filtering rule, an ACL, a route, a service.
	KindFlowFirstFailureForPair Kind = "flow.first_failure_for_pair"
	KindFlowRollup              Kind = "flow.rollup"
)

// dhcp.* and dns.* - naming and addressing. Metadata only, never payloads.
const (
	KindDHCPOffer        Kind = "dhcp.offer"
	KindDHCPAck          Kind = "dhcp.ack"
	KindDHCPNak          Kind = "dhcp.nak"
	KindDHCPServerSeen   Kind = "dhcp.server_seen"
	KindDHCPLeaseChanged Kind = "dhcp.lease_changed"

	KindDNSQueryFail       Kind = "dns.query_fail"
	KindDNSResolverChanged Kind = "dns.resolver_changed"
	KindDNSLatencySpike    Kind = "dns.latency_spike"
)

// policy.* - the filtering rules in force.
//
// The *consequence* of a filtering change lives in flow.first_failure_for_pair,
// not here. A pair that stopped connecting is observable whatever did the
// blocking - a rule on this box, an ACL on a switch, a firewall three hops away
// - and an event that could only ever see the first of those would be the
// narrowest of the three dressed as the general case.
const (
	KindPolicyDropBurst   Kind = "policy.drop_burst"
	KindPolicyRuleChanged Kind = "policy.rule_changed"
)

// metric.* - measured series cross a threshold or depart from baseline.
const (
	KindMetricAnomaly Kind = "metric.anomaly"
)

// change.* - intended changes, fed in from NetIntent or from config diffing.
const (
	KindConfigApplied Kind = "change.config_applied"
	KindDeviceReboot  Kind = "change.device_reboot"
	KindAdminAction   Kind = "change.admin_action"
)

// system.* - the recorder reporting on itself. Without these the timeline has
// no forensic standing: a gap that is not recorded is indistinguishable from a
// period in which nothing happened.
const (
	KindSystemGap       Kind = "system.gap"
	KindSystemDrop      Kind = "system.drop"
	KindSystemClockStep Kind = "system.clock_step"
	KindSystemStart     Kind = "system.start"
	KindSystemStop      Kind = "system.stop"

	// KindCollectorDown says a source stopped feeding the record while the
	// recorder itself kept running. Without it, a collector that failed to
	// start leaves a timeline with a whole family of events missing and
	// nothing to say they were never being watched for - which reads exactly
	// like a network on which nothing of that kind happened.
	KindCollectorDown Kind = "system.collector_down"
)

// Families are the nine layers an event can describe, in the order they are
// documented. Enumerated rather than derived so a query tool can refuse a
// misspelled one: --family l4 quietly matching nothing is indistinguishable
// from a network on which nothing happened, which is the confusion this
// project exists to remove.
var Families = []string{
	"link", "l2", "l3", "flow", "dhcp", "dns", "policy", "metric", "change", "system",
}

// KnownFamily reports whether a family is one this schema defines.
func KnownFamily(name string) bool {
	for _, f := range Families {
		if f == name {
			return true
		}
	}
	return false
}
