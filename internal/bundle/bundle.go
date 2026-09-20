// Package bundle implements the evidence bundle: a self-contained, signed,
// read-only export of part of the record, meant to be handed to someone else
// (PRD.md, use case U5) or opened by a second copy of NetRewind without ever
// touching that copy's own live store.
//
// PRODUCT_RELEASE_PLAN_AR.md §4.1: "صمم evidence bundle read-only: manifest،
// schema/app version، recorder capabilities، events/incidents/rules ذات
// الصلة، checksums، optional signature، وبدون أسرار أو أسماء DNS ما لم
// يوافق المستخدم. نفذ import في DB مؤقت ثم atomic promote؛ لا يلمس السجل
// الأصلي. أضف export redacted."
package bundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/incident"
	"github.com/OmarAlghafri/netrewind/internal/notes"
	"github.com/OmarAlghafri/netrewind/internal/registry"
	"github.com/OmarAlghafri/netrewind/internal/store"
	"github.com/OmarAlghafri/netrewind/internal/update"
)

// FormatVersion is the bundle container format, independent of
// event.SchemaVersion - the two change for different reasons (this package's
// own layout, versus the event envelope) and conflating them would make a
// harmless bundle-format tweak look like an event schema break.
const FormatVersion = 1

// manifestFile, eventsFile, incidentsFile, checksumsFile and signatureFile
// are the only file names Import will ever look for or write to disk. This
// fixed allow-list, not the archive's own entry names, is what makes
// extraction safe from zip-slip: an archive entry naming anything else, or
// trying to escape the extraction directory, is skipped rather than acted on.
const (
	manifestFile  = "manifest.json"
	eventsFile    = "events.json"
	incidentsFile = "incidents.json"
	notesFile     = "notes.json"
	checksumsFile = "SHA256SUMS"
	signatureFile = "SHA256SUMS.sig"
)

// maxBundleMember bounds any single decompressed member. A bundle is a few
// thousand events at most; anything claiming to be larger is either
// misuse or an attempt to exhaust disk during extraction, and either way
// does not deserve to be read to find out which.
const maxBundleMember = 256 << 20 // 256 MiB

// DefaultExportLimit bounds how many events and incidents Export reads when
// ExportOptions.Limit is left at zero. store.Filter's own default (500,
// tuned for an interactive CLI query) would silently truncate a bundle meant
// to be the complete, self-contained record of a window - a bundle that
// quietly dropped most of what it claimed to cover would be worse than no
// bundle, so this is deliberately much larger, and Export still reports
// Truncated honestly if even this is not enough.
const DefaultExportLimit = 50000

// Manifest describes a bundle without requiring anything else be read first.
type Manifest struct {
	FormatVersion int       `json:"format_version"`
	SchemaVersion int       `json:"schema_version"`
	AppVersion    string    `json:"app_version"`
	ObserverID    string    `json:"observer_id"`
	CreatedAt     time.Time `json:"created_at"`
	WindowFrom    time.Time `json:"window_from"`
	WindowTo      time.Time `json:"window_to"`
	EventCount    int       `json:"event_count"`
	IncidentCount int       `json:"incident_count"`
	// Truncated is true when Export's own limit was reached, meaning the
	// window may hold more events than this bundle carries. A bundle must
	// say so rather than quietly look complete.
	Truncated    bool                `json:"truncated"`
	Redacted     bool                `json:"redacted"`
	Capabilities []registry.Snapshot `json:"capabilities,omitempty"`
	// NotesCount is a pointer so "0 notes, genuinely queried" (an empty
	// notes.json member is present) is distinguishable from "not queried
	// at all" (ExportOptions.Notes was nil, no notes.json in the archive)
	// - the same reason a Go nil slice and an empty one encode differently
	// only when a caller bothers to check, which json.Marshal's omitempty
	// on a *int actually enforces.
	NotesCount *int `json:"notes_count,omitempty"`
}

// ExportOptions controls what Export includes.
type ExportOptions struct {
	From, To     time.Time
	AppVersion   string
	ObserverID   string
	Capabilities []registry.Snapshot
	// IncludeSecrets turns off redaction. False is the safe default: a
	// bundle leaves the machine to be shared with somebody else, and an
	// operator who wants everything in it has to ask for that in one
	// explicit place, not discover it was already included.
	IncludeSecrets bool
	// Limit bounds how many events and incidents are read. Zero means
	// DefaultExportLimit.
	Limit int
	// Notes, when non-nil, folds every operator annotation for an
	// exported incident into a notes.json member (ADR 0008's own tracked
	// gap: a bundle previously carried none of this at all). Nil - the
	// daemon's own notes.Store disabled, or a CLI export against a
	// events.db with no sibling notes.db - leaves notes.json out of the
	// archive entirely, never as a claimed-empty array.
	Notes notes.Store
}

