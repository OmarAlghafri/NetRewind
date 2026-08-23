package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
	_ "modernc.org/sqlite" // pure-Go driver: keeps CGO_ENABLED=0 and the binary static
)

// schemaDDL is applied on open and is idempotent.
//
// One wide table, with the kind-specific fields left as JSON in attrs. Event
// kinds are added constantly during development; a table per kind, or a column
// per attribute, would mean a migration every time a collector learns something
// new. The indexes carry the three questions the CLI actually asks: what
// happened in this window, what happened of this kind, and what happened to
// this entity.
const schemaDDL = `
CREATE TABLE IF NOT EXISTS events (
    event_id      TEXT    PRIMARY KEY,
    schema_v      INTEGER NOT NULL,
    ts_wall       INTEGER NOT NULL,
    ts_last       INTEGER NOT NULL,
    ts_mono       INTEGER NOT NULL,
    observer_id   TEXT    NOT NULL,
    source        TEXT    NOT NULL,
    kind          TEXT    NOT NULL,
    family        TEXT    NOT NULL,
    severity      TEXT    NOT NULL,
    confidence    INTEGER NOT NULL,
    subject_kind  TEXT    NOT NULL,
    subject_id    TEXT    NOT NULL DEFAULT '',
    subject_label TEXT    NOT NULL DEFAULT '',
    subject_attrs TEXT,
    related       TEXT,
    attrs         TEXT,
    evidence      TEXT,
    dedup_key     TEXT    NOT NULL DEFAULT '',
    count         INTEGER NOT NULL DEFAULT 1
);

CREATE INDEX IF NOT EXISTS idx_events_ts      ON events(ts_wall);
CREATE INDEX IF NOT EXISTS idx_events_kind_ts ON events(kind, ts_wall);
CREATE INDEX IF NOT EXISTS idx_events_fam_ts  ON events(family, ts_wall);
CREATE INDEX IF NOT EXISTS idx_events_subj_ts ON events(subject_id, ts_wall);
CREATE INDEX IF NOT EXISTS idx_events_labl_ts ON events(subject_label, ts_wall);
CREATE INDEX IF NOT EXISTS idx_events_dedup   ON events(dedup_key, ts_last);

CREATE TABLE IF NOT EXISTS meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
`

// SQLite is the embedded implementation of Store.
type SQLite struct {
	db *sql.DB
}

// OpenSQLite opens (creating if needed) the event store at path.
//
// WAL keeps the writer from blocking the CLI mid-incident, which is exactly
// when someone is querying. synchronous=NORMAL trades the most recent events on
// a hard power loss for an order of magnitude in write throughput - the right
// trade for telemetry, and the gap it can leave is bounded by the heartbeat, so
// it surfaces as system.gap rather than as silence.
func OpenSQLite(path string) (*SQLite, error) {
	dsn := fmt.Sprintf(
		"file:%s?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)",
		path,
	)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	// One writer. SQLite serialises writes anyway, and this keeps "database is
	// locked" out of the collector's hot path.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schemaDDL + identityDDL + incidentDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: apply schema: %w", err)
	}
	s := &SQLite{db: db}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.SetMeta(ctx, MetaSchemaVersion, fmt.Sprint(event.SchemaVersion)); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

const insertSQL = `
INSERT INTO events (
    event_id, schema_v, ts_wall, ts_last, ts_mono, observer_id, source, kind,
    family, severity, confidence, subject_kind, subject_id, subject_label,
    subject_attrs, related, attrs, evidence, dedup_key, count
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`

// findFoldSQL locates the row a repeat should join, if there is one inside the
// fold window. The same fact recurring an hour later is correctly a new event
// and not a bigger old one.
const findFoldSQL = `
SELECT event_id FROM events
WHERE dedup_key = ? AND dedup_key <> '' AND ts_last >= ?
ORDER BY ts_last DESC LIMIT 1`

// foldSQL extends that row. ts_wall keeps the first occurrence and ts_last
// tracks the most recent, because an incident timeline needs to know when
// something started, not only that it is still going.
const foldSQL = `UPDATE events SET count = count + ?, ts_last = ? WHERE event_id = ?`

