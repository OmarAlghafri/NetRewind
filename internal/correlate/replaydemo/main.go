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
//
// It reads the demo-incidents.json already on disk before overwriting it -
// see unfoldEvents's own doc comment for why a prior run's own chain data
// is a real, load-bearing input to a correct regeneration, not just a file
// this command happens to replace.
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
	rawCount := len(events)

	rules, err := correlate.LoadRules(filepath.Join(root, "rules"))
	if err != nil {
		fail(err)
	}

	dest := filepath.Join(root, "desktop", "src", "demo", "demo-incidents.json")
	known, err := loadKnownTimestamps(dest)
	if err != nil {
		fail(fmt.Errorf("read existing %s: %w", dest, err))
	}
	events = unfoldEvents(events, known, maxMinCountByKind(rules))

	// The writer pipeline (cmd/netrewindd/writer.go) offers events to the
	// engine in the order they were flushed, which is arrival order - always
	// chronological for a live recorder. demo-events.json should already be
	// in this order (it is itself a `-o json` export in ts_wall order), but
	// sorting explicitly makes that an enforced fact instead of an assumption
	// carried over from the file's history. Unfolding first, then sorting,
	// so a folded event's copies land correctly relative to every other
	// event even though several may share a timestamp with each other.
	sort.Slice(events, func(i, j int) bool { return events[i].TSWall < events[j].TSWall })

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	engine := correlate.NewEngine(rules, log)

	var order []string
	byID := make(map[string]*incident.Incident)
	for _, e := range events {
		for _, inc := range engine.Offer(e) {
			upsertIncident(byID, &order, inc)
		}
	}
	incidents := make([]*incident.Incident, len(order))
	for i, id := range order {
		incidents[i] = byID[id]
	}

	out, err := json.MarshalIndent(incidents, "", "  ")
	if err != nil {
		fail(err)
	}
	out = append(out, '\n')

	if err := os.WriteFile(dest, out, 0o644); err != nil {
		fail(err)
	}
	fmt.Printf("wrote %d incidents (from %d raw events, %d after unfolding) to %s\n",
		len(incidents), rawCount, len(events), dest)
}

// upsertIncident replicates internal/store/incident_sqlite.go's own
// AppendIncidents upsert exactly - "INSERT ... ON CONFLICT(incident_id) DO
// UPDATE SET closed_at, status, severity, confidence, root_cause, chain,
// victims" - deliberately NOT touching opened_at, title, rule_id, or
// advice on a repeat fire for the same ID.
//
// Found necessary because internal/correlate/engine.go's own dedup
// (`e.fired[key]`, "Same conclusion, better evidence: keep the identity so
// the fuller account replaces the thinner one instead of joining it")
// means engine.Offer can legitimately return more than one *incident.Incident
// for the same rule/subject as later events complete an optional clause a
// thinner earlier match had left unmet - correctly reusing the same
// Incident.ID both times. A live recorder never notices this happening
// twice for one ID because the store's own upsert quietly collapses it to
// one row; a replay tool that just appends every value Offer() returns
// does not get that for free and ends up with duplicate incident_ids
// carrying different content, which regenerating demo-incidents.json from
// this tool's very first append-only version actually did (found by
// comparing incident_id uniqueness in the output, not assumed).
//
// opened_at is the one field where this matters for real: it is computed
// from Chain[0].At (engine.go's build()), and a fuller second match can
// have an earlier Chain[0] than the first, thin match did (an optional
// clause landing earlier in time than the anchor). The real store's own
// schema explicitly excludes opened_at from its UPDATE SET for exactly
// this reason - an incident's opened time is fixed at first report, not
// revised backward each time more evidence arrives - so this function
// keeps the first incident's OpenedAt/Title/RuleID/Advice/ID and only
// takes ClosedAt/Status/Severity/Confidence/RootCause/Chain/Victims from
// whatever the latest call provides.
func upsertIncident(byID map[string]*incident.Incident, order *[]string, inc *incident.Incident) {
	prev, exists := byID[inc.ID]
	if !exists {
		byID[inc.ID] = inc
		*order = append(*order, inc.ID)
		return
	}
	inc.OpenedAt = prev.OpenedAt
	inc.Title = prev.Title
	inc.RuleID = prev.RuleID
	inc.Advice = prev.Advice
	byID[inc.ID] = inc
}

