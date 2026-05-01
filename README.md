# idlc-go

A Go reimplementation of `idlc.jar`, the IDL compiler used by [SWGEmu](https://github.com/swgemu/Core3)'s `engine3` distributed-object framework. The goal is byte-for-byte compatible C++ output, dropping the JRE dependency from the upstream toolchain.

## Status

*Pre-alpha — not yet wired into Core3's build.*

- **Hash compatibility**: CRC-32/BZIP2 reproduced, validated against 23 oracles. This is the load-bearing constraint — existing BerkeleyDB records depend on these hashes being byte-identical.
- **Test corpus**: 13/13 IDLs in `testdata/idl/` are byte-identical to JAR output.
- **Probe suite**: 7 synthetic IDLs targeting annotation matrices and edge cases. Have surfaced ~30 distinct emit rules the natural corpus didn't exercise — see [docs/jar-quirks.md](docs/jar-quirks.md).
- **CLI**: `compile`, `dump-ast`, `hash`. The legacy-flag form (`-sd`/`-od`/`-cp`/`-rbcpp`) for CMake drop-in is **not yet wired**.

## Building & testing

```bash
make build               # go build ./...
make test                # all tests; corpus tests skip if Core3 absent
make test-probes         # probe tests only (always available)
make test-corpus         # corpus tests only (requires Core3)
```

`make help` lists every target.

Module path is `github.com/bholten/tools/idlc-go`. Go 1.25.9.

### Core3 is optional

The Core3 corpus and `idlc.jar` are not redistributable, so they are
`.gitignore`d. A fresh checkout will build and pass the probe + unit
tests immediately. To run the corpus tests:

```bash
make pull-core3          # git submodule update --init --recursive
make fetch-corpus        # populates testdata/idl/ + testdata/autogen/ from Core3
make test-corpus         # now passes
```

`pull-core3` clones SWGEmu/Core3 (with its nested engine3 submodule —
that's where `idlc.jar` lives). `fetch-corpus` runs the JAR over each
corpus IDL inside Core3's source tree and copies the IDL + autogen into
`testdata/`. Override the Core3 location with `make CORE3_PATH=/abs/path …`.

Tests that need the corpus call `corpus.RequireOrSkip(t)` and emit a
helpful skip message when files are missing — they never error.

## Using the CLI

```bash
# Compile one IDL to <out>/<Class>.{h,cpp}
go run ./cmd/idlc compile -od /tmp/out testdata/idl/ChatMessage.idl

# Print the parsed AST
go run ./cmd/idlc dump-ast testdata/idl/ChatMessage.idl

# Compute the field-name hash (CRC-32/BZIP2)
go run ./cmd/idlc hash "ChatMessage.message"
# → 0xbd8f57ac
```

## Project layout

```
cmd/idlc/                          # CLI entry point
internal/
  lexer/                           # tokenizer (with CaptureBalancedBlock for method bodies)
  parser/                          # recursive-descent AST parser
  sema/                            # AST → emit-ready Model (type rules, RPC, registry)
  hash/                            # CRC-32/BZIP2 implementation
  emit/cpp/                        # C++ emitters (one file per generated class)
  cli/                             # subcommand dispatch
  golden/                          # byte-equality test against testdata/autogen/
  probe/                           # byte-equality test against synthetic probes
testdata/
  idl/                             # corpus IDLs (Core3-derived, .gitignored)
  autogen/                         # corpus JAR output (Core3-derived, .gitignored)
  mock/                            # @mock-class fixtures (hand-curated, committed)
  probe/
    src/                           # synthetic probe IDLs (committed)
    expected/                      # probe JAR output (committed; we own these)
docs/
  idl-toolchain-feasibility.md     # original design doc
  jar-quirks.md                    # JAR behaviours we deliberately reproduce
submodules/Core3/                  # SWGEmu/Core3 + nested engine3 (.gitignored contents)
scripts/
  gen-oracle.sh                    # regenerate hash-test oracle table
  gen-probe-goldens.sh             # regenerate probe goldens by running the JAR
  fetch-corpus-from-core3.sh       # copy IDLs + run JAR → testdata/{idl,autogen}/
Makefile                           # friendly entry points for common tasks
```

The `idlc.jar` itself lives at `submodules/Core3/MMOCoreORB/utils/engine3/MMOEngine/lib/idlc.jar`
(Core3's nested engine3 submodule). All scripts auto-detect it via
`CORE3_PATH` (defaults to `submodules/Core3`).

## Validation

Three layers, increasing in strictness:

1. **Hash oracle** — `internal/hash` validates CRC-32/BZIP2 against 23 oracles. Non-negotiable; a regression breaks every BerkeleyDB record.

2. **Corpus golden** — `internal/golden` does `git diff` against `testdata/autogen/` for 13 real Core3 IDLs.
   - Run: `go test ./internal/golden`
   - Refresh after intentional change: `go test ./internal/golden -update`
   - On mismatch: `<path>.got` is dropped next to the expected file for offline diffing.

3. **Probe golden** — `internal/probe` does the same against synthetic probe IDLs.
   - Run: `go test ./internal/probe`
   - Refresh: `bash scripts/gen-probe-goldens.sh && go test ./internal/probe -update`

## How the JAR is invoked

Both Core3 and our probe harness invoke the JAR identically. From `submodules/Core3/MMOCoreORB/cmake/Modules/FindEngine3.cmake`:

```bash
java -XX:TieredStopAtLevel=1 -client -Xmx128M \
     -cp <idlc.jar> org.sr.idlc.compiler.Compiler \
     -outdir autogen \
     -cp <engine3/MMOEngine/src> \
     -silence -rbcpp \
     -sd <idl_source_root> \
     <idl_relative_path>
```

The second `-cp` is the **classpath** — where the JAR finds non-IDL utility headers (`Vector.h`, `Reference.h`) and engine3's own IDLs (`engine.core.ManagedObject.idl`). Without it, the JAR errors on `import system.util.Vector;`.

## Distribution constraints

- **`idlc.jar`** is unlicensed proprietary code. We don't ship it; we
  reference it from Core3's engine3 submodule.
- **Corpus IDLs and autogen** under `testdata/idl/` + `testdata/autogen/`
  are derived from Core3 and `.gitignore`d. `make fetch-corpus`
  repopulates them locally for development.
- **Probe IDLs and probe goldens** under `testdata/probe/` are ours
  (we wrote the IDLs; the goldens are JAR output of our IDLs) and ARE
  committed.
- **Hash compatibility test** in `internal/hash/` uses oracle values
  extracted from JAR output, not the JAR itself — so it runs without
  Core3 present.

## Adding a corpus IDL

1. Drop the `.idl` into `testdata/idl/` and the JAR-generated `.h`/`.cpp` into `testdata/autogen/<pkg>/`.
2. Add `TestGolden<Class>` to `internal/golden/golden_test.go` calling `runGolden`.
3. `go test ./internal/golden` — fix mismatches against `<path>.got` until green.

Each new IDL tends to surface 5–15 emit rules; check [docs/jar-quirks.md](docs/jar-quirks.md) before adding a new emit branch.

## Adding a probe

Probes are synthetic IDLs that exercise annotation combinations the natural corpus doesn't.

1. Drop a `.idl` into `testdata/probe/src/probe/`.
2. `bash scripts/gen-probe-goldens.sh` — JAR generates `testdata/probe/expected/probe/<Name>.{h,cpp}`.
3. Register the probe class qname in `internal/probe/probe_test.go`'s `buildProbeRegistry`.
4. `go test ./internal/probe` — iterate until green.

The existing 7 probes (Locking, Returns, Params, Fields, Dispatch, Generics, Body) cover the orthogonal axes; new probes should target combinations not yet exercised.

## Documentation

- [docs/idl-toolchain-feasibility.md](docs/idl-toolchain-feasibility.md) — original design doc, motivation, execution plan.
- [docs/jar-quirks.md](docs/jar-quirks.md) — exhaustive list of JAR behaviours we deliberately reproduce, including bugs we replicate intentionally for byte-equality.
- [CLAUDE.md](CLAUDE.md) — agent-facing project guide (Claude Code).

## License

Matches the upstream Core3 license. The reference `idlc.jar` and engine3 are governed by their respective SWGEmu licenses.
