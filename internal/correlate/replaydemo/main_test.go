package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/OmarAlghafri/netrewind/internal/correlate"
	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/incident"
)

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestMaxMinCountByKindTakesTheLargestClauseAcrossRules(t *testing.T) {
	rules := []*correlate.Rule{
		{ID: "a", Match: []correlate.Clause{{Kinds: []string{"link.down"}, MinCount: 3}}},
		{ID: "b", Match: []correlate.Clause{{Kinds: []string{"link.down"}}}}, // unset means 1
		{ID: "c", Match: []correlate.Clause{{Kinds: []string{"metric.anomaly"}, MinCount: 2}}},
	}
	got := maxMinCountByKind(rules)
	if got["link.down"] != 3 {
		t.Errorf("link.down max min_count = %d, want 3 (the larger of the two rules asking for it)", got["link.down"])
	}
	if got["metric.anomaly"] != 2 {
		t.Errorf("metric.anomaly max min_count = %d, want 2", got["metric.anomaly"])
	}
	if _, ok := got["link.up"]; ok {
		t.Error("link.up: no rule asks for it, so it should not appear in the map at all")
	}
}

// upsertIncident has to replicate internal/store/incident_sqlite.go's own
// upsert exactly, or a replay that legitimately re-fires the same incident
// ID as a fuller match completes an optional clause ends up with duplicate
// incident_ids carrying different content instead of one row - which
// regenerating demo-incidents.json from this tool's first, append-only
// version actually produced.
func TestUpsertIncidentKeepsFirstReportButLatestEverythingElse(t *testing.T) {
	first := &incident.Incident{
		ID: "INC1", OpenedAt: 100, ClosedAt: 100, Title: "first title", RuleID: "r1",
		Advice: "first advice", Status: incident.StatusOpen, Severity: event.SevWarn,
		Confidence: 80, RootCause: incident.RootCause{EventID: "E1"},
		Chain: []incident.Link{{EventID: "E1", At: 100}},
	}
	second := &incident.Incident{
		ID: "INC1", OpenedAt: 50, ClosedAt: 200, Title: "second title", RuleID: "should-not-win",
		Advice: "second advice", Status: incident.StatusClosed, Severity: event.SevError,
		Confidence: 95, RootCause: incident.RootCause{EventID: "E2"},
		Chain:   []incident.Link{{EventID: "E1", At: 100}, {EventID: "E2", At: 200}},
		Victims: []string{"host-a"},
	}

	byID := map[string]*incident.Incident{}
	var order []string
	upsertIncident(byID, &order, first)
	upsertIncident(byID, &order, second)

	if len(order) != 1 || order[0] != "INC1" {
		t.Fatalf("order = %v, want a single INC1 entry - a repeat id must not duplicate the row", order)
	}
	got := byID["INC1"]
	if got.OpenedAt != 100 {
		t.Errorf("OpenedAt = %d, want 100 (the first report's) - an incident's opened time is fixed at first report", got.OpenedAt)
	}
	if got.Title != "first title" {
		t.Errorf("Title = %q, want the first incident's", got.Title)
	}
	if got.RuleID != "r1" {
		t.Errorf("RuleID = %q, want %q", got.RuleID, "r1")
	}
	if got.Advice != "first advice" {
		t.Errorf("Advice = %q, want the first incident's", got.Advice)
	}
	if got.ClosedAt != 200 {
		t.Errorf("ClosedAt = %d, want 200 (the latest report's) - more evidence arrived", got.ClosedAt)
	}
	if got.Severity != event.SevError || got.Confidence != 95 {
		t.Errorf("Severity/Confidence = %s/%d, want the latest report's (error/95)", got.Severity, got.Confidence)
	}
	if len(got.Chain) != 2 {
		t.Errorf("Chain has %d links, want 2 - the fuller, later match's chain", len(got.Chain))
	}
	if len(got.Victims) != 1 || got.Victims[0] != "host-a" {
		t.Errorf("Victims = %v, want the latest report's", got.Victims)
	}
}

func TestLoadKnownTimestampsIsEmptyForAFileThatDoesNotExistYet(t *testing.T) {
	known, err := loadKnownTimestamps(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("a first-ever run must not error just because there is nothing to read yet: %v", err)
	}
	if len(known) != 0 {
		t.Errorf("known = %v, want empty", known)
	}
}