// redactedAttrs are event attribute keys stripped from a redacted export -
// currently just the one field the schema already treats as sensitive
// (docs/schema.md: "Query names are recorded behind a switch... because
// there are networks where recording them is not permitted"). A bundle must
// honour that switch even if the local recorder was configured to ignore it,
// since the bundle is what leaves the machine.
var redactedAttrs = []string{"name"}

// Export writes a tar.gz bundle to w: the manifest, every event and incident
// in [opts.From, opts.To), and a checksum file covering all of it. It does
// not sign anything - signing is a separate, external step by design (the
// same reason internal/update's signing key never touches this process: see
// Makefile's "sign" target and its comment on why CI never holds the key).
// A caller that wants a signed bundle runs that step over the checksum file
// this produces, exactly as a release is signed, and ships the resulting
// SHA256SUMS.sig alongside it for Import to find.
func Export(ctx context.Context, st store.Store, w io.Writer, opts ExportOptions) (Manifest, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = DefaultExportLimit
	}

	events, err := st.Query(ctx, store.Filter{Since: opts.From, Until: opts.To, Limit: limit, Descending: false})
	if err != nil {
		return Manifest{}, fmt.Errorf("bundle: query events: %w", err)
	}
	incidents, err := st.QueryIncidents(ctx, store.IncidentFilter{Since: opts.From, Until: opts.To, Limit: limit})
	if err != nil {
		return Manifest{}, fmt.Errorf("bundle: query incidents: %w", err)
	}

	if !opts.IncludeSecrets {
		for _, e := range events {
			redact(e)
		}
	}

	var annotations []notes.Annotation
	if opts.Notes != nil {
		ids := make([]string, len(incidents))
		for i, inc := range incidents {
			ids[i] = inc.ID
		}
		annotations, err = opts.Notes.GetAnnotations(ctx, ids)
		if err != nil {
			return Manifest{}, fmt.Errorf("bundle: query notes: %w", err)
		}
	}

	manifest := Manifest{
		FormatVersion: FormatVersion,
		SchemaVersion: event.SchemaVersion,
		AppVersion:    opts.AppVersion,
		ObserverID:    opts.ObserverID,
		CreatedAt:     time.Now().UTC(),
		WindowFrom:    opts.From,
		WindowTo:      opts.To,
		EventCount:    len(events),
		IncidentCount: len(incidents),
		Truncated:     len(events) >= limit || len(incidents) >= limit,
		Redacted:      !opts.IncludeSecrets,
		Capabilities:  opts.Capabilities,
	}
	if opts.Notes != nil {
		n := len(annotations)
		manifest.NotesCount = &n
	}

	members, err := marshalMembers(manifest, events, incidents, opts.Notes != nil, annotations)
	if err != nil {
		return Manifest{}, err
	}
	if err := writeTarGz(w, members); err != nil {
		return Manifest{}, fmt.Errorf("bundle: write archive: %w", err)
	}
	return manifest, nil
}

// redact removes the fields a redacted export must not carry, in place. It
// touches Attrs only: Evidence is the raw observation kept specifically to
// show why an event is believed, and Subject/Related are resolved identity,
// neither of which is where a query name would appear.
func redact(e *event.Event) {
	for _, key := range redactedAttrs {
		delete(e.Attrs, key)
	}
}

func marshalMembers(m Manifest, events []*event.Event, incidents []*incident.Incident, includeNotes bool, annotations []notes.Annotation) (map[string][]byte, error) {
	// An empty window is an empty array, never null: a reader that is not
	// Go should not have to know that Go encodes a nil slice as null.
	if events == nil {
		events = []*event.Event{}
	}
	if incidents == nil {
		incidents = []*incident.Incident{}
	}
	manifestJSON, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("bundle: encode manifest: %w", err)
	}
	eventsJSON, err := json.MarshalIndent(events, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("bundle: encode events: %w", err)
	}
	incidentsJSON, err := json.MarshalIndent(incidents, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("bundle: encode incidents: %w", err)
	}

	members := map[string][]byte{
		manifestFile:  manifestJSON,
		eventsFile:    eventsJSON,
		incidentsFile: incidentsJSON,
	}
	if includeNotes {
		if annotations == nil {
			annotations = []notes.Annotation{}
		}
		notesJSON, err := json.MarshalIndent(annotations, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("bundle: encode notes: %w", err)
		}
		members[notesFile] = notesJSON
	}
	members[checksumsFile] = []byte(checksumFile(members))
	return members, nil
}

