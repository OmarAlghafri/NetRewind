package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/OmarAlghafri/netrewind/internal/identity"
)

// identityDDL is applied alongside the event schema.
//
// There is no uniqueness constraint on (attr_type, attr_value): the whole point
// is that the same address belongs to different hosts at different times, and
// the table has to be able to say so. Uniqueness applies only to the *current*
// binding, which the partial index enforces.
const identityDDL = `
CREATE TABLE IF NOT EXISTS identity_binding (
    host_id     TEXT    NOT NULL,
    attr_type   TEXT    NOT NULL,
    attr_value  TEXT    NOT NULL,
    valid_from  INTEGER NOT NULL,
    valid_to    INTEGER NOT NULL DEFAULT 0,
    confidence  INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_ident_attr ON identity_binding(attr_type, attr_value, valid_from);
CREATE INDEX IF NOT EXISTS idx_ident_host ON identity_binding(host_id, valid_from);
CREATE UNIQUE INDEX IF NOT EXISTS idx_ident_current
    ON identity_binding(attr_type, attr_value) WHERE valid_to = 0;
`

// LoadCurrent returns every binding that has not been superseded.
func (s *SQLite) LoadCurrent(ctx context.Context) ([]identity.Binding, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT host_id, attr_type, attr_value, valid_from, valid_to, confidence
		 FROM identity_binding WHERE valid_to = 0`)
	if err != nil {
		return nil, fmt.Errorf("store: load bindings: %w", err)
	}
	defer rows.Close()

	var out []identity.Binding
	for rows.Next() {
		var b identity.Binding
		if err := rows.Scan(&b.HostID, &b.AttrType, &b.AttrValue, &b.ValidFrom, &b.ValidTo, &b.Confidence); err != nil {
			return nil, fmt.Errorf("store: scan binding: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Open records a binding as current. Any current binding of the same attribute
// is closed first, so the unique index over live rows always holds.
func (s *SQLite) Open(ctx context.Context, b identity.Binding) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`UPDATE identity_binding SET valid_to = ?
		 WHERE attr_type = ? AND attr_value = ? AND valid_to = 0`,
		b.ValidFrom, b.AttrType, b.AttrValue); err != nil {
		return fmt.Errorf("store: close superseded binding: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO identity_binding (host_id, attr_type, attr_value, valid_from, valid_to, confidence)
		 VALUES (?,?,?,?,0,?)`,
		b.HostID, b.AttrType, b.AttrValue, b.ValidFrom, b.Confidence); err != nil {
		return fmt.Errorf("store: open binding: %w", err)
	}
	return tx.Commit()
}

// CloseBinding ends the current binding of an attribute without opening another.
func (s *SQLite) CloseBinding(ctx context.Context, attrType, attrValue string, at int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE identity_binding SET valid_to = ?
		 WHERE attr_type = ? AND attr_value = ? AND valid_to = 0`,
		at, attrType, attrValue)
	if err != nil {
		return fmt.Errorf("store: close binding: %w", err)
	}
	return nil
}

// PruneIdentity drops bindings that can no longer be needed.
//
// Retention was documented as how much history the recorder keeps and it
// bounded the events table alone. This table grew forever: one ARP sweep across
// a /16 is sixty-five thousand rows that no configured retention would ever
// remove, and the deployment that suffers most is the appliance, whose whole
// premise is being plugged in and forgotten.
//
// It cannot simply delete by age, and the trap is worth naming. A binding still
// in force carries the time it was made, not the last time it was seen, so a
// machine that has held one address for a year looks older than everything else
// in the table - and pruning by age would delete precisely the stable,
// long-lived, most useful entries while keeping the churn.
//
// Two rules, and both are provable rather than approximate:
//
//   - a binding that has been superseded exists to answer "which machine held
//     this address at that moment", and the moments anyone can ask about are
//     the ones there are still events for. Once it ended before the oldest
//     event kept, nothing can ask.
//
//   - a binding still in force is worth keeping while its host appears anywhere
//     in the record. When no retained event names that host, the binding cannot
//     be needed to explain one - and the next time the machine is seen it is
//     recorded again, at which point the recorder knows it once more.
//
// The second is run only against bindings older than the cutoff, so the
// correlated lookup is over the stale part of the table rather than all of it.
func (s *SQLite) PruneIdentity(ctx context.Context, before int64) (int64, error) {
	var removed int64
	res, err := s.db.ExecContext(ctx,
		"DELETE FROM identity_binding WHERE valid_to > 0 AND valid_to < ?", before)
	if err != nil {
		return 0, fmt.Errorf("store: prune superseded bindings: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: prune superseded bindings: %w", err)
	}
	removed += n

	res, err = s.db.ExecContext(ctx, `
		DELETE FROM identity_binding
		WHERE valid_to = 0 AND valid_from < ?
		  AND NOT EXISTS (SELECT 1 FROM events WHERE events.subject_id = identity_binding.host_id)`,
		before)
	if err != nil {
		return 0, fmt.Errorf("store: prune forgotten bindings: %w", err)
	}
	n, err = res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: prune forgotten bindings: %w", err)
	}
	return removed + n, nil
}

// ResolveAt returns the host an attribute belonged to at an instant.
func (s *SQLite) ResolveAt(ctx context.Context, attrType, value string, at int64) (string, bool, error) {
	var host string
	err := s.db.QueryRowContext(ctx,
		`SELECT host_id FROM identity_binding
		 WHERE attr_type = ? AND attr_value = ?
		   AND valid_from <= ? AND (valid_to = 0 OR valid_to > ?)
		 ORDER BY valid_from DESC LIMIT 1`,
		attrType, value, at, at).Scan(&host)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("store: resolve %s %s: %w", attrType, value, err)
	}
	return host, true, nil
}

// LabelsFor returns every attribute value a host answered to during a window.
//
// A binding counts if it overlaps the window at all, which is what makes it
// possible to ask about an address a machine has since given up.
func (s *SQLite) LabelsFor(ctx context.Context, hostID string, from, to int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT attr_value FROM identity_binding
		 WHERE host_id = ? AND valid_from <= ? AND (valid_to = 0 OR valid_to >= ?)`,
		hostID, to, from)
	if err != nil {
		return nil, fmt.Errorf("store: labels for %s: %w", hostID, err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("store: scan label: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

var _ identity.Repo = (*SQLite)(nil)
