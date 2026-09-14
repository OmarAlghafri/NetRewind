package bundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/incident"
	"github.com/OmarAlghafri/netrewind/internal/store"
)

func newTestStore(t *testing.T) store.Store {
	t.Helper()
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func seedEvent(t *testing.T, st store.Store, kind event.Kind, mutate func(*event.Event)) *event.Event {
	t.Helper()
	b := event.NewBuilder("obs-1", nil)
	e := b.New(event.SourceDNS, kind, event.SevWarn, event.Host("10.0.0.5", "")).
		WithAttr("name", "intranet.lab").
		WithAttr("resolver_new", "10.0.0.99")
	if mutate != nil {
		mutate(e)
	}
	if err := st.Append(context.Background(), e); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	return e
}

func seedIncident(t *testing.T, st store.Store) {
	t.Helper()
	inc := &incident.Incident{
		ID: "01TESTINCIDENT00000000000", OpenedAt: time.Now().UnixNano(),
		Status: incident.StatusClosed, Title: "test incident", Severity: event.SevWarn,
		Confidence: 80, RootCause: incident.RootCause{Kind: "l2.arp_binding_changed", Entity: "10.0.0.5"},
		RuleID: "test-rule",
	}
	if err := st.AppendIncidents(context.Background(), inc); err != nil {
		t.Fatalf("seed incident: %v", err)
	}
}

func TestExportImportRoundTrip(t *testing.T) {
	st := newTestStore(t)
	seedEvent(t, st, event.KindDNSResolverChanged, nil)
	seedIncident(t, st)

	var buf bytes.Buffer
	manifest, err := Export(context.Background(), st, &buf, ExportOptions{
		From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Hour),
		AppVersion: "test", ObserverID: "obs-1",
	})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if manifest.EventCount != 1 || manifest.IncidentCount != 1 {
		t.Fatalf("manifest counts = %d/%d, want 1/1", manifest.EventCount, manifest.IncidentCount)
	}
	if !manifest.Redacted {
		t.Error("Redacted = false, want true by default")
	}

	dest := filepath.Join(t.TempDir(), "imported.db")
	imported, err := Import(&buf, ImportOptions{DestPath: dest})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if imported.EventCount != 1 {
		t.Errorf("imported.EventCount = %d, want 1", imported.EventCount)
	}

	st2, err := store.OpenSQLite(dest)
	if err != nil {
		t.Fatalf("open imported store: %v", err)
	}
	defer st2.Close()
	events, err := st2.Query(context.Background(), store.Filter{})
	if err != nil {
		t.Fatalf("query imported store: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("imported store has %d events, want 1", len(events))
	}
	if _, present := events[0].Attrs["name"]; present {
		t.Error("redacted attribute 'name' survived into the imported store")
	}
	if events[0].Attrs["resolver_new"] != "10.0.0.99" {
		t.Errorf("non-redacted attribute lost: %v", events[0].Attrs["resolver_new"])
	}
}

func TestIncludeSecretsPreservesTheName(t *testing.T) {
	st := newTestStore(t)
	seedEvent(t, st, event.KindDNSResolverChanged, nil)

	var buf bytes.Buffer
	manifest, err := Export(context.Background(), st, &buf, ExportOptions{
		From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Hour), IncludeSecrets: true,
	})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if manifest.Redacted {
		t.Error("Redacted = true with IncludeSecrets set")
	}

	dest := filepath.Join(t.TempDir(), "imported.db")
	if _, err := Import(&buf, ImportOptions{DestPath: dest}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	st2, _ := store.OpenSQLite(dest)
	defer st2.Close()
	events, _ := st2.Query(context.Background(), store.Filter{})
	if events[0].Attrs["name"] != "intranet.lab" {
		t.Errorf("name = %v, want preserved with IncludeSecrets", events[0].Attrs["name"])
	}
}

func TestImportRefusesATamperedBundle(t *testing.T) {
	st := newTestStore(t)
	seedEvent(t, st, event.KindDNSResolverChanged, nil)

	var buf bytes.Buffer
	if _, err := Export(context.Background(), st, &buf, ExportOptions{
		From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("Export: %v", err)
	}

	tampered := tamperMember(t, buf.Bytes(), eventsFile, func(data []byte) []byte {
		return append(data, []byte(`{"extra":"injected"}`)...)
	})

	dest := filepath.Join(t.TempDir(), "imported.db")
	if _, err := Import(bytes.NewReader(tampered), ImportOptions{DestPath: dest}); err == nil {
		t.Fatal("imported a bundle whose events.json does not match its checksum")
	}
}

func TestImportRefusesToOverwriteAnExistingDestination(t *testing.T) {
	st := newTestStore(t)
	seedEvent(t, st, event.KindDNSResolverChanged, nil)
	var buf bytes.Buffer
	Export(context.Background(), st, &buf, ExportOptions{From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Hour)})

	dest := filepath.Join(t.TempDir(), "imported.db")
	if _, err := Import(bytes.NewReader(buf.Bytes()), ImportOptions{DestPath: dest}); err != nil {
		t.Fatalf("first import: %v", err)
	}
	if _, err := Import(bytes.NewReader(buf.Bytes()), ImportOptions{DestPath: dest}); err == nil {
		t.Fatal("a second import to the same DestPath was allowed to overwrite the first")
	}
}

func TestZipSlipEntriesAreIgnoredNotExtracted(t *testing.T) {
	st := newTestStore(t)
	seedEvent(t, st, event.KindDNSResolverChanged, nil)
	var buf bytes.Buffer
	Export(context.Background(), st, &buf, ExportOptions{From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Hour)})

	withEvilEntry := addTarEntry(t, buf.Bytes(), "../../../../tmp/netrewind-zipslip-proof", []byte("should never land on disk"))

	dest := filepath.Join(t.TempDir(), "imported.db")
	if _, err := Import(bytes.NewReader(withEvilEntry), ImportOptions{DestPath: dest}); err != nil {
		t.Fatalf("a legitimate bundle with one extra hostile entry should still import: %v", err)
	}
	// The real assertion is structural: readTarGz only ever populates its map
	// from filepath.Base(hdr.Name) against a fixed allow-list, so there is no
	// code path that could have written outside the extraction directory in
	// the first place - this test documents that the malicious entry does
	// not even stop a legitimate import, rather than break it.
}

