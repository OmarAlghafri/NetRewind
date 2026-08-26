package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/subtle"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Applied describes what an update changed, for the record.
type Applied struct {
	From      string
	To        string
	Binaries  []string
	NewRules  []string
	Signed    bool
	SourceURL string
}

// Download fetches a release's tarball and checks it against the release's own
// checksums, and against a signature if one is configured.
//
// Returns the verified tarball in memory. It is about 11 MB, and holding it
// there rather than in a temporary file means a half-written download can never
// be mistaken for a finished one.
func (c *Client) Download(ctx context.Context, rel *Release, tarball, publicKey string) ([]byte, bool, error) {
	sums, ok := rel.Find(ChecksumAssetName)
	if !ok {
		return nil, false, fmt.Errorf(
			"update: release %s publishes no %s, so nothing can be verified", rel.Tag, ChecksumAssetName)
	}

	var sumsBuf bytes.Buffer
	if _, err := c.fetch(ctx, sums, &sumsBuf); err != nil {
		return nil, false, err
	}

	signed := false
	if publicKey != "" {
		sig, ok := rel.Find(SignatureAssetName)
		if !ok {
			// Configuring a key and accepting an unsigned release would make
			// the setting decorative.
			return nil, false, fmt.Errorf(
				"update: a public key is configured but release %s has no %s; refusing it",
				rel.Tag, SignatureAssetName)
		}
		var sigBuf bytes.Buffer
		if _, err := c.fetch(ctx, sig, &sigBuf); err != nil {
			return nil, false, err
		}
		if err := VerifySignature(publicKey, sumsBuf.Bytes(), sigBuf.Bytes()); err != nil {
			return nil, false, err
		}
		signed = true
	}

	checksums, err := ParseChecksums(bytes.NewReader(sumsBuf.Bytes()))
	if err != nil {
		return nil, false, err
	}
	want, ok := checksums[tarball]
	if !ok {
		return nil, false, fmt.Errorf(
			"update: %s is not listed in %s, so it cannot be verified", tarball, ChecksumAssetName)
	}

	asset, ok := rel.Find(tarball)
	if !ok {
		return nil, false, fmt.Errorf("update: release %s has no %s for this machine", rel.Tag, tarball)
	}

	var buf bytes.Buffer
	got, err := c.fetch(ctx, asset, &buf)
	if err != nil {
		return nil, false, err
	}
	// Constant time out of habit rather than necessity: nothing here is a
	// secret, but a checksum comparison is exactly the shape of code that gets
	// copied somewhere it matters.
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return nil, false, fmt.Errorf(
			"update: %s does not match its published checksum and was discarded", tarball)
	}
	return buf.Bytes(), signed, nil
}

// Install unpacks a verified tarball over the running installation.
//
// The order is deliberate. Everything is written beside its target first, the
// new binary is run to see whether it works at all, and only then is anything
// renamed into place. A rename on Linux is atomic and can replace a running
// executable - the running process keeps its own inode - so there is no moment
// where the recorder's binary is half written.
//
// The previous binary is kept as .old. An appliance in a cupboard that has
// updated itself into something that will not start is a recorder that has
// silently stopped recording, which is the failure this whole project exists to
// make impossible.
func Install(payload []byte, binDir, rulesDir, fromVersion string) (*Applied, error) {
	wanted := map[string]bool{"netrewindd": true, "netrewind": true}

	binaries := map[string][]byte{}
	rules := map[string][]byte{}

	gz, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("update: the download is not a gzip archive: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("update: read the archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		// The archive is ours, but it arrived over a network, and a tar entry
		// named ../../etc/passwd is the oldest trick there is.
		name := filepath.Base(filepath.Clean("/" + hdr.Name))
		if name == "." || name == "/" || strings.Contains(hdr.Name, "..") {
			continue
		}

		switch {
		case wanted[name]:
			data, err := io.ReadAll(io.LimitReader(tr, maxAssetBytes))
			if err != nil {
				return nil, fmt.Errorf("update: read %s: %w", name, err)
			}
			binaries[name] = data
		case strings.HasSuffix(name, ".yaml") && strings.Contains(hdr.Name, "rules/"):
			data, err := io.ReadAll(io.LimitReader(tr, 1<<20))
			if err != nil {
				return nil, fmt.Errorf("update: read %s: %w", name, err)
			}
			rules[name] = data
		}
	}

	if len(binaries) != len(wanted) {
		return nil, fmt.Errorf("update: the archive is missing one of the binaries; refusing it")
	}

	applied := &Applied{From: fromVersion}

	// Stage, then test, then commit.
	staged := map[string]string{}
	for name, data := range binaries {
		tmp := filepath.Join(binDir, "."+name+".new")
		if err := os.WriteFile(tmp, data, 0o755); err != nil {
			cleanup(staged)
			return nil, fmt.Errorf("update: stage %s: %w", name, err)
		}
		staged[name] = tmp
	}

	newVersion, err := versionOf(staged["netrewindd"])
	if err != nil {
		cleanup(staged)
		return nil, fmt.Errorf("update: the downloaded recorder does not run: %w", err)
	}
	applied.To = newVersion

	for name, tmp := range staged {
		final := filepath.Join(binDir, name)
		// Keep what is being replaced. If the new one turns out to be broken in
		// a way that running --version did not reveal, this is what somebody at
		// the console has to work with.
		if _, err := os.Stat(final); err == nil {
			_ = os.Rename(final, final+".old")
		}
		if err := os.Rename(tmp, final); err != nil {
			cleanup(staged)
			return nil, fmt.Errorf("update: install %s: %w", name, err)
		}
		applied.Binaries = append(applied.Binaries, name)
	}

	// Rules that are new are added; rules that exist are left exactly as they
	// are. The library is meant to be edited, and an update that reverted
	// somebody's rule would teach them not to write any.
	if rulesDir != "" {
		for name, data := range rules {
			target := filepath.Join(rulesDir, name)
			if _, err := os.Stat(target); err == nil {
				continue
			}
			if err := os.WriteFile(target, data, 0o640); err == nil {
				applied.NewRules = append(applied.NewRules, name)
			}
		}
	}

	sortStrings(applied.Binaries)
	sortStrings(applied.NewRules)
	return applied, nil
}

func cleanup(staged map[string]string) {
	for _, p := range staged {
		_ = os.Remove(p)
	}
}

// versionOf runs a binary and asks what it is.
//
// This is the check that a download is not merely intact but usable: the right
// architecture, not truncated, and able to reach the point of printing its own
// version. It costs a fork and it is the difference between an update and a
// brick.
func versionOf(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "not defined: -version") {
			// Releases before 0.9.0 have no --version flag, so they cannot pass
			// this check and are not installable by it. That costs nothing in
			// practice - the updater first shipped in 0.9.0, and every release
			// after it answers the flag - and it is worth far less than
			// loosening the one check standing between an update and a
			// recorder that has silently stopped.
			return "", fmt.Errorf(
				"it is older than 0.9.0 and cannot report its version, so it cannot be verified")
		}
		return "", fmt.Errorf("running it failed: %w (%s)", err, firstLine(out))
	}
	for _, field := range strings.Fields(string(out)) {
		if v, err := ParseVersion(field); err == nil {
			return v.String(), nil
		}
	}
	// It ran but does not say what it is. That is not a build this should be
	// installing: either it is far older than the flag, or it is not the
	// recorder at all.
	return "", fmt.Errorf("it does not report a version: %q", firstLine(out))
}

func firstLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