// recovered is what a prior demo-incidents.json's own chain data revealed
// about one folded event's individual raw occurrences - see
// loadKnownTimestamps and unfoldEvents for how each field is used and why
// they need different treatment.
type recovered struct {
	// ranked holds a timestamp this tool can place at an EXACT
	// chronological position with confidence, keyed 1 = the group's first
	// occurrence. Only Incident.ClosedAt combined with its clause's own
	// Evidence["matched_count"] earns a rank: that pairing names exactly
	// which occurrence (the N-th) a min_count clause's greedy earliest-N
	// selection actually stopped at.
	ranked map[int]int64
	// unranked holds a timestamp known to be real (some incident cited it
	// via Link.At) but whose position within the group is not itself
	// evidenced - true whenever the citing clause matched only one event
	// (min_count 1, the ordinary case), where engine.go's build() never
	// records a matched_count at all. Placed late rather than guessed at
	// rank 1: a min_count 1 clause does not care which offered copy
	// satisfies it, so any position that keeps ranked evidence intact is
	// equally correct, and placing it early risks displacing a value a
	// *different*, more precise piece of evidence needs at that rank.
	unranked []int64
}

// loadKnownTimestamps reads whatever demo-incidents.json is already on
// disk (the file this command is about to overwrite) for real evidence of
// what a folded event's individual raw occurrences actually were - see
// unfoldEvents's own doc comment for why this file is a load-bearing
// input to a correct regeneration, not just the output being replaced. A
// file that does not exist yet (first-ever run) is not an error - an
// empty map, meaning unfoldEvents falls back to its own no-evidence
// default for every folded event.
func loadKnownTimestamps(path string) (map[string]*recovered, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]*recovered{}, nil
	}
	if err != nil {
		return nil, err
	}
	var incidents []*incident.Incident
	if err := json.Unmarshal(data, &incidents); err != nil {
		return nil, err
	}
	known := make(map[string]*recovered)
	get := func(id string) *recovered {
		if known[id] == nil {
			known[id] = &recovered{ranked: map[int]int64{}}
		}
		return known[id]
	}
	for _, inc := range incidents {
		for _, link := range inc.Chain {
			if link.EventID == "" {
				continue
			}
			get(link.EventID).unranked = append(get(link.EventID).unranked, link.At)
		}
		last := len(inc.Chain)
		if last == 0 {
			continue
		}
		link := inc.Chain[last-1]
		mc, ok := link.Evidence["matched_count"]
		if !ok || link.EventID == "" {
			continue
		}
		var rank int
		switch v := mc.(type) {
		case float64: // json.Unmarshal's numeric type for interface{}
			rank = int(v)
		case int:
			rank = v
		}
		if rank >= 1 {
			get(link.EventID).ranked[rank] = inc.ClosedAt
		}
	}
	return known, nil
}

