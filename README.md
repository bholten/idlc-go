# idlc-go

A Go reimplementation of `idlc.jar`, the IDL compiler used by [SWGEmu](https://www.swgemu.com)'s [Core3](https://github.com/swgemu/Core3) server and its [engine3](https://github.com/swgemu/MMOEngine) distributed-object framework. Drop-in replacement: produces byte-equivalent C++ output without the JRE dependency.

## Status

**Working.** The full Core3 server binary builds end-to-end with idlc-go as the IDL compiler. Server boots, login works, gameplay works.

- 18 of 21 corpus IDLs are byte-identical to JAR output. The 3 divergences produce semantically equivalent C++ that compiles and links cleanly.
- 25 synthetic probes cover annotation matrices and edge cases the natural corpus didn't exercise.
- CRC-32/BZIP2 hash compatibility validated against 23 oracles - required for existing BerkeleyDB persistence to keep working.

## Building

Requires Go 1.21+ (Debian Bookworm: `apt install -t bookworm-backports golang-go`).

```bash
make build       # → ./idlc-go
```

This produces a **single static binary** that can be dropped into your existing Core3/engine3 builds -- no Go toolchain needed in your server.

## CLI

```bash
# Compile one IDL to <out>/<Class>.{h,cpp}
./idlc-go compile -od /tmp/out testdata/idl/ChatMessage.idl

# Print the parsed AST
./idlc-go dump-ast testdata/idl/ChatMessage.idl

# Compute the field-name hash (CRC-32/BZIP2)
./idlc-go hash "ChatMessage.message"
# → 0xbd8f57ac
```

## Testing

Three layers, increasing in strictness:

1. **Hash oracle** (`internal/hash/`) - CRC-32/BZIP2 validated against 23 oracles. Non-negotiable; a regression breaks every BerkeleyDB record.
2. **Probe golden** (`internal/probe/`) - byte-diffs against synthetic probe IDLs. Runs without Core3.
3. **Corpus golden** (`internal/golden/`) - byte-diffs against `testdata/autogen/` for 21 real Core3 IDLs.

A fresh checkout passes the probe + unit + hash tests immediately. To run corpus tests, you need a local Core3 checkout:

```bash
git clone --recursive https://github.com/swgemu/Core3.git submodules/Core3
make fetch-corpus       # populates testdata/{idl,autogen}/ from Core3
make test-corpus
```

Or set `CORE3_PATH=/abs/path/to/your/Core3` to point anywhere else.

Convenience targets:

```bash
make test               # all tests; corpus tests skip if Core3 absent
make test-probes        # probe tests only (always available)
make test-corpus        # corpus tests only (requires Core3)
```

`make help` lists every target.

## Project layout

```
cmd/idlc/                 # CLI entry point
internal/
  lexer/                  # tokenizer (with CaptureBalancedBlock for method bodies)
  parser/                 # recursive-descent AST parser
  sema/                   # AST → emit-ready Model (type rules, RPC, registry)
  hash/                   # CRC-32/BZIP2
  emit/cpp/               # C++ emitters (one file per generated class)
  cli/                    # subcommand dispatch
  golden/                 # byte-equality tests against testdata/autogen/
  probe/                  # byte-equality tests against synthetic probes
testdata/
  idl/                    # corpus IDLs (Core3-derived, .gitignored)
  autogen/                # corpus JAR output (Core3-derived, .gitignored)
  mock/                   # @mock-class fixtures (committed)
  probe/
    src/                  # synthetic probe IDLs (committed)
    expected/             # probe JAR output (committed)
docs/
  idl-language-spec.md    # IDL reference and emit rules
  jar-quirks.md           # JAR behaviours we deliberately reproduce
```

For corpus tests and oracle regeneration, clone SWGEmu/Core3 to `submodules/Core3/` (the default `CORE3_PATH`) or anywhere else and override via `CORE3_PATH=/abs/path`. Core3 isn't required for the probe + hash + unit tests.

## Hash compatibility

For each managed-object field, idlc emits a 32-bit name hash (`0xbd8f57ac //ChatMessage.message`) into the persistence switch and the on-the-wire format. **These hashes are baked into existing BerkeleyDB databases.** Reproducing them byte-for-byte is the only hard correctness bar.

The hash is **CRC-32/BZIP2** over the ASCII bytes of `ClassName.fieldName`:
- polynomial `0x04C11DB7`
- init `0xFFFFFFFF`
- no input/output reflection
- final XOR `0xFFFFFFFF`

Go's `hash/crc32` doesn't ship a preconfigured BZIP2 variant (the standard table uses bit-reflection; BZIP2 doesn't), so a hand-rolled table-based implementation lives in `internal/hash/`.

## Known gaps

None of these block the live Core3 build, but they may matter depending on your setup. Tracked in [GitHub Issues](../../issues).

- **`Observable` / `Observer` golden tests diverge.** The golden harness doesn't load engine3-side classes when running engine3-side IDLs, so the registry can't classify imports like `Logger` and falls back to `#include`. The live build does register them and is correct - this is a test-harness limitation only.
- **`@async` annotation unimplemented.** Affects `TestIDLClass.cpp` and any other `@async` method: idlc-go omits the trailing `true` argument on `DistributedMethod(...)` and `executeWithVoidReturn(...)`. Not used in critical Core3 paths.
- **`@mock` whole-corpus walk.** idlc-go relies on per-class `.mock` fixtures committed to `testdata/mock/`; the JAR walks the IDL inheritance chain to compute mock methods automatically. Net effect: `-DCOMPILE_TESTS=ON` is currently unsupported - build with `-DCOMPILE_TESTS=OFF` (which propagates `-nomocks` to the IDL compiler).
- **`@lua` overloaded-method dispatch.** When a class has multiple methods with the same name (e.g. `getValueOf(int)` vs `getValueOf(string)`), idlc-go emits one body per name (first-occurrence wins) instead of the JAR's `lua_is<T>(L, -1)` dispatch ladder. Alternate overloads are unreachable from Lua scripts. Niche; only matters if Lua scripts call the alternate variants.

## Distribution

- **`idlc.jar`** is unlicensed proprietary code. We don't ship it; we reference it from a Core3 / engine3 checkout for oracle-based testing only.
- **Corpus IDLs and autogen** under `testdata/idl/` + `testdata/autogen/` are derived from Core3 and `.gitignore`d. `make fetch-corpus` repopulates them locally.
- **Probe IDLs and probe goldens** under `testdata/probe/` are ours and ARE committed.
- **Hash compatibility test** uses oracle values extracted from JAR output, not the JAR itself - runs without Core3 present.

## Documentation

- [docs/idl-language-spec.md](docs/idl-language-spec.md) - IDL language reference and emit rules.
- [docs/jar-quirks.md](docs/jar-quirks.md) - JAR behaviors we deliberately reproduce, including bugs replicated for byte-equality.

## Adding a corpus IDL

1. Drop the `.idl` into `testdata/idl/` and the JAR-generated `.h`/`.cpp` into `testdata/autogen/<pkg>/`.
2. Add `TestGolden<Class>` to `internal/golden/golden_test.go` calling `runGolden`.
3. `go test ./internal/golden` - fix mismatches against `<path>.got` until green.

Each new IDL tends to surface 5&ndash;15 emit rules; check [docs/jar-quirks.md](docs/jar-quirks.md) before adding a new emit branch.

## Adding a probe

Probes are synthetic IDLs that exercise annotation combinations the natural corpus doesn't.

1. Drop a `.idl` into `testdata/probe/src/probe/`.
2. `bash scripts/gen-probe-goldens.sh` - JAR generates `testdata/probe/expected/probe/<Name>.{h,cpp}`.
3. Register the probe class qname in `internal/probe/probe_test.go`'s `buildProbeRegistry`.
4. `go test ./internal/probe` - iterate until green.

## License

[AGPL-3.0](LICENSE), matching SWGEmu Core3 and engine3.

idlc-go is a clean-room reimplementation of `idlc.jar`; see [NOTICE](NOTICE) for attribution details.
