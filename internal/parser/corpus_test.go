package parser

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/bholten/idlc-go/internal/corpus"
)

func TestParseCorpus(t *testing.T) {
	corpus.RequireOrSkip(t)
	_, here, _, _ := runtime.Caller(0)
	idlDir := filepath.Join(filepath.Dir(here), "..", "..", "testdata", "idl")
	entries, err := os.ReadDir(idlDir)

	if err != nil {
		t.Fatal(err)
	}

	count := 0

	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".idl" {
			continue
		}

		count++
		path := filepath.Join(idlDir, e.Name())

		t.Run(e.Name(), func(t *testing.T) {
			src, err := os.ReadFile(path)

			if err != nil {
				t.Fatal(err)
			}

			f, err := Parse(e.Name(), src)

			if err != nil {
				t.Fatalf("parse %s: %v", e.Name(), err)
			}

			if f.Class == nil {
				t.Fatalf("%s: no class produced", e.Name())
			}

			if f.Package == "" {
				t.Errorf("%s: empty package", e.Name())
			}

			// Sanity: an extends-only class (e.g. ManagedService =
			// `class ManagedService extends ManagedObject {}`) is a
			// legitimate IDL — those have 0 members but a Base set.
			// Anything else with 0 members is suspicious.
			if len(f.Class.Members) == 0 && f.Class.Base == "" {
				t.Errorf("%s: class %q has 0 members and no extends clause", e.Name(), f.Class.Name)
			}
		})
	}

	if count == 0 {
		t.Fatal("no .idl files found")
	}

	t.Logf("parsed %d IDL files", count)
}
