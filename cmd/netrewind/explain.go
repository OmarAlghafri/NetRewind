package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/ai"
	"github.com/OmarAlghafri/netrewind/internal/incident"
	"github.com/OmarAlghafri/netrewind/internal/ipc"
	"github.com/OmarAlghafri/netrewind/internal/notes"
	"github.com/OmarAlghafri/netrewind/internal/store"
	"github.com/spf13/cobra"
)

// explainWindowPad is how far outside an incident's own [opened, end] span
// the surrounding events are pulled in from - enough context for a model
// (or a person) to see what led up to and followed the chain, without
// pulling in an unrelated day's worth of history.
const explainWindowPad = 5 * time.Minute

// hypothesisBanner is printed above every result: the whole point of
// execution order §4.10's "button says analyze locally, never find the
// cause" principle is that this output is never mistaken for the
// deterministic engine's own conclusion.
const hypothesisBanner = "NOTE: this is a local model's hypothesis, not confirmed evidence - cross-check it against the recorded events below."

// newExplainCmd asks a local model to explain one already-recorded
// incident, over an already-running llama-server this command does not
// yet start itself (the sidecar runtime that would - Phase 5 of the
// 1.2.0 plan - is Rust desktop-shell work, not built yet). It reads the
// incident and its surrounding window directly from the local record
// (like every other read command here), and its own operator note (if
// any) over the API - never writing anything, and never touching notes.db
// directly even to read it.
func newExplainCmd() *cobra.Command {
	var (
		serverURL string
		token     string
		endpoint  string
		lang      string
		ask       string
		timeout   time.Duration
	)
	cmd := &cobra.Command{
		Use:   "explain <incident-id>",
		Short: "Ask a local model to explain one incident (requires an already-running llama-server)",
		Long: "There is no automatic model runtime yet - point --server at an llama-server\n" +
			"you started yourself, serving a model that has passed its evaluation gate.\n" +
			"The desktop application will eventually start and manage this for you.",
		Example: "  netrewind explain 01J8ZQXK7X8VN5T4R6E9W1C2D3 --server http://127.0.0.1:8080",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if serverURL == "" {
				return fmt.Errorf("--server is required (no local model runtime is managed automatically yet)")
			}
			id := args[0]

			dbPath, _ := cmd.Flags().GetString("db")
			st, err := store.OpenSQLiteRead(dbPath)
			if err != nil {
				return err
			}
			defer st.Close()

			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()

			inc, err := findIncidentByID(ctx, st, id)
			if err != nil {
				return err
			}
			events, err := explainWindowEvents(ctx, st, inc)
			if err != nil {
				return err
			}
			history, err := explainHistoryCandidates(ctx, st, inc)
			if err != nil {
				return err
			}
			annotations := explainAnnotations(ctx, endpoint, id, timeout)

			question := ask
			if question == "" {
				question = ai.DefaultQuestion(lang)
			}

			analyzeCtx, analyzeCancel := context.WithTimeout(cmd.Context(), timeout)
			defer analyzeCancel()
			req := ai.Request{Incident: inc, Events: events, History: history}
			policy := ai.Policy{Token: token}
			result, err := ai.Analyze(analyzeCtx, ai.NewHTTPClient(timeout), serverURL, req, annotations, question, lang, policy)
			if err != nil {
				return err
			}

			return printExplainResult(safeOut(cmd), result)
		},
	}
	cmd.Flags().StringVar(&serverURL, "server", "", "llama-server URL, e.g. http://127.0.0.1:8080 (required)")
	cmd.Flags().StringVar(&token, "token", "", "bearer token, if the server requires one")
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "the recorder's own API socket/pipe, for reading this incident's operator note; default: the platform default")
	cmd.Flags().StringVar(&lang, "lang", "en", "en or ar - the language to ask in and expect the answer in")
	cmd.Flags().StringVar(&ask, "ask", "", "a specific question; default asks what caused it and what to check next")
	cmd.Flags().DurationVar(&timeout, "timeout", 180*time.Second, "how long to wait for the model")
	return cmd
}

