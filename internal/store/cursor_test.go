package store

import (
	"context"
	"testing"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/incident"
)

// A cursor's basic job: a second Query using the cursor from the first
// returns only what came after it, not the whole window again.
func TestQueryCursorExcludesAlreadySeenRows(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	b := event.NewBuilder("obs-1", nil)
	now := time.Now()

	first := build(b, event.KindLinkDown, "eth0", now)
	second := build(b, event.KindLinkUp, "eth0", now.Add(1*time.Second))
	if err := st.Append(ctx, first, second); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// A cursor positioned exactly at the first event excludes it and
	// returns only the second.
	got, err := st.Query(ctx, Filter{Cursor: &Cursor{TSWall: first.TSWall, EventID: first.ID, After: now.UnixNano()}})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 1 || got[0].ID != second.ID {
		t.Fatalf("got %d events, want exactly the second one (id=%s); got=%v", len(got), second.ID, got)
	}

	// A cursor positioned at the second (latest) event excludes both.
	got, err = st.Query(ctx, Filter{Cursor: &Cursor{TSWall: second.TSWall, EventID: second.ID, After: now.Add(2 * time.Second).UnixNano()}})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d events past the latest cursor, want 0; got=%v", len(got), got)
	}
}

// The scenario ADR 0005 and the execution order both call out by name: a
// repeated event folds into its existing row (same dedup_key, inside
// FoldWindow) rather than inserting a new one. Its ts_wall never moves -
// only ts_last and count do - so a cursor watching only the keyset
// (ts_wall, event_id) would never notice the row changed at all. This test
// fails if that fold-detection clause (the "OR ts_last > cursor.After"
// third arm in SQLite.Query) is ever removed or broken.
func TestQueryCursorCatchesAFoldedRowsCountChange(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	b := event.NewBuilder("obs-1", nil)
	now := time.Now()

	flap := build(b, event.KindLinkDown, "eth1", now)
	flap.WithDedup("link.down|eth1")
	if err := st.Append(ctx, flap); err != nil {
		t.Fatalf("Append (first flap): %v", err)
	}

	// A poll right after the first flap: sees it once, count 1. The cursor
	// it takes away has TSWall/EventID at this row and After set to "now",
	// a watermark strictly before the fold that is about to happen.
	firstPoll, err := st.Query(ctx, Filter{})
	if err != nil {
		t.Fatalf("Query (first poll): %v", err)
	}
	if len(firstPoll) != 1 || firstPoll[0].Count != 1 {
		t.Fatalf("first poll: got %+v, want exactly one event with count 1", firstPoll)
	}
	cursorAfterFirstPoll := Cursor{TSWall: firstPoll[0].TSWall, EventID: firstPoll[0].ID, After: now.UnixNano()}

	// The interface keeps flapping - a second occurrence of the same fact,
	// inside FoldWindow, folds into the existing row: ts_wall stays put,
	// ts_last and count move.
	secondFlap := build(b, event.KindLinkDown, "eth1", now.Add(5*time.Second))
	secondFlap.WithDedup("link.down|eth1")
	if err := st.Append(ctx, secondFlap); err != nil {
		t.Fatalf("Append (second flap): %v", err)
	}

	// A naive keyset-only cursor (ts_wall, event_id) would see nothing new
	// here, because the folded row's ts_wall never changed. The real
	// delta poll, using the cursor from before the fold, must see it.
	delta, err := st.Query(ctx, Filter{Cursor: &cursorAfterFirstPoll})
	if err != nil {
		t.Fatalf("Query (delta poll): %v", err)
	}
	if len(delta) != 1 {
		t.Fatalf("delta poll: got %d rows, want exactly 1 (the folded row, updated) - "+
			"a fold was silently missed", len(delta))
	}
	if delta[0].ID != firstPoll[0].ID {
		t.Fatalf("delta poll returned a different row (id=%s) than the one that folded (id=%s)",
			delta[0].ID, firstPoll[0].ID)
	}
	if delta[0].Count != 2 {
		t.Fatalf("delta poll: folded row's Count = %d, want 2 (the fold's whole point)", delta[0].Count)
	}

	// And a cursor taken *after* the fold (After moved forward past it)
	// correctly sees nothing further - the delta mechanism does not
	// re-report the same fold forever.
	afterTheFold := Cursor{TSWall: delta[0].TSWall, EventID: delta[0].ID, After: now.Add(10 * time.Second).UnixNano()}
	empty, err := st.Query(ctx, Filter{Cursor: &afterTheFold})
	if err != nil {
		t.Fatalf("Query (post-fold poll): %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("post-fold poll: got %d rows, want 0 (nothing new since the watermark moved past the fold)", len(empty))
	}
}

func TestQueryIncidentsCursorExcludesAlreadySeenRows(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	now := time.Now()

	mk := func(id string, at time.Time) *incident.Incident {
		return &incident.Incident{
			ID: id, OpenedAt: at.UnixNano(), Status: incident.StatusClosed,
			Title: "t", Severity: event.SevWarn, Confidence: 80, RuleID: "r",
			Chain: []incident.Link{{EventID: "e", At: at.UnixNano()}},
		}
	}
	first := mk("01AAAAAAAAAAAAAAAAAAAAAAAA", now)
	second := mk("01BBBBBBBBBBBBBBBBBBBBBBBB", now.Add(1*time.Minute))
	if err := st.AppendIncidents(ctx, first, second); err != nil {
		t.Fatalf("AppendIncidents: %v", err)
	}

	got, err := st.QueryIncidents(ctx, IncidentFilter{Cursor: &IncidentCursor{OpenedAt: first.OpenedAt, ID: first.ID}})
	if err != nil {
		t.Fatalf("QueryIncidents: %v", err)
	}
	if len(got) != 1 || got[0].ID != second.ID {
		t.Fatalf("got %d incidents, want exactly the second one (id=%s); got=%v", len(got), second.ID, got)
	}
}
