package aimodel

import "testing"

func TestLoadEmbeddedManifestParsesAndVerifies(t *testing.T) {
	m, err := LoadEmbeddedManifest()
	if err != nil {
		t.Fatalf("LoadEmbeddedManifest: %v", err)
	}
	if len(m.Models) != 3 {
		t.Fatalf("got %d models, want 3", len(m.Models))
	}
	for _, profile := range []string{"small", "balanced", "full"} {
		model := m.ByProfile(profile)
		if model == nil {
			t.Fatalf("no %q profile in the embedded manifest", profile)
		}
		if model.SizeBytes <= 0 {
			t.Errorf("%s: size_bytes = %d, want > 0", profile, model.SizeBytes)
		}
		if len(model.SHA256) != 64 {
			t.Errorf("%s: sha256 = %q, want a 64-hex-char digest", profile, model.SHA256)
		}
		if model.License.SPDX == "" {
			t.Errorf("%s: license.spdx is empty", profile)
		}
		if model.Gate.Passed {
			t.Errorf("%s: gate.passed = true, but no profile has actually passed an evaluation gate yet", profile)
		}
	}
}