// Append writes events in one transaction.
//
// An event that folds into an existing row has its ID rewritten to that row's,
// because after folding it is that row. Anything downstream - above all the
// correlation engine, which cites event ids as the evidence for its
// conclusions - must reference a row that exists. An incident whose chain
// points at an event the store never kept is worse than no incident.
func (s *SQLite) Append(ctx context.Context, events ...*event.Event) error {
	if len(events) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback()

	find, err := tx.PrepareContext(ctx, findFoldSQL)
	if err != nil {
		return fmt.Errorf("store: prepare fold lookup: %w", err)
	}
	defer find.Close()
	fold, err := tx.PrepareContext(ctx, foldSQL)
	if err != nil {
		return fmt.Errorf("store: prepare fold: %w", err)
	}
	defer fold.Close()
	ins, err := tx.PrepareContext(ctx, insertSQL)
	if err != nil {
		return fmt.Errorf("store: prepare insert: %w", err)
	}
	defer ins.Close()

	for _, e := range events {
		if err := e.Validate(); err != nil {
			return err
		}
		if e.DedupKey != "" {
			cutoff := e.TSWall - int64(FoldWindow)
			var survivor string
			switch err := find.QueryRowContext(ctx, e.DedupKey, cutoff).Scan(&survivor); {
			case err == nil:
				if _, err := fold.ExecContext(ctx, e.Count, e.TSWall, survivor); err != nil {
					return fmt.Errorf("store: fold %s: %w", e.Kind, err)
				}
				e.ID = survivor
				continue
			case err != sql.ErrNoRows:
				return fmt.Errorf("store: fold lookup %s: %w", e.Kind, err)
			}
		}
		subjAttrs, err := marshalMap(e.Subject.Attrs)
		if err != nil {
			return err
		}
		related, err := marshalRefs(e.Related)
		if err != nil {
			return err
		}
		attrs, err := marshalObj(e.Attrs)
		if err != nil {
			return err
		}
		evidence, err := marshalObj(e.Evidence)
		if err != nil {
			return err
		}
		if _, err := ins.ExecContext(ctx,
			e.ID, e.SchemaV, e.TSWall, e.TSWall, e.TSMono, e.ObserverID,
			string(e.Source), string(e.Kind), e.Kind.Family(), string(e.Severity),
			e.Confidence, string(e.Subject.Kind), e.Subject.ID, e.Subject.Label,
			subjAttrs, related, attrs, evidence, e.DedupKey, e.Count,
		); err != nil {
			return fmt.Errorf("store: insert %s: %w", e.Kind, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}

const selectCols = `
SELECT event_id, schema_v, ts_wall, ts_mono, observer_id, source, kind,
       severity, confidence, subject_kind, subject_id, subject_label,
       subject_attrs, related, attrs, evidence, dedup_key, count
FROM events`

// Query returns the matching slice of history.
func (s *SQLite) Query(ctx context.Context, f Filter) ([]*event.Event, error) {
	var where []string
	var args []any

	if !f.Since.IsZero() {
		where = append(where, "ts_wall >= ?")
		args = append(args, f.Since.UnixNano())
	}
	if !f.Until.IsZero() {
		where = append(where, "ts_wall <= ?")
		args = append(args, f.Until.UnixNano())
	}
	if len(f.Kinds) > 0 {
		ph := make([]string, len(f.Kinds))
		for i, k := range f.Kinds {
			ph[i] = "?"
			args = append(args, string(k))
		}
		where = append(where, "kind IN ("+strings.Join(ph, ",")+")")
	}
	if len(f.Families) > 0 {
		ph := make([]string, len(f.Families))
		for i, fam := range f.Families {
			ph[i] = "?"
			args = append(args, fam)
		}
		where = append(where, "family IN ("+strings.Join(ph, ",")+")")
	}
	if f.SubjectID != "" {
		where = append(where, "subject_id = ?")
		args = append(args, f.SubjectID)
	}
	if f.SubjectLabel != "" {
		// An entity is asked about by whatever name the operator knows it by,
		// which may be its label or something recorded in its attributes.
		where = append(where, "(subject_label = ? OR subject_attrs LIKE ?)")
		args = append(args, f.SubjectLabel, "%"+quoteForLike(f.SubjectLabel)+"%")
	}

	q := selectCols
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	if f.Descending {
		q += " ORDER BY ts_wall DESC"
	} else {
		q += " ORDER BY ts_wall ASC"
	}
	limit := f.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	q += " LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: query: %w", err)
	}
	defer rows.Close()

	var out []*event.Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func scanEvent(rows *sql.Rows) (*event.Event, error) {
	var (
		e                                   event.Event
		src, kind, sev, subjKind            string
		subjAttrs, related, attrs, evidence sql.NullString
	)
	if err := rows.Scan(
		&e.ID, &e.SchemaV, &e.TSWall, &e.TSMono, &e.ObserverID, &src, &kind,
		&sev, &e.Confidence, &subjKind, &e.Subject.ID, &e.Subject.Label,
		&subjAttrs, &related, &attrs, &evidence, &e.DedupKey, &e.Count,
	); err != nil {
		return nil, fmt.Errorf("store: scan: %w", err)
	}
	e.Source, e.Kind, e.Severity = event.Source(src), event.Kind(kind), event.Severity(sev)
	e.Subject.Kind = event.EntityKind(subjKind)
	if err := unmarshalInto(subjAttrs, &e.Subject.Attrs); err != nil {
		return nil, err
	}
	if err := unmarshalInto(related, &e.Related); err != nil {
		return nil, err
	}
	if err := unmarshalInto(attrs, &e.Attrs); err != nil {
		return nil, err
	}
	if err := unmarshalInto(evidence, &e.Evidence); err != nil {
		return nil, err
	}
	return &e, nil
}

// Prune drops history older than the cutoff.
func (s *SQLite) Prune(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM events WHERE ts_wall < ?", before.UnixNano())
	if err != nil {
		return 0, fmt.Errorf("store: prune: %w", err)
	}
	return res.RowsAffected()
}

// GetMeta returns a stored value, or "" if the key was never set.
func (s *SQLite) GetMeta(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM meta WHERE key = ?", key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("store: get meta %s: %w", key, err)
	}
	return v, nil
}

// SetMeta stores a value.
func (s *SQLite) SetMeta(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		key, value)
	if err != nil {
		return fmt.Errorf("store: set meta %s: %w", key, err)
	}
	return nil
}

