package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// modelTestServer serves a signed manifest (one passed profile "small",
// one not-yet-gated "full") and the small profile's own file content, all
// from one httptest server - manifest, signature and model asset alike,
// the same way one release actually publishes them together.
//
// aimodel.FetchManifest and aimodel.Download both correctly refuse a
// non-https URL, and neither exposes a way to inject a client (the CLI
// commands built on them intentionally do not either - a real download
// always talks to a real HTTPS host). A TLS test server plus a temporary
// swap of http.DefaultClient/DefaultTransport (restored via t.Cleanup) is
// what actually exercises that real code path instead of testing around it.
func modelTestServer(t *testing.T) (srv *httptest.Server, publicKeyB64 string, smallContent []byte) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	smallContent = []byte(strings.Repeat("m", 5000))
	smallSum := sha256.Sum256(smallContent)
	// full.gguf is a real, downloadable file too (correct size/hash) even
	// though its gate has not passed - otherwise a broken gate check would
	// still be "caught" by a 404 on a URL nobody serves, proving nothing
	// about the check itself. See TestAIModelDownloadRefusesAnUngatedProfile,
	// which found exactly this masking the first time this fixture only
	// registered small.gguf.
	fullContent := []byte(strings.Repeat("f", 3000))
	fullSum := sha256.Sum256(fullContent)

	mux := http.NewServeMux()
	srv = httptest.NewTLSServer(mux)
	trustTestServer(t, srv)
	manifestJSON, err := json.Marshal(map[string]any{
		"version": 1,
		"models": []map[string]any{
			{
				"profile": "small", "id": "test-small", "file_name": "small.gguf",
				"url": srv.URL + "/small.gguf", "size_bytes": len(smallContent), "sha256": hex.EncodeToString(smallSum[:]),
				"gate": map[string]any{"passed": true},
			},
			{
				"profile": "full", "id": "test-full", "file_name": "full.gguf",
				"url": srv.URL + "/full.gguf", "size_bytes": len(fullContent), "sha256": hex.EncodeToString(fullSum[:]),
				"gate": map[string]any{"passed": false},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(priv, manifestJSON)
	mux.HandleFunc("/models.json", func(w http.ResponseWriter, r *http.Request) { w.Write(manifestJSON) })
	mux.HandleFunc("/models.json.sig", func(w http.ResponseWriter, r *http.Request) { w.Write(sig) })
	mux.HandleFunc("/small.gguf", func(w http.ResponseWriter, r *http.Request) { w.Write(smallContent) })
	mux.HandleFunc("/full.gguf", func(w http.ResponseWriter, r *http.Request) { w.Write(fullContent) })

	return srv, base64.StdEncoding.EncodeToString(pub), smallContent
}

// trustTestServer makes http.DefaultClient/DefaultTransport - what
// aimodel.FetchManifest(nil, ...) and aimodel.Download both actually use -
// trust srv's self-signed certificate for the duration of the calling
// test, restoring the originals afterward.
func trustTestServer(t *testing.T, srv *httptest.Server) {
	t.Helper()
	origClient, origTransport := http.DefaultClient, http.DefaultTransport
	http.DefaultClient = srv.Client()
	http.DefaultTransport = srv.Client().Transport
	t.Cleanup(func() {
		http.DefaultClient, http.DefaultTransport = origClient, origTransport
	})
}

func modelFlags(srv *httptest.Server, pubKey, modelsDir string) []string {
	return []string{
		"--manifest-url", srv.URL + "/models.json",
		"--manifest-sig-url", srv.URL + "/models.json.sig",
		"--manifest-key", pubKey,
		"--models-dir", modelsDir,
	}
}

func TestAIModelListShowsEveryProfileAndGateStatus(t *testing.T) {
	srv, pubKey, _ := modelTestServer(t)
	defer srv.Close()

	args := append([]string{"ai", "model", "list"}, modelFlags(srv, pubKey, t.TempDir())...)
	out, err := run(t, args...)
	if err != nil {
		t.Fatalf("ai model list: %v\n%s", err, out)
	}
	if !strings.Contains(out, "small") || !strings.Contains(out, "passed") {
		t.Errorf("output does not show the passed small profile:\n%s", out)
	}
	if !strings.Contains(out, "full") || !strings.Contains(out, "not offered") {
		t.Errorf("output does not show the ungated full profile as not offered:\n%s", out)
	}
}

// TestAIModelListFallsBackToTheEmbeddedCatalogueWithoutManifestCoordinates
// pins the interim-source behavior: no --manifest-url/-sig-url/-key means
// internal/aimodel.LoadEmbeddedManifest, not a refusal - there is nowhere
// public to fetch a catalogue from yet (no release has been published),
// but the binary still ships one, signature-verified, of its own.
func TestAIModelListFallsBackToTheEmbeddedCatalogueWithoutManifestCoordinates(t *testing.T) {
	out, err := run(t, "ai", "model", "list", "-o", "json")
	if err != nil {
		t.Fatalf("ai model list: %v\n%s", err, out)
	}
	var rows []struct {
		Profile  string `json:"profile"`
		FileName string `json:"file_name"`
		GatePass bool   `json:"gate_passed"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("decoding output: %v\n%s", err, out)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d profiles, want the embedded catalogue's 3: %+v", len(rows), rows)
	}
	for _, r := range rows {
		if r.GatePass {
			t.Errorf("profile %q reports gate_passed=true - none has actually passed an evaluation gate yet", r.Profile)
		}
		// file_name is what a caller (the desktop shell) must write into
		// aiSettings.modelFileName after a successful download, so
		// ai_runtime_start later resolves the right file - a row without
		// it would leave that caller with no way to know what to write.
		if r.FileName == "" {
			t.Errorf("profile %q has an empty file_name", r.Profile)
		}
	}
}

// TestAIModelListRefusesAnIncompleteManifestConfiguration proves that
// naming only some of the three remote-catalogue flags is a request error,
// not a silent fall-through to the embedded catalogue - the all-or-nothing
// check fetchAIManifest itself relies on.
func TestAIModelListRefusesAnIncompleteManifestConfiguration(t *testing.T) {
	_, err := run(t, "ai", "model", "list", "--manifest-url", "https://example.com/models.json")
	if err == nil {
		t.Fatal("ai model list with only --manifest-url set was accepted")
	}
}

func TestAIModelDownloadThenListShowsInstalled(t *testing.T) {
	srv, pubKey, content := modelTestServer(t)
	defer srv.Close()
	dir := t.TempDir()

	out, err := run(t, append([]string{"ai", "model", "download", "small"}, modelFlags(srv, pubKey, dir)...)...)
	if err != nil {
		t.Fatalf("ai model download: %v\n%s", err, out)
	}
	got, err := os.ReadFile(filepath.Join(dir, "small.gguf"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Error("downloaded file content does not match the server's")
	}

	listOut, err := run(t, append([]string{"ai", "model", "list"}, modelFlags(srv, pubKey, dir)...)...)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(listOut, "\n") {
		if strings.HasPrefix(line, "small") && !strings.Contains(line, "yes") {
			t.Errorf("small profile not shown as installed after download: %q", line)
		}
	}
}

func TestAIModelDownloadRefusesAnUngatedProfile(t *testing.T) {
	srv, pubKey, _ := modelTestServer(t)
	defer srv.Close()

	_, err := run(t, append([]string{"ai", "model", "download", "full"}, modelFlags(srv, pubKey, t.TempDir())...)...)
	if err == nil {
		t.Fatal("downloading a profile that has not passed its evaluation gate was accepted")
	}
}

func TestAIModelDownloadRefusesAnUnknownProfile(t *testing.T) {
	srv, pubKey, _ := modelTestServer(t)
	defer srv.Close()

	_, err := run(t, append([]string{"ai", "model", "download", "nonexistent"}, modelFlags(srv, pubKey, t.TempDir())...)...)
	if err == nil {
		t.Fatal("downloading a profile not in the catalogue was accepted")
	}
}

func TestAIModelRemoveDeletesADownloadedModel(t *testing.T) {
	srv, pubKey, _ := modelTestServer(t)
	defer srv.Close()
	dir := t.TempDir()

	if _, err := run(t, append([]string{"ai", "model", "download", "small"}, modelFlags(srv, pubKey, dir)...)...); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, append([]string{"ai", "model", "remove", "small"}, modelFlags(srv, pubKey, dir)...)...); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "small.gguf")); !os.IsNotExist(err) {
		t.Error("model file still present after remove")
	}
}

func TestAIModelManifestPrintsVerifiedJSON(t *testing.T) {
	srv, pubKey, _ := modelTestServer(t)
	defer srv.Close()

	out, err := run(t, append([]string{"ai", "model", "manifest"}, modelFlags(srv, pubKey, t.TempDir())...)...)
	if err != nil {
		t.Fatalf("ai model manifest: %v\n%s", err, out)
	}
	var m struct {
		Models []struct {
			Profile string `json:"profile"`
		} `json:"models"`
	}
	if jerr := json.Unmarshal([]byte(out), &m); jerr != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", jerr, out)
	}
	if len(m.Models) != 2 {
		t.Errorf("got %d models, want 2", len(m.Models))
	}
}

func TestAIModelManifestRejectsATamperedSignature(t *testing.T) {
	srv, _, _ := modelTestServer(t)
	defer srv.Close()
	otherPub, _, err := ed25519.GenerateKey(nil) // a key that never signed this server's manifest
	if err != nil {
		t.Fatal(err)
	}
	otherPubKey := base64.StdEncoding.EncodeToString(otherPub)

	_, err = run(t, append([]string{"ai", "model", "manifest"}, modelFlags(srv, otherPubKey, t.TempDir())...)...)
	if err == nil {
		t.Fatal("a manifest verified against the wrong public key was accepted")
	}
}
