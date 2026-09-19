package v1

import (
	"context"
	"net/http"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/identity"
	"github.com/OmarAlghafri/netrewind/internal/incident"
	"github.com/OmarAlghafri/netrewind/internal/store"
)

// whatHappenedLimit mirrors cmd/netrewind/timeline.go's own hard-coded
// per-query cap exactly, for the same reason it exists there: a bound on
// how much one identity label or the always-included system window can
// return, not a client-tunable page size. This endpoint answers one
// bounded "what happened around this moment" question - it is not a live
// feed, and does not take a limit/cursor param the way /v1/events does
// (execution order §4.4: this is investigation parity with the CLI, not a
// new pagination surface).
const whatHappenedLimit = 2000

// whatHappenedIncidentLimit is generous on purpose: incidents are rare
// relative to events, and missing one inside the requested window would
// silently misrepresent what the record actually concluded.
const whatHappenedIncidentLimit = 500

// whatHappenedResponse mirrors what cmd/netrewind/timeline.go's
// what-happened prints, structured instead of rendered as text: the
// window actually used, every label the host resolved to (so a client can
// show "also answered to: ..." the way the CLI does), and the deduplicated
// events and incidents in it.
type whatHappenedResponse struct {
	From           string          `json:"from"`
	To             string          `json:"to"`
	ObservedLabels []string        `json:"observed_labels,omitempty"`
	Events         []*event.Event  `json:"events"`
	Incidents      []*incidentJSON `json:"incidents"`
}

// handleWhatHappened answers exactly what `netrewind what-happened
// --host <addr> --at <time> --window <dur>` answers, over the API instead
// of the store file directly - the GUI's Investigation page (execution
// order Phase 3) is required to match this output for identical inputs,
// not approximate it.
//
// host is optional (an empty host searches the whole window, matching the
// CLI's own behaviour when no --host is given); at defaults to now; window
// defaults to 5 minutes, the same default cmd/netrewind/timeline.go uses.
func (s *Server) handleWhatHappened(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	at := time.Now()
	if v := q.Get("at"); v != "" {
		parsed, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_param", "at: must be RFC3339")
			return
		}
		at = parsed
	}

	window := 5 * time.Minute
	if v := q.Get("window"); v != "" {
		parsed, err := time.ParseDuration(v)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "bad_param", "window: must be a positive duration (e.g. 5m)")
			return
		}
		window = parsed
	}

	from, to := at.Add(-window), at.Add(window)
	host := q.Get("host")

	labels, err := s.labelsToSearch(r.Context(), host, at)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query_failed", err.Error())
		return
	}

	var found []*event.Event
	if len(labels) == 0 {
		events, err := s.Store.Query(r.Context(), store.Filter{Since: from, Until: to, Limit: whatHappenedLimit})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "query_failed", err.Error())
			return
		}
		found = events
	} else {
		for _, label := range labels {
			events, err := s.Store.Query(r.Context(), store.Filter{
				Since: from, Until: to, SubjectLabel: label, Limit: whatHappenedLimit,
			})
			if err != nil {
				writeError(w, http.StatusInternalServerError, "query_failed", err.Error())
				return
			}
			found = append(found, events...)
		}
	}

	// A blind spot is never silently absent, whatever else the question
	// asked - the same reasoning cmd/netrewind/timeline.go states for why
	// it always folds in the system family regardless of the host filter.
	systemEvents, err := s.Store.Query(r.Context(), store.Filter{
		Since: from, Until: to, Families: []string{"system"}, Limit: whatHappenedIncidentLimit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query_failed", err.Error())
		return
	}
	found = append(found, systemEvents...)
	found = dedupeEvents(found)
	if found == nil {
		found = []*event.Event{}
	}

	incidents, err := s.Store.QueryIncidents(r.Context(), store.IncidentFilter{Since: from, Until: to, Limit: whatHappenedIncidentLimit})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query_failed", err.Error())
		return
	}
	incidentsOut := make([]*incidentJSON, 0, len(incidents))
	for _, inc := range incidents {
		incidentsOut = append(incidentsOut, (*incidentJSON)(inc))
	}

	observed := labels
	if host != "" {
		// The CLI reports what else the host answered to, not the query
		// label itself, which is always the first entry (labelsToSearch
		// seeds it that way) - see renderTimeline's "also answered to" line.
		observed = removeFirst(labels, host)
	}

	writeJSON(w, whatHappenedResponse{
		From:           from.UTC().Format(time.RFC3339),
		To:             to.UTC().Format(time.RFC3339),
		ObservedLabels: observed,
		Events:         found,
		Incidents:      incidentsOut,
	})
}

// incidentJSON exists only so whatHappenedResponse can name its field
// "incidents" with the exact same JSON shape incident.Incident already
// produces - a type alias would work too, but a defined type here keeps
// this file's import of "incident" implicit and obvious at the call site
// instead of needing its own import line for a one-field usage.
type incidentJSON = incident.Incident

// labelsToSearch is internal/api/v1's copy of cmd/netrewind/timeline.go's
// function of the same name and behaviour - deliberately not shared code:
// promoting store.Store to expose ResolveAt/LabelsFor (this commit) is
// already the real shared surface; the six lines of loop logic on top of
// it are small enough that a shared package for them would be more
// indirection than the duplication it removes. If this logic changes, it
// must change in both places, and a difference between them is exactly
// what docs/api.md's parity claim - and Phase 3's E2E test comparing this
// endpoint's output to the CLI's - exists to catch.
func (s *Server) labelsToSearch(ctx context.Context, host string, at time.Time) ([]string, error) {
	if host == "" {
		return nil, nil
	}
	labels := []string{host}
	for _, attrType := range []string{identity.AttrIPv4, identity.AttrIPv6, identity.AttrMAC, identity.AttrHostname} {
		hostID, ok, err := s.Store.ResolveAt(ctx, attrType, host, at.UnixNano())
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		known, err := s.Store.LabelsFor(ctx, hostID, at.Add(-24*time.Hour).UnixNano(), at.Add(24*time.Hour).UnixNano())
		if err != nil {
			return nil, err
		}
		labels = append(labels, known...)
		break
	}
	return uniqueStrings(labels), nil
}

func dedupeEvents(events []*event.Event) []*event.Event {
	seen := make(map[string]bool, len(events))
	out := events[:0]
	for _, e := range events {
		if seen[e.ID] {
			continue
		}
		seen[e.ID] = true
		out = append(out, e)
	}
	return out
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func removeFirst(values []string, drop string) []string {
	out := make([]string, 0, len(values))
	skipped := false
	for _, v := range values {
		if !skipped && v == drop {
			skipped = true
			continue
		}
		out = append(out, v)
	}
	return out
}
