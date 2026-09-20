package notes

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver: keeps CGO_ENABLED=0 and the binary static, matching internal/store
)

// DefaultPath is notes.db beside the record - store.DataDir()/notes.db -
// resolved lazily via a function argument at call sites instead of
// importing internal/store here, so this package has zero dependency on
// the record's own store package (nothing about notes needs to know the
// event schema).
func DefaultPath(dataDir string) string {
	return filepath.Join(dataDir, "notes.db")
}

const schemaDDL = `
CREATE TABLE IF NOT EXISTS incident_notes (
    incident_id       TEXT PRIMARY KEY,
    fingerprint       TEXT NOT NULL,
    rule_id           TEXT NOT NULL,
    root_cause_kind   TEXT NOT NULL,
    root_cause_entity TEXT NOT NULL,
    opened_at_ns      INTEGER NOT NULL,
    outcome           TEXT NOT NULL,
    cause_note        TEXT NOT NULL DEFAULT '',
    resolution_note   TEXT NOT NULL DEFAULT '',
    created_at_ms     INTEGER NOT NULL,
    updated_at_ms     INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_notes_similar ON incident_notes(rule_id, root_cause_kind, root_cause_entity, opened_at_ns);

CREATE TABLE IF NOT EXISTS answer_feedback (
    answer_id   TEXT PRIMARY KEY,
    incident_id TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    profile     TEXT NOT NULL,
    model_id    TEXT NOT NULL,
    helpful     INTEGER NOT NULL,
    at_ms       INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_feedback_at ON answer_feedback(at_ms);

CREATE TABLE IF NOT EXISTS analysis_threads (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    incident_id         TEXT NOT NULL,
    at_ms               INTEGER NOT NULL,
    answer_id           TEXT NOT NULL,
    question_redacted   TEXT NOT NULL,
    summary_redacted    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_threads_incident ON analysis_threads(incident_id, at_ms);

CREATE TABLE IF NOT EXISTS meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
`

const historyOptInKey = "history_opt_in"

// SQLite is the only real implementation of Store.
type SQLite struct {
	db   *sql.DB
	path string
}

// OpenSQLite opens (creating if needed) the notes database at path. The
// schema is additive-only (CREATE ... IF NOT EXISTS), matching
// internal/store's own reasoning: a new table needs no migration, only a
// column change ever would, and none exists yet.
func OpenSQLite(path string) (*SQLite, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("notes: create %s: %w", dir, err)
		}
	}
	dsn := fmt.Sprintf(
		"file:%s?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)",
		path,
	)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("notes: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, schemaDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("notes: apply schema: %w", err)
	}
	return &SQLite{db: db, path: path}, nil
}

func (s *SQLite) Close() error { return s.db.Close() }

func clip(s string) string {
	if len(s) > MaxNoteFieldLen {
		return s[:MaxNoteFieldLen]
	}
	return s
}

func (s *SQLite) GetAnnotation(ctx context.Context, incidentID string) (*Annotation, error) {
	row := s.db.QueryRowContext(ctx, `SELECT incident_id, fingerprint, rule_id, root_cause_kind, root_cause_entity,
		opened_at_ns, outcome, cause_note, resolution_note, created_at_ms, updated_at_ms
		FROM incident_notes WHERE incident_id = ?`, incidentID)
	var a Annotation
	if err := row.Scan(&a.IncidentID, &a.Fingerprint, &a.RuleID, &a.RootCauseKind, &a.RootCauseEntity,
		&a.OpenedAtNS, &a.Outcome, &a.CauseNote, &a.ResolutionNote, &a.CreatedAtMS, &a.UpdatedAtMS); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("notes: get annotation: %w", err)
	}
	return &a, nil
}

