package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/correlate"
	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/incident"
	"github.com/OmarAlghafri/netrewind/internal/store"
	"github.com/spf13/cobra"
)

func newIncidentsCmd() *cobra.Command {
	var (
		last   time.Duration
		minSev string
		ruleID string
		output string
		limit  int
	)
	cmd := &cobra.Command{
		Use:   "incidents",
		Short: "List what correlation concluded, with the chain behind each conclusion",
		Long: "An incident is not a louder alert. It has a beginning and an end, the machines\n" +
			"it affected, and a chain in which every link names the event it rests on and\n" +
			"states whether that event caused the next one or merely came before it.",
		Example: "  netrewind incidents --last 24h\n" +
			"  netrewind incidents --last 7d --min-severity error\n" +
			"  netrewind incidents --rule gateway-hijack -o json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dbPath, _ := cmd.Flags().GetString("db")
			st, err := store.OpenSQLiteRead(dbPath)
			if err != nil {
				return err
			}
			defer st.Close()

			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()

			found, err := st.QueryIncidents(ctx, store.IncidentFilter{
				Since:       time.Now().Add(-last),
				RuleID:      ruleID,
				MinSeverity: event.Severity(minSev),
				Limit:       limit,
			})
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if output == "json" {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(found)
			}
			if len(found) == 0 {
				fmt.Fprintln(out, "no incidents in this window")
				fmt.Fprintln(out, "\nnote: no incident is not the same as no problem. Correlation only")
				fmt.Fprintln(out, "reports shapes it has a rule for; check the timeline as well.")
				return nil
			}
			for i, inc := range found {
				if i > 0 {
					fmt.Fprintln(out)
				}
				renderIncident(out, inc)
			}
			return nil
		},
	}
	cmd.Flags().DurationVar(&last, "last", 24*time.Hour, "how far back to look")
	cmd.Flags().StringVar(&minSev, "min-severity", "", "info, notice, warn or error")
	cmd.Flags().StringVar(&ruleID, "rule", "", "only incidents from this rule")
	cmd.Flags().StringVarP(&output, "output", "o", "text", "text or json")
	cmd.Flags().IntVar(&limit, "limit", 50, "maximum incidents to return")
	return cmd
}

func renderIncident(w io.Writer, inc *incident.Incident) {
	opened := time.Unix(0, inc.OpenedAt).Local()
	closed := time.Unix(0, inc.ClosedAt).Local()

	fmt.Fprintf(w, "%s %s\n", marker(inc.Severity), inc.Title)
	fmt.Fprintf(w, "   %s to %s  (%s)   rule %s, confidence %d%%\n",
		opened.Format("15:04:05"), closed.Format("15:04:05"),
		time.Duration(inc.Duration()).Round(time.Second), inc.RuleID, inc.Confidence)
	if len(inc.Victims) > 0 {
		fmt.Fprintf(w, "   affected: %s\n", strings.Join(inc.Victims, ", "))
	}
	fmt.Fprintln(w)

	for i, link := range inc.Chain {
		at := time.Unix(0, link.At).Local()
		count := ""
		if n, ok := link.Evidence["matched_count"]; ok {
			count = fmt.Sprintf("  (x%v)", n)
		}
		fmt.Fprintf(w, "   %d  %s  %s  %s%s\n", i+1,
			at.Format("15:04:05.000"), link.Kind, link.Subject, count)
		for _, line := range wrap(link.Why, 66) {
			fmt.Fprintf(w, "      %s\n", line)
		}
		// The relation belongs between the links, because it is a claim about
		// the step from one to the next rather than about either of them.
		if i < len(inc.Chain)-1 {
			fmt.Fprintf(w, "      %s\n", relationArrow(inc.Chain[i+1].Relation))
		}
	}

	fmt.Fprintf(w, "\n   root cause: %s on %s (confidence %d%%)\n",
		inc.RootCause.Kind, inc.RootCause.Entity, inc.RootCause.Confidence)
	if inc.Advice != "" {
		fmt.Fprintln(w, "   next:")
		for _, line := range wrap(inc.Advice, 66) {
			fmt.Fprintf(w, "      %s\n", line)
		}
	}
}

// relationArrow spells out the claim being made, rather than drawing an arrow
// that would let a reader assume causation everywhere.
func relationArrow(r incident.Relation) string {
	switch r {
	case incident.RelCauses:
		return "|  which caused"
	case incident.RelCorrelates:
		return "|  and at the same time"
	case incident.RelPrecedes:
		return "|  and then, without a known link"
	default:
		return "|"
	}
}

func newRulesCmd() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "rules",
		Short: "Load and check the correlation rules",
		Long: "Rules are files rather than code, so anyone who has debugged a network can\n" +
			"contribute one. This validates them the same way the recorder does.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			rules, err := correlate.LoadRules(dir)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "%d rules loaded from %s\n\n", len(rules), dir)
			for _, r := range rules {
				required := 0
				for _, c := range r.Match {
					if !c.Optional {
						required++
					}
				}
				fmt.Fprintf(out, "  %-26s %-6s %3d%%  %d clauses (%d required)  window %s\n",
					r.ID, r.Severity, r.Confidence, len(r.Match), required, r.Window)
				fmt.Fprintf(out, "  %-26s %s\n\n", "", r.Title)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "rules", "directory of rule files")
	return cmd
}

// wrap breaks text onto lines short enough to read in a terminal, without
// hyphenating or breaking words.
func wrap(text string, width int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	var lines []string
	line := words[0]
	for _, w := range words[1:] {
		if len(line)+1+len(w) > width {
			lines = append(lines, line)
			line = w
			continue
		}
		line += " " + w
	}
	return append(lines, line)
}
