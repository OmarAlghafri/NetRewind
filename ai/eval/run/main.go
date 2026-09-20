// Command run is the ai/eval benchmark runner: it feeds each case in a
// split to a real local llama.cpp server running a real downloaded GGUF
// model, parses the model's constrained-JSON answer, and grades it with
// ai/eval/harness - the actual measurement PRODUCT_RELEASE_PLAN_AR.md §6.4
// requires before any AI feature is considered for integration.
//
// This program makes no network calls of its own beyond loopback HTTP to a
// llama-server process the operator already started on this machine.
// Everything model-facing (the system prompt, the per-case JSON schema, the
// HTTP client, the answer extractor) lives in internal/ai now, not here -
// this file is a thin wrapper over that package, kept that way on purpose
// (see internal/ai's own package doc): the CLI and the desktop shell call
// the identical functions, so what this runner measures and what the
// product ships cannot drift apart. See internal/ai/golden_test.go and
// ai/eval/run/golden_test.go for the parity proof against the pre-refactor
// version of this file.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/OmarAlghafri/netrewind/ai/eval/harness"
	"github.com/OmarAlghafri/netrewind/ai/eval/schema"
	"github.com/OmarAlghafri/netrewind/internal/ai"
)

type runResult struct {
	CaseID   string               `json:"case_id"`
	Scenario string               `json:"scenario"`
	Kind     string               `json:"kind"`
	Elapsed  string               `json:"elapsed"`
	RawTail  string               `json:"raw_tail"`
	ParseErr string               `json:"parse_err,omitempty"`
	Output   *harness.ModelOutput `json:"output,omitempty"`
	Grade    *harness.Result      `json:"grade,omitempty"`
}

func main() {
	split := flag.String("split", "dev", "which split from ai/eval/splits.json to run (dev by default - never test, to avoid tuning against the held-out set)")
	serverURL := flag.String("server", "http://127.0.0.1:8811", "base URL of an already-running llama-server (start it yourself: llama-server -m <model> --host 127.0.0.1 --port 8811 --no-jinja - the per-case JSON schema is sent with every request, not via -jf)")
	nPredict := flag.Int("n-predict", 700, "max tokens to generate per case (sent as max_tokens)")
	timeoutSeconds := flag.Int("timeout-seconds", 600, "HTTP client timeout per case - CPU inference on a 3.8B model is slow, minutes per case is normal")
	skipDeidentified := flag.Bool("skip-deidentified", false, "skip -deidentified case variants (now supported via buildDeidentifyTable/deidentify - default false)")
	lang := flag.String("lang", "en", "which question language to use: \"en\" (QuestionEn) or \"ar\" (QuestionAr) - grading criteria (Expected) are language-independent, only the prompt text changes")
	flag.Parse()

	if *lang != "en" && *lang != "ar" {
		fatal(fmt.Errorf("-lang must be \"en\" or \"ar\", got %q", *lang))
	}

	root := repoRoot()
	var splits map[string][]string
	readJSON(filepath.Join(root, "ai", "eval", "splits.json"), &splits)

	caseIDs := splits[*split]
	if caseIDs == nil {
		fatal(fmt.Errorf("no such split %q in splits.json", *split))
	}

	runDir := filepath.Join(root, "ai", "eval", "results", time.Now().UTC().Format("20060102T150405Z"))
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		fatal(err)
	}

	client := ai.NewHTTPClient(time.Duration(*timeoutSeconds) * time.Second)

	var results []runResult
	for _, id := range caseIDs {
		if *skipDeidentified && strings.Contains(id, "deidentified") {
			continue
		}
		var c schema.Case
		readJSON(filepath.Join(root, "ai", "eval", "cases", id+".json"), &c)

		events := loadScenarioEvents(root, c.Scenario, c.EventsOverride)
		hm := harness.BuildHandles(events)
		eventsText := buildEventsText(id, events, hm)

		question := c.QuestionEn
		if *lang == "ar" {
			question = c.QuestionAr
		}
		userPrompt := ai.BuildUserPrompt(eventsText, "", "", question)

		start := time.Now()
		chatResult, err := ai.ChatComplete(client, *serverURL, "", ai.SystemPrompt, userPrompt, *nPredict, ai.JSONSchemaFor(hm.Handles, c.Expected.MaxConfidence))
		elapsed := time.Since(start)

		rr := runResult{CaseID: id, Scenario: c.Scenario, Kind: c.Kind, Elapsed: elapsed.Round(time.Second).String()}

		if err != nil {
			rr.ParseErr = "llama-server request failed: " + err.Error()
			results = append(results, rr)
			fmt.Printf("[%s] FAILED TO RUN: %v\n", id, err)
			continue
		}
		rr.RawTail = chatResult.Content // the full response content, not a terminal transcript - no truncation needed

		var out harness.ModelOutput
		if perr := ai.ExtractModelOutput(chatResult.Content, &out); perr != nil {
			rr.ParseErr = perr.Error()
			results = append(results, rr)
			fmt.Printf("[%s] INVALID JSON OUTPUT: %v\n", id, perr)
			continue
		}
		rr.Output = &out

		grade := harness.Grade(id, c.Expected, hm, out)
		rr.Grade = &grade
		results = append(results, rr)

		status := "PASS"
		if !grade.Pass() {
			status = "FAIL"
		}
		fmt.Printf("[%s] %s (top1=%v top3=%v citePrec=%.2f refused=%v) violations=%v\n",
			id, status, grade.Top1CauseHit, grade.Top3CauseHit, grade.CitationPrecision, grade.RefusedCorrectly, grade.Violations)
	}

	writeJSON(filepath.Join(runDir, "results.json"), results)
	summarize(results)
	fmt.Printf("\nfull results + raw model output saved to %s\n", runDir)
}

