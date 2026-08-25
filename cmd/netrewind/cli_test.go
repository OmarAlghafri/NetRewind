package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/store"
	_ "modernc.org/sqlite"
)

// storeWith builds a real store with a small history and returns its path.
func storeWith(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.db")
	st, err := store.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	b := event.NewBuilder("obs-1", nil)
	now := time.Now()
	events := []*event.Event{
		b.New(event.SourceNetlink, event.KindLinkDown, event.SevWarn, event.Iface("eth1", 3)).
			WithAttr("ifname", "eth1").WithAttr("cause", "carrier"),
		b.New(event.SourceNetlink, event.KindARPBindingChanged, event.SevError,
			event.Host("192.168.20.1", "aa:bb:cc:dd:ee:ff")).
			WithAttr("ip", "192.168.20.1").WithAttr("is_gateway", true),
		b.New(event.SourceInternal, event.KindSystemGap, event.SevWarn, event.Observer("obs-1")).
			WithAttr("gap_duration_ms", int64(45000)),
	}
	for i, e := range events {
		e.TSWall = now.Add(-time.Duration(10-i) * time.Minute).UnixNano()
	}
	if err := st.Append(context.Background(), events...); err != nil {
		t.Fatal(err)
	}
	return path
}

// run executes the real command tree and returns what a user would see.
func run(t *testing.T, args ...string) (stdout string, err error) {
	t.Helper()
	var out bytes.Buffer
	cmd := newRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), err
}

func TestEventsCommandPrintsWhatWasRecorded(t *testing.T) {
	db := storeWith(t)

	out, err := run(t, "events", "--db", db, "--last", "1h")
	if err != nil {
		t.Fatalf("events: %v\n%s", err, out)
	}
	for _, want := range []string{"link.down", "eth1", "l2.arp_binding_changed", "192.168.20.1"} {
		if !strings.Contains(out, want) {
			t.Errorf("the output does not mention %q:\n%s", want, out)
		}
	}
}

func TestEventsAsJSONIsValidAndComplete(t *testing.T) {
	db := storeWith(t)

	out, err := run(t, "events", "--db", db, "--last", "1h", "-o", "json")
	if err != nil {
		t.Fatalf("events -o json: %v\n%s", err, out)
	}
	var events []map[string]any
	if err := json.Unmarshal([]byte(out), &events); err != nil {
		t.Fatalf("the json output does not parse: %v\n%s", err, out)
	}
	if len(events) != 3 {
		t.Errorf("got %d events, want 3", len(events))
	}
	// Machine output has to carry the identifiers a human table leaves out.
	for _, e := range events {
		for _, field := range []string{"event_id", "ts_wall", "kind", "observer_id"} {
			if _, ok := e[field]; !ok {
				t.Errorf("a json event is missing %s: %v", field, e)
			}
		}
	}
}

func TestFilteringByFamilyAndKind(t *testing.T) {
	db := storeWith(t)

	out, err := run(t, "events", "--db", db, "--last", "1h", "--family", "link")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "link.down") {
		t.Error("--family link dropped the link event")
	}
	if strings.Contains(out, "arp_binding_changed") {
		t.Error("--family link kept an l2 event")
	}
}

func TestTimelineAndIncidentsAndRulesAllRun(t *testing.T) {
	db := storeWith(t)

	for _, args := range [][]string{
		{"timeline", "--db", db, "--last", "1h"},
		{"incidents", "--db", db, "--last", "1h"},
		{"what-happened", "--db", db, "--host", "192.168.20.1", "--at", "-5m"},
		{"version"},
	} {
		out, err := run(t, args...)
		if err != nil {
			t.Errorf("%v failed: %v\n%s", args, err, out)
		}
	}
}

// A gap in the record must be impossible to miss when reading a timeline.
func TestTheTimelineSaysWhenTheRecorderWasBlind(t *testing.T) {
	db := storeWith(t)

	out, err := run(t, "timeline", "--db", db, "--last", "1h")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "not watching") && !strings.Contains(out, "gap") {
		t.Errorf("a timeline covering a system.gap does not mention it:\n%s", out)
	}
}

