# IDL Toolchain: What `idlc.jar` Does and Whether We Can Drop It

Status: investigative documentation. No code or build changes proposed yet.

This document explains what the `idlc.jar` IDL compiler shipped under `MMOCoreORB/utils/engine3/MMOEngine/lib/idlc.jar` actually generates, why it exists, and what the tradeoffs are between the three deletion strategies the team is considering. The proximate motivation is removing the JRE/Java dependency from the toolchain.

## TL;DR

* `idlc` is a domain-specific code generator for a CORBA-style **distributed-object system**. Each `.idl` file declares one "managed" class; idlc emits 5–6 cooperating C++ classes per IDL — RPC stub, server-side implementation skeleton, dispatch adapter, factory helper, persistence POD, and (optionally) a gmock mock.
* The JAR is **183 KB, obfuscated by Allatori v2.8** (single-letter package/class names, e.g. `org.sr.idlc.a.b.a`). Engine3 is a public GitHub repo (`swgemu/engine3`) but it ships only the JAR — no Java sources. Decompilation is possible but you get unreadable identifiers.
* 328 IDL files currently expand to **656 generated `.cpp`/`.h` files totalling ~509,000 lines of C++**, all in `MMOCoreORB/src/autogen/`. Modifying any IDL re-runs the JVM once per file; CMake wires one `add_custom_command` per IDL.
* The single biggest constraint on any replacement strategy: **persistent BerkeleyDB records and on-the-wire RPCs are keyed by precomputed name hash codes** (e.g. `0xbd8f57ac //ChatMessage.message`). Whatever replaces idlc has to produce **byte-identical hashes and field ordering** or existing databases break.
* The three options the team has identified (snapshot autogen / decompile / replace) are all viable; the right answer depends on whether you ever expect to add or rename a field on a managed class again.

## What an IDL file actually is

The `.idl` syntax is a small Java-shaped DSL. Example, from `MMOCoreORB/src/server/chat/ChatMessage.idl:1-25`:

```idl
package server.chat;

import engine.core.ManagedObject;

@json
class ChatMessage extends ManagedObject {
    protected string message;

    public ChatMessage() { message = ""; }

    public void setString(final string msg) { message = msg; }

    @read
    public string toString() { return message; }
}
```

Three things to notice:

1. **Method bodies are allowed inline.** For trivial classes like `ChatMessage` there is *no* hand-written `*Implementation.cpp`; the IDL is the whole truth.
2. **For non-trivial classes the IDL only declares signatures.** `ChatManager.idl` is 485 lines of declarations; the bodies live in `ChatManagerImplementation.cpp` (2,896 lines). 269 IDLs in the tree have a paired `*Implementation.cpp`.
3. **Annotations carry semantics.** They are how the IDL conveys things C++ has no syntax for: locking discipline (`@preLocked`, `@arg1preLocked`), serialization shape (`@json`, `@dereferenced`, `@weakReference`), method-call dispatch (`@dirty`, `@local`, `@read`), test plumbing (`@mock`), C++ escape hatches (`@rawTemplate`, `@nativeStub`).

### Annotation inventory (across all 328 IDLs)

| Annotation        | Count | What it controls                                                                 |
|-------------------|------:|----------------------------------------------------------------------------------|
| `@dirty`          | 1559  | Method skips lock acquisition (dirty read).                                      |
| `@local`          | 1406  | Method is C++-only; idlc skips RPC stub generation for it.                       |
| `@read`           | 1379  | Method takes a read-lock instead of a write-lock.                                |
| `@preLocked`      | 1069  | Caller already holds the receiver's lock; skip lock acquisition.                 |
| `@dereferenced`   |  943  | Field/parameter is embedded by value, not by pointer or reference.               |
| `@json`           |  199  | Generate `writeJSON()` for the class.                                            |
| `@weakReference`  |  163  | Field stored as `WeakReference<>`.                                               |
| `@arg1preLocked`  |  159  | Argument 1 to the method is already locked by the caller.                        |
| `@rawTemplate`    |   92  | Escape hatch: emit an arbitrary C++ type (used for STL containers, custom refs). |
| `@mock`           |   82  | Emit a gmock subclass when `COMPILE_TESTS=ON`.                                   |
| `@reference`      |   58  | Emit C++ reference parameter / return.                                           |
| `@nativeStub`     |   45  | The stub method is hand-written in C++ — emit a forward decl only.               |
| `@virtualStub`    |   24  | Stub method is `virtual`.                                                        |
| `@lua`            |   23  | Method is exposed to the Lua bridge.                                             |
| `@arg2preLocked`  |    7  | As `@arg1preLocked` but for the second argument.                                 |
| `@transactional`  |    6  | (Probably) flags the method as transactional. Verify against generated output.   |
| `@notifyClient`   |    1  | Used once. Likely emits a client-broadcast call.                                 |
| `@nonTransactional`|   1  |                                                                                  |
| `@final`          |    1  |                                                                                  |

