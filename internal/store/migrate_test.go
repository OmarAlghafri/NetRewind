package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/OmarAlghafri/netrewind/internal/event"
)

// withMigrations temporarily replaces the package's migration table for one
// test, so production code never has a real migration in it just to make a
// test possible - the runner is proven with a synthetic one instead.
func withMigrations(t *testing.T, m []migration) {
	t.Helper()
	orig := migrations
	migrations = m
	t.Cleanup(func() { migrations = orig })
}

// stampedDB creates a raw SQLite file with the "events" and "widgets" tables
// (widgets stands in for whatever a real migration would restructure) and a
// given schema_version already recorded, bypassing OpenSQLite entirely -
// this is what an on-disk store from an older or newer build actually looks
// like, not a simulation of one.
func stampedDB(t *testing.T, version int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatalf("create meta: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE widgets (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`); err != nil {
		t.Fatalf("create widgets: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO widgets (id, name) VALUES (1, 'original')`); err != nil {
		t.Fatalf("seed widgets: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO meta (key, value) VALUES (?, ?)`, MetaSchemaVersion, fmt.Sprint(version)); err != nil {
		t.Fatalf("stamp version: %v", err)
	}
	return path
}

func widgetName(t *testing.T, path string) string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer db.Close()
	var name string
	if err := db.QueryRow(`SELECT name FROM widgets WHERE id = 1`).Scan(&name); err != nil {
		t.Fatalf("read widgets: %v", err)
	}
	return name
}

func TestAFreshDatabaseNeedsNoMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := ensureSchemaVersion(context.Background(), db, path); err != nil {
		t.Fatalf("ensureSchemaVersion on a brand-new file: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".bak" {
			t.Errorf("a backup file %s was created for a brand-new database", e.Name())
		}
	}
}

func TestADatabaseAlreadyAtTheCurrentVersionIsUntouched(t *testing.T) {
	path := stampedDB(t, event.SchemaVersion)
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := ensureSchemaVersion(context.Background(), db, path); err != nil {
		t.Fatalf("ensureSchemaVersion at the current version: %v", err)
	}
	if got := widgetName(t, path); got != "original" {
		t.Errorf("widgets.name = %q, want untouched original", got)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".bak" {
			t.Errorf("a backup file %s was created when no migration ran", e.Name())
		}
	}
}

func TestANewerDatabaseThanThisBuildUnderstandsIsRefused(t *testing.T) {
	path := stampedDB(t, event.SchemaVersion+1)
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	err = ensureSchemaVersion(context.Background(), db, path)
	if err == nil {
		t.Fatal("opened a database from a newer schema version without error")
	}
	if got := widgetName(t, path); got != "original" {
		t.Errorf("widgets.name = %q; a refused open must not touch the file", got)
	}
}

func TestAnOlderDatabaseWithNoRegisteredPathIsRefused(t *testing.T) {
	// migrations is left empty (the real, production state), so a database
	// behind the current version has no way forward.
	path := stampedDB(t, event.SchemaVersion-1)
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	err = ensureSchemaVersion(context.Background(), db, path)
	if err == nil {
		t.Fatal("opened an old database with no migration path registered")
	}
	if got := widgetName(t, path); got != "original" {
		t.Errorf("widgets.name = %q; a refused open must not touch the file", got)
	}
}

func TestAnOlderDatabaseIsMigratedThroughARegisteredStep(t *testing.T) {
	from := event.SchemaVersion - 1
	withMigrations(t, []migration{
		{from: from, to: event.SchemaVersion, description: "rename widgets to the current schema", up: func(tx *sql.Tx) error {
			_, err := tx.Exec(`UPDATE widgets SET name = 'migrated' WHERE id = 1`)
			return err
		}},
	})

	path := stampedDB(t, from)
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := ensureSchemaVersion(context.Background(), db, path); err != nil {
		t.Fatalf("ensureSchemaVersion: %v", err)
	}

	if got := widgetName(t, path); got != "migrated" {
		t.Errorf("widgets.name = %q, want migrated - the migration step did not run", got)
	}

	var recorded string
	if err := db.QueryRow(`SELECT value FROM meta WHERE key = ?`, MetaSchemaVersion).Scan(&recorded); err != nil {
		t.Fatalf("read back schema version: %v", err)
	}
	if recorded != fmt.Sprint(event.SchemaVersion) {
		t.Errorf("recorded schema version = %q, want %d", recorded, event.SchemaVersion)
	}

	// A verified backup from before the migration must exist and still show
	// the pre-migration data.
	entries, _ := os.ReadDir(filepath.Dir(path))
	var backups int
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".bak" {
			backups++
			if got := widgetName(t, filepath.Join(filepath.Dir(path), e.Name())); got != "original" {
				t.Errorf("backup %s shows %q, want the pre-migration value", e.Name(), got)
			}
		}
	}
	if backups != 1 {
		t.Errorf("found %d backup files, want exactly 1", backups)
	}
}

