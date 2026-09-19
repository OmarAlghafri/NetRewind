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
	// firedOrder is the same keys in the order they were recorded, so expiring
	// them costs the number that actually expired rather than a walk of every
	// match still live. A key appears once per time it was recorded; the
	// superseded copies are discarded when they surface. Without this the
	// engine paid a full map traversal on every single event, which is the
	// cost that shows up under exactly the burst these rules exist for.
	firedOrder []firedKey
	// settled records, per rule, which openings have nothing further to say and
	// which clause each of the rest is still waiting for. Without it the engine
	// re-walks every event in the window as a candidate opening on every
	// arrival, which is quadratic per event and cubic over a burst - and the
	// burst is a flapping port, which is precisely what these rules exist to
	// recognise. See anchorMemo.
	settled map[*Rule]map[*event.Event]*anchorMemo
	// first is where each rule's scan for an opening starts: everything before
	// it is settled dead, so a rule with a ninety-second window stops paying
	// for the ten minutes of history the longest rule needs.
	first map[*Rule]int
	// scanned is how much of the window each rule has already considered as an
	// opening. Everything before it has a memo; everything after is new.
	scanned map[*Rule]int
	// waiting counts, per rule, how many live openings are waiting for an
	// event of each kind. When the arrival is of a kind nothing is waiting
	// for, the whole examined part of the window can be skipped without
	// looking at it, which is what turns a burst from quadratic into linear.
	waiting map[*Rule]map[string]int
	// truncated counts events dropped from the window because it hit
	// maxWindowEvents. Correlation is then working from part of the period;
	// the store still holds everything.
	truncated uint64
	// warnedTruncation keeps the message about that to one line per burst
	// rather than one per event.
	warnedTruncation bool
}

// maxWindowEvents bounds the correlation window by count as well as by time.
//
// The time bound alone is not a bound: a segment under a broadcast storm, or a
// port flapping as fast as netlink can report it, produces events faster than
// any window empties, and the engine would hold all of them. Two things follow
// from an unbounded window and both are worse than a truncated one: the process
// grows until it is killed, and matching costs time proportional to the window
// on every arrival, so the recorder slows down exactly when the network is
// worst and events are arriving fastest.
//
// The recorder's first duty is to keep recording. Correlation gives up the
// oldest part of its view rather than the process giving up its memory, says so
// through Truncated, and the store still holds every event either way.
//
// 25000 is 40 events a second sustained across the longest rule window, which
// is far above what a network produces when it is merely misbehaving, and it
// keeps the cost of one arrival under a millisecond on the hardware this is
// meant to run on.
const maxWindowEvents = 25000

