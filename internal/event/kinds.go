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

// flow.* - layer 4. Sourced from conntrack and eBPF. Only anomalous flows are
// recorded individually; everything else is rolled up.
const (
	KindFlowOpen            Kind = "flow.open"
	KindFlowClose           Kind = "flow.close"
	KindFlowReset           Kind = "flow.reset"
	KindFlowTimeoutNoClose  Kind = "flow.timeout_no_close"
	KindFlowRetransmitSpike Kind = "flow.retransmit_spike"
	KindFlowHandshakeFail   Kind = "flow.handshake_fail"
	KindFlowRollup          Kind = "flow.rollup"
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

// policy.* - filtering decisions.
const (
	KindPolicyDropBurst        Kind = "policy.drop_burst"
	KindPolicyRuleChanged      Kind = "policy.rule_changed"
	KindPolicyFirstDropForPair Kind = "policy.first_drop_for_pair"
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
)
