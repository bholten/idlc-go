// extract-seeds reads the JAR baseline at _baseline/jar/ and emits Go
// map entries for every first-method RPC seed found. Pipe the output
// into the `legacyRPCSeeds` declaration in internal/sema/rpc.go.
//
// For each .cpp in the baseline:
//  1. Read the `enum {RPC_X__SUFFIX_ = NNN, …};` line — extract the seed.
//  2. Find the corresponding .idl (under <CORE3_PATH>/MMOCoreORB/src or
//     /MMOCoreORB/utils/engine3/MMOEngine/src).
//  3. Parse + resolve the IDL with idlc-go's own parser/sema.
//  4. Find the first method that participates in the RPC enum (public,
//     non-@local) — that's the one carrying the seed.
//  5. Emit the legacy-seed-map key: "<pkg>.<Class>.<methodName>(<types>)": <seed>,
//
// Run from the idlc-go repo root:
//
//	go run ./cmd/extract-seeds
package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/bholten/tools/idlc-go/internal/parser"
	"github.com/bholten/tools/idlc-go/internal/sema"
)

var enumRe = regexp.MustCompile(`^enum \{RPC_[A-Z0-9_]+ = (\d+)`)

type entry struct {
	key  string
	seed string
}

func main() {
	repoRoot, err := os.Getwd()

	if err != nil {
		fatal(err)
	}

	core3 := os.Getenv("CORE3_PATH")

	if core3 == "" {
		core3 = filepath.Join(repoRoot, "submodules", "Core3")
	}

	baseline := filepath.Join(repoRoot, "_baseline", "jar")
	core3Src := filepath.Join(core3, "MMOCoreORB", "src")
	engine3Src := filepath.Join(core3, "MMOCoreORB", "utils", "engine3", "MMOEngine", "src")

	if _, err := os.Stat(baseline); err != nil {
		fatal(fmt.Errorf("baseline missing (%s) — run `make baseline-jar` first", baseline))
	}

	var entries []entry
	err = filepath.WalkDir(baseline, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".cpp") {
			return err
		}

		seed, ok := readSeed(p)

		if !ok {
			return nil
		}

		// Locate the IDL: _baseline/jar/<tree>/<pkg>/<Class>.cpp.
		rel, err := filepath.Rel(baseline, p)

		if err != nil {
			return nil
		}

		parts := strings.SplitN(rel, string(filepath.Separator), 2)

		if len(parts) != 2 {
			return nil
		}

		tree, restRel := parts[0], parts[1]

		var srcRoot string

		switch tree {
		case "core3":
			srcRoot = core3Src
		case "engine3":
			srcRoot = engine3Src
		default:
			return nil
		}

		idlPath := filepath.Join(srcRoot, strings.TrimSuffix(restRel, ".cpp")+".idl")
		key, err := makeKey(idlPath)

		if err != nil {
			fmt.Fprintf(os.Stderr, "  skip %s: %v\n", restRel, err)
			return nil
		}

		entries = append(entries, entry{key: key, seed: seed})

		return nil
	})

	if err != nil {
		fatal(err)
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].key < entries[j].key })

	for _, e := range entries {
		fmt.Printf("\t%q: %s,\n", e.key, e.seed)
	}

	fmt.Fprintf(os.Stderr, "[extract-seeds] %d entries\n", len(entries))
}

// readSeed reads the first `enum {RPC_… = NNN` line in the .cpp and
// returns the numeric seed as a string. Returns ok=false if the file
// has no seed (auto-numbering enum, e.g. `enum {RPC_X__,};`).
func readSeed(path string) (string, bool) {
	f, err := os.Open(path)

	if err != nil {
		return "", false
	}

	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "enum {RPC_") {
			continue
		}

		m := enumRe.FindStringSubmatch(line)

		if m == nil {
			return "", false
		}

		return m[1], true
	}

	return "", false
}

// makeKey parses the IDL and computes the legacy-seed-map key that
// matches `LookupSeed`'s lookup form: "<pkg>.<Class>.<method>(<types>)".
func makeKey(idlPath string) (string, error) {
	src, err := os.ReadFile(idlPath)

	if err != nil {
		return "", err
	}

	f, err := parser.Parse(idlPath, src)

	if err != nil {
		return "", err
	}

	m, err := sema.Resolve(f)

	if err != nil {
		return "", err
	}

	// Find the first method that participates in the RPC enum: public
	// (or default vis), non-@local.
	for _, meth := range m.Class.Methods {
		if meth.IsLocal {
			continue
		}

		if !(meth.Visibility == "" || meth.Visibility == "public") {
			continue
		}

		var types []string

		for _, p := range meth.Params {
			types = append(types, p.IDLType.Name)
		}

		return fmt.Sprintf("%s.%s.%s(%s)",
			strings.Join(m.Package, "."), m.Class.Name, meth.Name,
			strings.Join(types, ",")), nil
	}

	return "", fmt.Errorf("no enum-participating method")
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
