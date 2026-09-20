// Package aimodel is the model side of NetRewind's local AI assistant: a
// signed catalogue of downloadable models (never bundled - see ADR 0006)
// and a resumable, verified download into the desktop's or CLI's own data
// directory. Nothing here ever runs a model or opens a network port; it
// only fetches and verifies files.
package aimodel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/OmarAlghafri/netrewind/internal/update"
)

// License names a model's licence, kept alongside id/revision/checksum in
// the manifest so an in-app "technical details" panel can show and link it
// without the app needing its own copy of every model's licence text.
type License struct {
	SPDX string `json:"spdx"`
	URL  string `json:"url"`
}

// Gate records whether this model passed its pre-registered evaluation gate
// (docs/agent-state/AI_EVAL_PRE_REGISTERED_GATE.md and its siblings) on its
// tier's minimum hardware. A model is offered to a user only when this is
// true - the manifest, not a build constant, is what turns the feature on,
// because the same binary ships every profile and the gate result is what
// tells them apart.
type Gate struct {
	Passed   bool   `json:"passed"`
	Evidence string `json:"evidence,omitempty"`
}

// Model is one entry in the catalogue: everything needed to decide whether
// to offer it, download it, and verify it stayed what the manifest said.
type Model struct {
	// Profile is the tier shown in the UI (small/balanced/full) - never
	// the model's own name, which stays in the details block below.
	Profile              string  `json:"profile"`
	ID                   string  `json:"id"`
	Revision             string  `json:"revision"`
	Quantization         string  `json:"quantization"`
	FileName             string  `json:"file_name"`
	URL                  string  `json:"url"`
	SizeBytes            int64   `json:"size_bytes"`
	SHA256               string  `json:"sha256"`
	License              License `json:"license"`
	MinRAMBytes          int64   `json:"min_ram_bytes"`
	MinRAMAvailableBytes int64   `json:"min_ram_available_bytes"`
	RecommendedCtx       int     `json:"recommended_ctx"`
	RuntimeMinBuild      string  `json:"runtime_min_build"`
	Gate                 Gate    `json:"gate"`
}

// Manifest is the whole signed catalogue (ai/models/models.json, published
// under the fixed tag models-v1 - see FetchManifest).
type Manifest struct {
	Version int     `json:"version"`
	Models  []Model `json:"models"`
}

// ByProfile returns the model for a profile name, or nil if the manifest
// does not offer one.
func (m *Manifest) ByProfile(profile string) *Model {
	for i := range m.Models {
		if m.Models[i].Profile == profile {
			return &m.Models[i]
		}
	}
	return nil
}

// manifestMaxBytes bounds the catalogue fetch. A real models.json lists a
// handful of profiles; anything past a megabyte is not that file.
const manifestMaxBytes = 1 << 20

// signatureMaxBytes bounds the detached signature fetch.
const signatureMaxBytes = 4096

// FetchManifest downloads models.json and its detached signature over
// HTTPS and verifies the signature before trusting or parsing anything -
// the manifest names URLs and checksums a download will later be told to
// trust, so an unverified one is as dangerous as an unverified binary.
//
// client lets a caller supply timeouts; passing nil uses http.DefaultClient.
func FetchManifest(ctx context.Context, client *http.Client, manifestURL, signatureURL, publicKeyBase64 string) (*Manifest, error) {
	if !strings.HasPrefix(manifestURL, "https://") || !strings.HasPrefix(signatureURL, "https://") {
		return nil, fmt.Errorf("aimodel: manifest and signature urls must be https")
	}
	if client == nil {
		client = http.DefaultClient
	}
	data, err := fetchBytes(ctx, client, manifestURL, manifestMaxBytes)
	if err != nil {
		return nil, fmt.Errorf("aimodel: fetch manifest: %w", err)
	}
	sig, err := fetchBytes(ctx, client, signatureURL, signatureMaxBytes)
	if err != nil {
		return nil, fmt.Errorf("aimodel: fetch manifest signature: %w", err)
	}
	if err := update.VerifySignature(publicKeyBase64, data, sig); err != nil {
		return nil, fmt.Errorf("aimodel: manifest signature invalid: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("aimodel: manifest is not valid json: %w", err)
	}
	return &m, nil
}

func fetchBytes(ctx context.Context, client *http.Client, url string, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	limited := io.LimitReader(resp.Body, max+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%s: larger than %d bytes, refused", url, max)
	}
	return data, nil
}
