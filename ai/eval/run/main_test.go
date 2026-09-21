package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OmarAlghafri/netrewind/ai/eval/harness"
	"github.com/OmarAlghafri/netrewind/ai/eval/schema"
	"github.com/OmarAlghafri/netrewind/internal/ai"
)

// TestBuildDeidentifyTableMatchesGeneratorOrder proves this runner's table
// construction produces the identical <HOST_N> assignment ai/eval/gen would
// for the same real scenario - built from the actual gateway_hijack corpus
// events (a real, singular 10.99.0.201 address), not synthetic data. If
// this ever disagreed with gen/main.go's own deidentify table, a
// "-deidentified" case's context would show a DIFFERENT placeholder than
// the one already baked into that case's QuestionEn/Expected.RootCauseEntity,
// silently breaking every deidentified case's grading.
func TestBuildDeidentifyTableMatchesGeneratorOrder(t *testing.T) {
	events := []map[string]any{
		{"subject": map[string]any{"kind": "observer", "label": "Dell"}},
		{"subject": map[string]any{"kind": "host", "label": "10.99.0.201"}},
		{"subject": map[string]any{"kind": "host", "label": "10.99.0.201"}}, // repeat: must not get a second placeholder
		{"subject": map[string]any{"kind": "host", "label": "10.99.1.11"}},
		{"nosubject": true}, // malformed/missing subject must not panic
	}
	table := buildDeidentifyTable(events)
	if table["10.99.0.201"] != "<HOST_1>" {
		t.Errorf("10.99.0.201 = %q, want <HOST_1> (first-seen order)", table["10.99.0.201"])
	}
	if table["10.99.1.11"] != "<HOST_2>" {
		t.Errorf("10.99.1.11 = %q, want <HOST_2>", table["10.99.1.11"])
	}
	if len(table) != 2 {
		t.Errorf("table has %d entries, want 2 (Dell is not a 10.99.x.x address and must not be table)", len(table))
	}

	text := `event about 10.99.0.201 and again 10.99.0.201, also 10.99.1.11`
	got := deidentify(text, table)
	want := `event about <HOST_1> and again <HOST_1>, also <HOST_2>`
	if got != want {
		t.Errorf("deidentify() = %q, want %q", got, want)
	}
}

// TestLoadScenarioEventsRespectsEventsOverride is a real, on-disk check
// against the actual checked-in files (not synthetic strings): with no
// override, arp_change loads its own real corpus/v1 events, carrying real
// event IDs from that scenario. With an override set (exactly the value
// gen/main.go writes for the synthetic adversarial case), it loads from
// ai/eval/synthetic/ instead, carrying that file's own distinct event IDs
// - proving the two paths do not silently collapse to the same data.
func TestLoadScenarioEventsRespectsEventsOverride(t *testing.T) {
	root := repoRoot()

	real := loadScenarioEvents(root, "arp_change", "")
	if len(real) == 0 {
		t.Fatal("expected real arp_change events, got none")
	}

	override := loadScenarioEvents(root, "malicious_dns_name", "ai/eval/synthetic/malicious_dns_name_injected/events.json")
	if len(override) != 3 {
		t.Fatalf("expected 3 events from the synthetic override file, got %d", len(override))
	}
	found := false
	for _, e := range override {
		attrs, ok := e["attrs"].(map[string]any)
		if !ok {
			continue
		}
		if name, ok := attrs["name"].(string); ok && strings.Contains(name, "ignore-all-previous-instructions") {
			found = true
		}
	}
	if !found {
		t.Error("loaded override events do not contain the expected synthetic injected payload")
	}
}

