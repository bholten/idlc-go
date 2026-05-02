# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this project is

`idlc-go` is a Go reimplementation of `idlc.jar`, the CORBA-style IDL compiler used by SWGEmu's `engine3` distributed-object framework (Core3). The goal is to drop the Java/JRE dependency from the upstream toolchain by producing byte-identical (or, where acceptable, semantically equivalent) C++ output to what the obfuscated Allatori-packed JAR emits today.

**Documentation order of operations:**

- `docs/idl-language-spec.md` — semi-formal reference for the IDL language and emit rules. Start here for "what does the JAR do for X?" questions; this is the spec the implementation tries to match.
- `docs/jar-quirks.md` — catalog of specific JAR quirks and bugs we faithfully reproduce, with verification provenance.
- `docs/idl-toolchain-feasibility.md` — design rationale and execution plan. Read this for "why is the project structured this way?"; it's the design doc, not the spec.

**Status (Phase 6 complete, 2026-05-02):** The full Core3 server binary `core3` (47MB) builds end-to-end with idlc-go as the IDL compiler — engine3 + idlobjects + all static libs + the main binary all link clean. Server boots, login works, gameplay works (verified by user). The closed-source `idlc.jar` dependency is dropped.

Of 21 corpus IDLs, 18 are byte-identical to the JAR's autogen tree (gated by `internal/golden`); 3 diverge in cosmetic/quirky ways that don't affect runtime: Observable + Observer (cross-tree forward-decl handling — golden test harness limitation, live build is correct), and TestIDLClass (`@async` annotation unimplemented). 25 synthetic probes in `testdata/probe/src/probe/` cover edge cases the corpus doesn't exercise (annotation matrices, return-modifier matrices, recently-added rules like `transient static` fields and `@lua` Luna bridge). See `memory/project_idlc_go_outstanding.md` for the catalogue of remaining gaps.

The 21-IDL corpus, with the rule clusters each one surfaced:

