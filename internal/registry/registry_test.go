package registry

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestANewlyRegisteredCollectorStartsUnknown(t *testing.T) {
	r := New(nil)
	r.Register(Descriptor{Name: "netlink.link", Platform: "linux", Privilege: "CAP_NET_ADMIN", Coverage: []string{"link.*"}})

	snap := r.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("len(snap) = %d, want 1", len(snap))
	}
	if snap[0].Status != StatusUnknown {
		t.Errorf("status = %s, want %s before anything reports in", snap[0].Status, StatusUnknown)
	}
	if snap[0].Platform != "linux" || snap[0].Privilege != "CAP_NET_ADMIN" {
		t.Errorf("descriptor not preserved: %+v", snap[0].Descriptor)
	}
}

func TestUpAndDownTransitions(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	r := New(clock)
	r.Register(Descriptor{Name: "ebpf.flow"})

	r.Up("ebpf.flow")
	snap := r.Snapshot()[0]
	if snap.Status != StatusUp {
		t.Fatalf("status = %s, want up", snap.Status)
	}
	if !snap.LastSeen.Equal(now) {
		t.Errorf("last_seen = %v, want %v", snap.LastSeen, now)
	}
	if snap.Reason != "" {
		t.Errorf("reason = %q on a healthy collector, want empty", snap.Reason)
	}

	now = now.Add(5 * time.Minute)
	r.Down("ebpf.flow", "flow: attach sock/inet_sock_set_state: neither debugfs nor tracefs are mounted")
	snap = r.Snapshot()[0]
	if snap.Status != StatusDown {
		t.Fatalf("status = %s, want down", snap.Status)
	}
	if snap.Reason == "" {
		t.Error("a down collector must carry a reason, or an operator cannot act on it")
	}
	if !snap.LastChange.Equal(now) {
		t.Errorf("last_change = %v, want %v", snap.LastChange, now)
	}
	// last_seen is when it was last actually up, and must not be overwritten
	// by a status change that is not "up" - otherwise "last seen" would lie
	// about a collector that has been down for hours.
	wantLastSeen := now.Add(-5 * time.Minute)
	if !snap.LastSeen.Equal(wantLastSeen) {
		t.Errorf("last_seen = %v, want %v (the moment it was last up, not the down transition)", snap.LastSeen, wantLastSeen)
	}
}

func TestStatusForAnUnregisteredCollectorIsNotDropped(t *testing.T) {
	// This should not happen in a correctly wired daemon, but a status report
	// for a name nobody declared is a bug worth surfacing, not an event to
	// silently swallow - the alternative is a collector's failure vanishing
	// because of a typo in its name.
	r := New(nil)
	r.Down("some-typo", "boom")

	snap := r.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("len(snap) = %d, want 1 (the unregistered report should still appear)", len(snap))
	}
	if snap[0].Name != "some-typo" || snap[0].Status != StatusDown {
		t.Errorf("snapshot = %+v, want name=some-typo status=down", snap[0])
	}
}

func TestRegisteringTwiceResetsLiveState(t *testing.T) {
	r := New(nil)
	r.Register(Descriptor{Name: "wire"})
	r.Up("wire")
	if r.Snapshot()[0].Status != StatusUp {
		t.Fatal("setup: expected up before re-registering")
	}

	r.Register(Descriptor{Name: "wire", Platform: "linux"})
	snap := r.Snapshot()[0]
	if snap.Status != StatusUnknown {
		t.Errorf("status = %s after re-registration, want unknown - a re-declared collector has not reported in yet", snap.Status)
	}
	if snap.Platform != "linux" {
		t.Errorf("platform = %q, want the newly registered value", snap.Platform)
	}
}

func TestSnapshotOrderMatchesRegistrationOrder(t *testing.T) {
	r := New(nil)
	names := []string{"netlink.link", "netlink.neigh", "ebpf.flow", "wire", "probe.icmp"}
	for _, n := range names {
		r.Register(Descriptor{Name: n})
	}

	snap := r.Snapshot()
	if len(snap) != len(names) {
		t.Fatalf("len(snap) = %d, want %d", len(snap), len(names))
	}
	for i, n := range names {
		if snap[i].Name != n {
			t.Errorf("snap[%d].Name = %q, want %q - registration order must be stable for a UI to render consistently", i, snap[i].Name, n)
		}
	}
}

func TestSnapshotIsACopy(t *testing.T) {
	r := New(nil)
	r.Register(Descriptor{Name: "probe.icmp", Coverage: []string{"metric.anomaly"}})

	snap := r.Snapshot()
	snap[0].Coverage[0] = "tampered"
	snap[0].Name = "tampered"

	fresh := r.Snapshot()
	if fresh[0].Name != "probe.icmp" {
		t.Error("mutating a returned snapshot changed the registry's own state")
	}
}

