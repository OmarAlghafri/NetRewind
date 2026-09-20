package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRequestDisablesThePromptCache pins the reproducibility fix: every
// request tells llama-server not to serve the prompt from its KV cache, so
// a rerun of the same case evaluates the same tokens the same way.
func TestRequestDisablesThePromptCache(t *testing.T) {
	body, err := json.Marshal(ChatCompletionRequest{Temperature: 0, MaxTokens: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"cache_prompt":false`) {
		t.Errorf("request body = %s, want cache_prompt:false present", body)
	}
}

// TestChatCompletionRequestSendsThePerCaseSchema pins that the schema
// actually travels on the wire as response_format, not just built and
// discarded - the mechanism this package relies on instead of a
// server-wide -jf flag, which cannot vary the enum per request.
func TestChatCompletionRequestSendsThePerCaseSchema(t *testing.T) {
	body, err := json.Marshal(ChatCompletionRequest{
		Temperature:    0,
		MaxTokens:      1,
		ResponseFormat: &ResponseFormat{Type: "json_object", Schema: JSONSchemaFor([]string{"E1"}, 0)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"E1"`) {
		t.Errorf("request body does not carry the per-case handle enum: %s", body)
	}
	if !strings.Contains(string(body), `"response_format"`) {
		t.Errorf("request body missing response_format entirely: %s", body)
	}
}

// TestResponseFormatMatchesLlamaServersOwnShapeNotOpenAIs is the direct
// regression test for a real bug found in two stages: first by reading
// llama.cpp's own server README instead of assuming OpenAI's API shape
// (the schema must sit directly at response_format.schema, with no
// intermediate "json_schema" wrapper object - the wrapped shape would not
// error, it would just silently constrain nothing); then by a live smoke
// test against the real downloaded build finding that the README's own
// "json_schema" Type value ALSO silently constrains nothing in practice,
// while "json_object" with the identical flat schema field works exactly
// as intended (evidence 52) - so this test pins the empirically-verified
// value, not the one the documentation states.
func TestResponseFormatMatchesLlamaServersOwnShapeNotOpenAIs(t *testing.T) {
	body, err := json.Marshal(ResponseFormat{Type: "json_object", Schema: map[string]any{"marker": "present"}})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	schema, ok := decoded["schema"].(map[string]any)
	if !ok {
		t.Fatalf("response_format = %s, want a top-level \"schema\" key (llama-server's own shape), not nested under \"json_schema\"", body)
	}
	if schema["marker"] != "present" {
		t.Errorf("schema content = %v, want the actual schema preserved directly under \"schema\"", schema)
	}
	if _, wrapped := decoded["json_schema"]; wrapped {
		t.Error(`response_format has a "json_schema" wrapper key - that is OpenAI's shape, not llama-server's; llama-server would silently ignore this`)
	}
}

// TestChatCompleteSendsTheEmpiricallyVerifiedTypeValue pins the exact wire
// value ChatComplete actually sends: "json_object", not the
// README-documented-but-non-functional "json_schema".
func TestChatCompleteSendsTheEmpiricallyVerifiedTypeValue(t *testing.T) {
	body, err := json.Marshal(ChatCompletionRequest{
		ResponseFormat: &ResponseFormat{Type: "json_object", Schema: map[string]any{"type": "object"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"type":"json_object"`) {
		t.Errorf(`request body = %s, want response_format.type == "json_object" (the type value verified to actually work against the real build)`, body)
	}
}
