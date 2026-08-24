// Package web serves the recorded history over HTTP.
//
// Server-rendered HTML with no JavaScript and no build step. That is not
// austerity for its own sake: the recorder ships as one static binary, and a
// web interface that needed a bundler would either bloat the repository with
// generated assets or make the appliance depend on a toolchain it has no other
// use for. Templates are embedded, so `netrewind serve` works from the same
// single file as everything else.
//
// It is strictly read-only. The store is evidence; the thing that displays
// evidence has no business modifying it.
package web

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/incident"
	"github.com/OmarAlghafri/netrewind/internal/store"
)

//go:embed templates/*.html static/*
var assets embed.FS

// DefaultAddr is loopback on purpose. The interface has no authentication, and
// the recorder should not be reachable from the network it is watching.
const DefaultAddr = "127.0.0.1:8464"

// maxWindow bounds how much history one page may ask for. Without it a typo in
// a query string turns into a scan of the whole store.
const maxWindow = 30 * 24 * time.Hour

// Server renders the store.
type Server struct {
	st    *store.SQLite
	log   *slog.Logger
	tmpl  *template.Template
	title string
}

// New returns a server over an already-open store.
func New(st *store.SQLite, log *slog.Logger, observerID string) (*Server, error) {
	tmpl, err := template.New("").Funcs(funcs).ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("web: parse templates: %w", err)
	}
	return &Server{st: st, log: log, tmpl: tmpl, title: observerID}, nil
}

// Handler returns the routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /incidents", s.handleIncidents)
	mux.HandleFunc("GET /timeline", s.handleTimeline)
	mux.HandleFunc("GET /host", s.handleHost)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})

	if static, err := fs.Sub(assets, "static"); err == nil {
		mux.Handle("GET /static/", http.StripPrefix("/static/",
			cacheForever(http.FileServer(http.FS(static)))))
	}
	return withLogging(s.log, mux)
}

// Serve builds the HTTP server. The caller owns its lifetime.
func (s *Server) Serve(addr string) *http.Server {
	if host, _, err := net.SplitHostPort(addr); err == nil && !isLoopback(host) {
		// Said once, loudly. Nobody should discover this from a scan.
		s.log.Warn("the web interface is bound beyond loopback and has no authentication",
			"addr", addr)
	}
	return &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

/* ------------------------------------------------------------------ */
/* Pages                                                              */
/* ------------------------------------------------------------------ */

type pageData struct {
	Observer  string
	Nav       string
	Window    time.Duration
	From      time.Time
	To        time.Time
	Windows   []windowChoice
	Host      string
	Events    []*event.Event
	Incidents []*incident.Incident
	// Gaps is surfaced on every page. An empty window means nothing if the
	// recorder was not watching for it, and that has to be impossible to miss
	// rather than something the reader has to think to check.
	Gaps  []*event.Event
	Error string
}

type windowChoice struct {
	Label    string
	Value    string
	Selected bool
}

var offered = []struct {
	label string
	value string
	d     time.Duration
}{
	{"15m", "15m", 15 * time.Minute},
	{"1h", "1h", time.Hour},
	{"6h", "6h", 6 * time.Hour},
	{"24h", "24h", 24 * time.Hour},
	{"7d", "168h", 7 * 24 * time.Hour},
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "index.html", "overview")
}

func (s *Server) handleIncidents(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "incidents.html", "incidents")
}

func (s *Server) handleTimeline(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "timeline.html", "timeline")
}

func (s *Server) handleHost(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "host.html", "host")
}

// render gathers what every page needs and executes one template.
func (s *Server) render(w http.ResponseWriter, r *http.Request, name, nav string) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	window := parseWindow(r.URL.Query().Get("window"))
	to := time.Now()
	from := to.Add(-window)

	data := pageData{
		Observer: s.title,
		Nav:      nav,
		Window:   window,
		From:     from,
		To:       to,
		Host:     strings.TrimSpace(r.URL.Query().Get("q")),
		Windows:  choices(window),
	}

	if err := s.gather(ctx, &data, nav); err != nil {
		s.log.Error("could not read the store", "page", nav, "err", err)
		data.Error = err.Error()
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// The store is the source of truth and it changes constantly; a cached
	// timeline is a misleading timeline.
	w.Header().Set("Cache-Control", "no-store")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		// Too late for a status code: the response is already going out.
		s.log.Error("template failed mid-render", "template", name, "err", err)
	}
}