func TestLoadKnownTimestampsRecoversRankedAndUnrankedEvidence(t *testing.T) {
	prior := []*incident.Incident{
		{
			ID: "INC1", RuleID: "port-flapping",
			Chain: []incident.Link{
				{EventID: "FOLDED1", At: 1000, Evidence: map[string]any{"matched_count": float64(3)}},
			},
			ClosedAt: 3000, // the 3rd occurrence's real, recovered timestamp
		},
		{
			ID: "INC2", RuleID: "link-down-isolated-hosts",
			Chain: []incident.Link{
				{EventID: "FOLDED1", At: 1000}, // a different rule's min_count-1 clause citing the same lead event
				{EventID: "OTHER", At: 5000},
			},
		},
	}
	path := filepath.Join(t.TempDir(), "demo-incidents.json")
	b, err := json.Marshal(prior)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}

	known, err := loadKnownTimestamps(path)
	if err != nil {
		t.Fatal(err)
	}

	rec := known["FOLDED1"]
	if rec == nil {
		t.Fatal("FOLDED1: no evidence recovered at all")
	}
	if got := rec.ranked[3]; got != 3000 {
		t.Errorf("ranked[3] = %d, want 3000 (the matched_count=3 clause's own ClosedAt)", got)
	}
	if len(rec.unranked) != 2 {
		t.Fatalf("unranked = %v, want 2 entries - Link.At is recorded for every citing clause, ranked or not", rec.unranked)
	}
	for i, v := range rec.unranked {
		if v != 1000 {
			t.Errorf("unranked[%d] = %d, want 1000 (both clauses cite the same lead event)", i, v)
		}
	}

	other := known["OTHER"]
	if other == nil || len(other.unranked) != 1 || other.unranked[0] != 5000 {
		t.Errorf("OTHER = %+v, want a single unranked entry of 5000", other)
	}
	if other != nil && len(other.ranked) != 0 {
		t.Errorf("OTHER: ranked = %v, want none - INC2's last link carries no matched_count", other.ranked)
	}
}

func TestUnfoldEventsPassesThroughAnEventThatNeverFolded(t *testing.T) {
	e := &event.Event{ID: "E1", Kind: "link.down", TSWall: 1000, Count: 1}
	out := unfoldEvents([]*event.Event{e}, map[string]*recovered{}, map[string]int{})
	if len(out) != 1 {
		t.Fatalf("got %d events, want 1", len(out))
	}
	if out[0] == e {
		t.Error("unfoldEvents must return a copy, not the original pointer - main() sorts and sets fields on what it emits")
	}
	if out[0].TSWall != 1000 || out[0].Count != 1 {
		t.Errorf("got TSWall=%d Count=%d, want unchanged (1000, 1)", out[0].TSWall, out[0].Count)
	}
}

// This is rules/reachability-lost.yaml's own bug: a folded "loss" anchor
// (min_count 1, no historical evidence beyond ts_wall) was being unfolded to
// its full historical foldCount regardless, and the spurious extra copy -
// sharing the anchor's exact timestamp - was then greedily matched by the
// rule's own unrestricted, optional "recovery" clause instead of the real,
// distinct metric.anomaly event the original incident actually used.
func TestUnfoldEventsDoesNotInventCopiesNothingRequires(t *testing.T) {
	e := &event.Event{ID: "E1", Kind: "metric.anomaly", TSWall: 1000, Count: 2}
	out := unfoldEvents([]*event.Event{e}, map[string]*recovered{}, map[string]int{"metric.anomaly": 1})
	if len(out) != 1 {
		t.Fatalf("got %d copies, want 1 - nothing here (no rule requirement, no historical evidence) justifies inventing a second one", len(out))
	}
}

// rules/port-flapping.yaml's `min_count: 3` on link.down is reason enough by
// itself to keep a folded group unfolded, even with zero historical evidence
// of its own: this is a replay of the fact that the live engine received 3
// separate Offer() calls, however this tool later spaces their timestamps.
func TestUnfoldEventsHonoursARulesOwnMinCountRegardlessOfHistory(t *testing.T) {
	e := &event.Event{ID: "E1", Kind: "link.down", TSWall: 1000, Count: 3}
	out := unfoldEvents([]*event.Event{e}, map[string]*recovered{}, map[string]int{"link.down": 3})
	if len(out) != 3 {
		t.Fatalf("got %d copies, want 3 (the rule's own min_count, with no history to say otherwise)", len(out))
	}
	for i, c := range out {
		if c.ID != "E1" {
			t.Errorf("copy %d: ID = %q, want E1 - every consumer looks this event up by its original id against the unchanged demo-events.json", i, c.ID)
		}
		if c.Count != 1 {
			t.Errorf("copy %d: Count = %d, want 1 - it now represents one individual raw occurrence", i, c.Count)
		}
	}
}

func TestUnfoldEventsCapsAtTheHistoricalFoldCount(t *testing.T) {
	// A rule asking for more than a group's own history ever recorded must
	// not manufacture occurrences that never happened.
	e := &event.Event{ID: "E1", Kind: "link.down", TSWall: 1000, Count: 2}
	out := unfoldEvents([]*event.Event{e}, map[string]*recovered{}, map[string]int{"link.down": 5})
	if len(out) != 2 {
		t.Fatalf("got %d copies, want 2 (this group only ever folded 2 raw occurrences)", len(out))
	}
}

