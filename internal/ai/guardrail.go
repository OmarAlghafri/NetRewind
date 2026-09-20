package ai

import (
	"strings"

	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/incident"
	"github.com/OmarAlghafri/netrewind/internal/registry"
)

// Request is everything Guardrail (and, later, Analyze) needs to decide
// whether, and how confidently, a local model may be asked about one
// incident.
type Request struct {
	// Incident is nil for a general "what is happening in this window"
	// question with no specific incident (every eval corpus "negative"
	// case uses this) - see Guardrail's own doc for what that means for
	// the ceiling.
	Incident *incident.Incident
	// Events are every event offered as evidence, in the same map shape
	// BuildHandles/HandleMap.RedactEvent already expect: real "event_id",
	// "kind", "ts_wall", "attrs", ... (internal/event.Event's own json
	// tags - see corpus/v1/*/events.json for real examples).
	Events []map[string]any
	// Capabilities is nil when unknown (the evaluation runner has no live
	// registry to ask, and a general question with no incident never
	// reaches these rules anyway) - rules 6/7 below only apply when this
	// is non-nil.
	Capabilities []registry.Snapshot
}

// windowPadSeconds is how far outside an incident's own [opened, end]
// window a system.gap/system.drop/system.collector_down event still counts
// against it (execution order §4.10).
const windowPadSeconds = 60

// Reason is one structured entry in GuardrailResult.Reasons - the same idea
// as registry.Snapshot's own ReasonCode/ReasonParams, so a client can
// translate it instead of pattern-matching English text.
type Reason struct {
	Code   string            `json:"code"`
	Params map[string]string `json:"params,omitempty"`
}

// GuardrailResult is Guardrail's verdict on whether a model may be asked at
// all, and above what confidence it may never claim to be regardless.
type GuardrailResult struct {
	// Refuse means a model must never be called for this request: the
	// caller reports verdict "insufficient_evidence" using Reasons alone,
	// with no inference run at all (execution order §4.10: "force
	// insufficient_evidence before inference on gap/collector-down").
	Refuse bool
	// Ceiling bounds every confidence field in the per-request JSON schema
	// (see JSONSchemaFor). It is computed the same way whether or not
	// Refuse ends up true: a gap forces a refusal, it does not change what
	// the deterministic engine itself was confident about, so the two are
	// reported independently rather than one zeroing the other out.
	Ceiling int
	// Reasons lists every rule that matched, not only the first - a client
	// showing "why can't I get an answer" should be able to name every
	// contributing cause, not just whichever was checked first.
	Reasons []Reason
	// BlindFamilies lists event-kind families this recorder currently
	// cannot see (a collector down or unsupported, but not one covering
	// this incident's own root-cause/chain kinds - see Reasons for that
	// stronger case instead), disclosed in the prompt so the model can
	// name them as a limit on its own answer rather than reasoning as if
	// they do not exist.
	BlindFamilies []string
}

// blindnessRuleIDs are the two correlation rules whose entire conclusion is
// "the recorder could not see enough to know anything" (ai/eval/gen/main.go
// encodes the identical set, for the same reason): a live confidence value
// on such an incident measures certainty THAT the recorder was blind, not
// certainty about an actual network root cause, so a model must never be
// asked to reason past it.
var blindnessRuleIDs = map[string]bool{
	"recorder-was-blind":     true,
	"collector-not-watching": true,
}

// Guardrail runs the decision table before anything is ever sent to a
// model (docs/product/adr/0006, execution order §4.10). It never returns
// an error: a request whose referenced evidence is missing from Events is
// a caller bug, not a "not enough evidence" situation, and is checked
// separately by MissingEvidence before Guardrail is ever called - see
// Analyze.
func Guardrail(req Request) GuardrailResult {
	ceiling := 100
	if req.Incident != nil {
		ceiling = int(req.Incident.Confidence)
	}
	res := GuardrailResult{Ceiling: ceiling}

	if req.Incident == nil {
		return res // rule 1: a general question with no incident proceeds uncapped
	}
	inc := req.Incident

	if blindnessRuleIDs[inc.RuleID] {
		res.Refuse = true
		res.Reasons = append(res.Reasons, Reason{
			Code:   "insufficient_evidence",
			Params: map[string]string{"reason": "rule_is_blindness", "rule_id": inc.RuleID},
		})
	}

	start, end := windowBounds(inc)
	for _, e := range req.Events {
		kind := eventKindOf(e)
		if kind != string(event.KindSystemGap) && kind != string(event.KindSystemDrop) {
			continue
		}
		if !withinWindow(eventTSWall(e), start, end) {
			continue
		}
		code := "gap_in_window"
		if kind == string(event.KindSystemDrop) {
			code = "drop_in_window"
		}
		res.Refuse = true
		res.Reasons = append(res.Reasons, Reason{Code: code, Params: map[string]string{"event_id": eventID(e)}})
	}
	for _, e := range req.Events {
		if eventKindOf(e) != string(event.KindCollectorDown) {
			continue
		}
		if !withinWindow(eventTSWall(e), start, end) {
			continue
		}
		res.Refuse = true
		res.Reasons = append(res.Reasons, Reason{
			Code:   "collector_down_in_window",
			Params: map[string]string{"collector": eventAttrString(e, "collector")},
		})
	}

	if req.Capabilities != nil {
		required := requiredKinds(inc)
		for _, snap := range req.Capabilities {
			if snap.Status == registry.StatusUp {
				continue
			}
			if coversAny(snap.Coverage, required) {
				code := "required_collector_down"
				if snap.Status == registry.StatusUnsupported {
					code = "required_collector_unsupported"
				}
				res.Refuse = true
				res.Reasons = append(res.Reasons, Reason{Code: code, Params: map[string]string{"collector": snap.Name}})
			} else {
				res.BlindFamilies = append(res.BlindFamilies, familiesOf(snap.Coverage)...)
			}
		}
	}

	if len(inc.Chain) == 0 || inc.Confidence == 0 {
		res.Refuse = true
		res.Reasons = append(res.Reasons, Reason{Code: "no_chain"})
	}

	return res
}

