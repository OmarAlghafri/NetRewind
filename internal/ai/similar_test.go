package ai

import (
	"testing"

	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/incident"
)

func inc(id, ruleID, kind, entity string, openedAt int64, chainKinds []string, victims []string) *incident.Incident {
	var chain []incident.Link
	for _, k := range chainKinds {
		chain = append(chain, incident.Link{Kind: event.Kind(k)})
	}
	return &incident.Incident{
		ID: id, RuleID: ruleID, OpenedAt: openedAt, Victims: victims,
		RootCause: incident.RootCause{Kind: event.Kind(kind), Entity: entity},
		Chain:     chain,
	}
}

func TestRankSimilarExcludesTheTargetItself(t *testing.T) {
	target := inc("t", "r", "k", "e", 100, []string{"k"}, nil)
	got := RankSimilar(target, []*incident.Incident{target, inc("other", "r", "k", "e", 90, []string{"k"}, nil)}, 3)
	for _, c := range got {
		if c.ID == "t" {
			t.Error("RankSimilar returned the target itself")
		}
	}
	if len(got) != 1 {
		t.Fatalf("got %d results, want 1 (target excluded)", len(got))
	}
}

// TestRankSimilarWeighsRuleMatchAboveKindMatch checks the plan's own
// ordering among the individual signals (rule=4 > kind=2=entity),
// isolating just those two rather than combining every signal at once,
// where a wrong weight could cancel out against another and pass by
// coincidence (see the guardrail work's own lesson on that).
func TestRankSimilarWeighsRuleMatchAboveKindMatch(t *testing.T) {
	target := inc("t", "gateway-hijack", "l2.arp_binding_changed", "10.0.0.1", 1000, nil, nil)
	sameRuleOnly := inc("a", "gateway-hijack", "different.kind", "different-entity", 1, nil, nil)
	sameKindOnly := inc("b", "different-rule", "l2.arp_binding_changed", "different-entity", 1, nil, nil)

	got := RankSimilar(target, []*incident.Incident{sameKindOnly, sameRuleOnly}, 2)
	if len(got) != 2 || got[0].ID != "a" {
		t.Fatalf("got %v, want same-rule-only (weight 4) ranked above same-kind-only (weight 2)", ids(got))
	}
}

// TestRankSimilarCombinesSignalsAdditively proves the weights actually add
// rather than the first match short-circuiting the rest: an incident
// matching on kind+entity+chain+victims together (2+2+1+1=6) outranks one
// matching on rule alone (4), since the plan's weights are a sum, not a
// priority order.
func TestRankSimilarCombinesSignalsAdditively(t *testing.T) {
	target := inc("t", "gateway-hijack", "l2.arp_binding_changed", "10.0.0.1", 1000, []string{"l2.arp_binding_changed"}, []string{"10.0.0.1"})
	sameRuleOnly := inc("a", "gateway-hijack", "different.kind", "different-entity", 1, nil, nil)
	sameEverythingElseButRule := inc("b", "different-rule", "l2.arp_binding_changed", "10.0.0.1", 999, []string{"l2.arp_binding_changed"}, []string{"10.0.0.1"})

	got := RankSimilar(target, []*incident.Incident{sameRuleOnly, sameEverythingElseButRule}, 2)
	if len(got) != 2 || got[0].ID != "b" {
		t.Fatalf("got %v, want the combined-signal incident (score 6) ranked above rule-only (score 4)", ids(got))
	}
}

func TestRankSimilarBreaksTiesNewestFirst(t *testing.T) {
	target := inc("t", "r", "k", "e", 1000, nil, nil)
	older := inc("older", "r", "k", "e", 500, nil, nil)
	newer := inc("newer", "r", "k", "e", 900, nil, nil)

	got := RankSimilar(target, []*incident.Incident{older, newer}, 2)
	if len(got) != 2 || got[0].ID != "newer" || got[1].ID != "older" {
		t.Fatalf("got %v, want [newer, older] for a tied score", ids(got))
	}
}

func TestRankSimilarRespectsLimit(t *testing.T) {
	target := inc("t", "r", "k", "e", 1000, nil, nil)
	var candidates []*incident.Incident
	for i := 0; i < 5; i++ {
		candidates = append(candidates, inc(string(rune('a'+i)), "r", "k", "e", int64(i), nil, nil))
	}
	if got := RankSimilar(target, candidates, 3); len(got) != 3 {
		t.Errorf("got %d results, want the limit of 3", len(got))
	}
}

func TestRankSimilarWithNoCandidatesReturnsNil(t *testing.T) {
	target := inc("t", "r", "k", "e", 1000, nil, nil)
	if got := RankSimilar(target, nil, 3); got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestJaccardOfIdenticalChainShapesIsOne(t *testing.T) {
	a := chainKindSet(inc("a", "", "", "", 0, []string{"link.down", "l2.arp_binding_changed"}, nil))
	b := chainKindSet(inc("b", "", "", "", 0, []string{"link.down", "l2.arp_binding_changed"}, nil))
	if got := jaccard(a, b); got != 1 {
		t.Errorf("jaccard of identical sets = %v, want 1", got)
	}
}

func TestJaccardOfDisjointChainShapesIsZero(t *testing.T) {
	a := chainKindSet(inc("a", "", "", "", 0, []string{"link.down"}, nil))
	b := chainKindSet(inc("b", "", "", "", 0, []string{"l3.route_changed"}, nil))
	if got := jaccard(a, b); got != 0 {
		t.Errorf("jaccard of disjoint sets = %v, want 0", got)
	}
}

func ids(incidents []*incident.Incident) []string {
	out := make([]string, len(incidents))
	for i, c := range incidents {
		out[i] = c.ID
	}
	return out
}
