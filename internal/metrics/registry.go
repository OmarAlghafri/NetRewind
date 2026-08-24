// Package metrics exposes what the recorder has seen - and, more usefully, what
// it has missed - in the Prometheus text format.
//
// There is no client library here on purpose. The exposition format is a dozen
// lines of text, the recorder ships as one static binary with no runtime
// dependencies, and a monitoring integration is not worth giving that up for.
package metrics

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
)

// Kind is what a series means to a scraper.
type Kind string

const (
	// Counter only ever goes up, or resets to zero on restart.
	Counter Kind = "counter"
	// Gauge can go up and down.
	Gauge Kind = "gauge"
)

// Labels are the dimensions of one series.
type Labels map[string]string

// Registry holds the series a process exposes.
//
// It is deliberately tiny: counters and gauges with string labels, nothing
// else. Histograms would be the next thing to want, and the moment they are
// genuinely needed is the moment to reconsider taking the dependency.
type Registry struct {
	mu       sync.RWMutex
	families map[string]*family
	order    []string
}

type family struct {
	name   string
	kind   Kind
	help   string
	series map[string]*point
}

type point struct {
	labels Labels
	value  float64
}

// New returns an empty registry.
func New() *Registry {
	return &Registry{families: make(map[string]*family)}
}

// Declare registers a metric name with its type and description.
//
// Declaring up front means a metric exists at zero rather than appearing only
// once something has gone wrong. A series that springs into existence on the
// first failure cannot be alerted on before it, which is exactly backwards.
func (r *Registry) Declare(name string, kind Kind, help string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.families[name]; exists {
		return
	}
	r.families[name] = &family{name: name, kind: kind, help: help, series: make(map[string]*point)}
	r.order = append(r.order, name)
}

// Add increases a counter, creating the series if this is its first sighting.
func (r *Registry) Add(name string, labels Labels, delta float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p := r.pointLocked(name, labels); p != nil {
		p.value += delta
	}
}

// Inc adds one.
func (r *Registry) Inc(name string, labels Labels) { r.Add(name, labels, 1) }

// Set replaces a gauge's value.
func (r *Registry) Set(name string, labels Labels, value float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p := r.pointLocked(name, labels); p != nil {
		p.value = value
	}
}

// Value reads a series back, for tests and for the CLI's own reporting.
func (r *Registry) Value(name string, labels Labels) (float64, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, ok := r.families[name]
	if !ok {
		return 0, false
	}
	p, ok := f.series[seriesKey(labels)]
	if !ok {
		return 0, false
	}
	return p.value, true
}

func (r *Registry) pointLocked(name string, labels Labels) *point {
	f, ok := r.families[name]
	if !ok {
		return nil // an undeclared metric is a bug, not a runtime condition
	}
	key := seriesKey(labels)
	p, ok := f.series[key]
	if !ok {
		p = &point{labels: labels}
		f.series[key] = p
	}
	return p
}

// WriteTo renders the registry in the Prometheus text exposition format.
func (r *Registry) WriteTo(w io.Writer) (int64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var b strings.Builder
	for _, name := range r.order {
		f := r.families[name]
		if f.help != "" {
			fmt.Fprintf(&b, "# HELP %s %s\n", f.name, escapeHelp(f.help))
		}
		fmt.Fprintf(&b, "# TYPE %s %s\n", f.name, f.kind)

		keys := make([]string, 0, len(f.series))
		for k := range f.series {
			keys = append(keys, k)
		}
		// Stable output: two scrapes of an unchanged registry are identical,
		// which makes diffs and tests mean something.
		sort.Strings(keys)

		for _, k := range keys {
			p := f.series[k]
			fmt.Fprintf(&b, "%s%s %s\n", f.name, renderLabels(p.labels), formatValue(p.value))
		}
	}
	n, err := io.WriteString(w, b.String())
	return int64(n), err
}

// seriesKey identifies a label set independently of map iteration order.
func seriesKey(labels Labels) string {
	if len(labels) == 0 {
		return ""
	}
	names := make([]string, 0, len(labels))
	for k := range labels {
		names = append(names, k)
	}
	sort.Strings(names)

	var b strings.Builder
	for i, k := range names {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(labels[k])
	}
	return b.String()
}

func renderLabels(labels Labels) string {
	if len(labels) == 0 {
		return ""
	}
	names := make([]string, 0, len(labels))
	for k := range labels {
		names = append(names, k)
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteByte('{')
	for i, k := range names {
		if i > 0 {
			b.WriteByte(',')
		}
		// The quotes are written by hand rather than with %q. A Go quoted
		// string also escapes every non-ASCII rune, which would turn a hostname
		// in Arabic or any other script into a row of \u sequences - and the
		// exposition format is UTF-8, so only the three characters below
		// actually need escaping.
		fmt.Fprintf(&b, "%s=\"%s\"", k, escapeLabel(labels[k]))
	}
	b.WriteByte('}')
	return b.String()
}

// escapeLabel escapes the three characters the format reserves inside a label
// value. Event kinds and rule ids are safe, but a subject label comes off the
// wire and must never be able to forge a series.
func escapeLabel(v string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(v)
}

func escapeHelp(v string) string {
	return strings.NewReplacer(`\`, `\\`, "\n", `\n`).Replace(v)
}

// formatValue renders without an exponent for whole numbers, which is what
// counters almost always are and what a human reading a scrape expects.
func formatValue(v float64) string {
	if v == float64(int64(v)) {
		return fmt.Sprintf("%d", int64(v))
	}
	return fmt.Sprintf("%g", v)
}