The remaining annotations seen (`@param`, `@return`, `@pre`, `@post`, `@error`, `@TODO`, `@quest`, `@gambling`, `@spice`, `@crafting`, `@slicing`, `@city`, `@obj`, `@notifyClient`, `@back`, `@cancel`, `@ok`, `@paran`, `@paramt`) are javadoc-style documentation tags — they live in IDL comments and are not parsed.

So a port has to faithfully implement **roughly 20 functional annotations**, plus the tiny "primitive types" surface (`int`, `unsigned int`, `long`, `unsigned long`, `byte`, `boolean`, `float`, `string`, plus templated containers via `@rawTemplate`).

## What idlc generates

For each IDL class `Foo`, idlc writes two files into `MMOCoreORB/src/autogen/<package-path>/`: `Foo.h` and `Foo.cpp`. Inside those files, six classes appear (the optional ones depend on annotations / build flags):

```
+------------------+      +-----------------------+      +--------------------+
| Foo (Stub)       |----->| FooImplementation     |<-----| FooAdapter         |
| public API       |      | hand-written bodies   |      | RPC dispatch       |
| RPC marshalling  |      | + locking, serialize  |      |                    |
+------------------+      +-----------------------+      +--------------------+
        |                                                          ^
        |                                                          |
        v                                                          |
+------------------+      +--------------+                +-------------------+
| FooHelper        |      | FooPOD       |                | (Optional)        |
| factory singleton|      | partial-update                | MockFoo           |
| broker registry  |      | shadow type   |                | gmock subclass    |
+------------------+      +--------------+                +-------------------+
```

### 1. `Foo` — the **Stub**

The thing the rest of the codebase holds pointers to. It has no fields beyond the inherited `_impl` pointer. Each non-`@local` method generates a body that looks like this (`MMOCoreORB/src/autogen/server/chat/ChatMessage.cpp:29-42`):

```cpp
void ChatMessage::setString(const String& msg) {
    ChatMessageImplementation* _implementation =
        static_cast<ChatMessageImplementation*>(_getImplementation());
    if (unlikely(_implementation == NULL)) {
        if (!deployed)
            throw ObjectNotDeployedException(this);

        DistributedMethod method(this, RPC_SETSTRING__STRING_);
        method.addAsciiParameter(msg);
        method.executeWithVoidReturn();
    } else {
        _implementation->setString(msg);
    }
}
```

Two paths: in-process (call the impl directly) or remote (marshal arguments, dispatch via the `DistributedMethod` runtime in engine3). The method id `RPC_SETSTRING__STRING_ = 3293646548` is computed by hashing the method name + signature; the same hash is the dispatch key inside the Adapter.

### 2. `FooImplementation` — the **Implementation skeleton**

Holds the actual fields. Inherits the lock primitives (`lock`, `rlock`, `wlock`, `unlock`, `runlock`) — idlc emits trivial wrappers that delegate to the stub's mutex (`MMOCoreORB/src/autogen/server/chat/ChatMessage.cpp:111-137`). Hand-written method bodies in `*Implementation.cpp` go on this class.

It also carries the **persistence machinery**:

* `readObject` / `writeObject` — binary BerkeleyDB serialization.
* `readObjectMember` / `writeObjectMembers` — per-field dispatch keyed by **a 32-bit name hash** (`MMOCoreORB/src/autogen/server/chat/ChatMessage.cpp:165-200`):

```cpp
case 0xbd8f57ac: //ChatMessage.message
    TypeInfo<String>::parseFromBinaryStream(&message, stream);
    return true;
```

* `writeJSON` — emitted only when `@json` is present.

