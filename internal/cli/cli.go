// Package cli implements the idlc-go command-line interface.
//
// Two surface styles, both dispatched from `Run`:
//
//  1. Subcommand style — friendly for humans and scripts:
//     idlc-go compile [-od dir] <file.idl>
//     idlc-go dump-ast <file.idl>
//     idlc-go hash <ClassName.fieldName>
//
//  2. JAR-compat style — drop-in for both invocation patterns the JAR
//     understands:
//
//     a. Per-file (Core3's CMake): one .idl path + `-outdir`,
//     output goes to `<sd>/<outdir>/<pkg>/<Class>.{h,cpp}`.
//     idlc-go -outdir autogen -cp <engine3> -silence -rbcpp \
//     -sd <src> <pkg/Class.idl>
//
//     b. Directory-walk (engine3's CMake): magic positional sentinel
//     (`anyadEclipse` is the JAR's hardcoded marker — any non-IDL
//     positional triggers walk mode here). With `-outdir` set,
//     output is `<sd>/<outdir>/<pkg>/`; without `-outdir`, output
//     lands alongside source at `<sd>/<pkg>/`, matching the JAR's
//     behavior for engine3's checked-in autogen tree.
//     idlc-go [-rb] -sd <src> anyadEclipse
//
// Detection: if the first arg starts with `-`, it's JAR-compat;
// otherwise it's a subcommand. This matches how Core3's and engine3's
// CMake substitute idlc-go for `java -cp idlc.jar org.sr.idlc.compiler.Compiler`.
package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bholten/tools/idlc-go/internal/emit/cpp"
	"github.com/bholten/tools/idlc-go/internal/hash"
	"github.com/bholten/tools/idlc-go/internal/parser"
	"github.com/bholten/tools/idlc-go/internal/sema"
)

