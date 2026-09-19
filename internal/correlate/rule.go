// Package correlate is the Isnad engine: it turns a stream of events into
// incidents whose every link names its evidence.
//
// The name is from the classical Arabic method of establishing a chain of
// transmission, in which a report is judged by whether every link in the chain
// is named and every link is verifiable. That is the standard here. A rule may
// only claim a cause where it can point at the events that show it, and the
// strength of the chain is bounded by the weakest link in it.
package correlate

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/incident"
	"gopkg.in/yaml.v3"
)

// Rule describes one recognisable shape of failure.
//
// Rules are files rather than code on purpose. The library of them is the part
// of this project that cannot be cloned: it accumulates from real incidents,
// and anyone who has debugged a network can contribute one without touching Go.
type Rule struct {
	ID       string `yaml:"id"`
	Title    string `yaml:"title"`
	Severity string `yaml:"severity"`
	// Confidence is how far the rule's author trusts the inference. It is an
	// upper bound: the engine lowers it to match the events actually matched.
	Confidence uint8 `yaml:"confidence"`
	// Window is how far apart the first and last clause may be.
	Window time.Duration `yaml:"window"`
	// Match is an ordered list of clauses. They must occur in this order.
	Match []Clause `yaml:"match"`
	// CorrelateOn names fields that must be equal across every matched event,
	// so two unrelated failures happening at once are not woven together.
	CorrelateOn []string `yaml:"correlate_on"`
	// RootCause names the clause (by its `as`) that the rule blames.
	RootCause string `yaml:"root_cause"`
	Advice    string `yaml:"advice"`
	// I18n carries this rule's translated narrative text, keyed by
	// language code ("ar") - execution order §4.5 / ADR 0004. Absent
	// entirely, or missing a field within one language, means "no
	// translation yet": the GUI falls back to the English Title/Advice/
	// Clause.Why above, tagged as the original text, never blank and
	// never a hard failure. Optional so every rule file written before
	// this field existed keeps loading unchanged.
	I18n map[string]RuleI18n `yaml:"i18n,omitempty"`
}

// RuleI18n is one language's translation of a rule's narrative text.
type RuleI18n struct {
	Title  string `yaml:"title,omitempty" json:"title,omitempty"`
	Advice string `yaml:"advice,omitempty" json:"advice,omitempty"`
	// Clauses is keyed by the clause's own `as` name, matching how
	// RootCause already refers to a clause - so a translation for the
	// wrong clause name is a validation error, not a silently-ignored typo.
	Clauses map[string]string `yaml:"clauses,omitempty" json:"clauses,omitempty"`
}

// Clause is one event the rule is looking for.
type Clause struct {
	// As names the clause so root_cause can refer to it.
	As string `yaml:"as"`
	// Kinds are the event kinds that satisfy this clause; any one of them.
	Kinds []string `yaml:"kinds"`
	// Where narrows the match by attribute value, severity or subject.
	Where Where `yaml:"where"`
	// MinCount requires the clause to match this many times. A single port
	// going down is an event; the same port going down five times is a fault.
	MinCount int `yaml:"min_count"`
	// Optional lets the rule fire without this clause, which is how a rule can
	// describe a consequence that sometimes follows and sometimes does not.
	//
	// Optional clauses placed *before* the first required one are searched
	// backwards in time from it. That is how a rule asks the question this
	// whole project exists for: the outage has happened, so what changed just
	// before it?
	Optional bool `yaml:"optional"`
	// Relation is what this clause claims about its predecessor. The first
	// clause has none.
	Relation incident.Relation `yaml:"relation"`
	// Why is the sentence this step contributes to the account.
	Why string `yaml:"why"`
}

// Where narrows a clause.
type Where struct {
	// Attrs must all be present and equal. Values are compared as text so a
	// rule file does not have to know whether the collector wrote a number or
	// a string.
	Attrs map[string]string `yaml:"attrs"`
	// MinSeverity excludes anything gentler.
	MinSeverity string `yaml:"min_severity"`
	// SubjectKind restricts what the event may be about.
	SubjectKind string `yaml:"subject_kind"`
}