// PutAnnotation upserts by incident_id, keeping created_at_ms from the
// first write - the same "identity fixed at first report, everything else
// replaced by the latest" rule internal/store/incident_sqlite.go's
// AppendIncidents already uses for the record itself.
func (s *SQLite) PutAnnotation(ctx context.Context, a Annotation) error {
	if !a.Outcome.Valid() {
		return fmt.Errorf("notes: invalid outcome %q", a.Outcome)
	}
	a.CauseNote = clip(a.CauseNote)
	a.ResolutionNote = clip(a.ResolutionNote)
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO incident_notes (incident_id, fingerprint, rule_id, root_cause_kind, root_cause_entity,
			opened_at_ns, outcome, cause_note, resolution_note, created_at_ms, updated_at_ms)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(incident_id) DO UPDATE SET
			fingerprint=excluded.fingerprint, rule_id=excluded.rule_id,
			root_cause_kind=excluded.root_cause_kind, root_cause_entity=excluded.root_cause_entity,
			opened_at_ns=excluded.opened_at_ns, outcome=excluded.outcome,
			cause_note=excluded.cause_note, resolution_note=excluded.resolution_note,
			updated_at_ms=excluded.updated_at_ms`,
		a.IncidentID, a.Fingerprint, a.RuleID, a.RootCauseKind, a.RootCauseEntity,
		a.OpenedAtNS, string(a.Outcome), a.CauseNote, a.ResolutionNote, now, now)
	if err != nil {
		return fmt.Errorf("notes: put annotation: %w", err)
	}
	return nil
}

func (s *SQLite) DeleteAnnotation(ctx context.Context, incidentID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM incident_notes WHERE incident_id = ?`, incidentID)
	if err != nil {
		return fmt.Errorf("notes: delete annotation: %w", err)
	}
	return nil
}

