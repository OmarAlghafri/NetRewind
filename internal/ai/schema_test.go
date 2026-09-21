package ai

import "testing"

// TestJSONSchemaForConstrainsEvidenceHandlesToExactlyWhatWasOffered pins
// execution order §4.10's actual fix: "evidence_handles" is a closed enum
// of exactly the handles passed in, not a free-form string array - a model
// literally cannot sample a handle outside this list once llama-server
// applies this schema, regardless of what the system prompt merely asks
// for.
func TestJSONSchemaForConstrainsEvidenceHandlesToExactlyWhatWasOffered(t *testing.T) {
	s := JSONSchemaFor([]string{"E1", "E2", "E3"}, 0)
	props := s["properties"].(map[string]any)
	handles := props["evidence_handles"].(map[string]any)
	items := handles["items"].(map[string]any)
	enum := items["enum"].([]any)
	if len(enum) != 3 || enum[0] != "E1" || enum[2] != "E3" {
		t.Errorf("evidence_handles enum = %v, want exactly [E1 E2 E3]", enum)
	}
}

// TestJSONSchemaForWithNoEvidenceForcesAnEmptyCitationList proves a request
// with zero real events (or all malformed) produces an enum with zero
// allowed values, not an unconstrained array.
func TestJSONSchemaForWithNoEvidenceForcesAnEmptyCitationList(t *testing.T) {
	s := JSONSchemaFor(nil, 0)
	props := s["properties"].(map[string]any)
	handles := props["evidence_handles"].(map[string]any)
	items := handles["items"].(map[string]any)
	enum := items["enum"].([]any)
	if len(enum) != 0 {
		t.Errorf("evidence_handles enum = %v, want empty", enum)
	}
}

// TestJSONSchemaForAppliesTheConfidenceCeilingStructurally pins execution
// order §4.10's "the model does not get to set its own ceiling" as a
// schema-level maximum, not merely a post-hoc grading check.
func TestJSONSchemaForAppliesTheConfidenceCeilingStructurally(t *testing.T) {
	s := JSONSchemaFor([]string{"E1"}, 62)
	props := s["properties"].(map[string]any)

	hypProps := props["ranked_hypotheses"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	if hypProps["confidence"].(map[string]any)["maximum"] != 62 {
		t.Errorf("ranked_hypotheses[].confidence maximum = %v, want 62", hypProps["confidence"].(map[string]any)["maximum"])
	}
	if props["confidence_ceiling"].(map[string]any)["maximum"] != 62 {
		t.Errorf("confidence_ceiling maximum = %v, want 62", props["confidence_ceiling"].(map[string]any)["maximum"])
	}
}

// TestJSONSchemaForWithNoCeilingStillBoundsConfidenceTo100 proves a
// request with no specific ceiling (0, meaning "not set") still rejects a
// nonsensical confidence like 250 - the unconditional [0,100] bound, not an
// open-ended integer.
func TestJSONSchemaForWithNoCeilingStillBoundsConfidenceTo100(t *testing.T) {
	s := JSONSchemaFor([]string{"E1"}, 0)
	props := s["properties"].(map[string]any)
	ceiling := props["confidence_ceiling"].(map[string]any)
	if ceiling["maximum"] != 100 || ceiling["minimum"] != 0 {
		t.Errorf("confidence_ceiling bounds = [%v,%v], want [0,100] even with no specific ceiling", ceiling["minimum"], ceiling["maximum"])
	}
}

// TestJSONSchemaForBoundsEveryStringFieldsLength pins evidence 55's fix: a
// model that reasons out loud inside a field's own text (there, an
// `entity` value running to hundreds of words) must be stopped by the
// grammar itself, not merely asked nicely by the system prompt - so every
// string-bearing field carries a `maxLength`, with no field left
// unbounded.
func TestJSONSchemaForBoundsEveryStringFieldsLength(t *testing.T) {
	s := JSONSchemaFor([]string{"E1"}, 0)
	props := s["properties"].(map[string]any)

	summary := props["summary"].(map[string]any)
	if ml, ok := summary["maxLength"]; !ok || ml.(int) <= 0 {
		t.Errorf("summary maxLength = %v, want a positive bound", ml)
	}

	hypProps := props["ranked_hypotheses"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	for _, field := range []string{"cause", "entity"} {
		if ml, ok := hypProps[field].(map[string]any)["maxLength"]; !ok || ml.(int) <= 0 {
			t.Errorf("ranked_hypotheses[].%s maxLength = %v, want a positive bound", field, ml)
		}
	}

	for _, field := range []string{"counter_evidence", "unknowns", "next_checks"} {
		item := props[field].(map[string]any)["items"].(map[string]any)
		if ml, ok := item["maxLength"]; !ok || ml.(int) <= 0 {
			t.Errorf("%s[] maxLength = %v, want a positive bound", field, ml)
		}
	}
}
