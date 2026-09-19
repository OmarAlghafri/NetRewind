package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRedactAddressesReplacesEveryRFC1918Range(t *testing.T) {
	in := "hosts: 10.99.0.11, 172.16.0.5, 172.31.255.254, 192.168.1.1"
	got := RedactAddresses(in)
	for _, real := range []string{"10.99.0.11", "172.16.0.5", "172.31.255.254", "192.168.1.1"} {
		if strings.Contains(got, real) {
			t.Errorf("RedactAddresses(%q) = %q, still contains real address %q", in, got, real)
		}
	}
}

// TestRedactAddressesLeavesAdjacentRangesAlone proves the 172.16-31 bound
// is exact: 172.15.x.x and 172.32.x.x are real addresses just outside
// RFC 1918's 172.16.0.0/12, and a private-range pattern that is even one
// octet too loose would over-redact (or, the opposite mistake, a too-tight
// one would miss a real private address).
func TestRedactAddressesLeavesAdjacentRangesAlone(t *testing.T) {
	in := "172.15.0.1 and 172.32.0.1 are not RFC 1918"
	got := RedactAddresses(in)
	if got != in {
		t.Errorf("RedactAddresses(%q) = %q, want unchanged (both addresses are outside 172.16.0.0/12)", in, got)
	}
}

func TestRedactAddressesLeavesPublicAddressesAlone(t *testing.T) {
	in := "resolver at 8.8.8.8 and 1.1.1.1"
	got := RedactAddresses(in)
	if got != in {
		t.Errorf("RedactAddresses(%q) = %q, want unchanged (both are public addresses)", in, got)
	}
}

func TestRedactAddressesGivesTheSameRealValueTheSamePlaceholder(t *testing.T) {
	in := "10.99.0.11 changed, then 10.99.0.11 changed again, then 10.99.1.11 appeared"
	got := RedactAddresses(in)
	want := "<HOST_1> changed, then <HOST_1> changed again, then <HOST_2> appeared"
	if got != want {
		t.Errorf("RedactAddresses(%q) = %q, want %q", in, got, want)
	}
}

func TestRedactAddressesHandlesMacAddressesSeparatelyFromHosts(t *testing.T) {
	in := "ip 10.99.0.11 mac 02:00:00:00:00:aa mac 02:00:00:00:00:aa mac 02:00:00:00:00:bb"
	got := RedactAddresses(in)
	want := "ip <HOST_1> mac <MAC_1> mac <MAC_1> mac <MAC_2>"
	if got != want {
		t.Errorf("RedactAddresses(%q) = %q, want %q", in, got, want)
	}
}

// TestRedactAddressesOnARealEvent runs against real, unmodified events from
// the checked-in corpus (not a synthetic string) - the actual data this
// function will process for real. arp_change's events are known (read
// directly) to carry both subject.attrs.ip and subject.attrs.mac.
func TestRedactAddressesOnARealEvent(t *testing.T) {
	events := loadRealScenarioEvents(t, "arp_change")
	if len(events) == 0 {
		t.Fatal("no events loaded from the real arp_change scenario")
	}
	data, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	redacted := RedactAddresses(string(data))

	sawIP, sawMAC := false, false
	for _, e := range events {
		subject, ok := e["subject"].(map[string]any)
		if !ok {
			continue
		}
		attrs, ok := subject["attrs"].(map[string]any)
		if !ok {
			continue
		}
		if ip, ok := attrs["ip"].(string); ok && ip != "" {
			sawIP = true
			if strings.Contains(redacted, ip) {
				t.Errorf("real IP %q survived redaction", ip)
			}
		}
		if mac, ok := attrs["mac"].(string); ok && mac != "" {
			sawMAC = true
			if strings.Contains(redacted, mac) {
				t.Errorf("real MAC %q survived redaction", mac)
			}
		}
	}
	if !sawIP || !sawMAC {
		t.Fatalf("test fixture assumption broken: sawIP=%v sawMAC=%v - arp_change was expected to carry both", sawIP, sawMAC)
	}
}

// TestPrivateIPPatternBoundsAreExact is a deliberate-break-style direct
// check of the regex's own boundary values, independent of the higher-level
// replacement tests above: a private-range pattern off by one octet in the
// 172.16-31 bound would either over- or under-redact.
func TestPrivateIPPatternBoundsAreExact(t *testing.T) {
	inRange := []string{"172.16.0.0", "172.31.255.255"}
	outOfRange := []string{"172.15.255.255", "172.32.0.0"}
	for _, ip := range inRange {
		if !privateIPPattern.MatchString(ip) {
			t.Errorf("privateIPPattern does not match in-range address %s", ip)
		}
	}
	for _, ip := range outOfRange {
		if privateIPPattern.MatchString(ip) {
			t.Errorf("privateIPPattern incorrectly matches out-of-range address %s", ip)
		}
	}
}

func loadRealScenarioEvents(t *testing.T, scenario string) []map[string]any {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	var path string
	for i := 0; i < 6; i++ {
		candidate := filepath.Join(dir, "corpus", "v1", scenario, "events.json")
		if _, err := os.Stat(candidate); err == nil {
			path = candidate
			break
		}
		dir = filepath.Dir(dir)
	}
	if path == "" {
		t.Fatalf("could not find corpus/v1/%s/events.json from the test's working directory", scenario)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var events []map[string]any
	if err := json.Unmarshal(data, &events); err != nil {
		t.Fatal(err)
	}
	return events
}
