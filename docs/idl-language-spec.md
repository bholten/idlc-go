# IDL Language Specification

A semi-formal description of the IDL language as accepted by `idlc.jar`, the engine3 IDL compiler, and as faithfully reimplemented by `idlc-go`. This document is descriptive, not prescriptive — it documents what the JAR does, not what one might wish it did.

For the catalog of specific bugs and surprising behaviours we reproduce, see `jar-quirks.md`. This document is the structured reference for the language and its semantics.

## Conventions

- *Productions* in this document use a relaxed BNF: `|` for alternation, `?` for optional, `*` for zero-or-more, `+` for one-or-more, `[...]` for character classes. Where the grammar is ambiguous in practice, prose follows the production.
- *"The JAR"* refers to `ref/idlc.jar` (md5-equivalent to the binary shipped inside Core3's engine3 submodule at `MMOCoreORB/utils/engine3/MMOEngine/lib/idlc.jar`). Newer forks in the wild emit `nullptr` and different path prefixes; this spec describes the original JAR.
- *"Faithfully reproduces"* means idlc-go emits byte-identical output for the construct under discussion, including bugs.

---

## 1. Project structure

An IDL compilation unit is a single `.idl` file describing **one** managed class. The JAR generates 5–6 cooperating C++ classes per IDL (stub, implementation, adapter, helper, POD, optional mock — see §8).

The compiler resolves `import` and `include` references by walking two source trees:

- **`-sd` (source dir)** — the project's IDL/header tree (Core3's `src/`).
- **`-cp` (classpath)** — engine3's IDL/header tree (`engine3/MMOEngine/src/`).

