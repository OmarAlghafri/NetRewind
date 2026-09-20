package v1

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/ai"
	"github.com/OmarAlghafri/netrewind/internal/notes"
)

// notesMaxBody bounds every /v1/notes request body - an operator note is a
// few sentences, not a place to smuggle an unbounded payload through the
// one write surface this API has.
const notesMaxBody = 64 * 1024

// registerNotesRoutes is called from Handler() only when s.Notes is set,
// so a daemon with notes disabled (or an older build without this file at
// all) never exposes the path. Every handler here takes s.Notes
// (notes.Store) and NEVER s.Store (store.Store) - see
// TestNotesHandlersNeverTouchTheRecord, which pins that a request here
// cannot reach the record no matter what it asks for.
func (s *Server) registerNotesRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/notes/incidents/{id}", s.handleNotesGet)
	mux.HandleFunc("PUT /v1/notes/incidents/{id}", s.handleNotesPut)
	mux.HandleFunc("DELETE /v1/notes/incidents/{id}", s.handleNotesDeleteOne)
	mux.HandleFunc("GET /v1/notes/similar", s.handleNotesSimilar)
	mux.HandleFunc("POST /v1/notes/feedback", s.handleNotesFeedback)
	mux.HandleFunc("GET /v1/notes/threads/{id}", s.handleNotesThreadsGet)
	mux.HandleFunc("POST /v1/notes/threads/{id}", s.handleNotesThreadsAppend)
	mux.HandleFunc("GET /v1/notes/settings", s.handleNotesSettingsGet)
	mux.HandleFunc("PUT /v1/notes/settings", s.handleNotesSettingsPut)
	mux.HandleFunc("DELETE /v1/notes", s.handleNotesForgetAll)
}

func (s *Server) handleNotesGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a, err := s.Notes.GetAnnotation(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "notes_read_failed", err.Error())
		return
	}
	if a == nil {
		writeError(w, http.StatusNotFound, "not_found", "no note for this incident")
		return
	}
	writeJSON(w, a)
}

// notesPutRequest is the body PUT /v1/notes/incidents/{id} accepts. The
// client supplies rule_id/root_cause_kind/root_cause_entity/opened_at_ns
// directly (it already has the incident from the read-only record API) -
// this handler never queries store.Store to look them up, which is exactly
// what keeps it able to accept a notes.Store and nothing else.
type notesPutRequest struct {
	RuleID          string        `json:"rule_id"`
	RootCauseKind   string        `json:"root_cause_kind"`
	RootCauseEntity string        `json:"root_cause_entity"`
	OpenedAtNS      int64         `json:"opened_at_ns"`
	Outcome         notes.Outcome `json:"outcome"`
	CauseNote       string        `json:"cause_note,omitempty"`
	ResolutionNote  string        `json:"resolution_note,omitempty"`
}

func (s *Server) handleNotesPut(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req notesPutRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.RuleID == "" || req.RootCauseKind == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "rule_id and root_cause_kind are required")
		return
	}
	if !req.Outcome.Valid() {
		writeError(w, http.StatusBadRequest, "bad_request", "outcome must be confirmed, false_positive or unresolved")
		return
	}
	a := notes.Annotation{
		IncidentID:      id,
		Fingerprint:     ai.Fingerprint(req.RuleID, req.RootCauseKind, req.RootCauseEntity),
		RuleID:          req.RuleID,
		RootCauseKind:   req.RootCauseKind,
		RootCauseEntity: req.RootCauseEntity,
		OpenedAtNS:      req.OpenedAtNS,
		Outcome:         req.Outcome,
		CauseNote:       req.CauseNote,
		ResolutionNote:  req.ResolutionNote,
	}
	if err := s.Notes.PutAnnotation(r.Context(), a); err != nil {
		writeError(w, http.StatusInternalServerError, "notes_write_failed", err.Error())
		return
	}
	got, err := s.Notes.GetAnnotation(r.Context(), id)
	if err != nil || got == nil {
		writeError(w, http.StatusInternalServerError, "notes_write_failed", "wrote the note but could not read it back")
		return
	}
	writeJSON(w, got)
}

