package main

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/identity"
	"github.com/OmarAlghafri/netrewind/internal/store"
	"github.com/spf13/cobra"
)

func newTimelineCmd() *cobra.Command {
	var (
		last     time.Duration
		families []string
		minSev   string
	)
	cmd := &cobra.Command{
		Use:     "timeline",
		Short:   "Read recorded history as a narrative",
		Example: "  netrewind timeline --last 30m\n  netrewind timeline --last 2h --min-severity warn",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dbPath, _ := cmd.Flags().GetString("db")
			st, err := store.OpenSQLiteRead(dbPath)
			if err != nil {
				return err
			}
			defer st.Close()

			to := time.Now()
			from := to.Add(-last)
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()

			events, err := st.Query(ctx, store.Filter{Since: from, Until: to, Families: families, Limit: 2000})
			if err != nil {
				return err
			}
			return renderTimeline(safeOut(cmd), filterSeverity(events, minSev), from, to)
		},
	}
	cmd.Flags().DurationVar(&last, "last", 30*time.Minute, "how far back to read")
	cmd.Flags().StringSliceVar(&families, "family", nil, "restrict to these families, e.g. link,l2,l3")
	cmd.Flags().StringVar(&minSev, "min-severity", "info", "info, notice, warn or error")
	return cmd
}

