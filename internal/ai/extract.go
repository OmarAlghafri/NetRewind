package ai

import (
	"encoding/json"
	"fmt"
)

// ExtractModelOutput finds the LAST top-level (brace-depth-0-to-0) {...}
// JSON object anywhere in raw output and unmarshals it into v - this is the
// model's actual final answer, printed after everything else. See
// ai/eval/run/main.go's git history for the full account of why a single
// LastIndex(raw, "{") is wrong (it can match an inner brace of the model's
// own nested answer) and why brace-depth tracking is what actually finds
// the real top-level object.
func ExtractModelOutput(raw string, v any) error {
	depth := 0
	start := -1
	spanStart, spanEnd := -1, -1
	for i, c := range raw {
		switch c {
		case '{':
			if depth == 0 {
				start = i
			}
			depth++
		case '}':
			if depth == 0 {
				continue // stray/unbalanced closer, ignore rather than going negative
			}
			depth--
			if depth == 0 && start != -1 {
				spanStart, spanEnd = start, i
			}
		}
	}
	if spanStart == -1 {
		return fmt.Errorf("no complete top-level JSON object found in model output")
	}
	if err := json.Unmarshal([]byte(raw[spanStart:spanEnd+1]), v); err != nil {
		return fmt.Errorf("json.Unmarshal(%q): %w", raw[spanStart:spanEnd+1], err)
	}
	return nil
}
