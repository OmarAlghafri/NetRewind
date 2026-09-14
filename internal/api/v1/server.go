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
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/incident"
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
}

// Handler returns the routed API. Every route requires GET: this interface
// cannot change the record, matching internal/web's own read-only design
// (see its TestTheInterfaceIsReadOnly) for the same reason - a recorder's
// job is to observe, and an interface that could write to it would be a
// second, less-audited path to the same mistake.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", s.handleHealth)
	mux.HandleFunc("GET /v1/capabilities", s.handleCapabilities)
	mux.HandleFunc("GET /v1/events", s.handleEvents)
	mux.HandleFunc("GET /v1/incidents", s.handleIncidents)
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

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, struct {
		Status string `json:"status"`
	}{Status: "ok"})
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
		writeJSON(w, capabilitiesResponse{Capabilities: []registry.Snapshot{}})
		return
	}
	writeJSON(w, capabilitiesResponse{Capabilities: s.Registry.Snapshot()})
}

type eventsResponse struct {
	Events []*event.Event `json:"events"`
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	since, until, limit, err := parseWindow(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_param", err.Error())
		return
	}
	f := store.Filter{Since: since, Until: until, Limit: limit}
	if kind := r.URL.Query().Get("kind"); kind != "" {
		f.Kinds = []event.Kind{event.Kind(kind)}
	}
	if family := r.URL.Query().Get("family"); family != "" {
		f.Families = []string{family}
	}
	if subject := r.URL.Query().Get("subject"); subject != "" {
		f.SubjectLabel = subject
	}
	events, err := s.Store.Query(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query_failed", err.Error())
		return
	}
	if events == nil {
		events = []*event.Event{}
	}
	writeJSON(w, eventsResponse{Events: events})
}

// incidentsResponse wraps the list in an object keyed "incidents" (matching
// eventsResponse's shape) rather than returning a bare JSON array, so a
// future top-level field - a cursor, a server-side summary - can be added
// without an existing client's decoder needing to change from expecting an
// array to expecting an object.
type incidentsResponse struct {
	Incidents []*incident.Incident `json:"incidents"`
}

func (s *Server) handleIncidents(w http.ResponseWriter, r *http.Request) {
	since, until, limit, err := parseWindow(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_param", err.Error())
		return
	}
	f := store.IncidentFilter{Since: since, Until: until, Limit: limit}
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
	writeJSON(w, incidentsResponse{Incidents: incidents})
}
