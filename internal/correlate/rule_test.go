package correlate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A misspelled key in a rule file is the quietest way to get a rule wrong: it
// loads, the engine counts it as active, and the clause the author believed
// they wrote is simply not there. Found by writing `before: true` - which is
// not a field, because backwards matching is positional - and watching the rule
// load without complaint.
func TestAnUnknownKeyInARuleIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "typo.yaml")
	const rule = `
id: typo
title: A rule with a key that does not exist
severity: warn
confidence: 90
window: 60s
match:
  - as: thing
    kinds: [link.down]
    before: true
    why: the key above is not a field
`
	if err := os.WriteFile(path, []byte(rule), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadRule(path)
	if err == nil {
		t.Fatal("a rule with an unknown key loaded cleanly; the clause would silently not exist")
	}
	if !strings.Contains(err.Error(), "before") {
		t.Errorf("the error does not name the offending key: %v", err)
	}
}

// Every rule that ships has to survive the strict decoder, or an upgrade turns
// somebody's working library into a daemon that will not start.
func TestTheShippedRulesAllLoad(t *testing.T) {
	rules, err := LoadRules(filepath.Join("..", "..", "rules"))
	if err != nil {
		t.Fatalf("the shipped rule library does not load: %v", err)
	}
	if len(rules) < 15 {
		t.Errorf("loaded %d rules, expected the whole library", len(rules))
	}
	for _, r := range rules {
		if r.Anchor() < 0 {
			t.Errorf("rule %s has no required clause, so it would match everything", r.ID)
		}
		if r.RootCause == "" {
			t.Errorf("rule %s blames nothing, so an incident from it names no cause", r.ID)
		}
	}
	t.Logf("%d rules load", len(rules))
}
