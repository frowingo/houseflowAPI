package testkit

import (
	"path/filepath"
	"runtime"
	"testing"
)

// RepoPath resolves a path given relative to the repository root. Test
// packages sit at different depths under tests/, so they never spell "../".
func RepoPath(relative string) string {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		panic("testkit: cannot resolve the repository root")
	}
	return filepath.Join(filepath.Dir(currentFile), "..", "..", filepath.FromSlash(relative))
}

func ApplicationDirectory(t *testing.T) string {
	t.Helper()
	return RepoPath("internal/application")
}