// Link.At restates ts_wall for any clause whose own lead event is this
// group's first occurrence - including a min_count>1 clause, whose lead
// event is ts_wall by definition. Counting that repeat as a second genuine
// occurrence invented a spare slot that could, and did, outrank and
// overwrite an actually-ranked value at the group's true last slot.
func TestUnfoldEventsFiltersOutRedundantBaselineEvidence(t *testing.T) {
	known := map[string]*recovered{
		"E1": {ranked: map[int]int64{}, unranked: []int64{1000, 1000}}, // both restate ts_wall
	}
	e := &event.Event{ID: "E1", Kind: "metric.anomaly", TSWall: 1000, Count: 2}
	out := unfoldEvents([]*event.Event{e}, known, map[string]int{})
	if len(out) != 1 {
		t.Fatalf("got %d copies, want 1 - both unranked values restate ts_wall and are not evidence of a second occurrence", len(out))
	}
}

// A value recovered at rank 3 has to land as the 3rd-smallest timestamp once
// every copy is re-sorted into one global timeline (main()'s own
// sort.Slice by TSWall) - not later, which is what a first version of this
// forward-fill got wrong by defaulting every unfilled slot back to ts_wall
// regardless of position, pushing a ranked value behind padding copies that
// an engine's greedy earliest-N selection would reach first instead.
func TestUnfoldEventsPlacesRankedEvidenceAtItsExactPosition(t *testing.T) {
	known := map[string]*recovered{
		"E1": {
			ranked: map[int]int64{3: 3000},
			// 1000 restates ts_wall (filtered, not a genuine 2nd occurrence);
			// 4000 is a real, later occurrence some other min_count-1 clause
			// happened to cite, with no way to know which rank it actually was.
			unranked: []int64{1000, 4000},
		},
	}
	e := &event.Event{ID: "E1", Kind: "metric.anomaly", TSWall: 1000, Count: 4}
	// Nothing about this group's own history needs a 4th copy (ranked tops out
	// at 3, and there is only one genuine unranked value) - a rule requiring
	// min_count 4 for this kind is what forces it, exactly like port-flapping's
	// min_count: 3 forces link.down's unfolding independent of history.
	out := unfoldEvents([]*event.Event{e}, known, map[string]int{"metric.anomaly": 4})

	if len(out) != 4 {
		t.Fatalf("got %d copies, want 4", len(out))
	}
	ts := make([]int64, len(out))
	for i, c := range out {
		ts[i] = c.TSWall
	}
	sort.Slice(ts, func(i, j int) bool { return ts[i] < ts[j] })
	want := []int64{1000, 1000, 3000, 4000}
	for i := range want {
		if ts[i] != want[i] {
			t.Errorf("sorted timestamps = %v, want %v (rank 3's evidenced value must be the 3rd-smallest)", ts, want)
			break
		}
	}
}

// This is the regression the fix in this package exists for. Replaying
// desktop/src/demo/demo-events.json against the real rules by calling
// engine.Offer once per RAW event - as cmd/netrewindd/writer.go actually
// does - rather than once per folded JSON entry, is what let port-flapping
// disappear entirely (1 incident -> 0) the first time this tool naively
// replayed a folded link.down group as a single Offer() call.
func TestUnfoldingAFoldedLinkDownGroupIsWhatLetsPortFlappingFireAgain(t *testing.T) {
	rules, err := correlate.LoadRules(filepath.Join("..", "..", "..", "rules"))
	if err != nil {
		t.Fatal(err)
	}

	folded := &event.Event{
		ID: "FOLDEDLINKDOWN", Kind: event.KindLinkDown, Source: event.SourceNetlink,
		Severity: event.SevWarn, Confidence: 100, ObserverID: "obs", SchemaV: event.SchemaVersion,
		Subject: event.Iface("eth0", 7), TSWall: 1_700_000_000_000_000_000, Count: 3,
	}

	naive := correlate.NewEngine(rules, quietLog())
	for _, inc := range naive.Offer(folded) {
		if inc.RuleID == "port-flapping" {
			t.Fatal("port-flapping fired from a single folded Offer() call; the bug this test pins no longer reproduces the way it is documented to")
		}
	}

	fixed := correlate.NewEngine(rules, quietLog())
	unfolded := unfoldEvents([]*event.Event{folded}, map[string]*recovered{}, maxMinCountByKind(rules))
	if len(unfolded) != 3 {
		t.Fatalf("unfolded to %d events, want 3", len(unfolded))
	}
	var fired bool
	for _, e := range unfolded {
		for _, inc := range fixed.Offer(e) {
			if inc.RuleID == "port-flapping" {
				fired = true
			}
		}
	}
	if !fired {
		t.Fatal("port-flapping did not fire even after unfolding to 3 separate events - the fix does not reproduce what a live engine would have concluded")
	}
}
