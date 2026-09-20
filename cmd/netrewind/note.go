package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"text/tabwriter"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/incident"
	"github.com/OmarAlghafri/netrewind/internal/ipc"
	"github.com/OmarAlghafri/netrewind/internal/notes"
	"github.com/OmarAlghafri/netrewind/internal/store"
	"github.com/spf13/cobra"
)

// newNoteCmd records the operator's own conclusion about one incident -
// confirmed cause, false positive, or unresolved, with optional free text -
// into the recorder's separate notes store (ADR 0008; internal/notes's own
// doc explains why that store is not part of the record). The incident's
// rule/root-cause fields are read from the local record directly, the same
// way every other command here reads it; the note itself is sent over the
// API, because writing is the one thing notes.db allows that events.db does
// not, and the API is its only write surface.
func newNoteCmd() *cobra.Command {
	var (
		endpoint   string
		timeout    time.Duration
		outcomeArg string
		cause      string
		resolution string
	)
	cmd := &cobra.Command{
		Use:   "note <incident-id>",
		Short: "Record your own conclusion about an incident",
		Example: "  netrewind note 01J8ZQXK7X8VN5T4R6E9W1C2D3 --outcome confirmed --cause \"replaced the switch\"\n" +
			"  netrewind note 01J8ZQXK7X8VN5T4R6E9W1C2D3 --outcome false-positive",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			outcome, err := parseOutcomeFlag(outcomeArg)
			if err != nil {
				return err
			}

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

			if endpoint == "" {
				endpoint = ipc.DefaultPath()
			}
			client := ipc.HTTPClient(endpoint, timeout)
			apiCtx, apiCancel := context.WithTimeout(cmd.Context(), timeout)
			defer apiCancel()

			body := map[string]any{
				"rule_id":           inc.RuleID,
				"root_cause_kind":   string(inc.RootCause.Kind),
				"root_cause_entity": inc.RootCause.Entity,
				"opened_at_ns":      inc.OpenedAt,
				"outcome":           outcome,
				"cause_note":        cause,
				"resolution_note":   resolution,
			}
			var saved notes.Annotation
			if err := sendJSON(apiCtx, client, http.MethodPut, "/v1/notes/incidents/"+id, body, &saved); err != nil {
				return fmt.Errorf("recorder not reachable at %s: %w", endpoint, err)
			}
			fmt.Fprintf(safeOut(cmd), "recorded: %s on %s (%s)\n", saved.Outcome, id, saved.RootCauseKind)
			return nil
		},
	}
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "socket path (Linux) or pipe name (Windows) of the recorder's API; default: the platform default")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Second, "how long to wait for the recorder")
	cmd.Flags().StringVar(&outcomeArg, "outcome", "", "confirmed, false-positive, or unresolved (required)")
	cmd.Flags().StringVar(&cause, "cause", "", "free-text note on the real cause")
	cmd.Flags().StringVar(&resolution, "resolution", "", "free-text note on how it was resolved")
	cmd.MarkFlagRequired("outcome")
	return cmd
}

// newNotesCmd lists previously annotated incidents matching the same rule
// and root cause - "has this happened before, and what did we conclude last
// time" - read straight from the API, like newNoteCmd's write.
func newNotesCmd() *cobra.Command {
	var (
		endpoint      string
		timeout       time.Duration
		ruleID        string
		rootCauseKind string
		entity        string
		exclude       string
		limit         int
		output        string
	)
	cmd := &cobra.Command{
		Use:     "notes",
		Short:   "List your own past conclusions for incidents with the same rule and root cause",
		Example: "  netrewind notes --rule gateway-hijack --kind l2.arp_binding_changed --entity 10.99.0.1",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if ruleID == "" || rootCauseKind == "" {
				return fmt.Errorf("--rule and --kind are required")
			}
			if endpoint == "" {
				endpoint = ipc.DefaultPath()
			}
			client := ipc.HTTPClient(endpoint, timeout)
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			q := fmt.Sprintf("/v1/notes/similar?rule_id=%s&root_cause_kind=%s&entity=%s&exclude=%s&limit=%d",
				url.QueryEscape(ruleID), url.QueryEscape(rootCauseKind), url.QueryEscape(entity), url.QueryEscape(exclude), limit)
			var got []notes.Annotation
			if err := getJSON(ctx, client, q, &got); err != nil {
				return fmt.Errorf("recorder not reachable at %s: %w", endpoint, err)
			}

			out := safeOut(cmd)
			if output == "json" {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(got)
			}
			if len(got) == 0 {
				fmt.Fprintln(out, "no prior notes for this rule and root cause")
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "INCIDENT\tOUTCOME\tCAUSE\tRESOLUTION")
			for _, a := range got {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", a.IncidentID, a.Outcome, a.CauseNote, a.ResolutionNote)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "socket path (Linux) or pipe name (Windows) of the recorder's API; default: the platform default")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Second, "how long to wait for the recorder")
	cmd.Flags().StringVar(&ruleID, "rule", "", "rule id (required)")
	cmd.Flags().StringVar(&rootCauseKind, "kind", "", "root cause event kind (required)")
	cmd.Flags().StringVar(&entity, "entity", "", "root cause entity, e.g. an address")
	cmd.Flags().StringVar(&exclude, "exclude", "", "an incident id to leave out of the results")
	cmd.Flags().IntVar(&limit, "limit", 3, "maximum number of results")
	cmd.Flags().StringVarP(&output, "output", "o", "table", "table or json")
	return cmd
}

// parseOutcomeFlag accepts the hyphenated spelling a person types on a
// command line and maps it to notes' own underscored wire value.
func parseOutcomeFlag(s string) (notes.Outcome, error) {
	switch s {
	case "confirmed":
		return notes.OutcomeConfirmed, nil
	case "false-positive":
		return notes.OutcomeFalsePositive, nil
	case "unresolved":
		return notes.OutcomeUnresolved, nil
	default:
		return "", fmt.Errorf("--outcome must be confirmed, false-positive, or unresolved (got %q)", s)
	}
}

// findIncidentByID pages through the local record looking for one incident.
// There is no by-ID lookup in the store: incident IDs are ULIDs assigned
// when correlation closes the incident, not when its root-cause event
// happened, so the ID's own embedded timestamp cannot be trusted as a
// window to search around - the incident could have opened long before.
// The cap of pages bounds the worst case (an old incident on a long-lived
// recorder) to a fixed amount of work instead of an unbounded scan.
func findIncidentByID(ctx context.Context, st store.Store, id string) (*incident.Incident, error) {
	var cursor *store.IncidentCursor
	const pageSize = 500
	for page := 0; page < 50; page++ {
		f := store.IncidentFilter{Limit: pageSize}
		if cursor != nil {
			f.Cursor = cursor
		}
		batch, err := st.QueryIncidents(ctx, f)
		if err != nil {
			return nil, err
		}
		for _, inc := range batch {
			if inc.ID == id {
				return inc, nil
			}
		}
		if len(batch) < pageSize {
			break
		}
		last := batch[len(batch)-1]
		cursor = &store.IncidentCursor{OpenedAt: last.OpenedAt, ID: last.ID}
	}
	return nil, fmt.Errorf("no incident with id %s found in the local record (it may have aged out of retention)", id)
}

// sendJSON issues a PUT or POST with a JSON body against the recorder's
// local API and decodes the response into v (nil to discard it).
func sendJSON(ctx context.Context, client *http.Client, method, path string, body any, v any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://netrewind"+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("%s: %s: %s", path, resp.Status, respBody)
	}
	if v == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(v)
}
