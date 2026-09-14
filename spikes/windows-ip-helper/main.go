//go:build windows

// Command windows-ip-helper is a throwaway spike, not a NetRewind collector.
//
// PRODUCT_RELEASE_PLAN_AR.md §5 Phase 5A: "spike معزول، لا تدمجه مبكراً" - an
// isolated spike, do not integrate it early. Its only job is to find out,
// empirically, on a real Windows machine, whether the IP Helper
// (NetIO/iphlpapi) change-notification APIs can plausibly stand in for what
// the Linux netlink collectors (internal/collect/netlink) get from the
// kernel for free: interface, address, and route change notifications with
// enough detail to run internal/analyze's diff logic against, including the
// admin-vs-carrier distinction internal/analyze/interface.go cares about.
//
// Safety: this program NEVER touches any real adapter, route, or IP
// configuration. It only calls the Notify*Change2 registration APIs and
// GetIfTable2Ex/GetUnicastIpAddressTable/GetIpForwardTable2 read-only
// queries, then listens for whatever notifications naturally arrive during a
// bounded observation window. It does not run netsh, does not call any
// "Set"/"Create"/"Delete" IP Helper function, and does not add or remove any
// address - including a loopback one - because this run was done on the
// operator's real working machine rather than the isolated VM the product
// plan itself reserves for active network-change testing (§5B: "تجرى
// تغييرات الشبكة فقط داخل VM معزولة ثم تستعاد snapshot"). See
// docs/product/adr/0002-windows-spike-findings.md for why that call was made
// and what it costs the spike's conclusiveness.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func main() {
	duration := flag.Duration("duration", 25*time.Second, "how long to observe for notifications")
	splitFamily := flag.Bool("split-family", false, "register each notification type twice, once for AF_INET and once for AF_INET6, instead of once for AF_UNSPEC (see ADR 0002's double-initial-notification anomaly: this tests the hypothesis that AF_UNSPEC internally registers per-family, each with its own synthetic initial callback)")
	flag.BoolVar(&requery, "requery", false, "on every real (non-initial) notification, fetch the full row by its key (GetIfEntry2Ex/GetIpInterfaceEntry/GetUnicastIpAddressEntry) and log the ACTUAL state - the row handed to a notification callback is documented to carry only key fields, so its Connected/DadState values are not meaningful on their own")
	flag.Parse()

	log.SetFlags(0)
	start := time.Now()
	logf("=== NetRewind Windows IP Helper spike ===")
	logf("start=%s duration=%s split-family=%v requery=%v pid=%d", start.Format(time.RFC3339Nano), *duration, *splitFamily, requery, syscall.Getpid())

	logf("")
	logf("--- seed: interfaces (GetIfTable2Ex) ---")
	seedInterfaces()

	logf("")
	logf("--- seed: unicast addresses (GetUnicastIpAddressTable) ---")
	seedAddresses()

	logf("")
	logf("--- seed: routes (GetIpForwardTable2) ---")
	seedRoutes()

	rec := newRecorder()

	logf("")
	logf("--- registering change notifications (split-family=%v) ---", *splitFamily)

	var handles []windows.Handle
	families := []uint16{windows.AF_UNSPEC}
	familyNames := []string{"AF_UNSPEC"}
	if *splitFamily {
		families = []uint16{windows.AF_INET, windows.AF_INET6}
		familyNames = []string{"AF_INET", "AF_INET6"}
	}

	for i, fam := range families {
		fname := familyNames[i]

		ifaceHandle, ifaceErr := registerIfaceNotify(rec, fam)
		if ifaceErr != nil {
			logf("NotifyIpInterfaceChange(%s): FAILED: %v", fname, ifaceErr)
		} else {
			logf("NotifyIpInterfaceChange(%s): registered ok, handle=%v", fname, ifaceHandle)
			handles = append(handles, ifaceHandle)
		}

		addrHandle, addrErr := registerAddrNotify(rec, fam)
		if addrErr != nil {
			logf("NotifyUnicastIpAddressChange(%s): FAILED: %v", fname, addrErr)
		} else {
			logf("NotifyUnicastIpAddressChange(%s): registered ok, handle=%v", fname, addrHandle)
			handles = append(handles, addrHandle)
		}

		routeHandle, routeErr := registerRouteNotify(rec, fam)
		if routeErr != nil {
			logf("NotifyRouteChange2(%s): FAILED: %v", fname, routeErr)
		} else {
			logf("NotifyRouteChange2(%s): registered ok, handle=%v", fname, routeHandle)
			handles = append(handles, routeHandle)
		}
	}

	defer func() {
		for _, h := range handles {
			if err := windows.CancelMibChangeNotify2(h); err != nil {
				logf("CancelMibChangeNotify2(%v): %v", h, err)
			}
		}
	}()

	logf("")
	logf("--- observing for %s (passive only - no network changes made by this program) ---", *duration)
	time.Sleep(*duration)

	logf("")
	logf("--- observation window elapsed at %s (elapsed=%s) ---", time.Now().Format(time.RFC3339Nano), time.Since(start))
	logf("total notifications received: %d", rec.count())
	for kind, n := range rec.countsByKind() {
		logf("  %-14s %d", kind, n)
	}
	logf("")
	logf("=== spike run complete ===")
}