// Validate checks a rule is answerable before the engine trusts it at runtime.
func (r *Rule) Validate() error {
	switch {
	case r.ID == "":
		return fmt.Errorf("rule has no id")
	case r.Title == "":
		return fmt.Errorf("rule %s has no title", r.ID)
	case len(r.Match) == 0:
		return fmt.Errorf("rule %s matches nothing", r.ID)
	case r.Window <= 0:
		return fmt.Errorf("rule %s has no window", r.ID)
	case r.Confidence == 0 || r.Confidence > 100:
		return fmt.Errorf("rule %s has confidence %d, want 1-100", r.ID, r.Confidence)
	}

	named := make(map[string]bool, len(r.Match))
	for i, c := range r.Match {
		if len(c.Kinds) == 0 {
			return fmt.Errorf("rule %s clause %d matches no kinds", r.ID, i)
		}
		if c.As != "" {
			named[c.As] = true
		}
		if i > 0 && c.Relation == "" {
			return fmt.Errorf("rule %s clause %d does not say how it relates to the previous one", r.ID, i)
		}
		switch c.Relation {
		case "", incident.RelCauses, incident.RelCorrelates, incident.RelPrecedes:
		default:
			return fmt.Errorf("rule %s clause %d has unknown relation %q", r.ID, i, c.Relation)
		}
	}
	if r.RootCause != "" && !named[r.RootCause] {
		return fmt.Errorf("rule %s blames %q, which is not one of its clauses", r.ID, r.RootCause)
	}
	for lang, tr := range r.I18n {
		for as := range tr.Clauses {
			if !named[as] {
				return fmt.Errorf("rule %s: i18n.%s.clauses names %q, which is not one of its clauses", r.ID, lang, as)
			}
		}
	}
	if r.Anchor() < 0 {
		return fmt.Errorf("rule %s has no required clause, so it would match everything", r.ID)
	}
	return nil
}

// Anchor is the index of the first required clause: the event whose arrival
// makes the engine evaluate this rule at all.
//
// Clauses before it are searched backwards in time, clauses after it forwards.
// Returns -1 if every clause is optional, which is not a rule.
func (r *Rule) Anchor() int {
	for i := range r.Match {
		if !r.Match[i].Optional {
			return i
		}
	}
	return -1
}

func (r *Rule) severity() event.Severity {
	if r.Severity == "" {
		return event.SevWarn
	}
	return event.Severity(r.Severity)
}

// matches reports whether an event satisfies a clause.
func (c *Clause) matches(e *event.Event) bool {
	found := false
	for _, k := range c.Kinds {
		if string(e.Kind) == k {
			found = true
			break
		}
	}
	if !found {
		return false
	}
	if c.Where.SubjectKind != "" && string(e.Subject.Kind) != c.Where.SubjectKind {
		return false
	}
	if c.Where.MinSeverity != "" && rank(e.Severity) < rank(event.Severity(c.Where.MinSeverity)) {
		return false
	}
	for key, want := range c.Where.Attrs {
		if !attrEquals(e, key, want) {
			return false
		}
	}
	return true
}

// attrEquals compares an attribute as text.
//
// The same attribute arrives as a Go bool from a collector and as a json.Number
// or string from the store, and a rule file should not have to care which.
func attrEquals(e *event.Event, key, want string) bool {
	v, ok := e.Attrs[key]
	if !ok {
		return false
	}
	return strings.EqualFold(fmt.Sprint(v), want)
}

func rank(s event.Severity) int {
	switch s {
	case event.SevError:
		return 3
	case event.SevWarn:
		return 2
	case event.SevNotice:
		return 1
	default:
		return 0
	}
}

// LoadRules reads every .yaml file in a directory.
func LoadRules(dir string) ([]*Rule, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("correlate: read rules from %s: %w", dir, err)
	}
	var rules []*Rule
	seen := make(map[string]string, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !isYAML(entry.Name()) {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		r, err := LoadRule(path)
		if err != nil {
			return nil, err
		}
		if other, dup := seen[r.ID]; dup {
			return nil, fmt.Errorf("correlate: rule id %q defined in both %s and %s", r.ID, other, path)
		}
		seen[r.ID] = path
		rules = append(rules, r)
	}
	if len(rules) == 0 {
		return nil, fmt.Errorf("correlate: no rules found in %s", dir)
	}
	return rules, nil
}

// LoadRule reads and validates a single rule file.
func LoadRule(path string) (*Rule, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("correlate: read %s: %w", path, err)
	}
	// Unknown keys are refused rather than ignored.
	//
	// A rule is the one part of this system people are meant to write, and a
	// misspelled key is the quietest way to get it wrong: the rule loads, the
	// engine reports it as active, and the clause the author thought they had
	// written is simply not there. The rule then never fires, or fires far too
	// often, and nothing anywhere says why. This was found by writing a rule
	// with a key that does not exist and watching it load cleanly.
	var r Rule
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&r); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("correlate: parse %s: %w", path, err)
	}
	if err := r.Validate(); err != nil {
		return nil, fmt.Errorf("correlate: %s: %w", path, err)
	}
	return &r, nil
}

func isYAML(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".yaml" || ext == ".yml"
}

// minCount is how many times a clause must match; unset means once.
func (c *Clause) minCount() int {
	if c.MinCount < 1 {
		return 1
	}
	return c.MinCount
}
