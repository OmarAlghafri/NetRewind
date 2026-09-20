package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/aimodel"
	"github.com/spf13/cobra"
)

// aiManifestURL/aiManifestSignatureURL/aiManifestPublicKey are the model
// catalogue's coordinates, stamped at build time with -ldflags the same
// way `version` is, once ai/models/models.json is first published under
// the models-v1 tag (see docs/releasing.md's eventual "make sign-models"
// step). Empty until then - a build without them requires --manifest-url/
// --manifest-sig-url/--manifest-key explicitly rather than silently
// pointing at a URL or key that does not exist yet.
var (
	aiManifestURL          = ""
	aiManifestSignatureURL = ""
	aiManifestPublicKey    = ""
)

func newAIModelCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "model",
		Short: "List, download or remove local-AI model files",
		Long: "The catalogue (internal/aimodel's embedded models.json, or a remote one when\n" +
			"--manifest-url/-sig-url/-key are given) is signature-verified before anything\n" +
			"in it is trusted - a model profile is only ever downloadable when its own\n" +
			"gate.passed is true.",
	}
	cmd.PersistentFlags().String("manifest-url", aiManifestURL, "URL of the signed model catalogue (models.json)")
	cmd.PersistentFlags().String("manifest-sig-url", aiManifestSignatureURL, "URL of the catalogue's detached signature")
	cmd.PersistentFlags().String("manifest-key", aiManifestPublicKey, "base64 ed25519 public key the catalogue must be signed with")
	cmd.PersistentFlags().String("models-dir", "", "directory downloaded models are stored in; empty means the platform's per-user cache directory")
	cmd.AddCommand(newAIModelListCmd(), newAIModelManifestCmd(), newAIModelDownloadCmd(), newAIModelRemoveCmd())
	return cmd
}

// aiModelDir resolves --models-dir, defaulting to a per-user cache
// directory (never the recorder's own, usually service-owned, data
// directory - a model download must work for an ordinary user on a board
// with no recorder installed at all, per the plan's "CLI only" Small
// tier).
func aiModelDir(cmd *cobra.Command) (string, error) {
	dir, _ := cmd.Flags().GetString("models-dir")
	if dir != "" {
		return dir, nil
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("no per-user cache directory available on this platform; pass --models-dir explicitly: %w", err)
	}
	return filepath.Join(cacheDir, "netrewind", "ai-models"), nil
}

// fetchAIManifest prefers an explicitly configured remote catalogue (a
// future build's --manifest-url/-sig-url/-key or the aiManifestURL build
// vars above, once ai/models/models.json has somewhere public to be
// hosted); with none of those set, it falls back to the manifest embedded
// in this very binary (internal/aimodel.LoadEmbeddedManifest) rather than
// refusing outright - the interim source for as long as no release has
// been published to fetch one from.
func fetchAIManifest(cmd *cobra.Command) (*aimodel.Manifest, error) {
	url, _ := cmd.Flags().GetString("manifest-url")
	sigURL, _ := cmd.Flags().GetString("manifest-sig-url")
	key, _ := cmd.Flags().GetString("manifest-key")
	if url == "" && sigURL == "" && key == "" {
		return aimodel.LoadEmbeddedManifest()
	}
	if url == "" || sigURL == "" || key == "" {
		return nil, fmt.Errorf("incomplete model catalogue configuration; pass all of --manifest-url, --manifest-sig-url and --manifest-key, or none to use the embedded catalogue")
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()
	return aimodel.FetchManifest(ctx, nil, url, sigURL, key)
}

func newAIModelListCmd() *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List every model profile the catalogue offers, and whether it is installed",
		RunE: func(cmd *cobra.Command, _ []string) error {
			manifest, err := fetchAIManifest(cmd)
			if err != nil {
				return err
			}
			dir, err := aiModelDir(cmd)
			if err != nil {
				return err
			}

			type row struct {
				Profile   string `json:"profile"`
				ID        string `json:"id"`
				SizeBytes int64  `json:"size_bytes"`
				GatePass  bool   `json:"gate_passed"`
				Installed bool   `json:"installed"`
			}
			var rows []row
			for _, m := range manifest.Models {
				meta, err := aimodel.ReadMeta(dir, m.FileName)
				if err != nil {
					return err
				}
				rows = append(rows, row{Profile: m.Profile, ID: m.ID, SizeBytes: m.SizeBytes, GatePass: m.Gate.Passed, Installed: meta != nil})
			}

			out := safeOut(cmd)
			if output == "json" {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(rows)
			}
			tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "PROFILE\tSIZE\tGATE\tINSTALLED")
			for _, r := range rows {
				gate := "not offered"
				if r.GatePass {
					gate = "passed"
				}
				installed := "no"
				if r.Installed {
					installed = "yes"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Profile, formatBytes(r.SizeBytes), gate, installed)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "table", "table or json")
	return cmd
}

func newAIModelManifestCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "manifest",
		Short: "Print the verified catalogue as raw JSON",
		RunE: func(cmd *cobra.Command, _ []string) error {
			manifest, err := fetchAIManifest(cmd)
			if err != nil {
				return err
			}
			enc := json.NewEncoder(safeOut(cmd))
			enc.SetIndent("", "  ")
			return enc.Encode(manifest)
		},
	}
}

func newAIModelDownloadCmd() *cobra.Command {
	var jsonProgress bool
	cmd := &cobra.Command{
		Use:   "download <profile>",
		Short: "Download one model profile, resuming and verifying as needed",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			manifest, err := fetchAIManifest(cmd)
			if err != nil {
				return err
			}
			model := manifest.ByProfile(args[0])
			if model == nil {
				return fmt.Errorf("no profile %q in the catalogue", args[0])
			}
			if !model.Gate.Passed {
				return fmt.Errorf("profile %q has not passed its evaluation gate and is not offered", args[0])
			}
			dir, err := aiModelDir(cmd)
			if err != nil {
				return err
			}

			out := safeOut(cmd)
			fmt.Fprintf(out, "downloading %s (%s) to %s\n", model.ID, formatBytes(model.SizeBytes), filepath.Join(dir, model.FileName))

			lastPrint := time.Now()
			progress := func(p aimodel.Progress) {
				if !jsonProgress {
					return
				}
				if time.Since(lastPrint) < 500*time.Millisecond && p.Downloaded != p.Total {
					return
				}
				lastPrint = time.Now()
				b, _ := json.Marshal(struct {
					Downloaded int64 `json:"downloaded"`
					Total      int64 `json:"total"`
				}{p.Downloaded, p.Total})
				fmt.Fprintln(out, string(b))
			}
			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()
			if err := aimodel.Download(ctx, *model, dir, progress); err != nil {
				return err
			}
			fmt.Fprintln(out, "done")
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonProgress, "json", false, "print progress as JSON lines, at most every 500ms")
	return cmd
}

func newAIModelRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <profile>",
		Short: "Delete a downloaded model and its metadata",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			manifest, err := fetchAIManifest(cmd)
			if err != nil {
				return err
			}
			model := manifest.ByProfile(args[0])
			if model == nil {
				return fmt.Errorf("no profile %q in the catalogue", args[0])
			}
			dir, err := aiModelDir(cmd)
			if err != nil {
				return err
			}
			return aimodel.Remove(dir, model.FileName)
		},
	}
}

// formatBytes renders a byte count the way an operator reads a download
// size, e.g. "2.4 GB" - always base-1000 (GB, not GiB), matching how model
// hosts and browsers report download sizes rather than how a filesystem
// reports disk usage.
func formatBytes(n int64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "kMGTPE"[exp])
}