func logf(format string, args ...any) {
	ts := time.Now().Format("15:04:05.000000")
	log.Printf("[%s] %s", ts, fmt.Sprintf(format, args...))
}

// recorder collects every notification callback invocation with a real
// timestamp. Callbacks arrive on IP Helper's own worker thread(s), not the
// main goroutine, so every access is mutex-guarded.
type recorder struct {
	mu     sync.Mutex
	events []string
	counts map[string]int
}

func newRecorder() *recorder {
	return &recorder{counts: make(map[string]int)}
}

func (r *recorder) record(kind, detail string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	line := fmt.Sprintf("%s: %s", kind, detail)
	r.events = append(r.events, line)
	r.counts[kind]++
	// Log immediately (not just at the end) so a run that is interrupted, or
	// whose final summary is never reached, still leaves a real record of
	// what fired and when.
	logf("NOTIFY %s", line)
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

func (r *recorder) countsByKind() map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]int, len(r.counts))
	for k, v := range r.counts {
		out[k] = v
	}
	return out
}

// notificationTypeName renders MIB_NOTIFICATION_TYPE for logging. See
// https://learn.microsoft.com/en-us/windows/win32/api/netioapi/ne-netioapi-mib_notification_type.
func notificationTypeName(t uint32) string {
	switch t {
	case windows.MibParameterNotification:
		return "ParameterChange"
	case windows.MibAddInstance:
		return "AddInstance"
	case windows.MibDeleteInstance:
		return "DeleteInstance"
	case windows.MibInitialNotification:
		return "InitialNotification"
	default:
		return fmt.Sprintf("unknown(%d)", t)
	}
}

// sockToIP reads an IPv4 or IPv6 address out of a SOCKADDR_INET union, given
// its address family and a pointer to the union. RawSockaddrInet4 and
// RawSockaddrInet6 both start with the same Family/Port header, which is what
// makes this reinterpretation via the family field, rather than needing a
// distinct struct per call site, valid.
func sockToIP(family uint16, ptr unsafe.Pointer) net.IP {
	switch family {
	case windows.AF_INET:
		s := (*windows.RawSockaddrInet4)(ptr)
		ip := make(net.IP, 4)
		copy(ip, s.Addr[:])
		return ip
	case windows.AF_INET6:
		s := (*windows.RawSockaddrInet6)(ptr)
		ip := make(net.IP, 16)
		copy(ip, s.Addr[:])
		return ip
	default:
		return nil
	}
}

func inetToIP(s windows.RawSockaddrInet6) net.IP {
	return sockToIP(s.Family, unsafe.Pointer(&s))
}

func inetToIPPlain(s windows.RawSockaddrInet) net.IP {
	return sockToIP(s.Family, unsafe.Pointer(&s))
}

