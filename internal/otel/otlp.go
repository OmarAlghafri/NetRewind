// Package otel exports the record to an OpenTelemetry collector.
//
// OTLP over HTTP with JSON bodies, encoded by hand. There is no OpenTelemetry
// SDK here for the same reason there is no Prometheus client library: the
// recorder ships as one static binary with no runtime dependencies, and the
// SDK, its protobuf runtime and their transitive dependencies are a large price
// for a wire format that is a few hundred lines of JSON. The encoding is
// specified and stable, and what is produced here is checked against the
// specification by tests rather than by trusting a library to be right.
//
// Events are exported as **logs**, not metrics. Metrics are already served on
// the Prometheus endpoint, and they answer "how much" - but the thing worth
// getting into somebody else's observability stack is the record itself, so an
// application incident can be lined up against what the network did underneath
// it. A log record carries the narration, the kind and every attribute, which
// is enough to ask that question in whatever the team already uses.
package otel

import (
	"encoding/json"
	"strconv"

	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/incident"
)

// Severity numbers from the OpenTelemetry logs data model. The scale has four
// steps per level; NetRewind's four severities map onto the first of each,
// except notice, which the model has no name for and which sits between info
// and warn.
const (
	sevInfo   = 9
	sevNotice = 11
	sevWarn   = 13
	sevError  = 17
)

func severityNumber(s event.Severity) int {
	switch s {
	case event.SevError:
		return sevError
	case event.SevWarn:
		return sevWarn
	case event.SevNotice:
		return sevNotice
	default:
		return sevInfo
	}
}

/* ------------------------------------------------------------------ */
/* The wire shape                                                     */
/* ------------------------------------------------------------------ */

type payload struct {
	ResourceLogs []resourceLogs `json:"resourceLogs"`
}

type resourceLogs struct {
	Resource  resource    `json:"resource"`
	ScopeLogs []scopeLogs `json:"scopeLogs"`
}

type resource struct {
	Attributes []keyValue `json:"attributes"`
}

type scopeLogs struct {
	Scope      scope       `json:"scope"`
	LogRecords []logRecord `json:"logRecords"`
}

type scope struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type logRecord struct {
	// Strings, not numbers: the field is a uint64 in the specification, and
	// JSON numbers are float64 in most decoders, which would round a
	// nanosecond timestamp. The specification allows a decimal string for
	// exactly this reason.
	TimeUnixNano         string     `json:"timeUnixNano"`
	ObservedTimeUnixNano string     `json:"observedTimeUnixNano"`
	SeverityNumber       int        `json:"severityNumber"`
	SeverityText         string     `json:"severityText"`
	Body                 anyValue   `json:"body"`
	Attributes           []keyValue `json:"attributes"`
}

type keyValue struct {
	Key   string   `json:"key"`
	Value anyValue `json:"value"`
}

// anyValue is OTLP's tagged union. Only the field that applies is emitted.
type anyValue struct {
	String *string     `json:"stringValue,omitempty"`
	Bool   *bool       `json:"boolValue,omitempty"`
	Int    *string     `json:"intValue,omitempty"`
	Double *float64    `json:"doubleValue,omitempty"`
	Array  *arrayValue `json:"arrayValue,omitempty"`
}

type arrayValue struct {
	Values []anyValue `json:"values"`
}

func str(s string) anyValue  { return anyValue{String: &s} }
func b(v bool) anyValue      { return anyValue{Bool: &v} }
func i64(v int64) anyValue   { s := strconv.FormatInt(v, 10); return anyValue{Int: &s} }
func f64(v float64) anyValue { return anyValue{Double: &v} }

// value converts an attribute the collectors produced into OTLP's union.
//
// Attributes arrive as Go values from a collector and as json.Number or string
// after a round trip through the store, and both have to land on the same wire
// type or a query that works on live data breaks on replayed data.
func value(v any) anyValue {
	switch t := v.(type) {
	case nil:
		return str("")
	case string:
		return str(t)
	case bool:
		return b(t)
	case int:
		return i64(int64(t))
	case int32:
		return i64(int64(t))
	case int64:
		return i64(t)
	case uint32:
		return i64(int64(t))
	case uint64:
		return i64(int64(t))
	case float32:
		return f64(float64(t))
	case float64:
		// A whole number that arrived as a float is still a count, and
		// exporting 3 as 3.0 makes it awkward to match against the same
		// attribute from a live collector.
		if t == float64(int64(t)) {
			return i64(int64(t))
		}
		return f64(t)
	case json.Number:
		if n, err := t.Int64(); err == nil {
			return i64(n)
		}
		if f, err := t.Float64(); err == nil {
			return f64(f)
		}
		return str(t.String())
	case []string:
		vals := make([]anyValue, len(t))
		for i, s := range t {
			vals[i] = str(s)
		}
		return anyValue{Array: &arrayValue{Values: vals}}
	default:
		// Anything else is rendered rather than dropped. A missing attribute
		// is a question that cannot be asked later; an awkwardly typed one is
		// merely awkward.
		bytes, err := json.Marshal(t)
		if err != nil {
			return str("")
		}
		return str(string(bytes))
	}
}

/* ------------------------------------------------------------------ */
/* Encoding                                                           */
/* ------------------------------------------------------------------ */

// ScopeName identifies this producer in the receiving pipeline.
const ScopeName = "github.com/OmarAlghafri/netrewind"

