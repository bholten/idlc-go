package parser

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/bholten/tools/idlc-go/internal/corpus"
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

			// Sanity: every test IDL has at least one member.
			if len(f.Class.Members) == 0 {
				t.Errorf("%s: class %q has 0 members", e.Name(), f.Class.Name)
			}
		})
	}

	if count == 0 {
		t.Fatal("no .idl files found")
	}

	t.Logf("parsed %d IDL files", count)
}