// anchorMemo is what a previous walk from one candidate opening concluded.
//
// dead means the anchor can never produce anything again: its correlation
// fields were missing, or it has aged past the rule's window. unmet are the
// clauses the walk was still waiting for - a required one it could not satisfy,
// or optional ones that would make the account fuller. A new event can only
// change the outcome if it satisfies one of those, so anything else skips the
// walk entirely.
//
// This is sound because the window only ever grows at the end. A clause that
// already has its events keeps them: the walk is greedy from the earliest
// candidate, and an event appended after them cannot change which ones it
// picked.
type anchorMemo struct {
	dead  bool
	unmet []int
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

// firedKey is one entry in the expiry queue: which match, and when it was
// recorded. The time is what tells a superseded entry from the live one.
type firedKey struct {
	key string
	at  time.Time
}

// NewEngine returns an engine over a set of rules.
func NewEngine(rules []*Rule, log *slog.Logger) *Engine {
	var span time.Duration
	for _, r := range rules {
		if r.Window > span {
			span = r.Window
		}
	}
	return &Engine{
		rules:   rules,
		log:     log,
		span:    span,
		fired:   make(map[string]firedMatch),
		settled: make(map[*Rule]map[*event.Event]*anchorMemo, len(rules)),
		first:   make(map[*Rule]int, len(rules)),
		scanned: make(map[*Rule]int, len(rules)),
		waiting: make(map[*Rule]map[string]int, len(rules)),
	}
}

// Truncated reports how many events correlation dropped from its window
// because the window was full. Any number above zero means the engine was
// reasoning about part of the period rather than all of it.
func (e *Engine) Truncated() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.truncated
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
	// A window that is full drops its oldest regardless of age. Correlation
	// loses the front of the period; the recorder keeps recording, which is
	// the trade that matters.
	if over := len(e.window) - keep - maxWindowEvents; over > 0 {
		keep += over
		e.truncated += uint64(over)
		if !e.warnedTruncation && e.log != nil {
			e.warnedTruncation = true
			e.log.Warn("correlation window is full; dropping the oldest events from it",
				"limit", maxWindowEvents, "dropped", over,
				"consequence", "incidents may be missed for this period. The store still holds every event.")
		}
	} else if len(e.window)-keep < maxWindowEvents/2 {
		e.warnedTruncation = false
	}
	if keep > 0 {
		// Copied before the shift: the shift writes over the front of the same
		// backing array, so a slice of it would be garbage by the time the
		// memos were cleaned up against it.
		dropped := make([]*event.Event, keep)
		copy(dropped, e.window[:keep])

		n := copy(e.window, e.window[keep:])
		// The tail still points at the dropped events; clearing it lets them
		// be collected rather than pinned by an oversized backing array.
		for i := n; i < len(e.window); i++ {
			e.window[i] = nil
		}
		e.window = e.window[:n]

		for _, r := range e.rules {
			for _, cursor := range []map[*Rule]int{e.first, e.scanned} {
				if n := cursor[r] - keep; n > 0 {
					cursor[r] = n
				} else {
					delete(cursor, r)
				}
			}
			memo := e.settled[r]
			if memo == nil {
				continue
			}
			for _, ev := range dropped {
				// An opening that leaves the window stops waiting for
				// anything, so the tally has to lose it too or the fast path
				// would be disabled by openings that no longer exist.
				if m, ok := memo[ev]; ok {
					e.unwait(r, m)
					delete(memo, ev)
				}
			}
		}
	}
	// Oldest first, stopping at the first entry still inside the span: the
	// queue is in the order the matches were recorded, so everything behind it
	// is newer. A key recorded again has a newer time in the map than the
	// entry that queued it, which is how a superseded entry is recognised and
	// discarded without touching the match itself.
	drop := 0
	for ; drop < len(e.firedOrder); drop++ {
		q := e.firedOrder[drop]
		f, live := e.fired[q.key]
		switch {
		case !live || !f.at.Equal(q.at):
			continue // already expired, or superseded by a later entry
		case now.Sub(f.at) > e.span:
			delete(e.fired, q.key)
		default:
			// Still inside the span, and it is the live entry for this key.
			goto trimmed
		}
	}
trimmed:
	if drop > 0 {
		e.firedOrder = append(e.firedOrder[:0], e.firedOrder[drop:]...)
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
	anchor := r.Anchor()
	arrived := e.window[len(e.window)-1]
	memos := e.settled[r]
	if memos == nil {
		memos = make(map[*event.Event]*anchorMemo)
		e.settled[r] = memos
	}
	// Everything before first is settled dead. Advancing it is what keeps a
	// short-windowed rule from re-reading the whole span on every event.
	first := e.first[r]
	if first > len(e.window) {
		first = len(e.window)
	}
	scanned := e.scanned[r]
	if scanned > len(e.window) {
		scanned = len(e.window)
	}

	// The part of the window this rule has already considered can only produce
	// something new if an opening in it is waiting for exactly what just
	// arrived. When nothing is, the whole prefix is skipped without being
	// looked at, and only the events appended since the last look are
	// examined.
	start := scanned
	if e.waiting[r][string(arrived.Kind)] > 0 {
		start = first
	}
	if start < first {
		start = first
	}

	setMemo := func(i int, at *event.Event, m *anchorMemo) {
		e.unwait(r, memos[at])
		memos[at] = m
		e.wait(r, m)
		if m.dead && i == first {
			first++
		}
	}
	for ; start < len(e.window); start++ {
		at := e.window[start]
		m := memos[at]
		if m != nil && m.dead {
			if start == first {
				first++
			}
			continue
		}
		if now.Sub(at.WallTime()) > r.Window {
			// Too old to complete, and time only moves one way.
			setMemo(start, at, deadMemo)
			continue
		}
		if m == nil {
			if !r.Match[anchor].matches(at) {
				// Not an opening for this rule, and never will be.
				setMemo(start, at, deadMemo)
				continue
			}
		} else if !matchesAny(r, m.unmet, arrived) {
			// The previous walk from here is still the answer unless the event
			// that just arrived is one of the things it was waiting for.
			continue
		}
		matched, unmet, ok := e.walk(r, anchor, start)
		switch {
		case !ok && unmet == nil:
			// The anchor does not carry the fields this rule correlates on, so
			// no arrangement of later events can rescue it.
			setMemo(start, at, deadMemo)
			continue
		case !ok:
			setMemo(start, at, &anchorMemo{unmet: unmet})
			continue
		case len(unmet) == 0:
			// Every clause matched: this opening has said all it can.
			setMemo(start, at, deadMemo)
		default:
			setMemo(start, at, &anchorMemo{unmet: unmet})
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
		e.firedOrder = append(e.firedOrder, firedKey{key: key, at: now})
		// Openings after this one have not been looked at, so the rule has not
		// finished scanning: scanned stays where it was and they are examined
		// on the next arrival.
		e.first[r] = first
		return inc
	}
	e.first[r] = first
	e.scanned[r] = len(e.window)
	return nil
}

// wait and unwait keep the per-rule tally of what live openings are waiting
// for. An opening waits for any event that could satisfy one of its unmet
// clauses, which is exactly the kinds those clauses name.
func (e *Engine) wait(r *Rule, m *anchorMemo) {
	if m == nil || m.dead || len(m.unmet) == 0 {
		return
	}
	tally := e.waiting[r]
	if tally == nil {
		tally = make(map[string]int)
		e.waiting[r] = tally
	}
	for _, kind := range unmetKinds(r, m.unmet) {
		tally[kind]++
	}
}

func (e *Engine) unwait(r *Rule, m *anchorMemo) {
	if m == nil || m.dead || len(m.unmet) == 0 {
		return
	}
	tally := e.waiting[r]
	for _, kind := range unmetKinds(r, m.unmet) {
		if tally[kind] <= 1 {
			delete(tally, kind)
			continue
		}
		tally[kind]--
	}
}

// unmetKinds is every event kind that could satisfy one of these clauses.
func unmetKinds(r *Rule, unmet []int) []string {
	var out []string
	for _, i := range unmet {
		if i < 0 || i >= len(r.Match) {
			continue
		}
		out = append(out, r.Match[i].Kinds...)
	}
	return out
}

// match holds the events satisfying one clause.
type match struct {
	clause *Clause
	events []*event.Event
}

// walk matches a rule's clauses around the anchor event: the ones after it
// forwards in time, the ones before it backwards.
//
// Searching backwards is what lets a rule ask the question the whole project
// exists for. The symptom is what arrives - a pair that stopped working, a
// route that vanished - and the useful question is what changed just before it.
// A forward-only engine can only describe consequences.
// It also reports, as unmet, the clauses the walk was still waiting for: the
// required one it could not satisfy, or the optional ones that would have made
// the account fuller. tryRule keeps that so it can skip re-walking this opening
// until an event arrives that could actually change the answer. A nil unmet
// with ok false means the opening is unusable outright.
func (e *Engine) walk(r *Rule, anchor, start int) ([]match, []int, bool) {
	at := e.window[start]
	latest := at.WallTime().Add(r.Window)
	earliest := at.WallTime().Add(-r.Window)

	// Fields that must agree across everything matched, taken from the anchor.
	// Two failures happening at the same moment on different machines are two
	// incidents, not one.
	pin := make(map[string]string, len(r.CorrelateOn))
	for _, field := range r.CorrelateOn {
		v, ok := fieldValue(at, field)
		if !ok {
			return nil, nil, false
		}
		pin[field] = v
	}

	var unmet []int
	found := make([][]*event.Event, len(r.Match))
	found[anchor] = []*event.Event{at}

	// Backwards, nearest first: the most recent change before a failure is the
	// best explanation of it, and an older one would be a worse guess dressed
	// up as the same claim.
	cursor := start - 1
	for i := anchor - 1; i >= 0; i-- {
		clause := &r.Match[i]
		need := clause.minCount()
		hits := make([]*event.Event, 0, need)
		scan := cursor
		for ; scan >= 0 && len(hits) < need; scan-- {
			ev := e.window[scan]
			if ev.WallTime().Before(earliest) {
				break
			}
			if !clause.matches(ev) || !agrees(ev, pin) {
				continue
			}
			hits = append(hits, ev)
		}
		if len(hits) < need {
			if clause.Optional {
				unmet = append(unmet, i)
				continue
			}
			return nil, []int{i}, false
		}
		// Put them back in time order so the chain reads forwards.
		for l, r2 := 0, len(hits)-1; l < r2; l, r2 = l+1, r2-1 {
			hits[l], hits[r2] = hits[r2], hits[l]
		}
		found[i] = hits
		cursor = scan
	}

	// Forwards from the anchor.
	cursor = start + 1
	for i := anchor + 1; i < len(r.Match); i++ {
		clause := &r.Match[i]
		need := clause.minCount()
		hits := make([]*event.Event, 0, need)
		scan := cursor
		for ; scan < len(e.window) && len(hits) < need; scan++ {
			ev := e.window[scan]
			if ev.WallTime().After(latest) {
				break
			}
			if !clause.matches(ev) || !agrees(ev, pin) {
				continue
			}
			hits = append(hits, ev)
		}
		if len(hits) < need {
			if clause.Optional {
				unmet = append(unmet, i)
				continue
			}
			return nil, []int{i}, false
		}
		found[i] = hits
		cursor = scan
	}

	// The anchor clause may itself need repeating.
	if need := r.Match[anchor].minCount(); need > 1 {
		hits := []*event.Event{at}
		for scan := start + 1; scan < len(e.window) && len(hits) < need; scan++ {
			ev := e.window[scan]
			if ev.WallTime().After(latest) {
				break
			}
			if r.Match[anchor].matches(ev) && agrees(ev, pin) {
				hits = append(hits, ev)
			}
		}
		if len(hits) < need {
			return nil, []int{anchor}, false
		}
		found[anchor] = hits
	}

	matched := make([]match, 0, len(r.Match))
	for i := range r.Match {
		if len(found[i]) == 0 {
			continue
		}
		matched = append(matched, match{clause: &r.Match[i], events: found[i]})
	}
	return matched, unmet, true
}

// deadMemo marks an opening that can never produce anything again. One shared
// value: there is nothing in it to distinguish one from another.
var deadMemo = &anchorMemo{dead: true}

// matchesAny reports whether an event satisfies any of the named clauses.
func matchesAny(r *Rule, clauses []int, ev *event.Event) bool {
	for _, i := range clauses {
		if i >= 0 && i < len(r.Match) && r.Match[i].matches(ev) {
			return true
		}
	}
	return false
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
			Clause:   m.clause.As,
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
