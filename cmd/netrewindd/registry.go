package main

import "github.com/OmarAlghafri/netrewind/internal/registry"

// collectorDescriptors is the static, platform-honest description of every
// collector this daemon can build, independent of whether it can actually run
// on the machine it was started on.
//
// This is the "قدرة" table PRODUCT_RELEASE_PLAN_AR.md requires a desktop UI
// to show instead of a bare "supported" claim: what a collector needs, and
// which event kind families go missing if it is not watching. Name must match
// the corresponding collect.Collector.Name() exactly - a contract test in
// this package pins that so the two cannot drift apart silently.
var collectorDescriptors = []registry.Descriptor{
	{Name: "netlink.link", Platform: "linux", Privilege: "CAP_NET_ADMIN", Coverage: []string{"link.*"}},
	{Name: "netlink.neigh", Platform: "linux", Privilege: "CAP_NET_ADMIN", Coverage: []string{"l2.arp_binding_new", "l2.arp_binding_changed", "l2.mac_moved", "l2.duplicate_ip", "l2.neighbor_failed"}},
	{Name: "netlink.route", Platform: "linux", Privilege: "CAP_NET_ADMIN", Coverage: []string{"l3.route_added", "l3.route_removed", "l3.route_changed", "l3.default_route_changed"}},
	{Name: "netlink.addr", Platform: "linux", Privilege: "CAP_NET_ADMIN", Coverage: []string{"l3.addr_added", "l3.addr_removed"}},
	{Name: "ebpf.flow", Platform: "linux", Privilege: "CAP_BPF (or root) + kernel BTF", Coverage: []string{"flow.*"}},
	{Name: "policy.nftables", Platform: "linux", Privilege: "CAP_NET_ADMIN", Coverage: []string{"policy.rule_changed"}},
	{Name: "wire", Platform: "linux", Privilege: "CAP_NET_RAW", Coverage: []string{"dhcp.*", "dns.*", "l3.icmp_unreachable", "l3.mtu_blackhole"}},
	{Name: "probe.icmp", Platform: "linux", Privilege: "CAP_NET_RAW", Coverage: []string{"metric.anomaly"}},
	{Name: "update", Platform: "any", Privilege: "none (network egress only, and only if update.check is on)", Coverage: []string{"system.update_available", "system.updated", "system.update_failed"}},
}