// ADR 0004 §4.5: the Go capability reason becomes {code, params,
// technical_detail} - DownCoded/UnsupportedCoded are the coded path;
// Down/Unsupported (still used by a collector's own free-text startup
// error, which has no closed set of codes to belong to) must keep leaving
// the code empty, not some zero-value placeholder a GUI might mistake for
// a real one.
func TestUnsupportedCodedCarriesTheStructuredReason(t *testing.T) {
	r := New(nil)
	r.Register(Descriptor{Name: "ebpf.flow", Platform: "linux"})

	r.UnsupportedCoded("ebpf.flow", "requires_platform", map[string]string{"platform": "linux"}, "requires linux")

	snap := r.Snapshot()[0]
	if snap.Status != StatusUnsupported {
		t.Fatalf("status = %s, want unsupported", snap.Status)
	}
	if snap.Reason != "requires linux" {
		t.Errorf("reason = %q, want the plain-text fallback preserved unchanged", snap.Reason)
	}
	if snap.ReasonCode != "requires_platform" {
		t.Errorf("reason_code = %q, want requires_platform", snap.ReasonCode)
	}
	if snap.ReasonParams["platform"] != "linux" {
		t.Errorf("reason_params[platform] = %q, want linux", snap.ReasonParams["platform"])
	}
}

func TestDownCodedCarriesTheStructuredReason(t *testing.T) {
	r := New(nil)
	r.Register(Descriptor{Name: "netlink.link"})

	r.DownCoded("netlink.link", "collector_stopped", nil, "stopped")

	snap := r.Snapshot()[0]
	if snap.ReasonCode != "collector_stopped" {
		t.Errorf("reason_code = %q, want collector_stopped", snap.ReasonCode)
	}
}

// The plain, uncoded path (a collector's own free-text startup error has no
// closed set of codes to belong to) must leave ReasonCode empty, not a
// zero-value placeholder a GUI could mistake for a real code.
func TestPlainDownAndUnsupportedLeaveTheCodeEmpty(t *testing.T) {
	r := New(nil)
	r.Register(Descriptor{Name: "a"})
	r.Register(Descriptor{Name: "b"})

	r.Down("a", "nft: executable file not found")
	r.Unsupported("b", "requires windows")

	for _, snap := range r.Snapshot() {
		if snap.ReasonCode != "" {
			t.Errorf("%s: reason_code = %q, want empty for the uncoded path", snap.Name, snap.ReasonCode)
		}
		if snap.ReasonParams != nil {
			t.Errorf("%s: reason_params = %v, want nil for the uncoded path", snap.Name, snap.ReasonParams)
		}
	}
}

// json:"...,omitempty" must actually omit an empty code - present-but-empty
// would be indistinguishable from "the code is the empty string" to a
// naive reader, rather than "there is no code at all".
func TestUncodedReasonOmitsTheCodeFieldsFromJSON(t *testing.T) {
	r := New(nil)
	r.Register(Descriptor{Name: "a"})
	r.Down("a", "flapping")

	data, err := json.Marshal(r.Snapshot()[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), "reason_code") || strings.Contains(string(data), "reason_params") {
		t.Errorf("uncoded snapshot JSON has reason_code/reason_params present: %s", data)
	}
}

// desktop/src/i18n/capabilityReasonCatalogue.ts hand-keeps a matching
// literal list checked against ReasonCodesUsedByThisBuild - this proves
// this side's own list has no duplicate and no typo'd entry, so what the
// TS side is being kept in sync with is correct in the first place.
func TestReasonCodesUsedByThisBuildHasNoDuplicates(t *testing.T) {
	seen := make(map[string]bool, len(ReasonCodesUsedByThisBuild))
	for _, code := range ReasonCodesUsedByThisBuild {
		if code == "" {
			t.Error("ReasonCodesUsedByThisBuild has an empty entry")
		}
		if seen[code] {
			t.Errorf("%q appears more than once in ReasonCodesUsedByThisBuild", code)
		}
		seen[code] = true
	}
	// A change to this number is exactly the signal to go update
	// capabilityReasonCatalogue.test.ts's EXPECTED_CODES list too.
	if len(ReasonCodesUsedByThisBuild) != 2 {
		t.Errorf("len = %d, want 2 - a code was added or removed, update capabilityReasonCatalogue.test.ts too", len(ReasonCodesUsedByThisBuild))
	}
}

// Collectors report their own status from their own goroutines, so this must
// hold up under -race, not merely under a single-threaded test.
func TestConcurrentReportsAreSafe(t *testing.T) {
	r := New(nil)
	collectors := []string{"a", "b", "c", "d", "e"}
	for _, n := range collectors {
		r.Register(Descriptor{Name: n})
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		for _, n := range collectors {
			wg.Add(2)
			go func(n string) { defer wg.Done(); r.Up(n) }(n)
			go func(n string) { defer wg.Done(); r.Down(n, "flapping") }(n)
		}
		wg.Add(1)
		go func() { defer wg.Done(); r.Snapshot() }()
	}
	wg.Wait()

	if len(r.Snapshot()) != len(collectors) {
		t.Errorf("len(snapshot) = %d, want %d - concurrent access must not create duplicate or lost entries", len(r.Snapshot()), len(collectors))
	}
}
