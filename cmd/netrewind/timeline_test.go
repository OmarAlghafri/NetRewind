package main

import (
	"strings"
	"testing"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/incident"
)

// The ways someone actually types a moment when something has just broken. If
// any of these is rejected, the operator retypes it under pressure instead of
// reading the timeline.
func TestParseAtAcceptsHowPeopleType(t *testing.T) {
	now := time.Now()

	t.Run("now and empty", func(t *testing.T) {
		for _, in := range []string{"", "now", "  now  "} {
			got, err := parseAt(in)
			if err != nil {
				t.Fatalf("parseAt(%q): %v", in, err)
			}
			if d := got.Sub(now); d < -time.Minute || d > time.Minute {
				t.Errorf("parseAt(%q) = %v, want about now", in, got)
			}
		}
	})

	t.Run("offset back from now", func(t *testing.T) {
		got, err := parseAt("-2h")
		if err != nil {
			t.Fatalf("parseAt(-2h): %v", err)
		}
		if d := now.Sub(got); d < 119*time.Minute || d > 121*time.Minute {
			t.Errorf("parseAt(-2h) is %v ago, want about two hours", d)
		}
	})

	t.Run("time of day means today", func(t *testing.T) {
		got, err := parseAt("15:04")
		if err != nil {
			t.Fatalf("parseAt(15:04): %v", err)
		}
		if got.Hour() != 15 || got.Minute() != 4 {
			t.Errorf("parseAt(15:04) = %v", got)
		}
		if got.Year() != now.Year() || got.Day() != now.Day() {
			t.Errorf("parseAt(15:04) landed on %v, want today", got)
		}
	})

	t.Run("seconds included", func(t *testing.T) {
		got, err := parseAt("10:43:07")
		if err != nil {
			t.Fatalf("parseAt(10:43:07): %v", err)
		}
		if got.Hour() != 10 || got.Minute() != 43 || got.Second() != 7 {
			t.Errorf("parseAt(10:43:07) = %v", got)
		}
	})

	t.Run("absolute forms", func(t *testing.T) {
		for _, in := range []string{
			"2026-08-23T10:43:00Z",
			"2026-08-23 10:43:00",
			"2026-08-23 10:43",
			"2026-08-23",
		} {
			got, err := parseAt(in)
			if err != nil {
				t.Fatalf("parseAt(%q): %v", in, err)
			}
			if got.Year() != 2026 || got.Month() != time.August || got.Day() != 23 {
				t.Errorf("parseAt(%q) = %v", in, got)
			}
		}
	})
}

func TestParseAtRejectsNonsenseWithAUsefulMessage(t *testing.T) {
	for _, in := range []string{"yesterday", "half past ten", "-2 hours", "25:99"} {
		_, err := parseAt(in)
		if err == nil {
			t.Errorf("parseAt(%q) was accepted", in)
			continue
		}
		// The message has to name what is accepted, or the operator guesses.
		if !strings.Contains(err.Error(), "RFC3339") {
			t.Errorf("parseAt(%q) error does not say what is accepted: %v", in, err)
		}
	}
}

func TestSummariseIsStableAndSkipsTheSubject(t *testing.T) {
	e := &event.Event{Attrs: map[string]any{
		"ifname": "eth1", // already shown as the subject
		"cause":  "carrier",
		"admin":  false,
		"mtu":    1500,
	}}
	got := summarise(e.Attrs)

	if strings.Contains(got, "ifname") {
		t.Errorf("the subject was repeated in the detail column: %q", got)
	}
	if got != summarise(e.Attrs) {
		t.Error("two renders of the same attributes differed")
	}
	// Sorted, so two runs of the same query are comparable by eye.
	if !strings.HasPrefix(got, "admin=") {
		t.Errorf("attributes not in stable order: %q", got)
	}
}

func TestFilterSeverityKeepsTheFloorAndAbove(t *testing.T) {
	mk := func(s event.Severity) *event.Event { return &event.Event{Severity: s} }
	all := []*event.Event{mk(event.SevInfo), mk(event.SevNotice), mk(event.SevWarn), mk(event.SevError)}

	if got := filterSeverity(append([]*event.Event(nil), all...), "warn"); len(got) != 2 {
		t.Errorf("warn and above = %d events, want 2", len(got))
	}
	if got := filterSeverity(append([]*event.Event(nil), all...), "error"); len(got) != 1 {
		t.Errorf("error and above = %d events, want 1", len(got))
	}
	// info is the floor, so filtering by it must not drop anything.
	if got := filterSeverity(append([]*event.Event(nil), all...), "info"); len(got) != 4 {
		t.Errorf("info and above = %d events, want all 4", len(got))
	}
}

// what-happened merges several queries, one per address a machine answered to,
// so the same event can arrive more than once.
func TestDedupeKeepsOneOfEach(t *testing.T) {
	a := &event.Event{ID: "a"}
	b := &event.Event{ID: "b"}
	got := dedupe([]*event.Event{a, b, a, b, a})

	if len(got) != 2 {
		t.Fatalf("got %d events, want 2", len(got))
	}
	if got[0].ID != "a" || got[1].ID != "b" {
		t.Errorf("dedupe reordered events: %v, %v", got[0].ID, got[1].ID)
	}
}

func TestUniqueDropsBlanksAndRepeats(t *testing.T) {
	got := unique([]string{"10.0.0.5", "", "aa:bb", "10.0.0.5", ""})
	if len(got) != 2 || got[0] != "10.0.0.5" || got[1] != "aa:bb" {
		t.Errorf("unique = %v", got)
	}
}

