package harness

import "fmt"

// BuildHandles assigns each event a short handle ("E1".."En", in the
// events' own order - the same order the recorder produced them in) instead
// of the model ever seeing that event's own long ULID `event_id`.
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
// the real ULID is never present anywhere in what the model is shown, so
// there is nothing to fabricate a plausible-looking variant of.
//
// A HandleMap always keys off event_id -> handle *and* handle -> event_id,
// because both directions are needed: ToHandle to redact an event for
// display and to translate an Expected case's real-ID citations into the
// handles a correct answer should use; ToID to resolve a model's answer
// back to real event IDs for grading and for the eventual GUI's evidence
// links.
type HandleMap struct {
	ToID     map[string]string // handle -> event_id
	ToHandle map[string]string // event_id -> handle
	Handles  []string          // in assignment order, E1..En
}

// BuildHandles skips any event missing an "event_id" string field rather
// than assigning it a handle it could never be cited by - such an event
// would be a malformed fixture, not a real evidence item, and silently
// handing it a handle would let a case "pass" while actually never having
// offered that evidence to the model at all.
func BuildHandles(events []map[string]any) HandleMap {
	hm := HandleMap{ToID: map[string]string{}, ToHandle: map[string]string{}}
	for _, e := range events {
		id, ok := e["event_id"].(string)
		if !ok || id == "" {
			continue
		}
		handle := fmt.Sprintf("E%d", len(hm.Handles)+1)
		hm.ToID[handle] = id
		hm.ToHandle[id] = handle
		hm.Handles = append(hm.Handles, handle)
	}
	return hm
}

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

// ResolveHandles translates a model's cited handles back to real event
// IDs, for grading and for the eventual GUI's evidence links. A handle
// outside hm.ToID (never offered for this case) is reported by name rather
// than silently dropped, so the caller can fail the answer closed instead
// of quietly scoring against a smaller set than the model actually claimed.
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
