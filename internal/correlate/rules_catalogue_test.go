package correlate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// generatedRuleSummary mirrors internal/correlate/gen/main.go's ruleSummary
// (and internal/api/v1/server.go's ruleSummary, the same shape over the
// wire) - duplicated rather than imported because gen is package main.
type generatedRuleSummary struct {
	ID         string              `json:"id"`
	Title      string              `json:"title"`
	Severity   string              `json:"severity"`
	Confidence uint8               `json:"confidence"`
	Window     string              `json:"window"`
	RootCause  string              `json:"root_cause"`
	Advice     string              `json:"advice"`
	I18n       map[string]RuleI18n `json:"i18n,omitempty"`
}

// TestTheGeneratedRulesCatalogueIsUpToDate keeps
// desktop/src/i18n/generated/rules.json in step with rules/*.yaml the same
// way internal/event's TestTheGeneratedKindCatalogueIsUpToDate keeps
// kinds.json in step with kinds.go: by loading the real source through the
// same function the recorder itself uses (LoadRules - strict decoder,
// Validate(), the lot) and comparing, rather than trusting that whoever
// edited or added a rule remembered to run the generator.
//
// This matters more here than for kinds.go: rules/*.yaml changes far more
// often (a new rule, a reworded translation), and a stale rules.json would
// leave demo and bundle mode showing an old title or a missing translation
// while every Go test still passes.
func TestTheGeneratedRulesCatalogueIsUpToDate(t *testing.T) {
	rules, err := LoadRules(filepath.Join("..", "..", "rules"))
	if err != nil {
		t.Fatalf("LoadRules: %v", err)
	}

	want := make([]generatedRuleSummary, 0, len(rules))
	for _, r := range rules {
		want = append(want, generatedRuleSummary{
			ID:         r.ID,
			Title:      r.Title,
			Severity:   r.Severity,
			Confidence: r.Confidence,
			Window:     r.Window.String(),
			RootCause:  r.RootCause,
			Advice:     r.Advice,
			I18n:       r.I18n,
		})
	}
	sort.Slice(want, func(i, j int) bool { return want[i].ID < want[j].ID })

	genPath := filepath.Join("..", "..", "desktop", "src", "i18n", "generated", "rules.json")
	data, err := os.ReadFile(genPath)
	if err != nil {
		t.Fatalf("read %s: %v (run `go run ./internal/correlate/gen` first)", genPath, err)
	}
	var got struct {
		Rules []generatedRuleSummary `json:"rules"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("parse %s: %v", genPath, err)
	}

	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal want: %v", err)
	}
	gotJSON, err := json.Marshal(got.Rules)
	if err != nil {
		t.Fatalf("marshal got: %v", err)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("%s is stale against rules/*.yaml - run `go run ./internal/correlate/gen`\n  got:  %s\n  want: %s",
			genPath, gotJSON, wantJSON)
	}
}
