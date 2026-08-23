package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
)

func openTestStore(t *testing.T) *SQLite {
	t.Helper()
	st, err := OpenSQLite(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// build makes an event at a chosen wall time, so tests can lay out a timeline
// without waiting for one.
func build(b *event.Builder, kind event.Kind, iface string, at time.Time) *event.Event {
	e := b.New(event.SourceNetlink, kind, event.SevWarn, event.Iface(iface, 3))
	e.TSWall = at.UnixNano()
	return e
}

func TestAppendQueryRoundTrip(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	b := event.NewBuilder("obs-1", nil)

	want := b.New(event.SourceNetlink, event.KindLinkDown, event.SevWarn, event.Iface("eth1", 3)).
		WithAttr("cause", "carrier").
		WithAttr("down_duration_ms", 4200).
		WithEvidence("oper_state_before", "up").
		WithRelated(event.Observer("obs-1")).
		WithConfidence(90)

	if err := st.Append(ctx, want); err != nil {
		t.Fatalf("Append: %v", err)
	}

	got, err := st.Query(ctx, Filter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	g := got[0]

	if g.ID != want.ID || g.Kind != want.Kind || g.Source != want.Source {
		t.Errorf("identity not round-tripped: %+v", g)
	}
	if g.TSWall != want.TSWall || g.TSMono != want.TSMono {
		t.Errorf("timestamps not round-tripped: wall %d/%d mono %d/%d",
			g.TSWall, want.TSWall, g.TSMono, want.TSMono)
	}
	if g.Confidence != 90 {
		t.Errorf("confidence = %d, want 90", g.Confidence)
	}
	if g.Subject.Label != "eth1" || g.Subject.Attrs["ifindex"] != "3" {
		t.Errorf("subject not round-tripped: %+v", g.Subject)
	}
	if g.Attrs["cause"] != "carrier" {
		t.Errorf("attrs not round-tripped: %v", g.Attrs)
	}
	if g.Evidence["oper_state_before"] != "up" {
		t.Errorf("evidence not round-tripped: %v", g.Evidence)
	}
	if len(g.Related) != 1 || g.Related[0].ID != "obs-1" {
		t.Errorf("related not round-tripped: %v", g.Related)
	}
	if fmt.Sprint(g.Attrs["down_duration_ms"]) != "4200" {
		t.Errorf("numeric attr came back as %v", g.Attrs["down_duration_ms"])
	}
}

// Nanosecond timestamps, byte counters and interface counters all exceed the
// range a float64 can hold exactly. If they come back widened, the stored
// evidence is quietly wrong.
func TestLargeIntegersSurviveRoundTrip(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	b := event.NewBuilder("obs-1", nil)

	const bigNS int64 = 1787485379031531800
	e := b.New(event.SourceInternal, event.KindSystemGap, event.SevWarn, event.Observer("obs-1")).
		WithAttr("gap_start_ns", bigNS)
	if err := st.Append(ctx, e); err != nil {
		t.Fatalf("Append: %v", err)
	}

	got, err := st.Query(ctx, Filter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if want := strconv.FormatInt(bigNS, 10); fmt.Sprint(got[0].Attrs["gap_start_ns"]) != want {
		t.Errorf("gap_start_ns came back as %v, want %s", got[0].Attrs["gap_start_ns"], want)
	}
}

// A flapping interface must produce one growing event, not a row per flap.
// This is the cardinality decision the whole storage budget depends on.
func TestAppendFoldsRepeatsInsideTheWindow(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	b := event.NewBuilder("obs-1", nil)
	now := time.Now()

	for i := 0; i < 5; i++ {
		e := build(b, event.KindLinkDown, "eth1", now.Add(time.Duration(i)*time.Second))
		e.WithDedup("link.down|eth1")
		if err := st.Append(ctx, e); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}

	got, err := st.Query(ctx, Filter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1 folded row", len(got))
	}
	if got[0].Count != 5 {
		t.Errorf("Count = %d, want 5", got[0].Count)
	}
	// The folded row must still point at when the trouble started.
	if got[0].TSWall != now.UnixNano() {
		t.Errorf("folded event lost its first-occurrence time")
	}
}

func TestAppendStartsANewRowOutsideTheWindow(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	b := event.NewBuilder("obs-1", nil)
	now := time.Now()

	first := build(b, event.KindLinkDown, "eth1", now)
	first.WithDedup("link.down|eth1")
	later := build(b, event.KindLinkDown, "eth1", now.Add(FoldWindow+time.Minute))
	later.WithDedup("link.down|eth1")

	if err := st.Append(ctx, first, later); err != nil {
		t.Fatalf("Append: %v", err)
	}
	got, err := st.Query(ctx, Filter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2: the same fact an hour later is a new event", len(got))
	}
}

func TestQueryFilters(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	b := event.NewBuilder("obs-1", nil)
	now := time.Now()

	events := []*event.Event{
		build(b, event.KindLinkDown, "eth1", now.Add(-10*time.Minute)),
		build(b, event.KindLinkUp, "eth1", now.Add(-9*time.Minute)),
		build(b, event.KindARPBindingChanged, "eth2", now.Add(-5*time.Minute)),
		build(b, event.KindLinkDown, "eth2", now.Add(-1*time.Minute)),
	}
	if err := st.Append(ctx, events...); err != nil {
		t.Fatalf("Append: %v", err)
	}

	t.Run("by kind", func(t *testing.T) {
		got, err := st.Query(ctx, Filter{Kinds: []event.Kind{event.KindLinkDown}})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d, want 2", len(got))
		}
	})

	t.Run("by family", func(t *testing.T) {
		got, err := st.Query(ctx, Filter{Families: []string{"l2"}})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Kind != event.KindARPBindingChanged {
			t.Fatalf("got %v, want the one l2 event", got)
		}
	})

	t.Run("by subject label", func(t *testing.T) {
		got, err := st.Query(ctx, Filter{SubjectLabel: "eth2"})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d, want 2", len(got))
		}
	})

	t.Run("by window", func(t *testing.T) {
		got, err := st.Query(ctx, Filter{Since: now.Add(-6 * time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d, want 2", len(got))
		}
	})

	t.Run("ordered oldest first by default", func(t *testing.T) {
		got, err := st.Query(ctx, Filter{})
		if err != nil {
			t.Fatal(err)
		}
		for i := 1; i < len(got); i++ {
			if got[i].TSWall < got[i-1].TSWall {
				t.Fatalf("events came back out of order at %d", i)
			}
		}
	})

	t.Run("newest first on request", func(t *testing.T) {
		got, err := st.Query(ctx, Filter{Descending: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) < 2 || got[0].TSWall < got[1].TSWall {
			t.Fatal("descending order not applied")
		}
	})

	t.Run("limit is honoured", func(t *testing.T) {
		got, err := st.Query(ctx, Filter{Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 {
			t.Fatalf("got %d, want 1", len(got))
		}
	})
}

func TestPruneDropsOldHistoryOnly(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	b := event.NewBuilder("obs-1", nil)
	now := time.Now()

	old := build(b, event.KindLinkDown, "eth1", now.Add(-48*time.Hour))
	recent := build(b, event.KindLinkUp, "eth1", now.Add(-1*time.Hour))
	if err := st.Append(ctx, old, recent); err != nil {
		t.Fatalf("Append: %v", err)
	}

	n, err := st.Prune(ctx, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if n != 1 {
		t.Errorf("pruned %d events, want 1", n)
	}
	got, err := st.Query(ctx, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != event.KindLinkUp {
		t.Errorf("prune removed the wrong events: %v", got)
	}
}

func TestMetaSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.db")
	ctx := context.Background()

	st, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	if err := st.SetMeta(ctx, MetaLastHeartbeat, "12345"); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	st.Close()

	st2, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st2.Close()

	got, err := st2.GetMeta(ctx, MetaLastHeartbeat)
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	if got != "12345" {
		t.Errorf("heartbeat = %q, want %q - gap detection depends on this", got, "12345")
	}

	missing, err := st2.GetMeta(ctx, "never-set")
	if err != nil {
		t.Fatalf("GetMeta on missing key: %v", err)
	}
	if missing != "" {
		t.Errorf("missing key returned %q, want empty", missing)
	}
}

func TestAppendRejectsInvalidEvents(t *testing.T) {
	st := openTestStore(t)
	b := event.NewBuilder("obs-1", nil)
	bad := b.New(event.SourceNetlink, event.KindLinkUp, event.SevInfo, event.Iface("eth0", 1))
	bad.Kind = ""

	if err := st.Append(context.Background(), bad); err == nil {
		t.Error("Append accepted an invalid event")
	}
}
