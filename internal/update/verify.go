package update

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// maxAssetBytes caps what will be pulled down for an update. The release
// tarball is about 11 MB; anything an order of magnitude past that is either a
// mistake or somebody trying to fill the disk of a recorder that is watching
// them.
const maxAssetBytes = 256 << 20

// Checksums maps an asset name to its expected SHA-256, as published in the
// release's SHA256SUMS.
type Checksums map[string][]byte

// ParseChecksums reads the `sha256sum` output format: hex, two spaces, name.
func ParseChecksums(r io.Reader) (Checksums, error) {
	out := Checksums{}
	sc := bufio.NewScanner(io.LimitReader(r, 1<<20))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("update: cannot read checksum line %q", line)
		}
		sum, err := hex.DecodeString(fields[0])
		if err != nil || len(sum) != sha256.Size {
			return nil, fmt.Errorf("update: %q is not a sha256", fields[0])
		}
		// The name may be prefixed with "*" for binary mode.
		out[strings.TrimPrefix(fields[1], "*")] = sum
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("update: read checksums: %w", err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("update: the checksum file lists nothing")
	}
	return out, nil
}

// fetch downloads an asset into w, returning its SHA-256.
func (c *Client) fetch(ctx context.Context, asset Asset, w io.Writer) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "netrewind")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	// A download is not a status check: give it longer than the API calls, but
	// not forever.
	client := &http.Client{Timeout: 15 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("update: download %s: %w", asset.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update: download %s: HTTP %d", asset.Name, resp.StatusCode)
	}

	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(w, h), io.LimitReader(resp.Body, maxAssetBytes))
	if err != nil {
		return nil, fmt.Errorf("update: download %s: %w", asset.Name, err)
	}
	if n >= maxAssetBytes {
		return nil, fmt.Errorf("update: %s is larger than %d bytes and was refused",
			asset.Name, int64(maxAssetBytes))
	}
	return h.Sum(nil), nil
}

// VerifySignature checks an ed25519 signature over the checksum file.
//
// Without this, checksums prove only that the bytes arrived as GitHub served
// them. That rules out corruption and a broken mirror; it does not rule out
// anybody who can publish a release. A signature made with a key that never
// touches CI is what turns "this is the file GitHub has" into "this is the file
// its author built", and it is the difference between an update channel and a
// way in.
//
// It is optional because a key nobody has set up cannot be required. When
// update.public_key is configured, a release without a valid signature is
// refused rather than installed - which is the only way an optional check is
// worth having.
func VerifySignature(pubKeyBase64 string, checksums, signature []byte) error {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(pubKeyBase64))
	if err != nil {
		return fmt.Errorf("update: public_key is not base64: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return fmt.Errorf("update: public_key is %d bytes, want %d for ed25519",
			len(raw), ed25519.PublicKeySize)
	}
	sig := signature
	// Accept the signature armoured, since that is how it travels in a text
	// file next to the checksums.
	if decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(signature))); err == nil &&
		len(decoded) == ed25519.SignatureSize {
		sig = decoded
	}
	if len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("update: signature is %d bytes, want %d", len(sig), ed25519.SignatureSize)
	}
	if !ed25519.Verify(ed25519.PublicKey(raw), checksums, sig) {
		return fmt.Errorf("update: the checksum file is not signed by the configured key")
	}
	return nil
}

// SignatureAssetName is the file expected beside SHA256SUMS when signing is in
// use.
const SignatureAssetName = "SHA256SUMS.sig"

// ChecksumAssetName is the file every release publishes.
const ChecksumAssetName = "SHA256SUMS"
