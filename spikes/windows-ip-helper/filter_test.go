//go:build windows

package main

import "testing"

// realInterfaceRow is one row transcribed verbatim (alias, mtu, decoded
// OperStatus) from spikes/windows-ip-helper/run1-observation-25s.txt's
// interface seed section - the real GetIfTable2Ex output captured on this
// machine, not synthetic examples. wantReal is this test's own judgement
// call, documented inline per row group, not derived from the code under
// test - the point of this table is to catch the filter disagreeing with
// that judgement, not to restate the implementation.
type realInterfaceRow struct {
	ifIndex    int
	alias      string
	mtu        uint32
	operStatus string
	wantReal   bool
	wantReason string
}

// allSixtySixRealRows is the complete, real interface table from
// run1-observation-25s.txt lines 6-71 (the file's own "interfaces: 66"
// count). Kept as one literal table, not trimmed to "interesting" rows,
// so this test proves the filter's total kept/dropped count against the
// actual noise ratio ADR 0002 described, not a cherry-picked sample.
var allSixtySixRealRows = []realInterfaceRow{
	// --- 26 shim rows shadowing LAC*8/9/10, Ethernet, GNS3-Loopback,
	// Bluetooth, vSwitch(WSL), vEthernet(WSL) ---
	{26, "Local Area Connection* 8-WFP Native MAC Layer LightWeight Filter-0000", 1500, "up", false, "LWF shadow row"},
	{50, "Local Area Connection* 8-Npcap Packet Driver (NPCAP)-0000", 1500, "up", false, "Npcap shadow row"},
	{51, "Local Area Connection* 8-QoS Packet Scheduler-0000", 1500, "up", false, "QoS shadow row"},
	{52, "Local Area Connection* 9-WFP Native MAC Layer LightWeight Filter-0000", 1500, "up", false, "LWF shadow row"},
	{53, "Local Area Connection* 9-Npcap Packet Driver (NPCAP)-0000", 1500, "up", false, "Npcap shadow row"},
	{54, "Local Area Connection* 9-QoS Packet Scheduler-0000", 1500, "up", false, "QoS shadow row"},
	{55, "Local Area Connection* 10-WFP Native MAC Layer LightWeight Filter-0000", 1500, "up", false, "LWF shadow row"},
	{29, "Ethernet-WFP Native MAC Layer LightWeight Filter-0000", 1500, "up", false, "LWF shadow row"},
	{30, "GNS3-Loopback-WFP Native MAC Layer LightWeight Filter-0000", 1500, "up", false, "LWF shadow row"},
	{31, "Ethernet-Npcap Packet Driver (NPCAP)-0000", 1500, "up", false, "Npcap shadow row"},
	{32, "GNS3-Loopback-Npcap Packet Driver (NPCAP)-0000", 1500, "up", false, "Npcap shadow row"},
	{33, "GNS3-Loopback-VirtualBox NDIS Light-Weight Filter-0000", 1500, "up", false, "VirtualBox shadow row"},
	{34, "Ethernet-QoS Packet Scheduler-0000", 1500, "up", false, "QoS shadow row"},
	{35, "GNS3-Loopback-QoS Packet Scheduler-0000", 1500, "up", false, "QoS shadow row"},
	{36, "Ethernet-WFP 802.3 MAC Layer LightWeight Filter-0000", 1500, "up", false, "LWF shadow row"},
	{37, "GNS3-Loopback-WFP 802.3 MAC Layer LightWeight Filter-0000", 1500, "up", false, "LWF shadow row"},
	{38, "Bluetooth Network Connection 2-Npcap Packet Driver (NPCAP)-0000", 1500, "down", false, "Npcap shadow row"},
	{56, "Local Area Connection* 10-Npcap Packet Driver (NPCAP)-0000", 1500, "up", false, "Npcap shadow row"},
	{57, "Local Area Connection* 10-QoS Packet Scheduler-0000", 1500, "up", false, "QoS shadow row"},
	{59, "vSwitch (WSL (Hyper-V firewall))-Hyper-V Virtual Switch Extension Filter-0000", 1500, "up", false, "Hyper-V extension shadow row"},
	{60, "vSwitch (WSL (Hyper-V firewall))-Virtual Filtering Platform VMSwitch Extension-0000", 1500, "up", false, "VMSwitch extension shadow row"},
	{62, "vEthernet (WSL (Hyper-V firewall))-WFP Native MAC Layer LightWeight Filter-0000", 1500, "up", false, "LWF shadow row"},
	{63, "vEthernet (WSL (Hyper-V firewall))-Npcap Packet Driver (NPCAP)-0000", 1500, "up", false, "Npcap shadow row"},
	{64, "vEthernet (WSL (Hyper-V firewall))-VirtualBox NDIS Light-Weight Filter-0000", 1500, "up", false, "VirtualBox shadow row"},
	{65, "vEthernet (WSL (Hyper-V firewall))-QoS Packet Scheduler-0000", 1500, "up", false, "QoS shadow row"},
	{66, "vEthernet (WSL (Hyper-V firewall))-WFP 802.3 MAC Layer LightWeight Filter-0000", 1500, "up", false, "LWF shadow row"},

	// --- 11 base rows (the parents of the shims above, plus 3 with no
	// shim shadow at all: Kernel Debugger, LAC*7, loopback) ---
	{2, "Ethernet (Kernel Debugger)", 0, "notPresent", false, "permanently absent debug NIC, MTU 0 + notPresent"},
	{12, "Local Area Connection* 8", 1500, "up", true, "real base adapter"},
	{17, "Local Area Connection* 9", 1500, "up", true, "real base adapter"},
	{7, "Local Area Connection* 10", 1500, "up", true, "real base adapter"},
	{4, "GNS3-Loopback", 1500, "up", true, "real base adapter (GNS3 virtual NIC)"},
	{10, "Bluetooth Network Connection 2", 1500, "down", true, "real base adapter, carrier down (no paired device)"},
	{58, "vSwitch (WSL (Hyper-V firewall))", 1500, "up", true, "real base adapter"},
	{9, "Ethernet", 1500, "up", true, "real base adapter"},
	{61, "vEthernet (WSL (Hyper-V firewall))", 1500, "up", true, "real base adapter"},
	{21, "Local Area Connection* 7", 1494, "down", true, "real base adapter, no shim shadow observed"},
	{1, "Loopback Pseudo-Interface 1", 1500, "up", true, "the one real software loopback"},

	// --- 19 shim rows shadowing Wi-Fi, LAC*11, LAC*12 ---
	{39, "Wi-Fi-WFP Native MAC Layer LightWeight Filter-0000", 1500, "up", false, "LWF shadow row"},
	{40, "Wi-Fi-Virtual WiFi Filter Driver-0000", 1500, "up", false, "Virtual WiFi filter shadow row"},
	{41, "Wi-Fi-Native WiFi Filter Driver-0000", 1500, "up", false, "Native WiFi filter shadow row"},
	{42, "Wi-Fi-Npcap Packet Driver (NPCAP)-0000", 1500, "up", false, "Npcap shadow row"},
	{43, "Wi-Fi-VirtualBox NDIS Light-Weight Filter-0000", 1500, "up", false, "VirtualBox shadow row"},
	{44, "Wi-Fi-QoS Packet Scheduler-0000", 1500, "up", false, "QoS shadow row"},
	{45, "Wi-Fi-WFP 802.3 MAC Layer LightWeight Filter-0000", 1500, "up", false, "LWF shadow row"},
	{27, "Local Area Connection* 11-WFP Native MAC Layer LightWeight Filter-0000", 1500, "down", false, "LWF shadow row"},
	{28, "Local Area Connection* 11-Native WiFi Filter Driver-0000", 1500, "down", false, "Native WiFi filter shadow row"},
	{46, "Local Area Connection* 11-Npcap Packet Driver (NPCAP)-0000", 1500, "down", false, "Npcap shadow row"},
	{47, "Local Area Connection* 11-VirtualBox NDIS Light-Weight Filter-0000", 1500, "down", false, "VirtualBox shadow row"},
	{48, "Local Area Connection* 11-QoS Packet Scheduler-0000", 1500, "down", false, "QoS shadow row"},
	{49, "Local Area Connection* 11-WFP 802.3 MAC Layer LightWeight Filter-0000", 1500, "down", false, "LWF shadow row"},
	{14, "Local Area Connection* 12-WFP Native MAC Layer LightWeight Filter-0000", 1500, "down", false, "LWF shadow row"},
	{16, "Local Area Connection* 12-Native WiFi Filter Driver-0000", 1500, "down", false, "Native WiFi filter shadow row"},
	{22, "Local Area Connection* 12-Npcap Packet Driver (NPCAP)-0000", 1500, "down", false, "Npcap shadow row"},
	{23, "Local Area Connection* 12-VirtualBox NDIS Light-Weight Filter-0000", 1500, "down", false, "VirtualBox shadow row"},
	{24, "Local Area Connection* 12-QoS Packet Scheduler-0000", 1500, "down", false, "QoS shadow row"},
	{25, "Local Area Connection* 12-WFP 802.3 MAC Layer LightWeight Filter-0000", 1500, "down", false, "LWF shadow row"},

	// --- 10 remaining base rows ---
	{8, "Wi-Fi", 1500, "up", true, "real base adapter"},
	{6, "Local Area Connection* 11", 1500, "down", true, "real base adapter, currently down"},
	{19, "Local Area Connection* 12", 1500, "down", true, "real base adapter, currently down"},
	{13, "Teredo Tunneling Pseudo-Interface", 0, "notPresent", false, "permanent IPv6 transition scaffolding, MTU 0 + notPresent"},
	{5, "Microsoft IP-HTTPS Platform Interface", 0, "notPresent", false, "permanent IPv6 transition scaffolding, MTU 0 + notPresent"},
	{3, "6to4 Adapter", 0, "notPresent", false, "permanent IPv6 transition scaffolding, MTU 0 + notPresent"},
	{15, "Local Area Connection* 3", 4091, "down", true, "real, distinct non-zero MTU - no evidence this is noise"},
	{20, "Local Area Connection* 4", 1480, "down", true, "real, distinct non-zero MTU - no evidence this is noise"},
	{18, "Local Area Connection* 5", 1460, "down", true, "real, distinct non-zero MTU - no evidence this is noise"},
	{11, "Local Area Connection* 6", 1464, "down", true, "real, distinct non-zero MTU - no evidence this is noise"},
}

