package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/OmarAlghafri/netrewind/internal/incident"
)

// InvalidRequestError is what Analyze returns when the caller's own
// request is malformed - specifically, when the incident's root cause or
// chain names an event id that Events does not actually contain
// (MissingEvidence). This is never the network's fault and never the
// model's fault, so it is a distinct Go type a caller can recognise with
// errors.As instead of matching on error text (see cmd/netrewind/ai.go's
// exit-code mapping for why that distinction matters to a script).
type InvalidRequestError struct {
	MissingEventIDs []string
}

func (e *InvalidRequestError) Error() string {
	return fmt.Sprintf("ai: request references events not offered as evidence: %v", e.MissingEventIDs)
}

// AnnotationRef is one operator note offered to the model as an A-handle -
// already resolved to its own identity and rendered text by the caller
// (internal/notes.Annotation on the daemon side, or the CLI's own decode
// of a GET /v1/notes/similar response) rather than this package importing
// internal/notes directly, keeping this core library's dependencies to
// exactly what the evaluation runner and the CLI both need.
type AnnotationRef struct {
	ID   string
	Text string
}

// Policy tunes one Analyze call: token budgets, whether the one allowed
// retry runs, and the bearer token the sidecar runtime's per-session
// llama-server expects (empty for the evaluation runner's tokenless,
// operator-started server).
type Policy struct {
	MaxTokens      int
	RetryMaxTokens int
	NoRetry        bool
	Token          string
	// MaxHistory bounds how many prior incidents RankSimilar offers.
	MaxHistory int
}

func (p Policy) withDefaults() Policy {
	if p.MaxTokens <= 0 {
		p.MaxTokens = 700
	}
	if p.RetryMaxTokens <= 0 {
		p.RetryMaxTokens = 1000
	}
	if p.MaxHistory <= 0 {
		p.MaxHistory = 3
	}
	return p
}

// Verdict is Analyze's final outcome.
type Verdict string

const (
	// VerdictAnswered: at least one hypothesis, and it passed every check.
	VerdictAnswered Verdict = "answered"
	// VerdictInsufficientEvidence: Guardrail refused before a model was
	// ever called - see Result.Guardrail.Reasons.
	VerdictInsufficientEvidence Verdict = "insufficient_evidence"
	// VerdictRefusedByModel: the model itself declined (empty hypotheses,
	// non-empty unknowns) - a proper refusal, not a failure.
	VerdictRefusedByModel Verdict = "refused_by_model"
	// VerdictInvalid: the answer (after the one allowed retry, if it ran)
	// still failed a post-answer check - see Result.Violations.
	VerdictInvalid Verdict = "invalid"
)

// Result is Analyze's complete outcome - the Go-side shape the CLI's
// `netrewind ai analyze` and, eventually, the desktop's ai_analyze command
// serialize to their own stdout/IPC contracts.
type Result struct {
	Verdict Verdict
	// Guardrail is always populated, refused or not - Ceiling and
	// BlindFamilies matter even on a normal answer.
	Guardrail GuardrailResult
	// Handles is the exact offered set for this request (events, then
	// ranked history, then annotations) - what a caller resolves the
	// model's own evidence_handles/citations against.
	Handles HandleMap
	// History is the ranked prior-incident set actually offered (already
	// capped to Policy.MaxHistory) - empty unless req.Incident and
	// req.History both produced at least one match.
	History []*incident.Incident
	// Output is the parsed answer. Zero value when Verdict is
	// insufficient_evidence (no model was ever called) or when extraction
	// failed on both attempts (Verdict invalid, Violations names why).
	Output     ModelOutput
	Violations []Violation
	// Retried reports whether the one allowed retry actually ran.
	Retried bool
	// FirstAttemptValid reports whether the FIRST attempt (before any
	// retry) already had zero violations - tracked separately from
	// Verdict so a gate can report "how often did we need the retry" as
	// its own number.
	FirstAttemptValid bool
	Timing            ChatResult
}

