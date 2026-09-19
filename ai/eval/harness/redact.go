package harness

import (
	"fmt"
	"regexp"
)

// execution order §4.10: "Redact/replace DNS names and IPs before building
// the prompt ... Treat every hostname/DNS/rule-text value as untrusted
// data." privateIPPattern matches the three RFC 1918 private ranges this
// project's own lab and any real deployment's LAN addresses fall in -
// generalized from the eval corpus's own `buildDeidentifyTable` in
// ai/eval/run/main.go, which only matched a literal "10.99." prefix
// (correct for that one lab's fixed subnet, not a general private-address
// pattern a real product needs). macPattern matches a colon-separated MAC
// address, which no earlier redaction in this codebase touched at all -
// event `attrs.mac_old`/`attrs.mac_new` carry these verbatim.
//
// This is deliberately NOT wired into ai/eval/run/main.go's default prompt
// path yet: doing so would silently break every non "-deidentified" case's
// Top1/Top3 scoring, since those cases' schema.Expected.RootCauseEntity
// values are the real address the model would then never see. Making
// redaction the default for every case needs the corpus's Expected values
// reworked to match (the same kind of change already tracked separately as
// "expand ai/eval/cases/ meaningfully beyond 33 cases"), not a quiet
// side-effect of adding this function. This function itself is ready for
// the eventual product runtime to call directly, and is tested on its own.
var (
	privateIPPattern = regexp.MustCompile(`\b(?:10(?:\.\d{1,3}){3}|172\.(?:1[6-9]|2\d|3[01])(?:\.\d{1,3}){2}|192\.168(?:\.\d{1,3}){2})\b`)
	macPattern       = regexp.MustCompile(`\b[0-9A-Fa-f]{2}(?::[0-9A-Fa-f]{2}){5}\b`)
)

// RedactAddresses replaces every private IPv4 address with a sequential
// "<HOST_N>" placeholder and every MAC address with a sequential "<MAC_N>"
// placeholder, first-seen order within each category, the same real value
// always getting the same placeholder within one call so relationships
// between events (e.g. "this host's address changed from X to Y") remain
// visible to the model without exposing the real values.
func RedactAddresses(s string) string {
	hosts := map[string]string{}
	s = privateIPPattern.ReplaceAllStringFunc(s, func(m string) string {
		if _, ok := hosts[m]; !ok {
			hosts[m] = fmt.Sprintf("<HOST_%d>", len(hosts)+1)
		}
		return hosts[m]
	})
	macs := map[string]string{}
	s = macPattern.ReplaceAllStringFunc(s, func(m string) string {
		if _, ok := macs[m]; !ok {
			macs[m] = fmt.Sprintf("<MAC_%d>", len(macs)+1)
		}
		return macs[m]
	})
	return s
}
