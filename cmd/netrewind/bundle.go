package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/bundle"
	"github.com/OmarAlghafri/netrewind/internal/notes"
	"github.com/OmarAlghafri/netrewind/internal/store"
	"github.com/spf13/cobra"
)

// newBundleCmd groups the evidence-bundle operations: export a window of the
// record to a self-contained archive, inspect and verify one, or import one
// into a fresh store. Export reads the store file directly, like every other
// query here, so it works on a machine whose recorder is stopped.
func newBundleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bundle",
		Short: "Export, inspect or import an evidence bundle",
		Long: "An evidence bundle is a tar.gz of the events and incidents in a time window,\n" +
			"with a manifest and a checksum file, meant to leave the machine and be opened\n" +
			"elsewhere. DNS names are redacted unless --include-secrets is given.",
	}
	cmd.AddCommand(newBundleExportCmd(), newBundleInspectCmd(), newBundleImportCmd())
	return cmd
}

func newBundleExportCmd() *cobra.Command {
	var (
		out            string
		last           time.Duration
		since, until   string
		includeSecrets bool
		observer       string
	)
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Write a window of the record to a bundle file",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dbPath, _ := cmd.Flags().GetString("db")
			to := time.Now()
			from := to.Add(-last)
			if until != "" {
				t, err := parseWhen(until)
				if err != nil {
					return fmt.Errorf("--until: %w", err)
				}
				to = t
				from = to.Add(-last)
			}
			if since != "" {
				t, err := parseWhen(since)
				if err != nil {
					return fmt.Errorf("--since: %w", err)
				}
				from = t
			}
			if out == "" {
				out = fmt.Sprintf("netrewind-%s.tar.gz", to.UTC().Format("20060102T150405Z"))
			}
			if _, err := os.Stat(out); err == nil {
				return fmt.Errorf("%s already exists; choose another --out", out)
			}
			st, err := store.OpenSQLiteRead(dbPath)
			if err != nil {
				return err
			}
			defer st.Close()
			if observer == "" {
				observer, _ = os.Hostname()
			}
			f, err := os.OpenFile(out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
			if err != nil {
				return err
			}
			// notes.db beside events.db, opened only if it already exists -
			// this command must not create a fresh empty one just because
			// it ran against an events.db that happens to have no sibling
			// notes store (an old bundle, a bare CLI-only deployment).
			var notesStore notes.Store
			notesPath := notes.DefaultPath(filepath.Dir(dbPath))
			if _, statErr := os.Stat(notesPath); statErr == nil {
				ns, openErr := notes.OpenSQLite(notesPath)
				if openErr != nil {
					f.Close()
					os.Remove(out)
					return fmt.Errorf("opening %s: %w", notesPath, openErr)
				}
				defer ns.Close()
				notesStore = ns
			}
			m, err := bundle.Export(context.Background(), st, f, bundle.ExportOptions{
				From: from, To: to, AppVersion: version, ObserverID: observer, IncludeSecrets: includeSecrets,
				Notes: notesStore,
			})
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				os.Remove(out)
				return err
			}
			w := safeOut(cmd)
			fmt.Fprintf(w, "wrote %s: %d events, %d incidents, %s to %s",
				out, m.EventCount, m.IncidentCount, m.WindowFrom.Local().Format(time.RFC3339), m.WindowTo.Local().Format(time.RFC3339))
			if m.Redacted {
				fmt.Fprint(w, " (DNS names redacted)")
			}
			if m.Truncated {
				fmt.Fprint(w, " - TRUNCATED: the window holds more than fits in one bundle")
			}
			fmt.Fprintln(w)
			return nil
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "file to write; default netrewind-<time>.tar.gz")
	cmd.Flags().DurationVar(&last, "last", 24*time.Hour, "how far back from --until (or now) the window reaches")
	cmd.Flags().StringVar(&since, "since", "", "start of the window (RFC3339 or a duration ago, e.g. 2h)")
	cmd.Flags().StringVar(&until, "until", "", "end of the window (RFC3339 or a duration ago); default now")
	cmd.Flags().BoolVar(&includeSecrets, "include-secrets", false, "keep DNS names in the bundle instead of redacting them")
	cmd.Flags().StringVar(&observer, "observer", "", "observer id to stamp on the manifest; default this hostname")
	return cmd
}