func TestFilterAgainstRealCapturedSixtySixRows(t *testing.T) {
	if len(allSixtySixRealRows) != 66 {
		t.Fatalf("test fixture has %d rows, want 66 (run1-observation-25s.txt's own reported count) - the fixture itself was mistranscribed", len(allSixtySixRealRows))
	}

	kept, dropped := 0, 0
	for _, row := range allSixtySixRealRows {
		got := isCandidateRealInterface(row.alias, row.mtu, row.operStatus)
		if got != row.wantReal {
			t.Errorf("ifIndex=%d alias=%q: isCandidateRealInterface=%v, want %v (%s)",
				row.ifIndex, row.alias, got, row.wantReal, row.wantReason)
		}
		if got {
			kept++
		} else {
			dropped++
		}
	}

	// This is the real, measured result, not asserted from the ADR's own
	// prose estimate of "~9 real paths" - that estimate under-counted the
	// "Local Area Connection* N" family (there are six distinct numbered
	// ones - 7 through 12 - not three, once shims are correctly excluded).
	// 17 kept is the honest number this filter produces against the real
	// table: 45 shim rows + 4 permanently-absent-scaffolding rows dropped
	// (49 total), 17 kept.
	if kept != 17 {
		t.Errorf("kept %d candidate real interfaces, want 17 (see docs/evidence/18-windows-notification-family-split.log and ADR 0002's update for the reconciliation with the earlier ~9 estimate)", kept)
	}
	if dropped != 49 {
		t.Errorf("dropped %d rows, want 49 (45 shim + 4 never-present scaffolding)", dropped)
	}
}