- `testdata/idl/ChatMessage.idl` — leaf class with `@json` and `@read`.
- `testdata/idl/PendingMessageList.idl` — generics + `@local` + `@dereferenced` + primitive marshalling.
- `testdata/idl/ManagedObject.idl` — root class (no `extends`) with `@nativeStub` `__name()` companions, transient fields, special `_className` slot, doc comments, `@noImplementationDeclaration`, `abstract → virtual`, default-param emit rules.
- `testdata/idl/ChatRoom.idl` — `static final int` constants, IDL-registry-driven forward-decl imports, `ManagedReference<T* >` field wrap (incl. nested in field generics), `@reference → Reference<T* >` returns, class-arg generics in method signatures (`Vector<BaseMessage*>`), `synchronized` block rewrite + `synchronized` method `Locker` injection, body rewrites (`null → NULL`, class-typed `name.X → name->X`), `@preLocked` / `@arg1preLocked` asserts, default-vis `private:` field block, hash literals `0x%x` (not zero-padded), blank-line spacing between multi-field writeJSON entries.
- `testdata/idl/PersistentMessage.idl` — `byte` constants emit declaration-only in header + out-of-class definition (`const byte Class::X = N;`) in source; RPC enum trailing comma gated on `hasAbstractMethod` (not just `IsRoot`); imported-class body rewrite `Imported.X → Imported::X` for static-method calls (`System.getTime() → System::getTime()`).
- `testdata/idl/Zone.idl` — `@mock` class generation (gmock include + `MockZone` shell with mock-method body fixture-injected from `testdata/mock/<Class>.mock` since the JAR walks the whole corpus to compute it); registry-aware `Reference<T*>` vs `ManagedReference<T*>` field wrap (non-IDL imports get `Reference<>`); class-typed inner generic args wrap as `ManagedReference<T*>` only when the inner is a known IDL class; `@dereferenced` method return → render by-value (drop trailing `*`); `@dereferenced final T` param → `const T& name`; `lock(ManagedObject* obj)`/`wlock(ManagedObject* obj)` always use the literal `ManagedObject` (not `c.Base`); native ctor with params (e.g. `native Zone(ZoneProcessServer, string)`) → stub class header gets the args ctor + `protected: Class() { }` slot, impl class header gets `Impl(args);` + dummy ctor (no default-args ctor); stub source emits `Class::Class(args)` body with explicit `_initializeImplementation()` call; `Helper::instantiateServant` falls back to dummy ctor when the impl has a custom ctor; IDL-declared `finalize()` triggers special-casing — skip stub class decl + body, skip impl boilerplate `void X::finalize()` decl + definition, dtor body becomes `Class::finalize();`, POD dtor body becomes `finalize();`; forward-decl-only IDL imports (no POD companion) registered via `Registry.AddNoPOD`.
- `testdata/idl/ChatManager.idl` — class-level `@dirty` propagates to every method (use `_getImplementationForRead()` everywhere); `unsigned int` constants use the JAR's reordered form `unsigned static const int X;` + out-of-class def `unsigned const int Class::X = N;`; `null` default-value rewrites to `NULL` in C++ param decls; `final` return-type modifier prepends `const ` to the rendered type (also in adapter case body); `implements X, Y` in IDL → `: public Base, public X, public Y` impl-class base list; `short` and `unsigned short` registered as primitives; protected default-ctor slot in stub header is gated on `@mock` (Zone) and not emitted for non-`@mock` custom-ctor classes (ChatManager); POD class field rendering uses `<X>POD` suffix wrapped in `ManagedReference<...* >` (a dedicated `CppRenderPODFieldType` parallels `CppRenderFieldType`); impl/POD `TypeInfo<...>` for read/write member paths uses the field-type render so class fields wrap correctly; `@dereferenced` class param → wire via `addDereferencedSerializableParameter(x)` (stub) and `getDereferencedSerializableParameter<T >()` (adapter case body); `@rawTemplate(value="X*")` annotation on a param → opaque template paste `Reference<X* >* name` with the smart-pointer trailing-space rule when the value ends in `*`.
- `testdata/idl/TreeEntry.idl` — non-managed-parent classes (those whose ancestry doesn't reach ManagedObject, like `engine.util.Observable`) skip the forward-decl layout entirely — every IDL import becomes a regular `#include` (`Registry.AddNonManagedParent`); custom ctor decl uses `joinParamDeclsWithDefaults` so default values like `= NULL` survive into the stub/impl headers; `@virtualStub` adds `virtual ` to the stub-class method declaration; `@weakReference` on a method wraps the return as `ManagedWeakReference<T* >` (sibling of `@reference`'s `Reference<T* >`); `@weakReference` field's POD form keeps the wrap as `ManagedWeakReference<XPOD* >`; `@weakReference` adapter return calls `.get()` to extract the raw pointer (`@reference` does NOT — it relies on `Reference<>`'s implicit conversion); `@dereferenced final T` adapter param emits a leading-space `\t\t\t T name = inv->getDereferencedSerializableParameter<T >();` (matches the byref-string form), non-final emits without the space; method bodies have C-style `// line` and `/* block */` comments stripped before line-by-line emit; `include`-directive imports (e.g. `system.lang.Math`) participate in the body's `Type.X → Type::X` static-call rewrite; if-without-braces (single-line statement form, optionally followed by `else` + single-line) reproduces the JAR's bizarre comment-glomming output: a duplicate "preview" comment for the LAST sub-body line is emitted before the `if`, the if-line and next-line's comment are merged on one physical line with a tab separator, the body line emits without a comment, then a blank line, then `else `+space+tab+comment-for-else's-body, then else's body alone.
- `testdata/idl/LambdaObserver.idl` — non-native custom ctor (`Class(Type f) { field = f; }`): the impl class gets a definition `Impl::Impl(args) { _initializeImplementation(); <body> }` emitted *after* all method bodies (not before like the default-ctor path), and the stub-side ctor body skips the explicit `_implementation->_initializeImplementation();` line because the impl ctor body itself runs that — so the rule is "stub emits the call only when the custom ctor is `native` (no IDL body)"; non-IDL class fields (`include`-directive types like `LambdaObserverFunction`) wrap in the POD as `Optional<Reference<X* >>` (no `POD` suffix because no POD class is generated for non-IDL types — `CppRenderPODFieldType` now matches the impl field rule for the no-IDL branch); RPC enum trailing comma is gated on "abstract method *without* an IDL body" rather than just "any abstract method" — LambdaObserver has `public abstract int notifyObserverEvent(...) { ... }` (abstract WITH body) and emits no trailing comma, while PersistentMessage's `abstract native sendTo` (no body) does; the JAR normalises `if(` → `if (` in the if-without-braces emit even when the IDL author wrote no space.
- `testdata/idl/ZoneClientSession.idl` — `short` and `unsigned short` wire mangles wired into `WireMangle` (`SignedShort`/`UnsignedShort`) and `WireInsertMangle` (`unsigned short → Short` for the adapter `resp->insertShort(_m_res)` quirk, sibling of `unsigned int → Int` and `unsigned long → Long`); body rewrite `this` → `_this.getReferenceUnsafeStaticCast()` — IDL `this` refers to the stub but on the impl side the back-reference goes through the `_this` WeakReference, applies word-boundary so it doesn't touch identifiers like `pendingTasks`. Bonus confirmation: method overloading by name+param (e.g. `info(string, bool)` vs `info(int)`, `getCharacterCount()` vs `getCharacterCount(int)`, `final Time getCommandSpamCooldown()` vs `Time getCommandSpamCooldown()`) just works through the existing per-method emit — same name with different param signatures produces distinct stubs/impls/adapters/RPC enums; default-vis IDL fields between explicit `protected` ones produce a `private:` block with the JAR's transition labels; transient fields are kept in the impl class declaration but skipped from serialization.
- `testdata/idl/ZoneProcessServer.idl` — `Registry.classifies` now checks `idlNoPOD` *before* `idlClasses` so `AddNoPOD` classes return `idlNoPOD` (not `idlManaged`) — earlier `AddNoPOD` was a strict superset of `Add` because both populated `idlClasses`, so noPOD classes were misclassified as managed. The fix means `AddNoPOD` IDL classes (like `PlayerCreationManager`, `NameManager`, `HolocronManager`, `SuiManager`, `SkillManager`, `VendorManager`, `ZonePacketHandler`) now correctly forward-decl in the `.h`, `#include` in the `.cpp`, and wrap as `Reference<X* >` (not `ManagedReference<X* >`). The fixture also gained four regular-IDL managers (`ObjectController`, `FishingManager`, `GamblingManager`, `ForageManager`) that wrap as `ManagedReference<X* >` — same pattern as the existing CreatureObject etc. registrations. Existing tests remain green: the AddNoPOD classes that were previously registered (`ActiveAreaQuadTree`, `ActiveAreaOctree`, `ChatInstantMessageToCharacter`) only appeared as method-return / param types, where `CppRender*` doesn't consult classifies for non-generic types, so the reordering is a pure no-op there.
- `testdata/idl/GroundZone.idl` — `@mock` *on a method* (e.g. `@dirty @mock public native float getHeight(float x, float y);`) adds `virtual ` to BOTH the stub-class declaration AND the impl-class declaration so the gmock subclass can override. New `Method.IsMock` flag set during `lowerMethod`; `emitImplMethodDecls` adds the virtual prefix when `IsAbstract || IsMock`, `emitStubHeader` adds it when `IsVirtualStub || IsMock`. Mock-fixture file format: a trailing blank line is required after the last `MOCK_METHODn(...)` so the JAR's `};` close gets the blank line before it (Zone.mock had this; the new GroundZone.mock needs the same `\n\n` ending). Bonus confirmation: `include`-directive types can still be IDL-managed (CityRegion is `include`d in IDL but wraps as `ManagedReference<CityRegion* >` inside generic-arg positions — registration via `reg.Add` is independent of import-vs-include keyword).
- `testdata/idl/SpaceZone.idl` — final Core3-side corpus IDL, byte-identical on the first run. The clusters built up over the previous twelve IDLs all hit unchanged: `@mock` class with fixture-injected mock body (identical to Zone.mock), native ctor with `(ZoneProcessServer*, const String&)`, `Octree` and `ShipObjectTimerTask` registered as `AddNoPOD` (Reference-wrapped, forward-decl no POD), `OctreeReference` `@dereferenced` field rendering as bare value, mixed `abstract`-with-body and `native abstract` methods (the former no comma trigger, the latter triggers RPC enum trailing comma via `getZoneObjectRange`), `@nativeStub` `__name()` companion for `asSpaceZone`, all per-class emit rules carried over from Zone/GroundZone.

