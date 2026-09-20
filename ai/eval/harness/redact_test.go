package harness

import "testing"

// TestRedactAddressesDelegatesToInternalRedact is a thin check that this
// package's own alias still reaches the real implementation - the full
// behavioral test suite (RFC 1918 bounds, MAC handling, placeholder
// consistency, a real corpus fixture) now lives in internal/redact, so it
// is not duplicated here.
func TestRedactAddressesDelegatesToInternalRedact(t *testing.T) {
	got := RedactAddresses("10.99.0.11 changed")
	if got != "<HOST_1> changed" {
		t.Errorf("RedactAddresses(%q) = %q, want the real address redacted", "10.99.0.11 changed", got)
	}
}
