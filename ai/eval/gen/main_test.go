package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func readSplits(t *testing.T, path string) map[string][]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var splits map[string][]string
	if err := json.Unmarshal(data, &splits); err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}
	return splits
}

func bucketOf(splits map[string][]string, id string) string {
	for bucket, ids := range splits {
		for _, i := range ids {
			if i == id {
				return bucket
			}
		}
	}
	return ""
}

func TestWriteSplitsBootstrapsPinsWhenNoneExist(t *testing.T) {
	dir := t.TempDir()
	splitsPath := filepath.Join(dir, "splits.json")
	scenarioIDs := map[string][]string{
		"alpha": {"alpha-case"},
		"beta":  {"beta-case"},
	}

	writeSplits(splitsPath, scenarioIDs)

	pins := map[string]string{}
	data, err := os.ReadFile(filepath.Join(dir, "split_pins.json"))
	if err != nil {
		t.Fatalf("split_pins.json was not created: %v", err)
	}
	if err := json.Unmarshal(data, &pins); err != nil {
		t.Fatalf("decoding split_pins.json: %v", err)
	}
	if len(pins) != 2 {
		t.Fatalf("pins = %v, want exactly 2 entries", pins)
	}
	if _, ok := pins["alpha"]; !ok {
		t.Errorf("pins = %v, want an entry for alpha", pins)
	}
	if _, ok := pins["beta"]; !ok {
		t.Errorf("pins = %v, want an entry for beta", pins)
	}
}

// TestWriteSplitsNeverMovesAnAlreadyPinnedScenario is the pin the plan
// requires: adding a brand-new scenario that sorts alphabetically BEFORE
// every already-pinned scenario must not shift any of their bucket
// assignments - the exact failure mode an index-based (unpinned) scheme
// has, since "aaa_new" would take index 0 and push every later scenario's
// modulo-4 index up by one.
func TestWriteSplitsNeverMovesAnAlreadyPinnedScenario(t *testing.T) {
	dir := t.TempDir()
	splitsPath := filepath.Join(dir, "splits.json")
	pinsPath := filepath.Join(dir, "split_pins.json")

	// Pre-existing pins, as if from an earlier generation run.
	existingPins := map[string]string{"alpha": "test", "beta": "train", "gamma": "dev"}
	data, err := json.Marshal(existingPins)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pinsPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	scenarioIDs := map[string][]string{
		"alpha":    {"alpha-case"},
		"beta":     {"beta-case"},
		"gamma":    {"gamma-case"},
		"aaa_new":  {"aaa-new-case"}, // sorts before every pinned scenario
		"zzz_also": {"zzz-also-case"},
	}

	writeSplits(splitsPath, scenarioIDs)

	splits := readSplits(t, splitsPath)
	if got := bucketOf(splits, "alpha-case"); got != "test" {
		t.Errorf("alpha-case landed in %q, want it to stay pinned to test", got)
	}
	if got := bucketOf(splits, "beta-case"); got != "train" {
		t.Errorf("beta-case landed in %q, want it to stay pinned to train", got)
	}
	if got := bucketOf(splits, "gamma-case"); got != "dev" {
		t.Errorf("gamma-case landed in %q, want it to stay pinned to dev", got)
	}

	// The two new scenarios must have been pinned too, so a THIRD
	// generation run treats them exactly like any other already-pinned
	// scenario from here on.
	pinsData, err := os.ReadFile(pinsPath)
	if err != nil {
		t.Fatalf("reading updated split_pins.json: %v", err)
	}
	var pins map[string]string
	if err := json.Unmarshal(pinsData, &pins); err != nil {
		t.Fatal(err)
	}
	if pins["alpha"] != "test" || pins["beta"] != "train" || pins["gamma"] != "dev" {
		t.Errorf("pins = %v, an already-pinned scenario's bucket changed", pins)
	}
	if _, ok := pins["aaa_new"]; !ok {
		t.Errorf("pins = %v, want aaa_new to have been assigned a pin", pins)
	}
	if _, ok := pins["zzz_also"]; !ok {
		t.Errorf("pins = %v, want zzz_also to have been assigned a pin", pins)
	}
}

// TestWriteSplitsHoldsOutAlwaysTestCaseIDsRegardlessOfPin proves a case
// named in alwaysTestCaseIDs lands in "test" even when its own scenario is
// pinned to "train" - the override alwaysTestCaseIDs exists for keeps
// working now that pins (not a scenario's live index) decide the default
// bucket.
func TestWriteSplitsHoldsOutAlwaysTestCaseIDsRegardlessOfPin(t *testing.T) {
	alwaysTestCaseIDs["synthetic-always-test-case"] = true
	defer delete(alwaysTestCaseIDs, "synthetic-always-test-case")

	dir := t.TempDir()
	splitsPath := filepath.Join(dir, "splits.json")
	pinsPath := filepath.Join(dir, "split_pins.json")
	data, err := json.Marshal(map[string]string{"delta": "train"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pinsPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	scenarioIDs := map[string][]string{
		"delta": {"delta-case", "synthetic-always-test-case"},
	}
	writeSplits(splitsPath, scenarioIDs)

	splits := readSplits(t, splitsPath)
	if got := bucketOf(splits, "delta-case"); got != "train" {
		t.Errorf("delta-case landed in %q, want train (its scenario's pin)", got)
	}
	if got := bucketOf(splits, "synthetic-always-test-case"); got != "test" {
		t.Errorf("synthetic-always-test-case landed in %q, want test regardless of its scenario's pin", got)
	}
}