func prefixToString(p windows.IpAddressPrefix) string {
	ip := sockToIP(p.Prefix.Family, unsafe.Pointer(&p.Prefix))
	if ip == nil {
		return fmt.Sprintf("<unknown-family:%d>/%d", p.Prefix.Family, p.PrefixLength)
	}
	return fmt.Sprintf("%s/%d", ip.String(), p.PrefixLength)
}

// adminStatusName renders IF_ADMIN_STATUS (MIB_IF_ROW2.AdminStatus / used by
// GetIfEntry2's AdminStatus too). See
// https://learn.microsoft.com/en-us/windows/win32/api/ifdef/ne-ifdef-net_if_admin_status.
func adminStatusName(v uint32) string {
	switch v {
	case 1:
		return "up"
	case 2:
		return "down"
	case 3:
		return "testing"
	default:
		return fmt.Sprintf("unknown(%d)", v)
	}
}

// operStatusName renders IF_OPER_STATUS.
func operStatusName(v uint32) string {
	switch v {
	case windows.IfOperStatusUp:
		return "up"
	case windows.IfOperStatusDown:
		return "down"
	case windows.IfOperStatusTesting:
		return "testing"
	case windows.IfOperStatusUnknown:
		return "unknown"
	case windows.IfOperStatusDormant:
		return "dormant"
	case windows.IfOperStatusNotPresent:
		return "notPresent"
	case windows.IfOperStatusLowerLayerDown:
		return "lowerLayerDown"
	default:
		return fmt.Sprintf("unknown(%d)", v)
	}
}

// mediaConnectStateName renders NDIS_MEDIA_CONNECT_STATE - this is the
// carrier signal, distinct from AdminStatus, that
// internal/analyze/interface.go's AdminUp/OperUp split is looking for a
// Windows equivalent of. See
// https://learn.microsoft.com/en-us/windows-hardware/drivers/ddi/ntddndis/ne-ntddndis-_ndis_media_connect_state.
func mediaConnectStateName(v uint32) string {
	switch v {
	case 0:
		return "unknown"
	case 1:
		return "connected"
	case 2:
		return "disconnected"
	default:
		return fmt.Sprintf("unknown(%d)", v)
	}
}

func seedInterfaces() {
	var table *windows.MibIfTable2
	if err := windows.GetIfTable2Ex(windows.MibIfTableNormal, &table); err != nil {
		logf("GetIfTable2Ex: FAILED: %v", err)
		return
	}
	defer windows.FreeMibTable(unsafe.Pointer(table))

	// MibIfTable2, unlike MibIpForwardTable2, has no Rows() helper in this
	// x/sys/windows version - reslice the table's trailing 1-element array
	// out to NumEntries by hand, the same technique Rows() uses internally.
	rows := unsafe.Slice(&table.Table[0], table.NumEntries)
	logf("interfaces: %d", len(rows))
	for _, r := range rows {
		alias := windows.UTF16ToString(r.Alias[:])
		logf("  ifIndex=%-4d luid=%d alias=%-35q mtu=%-6d adminStatus=%-8s operStatus=%-15s mediaConnectState=%-12s type=%d",
			r.InterfaceIndex, r.InterfaceLuid, alias, r.Mtu,
			adminStatusName(r.AdminStatus), operStatusName(r.OperStatus), mediaConnectStateName(r.MediaConnectState), r.Type)
	}
}

func seedAddresses() {
	var table *windows.MibUnicastIpAddressTable
	if err := windows.GetUnicastIpAddressTable(windows.AF_UNSPEC, &table); err != nil {
		logf("GetUnicastIpAddressTable: FAILED: %v", err)
		return
	}
	defer windows.FreeMibTable(unsafe.Pointer(table))

	rows := unsafe.Slice(&table.Table[0], table.NumEntries)
	logf("unicast addresses: %d", len(rows))
	for _, r := range rows {
		ip := inetToIP(r.Address)
		logf("  ifIndex=%-4d addr=%-40s prefixOrigin=%-2d suffixOrigin=%-2d dadState=%-2d validLifetimeSec=%d",
			r.InterfaceIndex, ipString(ip), r.PrefixOrigin, r.SuffixOrigin, r.DadState, r.ValidLifetime)
	}
}