The hash codes are **the on-disk schema**. Existing BerkeleyDB databases under `MMOCoreORB/bin/databases/` contain records keyed by these hashes. Any replacement code generator has to reproduce them exactly.

### 3. `FooAdapter` — the **Server-side RPC adapter**

Switches on the same method id used in the stub and calls the matching Implementation method. This is what receives an inbound `DistributedMethod` and turns it back into a C++ call.

### 4. `FooHelper` — the **factory singleton**

Static initializer registers the class with the distributed-object broker so the OID system can resurrect objects from BerkeleyDB on demand:

```cpp
DistributedObject* instantiateObject();
DistributedObjectPOD* instantiatePOD();
DistributedObjectServant* instantiateServant();
DistributedObjectAdapter* createAdapter(DistributedObjectStub* obj);
```

### 5. `FooPOD` — the **plain-old-data shadow**

Mirrors the field layout but every field is wrapped in `Optional<>`. Used for partial-update serialization (e.g. when a single field needs to be persisted without re-writing the whole object). `MMOCoreORB/src/autogen/server/chat/ChatMessage.h:151-168`:

```cpp
class ChatMessagePOD : public ManagedObjectPOD {
public:
    Optional<String> message;
    ...
};
```

### 6. `MockFoo` — optional gmock subclass

Generated only when the IDL has `@mock` and CMake was run with `COMPILE_TESTS=ON` (controlled at `MMOCoreORB/CMakeLists.txt:220-233`).

## How the build invokes idlc

CMake adds one `custom_command` per IDL. The actual command (extracted from `MMOCoreORB/build/unix/ninja-debug/build.ninja:11929`) is:

```
java -XX:TieredStopAtLevel=1 -client -Xmx128M \
  -cp utils/engine3/MMOEngine/lib/idlc.jar \
  org.sr.idlc.compiler.Compiler \
  -outdir autogen \
  -cp utils/engine3/MMOEngine/src \
  -silence -rbcpp -sd src \
  server/chat/ChatManager.idl
```

* `-cp <engine3-src>` — the IDL compiler resolves cross-package imports against engine3's IDL tree (e.g. `engine.core.ManagedObject`).
* `-rbcpp` — emit C++ (vs. the `-rb` "rebuild all" mode that does a whole-tree pass).
* `-sd src` — source root.
* `-silence` — suppress non-error logging.

The `Makefile` target `idl` walks the tree once. The CMake `custom_command` per IDL handles incremental rebuilds. **One JVM per IDL fork** — 328 forks for a clean rebuild — is the dominant build cost on a cold tree, though Ninja parallelizes them well.

JRE requirement: any Java 8+ runtime works (the JAR was built in 2019; the launcher script `MMOCoreORB/utils/engine3/MMOEngine/bin/idlc` is the only place the path lives).

## Why this exists

Reading the generated code, the design intent is clear: engine3 is a **CORBA-derived distributed-object framework** built circa 2003–2010 on the assumption that game objects might one day be sharded across multiple processes (or hosts). Every managed object has:

* A globally-unique 64-bit OID.
* A backing BerkeleyDB record (so it survives a restart).
* A lock (so concurrent zone threads can mutate it safely).
* RPC stubs (so even in-process callers go through the same dispatch path that *would* work cross-process).

idlc is the spec-to-glue compiler that makes writing one of those classes only twice as expensive as writing a normal C++ class instead of ten times. In practice Core3 has never actually run the zone server sharded — every object lives in-process and the RPC path is the dead branch of `if (_implementation != NULL)`. **The framework is paying for distribution it doesn't use,** which is part of why removing the JAR feels attractive.

## Modern equivalents (and why they don't quite fit)

| Tool                     | Closest semantic match                                     | Why it isn't drop-in                                                                                                |
|--------------------------|------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------|
| **Protobuf / gRPC**      | RPC stubs + serialization                                  | No locking model, no in-process fast path, no BerkeleyDB schema compat, no field-hash stability across renames.     |
| **Cap'n Proto**          | Zero-copy serialization + RPC                              | Same gaps + on-disk format mismatch.                                                                                |
| **FlatBuffers**          | Serialization                                              | No RPC, no locking.                                                                                                 |
| **C++26 reflection**     | Could replace the codegen entirely                         | Project is locked to **C++14** (`CMAKE_CXX_STANDARD 14`, `MMOCoreORB/CMakeLists.txt`). Not an option without a major lift. |
| **`__attribute__` + macros** | Can simulate annotations                               | You'd still need a code generator to emit POD/Adapter/Helper. Macros aren't expressive enough.                       |
| **A 500-line Python script** | Bespoke replacement                                    | Closest to what we'd actually need. See option (C).                                                                  |

