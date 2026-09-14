package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"text/tabwriter"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/ipc"
	"github.com/OmarAlghafri/netrewind/internal/registry"
	"github.com/spf13/cobra"
)

// newStatusCmd asks the running recorder, over its local API, how it is
// doing: version, uptime, store size, and which collectors are watching.
// It is the one command here that talks to the daemon rather than reading
// the store file, which is why it works while the recorder holds the store
// open and needs no filesystem access to the record at all.
func newStatusCmd() *cobra.Command {
	var (
		endpoint string
		output   string
		timeout  time.Duration
	)
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Ask the running recorder what it is watching",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if endpoint == "" {
				endpoint = ipc.DefaultPath()
			}
			client := ipc.HTTPClient(endpoint, timeout)
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			var health map[string]any
			if err := getJSON(ctx, client, "/v1/health", &health); err != nil {
				return fmt.Errorf("recorder not reachable at %s: %w", endpoint, err)
			}
			var caps struct {
				Capabilities []registry.Snapshot `json:"capabilities"`
			}
			if err := getJSON(ctx, client, "/v1/capabilities", &caps); err != nil {
				return err
			}

			out := safeOut(cmd)
			if output == "json" {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{"health": health, "capabilities": caps.Capabilities})
			}
			fmt.Fprintf(out, "recorder %v on %v, up %vs, %v events stored (%v)\n",
				health["version"], health["observer_id"], health["uptime_seconds"],
				nested(health, "store", "events"), nested(health, "store", "path"))
			fmt.Fprintf(out, "endpoint %s\n\n", endpoint)
			tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "COLLECTOR\tSTATUS\tPLATFORM\tCOVERS\tNOTE")
			for _, c := range caps.Capabilities {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", c.Name, c.Status, c.Platform, joinCoverage(c.Coverage), c.Reason)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "socket path (Linux) or pipe name (Windows) of the recorder's API; default: the platform default")
	cmd.Flags().StringVarP(&output, "output", "o", "table", "table or json")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Second, "how long to wait for the recorder")
	return cmd
}

func getJSON(ctx context.Context, client *http.Client, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://netrewind"+path, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("%s: %s: %s", path, resp.Status, body)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

func nested(m map[string]any, keys ...string) any {
	var cur any = m
	for _, k := range keys {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = mm[k]
	}
	return cur
}

func joinCoverage(c []string) string {
	if len(c) == 0 {
		return "-"
	}
	s := c[0]
	for _, x := range c[1:] {
		s += ", " + x
	}
	return s
}
