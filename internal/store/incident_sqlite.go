package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/incident"
)

const incidentDDL = `
CREATE TABLE IF NOT EXISTS incidents (
    incident_id TEXT    PRIMARY KEY,
    opened_at   INTEGER NOT NULL,
    closed_at   INTEGER NOT NULL DEFAULT 0,
    status      TEXT    NOT NULL,
    title       TEXT    NOT NULL,
    severity    TEXT    NOT NULL,
    confidence  INTEGER NOT NULL,
    rule_id     TEXT    NOT NULL,
    root_cause  TEXT,
    chain       TEXT    NOT NULL,
    victims     TEXT,
    advice      TEXT
);

CREATE INDEX IF NOT EXISTS idx_incidents_opened ON incidents(opened_at);
CREATE INDEX IF NOT EXISTS idx_incidents_rule   ON incidents(rule_id, opened_at);
`

// IncidentFilter selects incidents.
type IncidentFilter struct {
	Since       time.Time
	Until       time.Time
	RuleID      string
	MinSeverity event.Severity
	Limit       int
}

// AppendIncidents stores incidents. Re-storing one is harmless: the identifier
// is the incident's own, so a correlation pass that is run twice over the same
// history does not duplicate its conclusions.
func (s *SQLite) AppendIncidents(ctx context.Context, incidents ...*incident.Incident) error {
	if len(incidents) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO incidents (incident_id, opened_at, closed_at, status, title,
		    severity, confidence, rule_id, root_cause, chain, victims, advice)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(incident_id) DO UPDATE SET
		    closed_at  = excluded.closed_at,
		    status     = excluded.status,
		    severity   = excluded.severity,
		    confidence = excluded.confidence,
		    root_cause = excluded.root_cause,
		    chain      = excluded.chain,
		    victims    = excluded.victims`)
	if err != nil {
		return fmt.Errorf("store: prepare incident insert: %w", err)
	}
	defer stmt.Close()

	for _, inc := range incidents {
		root, err := marshalJSON(inc.RootCause)
		if err != nil {
			return err
		}
		chain, err := marshalJSON(inc.Chain)
		if err != nil {
			return err
		}
		victims, err := marshalJSON(inc.Victims)
		if err != nil {
			return err
		}
		if _, err := stmt.ExecContext(ctx,
			inc.ID, inc.OpenedAt, inc.ClosedAt, string(inc.Status), inc.Title,
			string(inc.Severity), inc.Confidence, inc.RuleID, root, chain, victims, inc.Advice,
		); err != nil {
			return fmt.Errorf("store: insert incident %s: %w", inc.RuleID, err)
		}
	}
	return tx.Commit()
}

// QueryIncidents returns matching incidents, oldest first.
func (s *SQLite) QueryIncidents(ctx context.Context, f IncidentFilter) ([]*incident.Incident, error) {
	var where []string
	var args []any

	if !f.Since.IsZero() {
		where = append(where, "opened_at >= ?")
		args = append(args, f.Since.UnixNano())
	}
	if !f.Until.IsZero() {
		where = append(where, "opened_at <= ?")
		args = append(args, f.Until.UnixNano())
	}
	if f.RuleID != "" {
		where = append(where, "rule_id = ?")
		args = append(args, f.RuleID)
	}
	if f.MinSeverity != "" {
		// Severity is stored as a word, so the ordering has to be spelled out
		// rather than compared lexically.
		where = append(where, "CASE severity WHEN 'error' THEN 3 WHEN 'warn' THEN 2 WHEN 'notice' THEN 1 ELSE 0 END >= ?")
		args = append(args, severityRank(f.MinSeverity))
	}

	q := `SELECT incident_id, opened_at, closed_at, status, title, severity,
	             confidence, rule_id, root_cause, chain, victims, advice
	      FROM incidents`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY opened_at ASC LIMIT ?"
	limit := f.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: query incidents: %w", err)
	}
	defer rows.Close()

	var out []*incident.Incident
	for rows.Next() {
		var (
			inc                          incident.Incident
			status, severity             string
			root, chain, victims, advice string
		)
		if err := rows.Scan(&inc.ID, &inc.OpenedAt, &inc.ClosedAt, &status, &inc.Title,
			&severity, &inc.Confidence, &inc.RuleID, &root, &chain, &victims, &advice); err != nil {
			return nil, fmt.Errorf("store: scan incident: %w", err)
		}
		inc.Status, inc.Severity, inc.Advice = incident.Status(status), event.Severity(severity), advice
		if err := json.Unmarshal([]byte(root), &inc.RootCause); err != nil {
			return nil, fmt.Errorf("store: unmarshal root cause: %w", err)
		}
		if err := json.Unmarshal([]byte(chain), &inc.Chain); err != nil {
			return nil, fmt.Errorf("store: unmarshal chain: %w", err)
		}
		if victims != "" && victims != "null" {
			if err := json.Unmarshal([]byte(victims), &inc.Victims); err != nil {
				return nil, fmt.Errorf("store: unmarshal victims: %w", err)
			}
		}
		out = append(out, &inc)
	}
	return out, rows.Err()
}

// PruneIncidents drops incidents opened before the cutoff.
//
// Incidents are kept far longer than the events under them: they are the
// conclusion, they are small, and they are what someone comes back to months
// later. The links will eventually point at events that have been pruned, which
// is why every link carries its own description.
func (s *SQLite) PruneIncidents(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM incidents WHERE opened_at < ?", before.UnixNano())
	if err != nil {
		return 0, fmt.Errorf("store: prune incidents: %w", err)
	}
	return res.RowsAffected()
}

func severityRank(s event.Severity) int {
	switch s {
	case event.SevError:
		return 3
	case event.SevWarn:
		return 2
	case event.SevNotice:
		return 1
	default:
		return 0
	}
}
