package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

/* ------------------------------------------------------------------ */
/* Versions                                                           */
/* ------------------------------------------------------------------ */

func TestVersionOrdering(t *testing.T) {
	cases := []struct {
		a, b  string
		newer bool
		why   string
	}{
		{"0.8.0", "0.9.0", true, "a minor bump is newer"},
		{"0.8.0", "0.8.1", true, "a patch is newer"},
		{"0.8.0", "1.0.0", true, "a major is newer"},
		{"0.9.0", "0.8.0", false, "going backwards is not an update"},
		{"0.8.0", "0.8.0", false, "the same version is not an update"},
		{"0.9.0-rc1", "0.9.0", true, "the release supersedes its own candidate"},
		{"0.9.0", "0.9.0-rc1", false, "a candidate does not supersede the release"},
		{"0.9.0-rc1", "0.9.0-rc2", true, "rc2 follows rc1"},
		{"v0.8.0", "v0.9.0", true, "a leading v is accepted"},
	}
	for _, tc := range cases {
		a, err := ParseVersion(tc.a)
		if err != nil {
			t.Fatalf("ParseVersion(%q): %v", tc.a, err)
		}
		b, err := ParseVersion(tc.b)
		if err != nil {
			t.Fatalf("ParseVersion(%q): %v", tc.b, err)
		}
		if got := a.Newer(b); got != tc.newer {
			t.Errorf("%s -> %s: newer = %v, want %v — %s", tc.a, tc.b, got, tc.newer, tc.why)
		}
	}
}

// A binary somebody built from a working tree must never be replaced by a
// release: whatever they were testing would vanish with no explanation.
func TestADevelopmentBuildHasNoVersion(t *testing.T) {
	for _, s := range []string{"dev", "", "  ", "not-a-version", "0", "1.2.3.4"} {
		if _, err := ParseVersion(s); err == nil {
			t.Errorf("ParseVersion(%q) succeeded; a dev build would be auto-replaced", s)
		}
	}
}

/* ------------------------------------------------------------------ */
/* Checksums and signatures                                           */
/* ------------------------------------------------------------------ */

func TestParseChecksums(t *testing.T) {
	in := `
# a comment
7483e8b60ac91fa2d3a078eb3da7ea17b965f699b7131ed54258c2650f797077  netrewind-0.8.0-linux-amd64.tar.gz
a597aff6c0b7aa722d302a4819ab58a9eff9dfdb3c50a05a4e354f0fc2d61424 *netrewind-0.8.0-linux-arm64.tar.gz
`
	sums, err := ParseChecksums(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 2 {
		t.Fatalf("parsed %d checksums, want 2", len(sums))
	}
	if _, ok := sums["netrewind-0.8.0-linux-arm64.tar.gz"]; !ok {
		t.Error("the binary-mode asterisk was not stripped from the name")
	}
}

func TestMalformedChecksumsAreRefused(t *testing.T) {
	for _, in := range []string{
		"",
		"not-a-hash  file.tar.gz",
		"deadbeef  file.tar.gz", // too short for sha256
		"7483e8b60ac91fa2d3a078eb3da7ea17b965f699b7131ed54258c2650f797077",
	} {
		if _, err := ParseChecksums(strings.NewReader(in)); err == nil {
			t.Errorf("accepted malformed checksums: %q", in)
		}
	}
}

func TestSignatureVerification(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	pubB64 := base64.StdEncoding.EncodeToString(pub)
	sums := []byte("deadbeef  netrewind.tar.gz\n")
	sig := ed25519.Sign(priv, sums)

	if err := VerifySignature(pubB64, sums, sig); err != nil {
		t.Errorf("a valid signature was rejected: %v", err)
	}
	// Armoured, which is how it travels in a text file.
	armoured := []byte(base64.StdEncoding.EncodeToString(sig))
	if err := VerifySignature(pubB64, sums, armoured); err != nil {
		t.Errorf("a base64 signature was rejected: %v", err)
	}
	// Tampered content.
	if err := VerifySignature(pubB64, []byte("different  netrewind.tar.gz\n"), sig); err == nil {
		t.Error("a signature was accepted over content it does not cover")
	}
	// Someone else's key.
	other, _, _ := ed25519.GenerateKey(nil)
	if err := VerifySignature(base64.StdEncoding.EncodeToString(other), sums, sig); err == nil {
		t.Error("a signature from the wrong key was accepted")
	}
}

/* ------------------------------------------------------------------ */
/* Download                                                           */
/* ------------------------------------------------------------------ */

// fakeRelease serves a release the way GitHub does, so the client is exercised
// against the shape it will actually meet.
type fakeRelease struct {
	srv      *httptest.Server
	tarball  []byte
	sums     string
	sig      []byte
	tag      string
	withSums bool
	withSig  bool
}

func newFakeRelease(t *testing.T, tag string, tarball []byte) *fakeRelease {
	t.Helper()
	f := &fakeRelease{tarball: tarball, tag: tag, withSums: true}
	sum := sha256.Sum256(tarball)
	name := TarballName(strings.TrimPrefix(tag, "v"))
	f.sums = fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), name)

	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		assets := []map[string]any{
			{"name": name, "browser_download_url": f.srv.URL + "/dl/tarball", "size": len(f.tarball)},
		}
		if f.withSums {
			assets = append(assets, map[string]any{
				"name": ChecksumAssetName, "browser_download_url": f.srv.URL + "/dl/sums", "size": len(f.sums)})
		}
		if f.withSig {
			assets = append(assets, map[string]any{
				"name": SignatureAssetName, "browser_download_url": f.srv.URL + "/dl/sig", "size": len(f.sig)})
		}
		json.NewEncoder(w).Encode(map[string]any{
			"tag_name": f.tag, "name": f.tag, "draft": false, "prerelease": false,
			"html_url": "https://example.invalid/r", "assets": assets,
		})
	})
	mux.HandleFunc("/dl/tarball", func(w http.ResponseWriter, _ *http.Request) { w.Write(f.tarball) })
	mux.HandleFunc("/dl/sums", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(f.sums)) })
	mux.HandleFunc("/dl/sig", func(w http.ResponseWriter, _ *http.Request) { w.Write(f.sig) })

	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// client points at the fake, and permits plain HTTP for it. Production refuses
