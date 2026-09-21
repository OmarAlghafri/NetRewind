// Command golden (re)captures ai/eval/run/testdata/golden/*.request.json:
// the exact chatCompletionRequest body (system prompt, per-case schema,
// redacted+deidentified events, question) for every case in
// ai/eval/splits.json, in both languages. main_test.go's
// TestRequestGoldensMatchPreRefactorRunnerForEveryCaseAndLanguage compares
// against these files byte-for-byte; this command is that test's own
// documented way to regenerate them (its failure message names this exact
// package) whenever the prompt or schema changes on purpose, which the
// test cannot and should not distinguish from an accidental drift on its
// own - regenerating is a deliberate, reviewed action, never automatic.
//
// The loading/redaction/deidentify logic here is intentionally a copy of
// ai/eval/run/main.go's own unexported helpers, not an import of them -
// this package already tolerates the same duplication once, between this
// runner and ai/eval/gen/main.go (see buildDeidentifyTable's own doc
// comment there for why), for the same reason: these are corpus-authoring
// specifics that do not belong in the internal/ai library those two
// programs both call, and a private main-package helper cannot be
// imported anyway.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/OmarAlghafri/netrewind/ai/eval/harness"
	"github.com/OmarAlghafri/netrewind/ai/eval/schema"
	"github.com/OmarAlghafri/netrewind/internal/ai"
)

func main() {
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
		fatal(fmt.Errorf("no case IDs found in splits.json"))
	}

	written := 0
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
			data, err := json.MarshalIndent(reqBody, "", "  ")
			if err != nil {
				fatal(fmt.Errorf("[%s.%s] marshal: %w", id, lang, err))
			}
			path := filepath.Join(goldenDir, id+"."+lang+".request.json")
			if err := os.WriteFile(path, data, 0o644); err != nil {
				fatal(fmt.Errorf("[%s.%s] write: %w", id, lang, err))
			}
			written++
		}
	}
	fmt.Printf("wrote %d golden request files to %s\n", written, goldenDir)
}

// buildEventsText, buildDeidentifyTable, deidentify and loadScenarioEvents
// below are byte-for-byte copies of ai/eval/run/main.go's own unexported
// functions of the same name - see that file if either ever needs to
// change, since both must change together or the two programs' idea of
// "the request a case actually produces" will silently diverge.

func buildEventsText(caseID string, events []map[string]any, hm harness.HandleMap) string {
	redacted := make([]map[string]any, len(events))
	for i, e := range events {
		redacted[i] = hm.RedactEvent(e)
	}
	eventsJSON, err := json.MarshalIndent(redacted, "", "  ")
	if err != nil {
		fatal(err)
	}
	eventsText := string(eventsJSON)
	if strings.Contains(caseID, "deidentified") {
		table := buildDeidentifyTable(events)
		eventsText = deidentify(eventsText, table)
	}
	return eventsText
}

func buildDeidentifyTable(events []map[string]any) map[string]string {
	table := map[string]string{}
	for _, e := range events {
		subject, ok := e["subject"].(map[string]any)
		if !ok {
			continue
		}
		label, ok := subject["label"].(string)
		if !ok || !strings.HasPrefix(label, "10.99.") {
			continue
		}
		if _, seen := table[label]; !seen {
			table[label] = fmt.Sprintf("<HOST_%d>", len(table)+1)
		}
	}
	return table
}

func deidentify(s string, table map[string]string) string {
	for real, placeholder := range table {
		s = strings.ReplaceAll(s, real, placeholder)
	}
	return s
}

func loadScenarioEvents(root, scenario, eventsOverride string) []map[string]any {
	path := filepath.Join(root, "corpus", "v1", scenario, "events.json")
	if eventsOverride != "" {
		path = filepath.Join(root, filepath.FromSlash(eventsOverride))
	}
	var events []map[string]any
	readJSON(path, &events)
	return events
}

func repoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		fatal(err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	fatal(fmt.Errorf("could not find repo root (go.mod) from %s", dir))
	return ""
}

func readJSON(path string, v any) {
	data, err := os.ReadFile(path)
	if err != nil {
		fatal(fmt.Errorf("%s: %w", path, err))
	}
	if err := json.Unmarshal(data, v); err != nil {
		fatal(fmt.Errorf("%s: %w", path, err))
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "golden:", err)
	os.Exit(1)
}
