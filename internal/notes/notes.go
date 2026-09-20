// Package notes is the operator's own annotations on incidents - confirmed
// cause, false positive, unresolved, free-text notes, helpful/not-helpful
// feedback on a local-AI answer, and (opt-in only) follow-up question
// threads. This is deliberately NOT part of internal/store: the record
// (events.db) is read-only over the local API by construction
// (internal/api/v1/server_test.go's TestWriteMethodsAreNotRouted), and that
// guarantee has to keep meaning what it says. Notes are a second, isolated
// SQLite file next to the record (see DefaultPath), writable only through
// this package's own small API - never through anything that also touches
// events.db.
//
// See docs/product/adr/0008-operator-notes-beside-the-record.md for why this
// exists and what it deliberately does not do: a note is never treated as
// evidence, never merged in from an imported bundle, and never referenced by
// an incident's own stable identity (incident IDs are fresh ULIDs per
// firing, internal/correlate/engine.go's build() - see Fingerprint).
package notes

import "context"

// Outcome is what the operator concluded about one incident's real cause,
// independent of what either the deterministic engine or a local-AI
// hypothesis said.
type Outcome string

const (
	OutcomeConfirmed     Outcome = "confirmed"
	OutcomeFalsePositive Outcome = "false_positive"
	OutcomeUnresolved    Outcome = "unresolved"
)

// Valid reports whether o is one of the three defined outcomes - an empty
// or unrecognised value is a request error, not silently accepted.
func (o Outcome) Valid() bool {
	switch o {
	case OutcomeConfirmed, OutcomeFalsePositive, OutcomeUnresolved:
		return true
	}
	return false
}

// Field length caps. Generous for a real operator note, small enough that
// nobody can turn this into an unbounded local key-value store.
const (
	MaxNoteFieldLen = 2000
	MaxFeedback     = 500 // oldest dropped once exceeded
	MaxThreadTurns  = 20  // per incident
)

// Annotation is one incident's operator note. Fingerprint (see Fingerprint)
// is what actually identifies "this same conclusion" across a daemon
// restart or a re-fired incident with a new ULID; IncidentID is kept too,
// as the exact row a specific analysis session was looking at.
type Annotation struct {
	IncidentID      string  `json:"incident_id"`
	Fingerprint     string  `json:"fingerprint"`
	RuleID          string  `json:"rule_id"`
	RootCauseKind   string  `json:"root_cause_kind"`
	RootCauseEntity string  `json:"root_cause_entity"`
	OpenedAtNS      int64   `json:"opened_at_ns"`
	Outcome         Outcome `json:"outcome"`
	CauseNote       string  `json:"cause_note,omitempty"`
	ResolutionNote  string  `json:"resolution_note,omitempty"`
	CreatedAtMS     int64   `json:"created_at_ms"`
	UpdatedAtMS     int64   `json:"updated_at_ms"`
}

// Feedback is one helpful/not-helpful signal on a local-AI answer. Never
// sent anywhere; this package only ever writes it to the local notes store.
type Feedback struct {
	AnswerID    string `json:"answer_id"`
	IncidentID  string `json:"incident_id"`
	Fingerprint string `json:"fingerprint"`
	Profile     string `json:"profile"`
	ModelID     string `json:"model_id"`
	Helpful     bool   `json:"helpful"`
	AtMS        int64  `json:"at_ms"`
}

// ThreadTurn is one already-redacted follow-up question/answer pair, kept
// only when the operator has opted into on-device history (see
// Store.SetHistoryOptIn). The caller (internal/ai) is responsible for
// redaction before this ever reaches the store - this package stores
// exactly the strings it is given and never inspects their content.
type ThreadTurn struct {
	AtMS             int64  `json:"at_ms"`
	AnswerID         string `json:"answer_id"`
	QuestionRedacted string `json:"question_redacted"`
	SummaryRedacted  string `json:"summary_redacted"`
}

// Store is the write surface for operator notes. Every method is scoped to
// this package's own database; none can reach events.db, and nothing in
// this interface accepts a store.Store or any other record-facing type.
type Store interface {
	GetAnnotation(ctx context.Context, incidentID string) (*Annotation, error)
	PutAnnotation(ctx context.Context, a Annotation) error
	DeleteAnnotation(ctx context.Context, incidentID string) error

	// Similar returns prior annotated incidents matching the same rule and
	// root cause, ranked same-rule+kind+entity first, then same-rule+kind,
	// newest first within each tier, excluding excludeIncidentID. Never
	// returns more than limit rows.
	Similar(ctx context.Context, ruleID, rootCauseKind, rootCauseEntity, excludeIncidentID string, limit int) ([]Annotation, error)

	// GetAnnotations is the bulk form of GetAnnotation, for a bundle export
	// (internal/bundle) folding in every note for the incidents already in
	// its window - one query instead of one GetAnnotation call per
	// incident. Silently skips any id with no annotation (an incident an
	// operator never annotated is not an error); never returns more rows
	// than distinct ids were asked for. Empty in, empty (not nil-error)
	// out.
	GetAnnotations(ctx context.Context, incidentIDs []string) ([]Annotation, error)

	PutFeedback(ctx context.Context, f Feedback) error

	// HistoryOptIn reports the current on-device-history setting; threads
	// are stored only while it is true, and turning it off clears every
	// stored thread immediately (SetHistoryOptIn's own doc comment).
	HistoryOptIn(ctx context.Context) (bool, error)
	// SetHistoryOptIn changes the setting. Setting it to false deletes all
	// stored threads as part of the same call - "off" means forgotten, not
	// merely "stop adding more".
	SetHistoryOptIn(ctx context.Context, on bool) error
	GetThread(ctx context.Context, incidentID string) ([]ThreadTurn, error)
	// AppendThread refuses with ErrHistoryDisabled when HistoryOptIn is
	// false, rather than silently storing anonymous data nobody asked to
	// keep.
	AppendThread(ctx context.Context, incidentID string, turn ThreadTurn) error

	// ForgetAll deletes every annotation, feedback entry and thread. It
	// does not change the history-opt-in setting itself.
	ForgetAll(ctx context.Context) error

	// Stats reports counts and the on-disk size, for Diagnostics - never
	// the file path (which can carry a Windows username).
	Stats(ctx context.Context) (Stats, error)

	Close() error
}

// Stats is what Diagnostics shows about the notes store, deliberately
// nothing more specific than counts and a byte size.
type Stats struct {
	Annotations int   `json:"annotations"`
	Feedback    int   `json:"feedback"`
	Threads     int   `json:"threads"` // incidents with at least one stored turn
	Bytes       int64 `json:"bytes"`
}

// ErrHistoryDisabled is returned by AppendThread when HistoryOptIn is
// false.
var ErrHistoryDisabled = errHistoryDisabled{}

type errHistoryDisabled struct{}

func (errHistoryDisabled) Error() string {
	return "notes: history is not enabled; nothing was stored"
}
