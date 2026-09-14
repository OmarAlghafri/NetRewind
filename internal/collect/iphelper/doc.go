// Package iphelper is the Windows source for interface, address, route and
// neighbour facts: the same observations internal/collect/netlink produces
// from the Linux kernel, read from the IP Helper API (iphlpapi) instead and
// handed to the identical decision logic in internal/analyze.
//
// The collectors here do exactly what the platform notifications allow and
// nothing they do not:
//
//   - Interfaces, addresses and routes are pushed by the kernel through
//     NotifyIpInterfaceChange, NotifyUnicastIpAddressChange and
//     NotifyRouteChange2. Every notification carries only the row's key
//     fields, so each collector re-reads the full row before deciding
//     anything; a row that is already gone by then is treated as removed,
//     which is what it is.
//   - The neighbour (ARP/ND) table has no change notification, so it is
//     polled and diffed on a short interval. A binding that moves between
//     two polls is seen as one change, not two - a limitation the capability
//     report states rather than hides.
//
// Nothing here changes anything on the network: every call is a read or a
// subscription.
package iphelper
