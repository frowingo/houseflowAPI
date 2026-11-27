package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The worker command runs setupBackgroundJobs alone, so a job handler or
// periodic job bound anywhere else in internal/bootstrap would exist on API machines
// only and its jobs would never run with a worker group.
func TestJobHandlersAreBoundOnlyInBackgroundJobs(t *testing.T) {
	directory := filepath.Join(repositoryRoot(t), "internal", "bootstrap")
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	bindings := map[string]int{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(directory, entry.Name()), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (selector.Sel.Name != "Register" && selector.Sel.Name != "Every") {
				return true
			}
			bound := isIdent(selector.X, "jobRuntime")
			for _, argument := range call.Args {
				bound = bound || isIdent(argument, "jobRuntime")
			}
			if bound {
				bindings[entry.Name()]++
			}
			return true
		})
	}
	if bindings["backgroundJobs.go"] == 0 {
		t.Fatal("backgroundJobs.go binds no job handlers; the contract no longer finds them")
	}
	for name, count := range bindings {
		if name != "backgroundJobs.go" {
			t.Errorf("internal/bootstrap/%s binds %d job handler(s); bind them in setupBackgroundJobs so the worker has them", name, count)
		}
	}
}

func isIdent(expression ast.Expr, name string) bool {
	ident, ok := expression.(*ast.Ident)
	return ok && ident.Name == name
}