// checksumFile renders a sha256sum(1)-compatible listing, the same format
// internal/update already parses (ParseChecksums) and the release process
// already produces (Makefile's "release" target) - one format for every
// place this project hands someone a checksum file.
func checksumFile(members map[string][]byte) string {
	names := make([]string, 0, len(members))
	for name := range members {
		names = append(names, name)
	}
	sort.Strings(names)

	var out []byte
	for _, name := range names {
		sum := sha256.Sum256(members[name])
		out = append(out, []byte(hex.EncodeToString(sum[:])+"  "+name+"\n")...)
	}
	return string(out)
}

func writeTarGz(w io.Writer, members map[string][]byte) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)

	names := make([]string, 0, len(members))
	for name := range members {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		data := members[name]
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o600, Size: int64(len(data)), ModTime: time.Now(),
		}); err != nil {
			return err
		}
		if _, err := tw.Write(data); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// ImportOptions controls how Import verifies and where it promotes a bundle.
type ImportOptions struct {
	// DestPath is where the verified, promoted store is written. It must not
	// already exist: Import never overwrites another bundle's import, and
	// never touches any store not at this exact path.
	DestPath string
	// PublicKey, when set, makes a signature mandatory - the same
	// mandatory-once-configured policy internal/update already applies to
	// releases (see update.Config.PublicKey and its tests). Base64,
	// raw ed25519, matching update.VerifySignature's own input.
	PublicKey string
}

// Contents is a verified, parsed bundle held in memory: what Inspect returns
// and what Import promotes into a store.
type Contents struct {
	Manifest  Manifest
	Events    []*event.Event
	Incidents []*incident.Incident
	// Notes is nil when the bundle carries no notes.json member at all
	// (an older bundle, or one exported with no notes.Store available) -
	// distinct from a non-nil empty slice, which means the exporting
	// recorder genuinely had zero annotations for this window. ADR 0008:
	// these are the *sender's* own operator notes, shown read-only and
	// labelled as such by a caller - never merged into anything.
	Notes []notes.Annotation
	// Signed reports whether the archive carried a signature that verified
	// against the key given to Inspect. False with no key given means
	// "not checked", not "unsigned".
	Signed bool
}

// Inspect reads a bundle written by Export, verifies every member against
// the checksum file, verifies the signature when publicKey is set (and
// refuses an unsigned bundle in that case), and returns the parsed
// contents without writing anything anywhere. It is the read-only half of
// Import, for a viewer that wants to show a bundle rather than restore it.
func Inspect(r io.Reader, publicKey string) (Contents, error) {
	members, err := readTarGz(r)
	if err != nil {
		return Contents{}, fmt.Errorf("bundle: read archive: %w", err)
	}
	if err := verifyChecksums(members); err != nil {
		return Contents{}, fmt.Errorf("bundle: %w", err)
	}
	if err := verifySignatureIfRequired(members, publicKey); err != nil {
		return Contents{}, fmt.Errorf("bundle: %w", err)
	}

	var c Contents
	c.Signed = publicKey != "" && len(members[signatureFile]) > 0
	if err := json.Unmarshal(members[manifestFile], &c.Manifest); err != nil {
		return Contents{}, fmt.Errorf("bundle: manifest.json does not parse: %w", err)
	}
	if c.Manifest.FormatVersion > FormatVersion {
		return Contents{}, fmt.Errorf("bundle: format v%d, this build only understands up to v%d - "+
			"open it with a newer NetRewind", c.Manifest.FormatVersion, FormatVersion)
	}
	if c.Manifest.SchemaVersion > event.SchemaVersion {
		return Contents{}, fmt.Errorf("bundle: event schema v%d, this build only understands up to v%d - "+
			"open it with a newer NetRewind", c.Manifest.SchemaVersion, event.SchemaVersion)
	}
	if err := json.Unmarshal(members[eventsFile], &c.Events); err != nil {
		return Contents{}, fmt.Errorf("bundle: events.json does not parse: %w", err)
	}
	if err := json.Unmarshal(members[incidentsFile], &c.Incidents); err != nil {
		return Contents{}, fmt.Errorf("bundle: incidents.json does not parse: %w", err)
	}
	if raw, ok := members[notesFile]; ok {
		if err := json.Unmarshal(raw, &c.Notes); err != nil {
			return Contents{}, fmt.Errorf("bundle: notes.json does not parse: %w", err)
		}
	}
	return c, nil
}

