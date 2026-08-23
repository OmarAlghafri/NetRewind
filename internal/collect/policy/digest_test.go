package policy

import (
	"strings"
	"testing"
)

const baseline = `
table inet filter {
	chain input {
		type filter hook input priority 0; policy accept;
		ct state established,related accept
		tcp dport 22 accept
	}
}
`

func TestSnapshotIgnoresFormatting(t *testing.T) {
	spaced := "table inet filter {\n\n    chain input {\n        tcp dport 22 accept\n    }\n}\n"
	tight := "table inet filter {\nchain input {\ntcp dport 22 accept\n}\n}"

	if Snapshot(spaced).Digest != Snapshot(tight).Digest {
		t.Error("indentation and blank lines changed the digest")
	}
}

// A live firewall's counters move constantly. Treating that as a rule change
// would cry wolf every few seconds until nobody looked at the collector again.
func TestCountersAndHandlesAreNotChanges(t *testing.T) {
	first := `table inet filter {
	chain input { # handle 1
		tcp dport 22 counter packets 10 bytes 640 accept # handle 4
	}
}`
	later := `table inet filter {
	chain input { # handle 1
		tcp dport 22 counter packets 99999 bytes 7654321 accept # handle 4
	}
}`
	if Snapshot(first).Digest != Snapshot(later).Digest {
		t.Error("a counter ticking over was reported as a policy change")
	}
}

func TestAddedRuleIsDetected(t *testing.T) {
	before := Snapshot(baseline)
	after := Snapshot(strings.Replace(baseline,
		"tcp dport 22 accept", "tcp dport 22 accept\n\t\ttcp dport 9200 drop", 1))

	if before.Digest == after.Digest {
		t.Fatal("adding a rule did not change the digest")
	}
	d := Compare(before, after)
	if len(d.Added) != 1 {
		t.Fatalf("added = %v, want exactly the one new rule", d.Added)
	}
	if want := "table inet filter / chain input :: tcp dport 9200 drop"; d.Added[0] != want {
		t.Errorf("added = %q, want %q", d.Added[0], want)
	}
	if len(d.Removed) != 0 {
		t.Errorf("removed = %v, want nothing", d.Removed)
	}
}

func TestRemovedRuleIsDetected(t *testing.T) {
	before := Snapshot(baseline)
	after := Snapshot(strings.Replace(baseline, "\t\tct state established,related accept\n", "", 1))

	d := Compare(before, after)
	if len(d.Removed) != 1 {
		t.Fatalf("removed = %v, want the one dropped rule", d.Removed)
	}
	if !strings.HasSuffix(d.Removed[0], ":: ct state established,related accept") {
		t.Errorf("removed = %q", d.Removed[0])
	}
}

// A rule moving within a chain is not a policy change, and reporting it as one
// would bury the changes that are.
func TestReorderingWithinAChainIsNotAChange(t *testing.T) {
	a := Snapshot("table inet filter {\nchain input {\ntcp dport 22 accept\ntcp dport 80 accept\n}\n}")
	b := Snapshot("table inet filter {\nchain input {\ntcp dport 80 accept\ntcp dport 22 accept\n}\n}")

	if d := Compare(a, b); !d.Empty() {
		t.Errorf("reordering reported as a change: added %v removed %v", d.Added, d.Removed)
	}
}

// The same rule in a different chain is a different rule. Comparing bare rule
// text would call this no change at all, and moving a rule between chains is
// exactly the sort of edit that breaks a network quietly.
func TestARuleMovedBetweenChainsIsAChange(t *testing.T) {
	a := Snapshot("table inet filter {\nchain input {\ntcp dport 22 accept\n}\nchain forward {\n}\n}")
	b := Snapshot("table inet filter {\nchain input {\n}\nchain forward {\ntcp dport 22 accept\n}\n}")

	d := Compare(a, b)
	if d.Empty() {
		t.Fatal("a rule moving between chains was reported as no change")
	}
	if len(d.Added) != 1 || len(d.Removed) != 1 {
		t.Errorf("added %v, removed %v - want one of each", d.Added, d.Removed)
	}
}

// A chain appearing or vanishing matters even when it holds no rules.
func TestAnEmptyChainAppearingIsAChange(t *testing.T) {
	a := Snapshot("table inet filter {\nchain input {\n}\n}")
	b := Snapshot("table inet filter {\nchain input {\n}\nchain output {\n}\n}")

	d := Compare(a, b)
	if len(d.Added) != 1 || d.Added[0] != "[table inet filter / chain output]" {
		t.Errorf("added = %v, want the new chain", d.Added)
	}
}

func TestIdenticalRulesetsCompareEmpty(t *testing.T) {
	s := Snapshot(baseline)
	if d := Compare(s, s); !d.Empty() {
		t.Errorf("a ruleset differed from itself: %+v", d)
	}
}

// Adding a rule that drops traffic can break connectivity; one that logs or
// counts cannot. They must not read the same.
func TestOnlyBlockingVerdictsAreInteresting(t *testing.T) {
	blocking := []string{
		"tcp dport 9200 drop",
		"ip saddr 10.0.0.0/8 reject",
		"tcp dport 22 jump ssh-guard",
		"type filter hook input priority 0; policy drop;",
	}
	for _, line := range blocking {
		if !interesting(line) {
			t.Errorf("%q should count as potentially breaking", line)
		}
	}
	for _, line := range []string{"tcp dport 22 accept", "counter", "ct state established,related accept"} {
		if interesting(line) {
			t.Errorf("%q should not count as potentially breaking", line)
		}
	}
}
