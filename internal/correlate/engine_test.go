package correlate

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/incident"
)

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// at builds an event stamped at a chosen moment, so a test can lay out a
// sequence without waiting for one.
func at(kind event.Kind, subject string, when time.Time) *event.Event {
	b := event.NewBuilder("obs", nil)
	e := b.New(event.SourceNetlink, kind, event.SevWarn, event.Host(subject, ""))
	e.TSWall = when.UnixNano()
	return e
}

func TestSingleClauseRuleFires(t *testing.T) {
	r := &Rule{
		ID: "gateway", Title: "gateway moved", Severity: "error", Confidence: 90,
		Window: time.Minute, RootCause: "trigger",
		Match: []Clause{{As: "trigger", Kinds: []string{"l2.arp_binding_changed"}, Why: "it moved"}},
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	e := NewEngine([]*Rule{r}, quietLog())

	got := e.Offer(at(event.KindARPBindingChanged, "10.0.0.1", time.Now()))
	if len(got) != 1 {
		t.Fatalf("got %d incidents, want 1", len(got))
	}
	inc := got[0]
	if inc.Title != "gateway moved" || inc.RuleID != "gateway" {
		t.Errorf("wrong incident: %+v", inc)
	}
	if len(inc.Chain) != 1 || inc.Chain[0].Why != "it moved" {
		t.Errorf("chain not built from the rule: %+v", inc.Chain)
	}
	if inc.RootCause.EventID != inc.Chain[0].EventID {
		t.Error("root cause does not point at the clause the rule blamed")
	}
}

func twoClause() *Rule {
	return &Rule{
		ID: "chain", Title: "link failure isolated hosts", Severity: "warn", Confidence: 80,
		Window: 90 * time.Second, RootCause: "link",
		Match: []Clause{
			{As: "link", Kinds: []string{"link.down"}, Why: "a link went down"},
			{As: "isolation", Kinds: []string{"l2.neighbor_failed"},
				Relation: incident.RelCauses, Why: "hosts stopped answering"},
		},
	}
}

func TestChainNeedsBothClausesInOrder(t *testing.T) {
	e := NewEngine([]*Rule{twoClause()}, quietLog())
	now := time.Now()

	if got := e.Offer(at(event.KindLinkDown, "eth1", now)); len(got) != 0 {
		t.Fatal("fired on the opening clause alone")
	}
	got := e.Offer(at(event.KindNeighborFailed, "10.0.0.5", now.Add(2*time.Second)))
	if len(got) != 1 {
		t.Fatalf("got %d incidents, want 1", len(got))
	}
	if rel := got[0].Chain[1].Relation; rel != incident.RelCauses {
		t.Errorf("relation = %q, want causes", rel)
	}
}

// The consequence arriving before the cause is not the shape the rule
// describes, and treating it as one would invent a mechanism backwards.
func TestReversedOrderDoesNotFire(t *testing.T) {
	e := NewEngine([]*Rule{twoClause()}, quietLog())
	now := time.Now()

	e.Offer(at(event.KindNeighborFailed, "10.0.0.5", now))
	if got := e.Offer(at(event.KindLinkDown, "eth1", now.Add(2*time.Second))); len(got) != 0 {
		t.Error("fired on the consequence preceding the cause")
	}
}

func TestOutsideTheWindowDoesNotFire(t *testing.T) {
	e := NewEngine([]*Rule{twoClause()}, quietLog())
	now := time.Now()

	e.Offer(at(event.KindLinkDown, "eth1", now.Add(-10*time.Minute)))
	got := e.Offer(at(event.KindNeighborFailed, "10.0.0.5", now))
	if len(got) != 0 {
		t.Error("wove together two events far too far apart to be related")
	}
}

// Two failures happening at the same moment on different machines are two
// incidents, not one. Without this the engine invents connections.
func TestCorrelateOnKeepsUnrelatedFailuresApart(t *testing.T) {
	r := twoClause()
	r.CorrelateOn = []string{"subject"}
	e := NewEngine([]*Rule{r}, quietLog())
	now := time.Now()

	e.Offer(at(event.KindLinkDown, "eth1", now))
	if got := e.Offer(at(event.KindNeighborFailed, "eth2", now.Add(time.Second))); len(got) != 0 {
		t.Error("correlated two events that share nothing")
	}
	if got := e.Offer(at(event.KindNeighborFailed, "eth1", now.Add(2*time.Second))); len(got) != 1 {
		t.Error("failed to correlate two events that do share a subject")
	}
}

func TestMinCountRequiresRepetition(t *testing.T) {
	r := &Rule{
		ID: "flap", Title: "port flapping", Severity: "warn", Confidence: 88,
		Window: 5 * time.Minute, CorrelateOn: []string{"subject"}, RootCause: "flap",
		Match: []Clause{{As: "flap", Kinds: []string{"link.down"}, MinCount: 3, Why: "it keeps going down"}},
	}
	e := NewEngine([]*Rule{r}, quietLog())
	now := time.Now()

	for i := 0; i < 2; i++ {
		if got := e.Offer(at(event.KindLinkDown, "eth1", now.Add(time.Duration(i)*time.Second))); len(got) != 0 {
			t.Fatalf("fired after only %d occurrences", i+1)
		}
	}
	got := e.Offer(at(event.KindLinkDown, "eth1", now.Add(3*time.Second)))
	if len(got) != 1 {
		t.Fatalf("got %d incidents after three occurrences, want 1", len(got))
	}
	if n := got[0].Chain[0].Evidence["matched_count"]; n != 3 {
		t.Errorf("matched_count = %v, want 3", n)
	}
}

func TestOptionalClauseIsNotRequired(t *testing.T) {
	r := &Rule{
		ID: "hijack", Title: "gateway hijack", Severity: "error", Confidence: 90,
		Window: time.Minute, RootCause: "trigger",
		Match: []Clause{
			{As: "trigger", Kinds: []string{"l2.arp_binding_changed"}, Why: "gateway moved"},
			{As: "fallout", Kinds: []string{"l3.default_route_changed"}, Optional: true,
				Relation: incident.RelCauses, Why: "routing followed"},
		},
	}
	e := NewEngine([]*Rule{r}, quietLog())

	got := e.Offer(at(event.KindARPBindingChanged, "10.0.0.1", time.Now()))
	if len(got) != 1 {
		t.Fatalf("got %d incidents, want 1 without the optional consequence", len(got))
	}
	if len(got[0].Chain) != 1 {
		t.Errorf("chain has %d links, want 1", len(got[0].Chain))
	}
}

// A match still inside its window must be reported once, or every subsequent
// event re-reports it and the operator learns to ignore incidents.
func TestAMatchIsReportedOnce(t *testing.T) {
	r := &Rule{
		ID: "once", Title: "once", Severity: "warn", Confidence: 90,
		Window: time.Minute, RootCause: "trigger",
		Match: []Clause{{As: "trigger", Kinds: []string{"l2.duplicate_ip"}, Why: "contested"}},
	}
	e := NewEngine([]*Rule{r}, quietLog())
	now := time.Now()

	if got := e.Offer(at(event.KindDuplicateIP, "10.0.0.5", now)); len(got) != 1 {
		t.Fatal("did not fire on the first occurrence")
	}
	for i := 1; i < 5; i++ {
		e.Offer(at(event.KindLinkUp, "eth1", now.Add(time.Duration(i)*time.Second)))
	}
	if got := e.Offer(at(event.KindLinkDown, "eth1", now.Add(10*time.Second))); len(got) != 0 {
		t.Error("re-reported a match that was already reported")
	}
}

// An incident built on an inference cannot be more certain than the inference.
func TestConfidenceIsBoundedByTheWeakestEvent(t *testing.T) {
	r := &Rule{
		ID: "weak", Title: "weak", Severity: "warn", Confidence: 95,
		Window: time.Minute, RootCause: "trigger",
		Match: []Clause{{As: "trigger", Kinds: []string{"l2.duplicate_ip"}, Why: "contested"}},
	}
	e := NewEngine([]*Rule{r}, quietLog())

	ev := at(event.KindDuplicateIP, "10.0.0.5", time.Now())
	ev.Confidence = 60

	got := e.Offer(ev)
	if len(got) != 1 {
		t.Fatal("did not fire")
	}
	if got[0].Confidence != 60 {
		t.Errorf("incident confidence = %d, want 60 - it cannot exceed its evidence", got[0].Confidence)
	}
	if got[0].RootCause.Confidence != 60 {
		t.Errorf("root cause confidence = %d, want 60", got[0].RootCause.Confidence)
	}
}

func TestValidateRejectsUnanswerableRules(t *testing.T) {
	cases := map[string]*Rule{
		"no id": {Title: "t", Confidence: 50, Window: time.Minute,
			Match: []Clause{{Kinds: []string{"link.down"}}}},
		"no window": {ID: "x", Title: "t", Confidence: 50,
			Match: []Clause{{Kinds: []string{"link.down"}}}},
		"no clauses": {ID: "x", Title: "t", Confidence: 50, Window: time.Minute},
		"clause matches nothing": {ID: "x", Title: "t", Confidence: 50, Window: time.Minute,
			Match: []Clause{{As: "a"}}},
		"blames an unknown clause": {ID: "x", Title: "t", Confidence: 50, Window: time.Minute,
			RootCause: "ghost", Match: []Clause{{As: "a", Kinds: []string{"link.down"}}}},
		"opens with an optional clause": {ID: "x", Title: "t", Confidence: 50, Window: time.Minute,
			Match: []Clause{{As: "a", Kinds: []string{"link.down"}, Optional: true}}},
		"second clause states no relation": {ID: "x", Title: "t", Confidence: 50, Window: time.Minute,
			Match: []Clause{
				{As: "a", Kinds: []string{"link.down"}},
				{As: "b", Kinds: []string{"link.up"}},
			}},
		"unknown relation": {ID: "x", Title: "t", Confidence: 50, Window: time.Minute,
			Match: []Clause{
				{As: "a", Kinds: []string{"link.down"}},
				{As: "b", Kinds: []string{"link.up"}, Relation: "explains"},
			}},
		"impossible confidence": {ID: "x", Title: "t", Confidence: 150, Window: time.Minute,
			Match: []Clause{{Kinds: []string{"link.down"}}}},
	}
	for name, r := range cases {
		if err := r.Validate(); err == nil {
			t.Errorf("%s: Validate accepted a rule that cannot work", name)
		}
	}
}

func TestWhereNarrowsByAttribute(t *testing.T) {
	r := &Rule{
		ID: "gw", Title: "gateway only", Severity: "error", Confidence: 90,
		Window: time.Minute, RootCause: "trigger",
		Match: []Clause{{
			As: "trigger", Kinds: []string{"l2.arp_binding_changed"},
			Where: Where{Attrs: map[string]string{"is_gateway": "true"}},
			Why:   "the gateway moved",
		}},
	}
	e := NewEngine([]*Rule{r}, quietLog())
	now := time.Now()

	ordinary := at(event.KindARPBindingChanged, "10.0.0.5", now)
	ordinary.WithAttr("is_gateway", false)
	if got := e.Offer(ordinary); len(got) != 0 {
		t.Error("fired on a binding change that was not the gateway")
	}

	gateway := at(event.KindARPBindingChanged, "10.0.0.1", now.Add(time.Second))
	gateway.WithAttr("is_gateway", true)
	if got := e.Offer(gateway); len(got) != 1 {
		t.Error("did not fire on the gateway binding change")
	}
}

func TestShippedRulesAreValid(t *testing.T) {
	rules, err := LoadRules("../../rules")
	if err != nil {
		t.Fatalf("the shipped rule library does not load: %v", err)
	}
	if len(rules) < 5 {
		t.Errorf("only %d rules shipped", len(rules))
	}
	for _, r := range rules {
		if r.Advice == "" {
			t.Errorf("rule %s tells the operator what happened but not what to look at next", r.ID)
		}
		for i, c := range r.Match {
			if c.Why == "" {
				t.Errorf("rule %s clause %d has no explanation, so its link in the chain would be silent", r.ID, i)
			}
		}
	}
}
