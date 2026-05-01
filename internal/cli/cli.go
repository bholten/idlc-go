// Package cli implements the idlc-go command-line interface.
//
// Two surface styles, both dispatched from `Run`:
//
//  1. Subcommand style — friendly for humans and scripts:
//       idlc-go compile [-od dir] <file.idl>
//       idlc-go dump-ast <file.idl>
//       idlc-go hash <ClassName.fieldName>
//
//  2. JAR-compat style — drop-in for Core3's CMake invocation:
//       idlc-go -outdir autogen -cp <engine3> -silence -rbcpp \
//               -sd <src> <pkg/Class.idl>
//
// Detection: if the first arg starts with `-`, it's JAR-compat;
// otherwise it's a subcommand. This matches how Core3's CMake will
// substitute idlc-go for `java -cp idlc.jar org.sr.idlc.compiler.Compiler`.
package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
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

subcommands:
  compile [-od dir] <file.idl>   generate C++ from one IDL file
  dump-ast <file.idl>            print parsed AST as JSON
  hash <ClassName.fieldName>     print CRC-32/BZIP2 of a name
  help                           show this message

JAR-compat mode (when first arg starts with '-'):
  -outdir <dir> / -od <dir>      output dir (resolved relative to -sd)
  -cp <dir>                      classpath (engine3 source dir)
  -sd <dir>                      source dir (root for the IDL arg)
  -silence                       suppress info output
  -rbcpp                         rebuild C++ mode (default; only mode)
  -noprelocks                    disable @preLocked asserts (NOT YET HONORED)
  -nomocks                       disable @mock class generation (NOT YET HONORED)
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

// runJARCompat handles the JAR-style invocation Core3's CMake uses:
//
//	idlc-go -outdir <dir> -cp <engine3> -silence -rbcpp \
//	        -sd <src> <pkg/Class.idl>
//
// Output goes to <sd>/<outdir>/<pkg>/<Class>.{h,cpp} — `-outdir` is
// resolved RELATIVE to `-sd`, matching the JAR's quirky path handling
// (and matching what Core3's CMake `IDL_DIRECTIVES` produces).
//
// The registry is populated by scanning every `.idl` under `-sd` and
// `-cp` (engine3 source). This is a coarse approximation: every IDL
// found is registered as `Add` (managed). The JAR's actual rule —
// "managed iff transitively extends ManagedObject" — would change
// outputs for non-managed-parent IDLs (like Observable subclasses);
// extending the registry to compute the inheritance chain is on the
// follow-up list once we attempt the splice and see what diverges.
//
// `-rbcpp` is parsed but ignored (it's just a mode marker).
// `-noprelocks` and `-nomocks` are recognised but emit a warning to
// stderr — production builds with the defaults (asserts on, mocks on),
// which is what we already produce.
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
		noPreLocks bool
		noMocks    bool
	)

	fs.StringVar(&outdir, "outdir", "", "output dir (relative to -sd)")
	fs.StringVar(&odAlias, "od", "", "alias for -outdir")
	fs.StringVar(&cp, "cp", "", "classpath (engine3 source dir)")
	fs.StringVar(&sd, "sd", "", "source dir (root for the IDL arg)")
	fs.BoolVar(&silence, "silence", false, "suppress info output")
	fs.BoolVar(&rbcpp, "rbcpp", false, "rebuild C++ mode (default + only mode)")
	fs.BoolVar(&noPreLocks, "noprelocks", false, "disable @preLocked asserts (NOT YET HONORED)")
	fs.BoolVar(&noMocks, "nomocks", false, "disable @mock class generation (NOT YET HONORED)")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	_ = rbcpp

	if outdir == "" {
		outdir = odAlias
	}

	if sd == "" {
		fmt.Fprintln(stderr, "idlc-go: -sd <source-dir> is required in JAR-compat mode")
		return 2
	}

	if outdir == "" {
		fmt.Fprintln(stderr, "idlc-go: -outdir (or -od) is required in JAR-compat mode")
		return 2
	}

	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "idlc-go: expected exactly one IDL path argument")
		return 2
	}

	if noPreLocks && !silence {
		fmt.Fprintln(stderr, "idlc-go: -noprelocks recognised but not yet honored — output may differ from JAR")
	}

	idlRel := fs.Arg(0)
	idlPath := filepath.Join(sd, idlRel)

	reg := sema.NewRegistry()
	// IDLs first, so they win over same-named .h files. Headers second
	// to register C++ utility classes (`Vector.h`, `Reference.h`, etc.)
	// as `AddNoPOD` — the JAR uses this lookup to resolve
	// `import system.util.Vector;` against the .h file.
	if err := reg.LoadFromDir(sd); err != nil {
		fmt.Fprintf(stderr, "idlc-go: scan %s: %v\n", sd, err)
		return 1
	}
	if cp != "" {
		if err := reg.LoadFromDir(cp); err != nil {
			fmt.Fprintf(stderr, "idlc-go: scan %s: %v\n", cp, err)
			return 1
		}
	}
	// Only -sd headers register as `AddNoPOD` (forward-decl without
	// POD, Reference-wrapped). -cp headers go into the separate
	// `externalHeaders` bucket — they don't forward-decl, but the body
	// rewriter consults them so `Logger.X → Logger::X` works for
	// engine3 utilities the IDL author didn't explicitly import.
	if err := reg.LoadHeadersFromDir(sd); err != nil {
		fmt.Fprintf(stderr, "idlc-go: scan headers %s: %v\n", sd, err)
		return 1
	}
	if cp != "" {
		if err := reg.LoadExternalHeadersFromDir(cp); err != nil {
			fmt.Fprintf(stderr, "idlc-go: scan external headers %s: %v\n", cp, err)
			return 1
		}
	}

	m, err := loadAndResolve(idlPath)

	if err != nil {
		fmt.Fprintf(stderr, "idlc-go: %v\n", err)
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
	pkgDir := path.Dir(idlRel)
	if pkgDir == "." {
		pkgDir = ""
	}
	m.HeaderPath = path.Join(pkgDir, m.Class.Name+".h")
	m.SourcePath = path.Join(pkgDir, m.Class.Name+".cpp")

	// JAR embeds the literal `-outdir` value into every file-header
	// comment; thread it through so we match.
	m.OutdirPrefix = outdir

	headerBytes, sourceBytes, err := cpp.Generate(m, reg)

	if err != nil {
		fmt.Fprintf(stderr, "idlc-go: emit: %v\n", err)
		return 1
	}

	// JAR quirk: -outdir is resolved relative to -sd. So actual output
	// root is <sd>/<outdir>. Every generated file lands under there in
	// its package subdirectory.
	outRoot := filepath.Join(sd, outdir)
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
		fmt.Fprintf(stdout, "wrote %s/%s\n", outdir, m.HeaderPath)
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
