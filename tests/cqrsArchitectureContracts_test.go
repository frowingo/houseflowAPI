package tests

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"houseflowApi/internal/infrastructure/cqrs"
)

type architectureTestCommand struct{}
type architectureTestQuery struct{}
type architectureTestResult struct{}

type architectureTestCommandHandler struct{}

func (architectureTestCommandHandler) Handle(context.Context, architectureTestCommand) (architectureTestResult, error) {
	return architectureTestResult{}, nil
}

type architectureTestQueryHandler struct{}

func (architectureTestQueryHandler) Handle(context.Context, architectureTestQuery) (architectureTestResult, error) {
	return architectureTestResult{}, nil
}

var _ cqrs.CommandHandler[architectureTestCommand, architectureTestResult] = architectureTestCommandHandler{}
var _ cqrs.QueryHandler[architectureTestQuery, architectureTestResult] = architectureTestQueryHandler{}

func TestApplicationLayerHasNoTransportOrLegacyServiceDependencies(t *testing.T) {
	applicationDirectory := applicationDirectory(t)
	forbiddenImports := []string{
		"github.com/gofiber/fiber",
		"houseflowApi/internal/controllers",
		"houseflowApi/internal/services",
	}

	err := filepath.WalkDir(applicationDirectory, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}

		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imported := range parsed.Imports {
			importPath, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				return err
			}
			for _, forbidden := range forbiddenImports {
				if strings.HasPrefix(importPath, forbidden) {
					t.Errorf("application layer file %s imports forbidden dependency %q", path, importPath)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestApplicationHandlersFollowContextFirstSignature(t *testing.T) {
	applicationDirectory := applicationDirectory(t)

	err := filepath.WalkDir(applicationDirectory, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv == nil || function.Name.Name != "Handle" {
				continue
			}
			if !isApplicationHandlerSignature(function.Type) {
				t.Errorf("%s Handle must have signature Handle(context.Context, Request) (Result, error)", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func applicationDirectory(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve architecture test path")
	}
	return filepath.Join(filepath.Dir(currentFile), "..", "internal", "application")
}

func isApplicationHandlerSignature(function *ast.FuncType) bool {
	if function.Params == nil || function.Results == nil {
		return false
	}
	if fieldCount(function.Params.List) != 2 || fieldCount(function.Results.List) != 2 {
		return false
	}
	if !isContextType(function.Params.List[0].Type) {
		return false
	}
	return isErrorType(function.Results.List[len(function.Results.List)-1].Type)
}

func fieldCount(fields []*ast.Field) int {
	count := 0
	for _, field := range fields {
		if len(field.Names) == 0 {
			count++
		} else {
			count += len(field.Names)
		}
	}
	return count
}

func isContextType(expression ast.Expr) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Context" {
		return false
	}
	identifier, ok := selector.X.(*ast.Ident)
	return ok && identifier.Name == "context"
}

func isErrorType(expression ast.Expr) bool {
	identifier, ok := expression.(*ast.Ident)
	return ok && identifier.Name == "error"
}
