package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ResponseFormat is llama-server's per-request structured-output field -
// the mechanism that lets JSONSchemaFor's per-case "evidence_handles" enum
// actually vary case to case, which a server-wide `-jf <file>` startup flag
// could not do.
//
// The shape is flat, not OpenAI's nested `response_format.json_schema.
// schema` - `{"type": ..., "schema": {...}}` directly. That much matches
// tools/server/README.md at the exact pinned commit (b10948) this build
// is from. The `Type` value does NOT match the README as closely: the
// README documents both `"json_object"` and `"json_schema"` as valid
// alongside a `schema` key, but a live smoke test against this exact
// downloaded build (evidence 52) found `"json_schema"` silently applies no
// constraint at all - the model answered with a completely different,
// unconstrained shape and no error of any kind - while `"json_object"`
// with the identical flat `schema` field produced exactly the required
// shape, including the enum-constrained handles. Empirically verified
// behavior of the real binary wins over what its own documentation states;
// this package sends `"json_object"`.
type ResponseFormat struct {
	Type   string         `json:"type"`
	Schema map[string]any `json:"schema"`
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatCompletionRequest struct {
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens"`
	Messages    []ChatMessage `json:"messages"`
	// CachePrompt is sent as false so llama-server evaluates every prompt
	// from scratch. With its prompt cache on, a later run served from KV
	// state does not produce the same logits as a fresh evaluation, and at
	// temperature 0 a single flipped argmax early in the answer changes the
	// whole generation - so "the same run twice" gave different numbers.
	CachePrompt    bool            `json:"cache_prompt"`
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message ChatMessage `json:"message"`
	} `json:"choices"`
	Timings *struct {
		PromptMS    float64 `json:"prompt_ms"`
		PredictedMS float64 `json:"predicted_ms"`
	} `json:"timings"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// ChatResult is one completed request: the raw answer text plus whatever
// timing llama-server reported (both zero when the server sends none - an
// older build, or a request that failed before generation started).
type ChatResult struct {
	Content     string
	PromptMS    float64
	PredictedMS float64
}

// ChatComplete calls an already-running llama-server's OpenAI-compatible
// /v1/chat/completions endpoint and returns the assistant message content -
// the model's raw answer text. schema is sent per-request as
// response_format (see JSONSchemaFor) so the "evidence_handles" enum can
// differ for every request. token, when non-empty, is sent as
// "Authorization: Bearer <token>" - the per-session token the sidecar
// runtime starts llama-server with (ADR 0006); empty for the evaluation
// runner's operator-started, tokenless server.
func ChatComplete(client *http.Client, serverURL, token, sysPrompt, userPrompt string, maxTokens int, schema map[string]any) (ChatResult, error) {
	return Chat(context.Background(), client, serverURL, token, []ChatMessage{
		{Role: "system", Content: sysPrompt},
		{Role: "user", Content: userPrompt},
	}, maxTokens, schema)
}

// Chat is ChatComplete with the full message list exposed and a context
// threaded through to the request, for Analyze's one allowed retry (a
// retry resends the original system/user turns plus the malformed
// assistant reply and a fixed repair instruction, rather than starting
// over, so the model sees exactly what it got wrong) and for a caller that
// needs to cancel an in-flight analysis (the plan's "cancel mid-analysis
// kills the sidecar").
func Chat(ctx context.Context, client *http.Client, serverURL, token string, messages []ChatMessage, maxTokens int, schema map[string]any) (ChatResult, error) {
	reqBody := ChatCompletionRequest{
		Temperature:    0,
		MaxTokens:      maxTokens,
		CachePrompt:    false,
		ResponseFormat: &ResponseFormat{Type: "json_object", Schema: schema},
		Messages:       messages,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return ChatResult{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(serverURL, "/")+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return ChatResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return ChatResult{}, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return ChatResult{}, err
	}

	var cc chatCompletionResponse
	if err := json.Unmarshal(respBody, &cc); err != nil {
		return ChatResult{}, fmt.Errorf("unmarshal response (status %d): %w: %s", resp.StatusCode, err, truncateForError(respBody))
	}
	if cc.Error != nil {
		return ChatResult{}, fmt.Errorf("server error (status %d): %s", resp.StatusCode, cc.Error.Message)
	}
	if len(cc.Choices) == 0 {
		return ChatResult{}, fmt.Errorf("no choices in response (status %d): %s", resp.StatusCode, truncateForError(respBody))
	}
	result := ChatResult{Content: cc.Choices[0].Message.Content}
	if cc.Timings != nil {
		result.PromptMS = cc.Timings.PromptMS
		result.PredictedMS = cc.Timings.PredictedMS
	}
	return result, nil
}

func truncateForError(b []byte) string {
	s := string(b)
	if len(s) > 500 {
		return s[:500] + "..."
	}
	return s
}

// NewHTTPClient is a small helper so callers do not each need to import
// net/http just to build a client with a per-case timeout.
func NewHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout}
}