// explainWindowEvents queries the incident's own [opened-pad, end+pad]
// window - a superset of every chain event by construction, since chain
// events happen inside [opened, end] - and always includes the root cause
// event id even in the unlikely case an incident's own bookkeeping placed
// it fractionally outside that span.
func explainWindowEvents(ctx context.Context, st store.Store, inc *incident.Incident) ([]map[string]any, error) {
	end := inc.ClosedAt
	if end == 0 {
		if len(inc.Chain) > 0 {
			end = inc.Chain[len(inc.Chain)-1].At
		} else {
			end = inc.OpenedAt
		}
	}
	since := time.Unix(0, inc.OpenedAt).Add(-explainWindowPad)
	until := time.Unix(0, end).Add(explainWindowPad)

	found, err := st.Query(ctx, store.Filter{Since: since, Until: until, Limit: 200})
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(found)
	if err != nil {
		return nil, err
	}
	var events []map[string]any
	if err := json.Unmarshal(b, &events); err != nil {
		return nil, err
	}
	return events, nil
}

// explainHistoryCandidates gives RankSimilar a pool to choose from: recent
// incidents from the same rule (a bounded, relevant query rather than the
// whole history table), excluding the target itself.
func explainHistoryCandidates(ctx context.Context, st store.Store, inc *incident.Incident) ([]*incident.Incident, error) {
	if inc.RuleID == "" {
		return nil, nil
	}
	found, err := st.QueryIncidents(ctx, store.IncidentFilter{RuleID: inc.RuleID, Limit: 200})
	if err != nil {
		return nil, err
	}
	return found, nil
}

// explainAnnotations fetches the target incident's own operator note over
// the API, if the recorder's notes surface is reachable and one exists -
// never opening notes.db itself. A recorder with notes disabled, or simply
// not running, degrades to "no annotations" rather than failing the whole
// explanation: an operator's own note is helpful context, not a
// requirement.
func explainAnnotations(ctx context.Context, endpoint, incidentID string, timeout time.Duration) []ai.AnnotationRef {
	if endpoint == "" {
		endpoint = ipc.DefaultPath()
	}
	client := ipc.HTTPClient(endpoint, timeout)
	var a notes.Annotation
	if err := getJSON(ctx, client, "/v1/notes/incidents/"+incidentID, &a); err != nil {
		return nil
	}
	text := strings.TrimSpace(a.CauseNote + " " + a.ResolutionNote)
	if text == "" {
		return nil
	}
	return []ai.AnnotationRef{{ID: a.IncidentID, Text: string(a.Outcome) + ": " + text}}
}

func printExplainResult(w io.Writer, result ai.Result) error {
	fmt.Fprintln(w, hypothesisBanner)
	fmt.Fprintln(w)
	switch result.Verdict {
	case ai.VerdictInsufficientEvidence:
		fmt.Fprintln(w, "The model was not asked: there is not enough evidence to draw a conclusion.")
		for _, r := range result.Guardrail.Reasons {
			fmt.Fprintf(w, "  - %s %v\n", r.Code, r.Params)
		}
		return nil
	case ai.VerdictRefusedByModel:
		fmt.Fprintln(w, "The model declined to offer a cause. What it says is unknown:")
		for _, u := range result.Output.Unknowns {
			fmt.Fprintf(w, "  - %s\n", u)
		}
		return nil
	case ai.VerdictInvalid:
		fmt.Fprintln(w, "The answer did not pass validation even after a retry:")
		for _, v := range result.Violations {
			fmt.Fprintf(w, "  - %s: %s\n", v.Code, v.Detail)
		}
		return fmt.Errorf("answer did not pass validation")
	}

	fmt.Fprintln(w, result.Output.Summary)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Ranked hypotheses:")
	for _, h := range result.Output.RankedHypotheses {
		fmt.Fprintf(w, "  - %s on %s (confidence %d, ceiling %d)\n", h.Cause, h.Entity, h.Confidence, result.Guardrail.Ceiling)
	}
	if len(result.Output.Unknowns) > 0 {
		fmt.Fprintln(w, "Unknowns:")
		for _, u := range result.Output.Unknowns {
			fmt.Fprintf(w, "  - %s\n", u)
		}
	}
	if len(result.Output.NextChecks) > 0 {
		fmt.Fprintln(w, "Suggested next checks:")
		for _, c := range result.Output.NextChecks {
			fmt.Fprintf(w, "  - %s\n", c)
		}
	}
	return nil
}
