// Package event defines the common envelope every NetRewind observation is
// wrapped in, and the vocabulary of kinds those observations can have.
//
// The design rests on one decision: an event is a *state change* or a measured
// observation, never a packet. Recording deltas rather than traffic is what
// makes a week of network history fit in a few gigabytes instead of terabytes,
// and it is why most of what NetRewind stores is directly useful during an
// incident rather than needing to be mined for.
package event

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
)

// SchemaVersion is stamped on every event. Bump it when the envelope changes
// shape so that older stored events stay readable.
const SchemaVersion = 1

// Event is the common envelope. Everything NetRewind records is one of these,
// whatever layer it came from.
type Event struct {
	// ID is a ULID: unique, and lexically sortable by creation time.
	ID      string `json:"event_id"`
	SchemaV uint16 `json:"schema_v"`

	// TSWall is nanoseconds since the Unix epoch, UTC. TSMono is nanoseconds
	// on the observer's monotonic clock. See Clock for why both exist.
	TSWall int64 `json:"ts_wall"`
	TSMono int64 `json:"ts_mono"`

	// ObserverID names which NetRewind node saw this. It matters as soon as
	// there is more than one, and costs nothing before that.
	ObserverID string `json:"observer_id"`

	Source   Source   `json:"source"`
	Kind     Kind     `json:"kind"`
	Severity Severity `json:"severity"`

	// Confidence is 100 for something observed directly and less for something
	// inferred. Correlation multiplies these along a causal chain, so an
	// inferred link honestly marked cannot masquerade as a measured one.
	Confidence uint8 `json:"confidence"`

	Subject EntityRef   `json:"subject"`
	Related []EntityRef `json:"related,omitempty"`

	// Attrs carries the fields specific to this Kind.
	Attrs map[string]any `json:"attrs,omitempty"`
	// Evidence carries the raw observation, trimmed to what a human needs to
	// see to believe the event. Every event must be able to show its work.
	Evidence map[string]any `json:"evidence,omitempty"`

	// DedupKey folds repeats of the same fact together; Count is how many were
	// folded. A flapping interface must not be able to fill the store.
	DedupKey string `json:"dedup_key,omitempty"`
	Count    uint32 `json:"count"`
}

// WallTime returns TSWall as a time.Time in UTC.
func (e *Event) WallTime() time.Time { return time.Unix(0, e.TSWall).UTC() }

// Validate checks the invariants the store and the correlation engine rely on.
func (e *Event) Validate() error {
	switch {
	case e.ID == "":
		return errors.New("event: empty id")
	case e.SchemaV == 0:
		return errors.New("event: schema version not set")
	case e.TSWall == 0:
		return errors.New("event: wall timestamp not set")
	case e.Kind == "":
		return errors.New("event: empty kind")
	case e.Source == "":
		return errors.New("event: empty source")
	case e.ObserverID == "":
		return errors.New("event: empty observer id")
	case e.Subject.Kind == "":
		return errors.New("event: subject has no kind")
	case e.Confidence > 100:
		return fmt.Errorf("event: confidence %d out of range", e.Confidence)
	}
	return nil
}

func (e *Event) String() string {
	return fmt.Sprintf("%s %s %s %s", e.WallTime().Format(time.RFC3339Nano), e.Kind, e.Subject.Label, e.Severity)
}

// JSON renders the event for export and for the CLI's -o json mode.
func (e *Event) JSON() ([]byte, error) { return json.Marshal(e) }

// Builder stamps the fields that are the same for every event a given observer
// produces, so collectors only supply what they actually observed.
type Builder struct {
	ObserverID string
	Clock      *Clock
}

// NewBuilder returns a Builder for one observer. A nil clock gets a fresh one.
func NewBuilder(observerID string, clk *Clock) *Builder {
	if clk == nil {
		clk = NewClock()
	}
	return &Builder{ObserverID: observerID, Clock: clk}
}

// New starts an event. Confidence defaults to 100: a collector that is
// inferring rather than observing must say so with WithConfidence.
func (b *Builder) New(src Source, kind Kind, sev Severity, subject EntityRef) *Event {
	wall, mono := b.Clock.Now()
	return &Event{
		ID:         ulid.Make().String(),
		SchemaV:    SchemaVersion,
		TSWall:     wall,
		TSMono:     mono,
		ObserverID: b.ObserverID,
		Source:     src,
		Kind:       kind,
		Severity:   sev,
		Confidence: 100,
		Subject:    subject,
		Count:      1,
	}
}

// WithAttr sets a Kind-specific field.
func (e *Event) WithAttr(key string, val any) *Event {
	if e.Attrs == nil {
		e.Attrs = make(map[string]any, 4)
	}
	e.Attrs[key] = val
	return e
}

// WithEvidence attaches a piece of the raw observation.
func (e *Event) WithEvidence(key string, val any) *Event {
	if e.Evidence == nil {
		e.Evidence = make(map[string]any, 2)
	}
	e.Evidence[key] = val
	return e
}

// WithRelated adds other entities involved in this event.
func (e *Event) WithRelated(refs ...EntityRef) *Event {
	e.Related = append(e.Related, refs...)
	return e
}

// WithDedup marks the event as foldable against others sharing the key.
func (e *Event) WithDedup(key string) *Event { e.DedupKey = key; return e }

// WithConfidence lowers (or raises) the default certainty of 100.
func (e *Event) WithConfidence(c uint8) *Event { e.Confidence = c; return e }