A class is *known* if its package-qualified name has either a matching `.idl` file (in which case it's an IDL class) or a matching `.h` file (an external header). The split affects both type rendering (§5) and the include-vs-forward-decl decision (§8).

---

## 2. Lexical grammar

### 2.1 Whitespace and comments

- **Whitespace**: spaces, tabs, newlines. Insignificant except inside string literals.
- **Line comment**: `//` to end-of-line.
- **Block comment**: `/* ... */`. Doesn't nest.
- **Doc comment**: `/** ... */`. Same as block comment lexically, but the lexer **stashes the most-recent doc comment** for the parser to attach to the next member or class declaration. See §4.

Tagged annotations inside doc comments (`@param`, `@return`, `@TODO`, including misspellings like `@paran`) are not parsed as IDL annotations — they live in comment text and are ignored.

### 2.2 Identifiers and keywords

```
ident   ::= [a-zA-Z_] [a-zA-Z0-9_]*
qname   ::= ident ('.' ident)*
```

Reserved keywords (case-sensitive): `package`, `import`, `include`, `class`, `extends`, `implements`, `public`, `protected`, `private`, `static`, `final`, `transient`, `native`, `abstract`, `synchronized`, `void`, `boolean`, `byte`, `unsigned`, `short`, `int`, `long`, `float`, `double`, `string`, `unicode`, `true`, `false`, `null`, `this`, `super`, `return`, `if`, `else`, `for`, `while`, `switch`, `case`, `break`, `continue`, `new`.

`unsigned` is parsed as a modifier on the following primitive token (see §5.1).

### 2.3 Literals

- **Integer**: `[0-9]+`, optional `0x[0-9a-fA-F]+` for hex, optional trailing `L` (which is dropped).
- **Float**: standard C-like (`1.0`, `1.0f`, `1e3`).
- **String**: `"..."` *and* `'...'`. Both delimiters work; this is faithful to the JAR. Escapes: `\\`, `\"`, `\'`, `\n`, `\t`, `\r`. Multiline string literals are not supported.
- **Boolean**: `true`, `false`.
- **Null**: `null`.

### 2.4 Annotations

```
annotation ::= '@' ident annotation_args?
annotation_args ::= '(' (annotation_pair (',' annotation_pair)*)? ')'
annotation_pair  ::= ident '=' literal
```

Where `literal` is a string, integer, float, boolean, or qname. Examples: `@local`, `@dirty`, `@rawTemplate(value = "X*")`, `@arg2preLocked`.

The `@final` token is also accepted as an annotation form of the `final` modifier — used on parameters where the modifier syntax would conflict.

The full list of recognised annotations is in §6.

### 2.5 Method bodies

Method and constructor bodies are **not lexed structurally**. The lexer scans through them looking only for matching `{` / `}`, properly handling nested braces and quoted strings, and captures the body content (without the outer braces) as opaque text.

This is intentional: IDL bodies are written in Java syntax with engine3 type semantics, and parsing them as a typed AST is overkill. The text is rewritten textually at emit time (§7).

---

## 3. Syntactic grammar

### 3.1 File-level structure

```
file ::= package_decl? import_or_include* class_decl
package_decl   ::= 'package' qname ';'
import_or_include ::= ('import' | 'include') qname ';'
```

The `package` declaration is **optional**. Two Core3 IDLs (`HelperDroidObject.idl`, `CreditObject.idl`) lack it. When absent, the namespace blocks in the C++ output are skipped entirely.

`import` and `include` are syntactically identical. The semantic difference is in C++ rendering:
- `import` is for IDL classes — the compiler tries to resolve the qname against an `.idl` file first.
- `include` is for header-only types — typedefs, utility classes, enums.

If an `import`'d qname has only a `.h` (no `.idl`), the JAR still resolves it as if `include`d. The keyword is a hint, not a constraint.

### 3.2 Class declaration

```
class_decl ::= doc_comment? annotation* 'class' ident extends? implements? '{' member* '}'
extends    ::= 'extends' qname
implements ::= 'implements' qname (',' qname)*
```

Every IDL must declare exactly one class. Multiple classes per file are not supported.

The `extends` clause is optional. When absent, the class is *root*: its C++ stub/impl/adapter/POD inherit from engine3's `DistributedObjectStub`/`Servant`/`Adapter`/`POD`. When present, the C++ classes inherit from `<Base>`/`<Base>Implementation`/`<Base>Adapter`/`<Base>POD`.

`implements` is a comma-separated list of interfaces. Each interface name is appended to the implementation class's base list (after the parent class) as additional `public` bases.

A doc comment immediately preceding the `class` keyword is captured and emitted before the C++ stub class declaration AND before the C++ POD class declaration — but NOT before the impl, adapter, or helper classes. (See `jar-quirks.md`.)

### 3.3 Members

```
member ::= field | constructor | method
```

Members are grouped by kind in our internal representation, but the IDL author can interleave them in source. The JAR preserves the source-order interleaving for some emit purposes (e.g. POD field order) but groups them by kind for others (RPC enum).

#### 3.3.1 Field

```
field ::= doc_comment? annotation* visibility? modifier* type ident ('=' default_expr)? ';'
```

- `visibility` ::= `public` | `protected` | `private`. Default is *package-private* (treated as `private` in C++).
- `modifier` ::= `transient` | `static` | `final`. Order is flexible.
- `default_expr` is allowed only on `static final` primitive fields (constants).

Examples:
```idl
@dereferenced
protected transient Time galacticTime;
public static final byte HEALTHCHANGED = 12;
@weakReference
protected CreatureObject player;
```

#### 3.3.2 Constructor

```
constructor ::= doc_comment? annotation* visibility? 'native'? ident '(' params? ')' (body | ';')
params      ::= param (',' param)*
param       ::= annotation* 'final'? type ident ('=' default_expr)?
```

The constructor name MUST match the class name; the parser uses this as a probe to disambiguate constructors from fields/methods that happen to start with the class name.

`native` constructors have a `;`-terminated declaration (no body); the C++ implementation is hand-written elsewhere.

#### 3.3.3 Method

```
method ::= doc_comment? annotation* visibility? modifier* type ident '(' params? ')' (body | ';')
modifier ::= 'native' | 'abstract' | 'static' | 'final' | 'synchronized'
```

`native` and `abstract` methods have no IDL body. `abstract` methods carrying a body are allowed (acts as the default implementation). The body is opaque text (§2.5).

### 3.4 Types

```
type     ::= primitive | class_type
primitive ::= ('unsigned' ws)? ('byte' | 'short' | 'int' | 'long')
            | 'string' | 'unicode' | 'boolean' | 'float' | 'double' | 'void'
class_type ::= ident generics?
generics ::= '<' generic_args '>'
generic_args ::= type (',' type)*
```

Generics nest; the parser tracks bracket depth to handle `Vector<Reference<X> >` (the trailing space before `>` is required by the JAR's parser, which is C++98-style and rejects `>>`).

---

## 4. Doc comments

A `/** ... */` comment immediately preceding a class, constructor, method, or field declaration is captured and emitted verbatim into the C++ output.

- **On a method or field**: emitted before the stub-class declaration and the impl-class declaration.
- **On the class itself**: emitted before the stub class and before the POD class. Not before the impl, adapter, or helper.

Multi-line content with embedded `*` characters is preserved verbatim. The captured text already includes the surrounding `/**` and `*/`. The emit prefixes the first line with one tab; subsequent lines retain their source indentation.

`@param`, `@return`, `@TODO`, etc. inside doc comments are **not** annotations — they're plain text within a comment.

---

## 5. Type system

The IDL has three distinguishable kinds of types, and each renders differently in C++.

### 5.1 Primitive types

| IDL | C++ |
|---|---|
| `void` | `void` |
| `boolean` | `bool` |
| `byte`, `unsigned byte` | `byte` |
| `short` | `short` |
| `unsigned short` | `unsigned short` |
| `int` | `int` |
| `unsigned int` | `unsigned int` |
| `long` | `long long` |
| `unsigned long` | `unsigned long long` |
| `float` | `float` |
| `double` | `double` |
| `string` | `String` |
| `unicode` | `UnicodeString` |

Primitives are stored and passed by value. There's no pointer wrapping anywhere — fields are `int x;`, params are `int x` or `const String& x` (§5.4).

`String` and `UnicodeString` are technically engine3 utility classes but are treated as primitives by every rule in this spec.

### 5.2 Class types — four buckets

Class types fall into four buckets based on where they're defined and whether they participate in the managed-object lifecycle. The bucket determines how C++ wraps the type and how the body rewriter treats body access.

| Bucket | How registered | Field rendering | Local-var rendering |
|---|---|---|---|
| **IDL-managed** | `.idl` file in `-sd` (Core3) or `-cp` (engine3) registered via `Add` | `ManagedReference<X*>` | `ManagedReference<X*>` |
| **IDL noPOD** | `.h` file in `-sd` registered via `AddNoPOD` (forward-decl with no POD companion) | `Reference<X*>` | raw `X*` |
| **Engine3 Object-derived header** | `.h` file in `-cp` (engine3) — Task, ScreenHandler, etc. (Object-derived) | `Reference<X*>` | raw `X*` |
| **Engine3 utility (value)** | hardcoded set: `String`, `UnicodeString`, `Time`, `Vector3`, `Quaternion`, `Matrix4`, `AABB`, `Coordinate`, `StringId`, `Mutex`, `ReadWriteLock`, `AtomicInteger`, `AtomicBoolean`, `AtomicLong`, `Logger` | bare `X` (by-value) | bare `X` (by-value) |

The hardcoded value-type set distinguishes the JAR's apparent treatment of engine3 by-value types from Object-derived ones. We don't have a more semantic discriminator; this set is empirically correct for the Core3 corpus.

### 5.3 Generic types

The JAR's generic-arg rendering is **head-aware**: it depends on whether the outer head is a smart-pointer wrapper or a container.

A *smart-pointer wrapper* is any type whose name ends in `Reference`: `Reference<X>`, `ManagedReference<X>`, `WeakReference<X>`, `ManagedWeakReference<X>`, `TemplateReference<X>`. By engine3 convention, these wrap a pointer.

A *container* is everything else: `Vector<X>`, `VectorMap<K, V>`, `SortedVector<X>`, `Optional<X>`, etc.

#### Inner class arg rendering

| Outer | Inner is managed | Inner is non-managed |
|---|---|---|
| Smart-pointer | `ManagedReference<X*>` (nested wrap — yes, two levels) | bare `X*` |
| Container | `ManagedReference<X*>` | bare `X*` |

#### Inner generic arg rendering

| Outer | Inner head is smart-pointer | Inner head is container |
|---|---|---|
| Smart-pointer | recurse, no `*` | recurse, append `*` |
| Container | recurse, append `*` | recurse, append `*` |

The `*` is added when wrapping a value-typed thing; smart-pointers are already pointer-like and don't need an extra `*`.

#### Field outer wrap

When a generic type appears as a *field* (not a generic arg), the OUTER wrap depends on the field's head:

- IDL-managed head: `ManagedReference<<inner>* >` (or `<inner> ` if the head is itself a smart-pointer)
- everything else: `Reference<<inner>* >` (or `<inner> ` if the head is itself a smart-pointer)

The trailing `*` is dropped when the inner already ends with `>` (a nested smart-pointer or container) — the wrapper's job is to make the value pointer-like, but smart-pointer inners already are.

#### `final` modifier on a non-managed field

`final` on a non-managed-class field renders the inner as `const X*`:
```idl
protected final CharacterBuilderMenuNode rootNode;
// → Reference<const CharacterBuilderMenuNode*> rootNode;
```

This propagates the IDL's no-reassignment-after-construction semantics to the C++ Reference wrapper.

#### Trailing-space rule

When the inner ends with `>` or `*`, a space separates it from the outer `>`:
```
Reference<X*> ✗ — JAR's parser is C++98-style
Reference<X* > ✓
```

This rule applies recursively at every level.

### 5.4 Method parameters

Parameter rendering depends on type and annotations:

| Param shape | C++ rendering |
|---|---|
| primitive (no annotations) | `Type name` |
| `final` primitive | `const Type name` |
| `String` / `UnicodeString` | `const Type& name` (final) or `Type& name` (otherwise) |
| `@dereferenced` (any type) | `Type& name` (or `const Type& name` if final) |
| Smart-pointer-headed generic (`Reference<X>`, etc.) | `Type name` (by value — the wrapper holds the pointer) |
| Other class type, other generic | `Type* name` |
| `@rawTemplate(value = "X")` | `<TypeHead><X >* name` (opaque-paste; see §6) |

### 5.5 Method return types

Return rendering depends on type and annotations:

| Return shape | C++ rendering |
|---|---|
| `void` | `void` |
| Primitive (incl. `String`/`UnicodeString`) | by-value (`int`, `String`) |
| `@reference` non-generic class | `Reference<T*>` |
| `@weakReference` non-generic managed | `ManagedWeakReference<T*>` |
| `@weakReference` non-generic non-managed | `WeakReference<T*>` |
| `@dereferenced` non-`void` | by-value (drop the trailing `*`) |
| Smart-pointer-headed generic | by-value `Type` (wrapper already pointer-like) |
| Other class type, other generic | `Type*` (pointer return) |
| `final` modifier | prepend `const ` to the rendered type |
| `@rawTemplate(value = "X")` | `<TypeHead><X >*` (opaque-paste) |

---

## 6. Annotations

There are roughly 20 functional annotations across the corpus. Frequency, semantics, and emit consequences below.

### 6.1 Locking and dispatch

| Annotation | On | Semantics |
|---|---|---|
| `@local` | method | Skip RPC stub emit. The method exists only on the impl class (called directly through the stub's `_implementation->X()`). |
| `@nativeStub` | method | Forward-decl on the stub-class header only; the C++ body is hand-written. The impl class gets a `void __name(...)` companion in the protected section. |
| `@virtualStub` | method | Adds `virtual ` to the stub-class declaration (so a subclass stub can override). |
| `@noImplementationDeclaration` | method | Suppresses the method's declaration in the impl class header (the boilerplate methods `writeJSON` / `readObject` / `writeObject` are pre-declared as virtuals in `ManagedObjectImplementation` and shouldn't be re-declared). |
| `@preLocked`, `@arg1preLocked`, `@arg2preLocked` | method | Insert a runtime assertion at the top of the impl method that the relevant object is already locked. |
| `@dirty` | method | Use `_getImplementationForRead()` instead of `_getImplementation()` in the stub. (Surprising: `@dirty` means "skip lock", not "needs write lock".) |
| `@read` | method | Same as `@dirty` — uses `_getImplementationForRead()`. Also adds `const` to the impl method signature and emits `String FooImpl::bar() const{` (no space before `{`). |
| `@dirty` on class | class | Propagates to every method in the class — equivalent to writing `@dirty` on each method. |

### 6.2 Storage and serialization

| Annotation | On | Semantics |
|---|---|---|
| `@dereferenced` | field | Store inline by value. Body access rewrites `field.X → (&field)->X` for class-typed fields; primitive `@dereferenced` (e.g. `@dereferenced string jtlZoneName`) is a no-op. |
| `@dereferenced` | param | Pass by `&` reference. `@dereferenced final T x` → `const T& x`. |
| `@dereferenced` | method | Return by value (drop the trailing `*`). |
| `@reference` | method | Wrap return as `Reference<T*>`. |
| `@weakReference` | field | Wrap as `ManagedWeakReference<T*>` (managed) or `WeakReference<T*>` (non-managed). Body access uses `field.get()->X`. |
| `@weakReference` | method | Wrap return same as the field rule. |
| `@json` | class | Emit `writeJSON(JSONSerializationType)` boilerplate that serializes each field. |
| `@rawTemplate(value = "X")` | field/param/method-return | Opaque template paste. The annotation's value (a literal string) is pasted directly between `<` and ` >` of the type head — used by `DeltaAutoVariable` and similar engine3 templates. |

### 6.3 Test plumbing

| Annotation | On | Semantics |
|---|---|---|
| `@mock` | class | Emit a `Mock<Class>` subclass with gmock `MOCK_METHODn` declarations for each virtual method. The MOCK_METHOD body is read from a fixture file because the JAR walks the entire IDL inheritance chain to compute it; we can't reproduce that without whole-corpus access. |
| `@mock` | method | Add `virtual ` to both the stub-class declaration AND the impl-class declaration so a gmock subclass can override. |

### 6.4 Lua

| Annotation | On | Semantics |
|---|---|---|
| `@lua` | (varies) | Lua-binding marker. Not currently honored by idlc-go beyond parse acceptance; not load-bearing for any C++ output. |

---

## 7. Body rewrite rules

Method bodies are captured as opaque text and rewritten line-by-line at emit time. The rewriter is a chain of regex passes — explicit in `internal/emit/cpp/impl.go:rewriteCodeSegment`.

### 7.1 Pass ordering

The order matters because some passes synthesize text that other passes would re-match. The phasing:

1. **`rewriteNull`** — `null` → `NULL`.
2. **`rewriteThis`** — bare `this` → `_this.getReferenceUnsafeStaticCast()`. The IDL's `this` refers to the stub; on the impl side, the back-reference goes through the `_this` WeakReference.
3. **`rewriteDereferenced`** — `field.X` → `(&field)->X` for `@dereferenced` fields; bare `field` → `(&field)` only for class-typed `@dereferenced` fields (primitive `@dereferenced` skips the bare-form).
4. **`rewriteWeakRefFields`** — `field.X` → `field.get()->X` for `@weakReference` fields in the current class.
5. **`rewriteChainedCallDot`** — `).X` → `)->X` everywhere in body code. After this point, no rewrite should *introduce* a `).X` we want unchanged…
6. **`rewriteSuperDot`** — emits `BaseImpl::field.getForUpdate().get()->` (the `@weakReference` parent unwrap form), which contains a `).` we don't want re-rewritten. Running after chained-call means its emission survives untouched.
7. **`rewriteClassDot`** — `name.X` → `name->X` for class-typed identifiers (current-class fields + params + locals registered via the local-var pre-scan). Skips matches preceded by `::` to avoid clobbering `BaseImpl::field.getForUpdate()` emits.
8. **`rewriteImportedClassDot`** — `Imported.X` → `Imported::X` (static-method-call form).
9. **`rewritePrimitiveTypes`** — `string → String`, `unicode → UnicodeString`, `boolean → bool`, `unsigned long → unsigned long long`. Order within: longer keys first to avoid cascading.
10. **`rewriteCStyleCast`** — `(ClassName) v` → `dynamic_cast<ClassName*>(v)` for known IDL-class targets. Smart-pointer-wrapped operands get `.get()` appended (`v.get()`).

### 7.2 Local-variable class wrap

A pre-scan over body lines detects `^<TypeName> <ident> = <expr>;` declarations where TypeName is a known class. The line is rewritten:

| Bucket | Rewrite |
|---|---|
| IDL-managed | `Type ident = ...;` → `ManagedReference<Type*> ident = ...;` |
| IDL-noPOD or engine3 Object-derived header | `Type ident = ...;` → `Type* ident = ...;` |
| Engine3 value type | no rewrite |

The local's identifier is added to `classNames` for subsequent `.X → ->X` rewrites, and to `smartPtrNames` (for `dynamic_cast` operand) if the wrap is `ManagedReference<>`.

### 7.3 `super.X` rewrites

| IDL pattern | Emit | Driver |
|---|---|---|
| `super.method(args)` | `BaseImpl::method(args)` | textual |
| `super.field.method()` plain managed | `BaseImpl::field.getForUpdate()->method()` | walks parent's classMeta to find field annotations |
| `super.field.method()` `@weakReference` | `BaseImpl::field.getForUpdate().get()->method()` | same |
| `super.field.method()` `@dereferenced` | `BaseImpl::field->method()` | same |
| `super.field.method()` plain non-managed | `BaseImpl::field->method()` | same |
| `super.field` bare (managed) | `BaseImpl::field.getForUpdate()` | same |
| `super.field` bare (non-managed) | `BaseImpl::field` | same |

The "BaseImpl" is the *immediate* parent's `*Implementation` class (`Buff` → `BuffImplementation`), regardless of where in the inheritance chain the field is actually declared.

The `getForUpdate()` insertion is the JAR's way of making the write-barrier on a `ManagedReference<>` explicit before reading the underlying pointer. `Reference<>` (non-managed) doesn't need it.

### 7.4 Synchronized blocks

```idl
synchronized (X) {
    body
}
```

Becomes:

```cpp
{ // <comment>
    Locker _locker(<X-rewritten>);
    body
}
```

The opener regex handles nested parens via greedy `.*`. The arg goes through the body rewriter; if it matches a single `@dereferenced` field name, it's wrapped as `(&field)`.

### 7.5 `if`/`else` without braces

The JAR's emit for `if (cond)` followed by a single statement (and optional `else`) is unusually shaped. See `jar-quirks.md` for the full quirk. Briefly:

- A "preview" comment is emitted before the `if`. For if-with-else, the preview is the *else's body* (the last sub-body of the construct). For if-without-else, the preview is the line *after* the if-body — which can be the closing `}` of the surrounding method (we synthesize this when our body capture excludes the closing brace).
- The `if (cond)` line and the if-body line are *glommed* — the next-line's source comment is inlined onto the if-line with a tab separator, and the body line emits its content with no leading comment.
- `if(...)` (no space before paren) is normalized to `if (...)`.

---

## 8. Generated C++ structure

Each IDL produces 5 (or 6, with `@mock`) cooperating C++ classes:

### 8.1 The five-class layout

| Class | Purpose | Inherits |
|---|---|---|
| `Foo` (stub) | Public-facing API. Forwards to `_implementation` for in-process calls; wraps RPC for remote dispatch. | `<Base>` (parent's stub, or `DistributedObjectStub` for root) |
| `FooImplementation` | Server-side implementation. Holds fields, executes method bodies. | `<Base>Implementation` (or `DistributedObjectServant` for root) |
| `FooAdapter` | RPC dispatch table. Maps RPC ids to impl-method calls. | `<Base>Adapter` |
| `FooHelper` | Singleton factory. Creates instances and registers them with the runtime. | `DistributedObjectClassHelper, Singleton<FooHelper>` |
| `FooPOD` | Persistence companion. Used by BerkeleyDB serialization. | `<Base>POD` |
| `MockFoo` (`@mock` only) | gmock subclass. | `Foo` |

### 8.2 Header layout

The `.h` file contains, in order:

1. File-header comment (`/* path generated by engine3 IDL compiler 0.70 */`).
2. Header guard (`#ifndef FOO_H_` / `#define FOO_H_`).
3. Engine3 includes (`Core.h`, `ManagedReference.h`, `ManagedWeakReference.h`, `Optional.h`).
4. `likely` / `unlikely` macro block.
5. `engine/util/json_utils.h` include.
6. **Forward-declaration blocks** for IDL-class imports that *aren't* engine3 (Core3 IDL classes that aren't the immediate parent). Block shape: `namespace pkg { namespace sub { class Foo; class FooPOD; } } using namespace pkg::sub;`. Generated only when the class doesn't transitively inherit through a non-managed parent (see `Registry.IsNonManagedParent`).
7. `gmock/gmock.h` include (`@mock` classes only).
8. `#include` directives for `include`-keyword imports.
9. `#include` directives for engine3 IDL imports (`engine.*`, `system.*`) — these always `#include` rather than forward-decl.
10. `#include` for the immediate parent's header.
11. **Namespace open** for the class's own package.
12. **Stub class declaration**.
13. **Namespace close** + `using namespace pkg::sub;`.
14. **Namespace open**.
15. **Impl, adapter, helper class declarations**.
16. **Namespace close** + `using namespace`.
17. **Namespace open**.
18. **POD class declaration**.
19. **Namespace close** + `using namespace`.
20. **`#endif` with the guard token referring to the POD form** (e.g. `#endif /*FOOPOD_H_*/` even though the opening `#ifndef` was `FOO_H_`). This is a JAR quirk.

For *root* classes (no `extends`), the impl/adapter/helper share the stub's namespace block — only two namespace blocks total (stub+impl+adapter+helper, then POD).

### 8.3 Source layout

The `.cpp` file has parallel sections, separated by C-style comment banners (`/* * FooImplementation */`).

For each section, the JAR emits in a fixed order: constants → constructors → method bodies → boilerplate (`_serializationHelperMethod`, `_getImplementation`, etc.) → POD serialization (`writeObjectMembers`/`readObjectMember`/`writeObjectCompact`).

### 8.4 RPC enum

Public methods (those not annotated `@local`) get an entry in an anonymous enum at the top of the `.cpp`:

```cpp
enum {RPC_METHODNAME__ARG1_ARG2_ = <seed>, RPC_OTHERMETHOD__ARG1_, ... };
```

The first method in source order gets an explicit *seed* value (a 32-bit number); subsequent methods auto-increment. The seed comes from a hand-extracted CSV (`internal/sema/rpc.go:legacyRPCSeeds`); the JAR's seed-derivation formula remains unsolved. New IDLs whose first non-`@local` method needs an explicit seed must be added to the table for byte-exact parity. The seeds are *not* required for runtime correctness — Core3 only runs in-process and the RPC path is dead code.

### 8.5 RPC parameter mangling

Method-id name mangling in `RPC_<METHOD>__<ARG1>_<ARG2>_`:

| IDL type | Mangle |
|---|---|
| `string` | `STRING_` |
| `unicode` | `UNICODE_` |
| `boolean` | `BOOL_` (note: not `BOOLEAN_`) |
| `byte`, `unsigned byte` | `BYTE_` |
| `short`, `unsigned short` | `SHORT_` |
| `int`, `unsigned int` | `INT_` (drops `unsigned`) |
| `long`, `unsigned long` | `LONG_` (drops `unsigned`) |
| `float` | `FLOAT_` |
| `double` | `DOUBLE_` |
| any class type | `<CLASSNAME>_` (uppercase) |

### 8.6 Wire-format mangling

`DistributedMethod` uses similar but distinct mangling for its `addXxxParameter` / `executeWithXxxReturn` calls. The same translation table as RPC mangle, with two exceptions:

- The adapter's `resp->insertXxx(_m_res)` for unsigned-width returns drops the `unsigned ` prefix (`unsigned int` → `Int`, `unsigned long` → `Long`, `unsigned short` → `Short`); `add` and `get` keep `Unsigned` (`addUnsignedIntParameter`, `getUnsignedIntParameter`). This is inconsistent within the JAR — mirrored via `WireInsertMangle`.
- `String`/`UnicodeString` are by-reference (the get-pattern uses a reference parameter); other primitives are by-value.

---

## 9. Hash compatibility

For each managed-object field, the JAR emits a 32-bit name hash into the persistence switch and the on-the-wire format:

```cpp
case 0xbd8f57ac: //ChatMessage.message
    TypeInfo<String>::parseFromBinaryStream(&message, stream);
```

The hashes are baked into existing BerkeleyDB databases. Reproducing them byte-for-byte is the **only hard correctness bar** for this project.

The hash is **CRC-32/BZIP2** over the ASCII bytes of `ClassName.fieldName`:

- polynomial `0x04C11DB7`
- init `0xFFFFFFFF`
- no input/output reflection
- final XOR `0xFFFFFFFF`

Go's `hash/crc32` does not ship a preconfigured BZIP2 variant. A hand-rolled table-based implementation lives in `internal/hash`, validated against ~23 oracles (corpus-derived, JAR-generated probe, standard CRC vector, empty string).

The root class adds a special `_className` slot at hash `0x76457cca` (`nameHash("_className")`) — emitted in the writeObjectMembers/readObjectMember switches and `writeJSON`.

---

## 10. Quirks and bugs we faithfully replicate

The full catalog is in `jar-quirks.md`. The most surprising or load-bearing ones:

### Outright invalid C++ that the JAR emits anyway

- **`(&derefField) = NULL;`** — `@dereferenced` managed-class field assigned to null. The JAR's textual rewrite applies the `(&field)` form even on the LHS of an assignment, producing invalid C++. Documented in the `DerefManaged` probe; we faithfully reproduce.
- **`(&candidates)->get(currentoid)`** ambiguous overload — VectorMap has multiple `get(K)` overloads and the IDL passes a value that could match more than one. The JAR emits the same broken line.
- **`unsigned long currentoid = ...;`** — used to be invalid because `unsigned long` doesn't get translated to `unsigned long long`. We *do* translate this (matching the JAR via `bodyTypeRewrites`) — so this one is fixed.

### Surprising-but-intentional rules

- **`@dirty` and `@read` BOTH use `_getImplementationForRead()`**. `@dirty` reads "skip lock", not "needs write lock".
- **`#endif /*FOOPOD_H_*/`** even though the opening `#ifndef FOO_H_` uses the class name without `POD`. Header-guard token mismatch by design.
- **Class doc comments emit twice** — before stub and before POD, but not before impl/adapter/helper.
- **The stub adapter ctor uses `: BaseAdapter(static_cast<DistributedObjectStub*>(obj))`** for *root* classes only.
- **`engine3 IDL compiler 0.70`** version string in the file header is hardcoded.
- **RPC enum mangle**: `boolean → BOOL`, `unsigned int → INT`, `unsigned long → LONG`. The unsigned prefix is dropped in the enum but kept in `addUnsignedIntParameter`. This is a JAR-internal inconsistency.
- **Adapter `insertInt` for unsigned int return, but `addUnsignedIntParameter` for unsigned int param** — same family.

### Whitespace quirks (don't change without reason)

- Smart-pointer wrappers always have a trailing space: `Reference<T* >`, never `Reference<T*>`.
- `_serializationHelperMethod` ends with a stray blank line before `}`.
- `_getImplementation()` has a leading blank line and a single space before `if (!_updated)`.
- `if(` / `switch(` no space after the keyword in some branches; if-without-braces normalizes it to `if (`.
- `String FooImpl::bar() const{` — no space between `const` and `{` for `@read` methods on the impl side. (Stub side keeps the space.)
- Adapter case body's by-ref param decls have **three tabs plus a leading space**: `\t\t\t String msg; inv->getAsciiParameter(msg);`.
- `writeObjectMembers` wraps its per-field block in `if (field) { ... }` at single-tab column for the POD class.
- Body source-comment whitespace: `\t// <idlPath>():  <idl-leading-ws><body>` where space-runs collapse to a single space but tabs are preserved.

---

## 11. Validation and tests

idlc-go validates against three kinds of tests:

| Test | What it checks | Source of truth |
|---|---|---|
| **Hash oracles** (`internal/hash/crc_bzip2_test.go`) | Known field-name hashes match | Existing autogen tree + JAR-generated probes |
| **Goldens** (`internal/golden/golden_test.go`) | Byte-identical to JAR for 13 corpus IDLs | `ref/idlc.jar` over `testdata/idl/*.idl` |
| **Probes** (`internal/probe/probe_test.go`) | Byte-identical to JAR for synthetic IDLs targeting specific rules | `ref/idlc.jar` over `testdata/probe/src/probe/*.idl` |

The probes are the most useful single tool: each probe targets one rule (or one rule family) and surfaces gaps the natural corpus doesn't exercise. New rules should ship with a probe.

A planned **semantic** validator would assert behavioural parity (hash equivalence, RPC enum stability, field order, every non-`@local` method gets a stub, `@nativeStub` methods get no body, `@json` classes emit `writeJSON`) without requiring byte-equality. Once wired, the goldens become optional and the JAR can be retired from CI.

---

## 12. Open questions

Things we don't fully understand and have empirical-only answers for:

- **The JAR's RPC seed formula.** Each IDL's first non-`@local` method gets a 32-bit seed. We extracted them by hand into a CSV (`legacyRPCSeeds` in `internal/sema/rpc.go`). The seeds aren't required at runtime, but byte-equality with the JAR's autogen requires reproducing them. No formula has been derived.
- **How the JAR populates `@mock`'s `MOCK_METHODn` body.** It walks the entire IDL inheritance chain to compute the inherited-virtual list. We can't reproduce this without whole-corpus access; tests inject the expected body via fixture files.
- **Whether the engine3-value-type set is complete.** §5.2's table is empirical — derived from the Core3 corpus. A custom fork using a different by-value engine3 type would need the set extended.
- **Whether the JAR has any annotation-handling rules we haven't discovered.** Our 20-or-so annotation list (§6) covers the corpus, but the JAR may accept undocumented annotations. Seems unlikely but unverified.
- **The relationship between `@dirty` class-level and method-level.** The class-level `@dirty` propagates to every method, but interactions with explicit method-level `@read` / `@dirty` aren't fully characterized.

---

## Appendix: sources of truth

| Question | Source |
|---|---|
| What does the JAR do for X? | `ref/idlc.jar`, run over a probe IDL |
| What's the actual rule? | Cross-reference probe goldens with JAR output |
| Is X a managed class? | `Registry.classifies("X")` |
| Is X an engine3 value type? | `IsEngine3ValueType("X")` (hardcoded set in `internal/sema/registry.go`) |
| What does annotation A mean? | §6 of this document; `internal/sema/resolve.go` `lowerField` / `lowerMethod` |
| What's the body rewrite for construct C? | §7; `rewriteCodeSegment` in `internal/emit/cpp/impl.go` |
| What does the JAR's actual emit look like for case Y? | `bash scripts/gen-probe-goldens.sh` after dropping a probe IDL |

This document and the code agree by construction: every rule documented here has a corresponding test in the goldens or probes. When they disagree, the probes are correct (the JAR is the spec) and the document needs updating.