// repairInstruction is the fixed follow-up turn sent on the one allowed
// retry - execution order §4.10 permits exactly one retry, on a malformed
// or structurally-unsafe first attempt, and this is deliberately generic
// rather than naming each violation: the model already has the schema: it
// needs telling that the last attempt was rejected, not a longer prompt.
const repairInstruction = "Your previous answer did not satisfy the required rules (it may have cited a handle you were not given, claimed more confidence than allowed, or was not valid JSON matching the schema). Reply again with ONLY a single JSON object that fully satisfies every rule."

// Analyze runs the whole pipeline: Guardrail decides whether a model may
// be asked at all; if so, the prompt is built from req's events plus
// RankSimilar's top history and req.Annotations, sent once, validated, and
// - only if every violation present is retry-eligible - resent once with a
// repair turn at a higher token budget. It never panics on a malformed
// request: MissingEvidence is checked first and reported as an
// *InvalidRequestError (a caller integration bug, exit code 3 in the CLI
// contract - see errors.As), distinct from every other outcome, which is a
// Result with no error.
func Analyze(ctx context.Context, client *http.Client, serverURL string, req Request, annotations []AnnotationRef, question, lang string, policy Policy) (Result, error) {
	if missing := MissingEvidence(req); len(missing) > 0 {
		return Result{}, &InvalidRequestError{MissingEventIDs: missing}
	}
	policy = policy.withDefaults()

	guard := Guardrail(req)
	hm := BuildHandles(req.Events)
	ranked := RankSimilar(req.Incident, req.History, policy.MaxHistory)
	hm.AddHistory(fingerprintsOf(ranked))
	hm.AddAnnotations(annotationIDsOf(annotations))

	result := Result{Guardrail: guard, Handles: hm, History: ranked}
	if guard.Refuse {
		result.Verdict = VerdictInsufficientEvidence
		return result, nil
	}

	eventsJSON, err := marshalRedacted(hm, req.Events)
	if err != nil {
		return Result{}, fmt.Errorf("ai: marshal events: %w", err)
	}
	historyJSON, err := marshalHistory(hm, ranked)
	if err != nil {
		return Result{}, fmt.Errorf("ai: marshal history: %w", err)
	}
	annotationsJSON, err := marshalAnnotations(hm, annotations)
	if err != nil {
		return Result{}, fmt.Errorf("ai: marshal annotations: %w", err)
	}
	userPrompt := BuildUserPrompt(eventsJSON, historyJSON, annotationsJSON, question)
	schema := JSONSchemaFor(hm.Handles, guard.Ceiling)

	offeredKinds := offeredKindSet(req.Events)
	vi := ValidateInput{
		Handles: hm, OfferedKinds: offeredKinds, BlindFamilies: guard.BlindFamilies,
		Ceiling: guard.Ceiling, Lang: lang, RawInputText: userPrompt,
	}

	messages := []ChatMessage{{Role: "system", Content: SystemPrompt}, {Role: "user", Content: userPrompt}}
	out, violations, timing, raw, err := attempt(ctx, client, serverURL, policy.Token, messages, policy.MaxTokens, schema, vi)
	if err != nil {
		return Result{}, fmt.Errorf("ai: chat completion: %w", err)
	}
	result.FirstAttemptValid = len(violations) == 0
	result.Timing = timing

	if !policy.NoRetry && len(violations) > 0 && allRetryEligible(violations) {
		messages = append(messages,
			ChatMessage{Role: "assistant", Content: raw},
			ChatMessage{Role: "user", Content: repairInstruction})
		out, violations, timing, _, err = attempt(ctx, client, serverURL, policy.Token, messages, policy.RetryMaxTokens, schema, vi)
		if err != nil {
			return Result{}, fmt.Errorf("ai: retry chat completion: %w", err)
		}
		result.Retried = true
		result.Timing = timing
	}

	result.Output, result.Violations = out, violations
	switch {
	case len(violations) > 0:
		result.Verdict = VerdictInvalid
	case len(out.RankedHypotheses) == 0:
		result.Verdict = VerdictRefusedByModel
	default:
		result.Verdict = VerdictAnswered
	}
	return result, nil
}

