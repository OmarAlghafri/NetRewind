package event_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// reserved lists the event kinds that are declared but deliberately not
// produced yet, each with the reason.
//
// This list is the point of the test. Twenty-five kinds were once declared and
// produced by nothing at all, including dhcp.server_seen - which the project's
// own README opens with as its headline example. A schema that promises
// capabilities the code does not have is the exact failure this project claims
// to be built against, and a comment saying "planned" does not stop it
// happening again. Adding a kind here should feel like an admission.
var reserved = map[string]string{
	"l2.lldp_neighbor_changed": "needs LLDP, which needs switches; the lab has none",
	"l2.vlan_seen":             "needs a trunk carrying more than one VLAN",
	"flow.retransmit_spike":    "needs the tcp_retransmit_skb tracepoint; not yet attached",
	"policy.drop_burst":        "needs nftables rule counters, not the ruleset",
	"change.config_applied":    "fed by NetIntent or config diffing; neither is wired up",
	"change.device_reboot":     "needs SNMP or a device telling us",
	"change.admin_action":      "needs device authentication logs",
}

// TestEveryKindIsProducedOrReserved walks the source for each declared kind and
// fails if nothing outside the event package emits it.
func TestEveryKindIsProducedOrReserved(t *testing.T) {
	root := repoRoot(t)

	kinds := declaredKinds(t, filepath.Join(root, "internal", "event", "kinds.go"))
	if len(kinds) < 30 {
		t.Fatalf("only %d kinds parsed; the parser has probably broken", len(kinds))
	}

	produced := producersIn(t, root)

	for constName, kind := range kinds {
		if _, ok := reserved[kind]; ok {
			if produced[constName] {
				t.Errorf("%s is listed as reserved but something now produces it - remove it from the list", kind)
			}
			continue
		}
		if !produced[constName] {
			t.Errorf("%s is declared in the schema and produced by nothing.\n"+
				"    Either write the collector, or add it to `reserved` with the reason.", kind)
		}
	}

	// A reserved entry for a kind that no longer exists is stale and hides the
	// next real gap.
	for kind := range reserved {
		found := false
		for _, k := range kinds {
			if k == kind {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("reserved lists %q, which is not a declared kind any more", kind)
		}
	}
}

// declaredKinds returns constant name -> kind string from kinds.go.
func declaredKinds(t *testing.T, path string) map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	out := make(map[string]string)
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok || len(spec.Names) != 1 || len(spec.Values) != 1 {
			return true
		}
		ident, ok := spec.Type.(*ast.Ident)
		if !ok || ident.Name != "Kind" {
			return true
		}
		lit, ok := spec.Values[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		out[spec.Names[0].Name] = strings.Trim(lit.Value, `"`)
		return true
	})
	return out
}

// producersIn reports which Kind constants are referenced outside the event
// package and outside tests - that is, by something that actually records.
func producersIn(t *testing.T, root string) map[string]bool {
	t.Helper()
	produced := make(map[string]bool)

	// internal/update is here because the updater is a collector in every way
	// that matters: it runs under the same supervision and emits events into the
	// same queue. A kind produced only there would otherwise look unproduced.
	//
	// internal/analyze is here because PRODUCT_RELEASE_PLAN_AR.md §4.1 moves
	// the decision logic that builds events out of the collectors themselves
	// and into platform-neutral analyzers, so a collector's own file only
	// translates into internal/ports observations. A kind whose event is
	// built inside an analyzer would otherwise look unproduced the moment the
	// collector it used to live in stopped constructing it directly.
	for _, dir := range []string{"internal/collect", "internal/update", "internal/analyze", "cmd"} {
		err := filepath.Walk(filepath.Join(root, dir), func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, ref := range findKindRefs(string(src)) {
				produced[ref] = true
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	return produced
}

// findKindRefs pulls every `event.KindSomething` out of a file.
func findKindRefs(src string) []string {
	const prefix = "event.Kind"
	var out []string
	for i := 0; i+len(prefix) < len(src); {
		j := strings.Index(src[i:], prefix)
		if j < 0 {
			break
		}
		start := i + j + len(prefix)
		end := start
		for end < len(src) && (isIdentRune(src[end])) {
			end++
		}
		if end > start {
			out = append(out, "Kind"+src[start:end])
		}
		i = end
	}
	return out
}

func isIdentRune(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
}

// repoRoot walks up from the test's directory until it finds go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not find the repository root")
	return ""
}
