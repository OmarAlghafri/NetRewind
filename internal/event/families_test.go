package event_test

import (
	"path/filepath"
	"testing"

	"github.com/OmarAlghafri/netrewind/internal/event"
)

// event.Families is written out by hand, and it is what the query tool checks
// --family against. If it drifts from the kinds themselves, one of two things
// happens: a family with events in it gets refused as unknown, or a family that
// no longer exists is silently accepted and matches nothing. The second is the
// dangerous one - it looks exactly like a network on which nothing happened.
//
// The kinds are read from source rather than a hand-written list, so this
// cannot drift in turn.
func TestTheFamilyListMatchesTheKinds(t *testing.T) {
	root := repoRoot(t)
	kinds := declaredKinds(t, filepath.Join(root, "internal", "event", "kinds.go"))

	inUse := map[string]bool{}
	for _, kind := range kinds {
		inUse[event.Kind(kind).Family()] = true
	}

	for _, f := range event.Families {
		if !inUse[f] {
			t.Errorf("event.Families lists %q, but no kind belongs to it", f)
		}
	}
	for f := range inUse {
		if !event.KnownFamily(f) {
			t.Errorf("kinds exist in family %q but event.Families omits it, so --family %s would be refused", f, f)
		}
	}
}