// Import verifies a bundle written by Export and, only if every check
// passes, produces a new, independent SQLite store at opts.DestPath
// containing exactly its events and incidents. It never opens, modifies, or
// even looks at any other store on the machine - promotion is a single
// rename of a fully-built temporary file, so DestPath either ends up
// complete or is never created at all, with nothing in between ever visible.
func Import(r io.Reader, opts ImportOptions) (Manifest, error) {
	if opts.DestPath == "" {
		return Manifest{}, fmt.Errorf("bundle: import: DestPath is required")
	}
	if _, err := os.Stat(opts.DestPath); err == nil {
		return Manifest{}, fmt.Errorf("bundle: import: %s already exists; refusing to overwrite it", opts.DestPath)
	}

	contents, err := Inspect(r, opts.PublicKey)
	if err != nil {
		return Manifest{}, err
	}
	manifest, events, incidents := contents.Manifest, contents.Events, contents.Incidents

	tmp := opts.DestPath + ".importing"
	os.Remove(tmp)
	st, err := store.OpenSQLite(tmp)
	if err != nil {
		return Manifest{}, fmt.Errorf("bundle: create staging store: %w", err)
	}

	ctx := context.Background()
	var skipped int
	valid := make([]*event.Event, 0, len(events))
	for _, e := range events {
		if err := e.Validate(); err != nil {
			skipped++
			continue
		}
		valid = append(valid, e)
	}
	if len(valid) > 0 {
		if err := st.Append(ctx, valid...); err != nil {
			st.Close()
			os.Remove(tmp)
			return Manifest{}, fmt.Errorf("bundle: stage events: %w", err)
		}
	}
	if len(incidents) > 0 {
		if err := st.AppendIncidents(ctx, incidents...); err != nil {
			st.Close()
			os.Remove(tmp)
			return Manifest{}, fmt.Errorf("bundle: stage incidents: %w", err)
		}
	}
	if err := st.Close(); err != nil {
		os.Remove(tmp)
		return Manifest{}, fmt.Errorf("bundle: close staging store: %w", err)
	}

	if err := os.Rename(tmp, opts.DestPath); err != nil {
		os.Remove(tmp)
		return Manifest{}, fmt.Errorf("bundle: promote staged store to %s: %w", opts.DestPath, err)
	}

	manifest.EventCount = len(valid)
	_ = skipped // surfaced to the caller once Import grows a structured result; a bad event is dropped, never silently invented as valid
	return manifest, nil
}

func readTarGz(r io.Reader) (map[string][]byte, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("not gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	members := make(map[string][]byte)
	allowed := map[string]bool{
		manifestFile: true, eventsFile: true, incidentsFile: true, notesFile: true,
		checksumsFile: true, signatureFile: true,
	}

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("corrupt archive: %w", err)
		}
		// The fixed allow-list, not hdr.Name, decides what touches the
		// filesystem or memory - this is what makes a path like
		// "../../etc/passwd" or an absolute path in the archive inert rather
		// than a traversal.
		name := filepath.Base(hdr.Name)
		if !allowed[name] || hdr.Typeflag != tar.TypeReg {
			continue
		}
		if hdr.Size > maxBundleMember {
			return nil, fmt.Errorf("%s claims %d bytes, over the %d limit", name, hdr.Size, maxBundleMember)
		}
		data, err := io.ReadAll(io.LimitReader(tr, maxBundleMember+1))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		if len(data) > maxBundleMember {
			return nil, fmt.Errorf("%s exceeded the %d byte limit while reading", name, maxBundleMember)
		}
		members[name] = data
	}

	for _, required := range []string{manifestFile, eventsFile, incidentsFile, checksumsFile} {
		if _, ok := members[required]; !ok {
			return nil, fmt.Errorf("missing %s - not a NetRewind evidence bundle", required)
		}
	}
	return members, nil
}

func verifyChecksums(members map[string][]byte) error {
	sums, err := update.ParseChecksums(bytes.NewReader(members[checksumsFile]))
	if err != nil {
		return fmt.Errorf("%s does not parse: %w", checksumsFile, err)
	}
	for _, name := range []string{manifestFile, eventsFile, incidentsFile} {
		want, listed := sums[name]
		if !listed {
			return fmt.Errorf("%s is not listed in %s", name, checksumsFile)
		}
		got := sha256.Sum256(members[name])
		if !bytes.Equal(got[:], want) {
			return fmt.Errorf("%s does not match %s - the bundle is corrupt or was tampered with", name, checksumsFile)
		}
	}
	return nil
}

func verifySignatureIfRequired(members map[string][]byte, publicKey string) error {
	if publicKey == "" {
		return nil
	}
	sig, ok := members[signatureFile]
	if !ok {
		return fmt.Errorf("a public key is configured, so an unsigned bundle is refused (no %s)", signatureFile)
	}
	if err := update.VerifySignature(publicKey, members[checksumsFile], sig); err != nil {
		return fmt.Errorf("signature does not verify: %w", err)
	}
	return nil
}