// anything but https; the test server cannot serve that without a CA.
func (f *fakeRelease) client(t *testing.T) (*Client, *Release) {
	t.Helper()
	c := NewClient("owner/repo", "")
	c.apiBase = f.srv.URL
	c.allowInsecure = true
	rel, err := c.Latest(context.Background())
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	return c, rel
}

func TestAVerifiedDownloadIsAccepted(t *testing.T) {
	tarball := buildTarball(t, "0.9.0", nil)
	f := newFakeRelease(t, "v0.9.0", tarball)
	c, rel := f.client(t)

	got, signed, err := c.Download(context.Background(), rel, TarballName("0.9.0"), "")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !bytes.Equal(got, tarball) {
		t.Error("the downloaded bytes differ from what was served")
	}
	if signed {
		t.Error("reported as signed with no key configured")
	}
}

// The point of the whole exercise: a tarball that does not match its published
// checksum must be discarded, not installed.
func TestATamperedDownloadIsRefused(t *testing.T) {
	f := newFakeRelease(t, "v0.9.0", buildTarball(t, "0.9.0", nil))
	// Serve different bytes than the checksum covers.
	f.tarball = buildTarball(t, "0.9.0", []byte("something else entirely"))
	c, rel := f.client(t)

	_, _, err := c.Download(context.Background(), rel, TarballName("0.9.0"), "")
	if err == nil {
		t.Fatal("a tarball that does not match its checksum was accepted")
	}
	if !strings.Contains(err.Error(), "checksum") {
		t.Errorf("the error does not say what was wrong: %v", err)
	}
}

func TestAReleaseWithNoChecksumsIsRefused(t *testing.T) {
	f := newFakeRelease(t, "v0.9.0", buildTarball(t, "0.9.0", nil))
	f.withSums = false
	c, rel := f.client(t)

	if _, _, err := c.Download(context.Background(), rel, TarballName("0.9.0"), ""); err == nil {
		t.Fatal("a release publishing no checksums was accepted")
	}
}

