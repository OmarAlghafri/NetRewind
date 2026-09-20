package aimodel

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testManifestJSON = `{"version":1,"models":[
  {"profile":"small","id":"m-small","file_name":"small.gguf","size_bytes":100,"sha256":"abc"},
  {"profile":"full","id":"m-full","file_name":"full.gguf","size_bytes":200,"sha256":"def","gate":{"passed":true}}
]}`

// manifestServer serves manifestJSON at /models.json, signed with a freshly
// generated ed25519 key, and returns the server plus the base64 public key
// FetchManifest needs to verify it.
func manifestServer(t *testing.T, manifestJSON string) (*httptest.Server, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(priv, []byte(manifestJSON))
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models.json":
			w.Write([]byte(manifestJSON))
		case "/models.json.sig":
			w.Write(sig)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return srv, base64.StdEncoding.EncodeToString(pub)
}

func TestFetchManifestVerifiesAndParses(t *testing.T) {
	srv, pubKey := manifestServer(t, testManifestJSON)
	defer srv.Close()

	m, err := FetchManifest(context.Background(), srv.Client(), srv.URL+"/models.json", srv.URL+"/models.json.sig", pubKey)
	if err != nil {
		t.Fatalf("FetchManifest: %v", err)
	}
	if len(m.Models) != 2 {
		t.Fatalf("got %d models, want 2", len(m.Models))
	}
	full := m.ByProfile("full")
	if full == nil || full.ID != "m-full" || !full.Gate.Passed {
		t.Errorf("ByProfile(full) = %+v, want the passed full-profile model", full)
	}
	if m.ByProfile("nonexistent") != nil {
		t.Error("ByProfile of a profile not in the manifest should be nil")
	}
}

func TestFetchManifestRejectsAWrongSignature(t *testing.T) {
	srv, _ := manifestServer(t, testManifestJSON)
	defer srv.Close()
	_, otherPub := manifestServer(t, testManifestJSON) // a different key's public half

	_, err := FetchManifest(context.Background(), srv.Client(), srv.URL+"/models.json", srv.URL+"/models.json.sig", otherPub)
	if err == nil {
		t.Fatal("a manifest signed by a different key was accepted")
	}
	if !strings.Contains(err.Error(), "signature") {
		t.Errorf("error does not mention the signature: %v", err)
	}
}

func TestFetchManifestRejectsATamperedBody(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(priv, []byte(testManifestJSON))
	tampered := strings.Replace(testManifestJSON, "m-full", "m-tampered", 1)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models.json":
			w.Write([]byte(tampered)) // signature is over the ORIGINAL body
		case "/models.json.sig":
			w.Write(sig)
		}
	}))
	defer srv.Close()

	_, err = FetchManifest(context.Background(), srv.Client(), srv.URL+"/models.json", srv.URL+"/models.json.sig",
		base64.StdEncoding.EncodeToString(pub))
	if err == nil {
		t.Fatal("a manifest body that does not match its own signature was accepted")
	}
}

func TestFetchManifestRefusesNonHTTPSURLs(t *testing.T) {
	_, err := FetchManifest(context.Background(), nil, "http://example.com/models.json", "https://example.com/models.json.sig", "")
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Errorf("a non-https manifest url was not refused with an https-mentioning error: %v", err)
	}
}
