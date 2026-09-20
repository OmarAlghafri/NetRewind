package aimodel

import (
	"embed"
	"encoding/json"
	"fmt"

	"github.com/OmarAlghafri/netrewind/internal/update"
)

// EmbeddedManifestPublicKey is the base64 ed25519 public key models.json is
// signed with (make sign-models, this package's own signing key -
// Makefile's SIGNING_KEY, the same key used for release checksums). Baked
// into the binary rather than admin-configured (unlike internal/update's
// self-update key), matching the plan's "URL and public key are constants
// in the shell and the CLI."
const EmbeddedManifestPublicKey = "L3Q7cNQWNu50IjcKdJpCHTZXvwdk+qg3aN8c98UmZn8="

//go:embed models.json models.json.sig
var embeddedManifestFS embed.FS

// LoadEmbeddedManifest verifies and parses the catalogue built into this
// binary - the interim source while ai/models/models.json has nowhere
// public to be hosted over HTTPS yet (no release has been published;
// FetchManifest stays the path a future build switches to once one has).
// Signature-verified the same way a fetched one would be: an embedded
// manifest is only as trustworthy as the binary it ships in, and checking
// it here catches a corrupted or hand-edited copy the same way a fetched
// one catches a tampered download.
func LoadEmbeddedManifest() (*Manifest, error) {
	data, err := embeddedManifestFS.ReadFile("models.json")
	if err != nil {
		return nil, fmt.Errorf("aimodel: embedded manifest: %w", err)
	}
	sig, err := embeddedManifestFS.ReadFile("models.json.sig")
	if err != nil {
		return nil, fmt.Errorf("aimodel: embedded manifest signature: %w", err)
	}
	if err := update.VerifySignature(EmbeddedManifestPublicKey, data, sig); err != nil {
		return nil, fmt.Errorf("aimodel: embedded manifest signature invalid: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("aimodel: embedded manifest is not valid json: %w", err)
	}
	return &m, nil
}
