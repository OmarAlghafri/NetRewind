package correlate

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/incident"
	"github.com/oklog/ulid/v2"
)

// Engine holds a sliding window of recent events and the rules that recognise
// shapes in it.
//
// It is deliberately not a stream processor with its own storage. Everything it
// knows is in the window, and everything it concludes points back at stored
// events, so any incident it produces can be checked against the record rather
// than taken on faith.
type Engine struct {
	mu     sync.Mutex
	rules  []*Rule
	log    *slog.Logger
	window []*event.Event
	// span is the longest window any rule asks for, and so how much history
	// has to be kept.
	span time.Duration
	// fired remembers matches still inside their window, so one is reported
	// once rather than again on every event that arrives afterwards.
	fired map[string]firedMatch
}

// firedMatch is a conclusion already reported.
//
// links is how complete that conclusion was. A rule whose required clauses
// match will fire before its optional consequences have happened; when one of
// them arrives the account is fuller, and the incident should grow rather than
// a second, near-identical one appearing beside it.
type firedMatch struct {
	at    time.Time
	id    string
	links int
}

// NewEngine returns an engine over a set of rules.
func NewEngine(rules []*Rule, log *slog.Logger) *Engine {
	var span time.Duration
	for _, r := range rules {
		if r.Window > span {
			span = r.Window
		}
	}
	return &Engine{rules: rules, log: log, span: span, fired: make(map[string]firedMatch)}
}

// Rules returns the loaded rules, for reporting.
func (e *Engine) Rules() []*Rule { return e.rules }

// Offer adds an event to the window and returns any incidents it completes.
func (e *Engine) Offer(ev *event.Event) []*incident.Incident {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.window = append(e.window, ev)
	e.prune(ev.WallTime())

	var out []*incident.Incident
	for _, r := range e.rules {
		if !touches(r, ev) {
			continue
		}
		if inc := e.tryRule(r, ev.WallTime()); inc != nil {
			out = append(out, inc)
		}
	}
	return out
}

// prune drops events older than the longest rule window. Everything the engine
// can conclude is bounded by that, so keeping more would only cost memory.
func (e *Engine) prune(now time.Time) {
	cutoff := now.Add(-e.span).UnixNano()
	keep := 0
	for i, ev := range e.window {
		if ev.TSWall >= cutoff {
			keep = i
			break
		}
		keep = i + 1
	}
	if keep > 0 {
		e.window = append(e.window[:0], e.window[keep:]...)
	}
	for key, f := range e.fired {
		if now.Sub(f.at) > e.span {
			delete(e.fired, key)
		}
	}
}

// touches reports whether an event could possibly matter to a rule, so the
// expensive walk is only done for rules the event actually concerns.
func touches(r *Rule, ev *event.Event) bool {
	for i := range r.Match {
		if r.Match[i].matches(ev) {
			return true
		}
	}
	return false
}

// tryRule looks for a complete match of a rule inside the window.
//
// It walks candidates for the opening clause oldest-first and greedily matches
// the remaining clauses forward in time. Greedy is the right choice here: the
// earliest events that satisfy a shape are the ones that explain it, and a
// later coincidence should not be preferred over the thing that actually
// started it.
func (e *Engine) tryRule(r *Rule, now time.Time) *incident.Incident {
	for start := 0; start < len(e.window); start++ {
		first := e.window[start]
		if !r.Match[0].matches(first) {
			continue
		}
		if now.Sub(first.WallTime()) > r.Window {
			continue // this opening is already too old to complete
		}
		matched, ok := e.walk(r, start)
		if !ok {
			continue
		}
		key := fireKey(r, matched)
		prev, already := e.fired[key]
		if already && len(matched) <= prev.links {
			continue // nothing new to say about this one
		}

		inc := build(r, matched)
		if already {
			// Same conclusion, better evidence: keep the identity so the
			// fuller account replaces the thinner one instead of joining it.
			inc.ID = prev.id
		}
		e.fired[key] = firedMatch{at: now, id: inc.ID, links: len(matched)}
		return inc
	}
	return nil
}

// match holds the events satisfying one clause.
type match struct {
	clause *Clause
	events []*event.Event
}

// walk matches the rule's clauses forward from an opening event.
func (e *Engine) walk(r *Rule, start int) ([]match, bool) {
	first := e.window[start]
	deadline := first.WallTime().Add(r.Window)

	// Fields that must agree across everything matched, taken from the
	// opening event. Two failures happening at the same moment on different
	// machines are two incidents, not one.
	pin := make(map[string]string, len(r.CorrelateOn))
	for _, field := range r.CorrelateOn {
		v, ok := fieldValue(first, field)
		if !ok {
			return nil, false
		}
		pin[field] = v
	}

	matched := make([]match, 0, len(r.Match))
	cursor := start

	for i := range r.Match {
		clause := &r.Match[i]
		need := clause.MinCount
		if need < 1 {
			need = 1
		}

		found := make([]*event.Event, 0, need)
		scan := cursor
		if i == 0 {
			found = append(found, first)
			scan = start + 1
		}

		for ; scan < len(e.window) && len(found) < need; scan++ {
			ev := e.window[scan]
			if ev.WallTime().After(deadline) {
				break
			}
			if !clause.matches(ev) || !agrees(ev, pin) {
				continue
			}
			found = append(found, ev)
		}

		if len(found) < need {
			if clause.Optional {
				continue // the rule can complete without this consequence
			}
			return nil, false
		}
		matched = append(matched, match{clause: clause, events: found})
		cursor = scan
	}
	return matched, true
}

