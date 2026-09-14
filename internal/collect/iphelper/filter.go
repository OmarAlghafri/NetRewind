package iphelper

import "strings"

// shimSuffixes name the NDIS lightweight-filter and packet-driver bindings
// Windows reports as interfaces in their own right, one per real adapter per
// installed filter driver. They have no addresses, no routes and no link
// state of their own; a change on the adapter underneath shows up on that
// adapter's own row. Reporting them would multiply every link event by the
// number of filter drivers installed.
var shimSuffixes = []string{
	"-WFP Native MAC Layer LightWeight Filter-0000",
	"-WFP 802.3 MAC Layer LightWeight Filter-0000",
	"-Npcap Packet Driver (NPCAP)-0000",
	"-QoS Packet Scheduler-0000",
	"-VirtualBox NDIS Light-Weight Filter-0000",
	"-Virtual WiFi Filter Driver-0000",
	"-Native WiFi Filter Driver-0000",
	"-Hyper-V Virtual Switch Extension Filter-0000",
	"-Virtual Filtering Platform VMSwitch Extension-0000",
}

// isShimInterface reports whether an interface description is one of the
// filter-driver bindings above.
func isShimInterface(description string) bool {
	for _, suffix := range shimSuffixes {
		if strings.HasSuffix(description, suffix) {
			return true
		}
	}
	return false
}

// isScaffolding reports the rows Windows keeps for adapters that are not
// present and never carry traffic (MTU 0 and operational status
// "not present"): tunnel and pseudo-interface placeholders that exist for
// the stack's own bookkeeping.
func isScaffolding(mtu uint32, operStatus uint32) bool {
	return mtu == 0 && operStatus == ifOperStatusNotPresent
}

// isLoopback reports the software loopback interface.
func isLoopback(ifType uint32) bool { return ifType == ifTypeSoftwareLoopback }

// IsObservable is the one rule deciding whether an interface row is worth
// recording: real adapters (physical, virtual, VPN, Wi-Fi), whether up or
// down, are; filter-driver shims, never-present scaffolding and the
// loopback are not.
func IsObservable(description string, ifType, mtu, operStatus uint32) bool {
	if isLoopback(ifType) || isShimInterface(description) || isScaffolding(mtu, operStatus) {
		return false
	}
	return true
}

// Values from ifdef.h / ipifcons.h, duplicated here so the filter is testable
// on every platform.
const (
	ifOperStatusNotPresent uint32 = 6
	ifTypeSoftwareLoopback uint32 = 24
)
