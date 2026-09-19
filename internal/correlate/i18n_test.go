package correlate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
)

// The strict decoder (TestAnUnknownKeyInARuleIsRefused) rejects any YAML key
// the Go structs do not declare - so the localization contract (execution
// order §4.5 / ADR 0004) has to be a Go struct field before a single rule
// file can use it, or every rule with an i18n block fails to load. This
// proves the field actually works, not just that it compiles.
func writeRuleFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rule.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestARuleWithATranslationBlockLoadsAndKeepsBothLanguages(t *testing.T) {
	path := writeRuleFile(t, `
id: has-i18n
title: A path that was working stopped working
severity: error
confidence: 88
window: 60s
advice: Check your change record.
match:
  - as: change
    kinds: [link.down]
    why: The last thing that changed before it broke.
  - as: breakage
    kinds: [flow.first_failure_for_pair]
    relation: causes
    why: The connection stopped completing.
i18n:
  ar:
    title: مسار كان يعمل توقف عن العمل
    advice: راجع سجل التغييرات.
    clauses:
      change: آخر شيء تغيّر قبل أن يتعطل.
      breakage: توقف الاتصال عن الاكتمال.
`)
	r, err := LoadRule(path)
	if err != nil {
		t.Fatalf("LoadRule: %v", err)
	}
	// The English fields are untouched - i18n is additive, never a
	// replacement for the base language.
	if r.Title != "A path that was working stopped working" {
		t.Errorf("English title changed: %q", r.Title)
	}
	ar, ok := r.I18n["ar"]
	if !ok {
		t.Fatal("i18n.ar did not survive loading")
	}
	if ar.Title != "مسار كان يعمل توقف عن العمل" {
		t.Errorf("ar.title = %q", ar.Title)
	}
	if ar.Clauses["change"] == "" || ar.Clauses["breakage"] == "" {
		t.Errorf("ar.clauses missing an entry: %+v", ar.Clauses)
	}
}

// A rule file predating this feature (no i18n key at all) must keep
// loading exactly as before - the whole point of making the field
// optional. TestTheShippedRulesAllLoad already proves this for the 19
// real rule files; this is the same claim with an inline fixture the
// reader can see the shape of directly.
func TestARuleWithNoTranslationBlockStillLoads(t *testing.T) {
	path := writeRuleFile(t, `
id: no-i18n
title: t
severity: warn
confidence: 80
window: 60s
advice: a
match:
  - as: x
    kinds: [link.down]
    why: y
`)
	r, err := LoadRule(path)
	if err != nil {
		t.Fatalf("a pre-i18n rule file must still load: %v", err)
	}
	if len(r.I18n) != 0 {
		t.Errorf("I18n = %+v, want empty for a file with no i18n key", r.I18n)
	}
}

// A translated clause name is exactly as easy to typo as any other
// cross-reference in a rule file (root_cause has the same class of check,
// just above this one in rule.go) - it must be caught the same way, not
// silently accepted as a translation for a clause that does not exist.
func TestATranslationForANonexistentClauseIsRejected(t *testing.T) {
	path := writeRuleFile(t, `
id: bad-i18n
title: t
severity: warn
confidence: 80
window: 60s
advice: a
match:
  - as: real_clause
    kinds: [link.down]
    why: y
i18n:
  ar:
    clauses:
      typo_clause: لا يوجد
`)
	_, err := LoadRule(path)
	if err == nil {
		t.Fatal("a translation naming a clause that does not exist loaded cleanly")
	}
	if !strings.Contains(err.Error(), "typo_clause") {
		t.Errorf("error does not name the offending clause: %v", err)
	}
}

// A built incident's Link.Clause must name the actual clause that matched
// it, so a client can look up "rule_id + this clause" in a translation
// catalogue even when Why itself is the English event.Describe fallback -
// the entire reason this field exists (ADR 0004). Checked against a
// two-clause rule specifically, so a wrong index or an off-by-one would
// show up as the wrong clause name, not just *a* non-empty one.
func TestChainLinksNameTheClauseThatMatchedThem(t *testing.T) {
	r := &Rule{
		ID: "two-clause-i18n", Title: "t", Severity: "warn", Confidence: 80,
		Window: time.Minute, RootCause: "second",
		Match: []Clause{
			{As: "first", Kinds: []string{"link.down"}, Why: "it went down"},
			{As: "second", Kinds: []string{"link.up"}, Relation: "correlates", Why: "it came back"},
		},
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	e := NewEngine([]*Rule{r}, quietLog())
	now := time.Now()
	e.Offer(at(event.KindLinkDown, "eth0", now))
	got := e.Offer(at(event.KindLinkUp, "eth0", now.Add(time.Second)))
	if len(got) != 1 || len(got[0].Chain) != 2 {
		t.Fatalf("got %+v, want one incident with a two-link chain", got)
	}
	if got[0].Chain[0].Clause != "first" {
		t.Errorf("Chain[0].Clause = %q, want %q", got[0].Chain[0].Clause, "first")
	}
	if got[0].Chain[1].Clause != "second" {
		t.Errorf("Chain[1].Clause = %q, want %q", got[0].Chain[1].Clause, "second")
	}
}