// TestLoadScenarioEventsOverridePathIsPortable proves the exact literal
// value gen/main.go writes to a checked-in case file resolves on this OS -
// a real bug this test would have caught directly: an earlier version
// built this path with filepath.Join on Windows, baking in backslashes
// that do not resolve as directory separators on Linux, where the actual
// benchmark run happens.
func TestLoadScenarioEventsOverridePathIsPortable(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(), "ai", "eval", "cases", "malicious_dns_name-adversarial-injected-en.json"))
	if err != nil {
		t.Fatal(err)
	}
	var c schema.Case
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(c.EventsOverride, `\`) {
		t.Fatalf("events_override = %q contains a backslash - not portable to a Linux benchmark run", c.EventsOverride)
	}
	events := loadScenarioEvents(repoRoot(), c.Scenario, c.EventsOverride)
	if len(events) == 0 {
		t.Fatal("the case's own events_override value did not resolve to any events")
	}
}

// TestRequestGoldensMatchPreRefactorRunnerForEveryCaseAndLanguage started
// as Phase 0/1's exit gate (proving the internal/ai package split changed
// nothing about what a model is actually asked) and now serves the same
// purpose on an ongoing basis: internal/ai/golden captures the exact
// chatCompletionRequest body (system prompt, per-case schema, redacted+
// deidentified events, question) for all 37 cases in both languages - 74
// files - and this test rebuilds the identical request through today's
// code and compares byte-for-byte. A drift here means either an
// accidental change (a bug) or a deliberate one (e.g. evidence 55's
// maxLength/rule-7 fix for verbose model output) - in the deliberate
// case, `go run ./internal/ai/golden` regenerates the goldens as a real,
// reviewed step, never automatically.
func TestRequestGoldensMatchPreRefactorRunnerForEveryCaseAndLanguage(t *testing.T) {
	root := repoRoot()
	goldenDir := filepath.Join(root, "ai", "eval", "run", "testdata", "golden")

	var splits map[string][]string
	readJSON(filepath.Join(root, "ai", "eval", "splits.json"), &splits)

	seen := map[string]bool{}
	var allIDs []string
	for _, ids := range splits {
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				allIDs = append(allIDs, id)
			}
		}
	}
	if len(allIDs) == 0 {
		t.Fatal("no case IDs found in splits.json")
	}

	compared := 0
	for _, id := range allIDs {
		var c schema.Case
		readJSON(filepath.Join(root, "ai", "eval", "cases", id+".json"), &c)

		events := loadScenarioEvents(root, c.Scenario, c.EventsOverride)
		hm := harness.BuildHandles(events)
		eventsText := buildEventsText(id, events, hm)

		for _, lang := range []string{"en", "ar"} {
			question := c.QuestionEn
			if lang == "ar" {
				question = c.QuestionAr
			}
			userPrompt := ai.BuildUserPrompt(eventsText, "", "", question)
			reqBody := ai.ChatCompletionRequest{
				Temperature:    0,
				MaxTokens:      700,
				CachePrompt:    false,
				ResponseFormat: &ai.ResponseFormat{Type: "json_object", Schema: ai.JSONSchemaFor(hm.Handles, c.Expected.MaxConfidence)},
				Messages: []ai.ChatMessage{
					{Role: "system", Content: ai.SystemPrompt},
					{Role: "user", Content: userPrompt},
				},
			}
			got, err := json.MarshalIndent(reqBody, "", "  ")
			if err != nil {
				t.Fatalf("[%s.%s] marshal: %v", id, lang, err)
			}

			goldenPath := filepath.Join(goldenDir, id+"."+lang+".request.json")
			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("[%s.%s] reading golden: %v (run: go run ./internal/ai/golden to (re)capture)", id, lang, err)
			}
			if string(got) != string(want) {
				t.Errorf("[%s.%s] request body drifted from the golden at %s (run: go run ./internal/ai/golden to regenerate, if this drift is intentional)", id, lang, goldenPath)
			}
			compared++
		}
	}
	if compared != 74 {
		t.Errorf("compared %d requests, want 74 (37 cases x 2 languages) - a case was added/removed without updating this expectation or the goldens", compared)
	}
}