// Configuring a key and then accepting an unsigned release would make the
// setting decorative, which is worse than not having it.
func TestConfiguringAKeyMakesSignaturesMandatory(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(nil)
	f := newFakeRelease(t, "v0.9.0", buildTarball(t, "0.9.0", nil))
	f.withSig = false
	c, rel := f.client(t)

	_, _, err := c.Download(context.Background(), rel,
		TarballName("0.9.0"), base64.StdEncoding.EncodeToString(pub))
	if err == nil {
		t.Fatal("an unsigned release was accepted while a key was configured")
	}
	if !strings.Contains(err.Error(), "refusing") {
		t.Errorf("the error does not say it was refused: %v", err)
	}
}

func TestASignedReleaseIsAcceptedAndReported(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	tarball := buildTarball(t, "0.9.0", nil)
	f := newFakeRelease(t, "v0.9.0", tarball)
	f.withSig = true
	f.sig = ed25519.Sign(priv, []byte(f.sums))
	c, rel := f.client(t)

	_, signed, err := c.Download(context.Background(), rel,
		TarballName("0.9.0"), base64.StdEncoding.EncodeToString(pub))
	if err != nil {
		t.Fatalf("a correctly signed release was refused: %v", err)
	}
	if !signed {
		t.Error("a verified signature was not reported, so the record would not show it")
	}
}

/* ------------------------------------------------------------------ */
/* Install                                                            */
/* ------------------------------------------------------------------ */

func TestInstallReplacesBinariesAndKeepsTheOldOnes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the staged binary is executed, which needs a Linux build")
	}
	binDir := t.TempDir()
	rulesDir := t.TempDir()

	// Something already installed, and a rule the operator has edited.
	if err := os.WriteFile(filepath.Join(binDir, "netrewindd"), []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rulesDir, "mine.yaml"), []byte("edited by hand"), 0o640); err != nil {
		t.Fatal(err)
	}

	payload := buildTarball(t, "0.9.0", nil)
	applied, err := Install(payload, binDir, rulesDir, "0.8.0")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	if applied.To != "0.9.0" {
		t.Errorf("to = %q, want the version the new binary reports", applied.To)
	}
	if _, err := os.Stat(filepath.Join(binDir, "netrewindd.old")); err != nil {
		t.Error("the replaced binary was not kept; there is nothing to fall back to")
	}
	// A rule that is new arrives; one that exists is untouched.
	if _, err := os.Stat(filepath.Join(rulesDir, "shipped.yaml")); err != nil {
		t.Error("a rule that is new in the release was not installed")
	}
	kept, _ := os.ReadFile(filepath.Join(rulesDir, "mine.yaml"))
	if string(kept) != "edited by hand" {
		t.Error("an edited rule was overwritten, which teaches people not to write rules")
	}
}

// A download that unpacks but does not run must not be installed. This is the
// check that stands between an update and a recorder that has silently stopped.
func TestABinaryThatDoesNotRunIsNotInstalled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs an executable payload")
	}
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "netrewindd"), []byte("original"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Not a program at all.
	payload := tarballWith(t, map[string][]byte{
		"netrewind-0.9.0-linux-amd64/netrewindd": []byte("\x7fELF this is not really a binary"),
		"netrewind-0.9.0-linux-amd64/netrewind":  []byte("nor is this"),
	})

	if _, err := Install(payload, binDir, "", "0.8.0"); err == nil {
		t.Fatal("a payload that cannot run was installed")
	}
	current, _ := os.ReadFile(filepath.Join(binDir, "netrewindd"))
	if string(current) != "original" {
		t.Error("the running binary was replaced by one that does not work")
	}
	// And nothing half-written is left lying about.
	if entries, _ := os.ReadDir(binDir); len(entries) != 1 {
		t.Errorf("staging files were left behind: %d entries", len(entries))
	}
}

func TestAnArchiveMissingABinaryIsRefused(t *testing.T) {
	binDir := t.TempDir()
	payload := tarballWith(t, map[string][]byte{
		"netrewind-0.9.0-linux-amd64/netrewindd": []byte("only one"),
	})
	if _, err := Install(payload, binDir, "", "0.8.0"); err == nil {
		t.Fatal("an incomplete archive was installed")
	}
}

