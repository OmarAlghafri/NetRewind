package correlate

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
)

// This is the sequence a real Cisco topology produced when the gateway address
// was moved from one router to another, taken from a GNS3 run rather than
// invented. It is here because the synthetic lab could not produce it: with
// veth pairs the old ARP binding is still present when the new one arrives, so
// the recorder sees l2.arp_binding_changed. Real hardware fails the old entry
// first, and what arrives is a *new* binding for an address that has been seen
// before - which reported nothing at all until this was found.
//
// The timings are the ones observed: the gateway stopped answering, four
// minutes passed, and it came back as a different machine.
func TestAGatewayThatComesBackAsSomeoneElseIsReported(t *testing.T) {
	rules, err := LoadRules(filepath.Join("..", "..", "rules"))
	if err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(rules, quietLog())

	start := time.Now().Add(-10 * time.Minute)
	b := event.NewBuilder("NetRewind", nil)

	// The gateway stops answering. R1 has lost the address.
	failed := b.New(event.SourceNetlink, event.KindNeighborFailed, event.SevNotice,
		event.Host("10.1.10.1", "c2:01:16:78:00:01"))
	failed.TSWall = start.UnixNano()
	failed.WithAttr("ip", "10.1.10.1").WithAttr("mac", "c2:01:16:78:00:01")
	engine.Offer(failed)

	// Four minutes later it answers again - from R3.
	returned := b.New(event.SourceNetlink, event.KindARPBindingNew, event.SevError,
		event.Host("10.1.10.1", "c2:03:12:2c:00:01"))
	returned.TSWall = start.Add(4 * time.Minute).UnixNano()
	returned.WithAttr("ip", "10.1.10.1").
		WithAttr("mac", "c2:03:12:2c:00:01").
		WithAttr("is_gateway", true).
		WithAttr("address_changed_hands", true)

	incidents := engine.Offer(returned)

	var found bool
	for _, inc := range incidents {
		if inc.RuleID == "gateway-returned-different" {
			found = true
			if inc.Severity != event.SevError {
				t.Errorf("severity = %s; the way out of the network changed hands", inc.Severity)
			}
			// The account has to include the outage that hid the substitution,
			// which means the engine found it by searching backwards.
			if len(inc.Chain) < 2 {
				t.Errorf("the chain has %d links; it should also carry the failure that preceded the return",
					len(inc.Chain))
			}
			if inc.Advice == "" {
				t.Error("no advice: a restored path that is not a closed incident needs saying")
			}
		}
	}
	if !found {
		t.Fatal("a gateway returning on different hardware produced no incident, " +
			"so the record would show only an outage that recovered")
	}
}

// The ordinary case must not fire it: a machine genuinely seen for the first
// time is not a substitution, and reporting it as one would make the rule noise.
func TestAGenuineFirstSightingIsNotAnIncident(t *testing.T) {
	rules, err := LoadRules(filepath.Join("..", "..", "rules"))
	if err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(rules, quietLog())

	b := event.NewBuilder("NetRewind", nil)
	first := b.New(event.SourceNetlink, event.KindARPBindingNew, event.SevInfo,
		event.Host("10.1.10.77", "02:00:00:00:00:77"))
	first.TSWall = time.Now().UnixNano()
	first.WithAttr("ip", "10.1.10.77").
		WithAttr("mac", "02:00:00:00:00:77").
		WithAttr("is_gateway", false)

	for _, inc := range engine.Offer(first) {
		if inc.RuleID == "gateway-returned-different" {
			t.Error("a host appearing for the first time was reported as a gateway substitution")
		}
	}
}
