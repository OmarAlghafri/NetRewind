package ai

// JSONSchemaFor builds the per-case constrained-output schema (execution
// order §4.10: "enforce via a dynamic JSON-schema/grammar built per-request
// from the actual evidence set"). "evidence_handles" is a closed `enum` of
// exactly the handles offered for THIS request's events (plus any history/
// annotation handles) - not a free-form string array - so the model cannot
// sample a token sequence naming a handle it was never given, let alone a
// real identifier (which it never sees at all; see HandleMap.RedactEvent).
// Sent per-request as llama-server's own `response_format.schema` field
// (see ChatComplete's own doc comment for why this is NOT OpenAI's nested
// shape) rather than via llama-server's server-wide `-jf <file>` startup
// flag, because `-jf` fixes one schema for the whole server session and
// cannot vary per request the way this enum must.
//
// ceiling (0 means "no ceiling") makes the confidence ceiling structural
// rather than merely graded after the fact: "the model does not get to set
// its own ceiling" (§4.10) becomes a `"maximum"` bound on the confidence
// fields themselves, so a value above the deterministic engine's own
// confidence for this conclusion cannot be sampled at all.
//
// `maxLength` on every string field is the same idea applied to verbosity,
// added after evidence 55: a model that reasons out loud inside a field's
// own text (observed there as one `entity` value running to hundreds of
// words of hedged self-debate) exhausts the generation budget before the
// object can close, which fails validation exactly like a fabricated
// handle would. `shortField`/`freeTextItem`'s limits are generous over
// every real corpus value seen so far (short kind-like causes, addresses,
// hostnames) but firm enough that llama-server's own grammar - not just
// the system prompt's request - stops a runaway string well short of the
// token budget.
func JSONSchemaFor(handles []string, ceiling int) map[string]any {
	handleEnum := []any{}
	for _, h := range handles {
		handleEnum = append(handleEnum, h)
	}
	confidenceField := map[string]any{"type": "integer", "minimum": 0, "maximum": 100}
	if ceiling > 0 {
		confidenceField["maximum"] = ceiling
	}
	shortField := map[string]any{"type": "string", "maxLength": 100}
	freeTextItem := map[string]any{"type": "string", "maxLength": 300}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"summary": map[string]any{"type": "string", "maxLength": 600},
			"ranked_hypotheses": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"cause":      shortField,
						"entity":     shortField,
						"confidence": confidenceField,
					},
					"required": []any{"cause", "entity", "confidence"},
				},
			},
			"evidence_handles": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string", "enum": handleEnum},
			},
			"counter_evidence":   map[string]any{"type": "array", "items": freeTextItem},
			"unknowns":           map[string]any{"type": "array", "items": freeTextItem},
			"confidence_ceiling": confidenceField,
			"next_checks":        map[string]any{"type": "array", "items": freeTextItem},
		},
		"required": []any{
			"summary", "ranked_hypotheses", "evidence_handles", "counter_evidence",
			"unknowns", "confidence_ceiling", "next_checks",
		},
	}
}
