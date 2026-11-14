package architecture

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"houseflowApi/tests/testkit"
)

// These tests read the repository's sources and deployment files instead of
// importing them; scripts/testAffected.sh runs this package whenever such a
// file changes.

const concreteGamePackagePrefix = "houseflowApi/internal/application/game/games/"

// Only the composition root and a game's own package name a concrete game; the
// core, transport, persistence and controllers depend on the module contract.
func TestOnlyGameCompositionImportsConcreteGames(t *testing.T) {
	root := filepath.Join(testkit.ApplicationDirectory(t), "..", "..")
	composition := filepath.Join(root, "internal", "application", "game", "games")
	for _, directory := range []string{filepath.Join(root, "internal"), filepath.Join(root, "cmd")} {
		err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil || entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
				return walkErr
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
				if !strings.HasPrefix(importPath, concreteGamePackagePrefix) {
					continue
				}
				gameDirectory := filepath.Join(composition, strings.Split(strings.TrimPrefix(importPath, concreteGamePackagePrefix), "/")[0])
				if filepath.Dir(path) != composition && !strings.HasPrefix(path, gameDirectory+string(filepath.Separator)) {
					relative, _ := filepath.Rel(root, path)
					t.Errorf("%s imports concrete game %s", relative, importPath)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestMigrationsRunOutsideAPIStartup(t *testing.T) {
	repositoryRoot := repositoryRoot(t)
	apiMain := readContractFile(t, filepath.Join(repositoryRoot, "cmd", "api", "main.go"))
	migrationCommand := readContractFile(t, filepath.Join(repositoryRoot, "cmd", "api", "migrate.go"))
	flyConfig := readContractFile(t, filepath.Join(repositoryRoot, "fly.toml"))

	if strings.Contains(apiMain, "migration.RunAll") {
		t.Fatal("API startup still runs database migrations")
	}
	if !strings.Contains(migrationCommand, "migrations.RunAll") {
		t.Fatal("migration command does not run registered migrations")
	}
	if !strings.Contains(flyConfig, "release_command = './houseflowapi migrate'") {
		t.Fatal("Fly deployment does not use the migration release command")
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	return testkit.RepoPath(".")
}

func readContractFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

// Fiber matches routes in registration order, so the catalog routes must come
// before "/:id" or PUT /chore/catalog would reach UpdateChore.
func TestChoreCatalogRoutesPrecedeChoreIDRoute(t *testing.T) {
	routes := readContractFile(t, filepath.Join(repositoryRoot(t), "cmd", "api", "routes.go"))
	idRoute := strings.Index(routes, `choreRoutes.Put("/:id"`)
	if idRoute < 0 {
		t.Fatal(`choreRoutes.Put("/:id") not found`)
	}
	for _, route := range []string{`choreRoutes.Get("/catalog"`, `choreRoutes.Put("/catalog"`} {
		index := strings.Index(routes, route)
		if index < 0 || index > idRoute {
			t.Errorf("%s must be registered before the chore id route", route)
		}
	}
}