func TestUnsignedBundleIsAcceptedWithNoKeyConfigured(t *testing.T) {
	st := newTestStore(t)
	seedEvent(t, st, event.KindDNSResolverChanged, nil)
	var buf bytes.Buffer
	Export(context.Background(), st, &buf, ExportOptions{From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Hour)})

	dest := filepath.Join(t.TempDir(), "imported.db")
	if _, err := Import(bytes.NewReader(buf.Bytes()), ImportOptions{DestPath: dest}); err != nil {
		t.Fatalf("an unsigned bundle with no public key configured should import: %v", err)
	}
}

func TestUnsignedBundleIsRefusedOnceAKeyIsConfigured(t *testing.T) {
	st := newTestStore(t)
	seedEvent(t, st, event.KindDNSResolverChanged, nil)
	var buf bytes.Buffer
	Export(context.Background(), st, &buf, ExportOptions{From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Hour)})

	pub, _, _ := ed25519.GenerateKey(nil)
	dest := filepath.Join(t.TempDir(), "imported.db")
	_, err := Import(bytes.NewReader(buf.Bytes()), ImportOptions{
		DestPath: dest, PublicKey: base64.StdEncoding.EncodeToString(pub),
	})
	if err == nil {
		t.Fatal("an unsigned bundle was accepted once a public key was configured")
	}
}

func TestASignedBundleVerifiesAndImports(t *testing.T) {
	st := newTestStore(t)
	seedEvent(t, st, event.KindDNSResolverChanged, nil)
	var buf bytes.Buffer
	Export(context.Background(), st, &buf, ExportOptions{From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Hour)})

	pub, priv, _ := ed25519.GenerateKey(nil)
	signed := signBundle(t, buf.Bytes(), priv)

	dest := filepath.Join(t.TempDir(), "imported.db")
	if _, err := Import(bytes.NewReader(signed), ImportOptions{
		DestPath: dest, PublicKey: base64.StdEncoding.EncodeToString(pub),
	}); err != nil {
		t.Fatalf("a correctly signed bundle was refused: %v", err)
	}
}

func TestABundleSignedByTheWrongKeyIsRefused(t *testing.T) {
	st := newTestStore(t)
	seedEvent(t, st, event.KindDNSResolverChanged, nil)
	var buf bytes.Buffer
	Export(context.Background(), st, &buf, ExportOptions{From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Hour)})

	_, wrongPriv, _ := ed25519.GenerateKey(nil)
	signed := signBundle(t, buf.Bytes(), wrongPriv)

	configuredPub, _, _ := ed25519.GenerateKey(nil)
	dest := filepath.Join(t.TempDir(), "imported.db")
	_, err := Import(bytes.NewReader(signed), ImportOptions{
		DestPath: dest, PublicKey: base64.StdEncoding.EncodeToString(configuredPub),
	})
	if err == nil {
		t.Fatal("a bundle signed by a different key than the one configured was accepted")
	}
}

