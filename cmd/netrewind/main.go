// Command netrewind queries the recorded network history.
//
// The whole point of the tool is the question you can only ask afterwards:
// the outage is over, the evidence would normally be gone, and you still need
// to know what happened.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/store"
	"github.com/spf13/cobra"
)

// version is stamped at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "netrewind:", err)
		os.Exit(exitCodeFor(err))
	}
}

// exitCodeFor is 1 for an ordinary command failure, matching every command
// here except `ai analyze`, whose stdout contract distinguishes a
// malformed request, an unreachable server, and an answer that never
// passed validation from each other - see aiExitError.
func exitCodeFor(err error) int {
	var withCode aiExitError
	if errors.As(err, &withCode) {
		return withCode.code
	}
	return 1
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "netrewind",
		Short:         "Query recorded network history",
		Long:          "NetRewind records what changed on the network so it can be replayed after the fact.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().String("db", store.DefaultPath(), "path to the event store")
	root.AddCommand(
		newEventsCmd(), newTimelineCmd(), newWhatHappenedCmd(),
		newIncidentsCmd(), newRulesCmd(), newServeCmd(), newStatusCmd(),
		newBundleCmd(), newVersionCmd(), newNoteCmd(), newNotesCmd(), newAICmd(),
		newExplainCmd(),
	)
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintf(safeOut(cmd), "netrewind %s (schema v%d)\n", version, event.SchemaVersion)
		},
	}
}

func newEventsCmd() *cobra.Command {
	var (
		last     time.Duration
		since    string
		until    string
		kinds    []string
		families []string
		host     string
		limit    int
		output   string
		newest   bool
	)

	cmd := &cobra.Command{
		Use:   "events",
		Short: "List recorded events in time order",
		Example: "  netrewind events --last 10m\n" +
			"  netrewind events --last 1h --family link\n" +
			"  netrewind events --last 24h --host 192.168.20.10 -o json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			f := store.Filter{
				SubjectLabel: host,
				Limit:        limit,
				Descending:   newest,
			}
			for _, k := range kinds {
				f.Kinds = append(f.Kinds, event.Kind(k))
			}
			if err := checkFamilies(families); err != nil {
				return err
			}
			f.Families = families

			var err error
			if f.Since, f.Until, err = resolveWindow(last, since, until); err != nil {
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

			events, err := st.Query(ctx, f)
			if err != nil {
				return err
			}
			return render(safeOut(cmd), events, output)
		},
	}

	cmd.Flags().DurationVar(&last, "last", time.Hour, "look back this far from now")
	cmd.Flags().StringVar(&since, "since", "", "window start (RFC3339), overrides --last")
	cmd.Flags().StringVar(&until, "until", "", "window end (RFC3339), defaults to now")
	cmd.Flags().StringSliceVar(&kinds, "kind", nil, "restrict to these event kinds, e.g. link.down")
	cmd.Flags().StringSliceVar(&families, "family", nil, "restrict to these families, e.g. link,l2")
	cmd.Flags().StringVar(&host, "host", "", "restrict to one entity, by address or name")
	cmd.Flags().IntVar(&limit, "limit", store.DefaultLimit, "maximum events to return")
	cmd.Flags().StringVarP(&output, "output", "o", "table", "table or json")
	cmd.Flags().BoolVar(&newest, "newest-first", false, "reverse the order")
	return cmd
}

// resolveWindow turns the three mutually-informing time flags into one range.
func resolveWindow(last time.Duration, since, until string) (from, to time.Time, err error) {
	to = time.Now()
	if until != "" {
		if to, err = time.Parse(time.RFC3339, until); err != nil {
			return from, to, fmt.Errorf("--until: %w", err)
		}
	}
	if since != "" {
		if from, err = time.Parse(time.RFC3339, since); err != nil {
			return from, to, fmt.Errorf("--since: %w", err)
		}
		return from, to, nil
	}
	return to.Add(-last), to, nil
}

func render(w interface {
	Write([]byte) (int, error)
}, events []*event.Event, format string) error {
	switch format {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(events)
	case "table":
		return renderTable(w, events)
	default:
		return fmt.Errorf("unknown output format %q (want table or json)", format)
	}
}

func renderTable(w interface {
	Write([]byte) (int, error)
}, events []*event.Event) error {
	if len(events) == 0 {
		fmt.Fprintln(w, "no events in this window")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TIME\tKIND\tSUBJECT\tSEV\tDETAIL")
	for _, e := range events {
		detail := summarise(e.Attrs)
		if e.Count > 1 {
			detail = fmt.Sprintf("x%d %s", e.Count, detail)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			e.WallTime().Local().Format("15:04:05.000"),
			e.Kind, e.Subject.Label, e.Severity, detail)
	}
	return tw.Flush()
}

// summarise renders kind-specific attributes compactly, in a stable order so
// two runs of the same query produce comparable output.
func summarise(attrs map[string]any) string {
	if len(attrs) == 0 {
		return ""
	}
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		if k == "ifname" { // already shown as the subject
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, attrs[k]))
	}
	return strings.Join(parts, " ")
}

// checkFamilies refuses a family this schema does not define.
//
// Accepting one and matching nothing would answer a misspelling with "no events
// in this window" - the same words a genuinely quiet network produces. The
// whole tool exists so that an absence of events means something, and it cannot
// mean anything if a typo produces one.
func checkFamilies(families []string) error {
	for _, f := range families {
		if !event.KnownFamily(f) {
			return fmt.Errorf("%q is not an event family. Known families: %s",
				f, strings.Join(event.Families, ", "))
		}
	}
	return nil
}