// buildEventsText redacts every event to its handle form (internal/ai
// never sees a case's own ID scheme, only plain events) and, for a
// "-deidentified" case, applies the same real-address substitution table
// ai/eval/gen/main.go used to build that case's QuestionEn/QuestionAr/
// Expected fields in the first place - see buildDeidentifyTable's own
// comment for why the raw events loaded here still need it applied
// separately.
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

// buildDeidentifyTable replicates ai/eval/gen/main.go's deidentify table
// construction exactly: scan events in slice order (the same order both
// this program and the generator load them from the same events.json file,
// so this always produces the identical table gen used), and assign each
// distinct 10.99.x.x subject.label its own sequential <HOST_N> placeholder,
// first-seen order. Must stay in lockstep with gen/main.go's own version -
// if that logic ever changes, this needs the same change, or the two
// programs' <HOST_N> numbering will silently diverge. This is a corpus-
// authoring convenience specific to this lab's fixed subnet, not the
// product's own redaction policy (internal/redact, general RFC1918/MAC),
// which is why it stays here rather than moving into internal/ai.
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

// deidentify is byte-for-byte the same substitution ai/eval/gen/main.go
// applies to QuestionEn/QuestionAr/Expected.RootCauseEntity - applied here
// to the serialized event JSON instead, so a "-deidentified" case's context
// doesn't leak the real addresses its own question and expected answer
// already hide.
func deidentify(s string, table map[string]string) string {
	for real, placeholder := range table {
		s = strings.ReplaceAll(s, real, placeholder)
	}
	return s
}

// loadScenarioEvents loads from corpus/v1/<scenario>/ by default, or from
// eventsOverride (a repo-root-relative path) when the case sets one -
// currently only the synthetic held-out adversarial case; see
// schema.Case.EventsOverride and ai/eval/synthetic/README.md.
func loadScenarioEvents(root, scenario, eventsOverride string) []map[string]any {
	path := filepath.Join(root, "corpus", "v1", scenario, "events.json")
	if eventsOverride != "" {
		// eventsOverride is stored with forward slashes in the case JSON
		// (portable across the Windows/Linux machines this benchmark
		// actually runs on) - filepath.FromSlash converts to this OS's
		// separator before joining.
		path = filepath.Join(root, filepath.FromSlash(eventsOverride))
	}
	var events []map[string]any
	readJSON(path, &events)
	return events
}

func summarize(results []runResult) {
	var graded, passed, top1, top3, refusedCorrectly, invalid int
	var precisionSum float64
	var precisionN int
	for _, r := range results {
		if r.Grade == nil {
			invalid++
			continue
		}
		graded++
		if r.Grade.Pass() {
			passed++
		}
		if r.Grade.Top1CauseHit {
			top1++
		}
		if r.Grade.Top3CauseHit {
			top3++
		}
		if r.Grade.RefusedCorrectly {
			refusedCorrectly++
		}
		if r.Grade.CitationPrecision > 0 || len(r.Output.EvidenceHandles) > 0 {
			precisionSum += r.Grade.CitationPrecision
			precisionN++
		}
	}
	fmt.Printf("\n=== summary: %d cases, %d produced valid schema JSON, %d invalid/failed ===\n", len(results), graded, invalid)
	fmt.Printf("pass (no hard violations): %d/%d\n", passed, graded)
	fmt.Printf("top-1 cause hit: %d/%d\n", top1, graded)
	fmt.Printf("top-3 cause hit: %d/%d\n", top3, graded)
	fmt.Printf("correct refusals: %d\n", refusedCorrectly)
	if precisionN > 0 {
		fmt.Printf("mean citation precision (non-empty citations): %.2f\n", precisionSum/float64(precisionN))
	}
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

func writeJSON(path string, v any) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "run:", err)
	os.Exit(1)
}