The 7 engine3-side corpus IDLs landed during Phase 6 (engine3 self-compile support). All test against the same `internal/golden` harness, but use `package engine.*` / `testsuite3.*`:

- `testdata/idl/ManagedService.idl`, `ManagedVector.idl`, `TestNoOrbClass.idl` — empty extends-only classes (no fields, no methods). Verified the impl-class `<no field block>` + blank-line layout.
- `testdata/idl/Facade.idl` — `@json` class with NO serializable fields. Surfaced the empty-`@json` quirk: when a class has zero fields, the JAR omits `j[ClassName] = thisObject;` from `writeJSON` (declares the local `thisObject` but never assigns it to `j`). Reproduced.
- `testdata/idl/Observable.idl`, `Observer.idl` — sibling engine3-side classes that import each other. Surfaced the cross-tree forward-decl rule: when an engine3-side IDL (`engine.*` / `system.*`) imports another engine3-side IDL, the import gets a forward-decl block (NOT `#include`). Required to break the Observable.h ↔ Observer.h circular include in engine3 self-compile. Project-side IDLs (`server.*`) keep `#include` for engine3-side imports. (Goldens still diverge for these two — the test harness doesn't load engine3 headers, so the registry can't classify Logger; live build is correct.)
- `testdata/idl/TestIDLClass.idl` — `@json` + `@local` + `@weakReference` + `@async` (still unimplemented; this is the only engine3 corpus golden that diverges for non-harness reasons). Custom-non-native-ctor placement rule confirmed: managed-parent classes emit ctor BEFORE method bodies; non-managed-parent classes emit AFTER (LambdaObserver pattern).

