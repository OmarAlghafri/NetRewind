// Command gen writes desktop/src/i18n/generated/rules.json from rules/*.yaml,
// for the same reason internal/event/gen writes kinds.json from kinds.go:
// demo mode and bundle mode never talk to a live recorder, so they have no
// other way to get a rule's Arabic title/advice/clause text (ADR 0004).
//
// This loads the rules through correlate.LoadRules - the exact function
// netrewindd and `netrewind rules` use, strict-decoder and Validate() and
// all - rather than re-parsing the YAML a second, looser way. A rule that
// would fail to load for the recorder cannot silently end up in the GUI's
// catalogue looking fine.
//
// Run it after adding, removing or translating a rule:
//
//	go run ./internal/correlate/gen
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/OmarAlghafri/netrewind/internal/correlate"
)

// ruleSummary mirrors internal/api/v1/server.go's ruleSummary field for
// field - the same shape over the wire from /v1/rules and from this
// generated file, so the frontend can treat a live-fetched rule and a
// generated one as interchangeable.
type ruleSummary struct {
	ID         string                        `json:"id"`
	Title      string                        `json:"title"`
	Severity   string                        `json:"severity"`
	Confidence uint8                         `json:"confidence"`
	Window     string                        `json:"window"`
	RootCause  string                        `json:"root_cause"`
	Advice     string                        `json:"advice"`
	I18n       map[string]correlate.RuleI18n `json:"i18n,omitempty"`
}

type rulesFile struct {
	Rules []ruleSummary `json:"rules"`
}

func main() {
	root, err := repoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}

	rulesDir := filepath.Join(root, "rules")
	rules, err := correlate.LoadRules(rulesDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}

	out := make([]ruleSummary, 0, len(rules))
	for _, r := range rules {
		out = append(out, ruleSummary{
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
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })

	data, err := json.MarshalIndent(rulesFile{Rules: out}, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
	data = append(data, '\n')

	dest := filepath.Join(root, "desktop", "src", "i18n", "generated", "rules.json")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %d rules to %s\n", len(out), dest)
}

// repoRoot walks up from the working directory until it finds go.mod,
// matching internal/event/gen's own copy of the same small helper.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		dir = filepath.Dir(dir)
	}
	return "", fmt.Errorf("could not find the repository root above %s", dir)
}