func (s *Server) gather(ctx context.Context, d *pageData, nav string) error {
	// Gaps first, and on every page.
	gaps, err := s.st.Query(ctx, store.Filter{
		Since: d.From, Until: d.To,
		Kinds: []event.Kind{event.KindSystemGap, event.KindSystemDrop},
		Limit: 50,
	})
	if err != nil {
		return err
	}
	d.Gaps = gaps

	switch nav {
	case "overview", "incidents":
		incidents, err := s.st.QueryIncidents(ctx, store.IncidentFilter{
			Since: d.From, Until: d.To, Limit: 200,
		})
		if err != nil {
			return err
		}
		// Newest first: the reason someone opened this page is almost always
		// the most recent thing.
		for i, j := 0, len(incidents)-1; i < j; i, j = i+1, j-1 {
			incidents[i], incidents[j] = incidents[j], incidents[i]
		}
		d.Incidents = incidents
		if nav == "incidents" {
			return nil
		}
		// The overview also shows the tail of the timeline.
		events, err := s.st.Query(ctx, store.Filter{
			Since: d.From, Until: d.To, Limit: 60, Descending: true,
		})
		if err != nil {
			return err
		}
		d.Events = reversed(events)
		return nil

	case "timeline":
		events, err := s.st.Query(ctx, store.Filter{Since: d.From, Until: d.To, Limit: 1000})
		if err != nil {
			return err
		}
		d.Events = events
		return nil

	case "host":
		if d.Host == "" {
			return nil
		}
		labels, err := s.labelsFor(ctx, d.Host, d.To)
		if err != nil {
			return err
		}
		seen := make(map[string]bool)
		var merged []*event.Event
		for _, label := range labels {
			batch, err := s.st.Query(ctx, store.Filter{
				Since: d.From, Until: d.To, SubjectLabel: label, Limit: 500,
			})
			if err != nil {
				return err
			}
			for _, e := range batch {
				if !seen[e.ID] {
					seen[e.ID] = true
					merged = append(merged, e)
				}
			}
		}
		sortByTime(merged)
		d.Events = merged
		return nil
	}
	return nil
}

// labelsFor expands a query into every address the same machine answered to,
// the same way the CLI's what-happened does. Asking about an address has to find
// the events recorded while that machine was on a different one.
func (s *Server) labelsFor(ctx context.Context, q string, at time.Time) ([]string, error) {
	labels := []string{q}
	for _, attrType := range []string{"ipv4", "ipv6", "mac", "hostname"} {
		hostID, ok, err := s.st.ResolveAt(ctx, attrType, q, at.UnixNano())
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		known, err := s.st.LabelsFor(ctx, hostID,
			at.Add(-7*24*time.Hour).UnixNano(), at.Add(24*time.Hour).UnixNano())
		if err != nil {
			return nil, err
		}
		labels = append(labels, known...)
		break
	}
	return unique(labels), nil
}

/* ------------------------------------------------------------------ */
/* Helpers                                                            */
/* ------------------------------------------------------------------ */

func parseWindow(raw string) time.Duration {
	if raw == "" {
		return time.Hour
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return time.Hour
	}
	if d > maxWindow {
		return maxWindow
	}
	return d
}

func choices(current time.Duration) []windowChoice {
	out := make([]windowChoice, 0, len(offered))
	for _, o := range offered {
		out = append(out, windowChoice{Label: o.label, Value: o.value, Selected: o.d == current})
	}
	return out
}

func reversed(in []*event.Event) []*event.Event {
	out := make([]*event.Event, len(in))
	for i, e := range in {
		out[len(in)-1-i] = e
	}
	return out
}

func sortByTime(in []*event.Event) {
	for i := 1; i < len(in); i++ {
		for j := i; j > 0 && in[j].TSWall < in[j-1].TSWall; j-- {
			in[j], in[j-1] = in[j-1], in[j]
		}
	}
}

