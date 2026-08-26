// Package update keeps the recorder current from its own releases.
//
// This is a supply chain. The thing being replaced is a recorder whose output
// is meant to be evidence, so an update that could be tampered with is worse
// than no update at all - it is a way to replace the witness. Everything here
// is built around that:
//
//   - HTTPS only, with the platform's certificate verification
//   - every download checked against the SHA256SUMS published with the release
//   - an optional ed25519 signature over SHA256SUMS, which is what makes the
//     checksum mean something more than "not corrupted in transit"
//   - the new binary is run before it is trusted, and the old one is kept
//   - the update is recorded as an event, because a recorder that swaps itself
//     out and leaves a gap with no explanation has damaged its own record
//
// Checking and applying are separate settings. Plenty of operators want to know
// a version exists without a machine on their network deciding to change
// itself, and a network that forbids outbound connections should be able to
// turn the whole thing off.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// DefaultRepo is where releases are published.
const DefaultRepo = "OmarAlghafri/NetRewind"

// checkTimeout bounds one look at the release feed. The recorder has work to
// do; asking about updates is not it.
const checkTimeout = 20 * time.Second

// Release is the part of a GitHub release this needs.
type Release struct {
	Tag        string
	Name       string
	Prerelease bool
	Draft      bool
	Assets     []Asset
	URL        string
}

// Asset is one downloadable file.
type Asset struct {
	Name string
	URL  string
	Size int64
}

// Client reads releases. Zero value is not usable; use NewClient.
type Client struct {
	repo  string
	token string
	http  *http.Client

	// apiBase overrides api.github.com. Unexported: this exists so the client
	// can be tested against a server that behaves like GitHub, not so an
	// operator can point the update channel somewhere arbitrary.
	apiBase string
	// allowInsecure permits plain HTTP asset URLs, which only a test server
	// serves. Production refuses them: an attacker who can rewrite plaintext
	// can rewrite the checksum file alongside it.
	allowInsecure bool
}

// NewClient returns a client for a repository, e.g. "owner/name".
//
// The token is optional and only needed while the repository is private. It is
// never sent anywhere but api.github.com.
func NewClient(repo, token string) *Client {
	if repo == "" {
		repo = DefaultRepo
	}
	return &Client{
		repo:  repo,
		token: token,
		http:  &http.Client{Timeout: checkTimeout},
	}
}

// Latest returns the newest published release, ignoring drafts and
// pre-releases: an appliance should not move itself onto a version whose author
// has said it is not ready.
func (c *Client) Latest(ctx context.Context) (*Release, error) {
	base := c.apiBase
	if base == "" {
		base = "https://api.github.com/repos/" + c.repo
	}
	api := base + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "netrewind")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("update: ask %s for the latest release: %w", c.repo, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		// A private repository with no token looks exactly like one that does
		// not exist, and the difference matters to whoever has to fix it.
		return nil, fmt.Errorf(
			"update: %s has no releases, or is private and no token was configured", c.repo)
	case http.StatusForbidden, http.StatusTooManyRequests:
		return nil, fmt.Errorf("update: rate limited by github; the next check will try again")
	default:
		return nil, fmt.Errorf("update: github answered HTTP %d", resp.StatusCode)
	}

	var raw struct {
		TagName    string `json:"tag_name"`
		Name       string `json:"name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		HTMLURL    string `json:"html_url"`
		Assets     []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
			Size int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("update: parse the release feed: %w", err)
	}

	rel := &Release{
		Tag: raw.TagName, Name: raw.Name,
		Draft: raw.Draft, Prerelease: raw.Prerelease, URL: raw.HTMLURL,
	}
	for _, a := range raw.Assets {
		// Refuse anything that is not fetched over TLS. The checksum would
		// catch tampering, but an attacker who can rewrite plaintext can
		// rewrite the checksum file too, and there is no reason to allow it.
		u, err := url.Parse(a.URL)
		if err != nil {
			continue
		}
		if u.Scheme != "https" && !(c.allowInsecure && u.Scheme == "http") {
			continue
		}
		rel.Assets = append(rel.Assets, Asset{Name: a.Name, URL: a.URL, Size: a.Size})
	}
	return rel, nil
}

// Find returns the named asset.
func (r *Release) Find(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// TarballName is the release asset holding the binaries for this machine.
func TarballName(version string) string {
	return fmt.Sprintf("netrewind-%s-linux-%s.tar.gz", version, runtime.GOARCH)
}

/* ------------------------------------------------------------------ */
/* Versions                                                           */
/* ------------------------------------------------------------------ */

// Version is a released version, parsed far enough to be ordered.
type Version struct {
	Major, Minor, Patch int
	// Pre is the pre-release suffix, e.g. "rc1" in 0.9.0-rc1. A version with
	// one sorts before the same version without.
	Pre string
}

// ParseVersion reads "v0.8.0", "0.8.0" or "0.9.0-rc1".
//
// A development build ("dev", or anything with no numbers in it) does not
// parse, and that is deliberate: a binary somebody built from a working tree
// must never be quietly replaced by a release, because whatever they were
// testing would vanish without explanation.
func ParseVersion(s string) (Version, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if s == "" || s == "dev" {
		return Version{}, fmt.Errorf("update: %q is not a released version", s)
	}

	var v Version
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		v.Pre = s[i+1:]
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return Version{}, fmt.Errorf("update: %q is not a version number", s)
	}
	nums := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return Version{}, fmt.Errorf("update: %q is not a version number", s)
		}
		nums[i] = n
	}
	v.Major, v.Minor, v.Patch = nums[0], nums[1], nums[2]
	return v, nil
}

func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Pre != "" {
		s += "-" + v.Pre
	}
	return s
}

// Newer reports whether other is a later version than v.
func (v Version) Newer(other Version) bool {
	switch {
	case other.Major != v.Major:
		return other.Major > v.Major
	case other.Minor != v.Minor:
		return other.Minor > v.Minor
	case other.Patch != v.Patch:
		return other.Patch > v.Patch
	}
	// Same numbers: a release beats a pre-release of itself, and one
	// pre-release beats another only in plain string order, which is enough
	// for rc1 < rc2 and not pretended to be more.
	switch {
	case v.Pre == "" && other.Pre == "":
		return false
	case v.Pre != "" && other.Pre == "":
		return true
	case v.Pre == "" && other.Pre != "":
		return false
	default:
		return other.Pre > v.Pre
	}
}