**Phase 6 emit fixes** (surfaced by the live build, not pre-existing corpus):

- **Cross-tree forward-decl rule** (`internal/emit/cpp/header.go`) — engine3-side IDLs forward-decl same-tree imports instead of `#include`. Required for engine3 self-compile.
- **`transient static` field declarations** (`internal/sema/{model,resolve}.go`, `internal/emit/cpp/impl.go`) — added `Field.Static`, lowered from parser, emit prefix `static ` in impl-class field block. JAR reorders `unsigned <prim>` → `unsigned static <prim>` (sibling of the `unsigned static const` constant-decl quirk). Required for CityManager / CreatureManager hand-written `.cpp` out-of-line definitions to compile.
- **`@dereferenced @rawTemplate` method return** (`internal/emit/cpp/names.go`, `internal/emit/cpp/stub.go`) — when both annotations combine, drop the trailing `*` so the return is by-value. Stub-side `static_cast<...>` mirrors. Required for `CreditObject.getOwner()`.
- **`@lua` annotation** (`internal/emit/cpp/lua.go`, ~250 lines) — emits `class Lua<Class>` wrapper INSIDE the same autogen `<Class>.h`/`<Class>.cpp` (NOT a separate file — the JAR appends after the Helper class). Per-method bodies have type-check ladder (innermost-first, negative stack indexing), declaration-order marshalling (lua_tostring/tointeger/tonumber/toboolean/static_cast<T*>(lua_touserdata)), proper return-push per category (primitives/string/class*). Generic-typed params (`Vector<string>`) emit a stub body `return 0;`. Overloaded methods dedup by name (first-occurrence-wins). The JAR's Locker-emit heuristic for setters is empirically inconsistent (CellObject yes, QuestVectorMap no — same shape) so we never emit it; runtime locking is handled by the impl method's own internal locking.
- **`native` default-ctor** (`internal/emit/cpp/impl.go`, `internal/emit/cpp/stub.go`) — when IDL declares `public native Class();` and hand-written `<Class>Implementation.cpp` provides the body, autogen must NOT emit a body (multiple-definition link error). Stub-side ctor still emits `_implementation->_initializeImplementation();` because the hand-written impl ctor may not call it itself. Account is the canonical case.
- **Empty-`@json`-class** (`internal/emit/cpp/json.go`) — when the class has no serializable fields, skip the `j[ClassName] = thisObject;` line. Facade is the canonical case.
- **Custom-ctor placement** (`internal/emit/cpp/impl.go`) — managed-parent classes (TestIDLClass, SuiBoxPage) emit non-native custom ctor BEFORE method bodies; non-managed-parent classes (LambdaObserver) emit AFTER.

