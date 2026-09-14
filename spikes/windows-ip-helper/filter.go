//go:build windows

package main

import "strings"

// shimSuffixes is the fixed, closed set of NDIS/WFP/Npcap/VirtualBox/Wi-Fi-
// filter/Hyper-V-extension shadow-row suffixes that GetIfTable2Ex actually
// appended to a parent interface's alias in this spike's real captured data
// (spikes/windows-ip-helper/run1-observation-25s.txt): every one of the 45
// non-base rows out of 66 total follows exactly "<parent alias>-<one of
// these>", never any other pattern. This is not a guess from documentation -
// docs/product/adr/0002-windows-spike-findings.md named this exact set of
// LWF drivers (Npcap, WFP, QoS Packet Scheduler, VirtualBox NDIS) as the
// source of the interface-row noise; this is that finding turned into a
// concrete, testable rule against the real table rather than left as prose.
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

// isShimInterface reports whether alias is a filter-driver shadow row rather
// than a real interface. Suffix matching, not substring, deliberately: a
// real adapter alias containing one of these vendor names as a substring in
// the middle (unlikely, but not impossible) must not be misclassified - only
// the exact "parent-name" + this literal suffix pattern this spike actually
// observed counts.
func isShimInterface(alias string) bool {
	for _, suffix := range shimSuffixes {
		if strings.HasSuffix(alias, suffix) {
			return true
		}
	}
	return false
}

// isNeverPresentScaffolding reports whether a (non-shim) row is permanent,
// always-absent OS scaffolding - the automatic IPv6 transition tunnel
// adapters (Teredo, 6to4, IP-HTTPS) and this machine's disabled Kernel
// Debugger NIC - rather than a real network path. In the real captured
// data these four rows, and only these four among the 21 non-shim rows,
// share MTU 0 with OperStatus "notPresent" - a structural signal, not a
// name match, so it does not need updating if Microsoft ever renames one of
// these adapters. Deliberately NOT applied to the four numbered
// "Local Area Connection* N" (3-6) tunnel-type (131) rows even though they
// share the same interface Type as Teredo/6to4/IP-HTTPS: those have real,
// distinct, non-zero MTUs (4091/1480/1460/1464) and their OperStatus is
// "down", not "notPresent" - there is no positive evidence here that they
// are noise rather than real (if currently down) paths, so this rule does
// not guess about them either way; see the "Open question" note in
// docs/product/adr/0002-windows-spike-findings.md.
func isNeverPresentScaffolding(mtu uint32, operStatusName string) bool {
	return mtu == 0 && operStatusName == "notPresent"
}

// isCandidateRealInterface combines both stages: a row survives only if it
// is neither a filter-driver shadow row nor permanently-absent scaffolding.
// "Candidate" because clearing this bar is necessary, not sufficient, for a
// future WindowsLinkAdapter to treat a row as one real observable network
// path - it is a noise filter, not a full identity/dedup design.
func isCandidateRealInterface(alias string, mtu uint32, operStatusName string) bool {
	if isShimInterface(alias) {
		return false
	}
	if isNeverPresentScaffolding(mtu, operStatusName) {
		return false
	}
	return true
}
