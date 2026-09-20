package ai

import (
	"sort"

	"github.com/OmarAlghafri/netrewind/internal/incident"
)

// RankSimilar scores every candidate against target and returns the top k,
// ranked by how much a prior incident actually resembles this one - the
// "previously on this network" list the prompt offers as H-handles
// (HandleMap.AddHistory).
//
// Weights (1.2.0 plan): the deterministic engine agreeing on the rule is
// the strongest signal two incidents are "the same kind of thing" (4);
// agreeing on the root cause's kind or entity each add less (2 each,
// since either alone is a much weaker claim); the chain's overall shape
// contributes via Jaccard similarity of its kinds (0..1); and any shared
// victim machine adds a little more (1). Ties break newest-first: an
// operator asking "has this happened before" cares about the most recent
// precedent first. target itself is always excluded, matched by ID.
func RankSimilar(target *incident.Incident, candidates []*incident.Incident, k int) []*incident.Incident {
	if target == nil || k <= 0 {
		return nil
	}
	targetChainKinds := chainKindSet(target)
	targetVictims := stringSet(target.Victims)

	type scored struct {
		inc   *incident.Incident
		score float64
	}
	var pool []scored
	for _, c := range candidates {
		if c == nil || c.ID == target.ID {
			continue
		}
		score := 0.0
		if c.RuleID != "" && c.RuleID == target.RuleID {
			score += 4
		}
		if c.RootCause.Kind != "" && c.RootCause.Kind == target.RootCause.Kind {
			score += 2
		}
		if c.RootCause.Entity != "" && c.RootCause.Entity == target.RootCause.Entity {
			score += 2
		}
		score += jaccard(chainKindSet(c), targetChainKinds)
		if overlaps(stringSet(c.Victims), targetVictims) {
			score += 1
		}
		pool = append(pool, scored{c, score})
	}

	sort.SliceStable(pool, func(i, j int) bool {
		if pool[i].score != pool[j].score {
			return pool[i].score > pool[j].score
		}
		return pool[i].inc.OpenedAt > pool[j].inc.OpenedAt
	})
	if len(pool) == 0 {
		return nil
	}
	if len(pool) > k {
		pool = pool[:k]
	}
	out := make([]*incident.Incident, len(pool))
	for i, s := range pool {
		out[i] = s.inc
	}
	return out
}

func chainKindSet(inc *incident.Incident) map[string]bool {
	s := make(map[string]bool, len(inc.Chain))
	for _, link := range inc.Chain {
		if link.Kind != "" {
			s[string(link.Kind)] = true
		}
	}
	return s
}

func stringSet(items []string) map[string]bool {
	s := make(map[string]bool, len(items))
	for _, i := range items {
		s[i] = true
	}
	return s
}

// jaccard is |a∩b| / |a∪b|, 0 when both sets are empty (no chain shape to
// compare is not evidence of similarity).
func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	inter := 0
	for k := range a {
		if b[k] {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

func overlaps(a, b map[string]bool) bool {
	for k := range a {
		if b[k] {
			return true
		}
	}
	return false
}