// A tar entry named ../../etc/passwd is the oldest trick there is, and this
// archive arrived over a network.
func TestPathTraversalInTheArchiveIsIgnored(t *testing.T) {
	binDir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "escaped")

	payload := tarballWith(t, map[string][]byte{
		"../../../../../../" + outside: []byte("escaped"),
		"netrewind/netrewindd":         []byte("x"),
		"netrewind/netrewind":          []byte("x"),
	})
	// It will fail for another reason (the binaries do not run), which is fine.
	_, _ = Install(payload, binDir, "", "0.8.0")

	if _, err := os.Stat(outside); err == nil {
		t.Fatal("a tar entry wrote outside the target directory")
	}
}

/* ------------------------------------------------------------------ */

// buildTarball produces a release archive whose netrewindd is a real, runnable
// program that reports the given version.
func buildTarball(t *testing.T, version string, marker []byte) []byte {
	t.Helper()
	script := "#!/bin/sh\necho \"netrewindd " + version + " (schema v1)\"\n"
	if marker != nil {
		script += "# " + string(marker) + "\n"
	}
	prefix := "netrewind-" + version + "-linux-" + runtime.GOARCH + "/"
	return tarballWith(t, map[string][]byte{
		prefix + "netrewindd":         []byte(script),
		prefix + "netrewind":          []byte(script),
		prefix + "rules/shipped.yaml": []byte("id: shipped\n"),
		prefix + "docs/schema.md":     []byte("# schema\n"),
	})
}

func tarballWith(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, data := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o755, Size: int64(len(data)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// The whole path, end to end: discover a release, download it, verify the
// checksum, unpack it, run the new binary, replace the old one and keep it.
//
// Everything above tests one step. This is the one that would catch a mistake
// in how they are joined together, which is where the interesting bugs are.
func TestTheWholeUpdatePathFromReleaseToInstalledBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the staged binary is executed")
	}
	tarball := buildTarball(t, "0.9.0", nil)
	f := newFakeRelease(t, "v0.9.0", tarball)
	c, rel := f.client(t)

	binDir := t.TempDir()
	rulesDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "netrewindd"), []byte("the old one"), 0o755); err != nil {
		t.Fatal(err)
	}

	payload, signed, err := c.Download(context.Background(), rel, TarballName("0.9.0"), "")
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	applied, err := Install(payload, binDir, rulesDir, "0.8.0")
	if err != nil {
		t.Fatalf("install: %v", err)
	}

	if applied.From != "0.8.0" || applied.To != "0.9.0" {
		t.Errorf("recorded %s -> %s, want 0.8.0 -> 0.9.0", applied.From, applied.To)
	}
	if signed {
		t.Error("reported as signed with no key configured")
	}
	if len(applied.Binaries) != 2 {
		t.Errorf("installed %v, want both binaries", applied.Binaries)
	}
	if len(applied.NewRules) != 1 {
		t.Errorf("added %v, want the one rule that is new", applied.NewRules)
	}

	// The installed binary is the new one, and the old one is still reachable.
	got, err := versionOf(filepath.Join(binDir, "netrewindd"))
	if err != nil || got != "0.9.0" {
		t.Errorf("the installed binary reports %q (%v), want 0.9.0", got, err)
	}
	old, _ := os.ReadFile(filepath.Join(binDir, "netrewindd.old"))
	if string(old) != "the old one" {
		t.Error("the previous binary was not kept, so there is no way back")
	}
}

// A release older than the flag cannot be verified, and must be refused with a
// message that says why rather than a puzzle about exit status 2.
func TestAReleaseTooOldToReportItsVersionIsRefusedClearly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the staged binary is executed")
	}
	binDir := t.TempDir()
	// A binary that behaves the way pre-0.9.0 netrewindd does.
	old := "#!/bin/sh\necho 'flag provided but not defined: -version' >&2\nexit 2\n"
	payload := tarballWith(t, map[string][]byte{
		"netrewind-0.8.0-linux-amd64/netrewindd": []byte(old),
		"netrewind-0.8.0-linux-amd64/netrewind":  []byte(old),
	})

	_, err := Install(payload, binDir, "", "0.7.0")
	if err == nil {
		t.Fatal("a release that cannot report its version was installed")
	}
	if !strings.Contains(err.Error(), "older than 0.9.0") {
		t.Errorf("the error does not explain why: %v", err)
	}
}