/* ------------------------------------------------------------------ */
/* What a user gets wrong                                             */
/* ------------------------------------------------------------------ */

func TestAMissingStoreSaysSoRatherThanPrintingNothing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope", "events.db")

	out, err := run(t, "events", "--db", missing, "--last", "1h")
	if err == nil {
		t.Fatalf("a store that does not exist was read successfully:\n%s", out)
	}
}

func TestAStoreThatIsNotADatabaseIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.db")
	if err := os.WriteFile(path, []byte("not a database\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "events", "--db", path, "--last", "1h")
	if err == nil {
		t.Fatalf("a text file was accepted as an event store:\n%s", out)
	}
}

func TestAnUnparseableTimeIsExplained(t *testing.T) {
	db := storeWith(t)

	_, err := run(t, "what-happened", "--db", db, "--host", "x", "--at", "yesterday-ish")
	if err == nil {
		t.Fatal("an unparseable --at was accepted")
	}
	// The message has to show what would work, not just that this did not.
	if !strings.Contains(err.Error(), "15:04") && !strings.Contains(err.Error(), "-2h") {
		t.Errorf("the error does not show an acceptable form: %v", err)
	}
}

// A misspelled family must not be answered with the same words a genuinely
// quiet network produces.
func TestAnUnknownFamilyIsRefused(t *testing.T) {
	db := storeWith(t)

	out, err := run(t, "events", "--db", db, "--last", "1h", "--family", "layer8")
	if err == nil {
		t.Fatalf("an unknown family was accepted and matched nothing:\n%s", out)
	}
	if !strings.Contains(err.Error(), "layer8") {
		t.Errorf("the error does not name the offending family: %v", err)
	}
	// It has to say what would have worked.
	if !strings.Contains(err.Error(), "link") || !strings.Contains(err.Error(), "system") {
		t.Errorf("the error does not list the families that exist: %v", err)
	}
}

// Reading a store must never create one. A mistyped path answering "no events"
// is indistinguishable from a network on which nothing happened.
func TestReadingAMistypedPathDoesNotCreateAStore(t *testing.T) {
	typo := filepath.Join(t.TempDir(), "events.db")

	out, err := run(t, "events", "--db", typo, "--last", "1h")
	if err == nil {
		t.Fatalf("reading a store that does not exist succeeded:\n%s", out)
	}
	if _, statErr := os.Stat(typo); statErr == nil {
		t.Error("the query tool created the store it was asked to read")
	}
	if !strings.Contains(err.Error(), "no event store") {
		t.Errorf("the error does not say the store is missing: %v", err)
	}
}

// A database that is not an event store must be named as such rather than
// answering every question with silence.
func TestADatabaseThatIsNotAnEventStoreIsNamed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "other.db")
	other, err := store.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	other.Close()

	// Take the events table away: a truncated copy, or the wrong database.
	if err := dropEventsTable(path); err != nil {
		t.Fatal(err)
	}

	if _, err := run(t, "events", "--db", path, "--last", "1h"); err == nil {
		t.Fatal("a database with no events table was read as an event store")
	} else if !strings.Contains(err.Error(), "not an event store") {
		t.Errorf("the error does not explain what is wrong: %v", err)
	}
}

// The query tool holds the evidence open. It must not be able to change it.
func TestAReadOnlyStoreRefusesWrites(t *testing.T) {
	db := storeWith(t)

	st, err := store.OpenSQLiteRead(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	b := event.NewBuilder("obs-1", nil)
	e := b.New(event.SourceNetlink, event.KindLinkUp, event.SevInfo, event.Iface("eth9", 9))
	if err := st.Append(context.Background(), e); err == nil {
		t.Error("a store opened for reading accepted a write")
	}
}

func TestAnUnknownSubcommandFails(t *testing.T) {
	if _, err := run(t, "explain-everything"); err == nil {
		t.Error("an unknown subcommand was accepted")
	}
}

func dropEventsTable(path string) error {
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec("DROP TABLE events")
	return err
}
