// Package ai is the model-facing core shared by the evaluation runner, the
// CLI, and (eventually) the desktop shell — extracted from ai/eval so that
// what the pre-registered gate measures and what ships are, by construction,
// the same code. See docs/product/adr/0006-local-ai-runtime-and-model-selection.md
// and the 1.2.0 execution plan for the architecture this package implements.
package ai

import "fmt"

// HandleMap assigns each item a short handle ("E1".."En" for events, "H1"..
// for prior similar incidents, "A1".. for operator annotations) instead of
// the model ever seeing a real, long identifier.
//
// execution order §4.10: "why the two measured models failed... don't ask
// the model to reproduce long ULIDs. Give it short closed handles instead
// ... The model may only ever emit E1..En (enforce via a dynamic
// JSON-schema/grammar built per-request from the actual evidence set); the
// backend maps a handle back to the real event ID after validating it
// against the set that was actually offered - any handle outside that set
// fails the whole answer closed." This is structural, not a prompt
// instruction the model could ignore: the per-case JSON schema's evidence-
// reference field is a closed enum of exactly the handles returned here, so
// a model literally cannot sample a token sequence outside that set - and
// the real ID is never present anywhere in what the model is shown, so
// there is nothing to fabricate a plausible-looking variant of.
type HandleMap struct {
	ToID     map[string]string // handle -> real id
	ToHandle map[string]string // real id -> handle
	Handles  []string          // in assignment order
	kind     map[string]string // handle -> "event" | "history" | "annotation"
}

// NewHandleMap starts an empty map. Events, history and annotations are
// added in that order (AddHistory/AddAnnotations after BuildHandles) so
// E-handles are always assigned first, matching every existing eval case's
// expectations and keeping the parity goldens byte-identical when no
// history/annotations are present.
func newHandleMap() HandleMap {
	return HandleMap{ToID: map[string]string{}, ToHandle: map[string]string{}, kind: map[string]string{}}
}

// BuildHandles skips any event missing an "event_id" string field rather
// than assigning it a handle it could never be cited by - such an event
// would be a malformed fixture, not a real evidence item, and silently
// handing it a handle would let a case "pass" while actually never having
// offered that evidence to the model at all.
func BuildHandles(events []map[string]any) HandleMap {
	hm := newHandleMap()
	for _, e := range events {
		id, ok := e["event_id"].(string)
		if !ok || id == "" {
			continue
		}
		handle := fmt.Sprintf("E%d", len(hm.Handles)+1)
		hm.ToID[handle] = id
		hm.ToHandle[id] = handle
		hm.kind[handle] = "event"
		hm.Handles = append(hm.Handles, handle)
	}
	return hm
}

// AddHistory appends H-handles for a set of prior-incident references
// (identified by their fingerprint, internal/ai.Fingerprint), in the given
// order (the caller is expected to have already ranked them, RankSimilar).
func (hm *HandleMap) AddHistory(fingerprints []string) {
	for _, fp := range fingerprints {
		if fp == "" {
			continue
		}
		handle := fmt.Sprintf("H%d", 1+countKind(hm.kind, "history"))
		hm.ToID[handle] = fp
		hm.ToHandle[fp] = handle
		hm.kind[handle] = "history"
		hm.Handles = append(hm.Handles, handle)
	}
}

// AddAnnotations appends A-handles for a set of operator-note IDs, in the
// given order.
func (hm *HandleMap) AddAnnotations(ids []string) {
	for _, id := range ids {
		if id == "" {
			continue
		}
		handle := fmt.Sprintf("A%d", 1+countKind(hm.kind, "annotation"))
		hm.ToID[handle] = id
		hm.ToHandle[id] = handle
		hm.kind[handle] = "annotation"
		hm.Handles = append(hm.Handles, handle)
	}
}

func countKind(kind map[string]string, want string) int {
	n := 0
	for _, k := range kind {
		if k == want {
			n++
		}
	}
	return n
}

// Kind reports what a handle refers to ("event", "history", "annotation"),
// or "" if the handle was never assigned.
func (hm HandleMap) Kind(handle string) string { return hm.kind[handle] }

// RedactEvent returns a copy of e with "event_id" replaced by its handle
// and the two other identifiers that serve no analytical purpose for this
// prompt removed - "schema_v" (a wire-format version, meaningless to an
// analyst) and "ts_mono" (a monotonic counter with no wall-clock meaning
// outside this one process's uptime). Every other field (kind, subject,
// attrs, evidence, severity, confidence, ts_wall, source, observer_id,
// count) passes through unchanged: the fix is removing the one field that
// caused fabrication, not reducing what the model has to reason over.
//
// A shallow copy is enough: none of the retained fields are mutated by
// callers, only read and re-marshaled.
func (hm HandleMap) RedactEvent(e map[string]any) map[string]any {
	out := make(map[string]any, len(e))
	for k, v := range e {
		out[k] = v
	}
	if id, ok := e["event_id"].(string); ok {
		out["handle"] = hm.ToHandle[id]
	}
	delete(out, "event_id")
	delete(out, "schema_v")
	delete(out, "ts_mono")
	return out
}

// ResolveHandles translates a model's cited handles back to real IDs, for
// grading and for the eventual GUI's evidence links. A handle outside
// hm.ToID (never offered for this case) is reported by name rather than
// silently dropped, so the caller can fail the answer closed instead of
// quietly scoring against a smaller set than the model actually claimed.
func (hm HandleMap) ResolveHandles(handles []string) (ids []string, unknown []string) {
	for _, h := range handles {
		if id, ok := hm.ToID[h]; ok {
			ids = append(ids, id)
		} else {
			unknown = append(unknown, h)
		}
	}
	return ids, unknown
}
