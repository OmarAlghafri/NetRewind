package aimodel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func testModel(url string, content []byte) Model {
	sum := sha256.Sum256(content)
	return Model{
		Profile: "small", ID: "test-model", Revision: "rev1", FileName: "model.gguf",
		URL: url, SizeBytes: int64(len(content)), SHA256: hex.EncodeToString(sum[:]),
	}
}

// download runs downloadWithTransport against a real TLS test server -
// httptest.NewTLSServer's own URL already starts with https://, and its
// Client()'s Transport trusts that one server's self-signed certificate,
// so Download's https-only checks are exercised for real rather than
// bypassed.
func download(t *testing.T, srv *httptest.Server, spec Model, dir string, progress ProgressFunc) error {
	t.Helper()
	return downloadWithTransport(context.Background(), spec, dir, progress, srv.Client().Transport)
}

func TestDownloadVerifiesAndWritesMeta(t *testing.T) {
	content := []byte(strings.Repeat("x", 10000))
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(content)
	}))
	defer srv.Close()
	spec := testModel(srv.URL, content)

	dir := t.TempDir()
	if err := download(t, srv, spec, dir, nil); err != nil {
		t.Fatalf("Download: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, spec.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Error("downloaded file content does not match")
	}
	if _, err := os.Stat(filepath.Join(dir, spec.FileName+".partial")); !os.IsNotExist(err) {
		t.Error("the .partial file should be gone after a successful download")
	}
	meta, err := ReadMeta(dir, spec.FileName)
	if err != nil {
		t.Fatal(err)
	}
	if meta == nil || meta.SHA256 != spec.SHA256 || meta.SizeBytes != spec.SizeBytes {
		t.Errorf("meta = %+v, want a match for spec %+v", meta, spec)
	}
}

// TestDownloadResumesFromAPartialFile proves a download picks up where a
// previous, interrupted one left off rather than starting over, by seeding
// a genuine partial file and a server that honors Range and only serves
// the remainder.
func TestDownloadResumesFromAPartialFile(t *testing.T) {
	content := []byte(strings.Repeat("y", 20000))
	var sawRange string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawRange = r.Header.Get("Range")
		if sawRange == "" {
			w.Write(content)
			return
		}
		start, err := rangeStart(sawRange)
		if err != nil {
			t.Errorf("bad Range header %q: %v", sawRange, err)
			return
		}
		w.Header().Set("Content-Range", "bytes "+strconv.Itoa(start)+"-"+strconv.Itoa(len(content)-1)+"/"+strconv.Itoa(len(content)))
		w.WriteHeader(http.StatusPartialContent)
		w.Write(content[start:])
	}))
	defer srv.Close()
	spec := testModel(srv.URL, content)

	dir := t.TempDir()
	half := len(content) / 2
	if err := os.WriteFile(filepath.Join(dir, spec.FileName+".partial"), content[:half], 0o644); err != nil {
		t.Fatal(err)
	}

	if err := download(t, srv, spec, dir, nil); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if sawRange == "" {
		t.Error("the server never received a Range header; the download did not attempt to resume")
	}

	got, err := os.ReadFile(filepath.Join(dir, spec.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Error("the resumed download's content does not match the full expected content")
	}
}

