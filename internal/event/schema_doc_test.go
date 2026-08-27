package event_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// docTableRow matches one line of the reference table in docs/schema.md:
//
//	| `l2.mac_moved` | netlink | The same hardware address ... |
//
// The star that marks an incident-opening kind is allowed after the code span.
var docTableRow = regexp.MustCompile(
	"^\\| `([a-z0-9]+\\.[a-z0-9_]+)`[^|]*\\|([^|]*)\\|([^|]*)\\|\\s*$")

// TestTheSchemaDocumentListsEveryKind keeps the reference table and the code
// from drifting apart.
//
// Documentation that describes a schema the code does not have is the same
// failure as a schema the code does not implement, and it is the one that
// happens quietly: nobody notices a missing row. Twenty-five kinds were once
// declared and produced by nothing; this is the reader-facing half of the same
// guard that TestEveryKindIsProducedOrReserved provides for producers.
func TestTheSchemaDocumentListsEveryKind(t *testing.T) {
	root := repoRoot(t)

	kinds := declaredKinds(t, filepath.Join(root, "internal", "event", "kinds.go"))
	declared := make(map[string]bool, len(kinds))
	for _, kind := range kinds {
		declared[kind] = true
	}

	documented, reservedInDoc := parseSchemaTable(t, filepath.Join(root, "docs", "schema.md"))

	if len(documented) < len(declared) {
		t.Errorf("the table lists %d kinds but %d are declared", len(documented), len(declared))
	}

	for kind := range declared {
		if !documented[kind] {
			t.Errorf("%s is declared in kinds.go and missing from the table in docs/schema.md", kind)
		}
	}
	for kind := range documented {
		if !declared[kind] {
			t.Errorf("docs/schema.md documents %s, which no longer exists in kinds.go", kind)
		}
	}

	// A kind the code does not produce must say so where a reader will see it.
	// The two lists are maintained in different files by different people at
	// different times, which is exactly how they come apart.
	for kind := range reserved {
		if !reservedInDoc[kind] {
			t.Errorf("%s is reserved in coverage_test.go but the table does not tell a reader it is unimplemented", kind)
		}
	}
	for kind := range reservedInDoc {
		if _, ok := reserved[kind]; !ok {
			t.Errorf("the table calls %s reserved, but it is produced; the documentation understates what this records", kind)
		}
	}

	// The sentence above the table counts the reserved kinds. Nothing checked
	// it, so it said nine while the table marked seven - which is how a reader
	// learns to distrust the number and go count the rows themselves.
	assertReservedCountMatchesProse(t, filepath.Join(root, "docs", "schema.md"), len(reserved))
}

// numberWords covers the range the count can plausibly take. A count outside it
// fails loudly rather than silently skipping the check.
var numberWords = map[int]string{
	0: "None", 1: "One", 2: "Two", 3: "Three", 4: "Four", 5: "Five", 6: "Six",
	7: "Seven", 8: "Eight", 9: "Nine", 10: "Ten", 11: "Eleven", 12: "Twelve",
}

var proseCount = regexp.MustCompile(`(?s)(\w+) of them are\s+declared but not yet produced`)

func assertReservedCountMatchesProse(t *testing.T, path string, want int) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	m := proseCount.FindSubmatch(data)
	if m == nil {
		t.Fatal("docs/schema.md no longer states how many kinds are reserved; the sentence above the table is what a reader reads instead of counting rows")
	}
	word, ok := numberWords[want]
	if !ok {
		t.Fatalf("%d reserved kinds is outside the range this check spells out; extend numberWords", want)
	}
	if got := string(m[1]); got != word {
		t.Errorf("docs/schema.md says %q kinds are reserved; %d are", got, want)
	}
}

// parseSchemaTable returns the kinds the reference table lists, and which of
// them it marks as not yet produced.
func parseSchemaTable(t *testing.T, path string) (all, notProduced map[string]bool) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	all, notProduced = map[string]bool{}, map[string]bool{}
	inTable := false
	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case strings.HasPrefix(line, "## Every kind"):
			inTable = true
			continue
		case inTable && strings.HasPrefix(line, "## "):
			inTable = false
		}
		if !inTable {
			continue
		}
		m := docTableRow.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		kind := m[1]
		if all[kind] {
			t.Errorf("%s is listed twice in the table", kind)
		}
		all[kind] = true
		if strings.Contains(m[3], "*Reserved:*") {
			notProduced[kind] = true
		}
	}
	if len(all) == 0 {
		t.Fatalf("no table rows parsed from %s; the format changed and this guard has stopped guarding", path)
	}
	return all, notProduced
}
