// Command replaydemo regenerates desktop/src/demo/demo-incidents.json by
// replaying desktop/src/demo/demo-events.json through the current
// correlation engine and rules - the same conclusion a live recorder would
// reach given the same events, without lab/inject.sh's Linux network-
// namespace fault injection to reproduce them.
//
// docs/product/adr/0001-desktop-shell-and-demo-mode.md: the demo pair was
// originally an actual `netrewind events/incidents -o json` export from a
// real lab/inject.sh run. demo-events.json does not need to change - every
// schema addition since then (Link.Clause among them) is a correlation
// *output* field, not an input one - only demo-incidents.json goes stale
// when the engine or the rules change in a way that affects what they
// conclude, which is exactly what happened when Link.Clause landed after
// the original export: the demo fixture kept working, it just could never
// exercise the new field, which is how the i18n work in this session found
// it (docs/evidence/37-gui-modernization-phase4-rules-json-frontend.log).
//
// Run it after a rules/*.yaml or internal/correlate change that would
// affect what the demo recording concludes:
//
//	go run ./internal/correlate/replaydemo
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"

	"github.com/OmarAlghafri/netrewind/internal/correlate"
	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/incident"
)

func main() {
	root, err := repoRoot()
	if err != nil {
		fail(err)
	}

	eventsPath := filepath.Join(root, "desktop", "src", "demo", "demo-events.json")
	data, err := os.ReadFile(eventsPath)
	if err != nil {
		fail(fmt.Errorf("read %s: %w", eventsPath, err))
	}
	var events []*event.Event
	if err := json.Unmarshal(data, &events); err != nil {
		fail(fmt.Errorf("parse %s: %w", eventsPath, err))
	}
	// The writer pipeline (cmd/netrewindd/writer.go) offers events to the
	// engine in the order they were flushed, which is arrival order - always
	// chronological for a live recorder. demo-events.json should already be
	// in this order (it is itself a `-o json` export in ts_wall order), but
	// sorting explicitly makes that an enforced fact instead of an assumption
	// carried over from the file's history.
	sort.Slice(events, func(i, j int) bool { return events[i].TSWall < events[j].TSWall })

	rules, err := correlate.LoadRules(filepath.Join(root, "rules"))
	if err != nil {
		fail(err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	engine := correlate.NewEngine(rules, log)

	incidents := make([]*incident.Incident, 0)
	for _, e := range events {
		incidents = append(incidents, engine.Offer(e)...)
	}

	out, err := json.MarshalIndent(incidents, "", "  ")
	if err != nil {
		fail(err)
	}
	out = append(out, '\n')

	dest := filepath.Join(root, "desktop", "src", "demo", "demo-incidents.json")
	if err := os.WriteFile(dest, out, 0o644); err != nil {
		fail(err)
	}
	fmt.Printf("wrote %d incidents (from %d events) to %s\n", len(incidents), len(events), dest)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "replaydemo:", err)
	os.Exit(1)
}

// repoRoot walks up from the working directory until it finds go.mod,
// matching internal/event/gen's and internal/correlate/gen's own copy of
// the same small helper.
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
