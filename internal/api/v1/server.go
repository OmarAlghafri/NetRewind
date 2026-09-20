// Package v1 is NetRewind's versioned local API: the same record the CLI and
// the web interface already read, exposed as JSON over the local-only
// transport in internal/ipc, for a future desktop client (or any other local
// process) to use instead of opening the SQLite file directly.
//
// PRODUCT_RELEASE_PLAN_AR.md §4.1: "أنشئ API محلياً versioned (v1)... يصدر
// DTOs ولا يكشف اتصال SQLite للواجهة." A "v1" package name is the version
// contract itself: a breaking change gets a v2 package, not a modified v1,
// so an older client talking to a newer daemon degrades explicitly (a 404 on
// routes it doesn't recognise) rather than silently misreading a changed
// response shape.
package v1

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/bundle"
	"github.com/OmarAlghafri/netrewind/internal/correlate"
	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/incident"
	"github.com/OmarAlghafri/netrewind/internal/notes"
	"github.com/OmarAlghafri/netrewind/internal/registry"
	"github.com/OmarAlghafri/netrewind/internal/store"
)

// DefaultQueryWindow bounds an unqualified /v1/events or /v1/incidents
// request. Store.Filter's own default is tuned for an interactive CLI
// query; an API meant for a desktop client polling repeatedly should not
// default to scanning unbounded history on every request that forgot to
// pass "since".
const DefaultQueryWindow = time.Hour

// Server exposes a read-only view of one store and one registry over HTTP,
// meant to be served on a local-only internal/ipc listener - never on a
// network-reachable address; see internal/ipc's own doc comment for why that
// is a property of the transport, not of anything checked here. It never
// hands a caller the underlying store.Store or database handle: every
// response is JSON built from what Store.Query/QueryIncidents already
// return, the same values the CLI and internal/web already render.
type Server struct {
	Store    store.Store
	Registry *registry.Registry

	// Version, ObserverID, StorePath and StartedAt describe the daemon
	// serving this API; all optional (health reports what is set).
	Version    string
	ObserverID string
	StorePath  string
	StartedAt  time.Time
	// Rules is the loaded correlation catalogue, so a client can show what
	// the recorder is able to conclude, not only what it has concluded.
	Rules []*correlate.Rule

	// Notes is the operator's own annotations, feedback and (opt-in)
	// analysis history - a completely separate store from Store above (see
	// internal/notes's own package doc for why). Nil disables every
	// /v1/notes/* route entirely rather than serving them against nothing;
	// see registerNotesRoutes in notes.go. Every notes handler receives
	// this field's type, notes.Store, and never Store - see
	// TestNotesHandlersNeverTouchTheRecord.
	Notes notes.Store
	// NotesThreadsDisabled is an operator-level override (config's
	// notes.threads: false) that refuses every persisted follow-up thread
	// regardless of the per-installation history opt-in stored in
	// notes.db. False (the zero value) matches the default of allowing
	// threads, so existing callers that never set this field are
	// unaffected.
	NotesThreadsDisabled bool
}

// Handler returns the routed API. Every route on the record (events,
// incidents, health, capabilities, rules, bundle, what-happened) requires
// GET: this interface cannot change the record, matching internal/web's own
// read-only design (see its TestTheInterfaceIsReadOnly) for the same reason
// - a recorder's job is to observe, and an interface that could write to it
// would be a second, less-audited path to the same mistake. The one
// exception is /v1/notes/*, added below only when s.Notes is set: an
// operator's own annotations, on a completely separate store the record's
// own read-only guarantee never covered in the first place.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", s.handleHealth)
	mux.HandleFunc("GET /v1/capabilities", s.handleCapabilities)
	mux.HandleFunc("GET /v1/events", s.handleEvents)
	mux.HandleFunc("GET /v1/incidents", s.handleIncidents)
	mux.HandleFunc("GET /v1/rules", s.handleRules)
	mux.HandleFunc("GET /v1/bundle", s.handleBundle)
	mux.HandleFunc("GET /v1/what-happened", s.handleWhatHappened)
	if s.Notes != nil {
		s.registerNotesRoutes(mux)
	}
	return mux
}

