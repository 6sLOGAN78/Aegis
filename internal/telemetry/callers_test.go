package telemetry

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// nonRecorderMethods lists exported *Metrics methods that are not recorders and
// therefore need no production caller. Currently empty: every exported method on
// *Metrics is a recorder/setter. Add entries here with a justification comment
// (e.g. Handler/Registry accessors) if such methods are introduced.
var nonRecorderMethods = map[string]bool{}

// TestRecorderCallers fails if any exported method on *Metrics has no non-test
// caller anywhere in the module. Method-value references such as
// SetBytesWrittenHook(metrics.RecordSpoolBytes) count as uses.
func TestRecorderCallers(t *testing.T) {
	typ := reflect.TypeOf(&Metrics{})
	uses := map[string]int{}
	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		if !nonRecorderMethods[name] {
			uses[name] = 0
		}
	}

	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	selfFile := filepath.Join(root, "internal", "telemetry", "metrics.go")
	fset := token.NewFileSet()
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "vendor", "web", ".git", ".planning", "node_modules", ".claude":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || path == selfFile {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return nil
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if se, ok := n.(*ast.SelectorExpr); ok {
				if _, tracked := uses[se.Sel.Name]; tracked {
					uses[se.Sel.Name]++
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	var dead []string
	for name, n := range uses {
		if n == 0 {
			dead = append(dead, name)
		}
	}
	sort.Strings(dead)
	if len(dead) > 0 {
		t.Fatalf("recorders on *Metrics with no non-test caller: %s", strings.Join(dead, ", "))
	}
}
