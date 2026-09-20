package ai

import (
	"crypto/sha256"
	"encoding/hex"
)

// Fingerprint identifies "the same conclusion" across a daemon restart or a
// rule re-firing - unlike an incident's own ID (a fresh ULID assigned every
// time a rule fires, internal/correlate/engine.go's build()), this is
// stable for the same rule reaching the same root cause about the same
// entity, which is what operator notes and similarity retrieval actually
// need to key on.
func Fingerprint(ruleID, rootCauseKind, rootCauseEntity string) string {
	sum := sha256.Sum256([]byte(ruleID + "|" + rootCauseKind + "|" + rootCauseEntity))
	return hex.EncodeToString(sum[:])
}
