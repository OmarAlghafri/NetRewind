package harness

import "github.com/OmarAlghafri/netrewind/internal/redact"

// RedactAddresses is an alias onto internal/redact.String - see that
// package's own doc for why it exists and what it deliberately does not
// guarantee across separate calls (internal/redact.Redactor is for that).
// Kept here so this package's own callers and history do not need to
// change; the real implementation, and its full test coverage, now lives
// in internal/redact.
func RedactAddresses(s string) string { return redact.String(s) }
