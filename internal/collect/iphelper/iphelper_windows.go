package iphelper

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync/atomic"
	"syscall"
	"unsafe"

	"github.com/OmarAlghafri/netrewind/internal/collect"
	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/ports"
	"golang.org/x/sys/windows"
)

// Values from ifdef.h that golang.org/x/sys/windows does not export.
const (
	netIfAdminStatusUp uint32 = 1
)

// notification is what a change callback hands from IP Helper's thread to
// the collector's goroutine: the type of change and the row's key fields,
// which are all a callback row is guaranteed to carry.
type notification struct {
	kind   uint32 // windows.MibAddInstance, MibDeleteInstance, MibParameterNotification
	luid   uint64
	index  uint32
	family uint16
	// Address (unicast address notifications) or route destination and next
	// hop (route notifications); nil otherwise.
	ip      net.IP
	prefix  string
	nextHop string
}

// subscription is one registered change callback and the channel it feeds.
// Callbacks arrive on IP Helper's own worker threads; they never block, so a
// consumer that falls behind loses notifications, and that loss is counted
// and recorded rather than silently absorbed.
type subscription struct {
	handle  windows.Handle
	ch      chan notification
	dropped atomic.Uint64
}

func (s *subscription) push(n notification) {
	select {
	case s.ch <- n:
	default:
		s.dropped.Add(1)
	}
}

func (s *subscription) close() {
	if s.handle != 0 {
		windows.CancelMibChangeNotify2(s.handle)
		s.handle = 0
	}
}

const subscriptionDepth = 1024

// subscribeInterfaces registers for interface (MIB_IPINTERFACE_ROW) changes
// on both address families through one AF_UNSPEC registration. The
// synthetic initial notification is requested and ignored: the collector
// seeds from the full table instead, which is complete rather than
// per-family.
func subscribeInterfaces() (*subscription, error) {
	s := &subscription{ch: make(chan notification, subscriptionDepth)}
	cb := syscall.NewCallback(func(_ unsafe.Pointer, row *windows.MibIpInterfaceRow, kind uint32) uintptr {
		if row == nil || kind == windows.MibInitialNotification {
			return 0
		}
		s.push(notification{kind: kind, luid: row.InterfaceLuid, index: row.InterfaceIndex, family: row.Family})
		return 0
	})
	if err := windows.NotifyIpInterfaceChange(windows.AF_UNSPEC, cb, nil, true, &s.handle); err != nil {
		return nil, fmt.Errorf("iphelper: NotifyIpInterfaceChange: %w", err)
	}
	return s, nil
}

func subscribeAddresses() (*subscription, error) {
	s := &subscription{ch: make(chan notification, subscriptionDepth)}
	cb := syscall.NewCallback(func(_ unsafe.Pointer, row *windows.MibUnicastIpAddressRow, kind uint32) uintptr {
		if row == nil || kind == windows.MibInitialNotification {
			return 0
		}
		s.push(notification{kind: kind, luid: row.InterfaceLuid, index: row.InterfaceIndex,
			family: row.Address.Family, ip: sockaddrIP(row.Address.Family, unsafe.Pointer(&row.Address))})
		return 0
	})
	if err := windows.NotifyUnicastIpAddressChange(windows.AF_UNSPEC, cb, nil, true, &s.handle); err != nil {
		return nil, fmt.Errorf("iphelper: NotifyUnicastIpAddressChange: %w", err)
	}
	return s, nil
}

func subscribeRoutes() (*subscription, error) {
	s := &subscription{ch: make(chan notification, subscriptionDepth)}
	cb := syscall.NewCallback(func(_ unsafe.Pointer, row *windows.MibIpForwardRow2, kind uint32) uintptr {
		if row == nil || kind == windows.MibInitialNotification {
			return 0
		}
		s.push(notification{kind: kind, luid: row.InterfaceLuid, index: row.InterfaceIndex,
			family: row.DestinationPrefix.Prefix.Family,
			prefix: prefixString(row.DestinationPrefix), nextHop: nextHopString(row.NextHop)})
		return 0
	})
	if err := windows.NotifyRouteChange2(windows.AF_UNSPEC, cb, nil, true, &s.handle); err != nil {
		return nil, fmt.Errorf("iphelper: NotifyRouteChange2: %w", err)
	}
	return s, nil
}

