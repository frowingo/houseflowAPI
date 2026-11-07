package fausSwitch

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"slices"
	"strings"
	"testing"
)

// engineFiles are the pure rules: no clock, no JSON, no locks, no other
// houseflowApi package. They see only the contract constants and enums of
// definition.go, which shares the package.
var engineFiles = []string{"metrics.go", "course.go", "courseData.go", "camera.go", "startGrid.go", "physics.go"}

var engineImports = []string{"cmp", "math", "slices"}

// simulationFiles add ticks, rewinds and the race end on top of the engine;
// they know no clock, encoding or lock either.
var simulationFiles = []string{"simulation.go", "simulationState.go", "simulationResult.go"}

var simulationImports = []string{"cmp", "errors", "math", "slices"}

// Calls that would round differently from Swift or are not in the Swift engine.
var forbiddenMath = []string{"FMA", "Sin", "Cos", "Tan", "Asin", "Acos", "Atan", "Atan2", "Pow", "Exp", "Log", "Sqrt", "Hypot"}

func TestEngineAndSimulationArePure(t *testing.T) {
	fileSet := token.NewFileSet()
	check := func(names, allowed []string) {
		for _, name := range names {
			file, err := parser.ParseFile(fileSet, name, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, spec := range file.Imports {
				if path := strings.Trim(spec.Path.Value, `"`); !slices.Contains(allowed, path) {
					t.Errorf("%s imports %s; it may import only %v", name, path, allowed)
				}
			}
		}
	}
	check(engineFiles, engineImports)
	check(simulationFiles, simulationImports)
}

// The Go compiler may fuse x*y ± z into one FMA instruction (arm64 does),
// rounding once where Swift rounds twice. Every float product that is added or
// subtracted must go through float64(...), which forbids fusion (metrics.go).
func TestEngineNeverFusesMultiplyAdd(t *testing.T) {
	fileSet := token.NewFileSet()
	var files []*ast.File
	for _, name := range slices.Concat([]string{"definition.go"}, engineFiles, simulationFiles) {
		file, err := parser.ParseFile(fileSet, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
	}
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}}
	standard := importer.ForCompiler(fileSet, "source", nil)
	config := types.Config{
		// Only the standard library is loaded; houseflowApi imports of
		// definition.go fail quietly, which leaves the engine's own types intact.
		Importer: importerFunc(func(path string) (*types.Package, error) {
			if strings.Contains(strings.Split(path, "/")[0], ".") || strings.HasPrefix(path, "houseflowApi") {
				return nil, errUnloaded
			}
			return standard.Import(path)
		}),
		Error: func(error) {},
	}
	config.Check("fausSwitch", fileSet, files, info)
	isFloat := func(expr ast.Expr) bool {
		typed, ok := info.Types[expr]
		if !ok || typed.Value != nil {
			return false // constants are folded exactly, never fused
		}
		basic, ok := typed.Type.Underlying().(*types.Basic)
		return ok && basic.Info()&types.IsFloat != 0
	}
	isProduct := func(expr ast.Expr) bool {
		binary, ok := ast.Unparen(expr).(*ast.BinaryExpr)
		return ok && binary.Op == token.MUL && isFloat(binary)
	}
	for _, file := range files[1:] {
		ast.Inspect(file, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.BinaryExpr:
				if (node.Op == token.ADD || node.Op == token.SUB) && isFloat(node) && (isProduct(node.X) || isProduct(node.Y)) {
					t.Errorf("%s: unguarded multiply-add", fileSet.Position(node.Pos()))
				}
			case *ast.AssignStmt:
				if (node.Tok == token.ADD_ASSIGN || node.Tok == token.SUB_ASSIGN) && isFloat(node.Lhs[0]) && isProduct(node.Rhs[0]) {
					t.Errorf("%s: unguarded multiply-add", fileSet.Position(node.Pos()))
				}
			case *ast.SelectorExpr:
				if name, ok := node.X.(*ast.Ident); ok && name.Name == "math" && slices.Contains(forbiddenMath, node.Sel.Name) {
					t.Errorf("%s: math.%s is not in the Swift engine", fileSet.Position(node.Pos()), node.Sel.Name)
				}
			}
			return true
		})
	}
}

type importerFunc func(path string) (*types.Package, error)

func (load importerFunc) Import(path string) (*types.Package, error) { return load(path) }

var errUnloaded = &types.Error{Msg: "not loaded"}