Next phase: server validation downstream of idlc-go itself — see `memory/project_idlc_go_phases.md` for ongoing items, `memory/project_idlc_go_outstanding.md` for the catalogue of known gaps.

**Source-of-truth tool:** `ref/idlc.jar` is what we match. It emits `NULL` (not `nullptr`) and uses `autogen/<pkg>/Foo.h` paths in the file-header comment. There are newer idlc forks in the wild that emit `nullptr` and a different path prefix — if a user uploads a golden, regenerate it via `ref/idlc.jar` first instead of trusting the upload (see `memory/project_idlc_jar_versions.md`).

## Commands

```bash
go build ./...                                        # build idlc-go
go test ./...                                         # full test suite (lexer, parser, sema, hash, golden)
go test ./internal/golden                             # just the byte-equality test
go test ./internal/golden -update                     # rewrite golden files when behavior intentionally changes
go vet ./...

# CLI subcommands (matches the layout in docs/idl-toolchain-feasibility.md):
go run ./cmd/idlc compile -od /tmp/out testdata/idl/ChatMessage.idl
go run ./cmd/idlc dump-ast testdata/idl/ChatMessage.idl
go run ./cmd/idlc hash "ChatMessage.message"          # → 0xbd8f57ac

# Regenerate the JAR-derived hash oracle (requires ref/idlc.jar + java):
./scripts/gen-oracle.sh                               # paste output into internal/hash/crc_bzip2_test.go
```

Module path is `github.com/bholten/tools/idlc-go` (Go 1.25.9).

## Architecture

The pipeline is a one-directional chain: **lex → parse → sema → emit**.

```
.idl source ──► lexer ──► parser (AST) ──► sema (Model) ──► emit/cpp ──► .h / .cpp
                                              │
                                              └── hash (CRC-32/BZIP2)
```

- **`internal/lexer`** — hand-rolled tokenizer over `[]byte`. Tokens are split into `token.go` (kinds, keyword table, `Token`/`Pos`) and `lexer.go` (the scanner). Exposes `CaptureBalancedBlock()` for grabbing method bodies as opaque text without lexing C++.
- **`internal/parser`** — recursive-descent. `ast.go` holds the AST types (`File`, `Class`, `Field`, `Constructor`, `Method`, `Param`, `Annotation`, `Body`); `parser.go` has the parser. Method bodies are captured verbatim (`Body.Raw`).
- **`internal/sema`** — lowers an AST into a `Model`. `model.go` (data types: `Class`, `Field`, `Method`, `Param`, `Ctor`), `resolve.go` (the `Resolve(*parser.File) (*Model, error)` entry point), `types.go` (IDL → C++ type table), `annotations.go` (annotation flag detection), `rpc.go` (RPC symbol mangling + `legacyRPCSeeds` map).
- **`internal/hash`** — CRC-32/BZIP2 of `"ClassName.fieldName"`. Validated against 23 oracles — the corpus, fresh JAR-generated probe hashes, the standard `"123456789"` CRC vector, and the empty string. Non-negotiable: regressing this breaks every existing BerkeleyDB record.
- **`internal/emit/cpp`** — splits cleanly into one file per generated C++ class (`stub.go`, `impl.go`, `adapter.go`, `helper.go`, `pod.go`, `json.go`). `header.go` and `source.go` orchestrate the per-class emitters into the two output files; `generator.go` is the public entry; `names.go` holds shared helpers (param decls, include translation, type mangling). **Style is intentionally explicit `fmt.Fprintf` — no templates, no abstraction.** Each function mirrors its target output section so `git diff` debugging stays cheap.
- **`internal/cli`** — subcommand dispatch (`compile`, `dump-ast`, `hash`, `help`). The legacy-flag form (`-sd / -od / -cp / -rbcpp`) for CMake drop-in compatibility is *not yet wired*; that's a follow-up task tied to engine3 cutover.
- **`internal/golden`** — `golden_test.go` runs the full pipeline against `testdata/idl/ChatMessage.idl` and diffs against `testdata/autogen/server/chat/ChatMessage.{h,cpp}`. Run with `-update` to refresh the golden files when behavior intentionally changes. On mismatch, the actual output is dumped to `<path>.got` for offline diffing.
- **`cmd/idlc/main.go`** — three-line entry point; just calls `cli.Run`.