// errorResponse is the one shape every failure takes, so a client can parse
// errors without knowing which route produced one.
type errorResponse struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	var resp errorResponse
	resp.Error.Code = code
	resp.Error.Message = message
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(resp)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// The header and status are already sent by the time encoding a
		// value this package built itself could fail, which would mean a
		// bug here, not a bad request - there is nothing left to tell the
		// client except by closing the connection, which returning does.
		return
	}
}

// writeJSONWithETag serves /v1/rules and /v1/capabilities: content that
// rarely changes between polls, unlike /v1/events - a client that already
// has the current catalogue should not need to re-download and re-parse
// it every few seconds just to confirm nothing moved.
//
// Standard HTTP conditional GET, not a same-body hash field: the ETag is
// SHA-256 of the exact bytes about to be sent, quoted per RFC 9110. A
// matching If-None-Match gets 304 with no body at all - a real bandwidth
// and JSON-decode saving on the client, not merely a hint it could choose
// to act on.
func writeJSONWithETag(w http.ResponseWriter, r *http.Request, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "encode_failed", err.Error())
		return
	}
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:]) + `"`
	w.Header().Set("ETag", etag)
	if inm := r.Header.Get("If-None-Match"); inm != "" && inm == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}

// parseWindow reads since/until/limit query parameters shared by every
// list endpoint, applying DefaultQueryWindow when since is not given.
func parseWindow(r *http.Request) (since, until time.Time, limit int, err error) {
	q := r.URL.Query()
	until = time.Now()
	if v := q.Get("until"); v != "" {
		until, err = time.Parse(time.RFC3339, v)
		if err != nil {
			return time.Time{}, time.Time{}, 0, errBadParam("until", "must be RFC3339")
		}
	}
	since = until.Add(-DefaultQueryWindow)
	if v := q.Get("since"); v != "" {
		since, err = time.Parse(time.RFC3339, v)
		if err != nil {
			return time.Time{}, time.Time{}, 0, errBadParam("since", "must be RFC3339")
		}
	}
	limit = store.DefaultLimit
	if v := q.Get("limit"); v != "" {
		n, convErr := strconv.Atoi(v)
		if convErr != nil || n <= 0 {
			return time.Time{}, time.Time{}, 0, errBadParam("limit", "must be a positive integer")
		}
		limit = n
	}
	return since, until, limit, nil
}

type paramError struct{ param, reason string }

func (e paramError) Error() string           { return e.param + ": " + e.reason }
func errBadParam(param, reason string) error { return paramError{param, reason} }

// parseOrder reads the order=asc|desc query parameter, defaulting to false
// (ascending) - the existing, undocumented-until-now behaviour
// (docs/api.md used to claim "newest first"; it did not, and this makes the
// actual default explicit and choosable rather than silently fixing the doc
// out from under an existing caller who may have come to depend on it).
func parseOrder(r *http.Request) (descending bool, err error) {
	switch v := r.URL.Query().Get("order"); v {
	case "", "asc":
		return false, nil
	case "desc":
		return true, nil
	default:
		return false, errBadParam("order", "must be asc or desc")
	}
}

// clampLimit enforces store.MaxLimit on the interactive list endpoints
// (events, incidents) - not shared with parseWindow itself because
// handleBundle also calls parseWindow and legitimately allows a much
// larger default (bundle.DefaultExportLimit, 50000) for what is, by
// design, a bulk export rather than a paginated interactive query.
func clampLimit(limit int) int {
	if limit > store.MaxLimit {
		return store.MaxLimit
	}
	return limit
}