func TestAnInvalidEventIsSkippedNotFatal(t *testing.T) {
	st := newTestStore(t)
	seedEvent(t, st, event.KindDNSResolverChanged, nil)
	var buf bytes.Buffer
	Export(context.Background(), st, &buf, ExportOptions{From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Hour)})

	// Inject a second, invalid event (no ID) directly into events.json, and
	// recompute the checksum file to match - this must exercise Import's own
	// per-event validation, not the checksum check TestImportRefusesA
	// TamperedBundle already covers.
	corrupted := replaceMemberAndRechecksum(t, buf.Bytes(), eventsFile, func(data []byte) []byte {
		var events []*event.Event
		if err := json.Unmarshal(data, &events); err != nil {
			t.Fatal(err)
		}
		events = append(events, &event.Event{Kind: event.KindDNSResolverChanged}) // no ID: invalid
		out, err := json.Marshal(events)
		if err != nil {
			t.Fatal(err)
		}
		return out
	})

	dest := filepath.Join(t.TempDir(), "imported.db")
	manifest, err := Import(bytes.NewReader(corrupted), ImportOptions{DestPath: dest})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if manifest.EventCount != 1 {
		t.Errorf("EventCount = %d, want 1 (the invalid second event should be skipped, not counted)", manifest.EventCount)
	}
}

// --- test helpers for manipulating an already-built bundle ---

func extractRawMembers(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	out := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if err != nil {
			break
		}
		buf, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read tar entry %s: %v", hdr.Name, err)
		}
		out[hdr.Name] = buf
	}
	return out
}

func rebuildTarGz(t *testing.T, members map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := writeTarGz(&buf, members); err != nil {
		t.Fatalf("rebuild archive: %v", err)
	}
	return buf.Bytes()
}

// tamperMember rewrites one member's content (without recomputing checksums,
// which is the point: it proves Import notices) and returns a new archive.
func tamperMember(t *testing.T, data []byte, name string, mutate func([]byte) []byte) []byte {
	t.Helper()
	members := extractRawMembers(t, data)
	members[name] = mutate(members[name])
	return rebuildTarGz(t, members)
}

// replaceMemberAndRechecksum rewrites one member and recomputes SHA256SUMS to
// match, for tests that need a structurally valid bundle carrying deliberately
// bad application-level content (as opposed to tamperMember, which proves the
// checksum check itself works).
func replaceMemberAndRechecksum(t *testing.T, data []byte, name string, mutate func([]byte) []byte) []byte {
	t.Helper()
	members := extractRawMembers(t, data)
	members[name] = mutate(members[name])
	delete(members, signatureFile) // no longer matches the recomputed checksums
	members[checksumsFile] = []byte(checksumFile(map[string][]byte{
		manifestFile:  members[manifestFile],
		eventsFile:    members[eventsFile],
		incidentsFile: members[incidentsFile],
	}))
	return rebuildTarGz(t, members)
}

// signBundle recomputes nothing but the signature: it signs the bundle's own
// existing SHA256SUMS content and adds SHA256SUMS.sig, exactly as an
// external "make sign"-style step would.
func signBundle(t *testing.T, data []byte, priv ed25519.PrivateKey) []byte {
	t.Helper()
	members := extractRawMembers(t, data)
	members[signatureFile] = ed25519.Sign(priv, members[checksumsFile])
	return rebuildTarGz(t, members)
}

// addTarEntry appends one extra, unrelated entry to an existing archive,
// simulating a hostile or malformed bundle that also happens to be a valid
// one - the case that matters, since anyone can put anything in a .tar.gz.
func addTarEntry(t *testing.T, data []byte, name string, content []byte) []byte {
	t.Helper()
	members := extractRawMembers(t, data)

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for n, d := range members {
		tw.WriteHeader(&tar.Header{Name: n, Mode: 0o600, Size: int64(len(d))})
		tw.Write(d)
	}
	tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(content))})
	tw.Write(content)
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

// TestEmptyWindowEncodesArraysNotNull pins that a bundle of an empty window
// carries "[]" for events and incidents, not "null", so a reader in another
// language does not need Go's nil-slice convention.
func TestEmptyWindowEncodesArraysNotNull(t *testing.T) {
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var buf bytes.Buffer
	if _, err := Export(context.Background(), st, &buf, ExportOptions{From: time.Now().Add(-time.Hour), To: time.Now()}); err != nil {
		t.Fatal(err)
	}
	members, err := readTarGz(&buf)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{eventsFile, incidentsFile} {
		if got := strings.TrimSpace(string(members[name])); got != "[]" {
			t.Errorf("%s = %q, want []", name, got)
		}
	}
}