// reportDrops turns notifications lost to a full channel into the same
// system.drop event the netlink collectors record on a buffer overrun: a
// hole in the record has to be in the record.
func reportDrops(ctx context.Context, out chan<- *event.Event, b *event.Builder, log *slog.Logger, name string, s *subscription) bool {
	n := s.dropped.Swap(0)
	if n == 0 {
		return true
	}
	log.Warn("notifications were lost; the collector fell behind", "collector", name, "dropped", n)
	e := b.New(event.SourceInternal, event.KindSystemDrop, event.SevWarn, event.Observer(b.ObserverID)).
		WithAttr("dropped", n).
		WithAttr("source", name)
	return collect.Emit(ctx, out, e)
}

// sockaddrIP reads the address out of a SOCKADDR_INET union by its family.
func sockaddrIP(family uint16, ptr unsafe.Pointer) net.IP {
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
	}
	return nil
}

func prefixString(p windows.IpAddressPrefix) string {
	ip := sockaddrIP(p.Prefix.Family, unsafe.Pointer(&p.Prefix))
	if ip == nil {
		return ""
	}
	return fmt.Sprintf("%s/%d", ip, p.PrefixLength)
}

// nextHopString renders a route's next hop, empty for an on-link route
// (Windows reports 0.0.0.0 / :: there, which is "no gateway").
func nextHopString(s windows.RawSockaddrInet) string {
	ip := sockaddrIP(s.Family, unsafe.Pointer(&s))
	if ip == nil || ip.IsUnspecified() {
		return ""
	}
	return ip.String()
}

// interfaceRow re-reads the full MIB_IF_ROW2 for a LUID. ok is false when
// the interface no longer exists, which after a delete notification is the
// expected answer, not an error.
func interfaceRow(luid uint64) (row windows.MibIfRow2, ok bool, err error) {
	row.InterfaceLuid = luid
	err = windows.GetIfEntry2Ex(windows.MibIfEntryNormal, &row)
	switch {
	case err == nil:
		return row, true, nil
	case err == windows.ERROR_FILE_NOT_FOUND || err == windows.ERROR_NOT_FOUND:
		return row, false, nil
	}
	return row, false, err
}

func observableRow(row *windows.MibIfRow2) bool {
	alias := windows.UTF16ToString(row.Alias[:])
	description := windows.UTF16ToString(row.Description[:])
	return IsObservable(alias, row.Type, row.Mtu, row.OperStatus) && IsObservable(description, row.Type, row.Mtu, row.OperStatus)
}

// interfaceObservation is the platform-neutral view internal/analyze
// decides on. "Up" for the operational side means IfOperStatusUp; every
// other operational status (down, dormant, lower-layer-down, testing,
// not-present, unknown) is not carrying traffic, which is what matters.
func interfaceObservation(row *windows.MibIfRow2) ports.InterfaceObservation {
	return ports.InterfaceObservation{
		Index:   int(row.InterfaceIndex),
		Name:    windows.UTF16ToString(row.Alias[:]),
		AdminUp: row.AdminStatus == netIfAdminStatusUp,
		OperUp:  row.OperStatus == windows.IfOperStatusUp,
		MTU:     int(row.Mtu),
	}
}

func interfaceCounters(row *windows.MibIfRow2) ports.InterfaceCounters {
	return ports.InterfaceCounters{
		RxPackets: row.InUcastPkts + row.InNUcastPkts,
		TxPackets: row.OutUcastPkts + row.OutNUcastPkts,
		RxErrors:  row.InErrors,
		TxErrors:  row.OutErrors,
		RxDropped: row.InDiscards,
		TxDropped: row.OutDiscards,
	}
}

// listInterfaces returns every MIB_IF_ROW2 the stack has, observable or not.
func listInterfaces() ([]windows.MibIfRow2, error) {
	var table *windows.MibIfTable2
	if err := windows.GetIfTable2Ex(windows.MibIfEntryNormal, &table); err != nil {
		return nil, fmt.Errorf("iphelper: GetIfTable2Ex: %w", err)
	}
	defer windows.FreeMibTable(unsafe.Pointer(table))
	rows := unsafe.Slice(&table.Table[0], table.NumEntries)
	out := make([]windows.MibIfRow2, len(rows))
	copy(out, rows)
	return out, nil
}
