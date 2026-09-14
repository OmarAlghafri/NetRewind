package analyze

import (
	"testing"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/ports"
)

// TestAdministrativeDisableSequence replays the exact re-read sequence the
// Windows IP Helper collector sees when an adapter is disabled and
// re-enabled: several parameter-change and delete notifications per
// transition, each re-read giving the same post-change state. Exactly one
// link.down and one link.up must come out, at the first observation that
// shows the change, not at a later repeat.
func TestAdministrativeDisableSequence(t *testing.T) {
	a := NewInterfaceAnalyzer(event.NewBuilder("t", nil))
	up := ports.InterfaceObservation{Index: 16, Name: "Ethernet 2", AdminUp: true, OperUp: true, MTU: 1500}
	down := up
	down.AdminUp, down.OperUp = false, false
	a.Seed(up)

	at := time.Now()
	seq := []ports.InterfaceObservation{up, down, down, down, down, up, up, up}
	var got []event.Kind
	var firstDownAt int
	for i, obs := range seq {
		for _, e := range a.Observe(obs, at.Add(time.Duration(i)*100*time.Millisecond)) {
			got = append(got, e.Kind)
			if e.Kind == event.KindLinkDown {
				firstDownAt = i
			}
		}
	}
	if len(got) != 2 || got[0] != event.KindLinkDown || got[1] != event.KindLinkUp {
		t.Fatalf("events = %v, want exactly [link.down link.up]", got)
	}
	if firstDownAt != 1 {
		t.Errorf("link.down emitted at observation %d, want 1 (the first that showed the change)", firstDownAt)
	}
}
