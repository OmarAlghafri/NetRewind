package event_test

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// generatedCatalogue is desktop/src/i18n/generated/kinds.json's shape -
// duplicated from internal/event/gen/main.go's catalogue struct rather than
// imported, because that package is `main` and not meant to be imported; the
// two are kept in step by this test comparing their actual output, not by
// sharing a type.
type generatedCatalogue struct {
	Kinds    []string `json:"kinds"`
	Families []string `json:"families"`
}

// TestTheGeneratedKindCatalogueIsUpToDate keeps
// desktop/src/i18n/generated/kinds.json in step with kinds.go the same way
// TestTheSchemaDocumentListsEveryKind keeps docs/schema.md in step with it:
// by parsing kinds.go itself as the one source of truth and comparing, rather
// than trusting that whoever added a Kind remembered to run the generator.
//
// ADR 0004 requires this: "a new Go kind cannot silently ship without a
// matching GUI entry." A stale generated file would otherwise pass every Go
// test while the GUI catalogue quietly falls behind.
func TestTheGeneratedKindCatalogueIsUpToDate(t *testing.T) {
	root := repoRoot(t)

	kinds := declaredKinds(t, filepath.Join(root, "internal", "event", "kinds.go"))
	wantKinds := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		wantKinds = append(wantKinds, kind)
	}
	sort.Strings(wantKinds)

	wantFamilies := declaredFamilies(t, filepath.Join(root, "internal", "event", "kinds.go"))

	genPath := filepath.Join(root, "desktop", "src", "i18n", "generated", "kinds.json")
	data, err := os.ReadFile(genPath)
	if err != nil {
		t.Fatalf("read %s: %v (run `go run ./internal/event/gen` first)", genPath, err)
	}
	var got generatedCatalogue
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("parse %s: %v", genPath, err)
	}

	if !equalStrings(got.Kinds, wantKinds) {
		t.Errorf("%s's kinds are stale against kinds.go - run `go run ./internal/event/gen`\n  got:  %v\n  want: %v",
			genPath, got.Kinds, wantKinds)
	}
	if !equalStrings(got.Families, wantFamilies) {
		t.Errorf("%s's families are stale against kinds.go - run `go run ./internal/event/gen`\n  got:  %v\n  want: %v",
			genPath, got.Families, wantFamilies)
	}
}

// declaredFamilies returns the Families var's string elements from kinds.go,
// in declared order - the same order docs/schema.md documents them in.
func declaredFamilies(t *testing.T, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	var out []string
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok || len(spec.Names) != 1 || spec.Names[0].Name != "Families" || len(spec.Values) != 1 {
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
			out = append(out, strings.Trim(lit.Value, `"`))
		}
		return true
	})
	if len(out) == 0 {
		t.Fatalf("no Families literal found in %s; the parser or the source has changed shape", path)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