func unique(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func isLoopback(host string) bool {
	if host == "" || host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func cacheForever(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The assets are compiled into the binary, so they cannot change
		// without the binary changing.
		w.Header().Set("Cache-Control", "public, max-age=86400")
		h.ServeHTTP(w, r)
	})
}

func withLogging(log *slog.Logger, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		h.ServeHTTP(w, r)
		log.Debug("served", "path", r.URL.Path, "query", r.URL.RawQuery,
			"took", time.Since(start).Round(time.Millisecond))
	})
}

/* ------------------------------------------------------------------ */
/* Template functions                                                 */
/* ------------------------------------------------------------------ */

var funcs = template.FuncMap{
	// describe is the same narration the CLI prints. One implementation, so a
	// screenshot of the terminal and a screenshot of the browser cannot
	// disagree about what an event means.
	"describe": event.Describe,

	"marker": func(s event.Severity) string {
		switch s {
		case event.SevError:
			return "!!"
		case event.SevWarn:
			return "!"
		case event.SevNotice:
			return "-"
		default:
			return ""
		}
	},

	"sevClass": func(s event.Severity) string {
		switch s {
		case event.SevError:
			return "sev-error"
		case event.SevWarn:
			return "sev-warn"
		case event.SevNotice:
			return "sev-notice"
		default:
			return "sev-info"
		}
	},

	"clock":    func(ns int64) string { return time.Unix(0, ns).Local().Format("15:04:05.000") },
	"datetime": func(ns int64) string { return time.Unix(0, ns).Local().Format("2006-01-02 15:04:05") },
	"hhmm":     func(t time.Time) string { return t.Local().Format("15:04:05") },

	"since": func(a, b int64) string {
		if a == 0 {
			return ""
		}
		d := time.Duration(b - a)
		if d < 0 {
			return ""
		}
		return "+" + d.Round(10*time.Millisecond).String()
	},

	"duration": func(ns int64) string { return time.Duration(ns).Round(time.Second).String() },

	// relationWords renders the claim in full. Nothing here shortens a
	// non-causal relation into an arrow: a reader who sees an arrow assumes
	// causation, and two of the three relations do not assert it.
	"relationWords": func(r incident.Relation) string {
		switch r {
		case incident.RelCauses:
			return "which caused"
		case incident.RelCorrelates:
			return "and at the same time"
		case incident.RelPrecedes:
			return "and then, without a known link"
		default:
			return ""
		}
	},

	"relationClass": func(r incident.Relation) string {
		switch r {
		case incident.RelCauses:
			return "rel-causes"
		case incident.RelCorrelates:
			return "rel-correlates"
		case incident.RelPrecedes:
			return "rel-precedes"
		default:
			return ""
		}
	},

	"attrs": func(e *event.Event) string {
		if len(e.Attrs) == 0 {
			return ""
		}
		keys := make([]string, 0, len(e.Attrs))
		for k := range e.Attrs {
			if k == "ifname" {
				continue // already the subject
			}
			keys = append(keys, k)
		}
		sortStrings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, k+"="+fmt.Sprint(e.Attrs[k]))
		}
		return strings.Join(parts, "  ")
	},

	"plural": func(n int, one, many string) string {
		if n == 1 {
			return strconv.Itoa(n) + " " + one
		}
		return strconv.Itoa(n) + " " + many
	},

	"qwindow": func(d time.Duration) string { return d.String() },

	"add":   func(a, b int) int { return a + b },
	"int64": func(i int) int64 { return int64(i) },

	// row pairs an event with how long after the previous one it arrived. The
	// elapsed column is what makes a list of timestamps readable as a
	// sequence, and computing it in the template would need arithmetic the
	// template language does not have.
	"row": func(e *event.Event, prev int64) rowData {
		gap := ""
		if prev != 0 && e.TSWall >= prev {
			gap = "+" + time.Duration(e.TSWall-prev).Round(10*time.Millisecond).String()
		}
		return rowData{E: e, Since: gap}
	},
}

func sortStrings(in []string) {
	for i := 1; i < len(in); i++ {
		for j := i; j > 0 && in[j] < in[j-1]; j-- {
			in[j], in[j-1] = in[j-1], in[j]
		}
	}
}

// rowData is one line of the timeline: the event, and how long after the
// previous one it arrived.
type rowData struct {
	E     *event.Event
	Since string
}
