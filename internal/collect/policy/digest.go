// Package policy watches the filtering rules in force, so that a change to them
// can be placed on the same timeline as the connections it breaks.
//
// It records that the ruleset changed and how, never what the right ruleset
// would be. The recorder observes; it does not have opinions about
// configuration and it never changes anything.
package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Ruleset is a snapshot of the filtering rules in force.
type Ruleset struct {
	// Digest identifies the snapshot. Comparing digests is how a change is
	// noticed without keeping every past ruleset.
	Digest string
	// Lines are the significant lines, in order.
	Lines []string
}

// Diff is what changed between two snapshots.
type Diff struct {
	Added   []string
	Removed []string
}

// Empty reports whether anything actually changed.
func (d Diff) Empty() bool { return len(d.Added) == 0 && len(d.Removed) == 0 }

// Snapshot normalises a raw ruleset dump into something comparable.
//
// Each rule is qualified by the table and chain that contains it, because a
// rule is not the same rule in a different chain. Comparing bare rule text
// would report a rule moved from one chain to another as no change at all -
// which is exactly the sort of change that breaks a network quietly.
//
// Handles, counters and byte totals are stripped: they change constantly on a
// live firewall without the policy changing at all, and a collector that
// reported those as rule changes would cry wolf every few seconds until nobody
// looked at it again.
func Snapshot(raw string) Ruleset {
	var lines []string
	var stack []string

	for _, line := range strings.Split(raw, "\n") {
		line = normalise(line)
		switch {
		case line == "":
			continue
		case line == "}":
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case strings.HasSuffix(line, "{"):
			header := strings.TrimSpace(strings.TrimSuffix(line, "{"))
			stack = append(stack, header)
			// A container is recorded in its own right, so a chain being
			// added or removed is visible even when it holds no rules.
			lines = append(lines, "["+strings.Join(stack, " / ")+"]")
		default:
			lines = append(lines, strings.Join(stack, " / ")+" :: "+line)
		}
	}

	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return Ruleset{Digest: hex.EncodeToString(sum[:8]), Lines: lines}
}

// normalise strips the parts of a rule line that vary without the policy
// varying: whitespace, comments, rule handles and packet counters.
func normalise(line string) string {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return ""
	}
	var out []string
	fields := strings.Fields(line)
	for i := 0; i < len(fields); i++ {
		switch fields[i] {
		case "#":
			return strings.Join(out, " ") // trailing comment
		case "handle":
			i++ // and its number
			continue
		case "counter":
			// "counter packets N bytes M"
			for i+2 < len(fields) && (fields[i+1] == "packets" || fields[i+1] == "bytes") {
				i += 2
			}
			out = append(out, "counter")
			continue
		}
		out = append(out, fields[i])
	}
	return strings.Join(out, " ")
}

// Compare reports what changed between two snapshots.
//
// Multiset comparison rather than a line diff: a rule moving within a chain is
// not a policy change, and reporting it as one would bury the changes that are.
func Compare(before, after Ruleset) Diff {
	count := func(lines []string) map[string]int {
		m := make(map[string]int, len(lines))
		for _, l := range lines {
			m[l]++
		}
		return m
	}
	was, now := count(before.Lines), count(after.Lines)

	var d Diff
	for _, l := range after.Lines {
		if now[l] > was[l] {
			d.Added = append(d.Added, l)
			was[l]++
		}
	}
	for _, l := range before.Lines {
		if was[l] > now[l] {
			d.Removed = append(d.Removed, l)
			now[l]++
		}
	}
	return d
}

// interesting reports whether a rule line is one whose appearance could break
// connectivity. Used to raise severity: adding a drop is not the same kind of
// event as adding a log.
func interesting(line string) bool {
	for _, verdict := range []string{" drop", " reject", " jump ", "policy drop"} {
		if strings.Contains(line, verdict) {
			return true
		}
	}
	return false
}
