package update

import (
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The release is signed with openssl and verified with crypto/ed25519. Those
// are different implementations of the same primitive, and the ways they can
// disagree are all invisible until somebody tries to install an update:
// openssl's ed25519 needs -rawin or it hashes first and produces a signature
// nothing will accept, and its public key comes out as DER that has to be
// stripped to the 32 raw bytes.
//
// This pins the exact commands docs/releasing.md and `make release` use, so a
// change to either is caught here rather than by a recorder in the field
// refusing every release.
func TestOpenSSLSignaturesVerifyHere(t *testing.T) {
	openssl, err := exec.LookPath("openssl")
	if err != nil {
		t.Skip("openssl is not installed")
	}
	dir := t.TempDir()
	key := filepath.Join(dir, "signing.key")
	sums := filepath.Join(dir, "SHA256SUMS")
	sig := filepath.Join(dir, "SHA256SUMS.sig")

	content := "7483e8b60ac91fa2d3a078eb3da7ea17b965f699b7131ed54258c2650f797077  netrewind-0.9.0-linux-amd64.tar.gz\n"
	if err := os.WriteFile(sums, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) []byte {
		t.Helper()
		out, err := exec.Command(openssl, args...).Output()
		if err != nil {
			t.Fatalf("openssl %s: %v", strings.Join(args, " "), err)
		}
		return out
	}

	// Exactly what `make signing-key` does.
	run("genpkey", "-algorithm", "ed25519", "-out", key)

	// Exactly what `make signing-pubkey` does: the raw key is the last 32
	// bytes of the DER encoding.
	der := run("pkey", "-in", key, "-pubout", "-outform", "DER")
	if len(der) < 32 {
		t.Fatalf("the DER public key is %d bytes", len(der))
	}
	pub := base64.StdEncoding.EncodeToString(der[len(der)-32:])

	// Exactly what `make release` does. -rawin matters: without it openssl
	// pre-hashes, and the signature verifies against nothing.
	run("pkeyutl", "-sign", "-inkey", key, "-rawin", "-in", sums, "-out", sig)

	signature, err := os.ReadFile(sig)
	if err != nil {
		t.Fatal(err)
	}
	if len(signature) != 64 {
		t.Fatalf("signature is %d bytes, want 64", len(signature))
	}

	if err := VerifySignature(pub, []byte(content), signature); err != nil {
		t.Fatalf("a signature made the way the release process makes it was rejected: %v", err)
	}

	// And it must not verify against different content.
	if err := VerifySignature(pub, []byte("tampered\n"), signature); err == nil {
		t.Error("the signature verified against content it does not cover")
	}
}