## Subtle quirks the emitter has to reproduce

These are JAR behaviors discovered while chasing byte-equality. None are bugs in our code — they're in the spec we're matching. When a diff regresses, suspect one of these first.

**Whitespace / formatting:**
- `if(...)` and `switch(...)` — no space after the keyword in some generated branches.
- `TypeInfo<String >` — trailing space before `>`.
- `String FooImpl::bar() const{` — no space between `const` and `{` for `@read` methods on the implementation side (the stub side keeps the space).
- The header's final `#endif` references the *POD* guard token (`#endif /*CHATMESSAGEPOD_H_*/`) even though the opening `#ifndef` uses the class name. Reproduced verbatim.
- `_serializationHelperMethod` ends with a stray blank line before its closing brace.
- `_getImplementation()` has a leading blank line and a single space before `if (!_updated)` inside the body.
- Adapter case body's by-ref parameter declarations are indented with three tabs **plus a leading space**: `\t\t\t String msg; inv->getAsciiParameter(msg);`.
- `writeObjectMembers` wraps its per-field block in `if (field) { ... }` *at single-tab column* (not nested indent) for the POD class.
- The `engine3 IDL compiler 0.70` version string in the file header comment is hardcoded — we're claiming to be 0.70-compatible.
- Body source-comment whitespace: `\t// <idlPath>():  <idl-leading-ws><body>` where `<idl-leading-ws>` collapses runs of spaces to a single space but preserves tabs as-is.

**Type / mangle quirks:**
- `boolean` mangles to `BOOL` in the RPC enum (not `BOOLEAN`).
- `unsigned int`, `unsigned long` drop the "unsigned " prefix in the RPC enum (`INT`, `LONG`).
- `unsigned int` returns: adapter uses `resp->insertInt(_m_res)` while add/get use `getUnsignedIntParameter` / `addUnsignedIntParameter`. Inconsistent inside the JAR; we mirror via `sema.WireInsertMangle`.
- `unsigned long` IDL → `unsigned long long` C++ (NOT `uint64`). Same for `int`/`long` (no width promotion). Keep `idlToCpp` close to the raw C++ type names.

**Root vs non-root class:**
- Root class has no `extends` — bases switch to `DistributedObjectStub/Servant/Adapter/POD`. `IsRoot()` on `sema.Class` gates dozens of small forks.
- Root header uses TWO namespace blocks (stub+impl+adapter+helper / POD) instead of three.
- Root `writeObjectMembers` starts `_count = 0` (no parent call) and appends a special `_className` block at hash `0x76457cca` (computed as `nameHash("_className")`). Returns `_count + 1` to account for it. The comment uses `//_className` with no `Class.` prefix.
- Root `readObjectMember` has a special `if (nameHashCode == 0x76457cca) {//_className ...}` block before the regular switch.
- Root impl `writeJSON` doesn't call parent and appends `j["_className"] = _className;` after the per-class object.
- Root POD `writeObjectCompact` doesn't call parent.
- Root adapter ctor uses `: DistributedObjectAdapter(static_cast<DistributedObjectStub*>(obj))`. Default invokeMethod case throws `Exception("Method does not exists")`.