func TestAChainOfTwoMigrationsRunsBothInOrder(t *testing.T) {
	base := event.SchemaVersion - 2
	if base < 0 {
		t.Skip("event.SchemaVersion is too low to test a two-step chain below it")
	}
	withMigrations(t, []migration{
		{from: base, to: base + 1, description: "first step", up: func(tx *sql.Tx) error {
			_, err := tx.Exec(`UPDATE widgets SET name = name || '+step1' WHERE id = 1`)
			return err
		}},
		{from: base + 1, to: event.SchemaVersion, description: "second step", up: func(tx *sql.Tx) error {
			_, err := tx.Exec(`UPDATE widgets SET name = name || '+step2' WHERE id = 1`)
			return err
		}},
	})

	path := stampedDB(t, base)
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := ensureSchemaVersion(context.Background(), db, path); err != nil {
		t.Fatalf("ensureSchemaVersion: %v", err)
	}
	if got, want := widgetName(t, path), "original+step1+step2"; got != want {
		t.Errorf("widgets.name = %q, want %q - both steps must run, in order", got, want)
	}
}

func TestAFailedMigrationStepLeavesTheOriginalUntouched(t *testing.T) {
	from := event.SchemaVersion - 1
	withMigrations(t, []migration{
		{from: from, to: event.SchemaVersion, description: "a step that always fails", up: func(tx *sql.Tx) error {
			if _, err := tx.Exec(`UPDATE widgets SET name = 'partially-migrated' WHERE id = 1`); err != nil {
				return err
			}
			return fmt.Errorf("simulated failure partway through the step")
		}},
	})

	path := stampedDB(t, from)
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	err = ensureSchemaVersion(context.Background(), db, path)
	if err == nil {
		t.Fatal("a failing migration step was not reported as an error")
	}

	if got := widgetName(t, path); got != "original" {
		t.Errorf("widgets.name = %q after a failed migration, want the transaction rolled back to original", got)
	}
	var recorded string
	scanErr := db.QueryRow(`SELECT value FROM meta WHERE key = ?`, MetaSchemaVersion).Scan(&recorded)
	if scanErr != nil {
		t.Fatalf("read back schema version: %v", scanErr)
	}
	if recorded != fmt.Sprint(from) {
		t.Errorf("recorded schema version = %q after a failed migration, want it unchanged at %d", recorded, from)
	}
}

func TestEnsureSchemaVersionIsIdempotent(t *testing.T) {
	from := event.SchemaVersion - 1
	runs := 0
	withMigrations(t, []migration{
		{from: from, to: event.SchemaVersion, description: "counts its own runs", up: func(tx *sql.Tx) error {
			runs++
			_, err := tx.Exec(`UPDATE widgets SET name = 'migrated' WHERE id = 1`)
			return err
		}},
	})

	path := stampedDB(t, from)
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := ensureSchemaVersion(context.Background(), db, path); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := ensureSchemaVersion(context.Background(), db, path); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if runs != 1 {
		t.Errorf("migration step ran %d times, want exactly 1 - the second call should have seen the current version and stopped", runs)
	}
}

func TestRestoreBackupRoundTrips(t *testing.T) {
	original := stampedDB(t, event.SchemaVersion)
	backupPath, err := backupBeforeMigration(original)
	if err != nil {
		t.Fatalf("backupBeforeMigration: %v", err)
	}

	// Simulate the live file having since been damaged.
	db, err := sql.Open("sqlite", "file:"+original)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE widgets SET name = 'corrupted' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	if err := RestoreBackup(backupPath, original); err != nil {
		t.Fatalf("RestoreBackup: %v", err)
	}
	if got := widgetName(t, original); got != "original" {
		t.Errorf("widgets.name after restore = %q, want the backed-up original value", got)
	}
}

func TestRestoreBackupRefusesACorruptBackup(t *testing.T) {
	target := stampedDB(t, event.SchemaVersion)
	badBackup := filepath.Join(t.TempDir(), "not-a-database.bak")
	if err := os.WriteFile(badBackup, []byte("not a sqlite file"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := RestoreBackup(badBackup, target); err == nil {
		t.Fatal("restored from a file that is not a valid SQLite database")
	}
	if got := widgetName(t, target); got != "original" {
		t.Errorf("widgets.name = %q; a refused restore must not touch the target", got)
	}
}
