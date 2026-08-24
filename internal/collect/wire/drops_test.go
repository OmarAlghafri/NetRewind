package wire

import (
	"testing"

	"github.com/OmarAlghafri/netrewind/internal/event"
)

func TestDropEventNamesTheHole(t *testing.T) {
	b := event.NewBuilder("obs-1", nil)
	e := dropEvent(b, "wire", "eth0", 900, 100)

	if e.Kind != event.KindSystemDrop {
		t.Errorf("kind = %s, want %s", e.Kind, event.KindSystemDrop)
	}
	if e.Source != event.SourceInternal {
		t.Errorf("source = %s, want the recorder reporting on itself", e.Source)
	}
	if got := e.Attrs["dropped"]; got != int64(100) {
		t.Errorf("dropped = %v, want 100", got)
	}
	if got := e.Attrs["received"]; got != int64(900) {
		t.Errorf("received = %v, want 900", got)
	}
	if got := e.Attrs["iface"]; got != "eth0" {
		t.Errorf("iface = %v, want eth0", got)
	}
	// A tenth of the frames lost is past the point where what was seen can be
	// treated as the whole story.
	if e.Severity != event.SevError {
		t.Errorf("severity = %s for a 10%% loss, want error", e.Severity)
	}
	if got := e.Evidence["drop_share"]; got != 0.1 {
		t.Errorf("drop_share = %v, want 0.1", got)
	}
}

func TestASmallDropIsAWarningNotAnError(t *testing.T) {
	b := event.NewBuilder("obs-1", nil)
	e := dropEvent(b, "wire", "eth0", 99999, 1)

	if e.Severity != event.SevWarn {
		t.Errorf("severity = %s for one frame in a hundred thousand, want warn", e.Severity)
	}
}

// The counters are read from the kernel and a pathological reading must not
// crash the recorder that exists to survive pathological moments.
func TestDropEventSurvivesZeroTraffic(t *testing.T) {
	b := event.NewBuilder("obs-1", nil)
	e := dropEvent(b, "wire", "", 0, 0)

	if got := e.Evidence["drop_share"]; got != 0.0 {
		t.Errorf("drop_share = %v with no traffic at all, want 0", got)
	}
	if got := e.Attrs["iface"]; got != "all" {
		t.Errorf("iface = %v when unset, want all", got)
	}
	if err := e.Validate(); err != nil {
		t.Errorf("the event the store would reject: %v", err)
	}
}

// Every drop in a window folds into one row rather than one per sweep, so a
// segment that is dropping continuously does not bury the events it did catch.
func TestDropEventsFoldTogether(t *testing.T) {
	b := event.NewBuilder("obs-1", nil)
	first := dropEvent(b, "wire", "eth0", 100, 5)
	second := dropEvent(b, "wire", "eth0", 100, 7)

	if first.DedupKey == "" {
		t.Fatal("drop events carry no dedup key, so a dropping segment would flood the store")
	}
	if first.DedupKey != second.DedupKey {
		t.Errorf("dedup keys differ (%q vs %q); repeats would not fold",
			first.DedupKey, second.DedupKey)
	}
}