The mismatch is the **distributed-object semantics + name-hashed BDB schema**, not the codegen mechanics. No off-the-shelf tool encodes "one class, locked-on-method-call, persisted with stable hash keys, mockable for gtest." It's a custom DSL because the runtime is custom.

## Decision-relevant constraints

Before evaluating options, three constraints matter:

1. **The serialization format is in production data.** `bin/databases/` BDB files store records with the 32-bit name hashes baked in. If this fork is ever run against an existing database (yours or anyone else's), schema-affecting changes to idlc must reproduce those hashes byte-for-byte.

2. **The IDL is *also* documentation.** `@dirty`, `@preLocked`, `@arg1preLocked` are not just codegen hints — they encode the **locking contract** between the manager and its callers. Someone reading `CreatureManager.idl` learns what they can call without taking a lock first. Deleting the IDLs deletes that contract from the human-readable surface.

3. **engine3 is upstream.** It's a submodule pinned to `swgemu/engine3@7012c03`. If you keep tracking upstream, any new IDL features you adopt require regenerating the autogen tree, which means you still need *some* compiler — yours or theirs.

## Option A: Snapshot autogen, delete IDLs, drop JRE

Run `make rebuild-idl` once on a known-good tree, `git add MMOCoreORB/src/autogen/`, delete every `.idl`, remove the `add_custom_command` wiring, drop Java from `linux/install_deps.sh` and the docs.

**Pros**
* Smallest possible change. Probably one PR. Build time goes down (no JVM forks).
* Eliminates the opaque obfuscated dependency entirely.
* Anyone reading `Foo.h` now sees the declaration directly; no two-hop indirection.

**Cons**
* You permanently lose the IDL as a source of truth. Adding a field to a managed class becomes "edit five generated files in lockstep" — the Stub, the Adapter, the Implementation header, the POD, the readObjectMember switch, and the writeObjectMembers list, each in two places (.h and .cpp). It is doable but error-prone, and the BerkeleyDB hash math (`0xbd8f57ac //ChatMessage.message`) has to be reproduced by hand from whatever the original hashing function was.
* Annotations like `@preLocked` and `@dirty` no longer have anywhere to live; the locking contract becomes an oral tradition.
* Every future engine3 upstream sync that touches an IDL becomes a manual port.

**Best if**: you've decided you will *never* add or rename a field on a managed class, you'll never sync engine3 again, and you are happy treating the autogen tree as hand-written C++ from now on.

## Option B: Snapshot autogen, **keep** IDLs as documentation, drop JRE

Same as A, but leave the `.idl` files in tree as read-only documentation. Disable the `BUILD_IDL` CMake path by default; advanced users who *do* want to regenerate can flip a flag and supply Java themselves.

**Pros**
* Keeps the locking-contract documentation intact.
* Mainline build no longer needs Java.
* Migration is reversible — if you later change your mind and reimplement idlc (option C), the IDLs are still there.

**Cons**
* Drift risk. Without enforcement, the IDLs and the autogen will diverge over months. The IDLs become subtly wrong. A new contributor edits the IDL and is surprised when nothing happens.
* Mitigation: a CI job that runs idlc and `git diff --exit-code` against the committed autogen — but then you still need Java, just only in CI.

**Best if**: you want the JRE out of the contributor toolchain immediately but don't want to commit to "no schema changes ever," and you're willing to accept some drift or fund a CI Java install.

## Option C: Decompile or reimplement idlc

Two sub-flavors:

**C1 — Decompile + clean up.** CFR/Procyon will produce compilable Java from the obfuscated bytecode. Allatori is a name obfuscator, not a control-flow obfuscator, so the decompiled code will be structurally faithful but every identifier is `a` / `b` / `c`. Recovering meaningful names is days-to-weeks of grunt work but tractable. Output: a maintained Java project, possibly checked into the repo so the JAR is reproducible. **Doesn't actually drop the JRE** — just makes it less opaque.

**C2 — Rewrite from scratch.** The IDL grammar is small (Java-ish, ~12 statement forms, ~20 annotations, no generics beyond pass-through to `@rawTemplate`). The tricky parts are not the parser — they are the **stable name hash** function (must be reverse-engineered from existing autogen output by reading the `0x...` constants and matching them back to `ClassName.fieldName` strings) and the **locking discipline emitter** (correctly inserting lock/unlock pairs around `@preLocked` boundaries). Realistic estimate for a careful Python or Go reimplementation: 1–3 weeks for one engineer, dominated by validating that diffing the new tool's output against the JAR's output for all 328 IDLs comes back empty.

**Pros (both)**
* Removes the opaque JAR.
* C2 also removes Java itself, which was the explicit goal.
* You own the tool. Adding annotations or output formats becomes a normal change.

**Cons**
* Real work. C2 is the only path that actually drops the JRE *and* keeps schema evolution viable.
* Validation matters: a one-bit difference in a hash code corrupts every database that has the affected field.

**Best if**: you expect to keep evolving managed-object schemas (renaming fields, adding subclasses, etc.) and you want the freedom to do that without hand-editing six generated C++ files at a time.

## Recommendation framework (not a recommendation)

The choice maps cleanly to your tolerance for future schema changes:

* **Never going to touch a managed class again** → Option A.
* **Want JRE gone today, accept drift risk, may or may not revisit** → Option B.
* **Plan to keep evolving the object model** → Option C2 (or C1 as a stepping stone).

The hybrid B is the lowest-friction *immediate* win. C2 is the highest-value long-term answer if you are already considering forking the project for private playtest. A is appealing only if you are sure this is the last time you'll think about idlc.

## Open questions worth answering before committing

1. ~~**What is the hash function?**~~ **Resolved.** It is **CRC-32/BZIP2** (polynomial `0x04C11DB7`, init `0xFFFFFFFF`, no input/output reflection, final XOR `0xFFFFFFFF`) computed over the ASCII string `ClassName.fieldName`. The runtime implementation lives in `MMOCoreORB/utils/engine3/MMOEngine/src/system/lang/String.h:172-176` (a `constexpr` recursive form using a 256-entry table at line 26). Verified against seven known field hashes: `ChatMessage.message` → `0xbd8f57ac`, `ChatManager.systemRoom` → `0x783bbc2f`, etc. all reproduce exactly.
2. **How does idlc resolve cross-IDL imports?** Engine3 itself ships **8 IDL files** under `MMOCoreORB/utils/engine3/MMOEngine/src/` (e.g. `engine/core/ManagedObject.idl`, `engine/util/Observer.idl`). The `-cp utils/engine3/MMOEngine/src` flag is how Core3 IDLs find them. A replacement tool needs to walk both trees, or the engine3 IDLs need to be moved into Core3's own source tree.
3. **Do we ever sync engine3 upstream?** If yes, dropping idlc is genuinely costly — every upstream IDL change becomes a manual port. If we already plan to fork engine3 and stop tracking upstream, that cost goes to zero.
4. **Are any out-of-tree consumers (analytics, dump tools) reading the BerkeleyDB files directly?** If yes, they encode the same hash assumptions and constrain any future schema changes regardless of which option we pick.

## Appendix: Detailed Go-rewrite playbook

This section is for someone seriously evaluating C2. It is a sketch of where the work lives, not a final design.

### What got easier after a closer look

Three things reduced the risk estimate from "1–3 weeks" toward the lower end.

**1. The persistent name hash is a stock CRC variant.** It is not a custom algorithm. Go's standard library `hash/crc32` doesn't ship the BZIP2 polynomial preconfigured (it uses bit-reflection, BZIP2 doesn't), but a hand-rolled implementation is ~15 lines:

```go
// Generate the table once at package init.
var crcTable = func() (t [256]uint32) {
    const poly uint32 = 0x04C11DB7
    for i := 0; i < 256; i++ {
        c := uint32(i) << 24
        for j := 0; j < 8; j++ {
            if c&0x80000000 != 0 {
                c = (c << 1) ^ poly
            } else {
                c <<= 1
            }
        }
        t[i] = c
    }
    return
}()

func nameHash(s string) uint32 {
    crc := uint32(0xFFFFFFFF)
    for i := 0; i < len(s); i++ {
        crc = crcTable[((crc>>24)^uint32(s[i]))&0xFF] ^ (crc << 8)
    }
    return ^crc
}
```

This is the only hash that goes to disk. Once it matches, the persistence schema is safe.

**2. The RPC method ids are not persisted and don't need byte-for-byte parity with the JAR.** Looking at `MMOCoreORB/src/autogen/server/chat/ChatManager.cpp:37`, the entire RPC enum is written as `enum {RPC_STOP__ = 3192532258, RPC_INITIATEROOMS__, RPC_INITIATEPLANETROOMS__, ...}` — only the **first** id has an explicit number, the rest are sequential C++ enum values. So idlc's job is to pick *some* deterministic seed, and the rest of the ids are free.

   In single-process operation (which is how Core3 is actually deployed — every `_implementation != NULL` branch in the stub fires, the RPC marshalling path is dead code), even the seed value doesn't matter. A Go reimplementation could use any deterministic per-class seed (e.g. `nameHash(className)`) and the server would behave identically.

   The seed format used by the original JAR is *not* `nameHash` of any obvious string — I tried a dozen formats (`ClassName.method`, `ClassName::method__String_`, `RPC_METHODNAME__STRING_`, etc.) and none reproduced the observed values like `0xC4510ED4` for `ChatMessage.setString`. If you need byte-for-byte parity with the JAR's output (so you can validate via `git diff`), you'll have to either reverse-engineer the format from a larger sample or commit a one-time CSV mapping `class+sig → seed` extracted from the existing autogen and reuse it for known classes.

**3. `@rawTemplate` is opaque pass-through.** Examples: `@rawTemplate(value = "uint64, Vector<int>")`, `@rawTemplate(value = "BehaviorTreeSlot, Reference<Behavior*>")`. The contents are arbitrary C++. The IDL parser doesn't need to understand them — it just needs to capture the literal string and substitute it into the generated type. 92 occurrences across the tree.

### What stays risky

**1. There is no formal grammar.** The Java source for idlc is not shipped, the obfuscated JAR has no docs, and the only specification of "valid IDL" is "whatever produces correct output today." This makes the **328-IDL corpus your test suite**. Validation strategy:
   * `make rebuild-idl` against the unmodified Java tool produces a known-good `src/autogen/` tree. Capture it.
   * Run the Go tool, regenerate `src/autogen/`.
   * `git diff --stat` should be empty. Anywhere it isn't, you have a bug.

**2. Annotation interactions.** Individual annotations are simple. Combinations get hairy:
   * `@preLocked` + `@read` — does the method skip lock acquisition entirely, or take a read-lock?
   * `@arg1preLocked` on a method whose first arg is itself `@dereferenced` — is the lock taken on the value or the underlying object?
   * `@dirty` + `@local` — `@local` skips RPC stub generation entirely; does `@dirty` still affect lock acquisition in the impl call site?
   * `@nativeStub` says "the stub is hand-written in C++, just emit a forward declaration" — there are 45 of these. The Go tool has to detect them and *not* emit a body.
   * `@virtualStub` (24 uses) — emit `virtual` qualifier on the stub method.

   The Java tool's behavior on each combination is the spec. Plan to read its output for examples of each pairwise combination before writing the corresponding emit logic. **A grep over the existing autogen tree gives you a free oracle for every annotation combination.**

**3. The IDL parser has to tolerate javadoc-style nonsense.** `@param`, `@return`, `@pre`, `@post`, `@TODO`, `@quest`, `@gambling`, `@spice`, `@paran` (typo), `@paramt` (typo), `@back`, `@cancel`, `@ok` — all appear as `@`-prefixed tokens but only inside `/* ... */` comment blocks. The lexer needs to skip block comments cleanly *before* annotation parsing kicks in, otherwise a stray `@TODO` inside a doc comment will be picked up as an annotation. Trivial in practice, easy to forget.

**4. Output stability is a moving target.** `git diff` validation needs the Go tool to emit:
   * Member declarations in the same order as the input IDL.
   * `#include` directives in the same order as the JAR (no alphabetization — `.clang-format` has `SortIncludes: false`).
   * Identical whitespace, identical brace placement, identical blank-line counts between sections.
   * Identical emission of internal helper names like `RPC_SETSTRING__STRING_` (so the parameter-mangling rule that turns `String` into `STRING_` and so on has to be reproduced).

   None of this is hard but it's tedious. Budget ~30% of the project for "getting whitespace right." Or skip strict parity and invest in a semantic-equivalence test (compile both autogen trees, link both binaries, run gtest against both — same green) which is more robust but a different kind of work.

**5. The migration coexists with Java, not replaces it, until cutover.** Realistically the workflow is:
   * Phase 1: Go tool runs in CI alongside the JAR, output diffed automatically. Build still uses the JAR.
   * Phase 2: Build switches to Go tool, JAR retained as a comparison oracle. Both still required.
   * Phase 3: JAR removed, Java requirement dropped from docs and bootstrap scripts.

   So you don't actually delete Java for several weeks after the Go tool "works." Plan accordingly.

### Suggested execution order

1. **Day 1–2**: Hand-write a Go function that produces byte-identical `readObjectMember` / `writeObjectMembers` switch statements for one class (start with `ChatMessage` — 25 lines of IDL). Validate the CRC against the existing autogen. This proves the persistence-schema half of the problem is solved.
2. **Day 3–5**: Lexer + parser, hand-written recursive-descent. Target: parse all 328 Core3 IDLs + 8 engine3 IDLs without errors. No code generation yet — just confirm the AST is right by pretty-printing back to IDL and round-tripping.
3. **Week 2**: Implement the six emit modes (Stub, Implementation, Adapter, Helper, POD, optional Mock + JSON). One at a time. After each, regenerate the entire autogen tree and `git diff` against the JAR's output. Fix discrepancies. Most discrepancies in this phase are whitespace and member-ordering bugs.
4. **Week 3**: Annotation interactions, edge cases, the long tail. Expect to spend most of this week fixing bugs uncovered by `git diff` showing 12 unrelated mismatches across 30 files. The pattern: one annotation is misimplemented, fix it, 15 mismatches go away. Repeat.
5. **Cutover**: replace `idlc.jar` invocation in CMake with the Go binary. Remove Java from `linux/install_deps.sh`. Update `README.md`. Keep the JAR around for one more release cycle as an emergency fallback.

### Scope creep to refuse

* **Don't** redesign the IDL syntax. Tempting; will eat months. If the Go tool ships with a different syntax, every `.idl` file in the tree has to be migrated, and you've added a new code-mod project on top of the codegen project.
* **Don't** redesign the codegen output. The 5-class generated structure (Stub/Impl/Adapter/Helper/POD) is the runtime contract with engine3. Changing it means changing engine3 too.
* **Don't** try to support both old-format and new-format autogen during the transition. Pick one. Cut over once. Keep the JAR for a release as a safety net.
* **Don't** fix the typo annotations (`@paran`, `@paramt`). They live in comments. They affect nothing. Fixing them is a separate PR that will turn into a months-long IDL-cleanup project.

### Realistic schedule

| Phase                                      | Optimistic | Pessimistic |
|--------------------------------------------|-----------:|------------:|
| Hash function + persistence emit, one class|     2 days |      4 days |
| Parser + AST round-trip on full corpus     |     3 days |     1 week  |
| Six emit modes, byte-identical             |    1 week  |     3 weeks |
| Edge cases, annotation interactions        |    3 days  |     2 weeks |
| Validation, CI integration, cutover        |    3 days  |     1 week  |
| **Total**                                  | **~2 wk**  | **~6 wk**   |

The pessimistic column assumes you discover the RPC seed hash is something fiddly *and* you commit to byte-for-byte parity rather than semantic-equivalence validation. The optimistic column assumes you accept "diffing semantic output (compiled binary behavior) is enough, byte-level diffs in autogen are okay if functionally identical."

### Smaller question: is C2 a good fit for Go specifically?

Yes. The work is string handling, AST building, template-driven emission, and lots of file I/O — Go's strengths. `text/template` covers the emission cleanly. A hand-written recursive-descent parser works fine; the grammar is small enough that a parser library (e.g. `participle`) is overkill. Single-binary distribution means contributors don't need a Go toolchain to *use* the tool — only to develop it. Realistic LOC: 1500–3500.

The same argument applies to Rust (similar strengths, more upfront cost) or Python (faster iteration, slower runtime, but runtime doesn't matter here). Go has the edge purely on "single binary, zero runtime deps, compiles fast" — which is the thing the user wanted in the first place.
