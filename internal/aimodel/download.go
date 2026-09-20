package aimodel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// idleReadTimeout is how long a download may go without receiving any
// bytes before it is treated as stalled. It bounds a hung connection, not
// the whole transfer - a 2.5 GB file over a slow link can legitimately take
// a long time as long as bytes keep arriving.
const idleReadTimeout = 60 * time.Second

// Progress reports download state for a UI or CLI progress line.
type Progress struct {
	Downloaded int64
	Total      int64
}

// ProgressFunc is called periodically during Download; may be nil.
type ProgressFunc func(Progress)

// Meta is written as <file_name>.meta.json next to a verified download -
// what the runtime re-checks on every launch (threat-model.md: the model
// file is hashed again at launch, not trusted just because it once
// verified) and what Diagnostics reads to show what is installed.
type Meta struct {
	Profile      string    `json:"profile"`
	ID           string    `json:"id"`
	Revision     string    `json:"revision"`
	SHA256       string    `json:"sha256"`
	SizeBytes    int64     `json:"size_bytes"`
	DownloadedAt time.Time `json:"downloaded_at"`
}

// Download fetches spec.URL into dir/spec.FileName, verifying the result
// against spec.SHA256/spec.SizeBytes before it is ever offered to a
// runtime. It resumes an interrupted download from dir/<file>.partial when
// one exists, and quarantines (never deletes) a download whose bytes do
// not match what the manifest promised.
//
// ctx bounds the whole call; ProgressFunc, if non-nil, is called after
// every read.
func Download(ctx context.Context, spec Model, dir string, progress ProgressFunc) error {
	return downloadWithTransport(ctx, spec, dir, progress, http.DefaultTransport)
}

// downloadWithTransport is Download with the round tripper made explicit,
// so a test can point it at an httptest.NewTLSServer's own client transport
// (which trusts that one server's self-signed certificate) without
// changing what a real download does: the https-only scheme check and the
// https-only redirect check apply exactly the same way either way.
func downloadWithTransport(ctx context.Context, spec Model, dir string, progress ProgressFunc, transport http.RoundTripper) error {
	if !strings.HasPrefix(spec.URL, "https://") {
		return fmt.Errorf("aimodel: %s is not an https url; refused", spec.URL)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("aimodel: create %s: %w", dir, err)
	}

	partialPath := filepath.Join(dir, spec.FileName+".partial")
	finalPath := filepath.Join(dir, spec.FileName)

	hasher := sha256.New()
	var resumeFrom int64
	if fi, err := os.Stat(partialPath); err == nil && fi.Size() > 0 {
		// A partial file from a previous run is only a valid prefix to
		// resume from if the bytes it actually holds still hash correctly
		// as a prefix - re-hashing it now means an interrupted, truncated,
		// or tampered partial is caught before a single new byte is
		// appended to it, rather than trusted just because a file with the
		// expected name exists.
		n, err := hashExistingPrefix(partialPath, hasher)
		if err != nil {
			return fmt.Errorf("aimodel: re-hash existing partial download: %w", err)
		}
		resumeFrom = n
	}

	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, _ []*http.Request) error {
			if req.URL.Scheme != "https" {
				return fmt.Errorf("aimodel: refusing a redirect to a non-https url: %s", req.URL)
			}
			return nil
		},
	}

	idleCtx, cancelIdle := context.WithCancel(ctx)
	defer cancelIdle()
	timer := time.AfterFunc(idleReadTimeout, cancelIdle)
	defer timer.Stop()

	req, err := http.NewRequestWithContext(idleCtx, http.MethodGet, spec.URL, nil)
	if err != nil {
		return err
	}
	if resumeFrom > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", resumeFrom))
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("aimodel: download %s: %w", spec.FileName, err)
	}
	defer resp.Body.Close()

	var f *os.File
	switch resp.StatusCode {
	case http.StatusPartialContent:
		f, err = os.OpenFile(partialPath, os.O_APPEND|os.O_WRONLY, 0o644)
	case http.StatusOK:
		// The server ignored the Range request and is sending the whole
		// file from byte 0 - the only safe interpretation is a fresh
		// start, discarding whatever hashing progress was made above.
		resumeFrom = 0
		hasher = sha256.New()
		f, err = os.OpenFile(partialPath, os.O_TRUNC|os.O_CREATE|os.O_WRONLY, 0o644)
	default:
		return fmt.Errorf("aimodel: download %s: HTTP %d", spec.FileName, resp.StatusCode)
	}
	if err != nil {
		return fmt.Errorf("aimodel: open %s: %w", partialPath, err)
	}
	defer f.Close()

	if resp.ContentLength >= 0 {
		wantRemaining := spec.SizeBytes - resumeFrom
		if resp.ContentLength != wantRemaining {
			return fmt.Errorf("aimodel: server reports %d bytes remaining, manifest expects %d; refused",
				resp.ContentLength, wantRemaining)
		}
	}

	downloaded := resumeFrom
	reader := &idleResettingReader{r: resp.Body, timer: timer, idle: idleReadTimeout}
	buf := make([]byte, 256*1024)
	for {
		n, rerr := reader.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return fmt.Errorf("aimodel: write %s: %w", partialPath, werr)
			}
			hasher.Write(buf[:n])
			downloaded += int64(n)
			if progress != nil {
				progress(Progress{Downloaded: downloaded, Total: spec.SizeBytes})
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return fmt.Errorf("aimodel: download %s: %w", spec.FileName, rerr)
		}
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("aimodel: close %s: %w", partialPath, err)
	}

	if downloaded != spec.SizeBytes {
		return fmt.Errorf("aimodel: downloaded %d bytes, manifest expects %d", downloaded, spec.SizeBytes)
	}
	got := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(got, spec.SHA256) {
		return quarantine(dir, partialPath, spec.FileName,
			fmt.Sprintf("sha256 mismatch: got %s, manifest says %s", got, spec.SHA256))
	}

	if err := os.Rename(partialPath, finalPath); err != nil {
		return fmt.Errorf("aimodel: finalize %s: %w", finalPath, err)
	}
	return writeMeta(dir, spec, got)
}