// Close releases the database.
func (s *SQLite) Close() error { return s.db.Close() }

func marshalMap(m map[string]string) (any, error) {
	if len(m) == 0 {
		return nil, nil
	}
	return marshalJSON(m)
}

func marshalRefs(refs []event.EntityRef) (any, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	return marshalJSON(refs)
}

func marshalObj(m map[string]any) (any, error) {
	if len(m) == 0 {
		return nil, nil
	}
	return marshalJSON(m)
}

func marshalJSON(v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("store: marshal: %w", err)
	}
	return string(b), nil
}

// unmarshalInto decodes a JSON column, keeping numbers as json.Number.
//
// Without UseNumber, encoding/json widens every number to float64, which
// silently mangles anything above 2^53 - and nanosecond timestamps, byte
// counters and interface counters all live up there. An event store that
// corrupts the numbers it was given is worse than no event store.
func unmarshalInto(col sql.NullString, dst any) error {
	if !col.Valid || col.String == "" {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(col.String))
	dec.UseNumber()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("store: unmarshal: %w", err)
	}
	return nil
}

// quoteForLike keeps a label containing LIKE wildcards from matching more than
// it should. The attrs column is JSON, so the value is wrapped in quotes to
// avoid matching a substring of some other field.
func quoteForLike(s string) string {
	r := strings.NewReplacer("%", "", "_", "")
	return "\"" + r.Replace(s) + "\""
}

// compile-time check
var _ Store = (*SQLite)(nil)
