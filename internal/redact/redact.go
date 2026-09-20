// Package redact turns real private addresses into consistent, meaningless
// placeholders for anything that leaves the local-analysis panel: a copied
// report, a debug prompt log, a persisted follow-up-question thread. Local
// inference itself sees real addresses (nothing it produces leaves the
// device - ADR 0006), so this package is never on that path; it exists for
// the handful of places downstream of an answer that are no longer purely
// local by the same argument (a clipboard, a debug log file, a stored
// thread another session's opt-in-history feature will read back).
package redact

import (
	"fmt"
	"regexp"
)

// execution order §4.10: "Redact/replace DNS names and IPs before building
// the prompt ... Treat every hostname/DNS/rule-text value as untrusted
// data." privateIPPattern matches the three RFC 1918 private ranges a real
// deployment's LAN addresses fall in. macPattern matches a colon-separated
// MAC address, e.g. event attrs.mac_old/attrs.mac_new.
var (
	privateIPPattern = regexp.MustCompile(`\b(?:10(?:\.\d{1,3}){3}|172\.(?:1[6-9]|2\d|3[01])(?:\.\d{1,3}){2}|192\.168(?:\.\d{1,3}){2})\b`)
	macPattern       = regexp.MustCompile(`\b[0-9A-Fa-f]{2}(?::[0-9A-Fa-f]{2}){5}\b`)
)

// Redactor replaces every private IPv4 address with a sequential
// "<HOST_N>" placeholder and every MAC address with a sequential "<MAC_N>"
// placeholder, first-seen order within each category. The same real value
// always gets the same placeholder across every call made on one Redactor,
// so relationships between events (e.g. "this host's address changed from
// X to Y") stay visible across an entire multi-section document - a
// report's engine-facts section and its model-answer section must agree on
// which number a given address got, which a stateless per-call redaction
// cannot guarantee.
//
// Not safe for concurrent use: a Redactor is built for one document by one
// goroutine.
type Redactor struct {
	hosts map[string]string
	macs  map[string]string
}

// New returns an empty Redactor, ready to redact the first section of one
// document.
func New() *Redactor {
	return &Redactor{hosts: map[string]string{}, macs: map[string]string{}}
}

// Redact returns s with every private IPv4 and MAC address replaced by its
// placeholder, assigning a new one the first time a value is seen and
// reusing it on every later call (on this Redactor) where the same value
// appears again.
func (r *Redactor) Redact(s string) string {
	s = privateIPPattern.ReplaceAllStringFunc(s, func(m string) string {
		if _, ok := r.hosts[m]; !ok {
			r.hosts[m] = fmt.Sprintf("<HOST_%d>", len(r.hosts)+1)
		}
		return r.hosts[m]
	})
	s = macPattern.ReplaceAllStringFunc(s, func(m string) string {
		if _, ok := r.macs[m]; !ok {
			r.macs[m] = fmt.Sprintf("<MAC_%d>", len(r.macs)+1)
		}
		return r.macs[m]
	})
	return s
}

// String is a one-shot convenience for redacting a single, self-contained
// piece of text - equivalent to New().Redact(s), and consistent only
// within that one call (see Redactor's own doc for why a multi-section
// document needs one shared Redactor instead).
func String(s string) string {
	return New().Redact(s)
}
