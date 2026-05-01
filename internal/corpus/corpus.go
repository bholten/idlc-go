// Package corpus locates the Core3-derived test corpus on disk and
// helps tests skip cleanly when it isn't present.
//
// The corpus (`testdata/idl/`, `testdata/autogen/`, `testdata/mock/`)
// is copied from Core3 by `scripts/fetch-corpus-from-core3.sh` and is
// .gitignored — Core3 sources are licensed and can't be redistributed
// as part of idlc-go. Tests that depend on the corpus call
// `corpus.RequireOrSkip(t)` at the top so they no-op gracefully when
// a contributor hasn't run the fetch step.
package corpus

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// RepoRoot returns the absolute path to the idlc-go repository root,
// resolved from this file's location. Useful from anywhere under
// `internal/`.
func RepoRoot() string {
	_, here, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(here), "..", ".."))
}

// IDLDir returns the corpus IDL directory: `<repo>/testdata/idl`.
func IDLDir() string { return filepath.Join(RepoRoot(), "testdata", "idl") }

// AutogenDir returns the corpus autogen root: `<repo>/testdata/autogen`.
func AutogenDir() string { return filepath.Join(RepoRoot(), "testdata", "autogen") }

// MockDir returns the corpus mock fixtures dir: `<repo>/testdata/mock`.
func MockDir() string { return filepath.Join(RepoRoot(), "testdata", "mock") }

// Available reports whether the corpus is present (any .idl in IDLDir).
func Available() bool {
	entries, err := os.ReadDir(IDLDir())
	if err != nil {
		return false
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".idl" {
			return true
		}
	}
	return false
}

// RequireOrSkip skips the calling test with an explanatory message when
// the Core3 corpus is not available. Use at the top of tests that read
// `testdata/idl/` or `testdata/autogen/`.
func RequireOrSkip(t *testing.T) {
	t.Helper()
	if !Available() {
		t.Skip("Core3 corpus not present (run `make fetch-corpus` with CORE3_PATH set, or pull the Core3 submodule)")
	}
}