**Visibility / annotation behavior:**
- Stub class header + source + RPC enum + adapter all filter to PUBLIC methods. Protected methods (e.g. `_setClassName`) live impl-only.
- `@nativeStub` methods get a `void __name(...)` companion in the protected section of the stub class header; the .cpp stub method body uses `__name` (the public-facing `name` is hand-written native code).
- `@noImplementationDeclaration` suppresses the impl class header declaration entirely (e.g. `writeJSON`, `readObject`, `writeObject` are pre-declared as boilerplate virtuals).
- `abstract` modifier adds `virtual ` prefix on impl class header declaration.
- `@dirty` and `@read` BOTH use `_getImplementationForRead()` in the stub method body (JAR rule, surprising — `@dirty` is "skip lock" not "read-lock").
- `transient` fields are declared in the impl class but skipped from `readObjectMember`/`writeObjectMembers`/POD entirely. EXCEPT the boilerplate `_className` (which is special-cased into root-class serialization).
- `@dereferenced` on a field rewrites body references: `field.X → (&field)->X`, bare `field → (&field)`. Single-pass regex per field via `\bfield\b\.?`.

**Header structure:**
- Default param values appear in stub + impl declarations but are DROPPED from adapter declarations.
- Doc comments (`/** ... */`, distinct from `/* ... */`) are captured by the lexer and emitted verbatim before stub + impl class header declarations of the next member. Plain block comments are still skipped.

## Testing strategy

1. **Unit tests** per package — small, focused, written first.
2. **Golden-file diff** — current source of truth for emit correctness. Currently covers ChatMessage; expand to cover ManagedObject, a `@mock` class, a `@rawTemplate` class, and a manager-style class with mixed locking annotations as Phase 4 lands.
3. **Semantic tests** (planned) — assertions independent of whitespace: every persistent field hash matches; every non-`@local` method gets a stub; `@nativeStub` methods get no body; etc. These will become CI gating once the semantic-vs-golden split is wired into `idlc-go verify --mode=semantic|golden`.

The JAR is available at `ref/idlc.jar` and runnable via Java for oracle generation. `scripts/gen-oracle.sh` wraps the invocation; `scripts/oracle/src/probe/Probe.idl` is the probe source — extend it and rerun the script to add hash oracles.

## Hash compatibility — the load-bearing constraint

For each managed-object field, idlc emits a 32-bit name hash (e.g. `0xbd8f57ac //ChatMessage.message`) into the persistence switch and the on-the-wire format. **These hashes are baked into existing BerkeleyDB databases.** Reproducing them byte-for-byte is the only hard correctness bar for this project.

The hash is **CRC-32/BZIP2** over the ASCII bytes of `ClassName.fieldName`:

- polynomial `0x04C11DB7`
- init `0xFFFFFFFF`
- no input/output reflection
- final XOR `0xFFFFFFFF`

Go's `hash/crc32` does **not** ship a preconfigured BZIP2 variant (it uses bit-reflection; BZIP2 doesn't), so a hand-rolled table-based implementation is required. A reference implementation lives in `docs/idl-toolchain-feasibility.md` § Appendix.

Known oracles to validate against (from the existing autogen tree the JAR produced): `ChatMessage.message → 0xbd8f57ac`, `ChatManager.systemRoom → 0x783bbc2f`. Any change touching `internal/hash` or `model.AttachFieldHashes` should be checked against these.

The RPC method ids are different — only the first per class needs a stable seed; the rest are sequential C++ enum values. Byte-for-byte parity with the JAR's RPC seeds is *not* required for runtime correctness (Core3 only ever runs in-process; the RPC marshalling path is dead code), but it *is* required if you want clean `git diff` validation against the JAR's autogen output. There is no known formula for the JAR's seeds; if parity matters, plan to extract a one-time `class+sig → seed` CSV from the existing autogen.

## Annotations to support

Roughly 20 functional annotations across the 328-IDL Core3 corpus. Frequency and semantics are enumerated in `docs/idl-toolchain-feasibility.md`. The high-impact ones the codegen has to honour:

