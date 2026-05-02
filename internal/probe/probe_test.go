// Package probe_test runs idlc-go against the synthetic probe IDLs in
// testdata/probe/src/probe/ and diffs the output against the JAR-emitted
// goldens in testdata/probe/expected/probe/. The probes target specific
// annotation combinations (locking matrix, return-modifier matrix, etc.)
// to surface bugs that the natural Core3 corpus doesn't exercise.
//
// Refresh goldens via `scripts/gen-probe-goldens.sh`. Mismatches dump
// `<path>.got` next to the expected file for offline diffing.
package probe_test

import (
	"bytes"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bholten/tools/idlc-go/internal/emit/cpp"
	"github.com/bholten/tools/idlc-go/internal/parser"
	"github.com/bholten/tools/idlc-go/internal/sema"
)

var update = flag.Bool("update", false, "write generated output to testdata/probe/expected/ instead of diffing")

func repoRoot(t *testing.T) string {
	t.Helper()
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(here), "..", ".."))
}

// TestProbes walks every probe IDL, runs it through idlc-go, and diffs
// each generated .h/.cpp against the JAR-emitted golden. Each probe is
// reported as a sub-test so a failure points at the specific IDL.
func TestProbes(t *testing.T) {
	root := repoRoot(t)
	srcDir := filepath.Join(root, "testdata", "probe", "src", "probe")
	expectedDir := filepath.Join(root, "testdata", "probe", "expected", "probe")

	entries, err := os.ReadDir(srcDir)
	if err != nil {
		t.Fatal(err)
	}

	reg := buildProbeRegistry(t, root)

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".idl") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".idl")
		t.Run(name, func(t *testing.T) {
			runProbe(t, srcDir, expectedDir, name, reg)
		})
	}
}

// buildProbeRegistry mirrors the JAR's view of the probe IDL universe.
// The probe sandbox passes `-cp <engine3 src>` to the JAR (matching
// Core3's CMake invocation), so the JAR resolves `system.util.Vector`,
// `system.lang.ref.Reference`, etc. via engine3's real C++ headers —
// and treats them as non-IDL utility types. Only `engine.core.ManagedObject`
// (which has an actual .idl in engine3) and the probe classes that
// extend it are IDL-managed.
func buildProbeRegistry(t *testing.T, root string) *sema.Registry {
	t.Helper()
	reg := sema.NewRegistry()
	for _, qname := range []string{
		"engine.core.ManagedObject",
		"probe.Cast",
		"probe.ChainedCall",
		"probe.Constants",
		"probe.DerefManaged",
		"probe.Dispatch",
		"probe.Fields",
		"probe.Generics",
		"probe.Inheritance",
		"probe.Json",
		"probe.LocalVar",
		"probe.Locking",
		"probe.NestedGenerics",
		"probe.ParentFields",
		"probe.ParentFieldsBase",
		"probe.Params",
		"probe.Returns",
		"probe.WeakRef",
	} {
		reg.Add(qname)
	}
	// Also populate classMeta (parent name + per-class field annotations)
	// so the body rewriter's inherited-field lookup works in the probe
	// sandbox. The manual Add calls above already classify each probe
	// class; this pass walks the same probe IDLs and records their
	// fields.
	if err := reg.LoadFromDir(filepath.Join(root, "testdata", "probe", "src", "probe")); err != nil {
		t.Fatal(err)
	}
	return reg
}

func runProbe(t *testing.T, srcDir, expectedDir, name string, reg *sema.Registry) {
	t.Helper()
	idlPath := filepath.Join(srcDir, name+".idl")
	wantHeader := filepath.Join(expectedDir, name+".h")
	wantSource := filepath.Join(expectedDir, name+".cpp")

	src, err := os.ReadFile(idlPath)
	if err != nil {
		t.Fatal(err)
	}
	f, err := parser.Parse(name+".idl", src)
	if err != nil {
		t.Fatal(err)
	}
	m, err := sema.Resolve(f)
	if err != nil {
		t.Fatal(err)
	}
	gotH, gotC, err := cpp.Generate(m, reg)
	if err != nil {
		t.Fatal(err)
	}

	checkOrUpdate(t, wantHeader, gotH)
	checkOrUpdate(t, wantSource, gotC)
}

func checkOrUpdate(t *testing.T, path string, got []byte) {
	t.Helper()
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(want, got) {
		return
	}
	dump := path + ".got"
	_ = os.WriteFile(dump, got, 0o644)
	if d := unifiedDiff(want, got); d != "" {
		t.Errorf("%s mismatch (got dumped to %s):\n%s", filepath.Base(path), dump, d)
		return
	}
	t.Errorf("%s mismatch (got dumped to %s, %d bytes want vs %d bytes got)",
		filepath.Base(path), dump, len(want), len(got))
}

func unifiedDiff(want, got []byte) string {
	if _, err := exec.LookPath("diff"); err != nil {
		return ""
	}
	wantTmp, _ := os.CreateTemp("", "want-*")
	gotTmp, _ := os.CreateTemp("", "got-*")
	defer os.Remove(wantTmp.Name())
	defer os.Remove(gotTmp.Name())
	wantTmp.Write(want)
	gotTmp.Write(got)
	wantTmp.Close()
	gotTmp.Close()
	cmd := exec.Command("diff", "-u", wantTmp.Name(), gotTmp.Name())
	out, _ := cmd.Output()
	return string(out)
}
