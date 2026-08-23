// Package incident holds what correlation produces: an account of something
// that went wrong, assembled from the events that evidence it.
//
// An incident is not a louder alert. It has a beginning and an end, a set of
// machines it affected, and a chain in which every link names the event it
// rests on and states honestly whether that event caused the next one or merely
// happened before it.
package incident

import (
	"github.com/OmarAlghafri/netrewind/internal/event"
)

// Relation is the claim a link in the chain makes about the one after it.
//
// Keeping these distinct is the whole discipline of the engine. Anything can
// notice that two things happened close together; saying which of them caused
// the other is a much stronger claim, and one that has to be earned by a rule
// that encodes why. A tool that blurs the two teaches its operator to distrust
// it, and an operator who distrusts the timeline is back to guessing.
type Relation string

const (
	// RelCauses asserts a mechanism: this event is why the next one happened.
	RelCauses Relation = "causes"
	// RelCorrelates asserts only that the two moved together.
	RelCorrelates Relation = "correlates"
	// RelPrecedes asserts only ordering in time.
	RelPrecedes Relation = "precedes"
)

// Status is where an incident is in its life.
type Status string

const (
	StatusOpen   Status = "open"
	StatusClosed Status = "closed"
)

// Link is one step in the causal chain.
type Link struct {
	Seq      int        `json:"seq"`
	EventID  string     `json:"event_id"`
	Kind     event.Kind `json:"kind"`
	At       int64      `json:"at"`
	Subject  string     `json:"subject"`
	Relation Relation   `json:"relation"`
	// Why is the sentence explaining this step, taken from the rule that
	// matched. It is what the operator reads instead of the field names.
	Why      string         `json:"why"`
	Evidence map[string]any `json:"evidence,omitempty"`
}

// RootCause names what the rule believes started it.
type RootCause struct {
	Kind       event.Kind `json:"kind"`
	Entity     string     `json:"entity"`
	EventID    string     `json:"event_id"`
	Confidence uint8      `json:"confidence"`
}

// Incident is the account of one thing going wrong.
type Incident struct {
	ID       string         `json:"incident_id"`
	OpenedAt int64          `json:"opened_at"`
	ClosedAt int64          `json:"closed_at,omitempty"`
	Status   Status         `json:"status"`
	Title    string         `json:"title"`
	Severity event.Severity `json:"severity"`
	// Confidence is the rule's own certainty, reduced by the certainty of the
	// events it rests on. An incident built from inferred events cannot be
	// more certain than they are.
	Confidence uint8     `json:"confidence"`
	RootCause  RootCause `json:"root_cause"`
	Chain      []Link    `json:"chain"`
	Victims    []string  `json:"victims,omitempty"`
	RuleID     string    `json:"rule_id"`
	// Advice is what the rule suggests looking at next. It never suggests a
	// change: the recorder observes and does not touch the network.
	Advice string `json:"advice,omitempty"`
}

// Duration returns how long the incident ran, in nanoseconds. An open incident
// is measured to its last link.
func (i *Incident) Duration() int64 {
	end := i.ClosedAt
	if end == 0 && len(i.Chain) > 0 {
		end = i.Chain[len(i.Chain)-1].At
	}
	if end < i.OpenedAt {
		return 0
	}
	return end - i.OpenedAt
}