func TestMarkersAreDistinguishable(t *testing.T) {
	seen := map[string]event.Severity{}
	for _, s := range []event.Severity{event.SevInfo, event.SevNotice, event.SevWarn, event.SevError} {
		m := marker(s)
		if prev, clash := seen[m]; clash {
			t.Errorf("%s and %s both render as %q", prev, s, m)
		}
		seen[m] = s
	}
	if marker(event.SevError) != "!!" || marker(event.SevWarn) != "!" {
		t.Error("the severity markers changed; screenshots and docs assume !! and !")
	}
}

// The relation between two links is the engine's central claim. Rendering two
// of them the same way would let a reader assume causation everywhere.
func TestRelationsReadDifferently(t *testing.T) {
	causes := relationArrow(incident.RelCauses)
	correlates := relationArrow(incident.RelCorrelates)
	precedes := relationArrow(incident.RelPrecedes)

	if causes == correlates || causes == precedes || correlates == precedes {
		t.Fatalf("relations are not distinguishable: %q %q %q", causes, correlates, precedes)
	}
	if !strings.Contains(causes, "caused") {
		t.Errorf("the causal relation does not say so: %q", causes)
	}
	if strings.Contains(correlates, "caused") || strings.Contains(precedes, "caused") {
		t.Error("a non-causal relation claims causation")
	}
}

func TestWrapBreaksOnWordsOnly(t *testing.T) {
	const text = "Find which switch port the new hardware address is learned on before changing anything."
	lines := wrap(text, 30)

	if len(lines) < 3 {
		t.Fatalf("wrapped to %d lines, expected several", len(lines))
	}
	for _, l := range lines {
		if len(l) > 30 && !strings.Contains(l, " ") {
			continue // a single word longer than the width has to overflow
		}
		if len(l) > 30 {
			t.Errorf("line exceeds the width: %q", l)
		}
	}
	if strings.Join(lines, " ") != text {
		t.Errorf("wrapping changed the text:\n%q\n%q", strings.Join(lines, " "), text)
	}
	if wrap("   ", 30) != nil {
		t.Error("blank text produced a line")
	}
}

func TestResolveWindow(t *testing.T) {
	t.Run("last is relative to now", func(t *testing.T) {
		from, to, err := resolveWindow(time.Hour, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if d := to.Sub(from); d < 59*time.Minute || d > 61*time.Minute {
			t.Errorf("window = %v, want an hour", d)
		}
	})

	t.Run("since overrides last", func(t *testing.T) {
		from, _, err := resolveWindow(time.Hour, "2026-08-23T10:00:00Z", "")
		if err != nil {
			t.Fatal(err)
		}
		if from.UTC().Hour() != 10 {
			t.Errorf("since ignored: %v", from)
		}
	})

	t.Run("a bad timestamp names the flag", func(t *testing.T) {
		if _, _, err := resolveWindow(time.Hour, "not-a-time", ""); err == nil ||
			!strings.Contains(err.Error(), "--since") {
			t.Errorf("error does not name the offending flag: %v", err)
		}
		if _, _, err := resolveWindow(time.Hour, "", "also-not"); err == nil ||
			!strings.Contains(err.Error(), "--until") {
			t.Errorf("error does not name the offending flag: %v", err)
		}
	})
}

// An empty window must not read as "the network was fine". It has to say that
// no record is not the same as nothing happening.
func TestAnEmptyTimelineSaysWhatItDoesNotKnow(t *testing.T) {
	var b strings.Builder
	now := time.Now()
	if err := renderTimeline(&b, nil, now.Add(-time.Hour), now); err != nil {
		t.Fatal(err)
	}
	out := b.String()

	if !strings.Contains(out, "nothing was recorded") {
		t.Errorf("empty window not stated plainly:\n%s", out)
	}
	if !strings.Contains(out, "system.gap") {
		t.Errorf("an empty window does not point at the reason it might be empty:\n%s", out)
	}
}

func TestTimelineIsOrderedAndNarrated(t *testing.T) {
	now := time.Now()
	b := event.NewBuilder("obs", nil)
	mk := func(kind event.Kind, at time.Time, attrs map[string]any) *event.Event {
		e := b.New(event.SourceNetlink, kind, event.SevWarn, event.Iface("eth1", 3))
		e.TSWall = at.UnixNano()
		for k, v := range attrs {
			e.WithAttr(k, v)
		}
		return e
	}
	// Deliberately out of order: the store returns sorted, but what-happened
	// merges several queries and must sort for itself.
	events := []*event.Event{
		mk(event.KindLinkUp, now.Add(-time.Minute), map[string]any{"down_duration_ms": 60000}),
		mk(event.KindLinkDown, now.Add(-2*time.Minute), map[string]any{"cause": "administrative"}),
	}

	var out strings.Builder
	if err := renderTimeline(&out, events, now.Add(-time.Hour), now); err != nil {
		t.Fatal(err)
	}
	text := out.String()

	down := strings.Index(text, "was shut down administratively")
	up := strings.Index(text, "came back after")
	if down < 0 || up < 0 {
		t.Fatalf("events not narrated in words:\n%s", text)
	}
	if down > up {
		t.Errorf("the timeline is out of order:\n%s", text)
	}
	// The elapsed column is what makes a timeline readable as a sequence.
	if !strings.Contains(text, "+1m0s") {
		t.Errorf("no elapsed time between events:\n%s", text)
	}
}
