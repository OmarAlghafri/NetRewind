package metrics

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/incident"
	"github.com/OmarAlghafri/netrewind/internal/listen"
)

// Metric names. The prefix is fixed so a scrape can be attributed at a glance.
const (
	EventsTotal    = "netrewind_events_total"
	IncidentsTotal = "netrewind_incidents_total"
	BlindSeconds   = "netrewind_recorder_blind_seconds_total"
	DroppedTotal   = "netrewind_dropped_events_total"
	ClockSteps     = "netrewind_clock_steps_total"
	CollectorUp    = "netrewind_collector_up"
	StoredEvents   = "netrewind_stored_events"
	BuildInfo      = "netrewind_build_info"
)

// Recorder turns the event stream into metrics.
//
// The interesting ones are not the counts of what was seen. Every observability
// tool exports those. These export what the recorder *missed* -
// netrewind_recorder_blind_seconds_total and netrewind_dropped_events_total -
// so an operator can alert on the recorder being untrustworthy for a period
// instead of discovering it during the incident review, when it is too late to
// do anything about it.
type Recorder struct {
	reg *Registry
}

// NewRecorder returns a Recorder with every series declared at zero.
func NewRecorder(version, observerID string) *Recorder {
	reg := New()
	reg.Declare(BuildInfo, Gauge, "Version of the running recorder.")
	reg.Declare(EventsTotal, Counter, "Events recorded, by kind and severity.")
	reg.Declare(IncidentsTotal, Counter, "Incidents concluded, by rule and severity.")
	reg.Declare(BlindSeconds, Counter,
		"Seconds the recorder was not watching. Any increase means the record has a hole in it.")
	reg.Declare(DroppedTotal, Counter,
		"Events lost because they arrived faster than they could be read.")
	reg.Declare(ClockSteps, Counter,
		"Wall-clock jumps observed. Each one reorders the timeline for anyone reading it later.")
	reg.Declare(CollectorUp, Gauge, "1 while a collector is running, 0 once it has stopped.")
	reg.Declare(StoredEvents, Gauge, "Events currently held in the store.")

	reg.Set(BuildInfo, Labels{"version": version, "observer": observerID}, 1)

	// Create the series, not just the type declaration.
	//
	// A HELP and TYPE line with no sample under it is invisible to a scraper:
	// the series does not exist until something goes wrong, and a series that
	// springs into existence on the first failure cannot be alerted on before
	// it. These are precisely the metrics an operator most needs to have been
	// watching all along, so they start at zero.
	reg.Add(BlindSeconds, nil, 0)
	reg.Add(ClockSteps, nil, 0)
	reg.Add(DroppedTotal, Labels{"source": "ringbuf"}, 0)

	return &Recorder{reg: reg}
}

// Registry exposes the underlying registry, for the HTTP handler and tests.
func (r *Recorder) Registry() *Registry { return r.reg }

// Observe folds one event into the metrics.
func (r *Recorder) Observe(e *event.Event) {
	// Count is used rather than 1: a folded event stands for several
	// occurrences, and a metric that counted rows instead of occurrences would
	// under-report exactly when things are worst.
	n := float64(e.Count)
	if n < 1 {
		n = 1
	}
	r.reg.Add(EventsTotal, Labels{"kind": string(e.Kind), "severity": string(e.Severity)}, n)

	switch e.Kind {
	case event.KindSystemGap:
		if ms, ok := event.Int(e, "gap_duration_ms"); ok {
			r.reg.Add(BlindSeconds, nil, float64(ms)/1000)
		}
	case event.KindSystemDrop:
		source := event.Str(e, "source")
		if source == "" {
			source = "unknown"
		}
		if dropped, ok := event.Int(e, "dropped"); ok {
			r.reg.Add(DroppedTotal, Labels{"source": source}, float64(dropped))
		}
	case event.KindSystemClockStep:
		r.reg.Inc(ClockSteps, nil)
	}
}

// ObserveIncident folds one concluded incident into the metrics.
func (r *Recorder) ObserveIncident(inc *incident.Incident) {
	r.reg.Inc(IncidentsTotal, Labels{"rule": inc.RuleID, "severity": string(inc.Severity)})
}

// SetCollector records whether a collector is running.
func (r *Recorder) SetCollector(name string, up bool) {
	v := 0.0
	if up {
		v = 1
	}
	r.reg.Set(CollectorUp, Labels{"collector": name}, v)
}

// SetStoredEvents records how much history is currently held.
func (r *Recorder) SetStoredEvents(n int64) {
	r.reg.Set(StoredEvents, nil, float64(n))
}

// Handler serves the registry over HTTP.
func (r *Recorder) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		if _, err := r.reg.WriteTo(w); err != nil {
			return // the scraper went away mid-write; nothing useful to do
		}
	})
}

// Serve runs the metrics endpoint until the server is closed by the caller.
//
// It listens on its own address rather than sharing one with anything else: the
// recorder has no other network surface, and giving it one that could be
// reached from the network it is watching would be a poor trade.
func (r *Recorder) Serve(addr string, log *slog.Logger) (*http.Server, error) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", r.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})

	// The endpoint says how much the recorder saw, of what kind, and which of
	// its sources are alive. That is a description of the watched network, and
	// there is no authentication on it.
	listen.WarnIfExposed(log, addr, "metrics endpoint")

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	return srv, nil
}