// Run dispatches one CLI invocation. It returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		printUsage(stderr)
		return 2
	}

	// JAR-compat style — first arg is a flag.
	if strings.HasPrefix(args[0], "-") && args[0] != "-h" && args[0] != "--help" {
		return runJARCompat(args, stdout, stderr)
	}

	switch args[0] {
	case "compile":
		return runCompile(args[1:], stdout, stderr)
	case "dump-ast":
		return runDumpAST(args[1:], stdout, stderr)
	case "hash":
		return runHash(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "idlc-go: unknown command %q\n\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `usage: idlc-go <command> [args]
       idlc-go -outdir <dir> -cp <engine3> [-silence] [-rbcpp] -sd <src> <pkg/Class.idl>
       idlc-go [-rb] [-outdir <dir>] -sd <src> anyadEclipse

subcommands:
  compile [-od dir] <file.idl>   generate C++ from one IDL file
  dump-ast <file.idl>            print parsed AST as JSON
  hash <ClassName.fieldName>     print CRC-32/BZIP2 of a name
  help                           show this message

JAR-compat mode (when first arg starts with '-'):
  -outdir <dir> / -od <dir>      output dir (resolved relative to -sd);
                                 if absent, outputs land alongside source
  -cp <dir>                      classpath (engine3 source dir)
  -sd <dir>                      source dir (root for the IDL arg)
  -silence                       suppress info output
  -rbcpp                         rebuild C++ mode (default; only mode)
  -rb                            rebuild marker (engine3 dir-walk variant); no-op
  -noprelocks                    disable @preLocked asserts (NOT YET HONORED)
  -nomocks                       disable @mock class generation (NOT YET HONORED)

Positional arg:
  <pkg/Class.idl>                per-file mode: compile one IDL
  anyadEclipse (or any non-.idl) directory-walk mode: compile every
                                 .idl found under -sd
`)
}

func runCompile(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("compile", flag.ContinueOnError)
	fs.SetOutput(stderr)
	od := fs.String("od", "", "output directory; if empty, write to stdout")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}

	idlPath := fs.Arg(0)

	m, err := loadAndResolve(idlPath)

	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}

	headerBytes, sourceBytes, err := cpp.Generate(m, nil)

	if err != nil {
		fmt.Fprintf(stderr, "emit: %v\n", err)
		return 1
	}

	if *od == "" {
		stdout.Write(headerBytes)
		stdout.Write([]byte("\n----\n"))
		stdout.Write(sourceBytes)
		return 0
	}

	pkgDir := filepath.Join(*od, filepath.Dir(m.HeaderPath))

	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		fmt.Fprintf(stderr, "mkdir: %v\n", err)
		return 1
	}

	if err := os.WriteFile(filepath.Join(*od, m.HeaderPath), headerBytes, 0o644); err != nil {
		fmt.Fprintf(stderr, "write header: %v\n", err)
		return 1
	}

	if err := os.WriteFile(filepath.Join(*od, m.SourcePath), sourceBytes, 0o644); err != nil {
		fmt.Fprintf(stderr, "write source: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "wrote %s and %s\n", m.HeaderPath, m.SourcePath)

	return 0
}

func runDumpAST(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: idlc-go dump-ast <file.idl>")
		return 2
	}

	src, err := os.ReadFile(args[0])

	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}

	f, err := parser.Parse(args[0], src)

	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}

	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")

	if err := enc.Encode(f); err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}

	return 0
}

func runHash(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: idlc-go hash <ClassName.fieldName>")
		return 2
	}

	fmt.Fprintf(stdout, "%#08x\n", hash.NameHash(args[0]))

	return 0
}

// runJARCompat handles both JAR-style invocations:
//
//  1. Per-file (Core3): exactly one positional .idl arg. Requires
//     -outdir. Output goes to <sd>/<outdir>/<pkg>/<Class>.{h,cpp}.
//
//  2. Directory-walk (engine3): one positional sentinel arg that does
//     NOT end in .idl (the JAR uses `anyadEclipse` — we accept any
//     non-.idl token to be tolerant of fork variations). Walks every
//     .idl under -sd and compiles each. With -outdir, output goes to
//     <sd>/<outdir>/<pkg>/...; without, output lands alongside the
//     .idl at <sd>/<pkg>/ (engine3's checked-in autogen layout).
//
// `-rbcpp` and `-rb` are parsed but ignored — they're rebuild markers
// and we don't track output timestamps anyway. `-noprelocks` and
// `-nomocks` are recognised; `-nomocks` is honored, `-noprelocks` is
// not yet.
//
// The registry is populated by scanning every `.idl` under `-sd` and
// `-cp` (engine3 source). This is a coarse approximation: every IDL
// found is registered as `Add` (managed). The JAR's actual rule —
// "managed iff transitively extends ManagedObject" — would change
// outputs for non-managed-parent IDLs (like Observable subclasses);
// extending the registry to compute the inheritance chain is on the
// follow-up list once we attempt the splice and see what diverges.
func runJARCompat(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("idlc-go (jar-compat)", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		outdir     string
		odAlias    string
		cp         string
		sd         string
		silence    bool
		rbcpp      bool
		rb         bool
		noPreLocks bool
		noMocks    bool
	)

	fs.StringVar(&outdir, "outdir", "", "output dir (relative to -sd); empty = alongside source")
	fs.StringVar(&odAlias, "od", "", "alias for -outdir")
	fs.StringVar(&cp, "cp", "", "classpath (engine3 source dir)")
	fs.StringVar(&sd, "sd", "", "source dir (root for the IDL arg)")
	fs.BoolVar(&silence, "silence", false, "suppress info output")
	fs.BoolVar(&rbcpp, "rbcpp", false, "rebuild C++ mode (default + only mode)")
	fs.BoolVar(&rb, "rb", false, "rebuild marker (engine3 dir-walk variant); no-op")
	fs.BoolVar(&noPreLocks, "noprelocks", false, "disable @preLocked asserts (NOT YET HONORED)")
	fs.BoolVar(&noMocks, "nomocks", false, "disable @mock class generation")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	_, _ = rbcpp, rb

	if outdir == "" {
		outdir = odAlias
	}

	if sd == "" {
		fmt.Fprintln(stderr, "idlc-go: -sd <source-dir> is required in JAR-compat mode")
		return 2
	}

	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "idlc-go: expected exactly one positional argument (an .idl path or directory-walk sentinel)")
		return 2
	}

	if noPreLocks && !silence {
		fmt.Fprintln(stderr, "idlc-go: -noprelocks recognised but not yet honored — output may differ from JAR")
	}

	reg, regErr := buildRegistry(sd, cp)
	if regErr != nil {
		fmt.Fprintf(stderr, "idlc-go: %v\n", regErr)
		return 1
	}

	positional := fs.Arg(0)

	if strings.HasSuffix(positional, ".idl") {
		// Per-file mode (Core3 invocation pattern).
		if outdir == "" {
			fmt.Fprintln(stderr, "idlc-go: -outdir (or -od) is required when compiling a single .idl in JAR-compat mode")
			return 2
		}

		return compileOne(positional, sd, outdir, reg, noMocks, silence, stdout, stderr)
	}

	// Directory-walk mode (engine3 invocation pattern). Positional is
	// a sentinel token (`anyadEclipse` from the JAR, but we accept
	// anything that doesn't look like an .idl path).
	idlPaths, err := walkIDLs(sd)

	if err != nil {
		fmt.Fprintf(stderr, "idlc-go: walk %s: %v\n", sd, err)
		return 1
	}

	if len(idlPaths) == 0 {
		if !silence {
			fmt.Fprintf(stderr, "idlc-go: no .idl files found under %s\n", sd)
		}

		return 0
	}

	for _, idlRel := range idlPaths {
		if rc := compileOne(idlRel, sd, outdir, reg, noMocks, silence, stdout, stderr); rc != 0 {
			return rc
		}
	}

	return 0
}

// buildRegistry scans `-sd` and (optionally) `-cp` to populate the
// IDL/header registry that the emitter consults to choose between
// `ManagedReference<T*>`, `Reference<T*>`, and bare `T*`.
//
// Scan order:
//   - IDLs from -sd, then -cp: classes registered as managed.
//   - Headers from -sd: registered as `AddNoPOD` (forward-decl, no POD).
//   - Headers from -cp: registered in the `externalHeaders` bucket
//     (the body rewriter consults this so `Logger.X → Logger::X` works
//     for engine3 utilities the IDL author didn't explicitly import).
func buildRegistry(sd, cp string) (*sema.Registry, error) {
	reg := sema.NewRegistry()

	// IDLs first, so they win over same-named .h files.
	if err := reg.LoadFromDir(sd); err != nil {
		return nil, fmt.Errorf("scan %s: %w", sd, err)
	}

	if cp != "" {
		if err := reg.LoadFromDir(cp); err != nil {
			return nil, fmt.Errorf("scan %s: %w", cp, err)
		}
	}

	if err := reg.LoadHeadersFromDir(sd); err != nil {
		return nil, fmt.Errorf("scan headers %s: %w", sd, err)
	}

	if cp != "" {
		if err := reg.LoadExternalHeadersFromDir(cp); err != nil {
			return nil, fmt.Errorf("scan external headers %s: %w", cp, err)
		}
	}

	return reg, nil
}

// walkIDLs returns every .idl under sd as a slice of paths relative
// to sd. Sorted for stable output ordering.
func walkIDLs(sd string) ([]string, error) {
	var out []string

	err := filepath.WalkDir(sd, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		if !strings.HasSuffix(p, ".idl") {
			return nil
		}

		rel, err := filepath.Rel(sd, p)

		if err != nil {
			return err
		}

		out = append(out, filepath.ToSlash(rel))

		return nil
	})

	if err != nil {
		return nil, err
	}

	sort.Strings(out)

	return out, nil
}

// compileOne resolves and emits one IDL. `idlRel` is relative to sd;
// `outdir` is relative to sd (or "" to write alongside the .idl).
func compileOne(idlRel, sd, outdir string, reg *sema.Registry, noMocks, silence bool, stdout, stderr io.Writer) int {
	idlPath := filepath.Join(sd, idlRel)

	m, err := loadAndResolve(idlPath)

	if err != nil {
		fmt.Fprintf(stderr, "idlc-go: %s: %v\n", idlRel, err)
		return 1
	}

	// `-nomocks` strips @mock so no gmock include / Mock<Class> shell
	// is emitted (mirrors the JAR behavior under -DCOMPILE_TESTS=OFF).
	if noMocks {
		m.Class.IsMock = false

		for i := range m.Class.Methods {
			m.Class.Methods[i].IsMock = false
		}
	}

	// Override HeaderPath / SourcePath to use the input file's
	// directory rather than the IDL's `package` declaration. Core3
	// has IDLs where these disagree (e.g. `server/zone/managers/
	// ZoneManager.idl` declares `package server.zone.manager` — JAR
	// uses the file path for output, package only for the C++
	// namespace).
	pkgDir := path.Dir(filepath.ToSlash(idlRel))

	if pkgDir == "." {
		pkgDir = ""
	}

	m.HeaderPath = path.Join(pkgDir, m.Class.Name+".h")
	m.SourcePath = path.Join(pkgDir, m.Class.Name+".cpp")

	if outdir == "" {
		// engine3 layout: emit alongside source, file-header comment
		// has no `<outdir>/` prefix.
		m.NoOutdirPrefix = true
	} else {
		// Per-file / Core3 layout: the JAR embeds the literal `-outdir`
		// value in the file-header comment; thread it through.
		m.OutdirPrefix = outdir
	}

	headerBytes, sourceBytes, err := cpp.Generate(m, reg)

	if err != nil {
		fmt.Fprintf(stderr, "idlc-go: emit %s: %v\n", idlRel, err)
		return 1
	}

	// outdir, when set, is resolved RELATIVE to sd. When empty,
	// outputs land alongside source (sd directly).
	outRoot := sd
	if outdir != "" {
		outRoot = filepath.Join(sd, outdir)
	}

	outPkgDir := filepath.Join(outRoot, filepath.Dir(m.HeaderPath))

	if err := os.MkdirAll(outPkgDir, 0o755); err != nil {
		fmt.Fprintf(stderr, "idlc-go: mkdir %s: %v\n", outPkgDir, err)
		return 1
	}

	if err := os.WriteFile(filepath.Join(outRoot, m.HeaderPath), headerBytes, 0o644); err != nil {
		fmt.Fprintf(stderr, "idlc-go: write header: %v\n", err)
		return 1
	}

	if err := os.WriteFile(filepath.Join(outRoot, m.SourcePath), sourceBytes, 0o644); err != nil {
		fmt.Fprintf(stderr, "idlc-go: write source: %v\n", err)
		return 1
	}

	if !silence {
		if outdir == "" {
			fmt.Fprintf(stdout, "wrote %s\n", m.HeaderPath)
		} else {
			fmt.Fprintf(stdout, "wrote %s/%s\n", outdir, m.HeaderPath)
		}
	}

	return 0
}

func loadAndResolve(idlPath string) (*sema.Model, error) {
	src, err := os.ReadFile(idlPath)

	if err != nil {
		return nil, fmt.Errorf("read %s: %w", idlPath, err)
	}

	f, err := parser.Parse(idlPath, src)

	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}

	m, err := sema.Resolve(f)

	if err != nil {
		return nil, fmt.Errorf("resolve: %w", err)
	}

	return m, nil
}
