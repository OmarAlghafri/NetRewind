package store

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
)

// migration takes a database from exactly one schema version to the next.
// The runner chains them, so a jump of several versions runs every step in
// between, in order - nothing is ever skipped because it looked reachable in
// one leap.
type migration struct {
	from, to    int
	description string
	// up runs inside the same transaction as every other step and the final
	// version bump. Returning an error rolls the whole attempt back, so a
	// database that fails partway through a multi-step migration is left
	// exactly as it was, not half-migrated.
	up func(tx *sql.Tx) error
}

// migrations is empty today because event.SchemaVersion has only ever been 1
// - there is nothing to migrate from yet. It exists now, proven with a
// synthetic step in migrate_test.go, specifically so the day a real
// migration is needed it is the second entry in a mechanism already tested,
// not the first version of one written under pressure.
var migrations []migration

// ensureSchemaVersion runs before schemaDDL is applied. schemaDDL is written
// as CREATE TABLE/INDEX IF NOT EXISTS, which is safe for additive changes but
// would silently leave an old structure in place for anything a real
// migration needs to do - rename a column, split a table, transform stored
// data. This is what actually walks a database from whatever version is on
// disk to the version this binary expects, or refuses to touch it and says
// exactly why.
//
// PRODUCT_RELEASE_PLAN_AR.md §4.1: "أضف migration runner: backup قابل
// للتحقق، migration idempotent، نسخة schema، rollback/restore مجرّب، وفتح
// نسخة أحدث/أقدم بسلوك صريح."
func ensureSchemaVersion(ctx context.Context, db *sql.DB, path string) error {
	if _, err := db.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		return fmt.Errorf("store: create meta table: %w", err)
	}

	var raw string
	err := db.QueryRowContext(ctx, "SELECT value FROM meta WHERE key = ?", MetaSchemaVersion).Scan(&raw)
	switch {
	case err == sql.ErrNoRows:
		// A brand-new file, or one written before this table existed. There
		// is nothing to migrate: schemaDDL is about to create everything at
		// the current version for the first time, which is exactly what a
		// fresh store should do.
		return nil
	case err != nil:
		return fmt.Errorf("store: read schema version: %w", err)
	}

	on, convErr := strconv.Atoi(raw)
	if convErr != nil {
		return fmt.Errorf("store: %s has schema_version %q, which is not a number - "+
			"this is not a NetRewind store this binary recognises", path, raw)
	}

	// Idempotent: opening a store already at the current version is a no-op,
	// every time, whether or not it has ever been migrated before.
	if on == event.SchemaVersion {
		return nil
	}

	if on > event.SchemaVersion {
		return fmt.Errorf(
			"store: %s is schema v%d, but this build only understands up to v%d - "+
				"install a newer netrewindd rather than opening this file with an older one",
			path, on, event.SchemaVersion)
	}

	steps, err := planMigration(on, event.SchemaVersion)
	if err != nil {
		return fmt.Errorf("store: %s is schema v%d and cannot be brought to v%d: %w",
			path, on, event.SchemaVersion, err)
	}

	// Flush WAL into the main file first: a plain file copy of a database
	// still using write-ahead logging can miss committed rows that only
	// exist in the -wal file, which would make the backup a silent
	// downgrade rather than a safety net.
	if _, err := db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return fmt.Errorf("store: checkpoint before backup: %w", err)
	}
	backupPath, err := backupBeforeMigration(path)
	if err != nil {
		return fmt.Errorf("store: refusing to migrate %s without a verified backup: %w", path, err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin migration: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once Commit has succeeded

	for _, m := range steps {
		if err := m.up(tx); err != nil {
			return fmt.Errorf(
				"store: migration v%d->v%d (%s) failed; nothing on disk was changed, "+
					"and a verified backup from immediately before this attempt is at %s: %w",
				m.from, m.to, m.description, backupPath, err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		MetaSchemaVersion, strconv.Itoa(event.SchemaVersion)); err != nil {
		return fmt.Errorf("store: record migrated schema version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit migration v%d->v%d: %w", on, event.SchemaVersion, err)
	}
	return nil
}

// planMigration returns the ordered chain of steps from -> to, or an error
// naming exactly where the chain breaks.
func planMigration(from, to int) ([]migration, error) {
	var steps []migration
	cur := from
	for cur != to {
		m, ok := findStep(cur)
		if !ok {
			return nil, fmt.Errorf("no migration registered starting from v%d (%d short of v%d)", cur, to-cur, to)
		}
		steps = append(steps, m)
		cur = m.to
	}
	return steps, nil
}

func findStep(from int) (migration, bool) {
	for _, m := range migrations {
		if m.from == from {
			return m, true
		}
	}
	return migration{}, false
}

// backupBeforeMigration copies path to a sibling file and verifies the copy
// is a well-formed SQLite database before returning its path - a backup
// nobody has checked is not yet a safety net.
func backupBeforeMigration(path string) (string, error) {
	return copyAndVerify(path, fmt.Sprintf("%s.pre-migration-%d.bak", path, time.Now().UnixNano()))
}

// RestoreBackup copies a backup created before a migration attempt back over
// target, for an operator undoing a migration by hand. It verifies both the
// source and the restored copy before ever touching target, and replaces it
// with a single rename so target always holds either its original contents
// or the fully-restored ones - never a partially-written file.
func RestoreBackup(backupPath, target string) error {
	if err := verifySQLite(backupPath); err != nil {
		return fmt.Errorf("store: backup %s does not verify; refusing to restore from it: %w", backupPath, err)
	}
	tmp := target + ".restoring"
	if _, err := copyAndVerify(backupPath, tmp); err != nil {
		return fmt.Errorf("store: stage restored copy: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("store: replace %s with restored backup: %w", target, err)
	}
	return nil
}

// copyAndVerify copies src to dst (which must not already exist) and confirms
// the result is a well-formed SQLite database, removing it again if not.
func copyAndVerify(src, dst string) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", fmt.Errorf("create %s: %w", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return "", fmt.Errorf("copy %s to %s: %w", src, dst, err)
	}
	if err := out.Close(); err != nil {
		os.Remove(dst)
		return "", fmt.Errorf("close %s: %w", dst, err)
	}

	if err := verifySQLite(dst); err != nil {
		os.Remove(dst)
		return "", fmt.Errorf("%s did not verify as a SQLite database, removed: %w", dst, err)
	}
	return dst, nil
}

// verifySQLite opens path read-only and runs SQLite's own structural check,
// so a truncated or corrupt copy is caught here rather than the moment
// somebody actually needs to restore from it.
func verifySQLite(path string) error {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return err
	}
	defer db.Close()

	var result string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil {
		return fmt.Errorf("integrity_check: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("integrity_check reported %q", result)
	}
	return nil
}