- **Locking**: `@preLocked`, `@arg1preLocked`, `@arg2preLocked`, `@read`, `@dirty` — change which lock (if any) wraps the implementation call site.
- **Dispatch**: `@local` (skip RPC stub entirely), `@nativeStub` (forward decl only — body is hand-written C++), `@virtualStub` (emit `virtual`).
- **Storage / serialization**: `@json` (emit `writeJSON`), `@weakReference`, `@dereferenced`, `@reference`, `@rawTemplate(value = "...")` (opaque C++ pass-through — capture literal, substitute).
- **Test plumbing**: `@mock` (emit gmock subclass when `COMPILE_TESTS=ON`).
- **Lua bridge**: `@lua`.

Javadoc-style tags (`@param`, `@return`, `@TODO`, `@quest`, `@spice`, `@paran` typo, etc.) live in `/* ... */` comments and must be ignored by the lexer. The lexer must skip block comments **before** annotation parsing, otherwise stray doc tags will be misread as annotations.

## Validation strategy

The 328-IDL Core3 corpus + 8 engine3 IDLs is the test suite. There is no formal grammar; the JAR's output is the spec.

- `testdata/idl/` — checked-in IDL inputs (currently 13 files, all from `server.chat` and `server.zone`).
- `testdata/autogen/` — golden output the JAR produced for those IDLs. `git diff --exit-code` against a regenerated tree is the validation harness when one is wired up.

When adding emit logic, regenerate the relevant fixture and diff against the golden tree. Whitespace, member ordering, `#include` order (the C++ tree's `.clang-format` has `SortIncludes: false`), brace placement, and blank-line counts all matter for diff parity.

## Conventions specific to this project

- `internal/` packages should not import each other in cycles; the lex→parse→sema→emit chain is one-directional.
- Method bodies in IDL are captured raw and re-emitted verbatim. **Don't try to understand C++ in the parser.** `lexer.CaptureBalancedBlock()` exists exactly so the parser can defer.
- The IDL → C++ type table in `internal/sema/types.go` is small and explicit. Don't add inference; keep the mapping legible. The full type rendering (including generics) goes through `sema.CppRender(parser.Type)`; pointer-return decisions go through `sema.IsPointerReturn`.
- `legacyRPCSeeds` in `internal/sema/rpc.go` is the per-class CSV of "first non-@local method's RPC seed" extracted from the autogen tree. Currently 2 entries (ChatMessage.setString, ManagedObject.updateForWrite). Add a new entry whenever a new IDL's first method needs explicit numbering. The JAR's seed formula remains unsolved.

## Iteration loop for adding a new IDL

The diff-fix-rerun cadence is the same each time:

1. Drop the `.idl` into `testdata/idl/` and the matching `.h`/`.cpp` into `testdata/autogen/<pkg-path>/`. Regenerate the goldens via `ref/idlc.jar` (NOT a user-uploaded copy — those may be from a different idlc; see "Source-of-truth tool" above).
2. Add a `TestGolden<Class>` entry to `internal/golden/golden_test.go` calling `runGolden`.
3. Run `go test ./internal/golden/...`. The test dumps the actual output to `<path>.got` on mismatch; offline-diff that against the golden.
4. Each new IDL tends to surface 5–15 new emit rules. Add to `sema/types.go`, `sema/rpc.go`, or the `internal/emit/cpp/*.go` files as needed. Keep the "ugly but obvious" style — `fmt.Fprintf` per output line, no templates.
5. After each fix, also rerun the *full* suite (`go test ./...`) to confirm earlier goldens still pass. They will *frequently* break when adding a new mangle rule or visibility filter.
6. When green: clean up `.got` files (`rm -f testdata/autogen/**/*.got`), update CLAUDE.md's quirks list if you discovered a new one, append to `legacyRPCSeeds` if applicable.

## Things to refuse / scope creep to push back on

The feasibility doc lists scope traps; the same boundaries apply here:

- Don't redesign the IDL syntax. Every input file in the upstream tree would need migrating.
- Don't redesign the codegen output structure. The 5-class generated layout is the runtime contract with engine3.
- Don't fix typo annotations (`@paran`, `@paramt`). They live in comments and affect nothing.
- Don't add a parser library (`participle`, etc.). The grammar is small enough for hand-written recursive descent; a library is overkill and complicates dependency-free distribution.