func TestIsShimInterfaceDoesNotFalsePositiveOnRealAliases(t *testing.T) {
	realAliases := []string{
		"Wi-Fi", "Ethernet", "Local Area Connection* 8", "GNS3-Loopback",
		"Bluetooth Network Connection 2", "Loopback Pseudo-Interface 1",
		"vSwitch (WSL (Hyper-V firewall))", "vEthernet (WSL (Hyper-V firewall))",
	}
	for _, alias := range realAliases {
		if isShimInterface(alias) {
			t.Errorf("isShimInterface(%q) = true, want false - this is a real base adapter alias with no shim suffix", alias)
		}
	}
}

func TestIsNeverPresentScaffoldingRequiresBothSignals(t *testing.T) {
	// A real adapter with a normal MTU that happens to be down must not be
	// caught by this rule - only true, permanent absence (MTU 0 AND
	// notPresent together) qualifies. This is the exact distinction that
	// keeps "Local Area Connection* 3" (MTU 4091, down) real while dropping
	// Teredo (MTU 0, notPresent).
	if isNeverPresentScaffolding(4091, "down") {
		t.Error("a real adapter with a non-zero MTU that is merely down must not be classified as never-present scaffolding")
	}
	if isNeverPresentScaffolding(0, "down") {
		t.Error("MTU 0 alone (without notPresent) must not be classified as never-present scaffolding")
	}
	if !isNeverPresentScaffolding(0, "notPresent") {
		t.Error("MTU 0 together with notPresent is exactly the Teredo/6to4/IP-HTTPS/Kernel-Debugger signature and must be classified as never-present scaffolding")
	}
}
