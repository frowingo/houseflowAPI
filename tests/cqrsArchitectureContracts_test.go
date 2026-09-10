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
			if importPath == "houseflowApi/internal/abstract" {
				t.Errorf("application layer file %s imports removed repository implementation %q", path, importPath)
			}
			if importPath == "houseflowApi/internal/data/database" {
				t.Errorf("application layer file %s imports concrete database context %q", path, importPath)
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

func TestMigratedControllersUseCQRSWithoutLegacyServices(t *testing.T) {
	internalDirectory := filepath.Join(applicationDirectory(t), "..")
	controllerDirectory := filepath.Join(internalDirectory, "controllers")
	controllerFiles, err := os.ReadDir(controllerDirectory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range controllerFiles {
		fileName := entry.Name()
		if entry.IsDir() || filepath.Ext(fileName) != ".go" || fileName == "baseController.go" {
			continue
		}
		path := filepath.Join(controllerDirectory, fileName)
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}

		hasCQRSDependency := false
		for _, imported := range parsed.Imports {
			importPath, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if importPath == "houseflowApi/internal/services" {
				t.Errorf("migrated controller %s imports legacy services", path)
			}
			if importPath == "houseflowApi/internal/infrastructure/cqrs" {
				hasCQRSDependency = true
			}
		}
		if !hasCQRSDependency {
			t.Errorf("migrated controller %s does not import CQRS sender", path)
		}
	}
}

func TestEveryApplicationRequestIsRegisteredInCompositionRoot(t *testing.T) {
	requests := applicationRequests(t)
	registrations := registeredRequests(t)

	for requestID, path := range requests {
		if _, ok := registrations[requestID]; !ok {
			t.Errorf("application request %s declared in %s is not registered in cmd/api/routes.go", requestID, path)
		}
	}
	for requestID := range registrations {
		if _, ok := requests[requestID]; !ok {
			t.Errorf("cmd/api/routes.go registers unknown application request %s", requestID)
		}
	}
}

func TestServiceLayerContainsOnlyNotificationService(t *testing.T) {
	servicesDirectory := filepath.Join(applicationDirectory(t), "..", "services")
	entries, err := os.ReadDir(servicesDirectory)
	if err != nil {
		t.Fatal(err)
	}
	foundNotificationService := false
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" {
			continue
		}
		if entry.Name() == "notificationService.go" {
			foundNotificationService = true
			continue
		}
		t.Errorf("legacy business service file remains: %s", filepath.Join(servicesDirectory, entry.Name()))
	}
	if !foundNotificationService {
		t.Error("notificationService.go must remain as the notification adapter")
	}
}

func applicationRequests(t *testing.T) map[string]string {
	t.Helper()
	requests := make(map[string]string)
	err := filepath.WalkDir(applicationDirectory(t), func(path string, entry os.DirEntry, walkErr error) error {
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
			generic, ok := declaration.(*ast.GenDecl)
			if !ok || generic.Tok != token.TYPE {
				continue
			}
			for _, specification := range generic.Specs {
				typeSpecification, ok := specification.(*ast.TypeSpec)
				if !ok || !embedsCQRSRequest(typeSpecification.Type) {
					continue
				}

				requestName := typeSpecification.Name.Name
				requestDirectory := filepath.Base(filepath.Dir(path))
				expectedSuffix := ""
				switch requestDirectory {
				case "commands":
					expectedSuffix = "Command"
				case "queries":
					expectedSuffix = "Query"
				default:
					t.Errorf("application request %s must be declared under commands or queries: %s", requestName, path)
				}
				if expectedSuffix != "" && !strings.HasSuffix(requestName, expectedSuffix) {
					t.Errorf("application request %s in %s must end with %s", requestName, path, expectedSuffix)
				}
				relativeDirectory, err := filepath.Rel(applicationDirectory(t), filepath.Dir(path))
				if err != nil {
					return err
				}
				requestID := filepath.ToSlash(relativeDirectory) + "." + requestName
				requests[requestID] = path
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return requests
}

func registeredRequests(t *testing.T) map[string]struct{} {
	t.Helper()
	routesPath := filepath.Join(applicationDirectory(t), "..", "..", "cmd", "api", "routes.go")
	parsed, err := parser.ParseFile(token.NewFileSet(), routesPath, nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	applicationImports := make(map[string]string)
	const applicationImportPrefix = "houseflowApi/internal/application/"
	for _, imported := range parsed.Imports {
		importPath, err := strconv.Unquote(imported.Path.Value)
		if err != nil || !strings.HasPrefix(importPath, applicationImportPrefix) {
			continue
		}
		alias := filepath.Base(importPath)
		if imported.Name != nil {
			alias = imported.Name.Name
		}
		applicationImports[alias] = strings.TrimPrefix(importPath, applicationImportPrefix)
	}

	registrations := make(map[string]struct{})
	ast.Inspect(parsed, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		indexed, ok := call.Fun.(*ast.IndexListExpr)
		if !ok || len(indexed.Indices) != 2 {
			return true
		}
		selector, ok := indexed.X.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "MustRegister" {
			return true
		}
		packageName, ok := selector.X.(*ast.Ident)
		if !ok || packageName.Name != "cqrs" {
			return true
		}

		requestID := registeredRequestID(indexed.Indices[1], applicationImports)
		if requestID == "" {
			t.Errorf("cannot resolve registered request type at %s", routesPath)
			return true
		}
		if _, exists := registrations[requestID]; exists {
			t.Errorf("request %s is registered more than once in %s", requestID, routesPath)
		}
		registrations[requestID] = struct{}{}
		return true
	})
	return registrations
}

func embedsCQRSRequest(expression ast.Expr) bool {
	structure, ok := expression.(*ast.StructType)
	if !ok {
		return false
	}
	for _, field := range structure.Fields.List {
		indexed, ok := field.Type.(*ast.IndexExpr)
		if !ok {
			continue
		}
		selector, ok := indexed.X.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == "Request" {
			if packageName, ok := selector.X.(*ast.Ident); ok && packageName.Name == "cqrs" {
				return true
			}
		}
	}
	return false
}

func registeredRequestID(expression ast.Expr, applicationImports map[string]string) string {
	switch value := expression.(type) {
	case *ast.SelectorExpr:
		packageName, ok := value.X.(*ast.Ident)
		if !ok {
			return ""
		}
		importPath, ok := applicationImports[packageName.Name]
		if !ok {
			return ""
		}
		return importPath + "." + value.Sel.Name
	case *ast.Ident:
		return value.Name
	case *ast.StarExpr:
		return registeredRequestID(value.X, applicationImports)
	default:
		return ""
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