// hashExistingPrefix feeds path's full current contents into h and returns
// how many bytes it read.
func hashExistingPrefix(path string, h io.Writer) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return io.Copy(h, f)
}

// quarantine moves a download that failed verification out of the way
// instead of deleting it, so a real corruption or tampering incident stays
// inspectable rather than silently vanishing (ADR 0006's threat model).
func quarantine(dir, partialPath, fileName, reason string) error {
	qdir := filepath.Join(dir, "quarantine")
	if err := os.MkdirAll(qdir, 0o755); err != nil {
		return fmt.Errorf("aimodel: create quarantine dir: %w", err)
	}
	dest := filepath.Join(qdir, fmt.Sprintf("%s.%s.bad", fileName, time.Now().UTC().Format("20060102T150405Z")))
	if err := os.Rename(partialPath, dest); err != nil {
		return fmt.Errorf("aimodel: quarantine %s: %w", partialPath, err)
	}
	_ = os.WriteFile(dest+".reason.txt", []byte(reason+"\n"), 0o644)
	return fmt.Errorf("aimodel: %s (moved to %s)", reason, dest)
}

func metaPath(dir, fileName string) string {
	return filepath.Join(dir, fileName+".meta.json")
}

func writeMeta(dir string, spec Model, sha256Hex string) error {
	m := Meta{
		Profile: spec.Profile, ID: spec.ID, Revision: spec.Revision,
		SHA256: sha256Hex, SizeBytes: spec.SizeBytes, DownloadedAt: time.Now().UTC(),
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(metaPath(dir, spec.FileName), b, 0o644)
}

// ReadMeta reads back what Download wrote, or (nil, nil) if the model was
// never downloaded (or was removed) - a missing meta file is not an error
// here, callers ask "is it installed" by checking for a nil result.
func ReadMeta(dir, fileName string) (*Meta, error) {
	b, err := os.ReadFile(metaPath(dir, fileName))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m Meta
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("aimodel: %s is not valid json: %w", metaPath(dir, fileName), err)
	}
	return &m, nil
}

// Remove deletes a downloaded model and its metadata (and any leftover
// partial download), leaving quarantined files untouched - those are kept
// deliberately, for someone to inspect, until removed by hand.
func Remove(dir, fileName string) error {
	for _, p := range []string{filepath.Join(dir, fileName), metaPath(dir, fileName), filepath.Join(dir, fileName+".partial")} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("aimodel: remove %s: %w", p, err)
		}
	}
	return nil
}

// idleResettingReader wraps a response body so the download's context is
// cancelled after idle (no bytes received) for longer than idle, rather
// than after a fixed overall deadline - a slow-but-progressing transfer of
// a multi-gigabyte model must not be punished for its size alone.
type idleResettingReader struct {
	r     io.Reader
	timer *time.Timer
	idle  time.Duration
}

func (r *idleResettingReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 {
		r.timer.Reset(r.idle)
	}
	return n, err
}