func newBundleInspectCmd() *cobra.Command {
	var publicKey string
	cmd := &cobra.Command{
		Use:   "inspect <bundle.tar.gz>",
		Short: "Verify a bundle and describe what it holds, without importing it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := os.Open(args[0])
			if err != nil {
				return err
			}
			defer f.Close()
			c, err := bundle.Inspect(f, publicKey)
			if err != nil {
				return err
			}
			w := safeOut(cmd)
			m := c.Manifest
			fmt.Fprintf(w, "bundle %s\n", args[0])
			fmt.Fprintf(w, "  format v%d, schema v%d, written by NetRewind %s on %s\n", m.FormatVersion, m.SchemaVersion, m.AppVersion, m.ObserverID)
			fmt.Fprintf(w, "  created %s\n", m.CreatedAt.Local().Format(time.RFC3339))
			fmt.Fprintf(w, "  window  %s to %s\n", m.WindowFrom.Local().Format(time.RFC3339), m.WindowTo.Local().Format(time.RFC3339))
			fmt.Fprintf(w, "  %d events, %d incidents, %d collectors described\n", len(c.Events), len(c.Incidents), len(m.Capabilities))
			switch {
			case c.Signed:
				fmt.Fprintln(w, "  signature: verified")
			case publicKey != "":
				fmt.Fprintln(w, "  signature: required and verified")
			default:
				fmt.Fprintln(w, "  signature: not checked (pass --public-key to require one)")
			}
			if m.Redacted {
				fmt.Fprintln(w, "  DNS names: redacted")
			}
			if m.Truncated {
				fmt.Fprintln(w, "  TRUNCATED: the source window held more than this bundle carries")
			}
			fmt.Fprintln(w, "  checksums: verified")
			return nil
		},
	}
	cmd.Flags().StringVar(&publicKey, "public-key", "", "base64 ed25519 key the bundle must be signed with")
	return cmd
}

func newBundleImportCmd() *cobra.Command {
	var (
		into      string
		publicKey string
	)
	cmd := &cobra.Command{
		Use:   "import <bundle.tar.gz>",
		Short: "Verify a bundle and turn it into a new, separate event store",
		Long: "The bundle is checked (checksums, and the signature when --public-key is given)\n" +
			"and written to a new store at --into, which must not exist yet. The recorder's\n" +
			"own store is never touched; query the imported one with --db.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if into == "" {
				return fmt.Errorf("--into is required: the path of the new store to create")
			}
			f, err := os.Open(args[0])
			if err != nil {
				return err
			}
			defer f.Close()
			m, err := bundle.Import(f, bundle.ImportOptions{DestPath: into, PublicKey: publicKey})
			if err != nil {
				return err
			}
			fmt.Fprintf(safeOut(cmd), "imported %d events and %d incidents from %s into %s\n"+
				"query it with: netrewind --db %s timeline\n",
				m.EventCount, m.IncidentCount, m.ObserverID, into, into)
			return nil
		},
	}
	cmd.Flags().StringVar(&into, "into", "", "path of the new store to create (must not exist)")
	cmd.Flags().StringVar(&publicKey, "public-key", "", "base64 ed25519 key the bundle must be signed with")
	return cmd
}

// parseWhen accepts an RFC3339 timestamp or a duration meaning "that long
// ago" (2h, 30m, 7d is not a Go duration - write 168h).
func parseWhen(s string) (time.Time, error) {
	if d, err := time.ParseDuration(s); err == nil {
		return time.Now().Add(-d), nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("%q is neither RFC3339 nor a duration like 2h", s)
	}
	return t, nil
}