// unfoldEvents expands every event whose Count is greater than 1 back into
// that many separate copies - see this file's own top-of-file comment for
// why demo-events.json's folded entries are not equivalent to what the
// engine actually saw live: cmd/netrewindd/writer.go calls engine.Offer
// once per RAW event in a flush batch, before internal/store's Append ever
// runs, so live correlation sees every occurrence separately - folding
// (internal/store/sqlite.go's foldSQL, "UPDATE events SET count = count +
// ?, ts_last = ? ...") only ever affects what a later *query* returns.
// Replaying a folded entry as a single Offer() call silently drops
// (count-1) occurrences the live engine did see, which matters directly to
// any rule.Clause with min_count > 1 (rules/port-flapping.yaml's
// `min_count: 3` on link.down is exactly such a clause) and, because the
// engine's window is shared and cumulative across all rules, can shift
// other rules' matches too.
//
// Each copy shares the folded entry's EventID: they are, as far as the
// store and the frontend are concerned, "the same event" - that is the
// definition of folding (confirmed directly in internal/store/sqlite.go's
// own Append: a folded event's ID is rewritten to the survivor's before
// anything downstream, including the engine, ever sees it), and every
// consumer of the eventual incidents.json chain looks an event up by this
// ID against the unchanged demo-events.json, which still has exactly one
// entry per dedup group. Each copy's own Count is reset to 1: it now
// represents one individual raw occurrence, and nothing in
// internal/correlate reads Count for anything other than
// rule.Clause.MinCount, which is about how many *separate events* a clause
// requires, not a single event's own fold count.
//
// The one thing folding genuinely destroys is timing: internal/store only
// ever persists ts_wall of the first occurrence and ts_last of the latest
// (a store-internal column with no equivalent field on event.Event
// itself), so an export like `netrewind events -o json` cannot say when
// the 2nd, 3rd, ... occurrence of a folded group actually happened, and no
// single uniform guess at the gap works: swept every spacing from 0 to
// internal/store's own 60s FoldWindow directly against the real,
// live-captured original demo-incidents.json, and every value left at
// least one rule wrong - different folded groups plainly had different
// real internal spacings, which is exactly the information folding is
// designed to discard, not evidence of a second bug.
//
// What actually is recoverable, and how it is placed:
//
// Every copy starts at ts_wall (rank 1, always real). loadKnownTimestamps'
// ranked evidence (Incident.ClosedAt keyed by its clause's own
// Evidence["matched_count"], which names exactly which occurrence - the
// N-th - a min_count clause's greedy earliest-N selection actually
// stopped at) is applied by forward-filling: walking ranks 1..n in order,
// jumping to a ranked value where one is known and otherwise carrying the
// last known value forward, so a value at rank 3 lands with exactly 2
// smaller values before it once every copy is re-sorted into one global
// timeline in main() - not 3, which would push it past where a min_count
// 3 clause's greedy selection ever looks. unranked evidence (a bare
// Link.At from a min_count-1 clause, where build() never sets
// matched_count at all, so there is no rank to trust) is assigned to the
// highest slot not already claimed by ranked evidence: a min_count 1
// clause does not care which offered copy satisfies it, so placing it
// late costs nothing and cannot displace a precisely-ranked value
// elsewhere. A recovered value equal to ts_wall itself is dropped before
// any of this - it is rank 1's own baseline restated (every clause's
// Link.At is its lead event, which for a folded group's own min_count
// clause is ts_wall by definition), not evidence of a second occurrence,
// and keeping it as "unranked, assume late" would silently reintroduce
// the same misplacement ranked evidence exists to prevent.
//
// n itself - how many copies a group actually gets - is not always
// foldCount either. A copy invented beyond what anything requires is a
// spurious event with nowhere principled to sit, free to be picked up by
// a *different*, unrelated clause that accepts the same kind with no
// further restriction: rules/reachability-lost.yaml's optional "recovery"
// clause (min_count 1, unrestricted beyond kind: metric.anomaly) greedily
// matched a synthetic second copy of its own "loss" anchor's group
// instead of reaching the real, distinct metric.anomaly event the
// original incident used, once that anchor was unfolded to its full,
// unnecessary foldCount of 2. The live engine never had this problem:
// every raw occurrence's own distinct real timestamp kept it from
// colliding with an unrelated match the way synthetic copies at
// duplicate instants can. n is therefore the largest of: the highest rank
// any recovered evidence names outright; one slot per genuinely new
// (post-filtering) unranked value; and, independent of this group's own
// history entirely, maxMinCount's answer for this event's kind - a rule's
// own requirement (rules/port-flapping.yaml's `min_count: 3` on
// link.down, for a link.down event with no usable historical evidence of
// its own beyond ts_wall) is reason enough by itself to keep unfolding it
// - capped at foldCount, since a group never really had more raw
// occurrences than that.
//
// All three fixes were found the same way: regenerating, then diffing
// the result against the real, live-captured original programmatically,
// field by field, not by matching incident counts alone. The result now
// reproduces that original exactly (modulo one separate, genuine
// determinism fix to Victims ordering - see internal/correlate/engine.go's
// own comment on that) - see the evidence log for this fix for the full
// trail. This is why this command reads its own prior output first: each
// regeneration preserves whatever real timing evidence the previous one
// captured, rather than discarding it every time. A rule or engine change
// that alters which events end up cited will still lose timing
// information for occurrences no incident happens to reference - an
// inherent limit of what a folded, query-time export can ever reconstruct,
// not something a future run of this tool can solve by itself.
func unfoldEvents(events []*event.Event, known map[string]*recovered, maxMinCount map[string]int) []*event.Event {
	out := make([]*event.Event, 0, len(events))
	for _, e := range events {
		foldCount := int(e.Count)
		if foldCount < 1 {
			foldCount = 1
		}
		if foldCount == 1 {
			cp := *e
			out = append(out, &cp)
			continue
		}

		rec := known[e.ID]
		ranked := map[int]int64{}
		var unranked []int64
		if rec != nil {
			ranked = rec.ranked
			// A value equal to ts_wall is rank 1's own baseline restated,
			// not new evidence of a later occurrence - Link.At is recorded
			// for EVERY citing clause, including a min_count>1 one whose
			// own lead event is, by definition, ts_wall again. Treating it
			// as "unranked, so assume late" placed a second copy of ts_wall
			// at the group's LAST slot, one rank past a real min_count>1
			// clause's own precisely-ranked ClosedAt value at that same
			// slot - silently overwriting the correct, evidenced value with
			// a redundant, wrongly-late one. Found the same way as the
			// ClosedAt-ranking fix itself: regenerating, diffing
			// programmatically against the original, and tracing exactly
			// why a value that WAS being recovered correctly (confirmed by
			// printing loadKnownTimestamps's own output) still never
			// reached the final incident.
			for _, v := range rec.unranked {
				if v != e.TSWall {
					unranked = append(unranked, v)
				}
			}
		}

		// n is how many copies THIS event actually needs, which is not
		// always foldCount - the historical fold count says how many raw
		// occurrences existed, but a copy this tool invents beyond what
		// anything actually requires is a spurious event with nowhere
		// principled to sit, free to be picked up by a *different*,
		// unrelated clause that happens to accept the same kind with no
		// further restriction (rules/reachability-lost.yaml's optional
		// "recovery" clause, itself only min_count 1 and un-restricted
		// beyond kind, greedily matched a synthetic second copy of its own
		// "loss" anchor's group instead of reaching the real, distinct
		// metric.anomaly event the original incident used - found the same
		// way as every other case here, by regenerating and diffing
		// programmatically against the original). The real live engine
		// never had this problem: every raw occurrence's own natural,
		// distinct timestamp kept it from colliding with an unrelated
		// match the way two copies placed at the identical instant can.
		//
		// What this tool actually needs is bounded by three things, and
		// never more than foldCount (this group never really had more raw
		// occurrences than that): the highest rank any recovered evidence
		// names outright; one slot per genuinely new (non-ts_wall)
		// unranked value; and separately, independent of any of this
		// specific group's own history, the largest min_count any loaded
		// rule's clause asks of this event's kind at all (rules/port-
		// flapping.yaml's `min_count: 3` on link.down, for a link.down
		// event that happens to have no usable historical evidence of its
		// own beyond ts_wall - the rule's own requirement is reason enough
		// on its own to keep unfolding it).
		n := maxMinCount[string(e.Kind)]
		for r := range ranked {
			if r > n {
				n = r
			}
		}
		if u := len(unranked) + 1; u > n {
			n = u
		}
		if n < 1 {
			n = 1
		}
		if n > foldCount {
			n = foldCount
		}
		if n == 1 {
			cp := *e
			out = append(out, &cp)
			continue
		}

		// Unranked evidence claims the highest rank not already spoken for
		// by a precise, ranked one, so it never displaces a value the more
		// trustworthy evidence needs to stay exactly where it is.
		claimed := make(map[int]bool, len(ranked))
		for r := range ranked {
			claimed[r] = true
		}
		assigned := map[int]int64{}
		slot := n
		for _, v := range unranked {
			for slot >= 1 && claimed[slot] {
				slot--
			}
			if slot < 1 {
				break // more unranked evidence than spare slots past what ranked evidence already claimed
			}
			assigned[slot] = v
			claimed[slot] = true
			slot--
		}

		ts := make([]int64, n)
		last := e.TSWall
		for i := 0; i < n; i++ {
			rank := i + 1
			if v, ok := ranked[rank]; ok {
				last = v
			} else if v, ok := assigned[rank]; ok {
				last = v
			}
			ts[i] = last
		}

		for _, t := range ts {
			cp := *e
			cp.Count = 1
			cp.TSWall = t
			out = append(out, &cp)
		}
	}
	return out
}

// maxMinCountByKind is, for every event kind any loaded rule's clause
// matches at all, the largest min_count any such clause asks for - the
// independent-of-history floor unfoldEvents keeps a group unfolded to,
// because a rule's own requirement (rules/port-flapping.yaml's `min_count:
// 3` on link.down) is reason enough by itself, with no historical evidence
// needed to justify it.
func maxMinCountByKind(rules []*correlate.Rule) map[string]int {
	out := map[string]int{}
	for _, r := range rules {
		for _, c := range r.Match {
			need := c.MinCount
			if need < 1 {
				need = 1
			}
			for _, k := range c.Kinds {
				if need > out[k] {
					out[k] = need
				}
			}
		}
	}
	return out
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