// TestDownloadRestartsWhenTheServerIgnoresRange proves a 200 response to a
// Range request (server does not support resume) is treated as a full
// restart, not appended to the stale partial - appending would silently
// corrupt the file with the whole body glued onto an unrelated prefix.
func TestDownloadRestartsWhenTheServerIgnoresRange(t *testing.T) {
	content := []byte(strings.Repeat("z", 5000))
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Ignores any Range header and always serves the full body with
		// 200, exactly like a server with no resume support.
		w.Write(content)
	}))
	defer srv.Close()
	spec := testModel(srv.URL, content)

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, spec.FileName+".partial"), []byte("stale-unrelated-prefix"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := download(t, srv, spec, dir, nil); err != nil {
		t.Fatalf("Download: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, spec.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Error("got content different from the full fresh body; the stale partial must not have been appended to")
	}
}

// TestDownloadQuarantinesOnHashMismatch proves a download whose bytes do
// not match the manifest's sha256 is moved aside rather than accepted or
// silently deleted - the whole point of quarantine is that the failure
// stays inspectable.
func TestDownloadQuarantinesOnHashMismatch(t *testing.T) {
	served := []byte("this is not what the manifest promised, but same length!!")
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(served)
	}))
	defer srv.Close()
	// spec's hash describes different content than what the server actually
	// serves (same length, so the Content-Length check does not catch it
	// first) - simulating a corrupted or tampered transfer.
	spec := testModel(srv.URL, []byte("the real expected content, same length as served!!"))
	spec.SizeBytes = int64(len(served))

	dir := t.TempDir()
	err := download(t, srv, spec, dir, nil)
	if err == nil {
		t.Fatal("a hash mismatch was accepted as a successful download")
	}
	if !strings.Contains(err.Error(), "mismatch") {
		t.Errorf("error does not mention a mismatch: %v", err)
	}
	entries, rerr := os.ReadDir(filepath.Join(dir, "quarantine"))
	if rerr != nil || len(entries) == 0 {
		t.Fatalf("no quarantined file was written: %v", rerr)
	}
	if _, err := os.Stat(filepath.Join(dir, spec.FileName)); !os.IsNotExist(err) {
		t.Error("a hash-mismatched download must not be placed at its final path")
	}
}

func TestDownloadRefusesANonHTTPSURL(t *testing.T) {
	spec := testModel("http://example.com/model.gguf", []byte("x"))
	err := Download(context.Background(), spec, t.TempDir(), nil)
	if err == nil {
		t.Fatal("a plain http:// url was accepted")
	}
	if !strings.Contains(err.Error(), "https") {
		t.Errorf("error does not mention https: %v", err)
	}
}

func TestDownloadRefusesAContentLengthMismatch(t *testing.T) {
	content := []byte(strings.Repeat("a", 100))
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(content)
	}))
	defer srv.Close()
	spec := testModel(srv.URL, content)
	spec.SizeBytes = int64(len(content)) + 500 // manifest disagrees with the server

	err := download(t, srv, spec, t.TempDir(), nil)
	if err == nil {
		t.Fatal("a size mismatch between the manifest and the server's Content-Length was accepted")
	}
	if !strings.Contains(err.Error(), "bytes remaining") {
		t.Errorf("error does not explain the size disagreement: %v", err)
	}
}

func TestRemoveDeletesTheModelAndItsMeta(t *testing.T) {
	content := []byte("small content")
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(content) }))
	defer srv.Close()
	spec := testModel(srv.URL, content)
	dir := t.TempDir()
	if err := download(t, srv, spec, dir, nil); err != nil {
		t.Fatal(err)
	}
	if err := Remove(dir, spec.FileName); err != nil {
		t.Fatal(err)
	}
	if meta, err := ReadMeta(dir, spec.FileName); err != nil || meta != nil {
		t.Errorf("meta still present after Remove: %v %v", meta, err)
	}
	if _, err := os.Stat(filepath.Join(dir, spec.FileName)); !os.IsNotExist(err) {
		t.Error("model file still present after Remove")
	}
}

func TestReadMetaOfAnUndownloadedModelIsNilNotError(t *testing.T) {
	meta, err := ReadMeta(t.TempDir(), "never-downloaded.gguf")
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if meta != nil {
		t.Errorf("meta = %+v, want nil for a model never downloaded", meta)
	}
}

// rangeStart parses a "bytes=1234-" Range header's start offset.
func rangeStart(header string) (int, error) {
	const prefix = "bytes="
	rest := strings.TrimSuffix(strings.TrimPrefix(header, prefix), "-")
	return strconv.Atoi(rest)
}