func (s *Server) handleNotesDeleteOne(w http.ResponseWriter, r *http.Request) {
	if err := s.Notes.DeleteAnnotation(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, http.StatusInternalServerError, "notes_write_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleNotesSimilar(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ruleID, kind, entity, exclude := q.Get("rule_id"), q.Get("root_cause_kind"), q.Get("entity"), q.Get("exclude")
	if ruleID == "" || kind == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "rule_id and root_cause_kind are required")
		return
	}
	limit := 3
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "bad_request", "limit must be a positive integer")
			return
		}
		limit = n
	}
	got, err := s.Notes.Similar(r.Context(), ruleID, kind, entity, exclude, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "notes_read_failed", err.Error())
		return
	}
	if got == nil {
		got = []notes.Annotation{}
	}
	writeJSON(w, got)
}

type notesFeedbackRequest struct {
	AnswerID   string `json:"answer_id"`
	IncidentID string `json:"incident_id"`
	Profile    string `json:"profile"`
	ModelID    string `json:"model_id"`
	Helpful    bool   `json:"helpful"`
	RuleID     string `json:"rule_id"`
	RootCause  string `json:"root_cause_kind"`
	Entity     string `json:"root_cause_entity"`
}

func (s *Server) handleNotesFeedback(w http.ResponseWriter, r *http.Request) {
	var req notesFeedbackRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.AnswerID == "" || req.IncidentID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "answer_id and incident_id are required")
		return
	}
	f := notes.Feedback{
		AnswerID:    req.AnswerID,
		IncidentID:  req.IncidentID,
		Fingerprint: ai.Fingerprint(req.RuleID, req.RootCause, req.Entity),
		Profile:     req.Profile,
		ModelID:     req.ModelID,
		Helpful:     req.Helpful,
		AtMS:        time.Now().UnixMilli(),
	}
	if err := s.Notes.PutFeedback(r.Context(), f); err != nil {
		writeError(w, http.StatusInternalServerError, "notes_write_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleNotesThreadsGet(w http.ResponseWriter, r *http.Request) {
	got, err := s.Notes.GetThread(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "notes_read_failed", err.Error())
		return
	}
	if got == nil {
		got = []notes.ThreadTurn{}
	}
	writeJSON(w, got)
}

type notesThreadAppendRequest struct {
	AnswerID         string `json:"answer_id"`
	QuestionRedacted string `json:"question_redacted"`
	SummaryRedacted  string `json:"summary_redacted"`
}

func (s *Server) handleNotesThreadsAppend(w http.ResponseWriter, r *http.Request) {
	if s.NotesThreadsDisabled {
		writeError(w, http.StatusConflict, "history_disabled", "an operator policy on this recorder disables persisted analysis threads")
		return
	}
	var req notesThreadAppendRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	turn := notes.ThreadTurn{
		AtMS: time.Now().UnixMilli(), AnswerID: req.AnswerID,
		QuestionRedacted: req.QuestionRedacted, SummaryRedacted: req.SummaryRedacted,
	}
	if err := s.Notes.AppendThread(r.Context(), r.PathValue("id"), turn); err != nil {
		if err == notes.ErrHistoryDisabled {
			writeError(w, http.StatusConflict, "history_disabled", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "notes_write_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleNotesSettingsGet(w http.ResponseWriter, r *http.Request) {
	on, err := s.Notes.HistoryOptIn(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "notes_read_failed", err.Error())
		return
	}
	writeJSON(w, struct {
		HistoryOptIn bool `json:"history_opt_in"`
	}{on})
}

func (s *Server) handleNotesSettingsPut(w http.ResponseWriter, r *http.Request) {
	var req struct {
		HistoryOptIn bool `json:"history_opt_in"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if err := s.Notes.SetHistoryOptIn(r.Context(), req.HistoryOptIn); err != nil {
		writeError(w, http.StatusInternalServerError, "notes_write_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleNotesForgetAll(w http.ResponseWriter, r *http.Request) {
	if err := s.Notes.ForgetAll(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "notes_write_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// decodeJSONBody enforces notesMaxBody and writes a 400 on any decode
// failure, returning false so the caller returns immediately without a
// second response write.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, notesMaxBody)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid or oversized JSON body: "+err.Error())
		return false
	}
	return true
}