// Encode turns a batch of events and incidents into one OTLP logs payload.
//
// Incidents travel alongside the events rather than instead of them. They are
// the conclusion, and a conclusion whose evidence is in a different system is
// most of the way back to the problem this project exists to solve.
func Encode(observerID, version string, events []*event.Event, incidents []*incident.Incident) ([]byte, error) {
	records := make([]logRecord, 0, len(events)+len(incidents))
	for _, e := range events {
		records = append(records, eventRecord(e))
	}
	for _, inc := range incidents {
		records = append(records, incidentRecord(inc))
	}

	doc := payload{
		ResourceLogs: []resourceLogs{{
			Resource: resource{Attributes: []keyValue{
				// service.name is what almost every backend groups by, so it
				// has to be the thing an operator would search for.
				{Key: "service.name", Value: str("netrewind")},
				{Key: "service.version", Value: str(version)},
				{Key: "host.name", Value: str(observerID)},
			}},
			ScopeLogs: []scopeLogs{{
				Scope:      scope{Name: ScopeName, Version: version},
				LogRecords: records,
			}},
		}},
	}
	return json.Marshal(doc)
}

func eventRecord(e *event.Event) logRecord {
	attrs := []keyValue{
		{Key: "netrewind.event_id", Value: str(e.ID)},
		{Key: "netrewind.kind", Value: str(string(e.Kind))},
		{Key: "netrewind.family", Value: str(e.Kind.Family())},
		{Key: "netrewind.source", Value: str(string(e.Source))},
		{Key: "netrewind.confidence", Value: i64(int64(e.Confidence))},
		{Key: "netrewind.subject.kind", Value: str(string(e.Subject.Kind))},
		{Key: "netrewind.subject.label", Value: str(e.Subject.Label)},
	}
	if e.Subject.ID != "" {
		attrs = append(attrs, keyValue{Key: "netrewind.subject.id", Value: str(e.Subject.ID)})
	}
	// A folded event stands for several occurrences. Exporting it as one would
	// under-report exactly when things are worst.
	if e.Count > 1 {
		attrs = append(attrs, keyValue{Key: "netrewind.count", Value: i64(int64(e.Count))})
	}
	for _, k := range sortedKeys(e.Attrs) {
		attrs = append(attrs, keyValue{Key: "netrewind.attr." + k, Value: value(e.Attrs[k])})
	}
	for _, k := range sortedKeys(e.Evidence) {
		attrs = append(attrs, keyValue{Key: "netrewind.evidence." + k, Value: value(e.Evidence[k])})
	}

	ts := strconv.FormatInt(e.TSWall, 10)
	return logRecord{
		TimeUnixNano:         ts,
		ObservedTimeUnixNano: ts,
		SeverityNumber:       severityNumber(e.Severity),
		SeverityText:         string(e.Severity),
		// The same narration the CLI prints and the web interface renders. One
		// implementation, so a dashboard and a terminal cannot disagree about
		// what an event means.
		Body:       str(event.Describe(e)),
		Attributes: attrs,
	}
}

func incidentRecord(inc *incident.Incident) logRecord {
	attrs := []keyValue{
		{Key: "netrewind.incident_id", Value: str(inc.ID)},
		{Key: "netrewind.rule", Value: str(inc.RuleID)},
		{Key: "netrewind.confidence", Value: i64(int64(inc.Confidence))},
		{Key: "netrewind.chain_length", Value: i64(int64(len(inc.Chain)))},
		{Key: "netrewind.advice", Value: str(inc.Advice)},
		{Key: "netrewind.status", Value: str(string(inc.Status))},
		{Key: "netrewind.root_cause.kind", Value: str(string(inc.RootCause.Kind))},
		{Key: "netrewind.root_cause.entity", Value: str(inc.RootCause.Entity)},
		{Key: "netrewind.root_cause.event_id", Value: str(inc.RootCause.EventID)},
	}
	if len(inc.Victims) > 0 {
		attrs = append(attrs, keyValue{Key: "netrewind.victims", Value: value(inc.Victims)})
	}
	// The chain is what makes an incident more than an alert: every link names
	// the event it rests on and what it claims about it. Flattened here rather
	// than nested, because attribute maps are what backends can actually
	// filter on.
	for i, link := range inc.Chain {
		p := "netrewind.chain." + strconv.Itoa(i) + "."
		attrs = append(attrs,
			keyValue{Key: p + "event_id", Value: str(link.EventID)},
			keyValue{Key: p + "kind", Value: str(string(link.Kind))},
			keyValue{Key: p + "relation", Value: str(string(link.Relation))},
			keyValue{Key: p + "why", Value: str(link.Why)},
			keyValue{Key: p + "subject", Value: str(link.Subject)},
		)
	}

	ts := strconv.FormatInt(inc.OpenedAt, 10)
	return logRecord{
		TimeUnixNano:         ts,
		ObservedTimeUnixNano: ts,
		SeverityNumber:       severityNumber(inc.Severity),
		SeverityText:         string(inc.Severity),
		Body:                 str(inc.Title),
		Attributes:           attrs,
	}
}

// sortedKeys keeps the output stable, so two exports of the same batch are
// byte-identical and a diff means something changed.
func sortedKeys(m map[string]any) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sortStrings(keys)
	return keys
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