// Similar ranks same-rule+kind+entity first, then same-rule+kind, newest
// first within each tier - a single query with a tier column, ordered so
// tier 0 (the tightest match) always sorts before tier 1, matching the
// approved plan's own two-tier definition exactly.
func (s *SQLite) Similar(ctx context.Context, ruleID, rootCauseKind, rootCauseEntity, excludeIncidentID string, limit int) ([]Annotation, error) {
	if limit <= 0 {
		limit = 3
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT incident_id, fingerprint, rule_id, root_cause_kind, root_cause_entity,
			opened_at_ns, outcome, cause_note, resolution_note, created_at_ms, updated_at_ms,
			CASE WHEN root_cause_entity = ? THEN 0 ELSE 1 END AS tier
		FROM incident_notes
		WHERE rule_id = ? AND root_cause_kind = ? AND incident_id <> ?
		ORDER BY tier ASC, opened_at_ns DESC
		LIMIT ?`,
		rootCauseEntity, ruleID, rootCauseKind, excludeIncidentID, limit)
	if err != nil {
		return nil, fmt.Errorf("notes: similar: %w", err)
	}
	defer rows.Close()

	var out []Annotation
	for rows.Next() {
		var a Annotation
		var tier int
		if err := rows.Scan(&a.IncidentID, &a.Fingerprint, &a.RuleID, &a.RootCauseKind, &a.RootCauseEntity,
			&a.OpenedAtNS, &a.Outcome, &a.CauseNote, &a.ResolutionNote, &a.CreatedAtMS, &a.UpdatedAtMS, &tier); err != nil {
			return nil, fmt.Errorf("notes: similar scan: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// PutFeedback inserts one feedback row and, if that pushed the table over
// MaxFeedback, deletes the oldest rows until it fits - a growth cap, not a
// ring buffer with silent overwrite: every kept row is a real, complete
// entry.
func (s *SQLite) PutFeedback(ctx context.Context, f Feedback) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("notes: put feedback: %w", err)
	}
	defer tx.Rollback()

	helpful := 0
	if f.Helpful {
		helpful = 1
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO answer_feedback (answer_id, incident_id, fingerprint, profile, model_id, helpful, at_ms)
		VALUES (?,?,?,?,?,?,?)
		ON CONFLICT(answer_id) DO UPDATE SET helpful=excluded.helpful, at_ms=excluded.at_ms`,
		f.AnswerID, f.IncidentID, f.Fingerprint, f.Profile, f.ModelID, helpful, f.AtMS); err != nil {
		return fmt.Errorf("notes: put feedback: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM answer_feedback WHERE answer_id IN (
			SELECT answer_id FROM answer_feedback ORDER BY at_ms ASC
			LIMIT MAX(0, (SELECT COUNT(*) FROM answer_feedback) - ?)
		)`, MaxFeedback); err != nil {
		return fmt.Errorf("notes: trim feedback: %w", err)
	}
	return tx.Commit()
}

func (s *SQLite) HistoryOptIn(ctx context.Context) (bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, historyOptInKey)
	var v string
	if err := row.Scan(&v); err != nil {
		if err == sql.ErrNoRows {
			return false, nil // default off - §13.2: "do not log prompts or incident data by default"
		}
		return false, fmt.Errorf("notes: history opt-in: %w", err)
	}
	return v == "1", nil
}

// SetHistoryOptIn(false) clears every stored thread in the same
// transaction as the setting change - "off" means forgotten, not merely
// "stop adding more" (the approved plan's own wording).
func (s *SQLite) SetHistoryOptIn(ctx context.Context, on bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("notes: set history opt-in: %w", err)
	}
	defer tx.Rollback()

	v := "0"
	if on {
		v = "1"
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO meta (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, historyOptInKey, v); err != nil {
		return fmt.Errorf("notes: set history opt-in: %w", err)
	}
	if !on {
		if _, err := tx.ExecContext(ctx, `DELETE FROM analysis_threads`); err != nil {
			return fmt.Errorf("notes: clear threads on opt-out: %w", err)
		}
	}
	return tx.Commit()
}

func (s *SQLite) GetThread(ctx context.Context, incidentID string) ([]ThreadTurn, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT at_ms, answer_id, question_redacted, summary_redacted
		FROM analysis_threads WHERE incident_id = ? ORDER BY at_ms ASC`, incidentID)
	if err != nil {
		return nil, fmt.Errorf("notes: get thread: %w", err)
	}
	defer rows.Close()

	var out []ThreadTurn
	for rows.Next() {
		var t ThreadTurn
		if err := rows.Scan(&t.AtMS, &t.AnswerID, &t.QuestionRedacted, &t.SummaryRedacted); err != nil {
			return nil, fmt.Errorf("notes: get thread scan: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// AppendThread refuses when history is off, and caps each incident at
// MaxThreadTurns by dropping its own oldest turn first - a long-running
// investigation does not grow this file without bound.
func (s *SQLite) AppendThread(ctx context.Context, incidentID string, turn ThreadTurn) error {
	on, err := s.HistoryOptIn(ctx)
	if err != nil {
		return err
	}
	if !on {
		return ErrHistoryDisabled
	}
	turn.QuestionRedacted = clip(turn.QuestionRedacted)
	turn.SummaryRedacted = clip(turn.SummaryRedacted)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("notes: append thread: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO analysis_threads (incident_id, at_ms, answer_id, question_redacted, summary_redacted)
		VALUES (?,?,?,?,?)`, incidentID, turn.AtMS, turn.AnswerID, turn.QuestionRedacted, turn.SummaryRedacted); err != nil {
		return fmt.Errorf("notes: append thread: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM analysis_threads WHERE incident_id = ? AND id IN (
			SELECT id FROM analysis_threads WHERE incident_id = ? ORDER BY at_ms ASC
			LIMIT MAX(0, (SELECT COUNT(*) FROM analysis_threads WHERE incident_id = ?) - ?)
		)`, incidentID, incidentID, incidentID, MaxThreadTurns); err != nil {
		return fmt.Errorf("notes: trim thread: %w", err)
	}
	return tx.Commit()
}

func (s *SQLite) ForgetAll(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("notes: forget all: %w", err)
	}
	defer tx.Rollback()
	for _, table := range []string{"incident_notes", "answer_feedback", "analysis_threads"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table); err != nil {
			return fmt.Errorf("notes: forget all (%s): %w", table, err)
		}
	}
	return tx.Commit()
}

func (s *SQLite) Stats(ctx context.Context) (Stats, error) {
	var st Stats
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM incident_notes`).Scan(&st.Annotations); err != nil {
		return st, fmt.Errorf("notes: stats: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM answer_feedback`).Scan(&st.Feedback); err != nil {
		return st, fmt.Errorf("notes: stats: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT incident_id) FROM analysis_threads`).Scan(&st.Threads); err != nil {
		return st, fmt.Errorf("notes: stats: %w", err)
	}
	if info, err := os.Stat(s.path); err == nil {
		st.Bytes = info.Size()
	}
	return st, nil
}
