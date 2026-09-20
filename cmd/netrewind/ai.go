package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/ai"
	"github.com/OmarAlghafri/netrewind/internal/incident"
	"github.com/OmarAlghafri/netrewind/internal/registry"
	"github.com/spf13/cobra"
)

func newAICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ai",
		Short: "Local AI assistant plumbing, used by the desktop shell and netrewind explain",
		Long: "These commands never open the event store or notes.db themselves: every\n" +
			"fact they reason over arrives already resolved on stdin, from whatever\n" +
			"already read the local record over the read-only API. That is what lets\n" +
			"the same request be replayed byte-for-byte by the evaluation gate.",
	}
	cmd.AddCommand(newAIAnalyzeCmd(), newAIModelCmd())
	return cmd
}

// aiAnalyzeRequest is the stdin contract for `netrewind ai analyze`. This
// is the subset of the 1.2.0 plan's full contract internal/ai.Analyze
// actually implements so far - follow-up thread history and a
// prompt_sha256/raw debug echo are not yet wired (see Analyze's own doc
// comment for what it does not yet do).
type aiAnalyzeRequest struct {
	Incident     *incident.Incident   `json:"incident"`
	Events       []map[string]any     `json:"events"`
	Capabilities []registry.Snapshot  `json:"capabilities"`
	Annotations  []aiAnnotationWire   `json:"annotations"`
	History      []*incident.Incident `json:"history"`
	Question     string               `json:"question"`
	Lang         string               `json:"lang"`
	Server       struct {
		URL   string `json:"url"`
		Token string `json:"token"`
	} `json:"server"`
	Policy struct {
		MaxTokens      int  `json:"max_tokens"`
		RetryMaxTokens int  `json:"retry_max_tokens"`
		NoRetry        bool `json:"no_retry"`
		TimeoutSeconds int  `json:"timeout_seconds"`
		MaxHistory     int  `json:"max_history"`
	} `json:"policy"`
}

type aiAnnotationWire struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// aiAnalyzeResponse is the stdout contract. version pins the shape for a
// caller that needs to detect a future breaking change.
type aiAnalyzeResponse struct {
	Version    int                `json:"version"`
	Verdict    ai.Verdict         `json:"verdict"`
	Guardrail  ai.GuardrailResult `json:"guardrail"`
	Handles    []aiHandleWire     `json:"handles"`
	Retrieval  []aiRetrievalWire  `json:"retrieval,omitempty"`
	Output     ai.ModelOutput     `json:"output"`
	Validation aiValidationWire   `json:"validation"`
	Timing     aiTimingWire       `json:"timing"`
	Error      string             `json:"error,omitempty"`
}

type aiHandleWire struct {
	Handle string `json:"handle"`
	Kind   string `json:"kind"`
	Ref    string `json:"ref"`
}

type aiRetrievalWire struct {
	Handle          string `json:"handle"`
	IncidentID      string `json:"incident_id"`
	RuleID          string `json:"rule_id"`
	RootCauseKind   string `json:"root_cause_kind"`
	RootCauseEntity string `json:"root_cause_entity"`
}

type aiValidationWire struct {
	OK                bool           `json:"ok"`
	Retried           bool           `json:"retried"`
	FirstAttemptValid bool           `json:"first_attempt_valid"`
	Violations        []ai.Violation `json:"violations"`
}

type aiTimingWire struct {
	PromptMS    float64 `json:"prompt_ms"`
	PredictedMS float64 `json:"predicted_ms"`
	TotalMS     float64 `json:"total_ms"`
}