// MissingEvidence reports every event id an incident's root cause or chain
// names that Events does not actually contain - a request that omits one
// is malformed (a caller integration bug, not a genuinely inconclusive
// incident) and must be refused as invalid before Guardrail or a model
// ever sees it.
func MissingEvidence(req Request) []string {
	if req.Incident == nil {
		return nil
	}
	have := map[string]bool{}
	for _, e := range req.Events {
		if id := eventID(e); id != "" {
			have[id] = true
		}
	}
	var missing []string
	if id := req.Incident.RootCause.EventID; id != "" && !have[id] {
		missing = append(missing, id)
	}
	for _, link := range req.Incident.Chain {
		if link.EventID != "" && !have[link.EventID] {
			missing = append(missing, link.EventID)
		}
	}
	return missing
}

func windowBounds(inc *incident.Incident) (start, end int64) {
	pad := int64(windowPadSeconds) * int64(1e9)
	end = inc.ClosedAt
	if end == 0 {
		if len(inc.Chain) > 0 {
			end = inc.Chain[len(inc.Chain)-1].At
		} else {
			end = inc.OpenedAt
		}
	}
	return inc.OpenedAt - pad, end + pad
}

func withinWindow(ts, start, end int64) bool { return ts >= start && ts <= end }

func eventKindOf(e map[string]any) string {
	k, _ := e["kind"].(string)
	return k
}

// eventTSWall reads "ts_wall" regardless of whether it arrived as a Go
// float64 (the shape encoding/json produces when decoding into map[string]any)
// or an int64 (a value built in-process rather than round-tripped through JSON).
func eventTSWall(e map[string]any) int64 {
	switch v := e["ts_wall"].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	}
	return 0
}

func eventID(e map[string]any) string {
	id, _ := e["event_id"].(string)
	return id
}

func eventAttrString(e map[string]any, key string) string {
	attrs, _ := e["attrs"].(map[string]any)
	if attrs == nil {
		return ""
	}
	s, _ := attrs[key].(string)
	return s
}

// requiredKinds is the set of event kinds this incident's own conclusion
// rests on: its root cause and every link in its chain.
func requiredKinds(inc *incident.Incident) map[string]bool {
	kinds := map[string]bool{}
	if inc.RootCause.Kind != "" {
		kinds[string(inc.RootCause.Kind)] = true
	}
	for _, link := range inc.Chain {
		kinds[string(link.Kind)] = true
	}
	return kinds
}

// coversAny reports whether coverage names any kind in kinds, either
// exactly or via a family wildcard ("link.*"). A few Coverage entries in
// cmd/netrewindd/registry.go carry a trailing parenthetical for the
// capabilities page's own display (e.g. "l2.duplicate_ip (own address, via
// DAD)") - only the leading token is ever a kind pattern, so that is all
// this compares against.
func coversAny(coverage []string, kinds map[string]bool) bool {
	for _, c := range coverage {
		pattern := leadingToken(c)
		if pattern == "" {
			continue
		}
		if strings.HasSuffix(pattern, ".*") {
			prefix := strings.TrimSuffix(pattern, "*")
			for k := range kinds {
				if strings.HasPrefix(k, prefix) {
					return true
				}
			}
			continue
		}
		if kinds[pattern] {
			return true
		}
	}
	return false
}

// familiesOf extracts the kind-family prefix ("l2", "link") from each of a
// collector's Coverage entries, deduplicated and in first-seen order.
func familiesOf(coverage []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range coverage {
		pattern := leadingToken(c)
		family := strings.TrimSuffix(pattern, ".*")
		if i := strings.IndexByte(family, '.'); i >= 0 {
			family = family[:i]
		}
		if family == "" || seen[family] {
			continue
		}
		seen[family] = true
		out = append(out, family)
	}
	return out
}

func leadingToken(s string) string {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}