// eventCursor is the wire form of store.Cursor - opaque to the client,
// versioned by field count so a future addition fails closed (extra field
// on decode) rather than silently misreading. ULIDs are base32 Crockford
// (letters and digits only), so "|" is a safe delimiter.
func encodeEventCursor(c store.Cursor) string {
	raw := fmt.Sprintf("v1|%d|%s|%d", c.TSWall, c.EventID, c.After)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeEventCursor(s string) (store.Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return store.Cursor{}, errBadParam("cursor", "not valid")
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 4 || parts[0] != "v1" {
		return store.Cursor{}, errBadParam("cursor", "not valid")
	}
	tsWall, err1 := strconv.ParseInt(parts[1], 10, 64)
	after, err2 := strconv.ParseInt(parts[3], 10, 64)
	if err1 != nil || err2 != nil || parts[2] == "" {
		return store.Cursor{}, errBadParam("cursor", "not valid")
	}
	return store.Cursor{TSWall: tsWall, EventID: parts[2], After: after}, nil
}

// nextEventCursor computes the cursor a client should present on its next
// poll to receive only what is new since this response - the maximum
// (ts_wall, event_id) among the rows just returned (regardless of the
// order they were returned in: for a delta poll's purposes, "new" means
// "past what was already seen", not "past what was displayed last"), and
// the server's own clock at query time as the fold-detection watermark
// (never the client's clock - see store.Cursor's doc on why).
//
// When no rows are returned, the keyset half of a previous cursor carries
// forward unchanged (nothing new at the row level) but the watermark still
// advances to queriedAt, so an empty poll does not force the next one to
// re-scan the gap - and the very first call (no previous cursor) has
// nothing to carry forward, so its keyset starts at the window's own lower
// bound.
func nextEventCursor(events []*event.Event, descending bool, prev *store.Cursor, since time.Time, queriedAt time.Time) store.Cursor {
	next := store.Cursor{TSWall: since.UnixNano(), After: queriedAt.UnixNano()}
	if prev != nil {
		next.TSWall, next.EventID = prev.TSWall, prev.EventID
	}
	if len(events) == 0 {
		return next
	}
	max := events[0]
	if !descending {
		max = events[len(events)-1]
	}
	if max.TSWall > next.TSWall || (max.TSWall == next.TSWall && max.ID > next.EventID) {
		next.TSWall, next.EventID = max.TSWall, max.ID
	}
	return next
}

func encodeIncidentCursor(c store.IncidentCursor) string {
	raw := fmt.Sprintf("v1|%d|%s", c.OpenedAt, c.ID)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeIncidentCursor(s string) (store.IncidentCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return store.IncidentCursor{}, errBadParam("cursor", "not valid")
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 3 || parts[0] != "v1" || parts[2] == "" {
		return store.IncidentCursor{}, errBadParam("cursor", "not valid")
	}
	openedAt, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return store.IncidentCursor{}, errBadParam("cursor", "not valid")
	}
	return store.IncidentCursor{OpenedAt: openedAt, ID: parts[2]}, nil
}

func nextIncidentCursor(incidents []*incident.Incident, prev *store.IncidentCursor, since time.Time) store.IncidentCursor {
	next := store.IncidentCursor{OpenedAt: since.UnixNano()}
	if prev != nil {
		next = *prev
	}
	for _, inc := range incidents {
		if inc.OpenedAt > next.OpenedAt || (inc.OpenedAt == next.OpenedAt && inc.ID > next.ID) {
			next.OpenedAt, next.ID = inc.OpenedAt, inc.ID
		}
	}
	return next
}

// healthResponse is what a client shows on its overview page: who is
// serving, since when, and how much record there is. "status" stays "ok"
// whenever the API answers at all - a store that cannot be counted is
// reported in store.error rather than by refusing the request, so a client
// can still tell a reachable-but-unhealthy recorder from an absent one.
type healthResponse struct {
	Status        string `json:"status"`
	Version       string `json:"version"`
	SchemaVersion int    `json:"schema_version"`
	APIVersion    int    `json:"api_version"`
	ObserverID    string `json:"observer_id"`
	StartedAt     string `json:"started_at,omitempty"`
	UptimeSeconds int64  `json:"uptime_seconds"`
	Store         struct {
		Path   string `json:"path,omitempty"`
		Events int64  `json:"events"`
		Error  string `json:"error,omitempty"`
	} `json:"store"`
	Collectors struct {
		Up          int `json:"up"`
		Down        int `json:"down"`
		Unsupported int `json:"unsupported"`
	} `json:"collectors"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	resp := healthResponse{
		Status:        "ok",
		Version:       s.Version,
		SchemaVersion: event.SchemaVersion,
		APIVersion:    1,
		ObserverID:    s.ObserverID,
	}
	if !s.StartedAt.IsZero() {
		resp.StartedAt = s.StartedAt.UTC().Format(time.RFC3339)
		resp.UptimeSeconds = int64(time.Since(s.StartedAt).Seconds())
	}
	resp.Store.Path = s.StorePath
	if s.Store != nil {
		if n, err := s.Store.CountEvents(r.Context()); err != nil {
			resp.Store.Error = err.Error()
		} else {
			resp.Store.Events = n
		}
	}
	if s.Registry != nil {
		for _, c := range s.Registry.Snapshot() {
			switch c.Status {
			case registry.StatusUp:
				resp.Collectors.Up++
			case registry.StatusDown:
				resp.Collectors.Down++
			case registry.StatusUnsupported:
				resp.Collectors.Unsupported++
			}
		}
	}
	writeJSON(w, resp)
}

// ruleSummary is the client-facing shape of one correlation rule: enough
// to explain an incident's rule_id and to list what the recorder can
// conclude, without the match clauses, which are an implementation detail
// of the engine and not something a user acts on.
type ruleSummary struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Severity   string `json:"severity"`
	Confidence uint8  `json:"confidence"`
	Window     string `json:"window"`
	RootCause  string `json:"root_cause"`
	Advice     string `json:"advice"`
	// I18n carries this rule's translated narrative text (execution order
	// §4.5 / ADR 0004), keyed by language code - absent entirely for a
	// rule with no translation yet, never a placeholder empty object.
	I18n map[string]correlate.RuleI18n `json:"i18n,omitempty"`
}

type rulesResponse struct {
	Rules []ruleSummary `json:"rules"`
}

func (s *Server) handleRules(w http.ResponseWriter, r *http.Request) {
	out := make([]ruleSummary, 0, len(s.Rules))
	for _, rule := range s.Rules {
		if rule == nil {
			continue
		}
		out = append(out, ruleSummary{
			ID:         rule.ID,
			Title:      rule.Title,
			Severity:   rule.Severity,
			Confidence: rule.Confidence,
			Window:     rule.Window.String(),
			RootCause:  rule.RootCause,
			Advice:     rule.Advice,
			I18n:       rule.I18n,
		})
	}
	writeJSONWithETag(w, r, rulesResponse{Rules: out})
}

// handleBundle streams an evidence bundle (bundle.Export's tar.gz) for the
// requested window. It is still a read of the record - nothing is written
// anywhere - which is why it stays a GET on this read-only API. Redaction
// is on unless include_secrets=true is passed, matching bundle.Export's
// own safe default; the client is expected to ask the user before passing
// it.
func (s *Server) handleBundle(w http.ResponseWriter, r *http.Request) {
	since, until, limit, err := parseWindow(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_param", err.Error())
		return
	}
	if r.URL.Query().Get("limit") == "" {
		limit = 0 // bundle.DefaultExportLimit, not the interactive query default
	}
	includeSecrets := false
	if v := r.URL.Query().Get("include_secrets"); v != "" {
		includeSecrets, err = strconv.ParseBool(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_param", "include_secrets: must be true or false")
			return
		}
	}
	var caps []registry.Snapshot
	if s.Registry != nil {
		caps = s.Registry.Snapshot()
	}
	name := fmt.Sprintf("netrewind-%s-%s.tar.gz", s.ObserverID, until.UTC().Format("20060102T150405Z"))
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	_, err = bundle.Export(r.Context(), s.Store, w, bundle.ExportOptions{
		From:           since,
		To:             until,
		AppVersion:     s.Version,
		ObserverID:     s.ObserverID,
		Capabilities:   caps,
		IncludeSecrets: includeSecrets,
		Limit:          limit,
	})
	if err != nil {
		// Headers may already be out; the archive is then truncated and its
		// checksum file absent, so an importer rejects it rather than
		// trusting a partial record.
		writeError(w, http.StatusInternalServerError, "export_failed", err.Error())
	}
}

// capabilitiesResponse wraps registry.Snapshot rather than returning the
// slice bare, so a future field (a server-level summary, a schema version)
// can be added without every existing client's JSON decoder needing to
// change from expecting an array to expecting an object.
type capabilitiesResponse struct {
	Capabilities []registry.Snapshot `json:"capabilities"`
}

func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	if s.Registry == nil {
		writeJSONWithETag(w, r, capabilitiesResponse{Capabilities: []registry.Snapshot{}})
		return
	}
	writeJSONWithETag(w, r, capabilitiesResponse{Capabilities: s.Registry.Snapshot()})
}

// NextCursor/HasMore are always present, whether or not the request itself
// used a cursor: a client's very first (Since-based) call still needs a
// cursor to present on its second call, or every poll would re-scan the
// whole window forever instead of ever switching to a delta (ADR 0005).
type eventsResponse struct {
	Events     []*event.Event `json:"events"`
	NextCursor string         `json:"next_cursor"`
	HasMore    bool           `json:"has_more"`
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	since, until, limit, err := parseWindow(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_param", err.Error())
		return
	}
	limit = clampLimit(limit)
	descending, err := parseOrder(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_param", err.Error())
		return
	}
	f := store.Filter{Since: since, Until: until, Limit: limit, Descending: descending}
	var prevCursor *store.Cursor
	if v := r.URL.Query().Get("cursor"); v != "" {
		c, err := decodeEventCursor(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_param", err.Error())
			return
		}
		f.Cursor = &c
		prevCursor = &c
	}
	if kind := r.URL.Query().Get("kind"); kind != "" {
		f.Kinds = []event.Kind{event.Kind(kind)}
	}
	if family := r.URL.Query().Get("family"); family != "" {
		f.Families = []string{family}
	}
	if subject := r.URL.Query().Get("subject"); subject != "" {
		f.SubjectLabel = subject
	}
	queriedAt := time.Now()
	events, err := s.Store.Query(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query_failed", err.Error())
		return
	}
	if events == nil {
		events = []*event.Event{}
	}
	next := nextEventCursor(events, descending, prevCursor, since, queriedAt)
	writeJSON(w, eventsResponse{
		Events:     events,
		NextCursor: encodeEventCursor(next),
		HasMore:    len(events) == limit,
	})
}

// incidentsResponse wraps the list in an object keyed "incidents" (matching
// eventsResponse's shape) rather than returning a bare JSON array - the
// cursor/has_more fields below are exactly the future addition that
// comment anticipated.
type incidentsResponse struct {
	Incidents  []*incident.Incident `json:"incidents"`
	NextCursor string               `json:"next_cursor"`
	HasMore    bool                 `json:"has_more"`
}

func (s *Server) handleIncidents(w http.ResponseWriter, r *http.Request) {
	since, until, limit, err := parseWindow(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_param", err.Error())
		return
	}
	limit = clampLimit(limit)
	f := store.IncidentFilter{Since: since, Until: until, Limit: limit}
	var prevCursor *store.IncidentCursor
	if v := r.URL.Query().Get("cursor"); v != "" {
		c, err := decodeIncidentCursor(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_param", err.Error())
			return
		}
		f.Cursor = &c
		prevCursor = &c
	}
	if rule := r.URL.Query().Get("rule"); rule != "" {
		f.RuleID = rule
	}
	if sev := r.URL.Query().Get("min_severity"); sev != "" {
		f.MinSeverity = event.Severity(sev)
	}
	incidents, err := s.Store.QueryIncidents(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query_failed", err.Error())
		return
	}
	if incidents == nil {
		incidents = []*incident.Incident{}
	}
	next := nextIncidentCursor(incidents, prevCursor, since)
	writeJSON(w, incidentsResponse{
		Incidents:  incidents,
		NextCursor: encodeIncidentCursor(next),
		HasMore:    len(incidents) == limit,
	})
}
