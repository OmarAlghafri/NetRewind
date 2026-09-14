package main

import "github.com/OmarAlghafri/netrewind/internal/registry"

// collectorDescriptors is the static, platform-honest description of every
// collector this daemon can build on any platform, independent of whether
// it can run on the machine it was started on. At startup every descriptor
// is registered; the ones this platform cannot run are marked unsupported
// so a capability report lists them with the reason, rather than omitting
// them and letting a reader conclude that kind of fact was watched for.
//
// This is the "قدرة" table PRODUCT_RELEASE_PLAN_AR.md requires a desktop UI
// to show instead of a bare "supported" claim: what a collector needs, and
// which event kind families go missing if it is not watching. Name must
// match the corresponding collect.Collector.Name() exactly - a contract
// test in this package pins that so the two cannot drift apart silently.
var collectorDescriptors = []registry.Descriptor{
	// Linux
	{Name: "netlink.link", Platform: "linux", Privilege: "CAP_NET_ADMIN", Coverage: []string{"link.*"}},
	{Name: "netlink.neigh", Platform: "linux", Privilege: "CAP_NET_ADMIN", Coverage: []string{"l2.arp_binding_new", "l2.arp_binding_changed", "l2.mac_moved", "l2.duplicate_ip", "l2.neighbor_failed"}},
	{Name: "netlink.route", Platform: "linux", Privilege: "CAP_NET_ADMIN", Coverage: []string{"l3.route_added", "l3.route_removed", "l3.route_changed", "l3.default_route_changed"}},
	{Name: "netlink.addr", Platform: "linux", Privilege: "CAP_NET_ADMIN", Coverage: []string{"l3.addr_added", "l3.addr_removed"}},
	{Name: "ebpf.flow", Platform: "linux", Privilege: "CAP_BPF (or root) + kernel BTF", Coverage: []string{"flow.*"}},
	{Name: "policy.nftables", Platform: "linux", Privilege: "CAP_NET_ADMIN", Coverage: []string{"policy.rule_changed"}},
	{Name: "wire", Platform: "linux", Privilege: "CAP_NET_RAW", Coverage: []string{"dhcp.*", "dns.*", "l3.icmp_unreachable", "l3.mtu_blackhole"}},
	{Name: "probe.icmp", Platform: "linux", Privilege: "CAP_NET_RAW", Coverage: []string{"metric.anomaly"}},
	// Windows: the same link/address/route/neighbour facts through the IP
	// Helper API. Reading the tables and subscribing to change notifications
	// needs no elevation; the service runs as LocalSystem only so the record
	// outlives the interactive session.
	{Name: "iphelper.link", Platform: "windows", Privilege: "none", Coverage: []string{"link.*"}},
	{Name: "iphelper.addr", Platform: "windows", Privilege: "none", Coverage: []string{"l3.addr_added", "l3.addr_removed", "l2.duplicate_ip (own address, via DAD)"}},
	{Name: "iphelper.route", Platform: "windows", Privilege: "none", Coverage: []string{"l3.route_added", "l3.route_removed", "l3.route_changed", "l3.default_route_changed"}},
	{Name: "iphelper.neigh", Platform: "windows", Privilege: "none (polled every 2s; a binding that changes and reverts within one poll is not seen)", Coverage: []string{"l2.arp_binding_new", "l2.arp_binding_changed", "l2.mac_moved", "l2.duplicate_ip", "l2.neighbor_failed"}},
	// Everywhere
	{Name: "update", Platform: "any", Privilege: "none (network egress only, and only if update.check is on)", Coverage: []string{"system.update_available", "system.updated", "system.update_failed"}},
}