func newWhatHappenedCmd() *cobra.Command {
	var (
		host   string
		at     string
		window time.Duration
	)
	cmd := &cobra.Command{
		Use:   "what-happened",
		Short: "Reconstruct what happened around a moment, for one machine or for all of them",
		Long: "Answers the question that can only be asked afterwards: the outage is over, the\n" +
			"evidence would normally be gone, and you still need to know what happened.",
		Example: "  netrewind what-happened --host 192.168.20.10 --at 15:00\n" +
			"  netrewind what-happened --at 2026-08-23T10:43:00Z --window 5m\n" +
			"  netrewind what-happened --host 192.168.20.10 --at -2h",
		RunE: func(cmd *cobra.Command, _ []string) error {
			centre, err := parseAt(at)
			if err != nil {
				return err
			}
			from, to := centre.Add(-window), centre.Add(window)

			dbPath, _ := cmd.Flags().GetString("db")
			st, err := store.OpenSQLiteRead(dbPath)
			if err != nil {
				return err
			}
			defer st.Close()

			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()

			labels, err := labelsToSearch(ctx, st, host, centre)
			if err != nil {
				return err
			}

			var found []*event.Event
			if len(labels) == 0 {
				found, err = st.Query(ctx, store.Filter{Since: from, Until: to, Limit: 2000})
				if err != nil {
					return err
				}
			} else {
				for _, label := range labels {
					batch, err := st.Query(ctx, store.Filter{
						Since: from, Until: to, SubjectLabel: label, Limit: 2000,
					})
					if err != nil {
						return err
					}
					found = append(found, batch...)
				}
			}

			// Whatever the question, the answer has to include whether the
			// recorder was actually watching. An empty window means nothing if
			// the recorder was down for it.
			health, err := st.Query(ctx, store.Filter{
				Since: from, Until: to, Families: []string{"system"}, Limit: 200,
			})
			if err != nil {
				return err
			}
			found = append(found, health...)

			out := safeOut(cmd)
			if len(labels) > 1 {
				fmt.Fprintf(out, "%s also answered to: %s\n\n", host,
					strings.Join(without(labels, host), ", "))
			}
			return renderTimeline(out, dedupe(found), from, to)
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "the machine to follow, by address or hardware address")
	cmd.Flags().StringVar(&at, "at", "now", "the moment to look around: RFC3339, HH:MM, -2h, or now")
	cmd.Flags().DurationVar(&window, "window", 5*time.Minute, "how far either side of that moment to look")
	return cmd
}

// labelsToSearch expands a host into every address it answered to around the
// moment in question.
//
// This is the point of the identity table. Asking about 192.168.20.10 has to
// find the events recorded while that machine was on a different address, and
// must not drag in events belonging to whichever other machine holds that
// address today.
func labelsToSearch(ctx context.Context, st *store.SQLite, host string, at time.Time) ([]string, error) {
	if host == "" {
		return nil, nil
	}
	labels := []string{host}
	for _, attrType := range []string{identity.AttrIPv4, identity.AttrIPv6, identity.AttrMAC, identity.AttrHostname} {
		hostID, ok, err := st.ResolveAt(ctx, attrType, host, at.UnixNano())
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		known, err := st.LabelsFor(ctx, hostID, at.Add(-24*time.Hour).UnixNano(), at.Add(24*time.Hour).UnixNano())
		if err != nil {
			return nil, err
		}
		labels = append(labels, known...)
		break
	}
	return unique(labels), nil
}

// renderTimeline prints events as a story: when, how long after the previous
// thing, and what it was in words.
func renderTimeline(w io.Writer, events []*event.Event, from, to time.Time) error {
	fmt.Fprintf(w, "%s to %s (%s)\n\n",
		from.Local().Format("15:04:05"), to.Local().Format("15:04:05"),
		to.Sub(from).Round(time.Second))

	if len(events) == 0 {
		fmt.Fprintln(w, "  nothing was recorded in this window")
		fmt.Fprintln(w, "\n  note: no record is not the same as nothing happening. Check for")
		fmt.Fprintln(w, "  system.gap events before concluding the network was quiet.")
		return nil
	}

	sort.SliceStable(events, func(i, j int) bool { return events[i].TSWall < events[j].TSWall })

	var prev time.Time
	for _, e := range events {
		t := e.WallTime().Local()
		gap := ""
		if !prev.IsZero() {
			gap = "+" + t.Sub(prev).Round(10*time.Millisecond).String()
		}
		line := fmt.Sprintf("  %-2s %s  %-9s  %s",
			marker(e.Severity), t.Format("15:04:05.000"), gap, event.Describe(e))
		if e.Count > 1 {
			line += fmt.Sprintf("  (x%d)", e.Count)
		}
		fmt.Fprintln(w, line)
		prev = t
	}
	return nil
}

// marker gives severity a shape that survives a monochrome terminal and a
// screenshot, which is where these end up.
func marker(s event.Severity) string {
	switch s {
	case event.SevError:
		return "!!"
	case event.SevWarn:
		return "!"
	case event.SevNotice:
		return "-"
	default:
		return " "
	}
}

func severityRank(s event.Severity) int {
	switch s {
	case event.SevError:
		return 3
	case event.SevWarn:
		return 2
	case event.SevNotice:
		return 1
	default:
		return 0
	}
}

func filterSeverity(events []*event.Event, min string) []*event.Event {
	floor := severityRank(event.Severity(min))
	if floor == 0 {
		return events
	}
	kept := events[:0]
	for _, e := range events {
		if severityRank(e.Severity) >= floor {
			kept = append(kept, e)
		}
	}
	return kept
}

func dedupe(events []*event.Event) []*event.Event {
	seen := make(map[string]bool, len(events))
	out := events[:0]
	for _, e := range events {
		if seen[e.ID] {
			continue
		}
		seen[e.ID] = true
		out = append(out, e)
	}
	return out
}

func unique(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func without(values []string, drop string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v != drop {
			out = append(out, v)
		}
	}
	return out
}

// parseAt accepts the ways someone actually types a moment: an absolute
// timestamp, a time of day meaning today, an offset back from now, or "now".
func parseAt(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "now" {
		return time.Now(), nil
	}
	if strings.HasPrefix(s, "-") || strings.HasPrefix(s, "+") {
		d, err := time.ParseDuration(s)
		if err != nil {
			// The parser's own complaint ("unknown unit") does not tell anyone
			// what would work, and this gets typed under pressure.
			return time.Time{}, fmt.Errorf("--at %q: %w; %s", s, err, atFormats)
		}
		return time.Now().Add(d), nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, nil
		}
	}
	// A bare time of day means today, which is what someone means when they
	// say "it broke at ten to eleven".
	now := time.Now()
	for _, layout := range []string{"15:04:05", "15:04"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return time.Date(now.Year(), now.Month(), now.Day(),
				t.Hour(), t.Minute(), t.Second(), 0, time.Local), nil
		}
	}
	return time.Time{}, fmt.Errorf("--at %q: %s", s, atFormats)
}

// atFormats is the one place the accepted forms are written down, so an error
// message and the flag's help can never drift apart.
const atFormats = "expected RFC3339, YYYY-MM-DD HH:MM, HH:MM, -2h, or now"
