# JAR Quirks We Deliberately Reproduce

`idlc-go`'s contract is byte-for-byte parity with `ref/idlc.jar`. The JAR has many behaviours — some clearly intentional, others looking like bugs — that we faithfully reproduce because the entire point is replacing the JAR without changing what downstream consumers see.

Each entry below states the rule, where it was first verified (corpus IDL or probe), and flags items that look like JAR bugs we replicate intentionally.

**Source-of-truth**: `ref/idlc.jar` (md5sum matches the JAR shipped inside Core3's engine3 submodule at `MMOCoreORB/utils/engine3/MMOEngine/lib/idlc.jar`). Newer idlc forks in the wild emit `nullptr` instead of `NULL` and use a different file-header path prefix; if a "golden" comes from elsewhere, regenerate via `ref/idlc.jar` first.

---

## Whitespace and formatting

### Smart-pointer trailing space
`Reference<T* >`, `ManagedReference<T* >`, `ManagedWeakReference<T* >` always have a space before the closing `>`. The space is present even when the inside doesn't end with `*` — e.g. `ManagedReference<ManagedObject >` (no asterisk inside) for `@dereferenced` IDL-managed fields.
- *Verified by:* corpus, Fields probe.

### `if(` → `if (` in body emit
The if-without-braces body emit normalises `if(` to `if (` even when the IDL author wrote no space.
- *Verified by:* Locking probe (`if(function)` in IDL → `if (function)` in autogen).

### Empty RPC enum line omitted entirely
When zero methods participate, the JAR omits `enum {};` rather than emitting an empty enum.
- *Verified by:* Fields probe (zero methods).

### Empty-class layout (no IDL fields)
- A blank line before `public:` in the impl class body (in lieu of a field block).
- `String _className;` is omitted from the POD class.
- *Verified by:* Locking probe.

### No-IDL-ctor layout (zero constructors)
- Two impl ctors (`Class()` and `Class(DummyConstructorParameter*)`) stack with no blank line between them.
- `helper.instantiateServant()` falls back to `new Impl(DummyConstructorParameter::instance())`.
- Impl default ctor body is emitted *before* the dummy ctor with explicit `: BaseImplementation()` initializer-list call.
- Stub `Class()` decl + body are omitted.
- *Verified by:* Locking and Fields probes.

### Adapter case body leading space when `final`
`final` on a parameter adds a leading space before its variable declaration in the adapter case body. `final int x` → `\t\t\t int x = ...` instead of `\t\t\tint x = ...`.
- *Verified by:* Params probe.
- *Looks like a bug?* Yes — purely cosmetic, probably an over-eager whitespace insertion.

### Header `#endif` token quirk
The trailing `#endif` references the *POD* guard token (`#endif /*CHATMESSAGEPOD_H_*/`) even though the opening `#ifndef` uses the class name (`CHATMESSAGE_H_`). Reproduced verbatim.
- *Verified by:* corpus.
- *Looks like a bug?* Yes — leftover from when guards were per-section.

### `_serializationHelperMethod` trailing blank line
`_serializationHelperMethod` ends with a stray blank line before its closing brace.
- *Verified by:* corpus.

### `_getImplementation` blank line + space
`_getImplementation()`'s body has a leading blank line and ` if (!_updated)` (single space before `if`) inside the body.
- *Verified by:* corpus.

### Body source-comment whitespace
The `\t// <idlPath>():  <body>` comment that prefixes each emitted body line collapses runs of spaces to a single space but preserves tabs as-is.
- *Verified by:* corpus.

### Re-emit `public:` after a non-public method
The impl class's trailing `WeakReference<X*> _this;` / `operator const X*()` / `_getStub()` block must be in the public section. When the IDL's last method was `protected` or `private`, the JAR re-emits a `public:` label before this block.
- *Verified by:* Dispatch probe.

### Hash literal padding
Field hashes use `0x%x` (not zero-padded `0x%08x`). E.g. `0x76457cca`, `0xbd8f57ac`.
- *Verified by:* corpus.

---

## Type rendering

### IDL primitive → C++ type table
| IDL | C++ |
|---|---|
| `string` | `String` |
| `unicode` | `UnicodeString` |
| `boolean` | `bool` |
| `byte` | `byte` |
| `short` | `short` |
| `unsigned short` | `unsigned short` |
| `int` | `int` |
| `unsigned int` | `unsigned int` |
| `long` | `long long` |
| `unsigned long` | `unsigned long long` |
| `float` | `float` |
| `void` | `void` |

The JAR keeps width-explicit C++ names (`long long`); not `int64_t` etc.

### RPC enum mangle quirks
- `boolean` → `BOOL` (not `BOOLEAN`).
- Unsigned widths drop the "unsigned " prefix in RPC enum suffixes: `unsigned int` → `INT`, `unsigned long` → `LONG`, `unsigned short` → `SHORT`.

### Wire-insert mangle (adapter `resp->insertXxx(_m_res)`)
For unsigned-width returns, the JAR uses the *signed* insert variant even though `_m_res` is declared with the unsigned width:

| Return type | Local var type | `resp->insert…` call |
|---|---|---|
| `unsigned int` | `unsigned int _m_res` | `insertInt(_m_res)` |
| `unsigned long` | `unsigned long long _m_res` | `insertLong(_m_res)` |
| `unsigned short` | `unsigned short _m_res` | `insertShort(_m_res)` |
| `int` | `int _m_res` | `insertSignedInt(_m_res)` |

Tracked via `WireInsertMangle` in `internal/sema/rpc.go` (separate from `WireMangle` for `add…Parameter`/`get…Parameter`/`executeWith…Return`).

- *Verified by:* corpus + ZoneClientSession (`unsigned short`).

### `final` interaction with return wraps
- `final int retInt()` → `const int` (prepend).
- `final ManagedObject retClass()` → `const ManagedObject*` (prepend).
- `@reference final ManagedObject retX()` → `Reference<const ManagedObject* >` — `const` goes *inside* the wrap.
- `@weakReference final` → `ManagedWeakReference<const ManagedObject* >` (same inside-wrap rule).
- *Verified by:* Returns probe.

### Generic returns use `static_cast` (not by-ref)
For class- *and* generic-typed returns, the JAR uses `static_cast<Type*>(method.executeWithObjectReturn())`. Only `String`/`UnicodeString` use the by-ref pattern (allocate temp + `executeWith…Return(byref)`).
- *Verified by:* Returns probe.

### Adapter `static_cast` strips template args from generics
For class/generic param adapter cases, the variable type is the full rendered form (`Vector<ManagedReference<X* > >*`) but the cast type strips template args entirely (`Vector*`).
- *Verified by:* Params probe.
- *Looks like a bug?* Yes — the cast loses type information. Probably a JAR shortcut.

### `@dereferenced` cast type drops trailing `*`
`@dereferenced` returns are by-value, so the static_cast drops the `*`: `static_cast<ManagedObject>` not `static_cast<ManagedObject*>`. Applies to generic returns too: `static_cast<Vector<int>>` for `@dereferenced Vector<int>`.
- *Verified by:* Returns + Generics probes.

### Param `final` adds `const ` to ALL types
Including `String`/`UnicodeString` (which we previously made `const String&` unconditionally — actual rule is "only when `final`"), classes, primitives, and generics.
- *Verified by:* Params probe.

---

## Annotations

### `@arg2preLocked`
Sibling of `@preLocked` and `@arg1preLocked` — emits `assert((arg2 == NULL) || arg2->isLockedByCurrentThread());` for the *2nd* parameter. Not exercised in corpus; surfaced by the Locking probe.

### `@dereferenced` on a primitive (e.g. `string`)
Keeps the normal Ascii/Unicode wire (`addAsciiParameter` / `getAsciiParameter`) — does NOT switch to `addDereferencedSerializableParameter`. The DereferencedSerializable wire is reserved for non-primitive `@dereferenced` params.
- *Verified by:* Params probe.

### `@rawTemplate(value="X")` always uses ` >` trailing space
Whether `X` ends with `*` or not, the JAR renders the wrap as `Type<X >`. We previously gated the trailing space on `X` ending with `*`.
- *Verified by:* Params probe.

### `@mock` on a method (not class)
Adds `virtual ` to BOTH the stub-class decl AND the impl-class decl so a gmock subclass can override.
- *Verified by:* GroundZone (corpus).

### `@virtualStub`
Same shape as `@mock` for virtual prefix — `virtual ` on BOTH stub and impl decls.
- *Verified by:* TreeEntry (corpus, abstract case) + Dispatch probe (non-abstract case).

### `@reference` adapter return — no `.get()`
`Reference<T*>` implicitly converts to `T*`, so the adapter chains directly: `T* _m_res = method(); resp->insertLong(_m_res == NULL ? 0 : _m_res->_getObjectID())`.

### `@weakReference` adapter return — uses `.get()`
`ManagedWeakReference<T*>` doesn't implicitly convert, so the JAR appends `.get()`: `T* _m_res = method().get();`.

### `@dereferenced` IDL-managed class field wrap
Wraps as `ManagedReference<X >` — no `*` inside. `@weakReference` swaps to `ManagedWeakReference<X >` (still no `*`).
- *Verified by:* Fields probe.

### `@dereferenced` generic field
Bare `Head<args>`. Inner class args still wrap per IDL classification.
- *Verified by:* `PendingMessageList`, `ChatManager`, Fields probe.

### Class-level `@dirty`
Propagates to every method — every method body uses `_getImplementationForRead()` (skip lock).
- *Verified by:* ChatManager (corpus).

### `@noImplementationDeclaration`
Suppresses the impl class header decl entirely. Used for boilerplate methods like `writeJSON`, `readObject`, `writeObject` that the impl provides directly.

### `@nativeStub`
Adds a `void __name(...)` companion in the protected stub-class section; the public-facing `name` is hand-written native code. The .cpp stub method body uses `__name`.

### `@json` writeJSON emit is uniform
For every serializable (non-transient, non-constant) field, `writeJSON` emits the same line shape regardless of field type or annotation:

- Impl: `thisObject["fieldName"] = fieldName;`
- POD:  `if (fieldName) thisObject["fieldName"] = fieldName.value();`

No per-type wire mangling — the JAR delegates everything to `nlohmann::json`'s overloaded `operator=`, which handles primitives, `String`, `ManagedReference<X*>`, `Vector<X>`, and bare `@dereferenced` types alike.

Root classes additionally append `j["_className"] = _className;` after the per-class object; non-root classes call the parent's `writeJSON` first.

- *Verified by:* Json probe — exhaustive field-type matrix (primitive, string, bool, long, float, IDL class, `@dereferenced` IDL class, `@weakReference`, `@dereferenced @weakReference`, generic primitive, generic class, `@dereferenced` generic, non-IDL `@dereferenced`, transient, static-final constants — all cleanly handled by the existing emit).

---

## Class layout

### Root vs non-root
Root classes (no `extends`) get the runtime types as their C++ bases (`DistributedObjectStub`/`Servant`/`Adapter`/`POD`). Specifics:
- Header uses TWO namespace blocks (stub+impl+adapter+helper / POD) instead of three.
- Impl `writeObjectMembers` starts `_count = 0` (no parent call) and appends a special `_className` block at hash `0x76457cca`. Returns `_count + 1`.
- Impl `readObjectMember` has a special `if (nameHashCode == 0x76457cca)` block before the regular switch.
- Impl `writeJSON` doesn't call parent and appends `j["_className"] = _className;`.
- POD `writeObjectCompact` doesn't call parent.
- Adapter ctor uses `: DistributedObjectAdapter(static_cast<DistributedObjectStub*>(obj))`.
- *Verified by:* ManagedObject (corpus).

### Trailing-segment class lookup
When the IDL body references an unqualified name (`SceneObject`), the registry resolves it via the trailing segment of registered qnames (`server.zone.objects.scene.SceneObject`). Multiple qnames can share a trailing segment — Core3 has both `server/zone/objects/scene/SceneObject.idl` (managed) AND `client/zone/objects/scene/SceneObject.h` (header-only AddNoPOD). When this happens, **managed wins over noPOD** — IDL authors mean the server-side class. Surfaced when running idlc-go over the full Core3 source tree (the test fixture's hand-curated registry never had a same-name collision). Implementation in `Registry.classifies` partitions trailing-segment matches into managed-only / also-in-noPOD buckets; managed wins if any.

### Non-managed parent
When the parent class doesn't transitively reach `ManagedObject` (e.g. `Observable`, `Observer`, `ManagedService`), the entire class falls back to `Reference<>` semantics:
- All IDL imports become `#include`s; the forward-decl section is skipped entirely.
- Class fields wrap as `Reference<X* >`, not `ManagedReference<X* >`.
- Generic-class arg wraps inside generics also use `Reference<>` (or stay bare).
- `Registry.AddNonManagedParent(name)` controls this.
- *Verified by:* TreeEntry (Observable parent), LambdaObserver (Observer parent), ZoneProcessServer (ManagedService parent).

### `implements X, Y`
Adds `, public X, public Y` to the impl class's base list. The stub class and adapter class do NOT inherit the interfaces — they're impl-only. Multi-interface (e.g. `implements Logger, Lockable`) chains naturally with commas.
- *Verified by:* ChatManager (`implements Logger`), Inheritance probe (`implements Logger, Lockable` — multi-interface).

### `finalize()` declared in IDL
Triggers special-casing:
- Skip stub class decl + body (inherits parent's finalize).
- Skip impl boilerplate `void X::finalize()` decl + definition.
- Dtor body becomes `Class::finalize();` instead of empty.
- POD dtor body becomes `finalize();`.
- *Verified by:* Zone, GroundZone.

### Native ctor with custom args
- Stub class header gets the custom-args ctor; `@mock` classes additionally get a `protected: Class() { }` slot.
- Impl class header gets `Impl(args);` + dummy ctor (no default-args ctor).
- Stub source emits `Class::Class(args)` body with explicit `_initializeImplementation()` call.
- Helper `instantiateServant()` falls back to dummy ctor.
- *Verified by:* Zone, ChatManager.

### Non-native ctor with custom args (with body)
- Impl emits `Impl::Impl(args) { _initializeImplementation(); <body> }` *after* method bodies.
- Stub-side ctor body skips the explicit `_implementation->_initializeImplementation();` line because the impl ctor body itself runs it.
- Helper still uses dummy ctor.
- *Verified by:* LambdaObserver.

### `transient` field
Kept in the impl class declaration but skipped from `readObjectMember` / `writeObjectMembers` / POD entirely. Exception: the special `_className` field is part of root-class boilerplate.

### Default-vis IDL fields
Map to C++ private (matches the JAR). Visibility transitions in the field block emit explicit `private:` / `protected:` / `public:` labels.

---

## Field rendering

### IDL-managed class field
| Variant | Form |
|---|---|
| Plain | `ManagedReference<X* > field;` |
| `@weakReference` | `ManagedWeakReference<X* > field;` |
| `@dereferenced` | `ManagedReference<X > field;` (no `*`) |
| `@dereferenced @weakReference` | `ManagedWeakReference<X > field;` (no `*`) |

### Non-IDL class field
| Variant | Form |
|---|---|
| Plain | `Reference<X* > field;` |
| `@dereferenced` | bare `X field;` (TreeEntry's `Coordinate`) |

### Generic field, non-`@dereferenced`
Outer wrap depends on the HEAD type's IDL classification:
- HEAD IDL-managed: `ManagedReference<Head<args>* >`.
- HEAD non-IDL: `Reference<Head<args>* >`.

Inner generic args independently wrap per their own IDL classification.
- *Verified by:* Fields probe (HEAD = Vector, non-IDL).

### Generic field, `@dereferenced`
Bare `Head<args>` (no outer wrap). Inner class args still wrap per IDL classification.
- *Verified by:* PendingMessageList (`@dereferenced Vector<unsigned long>`), ChatManager (`@dereferenced VectorMap<string, ChatRoom>`).

### POD form
Append `POD` suffix to every IDL-managed class name in the rendered type. Non-IDL types keep their original name.
- Generic with IDL head: `ManagedReference<Head<args>POD* >` — POD on the generic head, plus POD on inner IDL args.
- Generic with non-IDL head: `Reference<Head<args>* >` — no POD on head, but inner IDL args still get POD.
- `@dereferenced` IDL-managed (non-generic): `ManagedReference<X >` (no POD, no `*`).
- `@dereferenced` non-IDL: bare `X` (no POD).

POD optional: `Optional<...>` with `>>` immediately concatenated (no extra space before the outer `>`).

### `static final` constants
Per-type emit form (in-class declaration vs out-of-class definition):

| Type | In-class | Out-of-class def |
|---|---|---|
| `int` | `static const int X = N;` (inline) | (none) |
| `short` | `static const short X = N;` (inline) | (none) |
| `byte` | `static const byte X;` | `const byte Class::X = N;` |
| `long` | `static const long long X;` | `const long long Class::X = N;` |
| `bool` | `static const bool X;` | `const bool Class::X = N;` |
| `float` | `static const float X;` | `const float Class::X = N;` |
| `unsigned int` | `unsigned static const int X;` *(reordered)* | `unsigned const int Class::X = N;` *(reordered)* |
| `unsigned long` | `unsigned static const long long X;` *(reordered)* | `unsigned const long long Class::X = N;` *(reordered)* |
| `unsigned short` | `unsigned static const short X;` *(reordered)* | `unsigned const short Class::X = N;` *(reordered)* |

Two patterns at play:

1. **In-class vs out-of-class init**: `int` and `short` get inline initializers in the class declaration. Every other primitive is declared without an initializer and defined out-of-class.
2. **Unsigned reorder**: any `unsigned X` constant emits `unsigned` *before* `static const`, splitting the unsigned token from the type. This is a JAR quirk we replicate.

Verified across:
- `int` — ChatRoom (corpus)
- `byte` — PersistentMessage (corpus)
- `unsigned int` — ChatManager (corpus)
- `short`, `unsigned short`, `unsigned long`, `long`, `bool`, `float`, hex literals (`0xFF`, `0xFFFFFFFF`), negative literals (`-1`, `-1.5`) — Constants probe.

Hex and negative literals pass through verbatim — no special handling needed.

---

## Method body rewrites

The JAR captures method bodies as raw text and applies these rewrites before emit.

### String literals are NOT rewritten
The rewriter walks character-by-character and copies `"..."` regions verbatim (with `\` escape support). Rewrites apply only to code segments outside string literals.
- *Verified by:* Body probe.
- *This is a real bug we caught*: without this, any IDL with a logging string containing `null`, `this`, an imported class name, or a class field name would have the literal corrupted.

### `null` → `NULL`
Word-boundary rewrite (`\bnull\b`).

### `this` → `_this.getReferenceUnsafeStaticCast()`
Word-boundary. The IDL `this` refers to the stub; on the impl side the back-reference goes through the WeakReference `_this`.
- *Verified by:* ZoneClientSession (corpus).

### Class-typed field/param: `name.X → name->X`
Applies to non-`@dereferenced` class fields and class-typed params.

### `@dereferenced` field rewrite
- `field.X → (&field)->X`
- bare `field → (&field)`

### Imported class static call: `Name.X → Name::X`
Applies to imported class names AND `include`d class names (e.g. `System.getTime()` → `System::getTime()`).
- *Verified by:* PersistentMessage, TreeEntry.

### `synchronized (X) { ... }` block
Rewrites to:

```cpp
\t// <idlPath>():  <closer-leading-ws>}
{
\tLocker _locker(<rewritten X>);
... body lines ...
}
```

The closing `}` is dropped (its source comment is glommed onto the opener's pre-comment). The Locker arg runs through the body rewriter:
- `@dereferenced` field arg → `(&field)`.
- Plain class-typed arg → bare `field`.
- *Verified by:* ChatRoom (`@dereferenced` case), Body probe (plain case).

### `if/else` without braces
Bizarre output shape: a duplicate "preview" comment for the LAST sub-body line is emitted before the `if`, the if-line and next-line's comment are merged on one physical line with a tab separator, the body line emits without a comment, then a blank line, then `else `+space+tab+comment-for-else's-body, then else's body alone.
- *Verified by:* TreeEntry (corpus) + Locking probe.
- *Looks like a bug?* Yes — the comment-glomming is bizarre.

### C-style comments stripped from bodies
Both `// line` and `/* block */` comments are stripped before line-by-line emit. Inside string literals, untouched.

---

## RPC enum

### Trailing comma rule
The enum gets a trailing comma when the **last IDL method is excluded from the enum** (i.e. `@local` or non-public visibility). Verified across corpus + Locking probe.

Previously thought to be `IsAbstract && Body == nil` — the corpus didn't disambiguate because all production IDLs satisfy both rules simultaneously. The Locking probe (zero abstract methods, last method `@local`) was the disambiguator.

### Empty enum omitted
When zero methods participate, the JAR omits the `enum {};` line entirely.
- *Verified by:* Fields probe.

### RPC seed
The first method's RPC enum value is per-class and not derivable from any obvious formula. Subsequent methods auto-number. We extract seeds from JAR autogen by hand and store them in `legacyRPCSeeds` (`internal/sema/rpc.go`).

The JAR's seed formula remains unsolved; if you add a new IDL or probe whose first non-`@local` public method needs a seed, run the JAR and copy the value.

---

## Header layout

### Order of emission
1. Forward-decl IDL imports (in IDL declaration order, excluding the parent class).
2. `#include "gmock/gmock.h"` if `@mock`.
3. `m.Includes` (`include` directives, in IDL order).
4. Non-FD `m.Imports` including the parent (in IDL order).

### Non-managed-parent (allIncludes) mode
When the parent is registered as non-managed (`Registry.AddNonManagedParent`), step 1 is skipped — every IDL import becomes a regular `#include` interleaved with the `include` directives.

### Forward-decl block shape
```cpp
namespace pkg { namespace sub {

class Foo;

class FooPOD;       // omitted when registry marks the class no-POD

} } // namespace

using namespace pkg::sub;
```

### Source-side `#include`s
The `.cpp` re-includes any forward-declared imports so method bodies have access to full type definitions. Non-managed-parent classes already have all `#include`s in the `.h` and skip this.

---

## Build invocation

### Core3's invocation
From Core3's `MMOCoreORB/cmake/Modules/FindEngine3.cmake`:

```bash
java -XX:TieredStopAtLevel=1 -client -Xmx128M \
     -cp <idlc.jar> org.sr.idlc.compiler.Compiler \
     -outdir autogen \
     -cp <engine3/MMOEngine/src> \
     -silence -rbcpp \
     -sd <idl_source_root> \
     <idl_relative_path>
```

### JAR flags

| Flag | Purpose |
|---|---|
| `-cp <classpath>` | Where the JAR finds non-IDL utility types (`Vector.h`, `Reference.h`) and engine3's IDLs (`engine.core.ManagedObject.idl`). |
| `-outdir <name>` | Output directory, resolved relative to `-sd`. The string is also embedded in the file-header comment (`autogen/<pkg>/Foo.h`). |
| `-rbcpp` | Rebuild C++ mode (the standard generation mode). |
| `-silence` | Suppress info/warnings. |
| `-noprelocks` | Disable `@preLocked` / `@arg1preLocked` / `@arg2preLocked` assert emission. **Not yet wired in idlc-go.** |
| `-nomocks` | Disable `MockX` class generation. **Not yet wired in idlc-go.** |

---

## Hash compatibility

### CRC-32/BZIP2 of `"ClassName.fieldName"`
- Polynomial `0x04C11DB7`
- Init `0xFFFFFFFF`
- No input/output reflection
- Final XOR `0xFFFFFFFF`

Validated against 23 oracles (corpus fields, JAR-generated probes, the standard `"123456789"` CRC vector, and the empty string). The implementation is hand-rolled because Go's `hash/crc32` doesn't ship a BZIP2 variant (it uses bit-reflection; BZIP2 doesn't).

### Special hashes
- `_className` (the boilerplate root-class field) → `0x76457cca`. Hardcoded as a constant in `internal/emit/cpp/impl.go`.

---

## Parser quirks (JAR side)

### `>>` is not two close-brackets
The JAR's parser doesn't accept `>>` in nested generics — requires `> >` with a space between them. Production IDLs always use the spaced form. Our Go parser handles either.
- *Verified by:* Generics probe (initial `Vector<Vector<int>>` errored; `Vector<Vector<int> >` works).

### Method bodies are type-checked
Even for `@local` methods, the JAR resolves method calls in IDL bodies against the receiver's declared type and errors on unknown methods.
- *Verified by:* Body probe (initial `classField.notifyEvent(this)` errored — `notifyEvent` not on `ManagedObject`).

---

## How to add a quirk

When a new finding lands:
1. Add an entry under the appropriate section above.
2. State the rule precisely.
3. Note which probe / corpus IDL surfaced it (and link to the autogen file when relevant).
4. Mark `*Looks like a bug?*` if the JAR's behaviour seems unintentional.
5. Cross-link to the implementing function in `internal/emit/cpp/` or `internal/sema/`.
6. Update `CLAUDE.md`'s "Subtle quirks the emitter has to reproduce" list if the quirk affects the per-class emit flow.