func seedRoutes() {
	var table *windows.MibIpForwardTable2
	if err := windows.GetIpForwardTable2(windows.AF_UNSPEC, &table); err != nil {
		logf("GetIpForwardTable2: FAILED: %v", err)
		return
	}
	defer windows.FreeMibTable(unsafe.Pointer(table))

	rows := table.Rows()
	logf("routes: %d", len(rows))
	for _, r := range rows {
		dst := prefixToString(r.DestinationPrefix)
		nh := inetToIPPlain(r.NextHop)
		logf("  ifIndex=%-4d dest=%-30s nextHop=%-20s metric=%-4d protocol=%-3d loopback=%v",
			r.InterfaceIndex, dst, ipString(nh), r.Metric, r.Protocol, r.Loopback != 0)
	}
}

func ipString(ip net.IP) string {
	if ip == nil {
		return "<none>"
	}
	return ip.String()
}

// registerIfaceNotify registers for MIB_IPINTERFACE_ROW change notifications
// (interface-level: MTU, forwarding, connected state, per address family).
// initialNotification=true is deliberate: Microsoft's documented behaviour is
// that the callback fires once immediately with Row=nil and
// NotificationType=MibInitialNotification purely to prove the registration
// itself is alive, before any real change ever happens. That synthetic first
// call is exactly the passive, non-mutating way this spike proves the
// callback mechanism works even if zero real changes occur in the
// observation window.
func registerIfaceNotify(rec *recorder, family uint16) (windows.Handle, error) {
	cb := syscall.NewCallback(func(callerContext unsafe.Pointer, row *windows.MibIpInterfaceRow, notificationType uint32) uintptr {
		if row == nil {
			rec.record("iface", fmt.Sprintf("reqFamily=%d type=%s row=nil", family, notificationTypeName(notificationType)))
			return 0
		}
		rec.record("iface", fmt.Sprintf("reqFamily=%d type=%s family=%d ifIndex=%d luid=%d connected=%v",
			family, notificationTypeName(notificationType), row.Family, row.InterfaceIndex, row.InterfaceLuid, row.Connected != 0))
		if requery && notificationType != windows.MibInitialNotification {
			requeryInterface(row.Family, row.InterfaceLuid, row.InterfaceIndex)
		}
		return 0
	})

	var handle windows.Handle
	err := windows.NotifyIpInterfaceChange(family, cb, nil, true, &handle)
	return handle, err
}

// registerAddrNotify registers for MIB_UNICASTIPADDRESS_ROW change
// notifications (an address being added, removed, or changing DAD/lifetime
// state on some interface).
func registerAddrNotify(rec *recorder, family uint16) (windows.Handle, error) {
	cb := syscall.NewCallback(func(callerContext unsafe.Pointer, row *windows.MibUnicastIpAddressRow, notificationType uint32) uintptr {
		if row == nil {
			rec.record("addr", fmt.Sprintf("reqFamily=%d type=%s row=nil", family, notificationTypeName(notificationType)))
			return 0
		}
		ip := inetToIP(row.Address)
		rec.record("addr", fmt.Sprintf("reqFamily=%d type=%s ifIndex=%d addr=%s dadState=%d",
			family, notificationTypeName(notificationType), row.InterfaceIndex, ipString(ip), row.DadState))
		if requery && notificationType != windows.MibInitialNotification {
			requeryAddress(row)
		}
		return 0
	})

	var handle windows.Handle
	err := windows.NotifyUnicastIpAddressChange(family, cb, nil, true, &handle)
	return handle, err
}