// newAIAnalyzeCmd reads one aiAnalyzeRequest as JSON from stdin, runs
// internal/ai.Analyze, and writes one aiAnalyzeResponse as JSON to stdout.
// Exit codes distinguish "the pipeline ran and produced a verdict" (0,
// including insufficient_evidence and refused_by_model - those are correct
// outcomes, not failures) from "the answer never became valid even after
// the retry" (2), a malformed request (3), and a server that could not be
// reached at all (4) - a caller (the desktop shell, netrewind explain)
// scripts against these rather than parsing English text.
func newAIAnalyzeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "analyze",
		Short: "Run one local-AI analysis request (JSON on stdin, JSON on stdout)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			var req aiAnalyzeRequest
			dec := json.NewDecoder(cmd.InOrStdin())
			if err := dec.Decode(&req); err != nil {
				return aiExitError{code: 3, err: fmt.Errorf("stdin is not a valid analyze request: %w", err)}
			}
			if req.Server.URL == "" {
				return aiExitError{code: 3, err: fmt.Errorf("server.url is required")}
			}

			timeout := time.Duration(req.Policy.TimeoutSeconds) * time.Second
			if timeout <= 0 {
				timeout = 180 * time.Second
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			aiReq := ai.Request{
				Incident: req.Incident, Events: req.Events, Capabilities: req.Capabilities, History: req.History,
			}
			var annotations []ai.AnnotationRef
			for _, a := range req.Annotations {
				annotations = append(annotations, ai.AnnotationRef{ID: a.ID, Text: a.Text})
			}
			policy := ai.Policy{
				MaxTokens: req.Policy.MaxTokens, RetryMaxTokens: req.Policy.RetryMaxTokens,
				NoRetry: req.Policy.NoRetry, Token: req.Server.Token, MaxHistory: req.Policy.MaxHistory,
			}

			client := ai.NewHTTPClient(timeout)
			result, err := ai.Analyze(ctx, client, req.Server.URL, aiReq, annotations, req.Question, req.Lang, policy)
			if err != nil {
				return classifyAnalyzeError(err)
			}

			resp := aiAnalyzeResponse{
				Version: 1, Verdict: result.Verdict, Guardrail: result.Guardrail,
				Handles: handleWireFrom(result.Handles), Retrieval: retrievalWireFrom(result.Handles, result.History),
				Output: result.Output,
				Validation: aiValidationWire{
					OK: result.Verdict != ai.VerdictInvalid, Retried: result.Retried,
					FirstAttemptValid: result.FirstAttemptValid, Violations: nonNilViolations(result.Violations),
				},
				Timing: aiTimingWire{
					PromptMS: result.Timing.PromptMS, PredictedMS: result.Timing.PredictedMS,
					TotalMS: result.Timing.PromptMS + result.Timing.PredictedMS,
				},
			}

			out := safeOut(cmd)
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			if err := enc.Encode(resp); err != nil {
				return err
			}
			if result.Verdict == ai.VerdictInvalid {
				return aiExitError{code: 2, err: fmt.Errorf("answer did not pass validation even after the retry")}
			}
			return nil
		},
	}
	return cmd
}

// aiExitError carries a specific process exit code through cobra's error
// return path - main.go's top-level handler still prints err.Error() to
// stderr, but newRootCmd's caller checks for this type to exit with
// something more specific than the blanket 1 every other command uses.
type aiExitError struct {
	code int
	err  error
}

func (e aiExitError) Error() string { return e.err.Error() }
func (e aiExitError) Unwrap() error { return e.err }

// classifyAnalyzeError maps an internal/ai.Analyze error to the CLI
// contract's exit codes: an *ai.InvalidRequestError is the one path
// Analyze itself reports as a plain error rather than a Result (a caller
// integration bug - exit 3); anything else reaching this point failed to
// even complete an HTTP round trip to the server (exit 4).
func classifyAnalyzeError(err error) error {
	var invalid *ai.InvalidRequestError
	if errors.As(err, &invalid) {
		return aiExitError{code: 3, err: err}
	}
	return aiExitError{code: 4, err: fmt.Errorf("local-AI server not reachable: %w", err)}
}

func handleWireFrom(hm ai.HandleMap) []aiHandleWire {
	out := make([]aiHandleWire, 0, len(hm.Handles))
	for _, h := range hm.Handles {
		out = append(out, aiHandleWire{Handle: h, Kind: hm.Kind(h), Ref: hm.ToID[h]})
	}
	return out
}

func retrievalWireFrom(hm ai.HandleMap, history []*incident.Incident) []aiRetrievalWire {
	if len(history) == 0 {
		return nil
	}
	out := make([]aiRetrievalWire, 0, len(history))
	for _, inc := range history {
		fp := ai.Fingerprint(inc.RuleID, string(inc.RootCause.Kind), inc.RootCause.Entity)
		out = append(out, aiRetrievalWire{
			Handle: hm.ToHandle[fp], IncidentID: inc.ID, RuleID: inc.RuleID,
			RootCauseKind: string(inc.RootCause.Kind), RootCauseEntity: inc.RootCause.Entity,
		})
	}
	return out
}

// nonNilViolations makes the JSON field "[]" rather than "null" when there
// are none - the shape a client parses is then uniform regardless of
// outcome.
func nonNilViolations(v []ai.Violation) []ai.Violation {
	if v == nil {
		return []ai.Violation{}
	}
	return v
}
