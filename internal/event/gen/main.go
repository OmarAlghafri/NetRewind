// Command gen writes desktop/src/i18n/generated/kinds.json from
// internal/event/kinds.go, the same way ai/eval/gen writes ai/eval/cases/*.json
// from corpus/v1/: a checked-in file derived from source, not hand-maintained,
// with a test on the other side (internal/event/kind_catalogue_test.go) that
// fails if the two drift apart.
//
// ADR 0004: "A generated event-kind catalogue mirrors internal/event/kinds.go's
// 49 kinds ... into a GUI-side table giving each kind a human name in both
// languages with the technical code shown beneath - generated, not
// hand-maintained, so a new Go kind cannot silently ship without a matching
// GUI entry." This program is the generator half of that sentence; the human
// names live by hand in desktop/src/i18n/kindCatalogue.ts, which a Vitest test
// checks against this file's output.
//
// Run it after adding, renaming or removing a Kind or a Family:
//
//	go run ./internal/event/gen
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// catalogue is the shape desktop/src/i18n/kindCatalogue.test.ts reads. Kinds
// are sorted for a stable diff; families keep kinds.go's declared order, which
// is the order docs/schema.md documents them in.
type catalogue struct {
	Kinds    []string `json:"kinds"`
	Families []string `json:"families"`
}

func main() {
	root, err := repoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}

	kindsPath := filepath.Join(root, "internal", "event", "kinds.go")
	kinds, families, err := parseKindsGo(kindsPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
	if len(kinds) < 30 {
		fmt.Fprintf(os.Stderr, "gen: only %d kinds parsed from %s; the parser has probably broken\n", len(kinds), kindsPath)
		os.Exit(1)
	}
	sort.Strings(kinds)

	out := catalogue{Kinds: kinds, Families: families}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
	data = append(data, '\n')

	dest := filepath.Join(root, "desktop", "src", "i18n", "generated", "kinds.json")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %d kinds and %d families to %s\n", len(kinds), len(families), dest)
}

// parseKindsGo extracts every `X Kind = "..."` constant value and the
// `Families = []string{...}` literal from kinds.go by walking its AST - the
// same technique internal/event/coverage_test.go's declaredKinds uses to keep
// docs/schema.md honest, applied here to the GUI catalogue instead.
func parseKindsGo(path string) (kinds, families []string, err error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", path, err)
	}

	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok || len(spec.Names) != 1 || len(spec.Values) != 1 {
			return true
		}

		if ident, ok := spec.Type.(*ast.Ident); ok && ident.Name == "Kind" {
			if lit, ok := spec.Values[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				kinds = append(kinds, strings.Trim(lit.Value, `"`))
			}
			return true
		}

		if spec.Names[0].Name != "Families" {
			return true
		}
		comp, ok := spec.Values[0].(*ast.CompositeLit)
		if !ok {
			return true
		}
		for _, el := range comp.Elts {
			lit, ok := el.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			families = append(families, strings.Trim(lit.Value, `"`))
		}
		return true
	})

	return kinds, families, nil
}

// repoRoot walks up from the working directory until it finds go.mod, mirroring
// internal/event/coverage_test.go's test-only repoRoot for this non-test binary.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		dir = filepath.Dir(dir)
	}
	return "", fmt.Errorf("could not find the repository root above %s", dir)
}