// registerRouteNotify registers for MIB_IPFORWARD_ROW2 change notifications
// (a route being added, removed, or changed in the forwarding table - any
// table, not just the "main" one netlink's RouteCollector filters to; this
// spike does not attempt that filtering, it just reports what arrives).
func registerRouteNotify(rec *recorder, family uint16) (windows.Handle, error) {
	cb := syscall.NewCallback(func(callerContext unsafe.Pointer, row *windows.MibIpForwardRow2, notificationType uint32) uintptr {
		if row == nil {
			rec.record("route", fmt.Sprintf("reqFamily=%d type=%s row=nil", family, notificationTypeName(notificationType)))
			return 0
		}
		dst := prefixToString(row.DestinationPrefix)
		nh := inetToIPPlain(row.NextHop)
		rec.record("route", fmt.Sprintf("reqFamily=%d type=%s ifIndex=%d dest=%s nextHop=%s metric=%d",
			family, notificationTypeName(notificationType), row.InterfaceIndex, dst, ipString(nh), row.Metric))
		return 0
	})

	var handle windows.Handle
	err := windows.NotifyRouteChange2(family, cb, nil, true, &handle)
	return handle, err
}

// requery is set by the -requery flag. Microsoft documents that the Row a
// change callback receives is a key, not a snapshot: NotifyIpInterfaceChange
// fills only Family/InterfaceLuid/InterfaceIndex, NotifyUnicastIpAddressChange
// only Address/InterfaceLuid/InterfaceIndex. Everything else in the struct is
// zero, which is why every notification above prints connected=false and
// dadState=0 regardless of reality. A real adapter therefore has to fetch the
// row itself, by that key, before it can say what changed - these two helpers
// do exactly that so the spike can show the actual before/after state.
var requery bool

func requeryInterface(family uint16, luid uint64, ifIndex uint32) {
	var ipRow windows.MibIpInterfaceRow
	ipRow.Family = family
	ipRow.InterfaceLuid = luid
	ipRow.InterfaceIndex = ifIndex
	if err := windows.GetIpInterfaceEntry(&ipRow); err != nil {
		logf("  requery GetIpInterfaceEntry(family=%d luid=%d): %v", family, luid, err)
	} else {
		logf("  requery GetIpInterfaceEntry: family=%d ifIndex=%d connected=%v metric=%d mtu=%d",
			ipRow.Family, ipRow.InterfaceIndex, ipRow.Connected != 0, ipRow.Metric, ipRow.NlMtu)
	}
	var ifRow windows.MibIfRow2
	ifRow.InterfaceLuid = luid
	if err := windows.GetIfEntry2Ex(windows.MibIfEntryNormal, &ifRow); err != nil {
		logf("  requery GetIfEntry2Ex(luid=%d): %v", luid, err)
		return
	}
	logf("  requery GetIfEntry2Ex: ifIndex=%d alias=%q adminStatus=%s operStatus=%s mediaConnectState=%s",
		ifRow.InterfaceIndex, windows.UTF16ToString(ifRow.Alias[:]),
		adminStatusName(ifRow.AdminStatus), operStatusName(ifRow.OperStatus), mediaConnectStateName(ifRow.MediaConnectState))
}

func requeryAddress(key *windows.MibUnicastIpAddressRow) {
	var row windows.MibUnicastIpAddressRow
	row.Address = key.Address
	row.InterfaceLuid = key.InterfaceLuid
	row.InterfaceIndex = key.InterfaceIndex
	if err := windows.GetUnicastIpAddressEntry(&row); err != nil {
		logf("  requery GetUnicastIpAddressEntry(%s): %v", ipString(inetToIP(key.Address)), err)
		return
	}
	logf("  requery GetUnicastIpAddressEntry: addr=%s dadState=%s prefixOrigin=%d suffixOrigin=%d validLifetime=%d",
		ipString(inetToIP(row.Address)), dadStateName(row.DadState), row.PrefixOrigin, row.SuffixOrigin, row.ValidLifetime)
}

// dadStateName renders NL_DAD_STATE (MIB_UNICASTIPADDRESS_ROW.DadState).
func dadStateName(v uint32) string {
	switch v {
	case 0:
		return "Invalid"
	case 1:
		return "Tentative"
	case 2:
		return "Duplicate"
	case 3:
		return "Deprecated"
	case 4:
		return "Preferred"
	}
	return fmt.Sprintf("unknown(%d)", v)
}