// agrees reports whether an event carries the pinned correlation values.
func agrees(ev *event.Event, pin map[string]string) bool {
	for field, want := range pin {
		got, ok := fieldValue(ev, field)
		if !ok || got != want {
			return false
		}
	}
	return true
}

// fieldValue reads a correlation field. "subject" is the label an operator
// knows the entity by; "subject.id" is the resolved identity; anything under
// "attrs." is a kind-specific field.
func fieldValue(ev *event.Event, field string) (string, bool) {
	switch {
	case field == "subject":
		return ev.Subject.Label, ev.Subject.Label != ""
	case field == "subject.id":
		return ev.Subject.ID, ev.Subject.ID != ""
	case field == "observer":
		return ev.ObserverID, true
	case strings.HasPrefix(field, "attrs."):
		v, ok := ev.Attrs[strings.TrimPrefix(field, "attrs.")]
		if !ok {
			return "", false
		}
		return fmt.Sprint(v), true
	case strings.HasPrefix(field, "subject.attrs."):
		v, ok := ev.Subject.Attrs[strings.TrimPrefix(field, "subject.attrs.")]
		return v, ok
	default:
		return "", false
	}
}

// fireKey identifies a match by the events that were required to make it.
//
// Optional clauses are excluded on purpose: the same conclusion arriving later
// with one of its consequences attached is the same conclusion, and must update
// the incident rather than stand beside it as a second, near-identical one.
func fireKey(r *Rule, matched []match) string {
	var b strings.Builder
	b.WriteString(r.ID)
	for _, m := range matched {
		if m.clause.Optional {
			continue
		}
		b.WriteByte('|')
		b.WriteString(m.events[0].ID)
	}
	return b.String()
}

// build assembles the incident from what matched.
func build(r *Rule, matched []match) *incident.Incident {
	inc := &incident.Incident{
		ID:         ulid.Make().String(),
		Status:     incident.StatusClosed,
		Title:      r.Title,
		Severity:   r.severity(),
		Confidence: r.Confidence,
		RuleID:     r.ID,
		Advice:     r.Advice,
	}

	victims := make(map[string]bool)
	seq := 0
	for _, m := range matched {
		lead := m.events[0]

		// An incident cannot be more certain than the observations under it.
		// A rule that is sure of itself resting on an inference is still only
		// as good as the inference.
		for _, ev := range m.events {
			if ev.Confidence < inc.Confidence {
				inc.Confidence = ev.Confidence
			}
		}

		why := m.clause.Why
		if why == "" {
			why = event.Describe(lead)
		}
		link := incident.Link{
			Seq:      seq,
			EventID:  lead.ID,
			Kind:     lead.Kind,
			At:       lead.TSWall,
			Subject:  lead.Subject.Label,
			Relation: m.clause.Relation,
			Why:      why,
			Evidence: map[string]any{"describe": event.Describe(lead)},
		}
		if len(m.events) > 1 {
			link.Evidence["matched_count"] = len(m.events)
			link.Evidence["last_event_id"] = m.events[len(m.events)-1].ID
		}
		inc.Chain = append(inc.Chain, link)
		seq++

		for _, ev := range m.events {
			if ev.Subject.Kind == event.EntityHost && ev.Subject.Label != "" {
				victims[ev.Subject.Label] = true
			}
		}

		if m.clause.As != "" && m.clause.As == r.RootCause {
			inc.RootCause = incident.RootCause{
				Kind:       lead.Kind,
				Entity:     lead.Subject.Label,
				EventID:    lead.ID,
				Confidence: inc.Confidence,
			}
		}
	}

	if inc.RootCause.EventID == "" && len(inc.Chain) > 0 {
		// No clause was blamed, so the thing that opened the incident is the
		// best account we have of why it happened.
		head := inc.Chain[0]
		inc.RootCause = incident.RootCause{
			Kind: head.Kind, Entity: head.Subject,
			EventID: head.EventID, Confidence: inc.Confidence,
		}
	}
	if len(inc.Chain) > 0 {
		inc.OpenedAt = inc.Chain[0].At
		last := matched[len(matched)-1].events
		inc.ClosedAt = last[len(last)-1].TSWall
	}
	for v := range victims {
		inc.Victims = append(inc.Victims, v)
	}
	return inc
}
