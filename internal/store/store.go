// Package store persists events and answers time-travel questions about them.
//
// The interface is deliberately small and deliberately not SQL-shaped: the
// first implementation is embedded SQLite because an appliance should not need
// a database server, but a busy site will eventually want a columnar store.
// Nothing above this package may know which one it is talking to.
package store

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/incident"
)

// Filter selects a slice of history. A zero Filter means "everything", which
// the CLI never asks for but tests do.
type Filter struct {
	// Since and Until bound the wall-clock window. Zero means unbounded.
	Since time.Time
	Until time.Time
	// Kinds restricts to these event kinds; empty means all kinds.
	Kinds []event.Kind
	// Families restricts to these families ("l2", "link"); empty means all.
	Families []string
	// SubjectID restricts to events anchored on one resolved entity.
	SubjectID string
	// SubjectLabel restricts by human label, for queries made before identity
	// resolution has anything to say - "what happened to 192.168.20.10".
	SubjectLabel string
	// Limit caps the number of rows returned. Zero means DefaultLimit.
	Limit int
	// Descending returns newest first. The timeline view wants oldest first.
	Descending bool
}

// DefaultLimit bounds an unbounded query so a typo cannot page in a week.
const DefaultLimit = 500

// FoldWindow is how long a repeat of the same dedup_key keeps folding into the
// existing row instead of starting a new one. A flapping interface should
// produce one growing event, not ten thousand rows.
const FoldWindow = 60 * time.Second

// Store is the event history.
type Store interface {
	// Append writes events. Events carrying a DedupKey may fold into an
	// existing row rather than insert, incrementing its count.
	Append(ctx context.Context, events ...*event.Event) error
	// Query returns matching events, ordered by wall time.
	Query(ctx context.Context, f Filter) ([]*event.Event, error)
	// Prune deletes events older than the cutoff and reports how many went.
	Prune(ctx context.Context, before time.Time) (int64, error)
	// CountEvents reports how much history is held. Exported as a gauge so an
	// operator can see retention working, and see it stop working.
	CountEvents(ctx context.Context) (int64, error)
	// AppendIncidents stores what correlation concluded.
	AppendIncidents(ctx context.Context, incidents ...*incident.Incident) error
	// QueryIncidents returns matching incidents, oldest first.
	QueryIncidents(ctx context.Context, f IncidentFilter) ([]*incident.Incident, error)
	// GetMeta and SetMeta hold small recorder state that must survive a
	// restart - most importantly the heartbeat that makes gap detection
	// possible.
	GetMeta(ctx context.Context, key string) (string, error)
	SetMeta(ctx context.Context, key, value string) error
	// Close flushes and releases the store.
	Close() error
}

// Meta keys used by the daemon.
const (
	MetaLastHeartbeat = "last_heartbeat_ns"
	MetaSchemaVersion = "schema_version"
)

// DefaultPath is where the recorder keeps its history. It is a fixed location
// rather than a setting so that the daemon and the CLI agree about where the
// evidence lives without either of them being configured.
func DefaultPath() string {
	if os.PathSeparator == '\\' {
		return filepath.Join(os.TempDir(), "netrewind", "events.db")
	}
	return "/var/lib/netrewind/events.db"
}