// attempt runs exactly one model call, then extraction, then Validate. An
// extraction failure is reported as a single ViolationSchemaInvalid rather
// than a Go error, so the retry decision (allRetryEligible) treats a
// malformed sample the same way it treats an unknown-handle or
// over-confidence violation - all three are "resend once", per the plan.
func attempt(ctx context.Context, client *http.Client, serverURL, token string, messages []ChatMessage, maxTokens int, schema map[string]any, vi ValidateInput) (ModelOutput, []Violation, ChatResult, string, error) {
	chatResult, err := Chat(ctx, client, serverURL, token, messages, maxTokens, schema)
	if err != nil {
		return ModelOutput{}, nil, chatResult, "", err
	}
	var out ModelOutput
	if err := ExtractModelOutput(chatResult.Content, &out); err != nil {
		return ModelOutput{}, []Violation{{Code: ViolationSchemaInvalid, Detail: err.Error()}}, chatResult, chatResult.Content, nil
	}
	return out, Validate(out, vi), chatResult, chatResult.Content, nil
}

func allRetryEligible(violations []Violation) bool {
	for _, v := range violations {
		if !v.Code.RetryEligible() {
			return false
		}
	}
	return true
}

func fingerprintsOf(incidents []*incident.Incident) []string {
	out := make([]string, len(incidents))
	for i, inc := range incidents {
		out[i] = Fingerprint(inc.RuleID, string(inc.RootCause.Kind), inc.RootCause.Entity)
	}
	return out
}

func annotationIDsOf(annotations []AnnotationRef) []string {
	out := make([]string, len(annotations))
	for i, a := range annotations {
		out[i] = a.ID
	}
	return out
}

func offeredKindSet(events []map[string]any) map[string]bool {
	kinds := make(map[string]bool, len(events))
	for _, e := range events {
		if k := eventKindOf(e); k != "" {
			kinds[k] = true
		}
	}
	return kinds
}

func marshalRedacted(hm HandleMap, events []map[string]any) (string, error) {
	redacted := make([]map[string]any, len(events))
	for i, e := range events {
		redacted[i] = hm.RedactEvent(e)
	}
	b, err := json.Marshal(redacted)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// marshalHistory renders each offered prior incident as its handle plus
// the fields relevant to "has this happened before": rule, root cause,
// outcome is not included here (it lives in the annotation, if any exists
// for that same fingerprint - this block is what the deterministic engine
// itself already knew about the prior incident, independent of whether an
// operator ever annotated it).
func marshalHistory(hm HandleMap, incidents []*incident.Incident) (string, error) {
	if len(incidents) == 0 {
		return "", nil
	}
	type entry struct {
		Handle          string `json:"handle"`
		RuleID          string `json:"rule_id"`
		RootCauseKind   string `json:"root_cause_kind"`
		RootCauseEntity string `json:"root_cause_entity"`
	}
	entries := make([]entry, len(incidents))
	for i, inc := range incidents {
		fp := Fingerprint(inc.RuleID, string(inc.RootCause.Kind), inc.RootCause.Entity)
		entries[i] = entry{
			Handle: hm.ToHandle[fp], RuleID: inc.RuleID,
			RootCauseKind: string(inc.RootCause.Kind), RootCauseEntity: inc.RootCause.Entity,
		}
	}
	b, err := json.Marshal(entries)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func marshalAnnotations(hm HandleMap, annotations []AnnotationRef) (string, error) {
	if len(annotations) == 0 {
		return "", nil
	}
	type entry struct {
		Handle string `json:"handle"`
		Text   string `json:"text"`
	}
	entries := make([]entry, len(annotations))
	for i, a := range annotations {
		entries[i] = entry{Handle: hm.ToHandle[a.ID], Text: a.Text}
	}
	b, err := json.Marshal(entries)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
