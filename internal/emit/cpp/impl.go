package cpp

import (
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/bholten/tools/idlc-go/internal/hash"
	"github.com/bholten/tools/idlc-go/internal/parser"
	"github.com/bholten/tools/idlc-go/internal/sema"
)

// classNameFieldHashInput is the magic string the JAR feeds into the
// CRC-32/BZIP2 hasher for the boilerplate `_className` slot. The result
// (0x76457cca, observed in the autogen) is used in serialization paths
// for the root class only.
const classNameFieldHashInput = "_className"

// classNameFieldHash returns the JAR's hash for the `_className` field.
// Computed at runtime so we can sanity-check the constant if anyone
// changes the hash function.
var classNameFieldHash = hash.NameHash(classNameFieldHashInput)

// emitImplHeader writes the FooImplementation class declaration.
func emitImplHeader(w io.Writer, m *sema.Model) {
	c := m.Class
	bases := "public " + c.ImplBase()

	for _, iface := range c.Implements {
		bases += ", public " + iface
	}

	fmt.Fprintf(w, "class %s : %s {\n", c.ImplName, bases)

	if hasNoFields(c) {
		// No IDL fields → JAR emits a blank line in lieu of the field
		// block before the `public:` label.
		fmt.Fprintln(w)
	} else {
		emitImplFields(w, m)
	}

	fmt.Fprintf(w, "public:\n")
	emitConstantDecls(w, c)

	if cc := customCtor(c); cc != nil {
		// IDL-declared non-default ctor: emit `Impl(args)` instead of
		// the parameterless default ctor decl, carrying the IDL
		// defaults. The default-ctor body is no longer emitted in
		// source either — see emitImplSource.
		fmt.Fprintf(w, "\t%s(%s);\n\n", c.ImplName, joinParamDeclsWithDefaults(cc.Params, m.Registry))
		fmt.Fprintf(w, "\t%s(DummyConstructorParameter* param);\n\n", c.ImplName)
	} else if c.IsRoot() || hasNoIDLCtor(c) {
		// Root classes and IDL-ctorless classes both stack the two
		// ctors (no blank line between them).
		fmt.Fprintf(w, "\t%s();\n", c.ImplName)
		fmt.Fprintf(w, "\t%s(DummyConstructorParameter* param);\n\n", c.ImplName)
	} else {
		fmt.Fprintf(w, "\t%s();\n\n", c.ImplName)
		fmt.Fprintf(w, "\t%s(DummyConstructorParameter* param);\n\n", c.ImplName)
	}

	lastMethodVis := emitImplMethodDecls(w, m)

	if lastMethodVis != parser.Public {
		// Last method was protected/private — switch back to public for
		// the impl-internal block (`_this`, operator cast, `_getStub`,
		// `readObject` / `writeObject`). Surfaced by the Dispatch probe
		// (IDL ending with a `private native void privateMethod();`).
		fmt.Fprintf(w, "public:\n")
	}

	fmt.Fprintf(w, "\tWeakReference<%s*> _this;\n\n", c.Name)
	fmt.Fprintf(w, "\toperator const %s*();\n\n", c.Name)
	fmt.Fprintf(w, "\tDistributedObjectStub* _getStub();\n")
	fmt.Fprintf(w, "\tvirtual void readObject(ObjectInputStream* stream);\n")
	fmt.Fprintf(w, "\tvirtual void writeObject(ObjectOutputStream* stream);\n")

	if c.HasJSON {
		fmt.Fprintf(w, "\tvirtual void writeJSON(nlohmann::json& j);\n")
	}

	fmt.Fprintf(w, "protected:\n")
	fmt.Fprintf(w, "\tvirtual ~%s();\n\n", c.ImplName)

	if !hasIDLFinalize(c) {
		fmt.Fprintf(w, "\tvoid finalize();\n\n")
	}

	fmt.Fprintf(w, "\tvoid _initializeImplementation();\n\n")
	fmt.Fprintf(w, "\tvoid _setStub(DistributedObjectStub* stub);\n\n")

	// Lock/unlock delegate-to-stub primitives are emitted only on
	// non-root classes — the root class's lock methods ARE the primary
	// public methods, not delegates.
	if !c.IsRoot() {
		// `lock(X* obj)` and `wlock(X* obj)` always use `ManagedObject`
		// as the parameter type — the JAR hardcodes the root managed
		// type rather than the direct parent. (Verified against
		// ChatRoom, PersistentMessage, Zone, where Zone's direct parent
		// is SceneObject yet the lock signature still says ManagedObject*.)
		// Skip boilerplate decls for any name the IDL re-declared
		// itself — the user's signature wins (e.g. ZoneServer's
		// `lock(boolean)` shadows our boilerplate `lock(bool)` AND
		// `lock(ManagedObject*)`, both must be omitted to avoid
		// "class member cannot be redeclared".
		idlNames := map[string]bool{}
		for _, mm := range c.Methods {
			idlNames[mm.Name] = true
		}
		if !idlNames["lock"] {
			fmt.Fprintf(w, "\tvoid lock(bool doLock = true);\n\n")
			fmt.Fprintf(w, "\tvoid lock(ManagedObject* obj);\n\n")
		}
		if !idlNames["rlock"] {
			fmt.Fprintf(w, "\tvoid rlock(bool doLock = true);\n\n")
		}
		if !idlNames["wlock"] {
			fmt.Fprintf(w, "\tvoid wlock(bool doLock = true);\n\n")
			fmt.Fprintf(w, "\tvoid wlock(ManagedObject* obj);\n\n")
		}
		if !idlNames["unlock"] {
			fmt.Fprintf(w, "\tvoid unlock(bool doLock = true);\n\n")
		}
		if !idlNames["runlock"] {
			fmt.Fprintf(w, "\tvoid runlock(bool doLock = true);\n\n")
		}
	}

	fmt.Fprintf(w, "\tvoid _serializationHelperMethod();\n")
	fmt.Fprintf(w, "\tbool readObjectMember(ObjectInputStream* stream, const uint32& nameHashCode);\n")
	fmt.Fprintf(w, "\tint writeObjectMembers(ObjectOutputStream* stream);\n\n")

	fmt.Fprintf(w, "\tfriend class %s;\n", c.Name)
	fmt.Fprintf(w, "};\n\n")
}

// emitImplMethodDecls writes the per-method declaration lines in the
// impl class header. Tracks visibility transitions for `protected`
// methods (e.g. `_setClassName`) interleaved with public ones. Returns
// the visibility of the *last emitted method decl* so the caller can
// re-emit `public:` before the trailing `_this` / `operator const X*` /
// `_getStub()` / `readObject` block when needed (Dispatch probe — IDL
// ending in a private/protected method).
func emitImplMethodDecls(w io.Writer, m *sema.Model) parser.Visibility {
	c := m.Class
	currentVis := parser.Public // we just emitted `public:` above

	for _, meth := range c.Methods {
		if meth.IsNoImplementationDeclaration {
			continue
		}

		// Visibility transitions inside the method block (e.g. a
		// `protected void _setClassName(...)` mid-class).
		methVis := meth.Visibility

		if methVis != parser.VisDefault && methVis != currentVis {
			fmt.Fprintf(w, "%s:\n", methVis)
			currentVis = methVis
		}

		emitDocComment(w, meth.Doc)

		virtualPrefix := ""

		if meth.IsAbstract || meth.IsMock || meth.IsVirtualStub {
			// `@virtualStub` adds `virtual ` to BOTH the stub-class
			// decl (already handled) and the impl-class decl —
			// surfaced by the Dispatch probe. `@mock` adds it on both
			// sides too (GroundZone). `abstract` is the third trigger.
			virtualPrefix = "virtual "
		}

		fmt.Fprintf(w, "\t%s%s %s(%s)%s;\n\n",
			virtualPrefix,
			returnDeclForMethod(meth, m.Registry), meth.Name,
			joinParamDeclsWithDefaults(meth.Params, m.Registry), constSuffix(meth))
	}

	return currentVis
}

// emitImplFields emits the field block at the top of the impl class.
// Visibility tracking: the C++ class default is `private`, so we don't
// emit a `private:` label until we've seen a non-private one. IDL
// default-visibility members map to C++ private (matching the JAR).
func emitImplFields(w io.Writer, m *sema.Model) {
	c := m.Class
	emittedAny := false
	currentLabel := "private" // C++ class default

	for _, f := range c.Fields {
		if f.IsConst {
			continue
		}

		access := "private"

		switch f.Vis {
		case parser.Public:
			access = "public"
		case parser.Protected:
			access = "protected"
		}

		if access != currentLabel || !emittedAny && access != "private" {
			// Skip the initial `private:` label — the C++ class default
			// is already private. Otherwise emit the label change.
			if access != "private" || emittedAny {
				fmt.Fprintf(w, "%s:\n", access)
			}
			currentLabel = access
		}

		emittedAny = true
		fmt.Fprintf(w, "\t%s %s;\n\n", sema.CppRenderFieldType(f, m.Registry), f.Name)
	}
}

// emitConstantDecls writes the in-class declarations for each
// `public static final <primitive> X = ...;` field. `int` and `short`
// constants get an inline initializer; other primitive types (byte,
// long, float, bool, unsigned*) are declared without one and defined
// out-of-class via emitConstantDefs. Caller is responsible for the
// surrounding `public:` label.
//
// `unsigned X` constants reproduce a JAR quirk: they emit
// `unsigned static const X NAME;` (with `unsigned` BEFORE `static`)
// rather than the standard `static const unsigned X NAME;`. Verified
// for `unsigned int` (corpus: ChatManager) and confirmed for
// `unsigned long` / `unsigned short` (Constants probe).
func emitConstantDecls(w io.Writer, c sema.Class) {
	for _, f := range c.Fields {
		if !f.IsConst {
			continue
		}

		if signed, ok := unsignedReorder(f.IDLType.Name); ok {
			fmt.Fprintf(w, "\tunsigned static const %s %s;\n\n", signed, f.Name)
			continue
		}

		if constInClass(f) {
			fmt.Fprintf(w, "\tstatic const %s %s = %s;\n\n", sema.CppRender(f.IDLType), f.Name, f.Default)
		} else {
			fmt.Fprintf(w, "\tstatic const %s %s;\n\n", sema.CppRender(f.IDLType), f.Name)
		}
	}
}

// emitConstantDefs writes out-of-class definitions for constants whose
// in-class declaration omits the initializer (everything but `int`/`short`).
// Each is followed by a blank line.
//
// `unsigned X` constants reproduce the JAR's reordered form
// `unsigned const X Class::NAME = N;` (not the standard
// `const unsigned X`).
func emitConstantDefs(w io.Writer, scope string, c sema.Class) {
	for _, f := range c.Fields {
		if !f.IsConst || constInClass(f) {
			continue
		}

		if signed, ok := unsignedReorder(f.IDLType.Name); ok {
			fmt.Fprintf(w, "unsigned const %s %s::%s = %s;\n\n", signed, scope, f.Name, f.Default)
			continue
		}

		fmt.Fprintf(w, "const %s %s::%s = %s;\n\n", sema.CppRender(f.IDLType), scope, f.Name, f.Default)
	}
}

// constInClass reports whether a constant gets an inline initializer
// in its in-class declaration. The JAR uses inline init for `int` and
// `short`; everything else (byte, long, float, bool, unsigned*) gets
// declared without an initializer and defined out-of-class.
// `unsigned short` does NOT count as `short` here — it goes through
// the unsigned-reorder path.
func constInClass(f sema.Field) bool {
	switch f.IDLType.Name {
	case "int", "short":
		return true
	}
	return false
}

// unsignedReorder returns the C++ rendering of the corresponding signed
// type and true when `name` is one of the unsigned IDL types that
// triggers the JAR's `unsigned static const X NAME;` reorder quirk for
// `static final` constants. The signed-name return is used as the inner
// type because the JAR's reorder splits `unsigned` from `int`/`long`/
// `short` and emits them around the `static const` keywords.
func unsignedReorder(name string) (string, bool) {
	switch name {
	case "unsigned int":
		return "int", true
	case "unsigned long":
		return "long long", true
	case "unsigned short":
		return "short", true
	}
	return "", false
}

// hasConstants reports whether the class declares any `static final`
// primitive constants.
func hasConstants(c sema.Class) bool {
	for _, f := range c.Fields {
		if f.IsConst {
			return true
		}
	}

	return false
}

// lastMethodExcludedFromRPCEnum reports whether the IDL's last method is
// excluded from the RPC enum (i.e. `@local` or non-public visibility).
// The JAR emits a trailing comma in the enum exactly when this is the
// case — verified across the 13-IDL corpus and the Locking probe.
//
// Earlier we used `hasAbstractMethod` (`IsAbstract && Body == nil`),
// which produced the same answer for every corpus IDL but failed the
// Locking probe (0 abstract methods, last method `@local` → trailing
// comma). The "last method excluded" rule subsumes the abstract case
// for every IDL we've observed.
func lastMethodExcludedFromRPCEnum(c sema.Class) bool {
	if len(c.Methods) == 0 {
		return false
	}

	last := c.Methods[len(c.Methods)-1]

	return last.IsLocal || !methodVisibleOnStub(last)
}

// hasNoFields reports whether the IDL class declares zero fields.
// Triggers JAR's "minimal" emit forms — surfaced by the Locking probe:
//   - POD class omits its `String _className;` field
//   - impl class header puts a blank line before `public:` (in lieu of
//     a field block)
func hasNoFields(c sema.Class) bool {
	return len(c.Fields) == 0
}

// hasNoIDLCtor reports whether the IDL class declares zero constructors
// (default-args or custom-args). When true, the JAR:
//   - omits the stub `Class()` default-ctor decl + body
//   - emits the impl default ctor BEFORE the dummy ctor, with an
//     explicit `: BaseImplementation()` initializer-list call
//   - stacks the impl class's two ctor decls with no blank line between
//   - routes `helper.instantiateServant()` through the Dummy ctor
//
// This unifies what looked like two different rules from the Locking
// (no fields, no ctors) and Fields (has fields, no ctors) probes — the
// trigger is actually "no IDL ctor", not "no fields".
func hasNoIDLCtor(c sema.Class) bool {
	return len(c.Ctors) == 0
}

// hasIDLFinalize reports whether the IDL declares a method named
// `finalize`. The JAR special-cases this name: the boilerplate
// `void finalize();` declaration is suppressed (the IDL-declared one
// takes its place), the impl source omits the empty boilerplate body
// definition and the stub method body, and the dtor body becomes
// `Class::finalize();` instead of empty.
func hasIDLFinalize(c sema.Class) bool {
	for _, m := range c.Methods {
		if m.Name == "finalize" {
			return true
		}
	}

	return false
}

// customCtor returns the first IDL constructor whose parameter list is
// non-empty, or nil. When present, the JAR replaces the default-ctor
// stub/impl path with one that takes those args (and inserts a
// `protected: Class() { }` slot in the stub class header).
func customCtor(c sema.Class) *sema.Ctor {
	for i := range c.Ctors {
		if len(c.Ctors[i].Params) > 0 {
			return &c.Ctors[i]
		}
	}

	return nil
}

// emitImplSource writes all FooImplementation .cpp definitions.
func emitImplSource(w io.Writer, m *sema.Model) {
	c := m.Class

	fmt.Fprintf(w, "/*\n")
	fmt.Fprintf(w, " *\t%s\n", c.ImplName)
	fmt.Fprintf(w, " */\n\n")

	emitConstantDefs(w, c.ImplName, c)

	// Empty-class JAR layout: emit the default ctor *before* the dummy
	// ctor, with an explicit `: BaseImplementation()` initializer-list
	// call. Non-empty classes emit the default ctor late (after method
	// bodies) and without the explicit base init — see emitImplDefaultCtor
	// at the bottom of this function.
	if hasNoIDLCtor(c) {
		emitImplDefaultCtorEmpty(w, c)
	}

	emitImplDummyCtor(w, c)
	emitImplDtor(w, c)

	if !hasIDLFinalize(c) {
		fmt.Fprintf(w, "void %s::finalize() {\n}\n\n", c.ImplName)
	}

	emitImplInitializeImplementation(w, c)
	emitImplSetStub(w, c)
	emitImplGetStub(w, c)
	emitImplOperatorCast(w, c)

	if !c.IsRoot() {
		idlNames := map[string]bool{}
		for _, mm := range c.Methods {
			idlNames[mm.Name] = true
		}
		if !idlNames["lock"] {
			emitLockDelegate(w, c, "lock", "bool doLock", "doLock")
			emitLockDelegate(w, c, "lock", "ManagedObject* obj", "obj")
		}
		if !idlNames["rlock"] {
			emitLockDelegate(w, c, "rlock", "bool doLock", "doLock")
		}
		if !idlNames["wlock"] {
			emitLockDelegate(w, c, "wlock", "bool doLock", "doLock")
			emitLockDelegate(w, c, "wlock", "ManagedObject* obj", "obj")
		}
		if !idlNames["unlock"] {
			emitLockDelegate(w, c, "unlock", "bool doLock", "doLock")
		}
		if !idlNames["runlock"] {
			emitLockDelegate(w, c, "runlock", "bool doLock", "doLock")
		}
	}

	emitImplSerializationHelper(w, c)

	emitImplReadObject(w, c)
	emitImplReadObjectMember(w, m)
	emitImplWriteObject(w, c)
	emitImplWriteObjectMembers(w, m)

	if c.HasJSON {
		emitImplWriteJSON(w, c)
	}

	if cc := customCtor(c); cc == nil {
		// Classes with no IDL ctor at all already emitted their default
		// ctor early (before the dummy ctor); skip emitting it again
		// here. Classes with a default-args IDL ctor (e.g. ChatMessage)
		// emit it here, late, with the IDL body.
		if !hasNoIDLCtor(c) {
			emitImplDefaultCtor(w, m)
		}
	} else if cc.Body == nil {
		// `native` custom ctor: body is hand-written elsewhere; the impl
		// has no autogen ctor body.
	}

	for _, meth := range c.Methods {
		emitImplMethodBody(w, m, meth)
	}

	// Non-native custom ctor: the impl ctor body is emitted after method
	// bodies (this matches the JAR's emit order). The body calls
	// `_initializeImplementation()` itself so the stub-side skips that
	// call (see emitStubCtors).
	if cc := customCtor(c); cc != nil && cc.Body != nil {
		emitImplCustomCtor(w, m, cc)
	}
}

func emitImplCustomCtor(w io.Writer, m *sema.Model, cc *sema.Ctor) {
	c := m.Class

	superArgs, restBody, hasSuper := extractSuperCall(cc.Body.Raw)
	ctx := makeBodyCtx(m, cc.Params)

	initList := ""
	if hasSuper {
		initList = " : " + c.ImplBase() + "(" + rewriteBodyLine(superArgs, ctx) + ")"
	}

	fmt.Fprintf(w, "%s::%s(%s)%s {\n",
		c.ImplName, c.ImplName, joinParamDecls(cc.Params, m.Registry), initList)
	fmt.Fprintf(w, "\t_initializeImplementation();\n")
	emitBodyWithSourceComments(w, m, restBody, ctx)
	fmt.Fprintf(w, "}\n\n")
}

func emitImplDummyCtor(w io.Writer, c sema.Class) {
	if c.IsRoot() {
		fmt.Fprintf(w, "%s::%s(DummyConstructorParameter* param) {\n", c.ImplName, c.ImplName)
	} else {
		fmt.Fprintf(w, "%s::%s(DummyConstructorParameter* param) : %sImplementation(param) {\n",
			c.ImplName, c.ImplName, c.Base)
	}

	fmt.Fprintf(w, "\t_initializeImplementation();\n")

	if c.IsRoot() {
		// Root: just one blank line after the closing brace, not three.
		fmt.Fprintf(w, "}\n\n")
	} else {
		fmt.Fprintf(w, "}\n\n\n")
	}
}

func emitImplDtor(w io.Writer, c sema.Class) {
	fmt.Fprintf(w, "%s::~%s() {\n", c.ImplName, c.ImplName)

	if hasIDLFinalize(c) {
		// JAR quirk: when IDL declares finalize, the dtor body invokes
		// it explicitly so destruction triggers the user-provided
		// native finalize implementation.
		fmt.Fprintf(w, "\t%s::finalize();\n", c.ImplName)
	}

	fmt.Fprintf(w, "}\n\n\n")
}

func emitImplInitializeImplementation(w io.Writer, c sema.Class) {
	fmt.Fprintf(w, "void %s::_initializeImplementation() {\n", c.ImplName)
	fmt.Fprintf(w, "\t_setClassHelper(%s::instance());\n\n", c.Helper)
	fmt.Fprintf(w, "\t_this = NULL;\n\n")
	fmt.Fprintf(w, "\t_serializationHelperMethod();\n")
	fmt.Fprintf(w, "}\n\n")
}

func emitImplSetStub(w io.Writer, c sema.Class) {
	fmt.Fprintf(w, "void %s::_setStub(DistributedObjectStub* stub) {\n", c.ImplName)
	fmt.Fprintf(w, "\t_this = static_cast<%s*>(stub);\n", c.Name)
	fmt.Fprintf(w, "\t%s::_setStub(stub);\n", c.ImplBase())
	fmt.Fprintf(w, "}\n\n")
}

func emitImplGetStub(w io.Writer, c sema.Class) {
	fmt.Fprintf(w, "DistributedObjectStub* %s::_getStub() {\n", c.ImplName)
	fmt.Fprintf(w, "\treturn _this.get();\n")
	fmt.Fprintf(w, "}\n\n")
}

func emitImplOperatorCast(w io.Writer, c sema.Class) {
	fmt.Fprintf(w, "%s::operator const %s*() {\n", c.ImplName, c.Name)
	fmt.Fprintf(w, "\treturn _this.get();\n")
	fmt.Fprintf(w, "}\n\n")
}

func emitImplSerializationHelper(w io.Writer, c sema.Class) {
	fmt.Fprintf(w, "void %s::_serializationHelperMethod() {\n", c.ImplName)

	if !c.IsRoot() {
		fmt.Fprintf(w, "\t%s::_serializationHelperMethod();\n\n", c.ImplBase())
	}

	fmt.Fprintf(w, "\t_setClassName(\"%s\");\n\n", c.Name)
	fmt.Fprintf(w, "}\n\n")
}

func emitLockDelegate(w io.Writer, c sema.Class, name, params, arg string) {
	fmt.Fprintf(w, "void %s::%s(%s) {\n", c.ImplName, name, params)
	fmt.Fprintf(w, "\t_this.getReferenceUnsafeStaticCast()->%s(%s);\n", name, arg)
	fmt.Fprintf(w, "}\n\n")
}

func emitImplReadObject(w io.Writer, c sema.Class) {
	fmt.Fprintf(w, "void %s::readObject(ObjectInputStream* stream) {\n", c.ImplName)
	fmt.Fprintf(w, "\tuint16 _varCount = stream->readShort();\n")
	fmt.Fprintf(w, "\tfor (int i = 0; i < _varCount; ++i) {\n")
	fmt.Fprintf(w, "\t\tuint32 _nameHashCode;\n")
	fmt.Fprintf(w, "\t\tTypeInfo<uint32>::parseFromBinaryStream(&_nameHashCode, stream);\n\n")
	fmt.Fprintf(w, "\t\tuint32 _varSize = stream->readInt();\n\n")
	fmt.Fprintf(w, "\t\tint _currentOffset = stream->getOffset();\n\n")
	fmt.Fprintf(w, "\t\tif(%s::readObjectMember(stream, _nameHashCode)) {\n", c.ImplName)
	fmt.Fprintf(w, "\t\t}\n\n")
	fmt.Fprintf(w, "\t\tstream->setOffset(_currentOffset + _varSize);\n")
	fmt.Fprintf(w, "\t}\n\n")
	fmt.Fprintf(w, "\tinitializeTransientMembers();\n")
	fmt.Fprintf(w, "}\n\n")
}

func emitImplReadObjectMember(w io.Writer, m *sema.Model) {
	c := m.Class
	fmt.Fprintf(w, "bool %s::readObjectMember(ObjectInputStream* stream, const uint32& nameHashCode) {\n", c.ImplName)

	if c.IsRoot() {
		// Root class handles the boilerplate `_className` field
		// directly. Note the JAR quirks: hash comment uses
		// "//_className " (trailing space, no `Class.` prefix), and
		// `TypeInfo<String>` has no trailing space inside `<>`.
		fmt.Fprintf(w, "\tif (nameHashCode == 0x%x) {//%s \n", classNameFieldHash, classNameFieldHashInput)
		fmt.Fprintf(w, "\t\tTypeInfo<String>::parseFromBinaryStream(&_className, stream);\n")
		fmt.Fprintf(w, "\t\treturn true;\n")
		fmt.Fprintf(w, "\t}\n\n")
	} else {
		fmt.Fprintf(w, "\tif (%s::readObjectMember(stream, nameHashCode))\n", c.ImplBase())
		fmt.Fprintf(w, "\t\treturn true;\n\n")
	}

	fmt.Fprintf(w, "\tswitch(nameHashCode) {\n")

	for _, f := range serializableFields(c) {
		fmt.Fprintf(w, "\tcase 0x%x: //%s\n", f.Hash, f.HashInput)
		fmt.Fprintf(w, "\t\tTypeInfo<%s >::parseFromBinaryStream(&%s, stream);\n", sema.CppRenderFieldType(f, m.Registry), f.Name)
		fmt.Fprintf(w, "\t\treturn true;\n\n")
	}

	fmt.Fprintf(w, "\t}\n\n")
	fmt.Fprintf(w, "\treturn false;\n")
	fmt.Fprintf(w, "}\n\n")
}

func emitImplWriteObject(w io.Writer, c sema.Class) {
	fmt.Fprintf(w, "void %s::writeObject(ObjectOutputStream* stream) {\n", c.ImplName)
	fmt.Fprintf(w, "\tint _currentOffset = stream->getOffset();\n")
	fmt.Fprintf(w, "\tstream->writeShort(0);\n")
	fmt.Fprintf(w, "\tint _varCount = %s::writeObjectMembers(stream);\n", c.ImplName)
	fmt.Fprintf(w, "\tstream->writeShort(_currentOffset, _varCount);\n")
	fmt.Fprintf(w, "}\n\n")
}

func emitImplWriteObjectMembers(w io.Writer, m *sema.Model) {
	c := m.Class
	fmt.Fprintf(w, "int %s::writeObjectMembers(ObjectOutputStream* stream) {\n", c.ImplName)

	if c.IsRoot() {
		fmt.Fprintf(w, "\tint _count = 0;\n")
	} else {
		fmt.Fprintf(w, "\tint _count = %s::writeObjectMembers(stream);\n\n", c.ImplBase())
	}

	fmt.Fprintf(w, "\tuint32 _nameHashCode;\n")
	fmt.Fprintf(w, "\tint _offset;\n")
	fmt.Fprintf(w, "\tuint32 _totalSize;\n")

	for _, f := range serializableFields(c) {
		fmt.Fprintf(w, "\t_nameHashCode = 0x%x; //%s\n", f.Hash, f.HashInput)
		fmt.Fprintf(w, "\tTypeInfo<uint32>::toBinaryStream(&_nameHashCode, stream);\n")
		fmt.Fprintf(w, "\t_offset = stream->getOffset();\n")
		fmt.Fprintf(w, "\tstream->writeInt(0);\n")
		fmt.Fprintf(w, "\tTypeInfo<%s >::toBinaryStream(&%s, stream);\n", sema.CppRenderFieldType(f, m.Registry), f.Name)
		fmt.Fprintf(w, "\t_totalSize = (uint32) (stream->getOffset() - (_offset + 4));\n")
		fmt.Fprintf(w, "\tstream->writeInt(_offset, _totalSize);\n")
		fmt.Fprintf(w, "\t_count++;\n\n")
	}

	if c.IsRoot() {
		// Root class appends a special `_className` block. Note the
		// JAR quirks: comment uses `//_className` (no leading space,
		// no period prefix), and `TypeInfo<String>` has no trailing
		// space inside `<>`. Returns `_count + 1` instead of `_count`.
		fmt.Fprintf(w, "\n\t_nameHashCode = 0x%x;//%s\n", classNameFieldHash, classNameFieldHashInput)
		fmt.Fprintf(w, "\tTypeInfo<uint32>::toBinaryStream(&_nameHashCode, stream);\n")
		fmt.Fprintf(w, "\t_offset = stream->getOffset();\n")
		fmt.Fprintf(w, "\tstream->writeInt(0);\n")
		fmt.Fprintf(w, "\tTypeInfo<String>::toBinaryStream(&_className, stream);\n")
		fmt.Fprintf(w, "\t_totalSize = (uint32) (stream->getOffset() - (_offset + 4));\n")
		fmt.Fprintf(w, "\tstream->writeInt(_offset, _totalSize);\n")
		fmt.Fprintf(w, "\treturn _count + 1;\n")
	} else {
		fmt.Fprintf(w, "\n\treturn _count;\n")
	}

	fmt.Fprintf(w, "}\n\n")
}

// emitImplDefaultCtorEmpty is the empty-class form of the impl default
// ctor: emitted *before* the dummy ctor, with an explicit
// `: BaseImplementation()` initializer-list call. The body is just
// `_initializeImplementation();`. Used only when hasNoIDLCtor(c) is true.
func emitImplDefaultCtorEmpty(w io.Writer, c sema.Class) {
	if c.IsRoot() {
		fmt.Fprintf(w, "%s::%s() {\n", c.ImplName, c.ImplName)
	} else {
		fmt.Fprintf(w, "%s::%s() : %sImplementation() {\n",
			c.ImplName, c.ImplName, c.Base)
	}

	fmt.Fprintf(w, "\t_initializeImplementation();\n")
	fmt.Fprintf(w, "}\n\n")
}

func emitImplDefaultCtor(w io.Writer, m *sema.Model) {
	c := m.Class

	if len(c.Ctors) == 0 {
		fmt.Fprintf(w, "%s::%s() {\n", c.ImplName, c.ImplName)
		fmt.Fprintf(w, "\t_initializeImplementation();\n")
		fmt.Fprintf(w, "}\n\n")
		return
	}

	ctor := c.Ctors[0]

	rawBody := ""
	if ctor.Body != nil {
		rawBody = ctor.Body.Raw
	}
	superArgs, restBody, hasSuper := extractSuperCall(rawBody)
	ctx := makeBodyCtx(m, ctor.Params)

	initList := ""
	if hasSuper {
		initList = " : " + c.ImplBase() + "(" + rewriteBodyLine(superArgs, ctx) + ")"
	}

	fmt.Fprintf(w, "%s::%s()%s {\n", c.ImplName, c.ImplName, initList)
	fmt.Fprintf(w, "\t_initializeImplementation();\n")

	if rawBody != "" {
		emitBodyWithSourceComments(w, m, restBody, ctx)
	}

	fmt.Fprintf(w, "}\n\n")
}

func emitImplMethodBody(w io.Writer, m *sema.Model, meth sema.Method) {
	c := m.Class
	if meth.Body == nil {
		// `native` / forward-decl methods — body is hand-written elsewhere.
		return
	}

	// JAR quirk: @read methods write `const{` (no space) on the impl side;
	// non-@read methods write ` {` (with space).
	suffix := " "

	if meth.IsRead {
		suffix = ""
	}

	fmt.Fprintf(w, "%s %s::%s(%s)%s%s{\n",
		returnDeclForMethod(meth, m.Registry), c.ImplName, meth.Name,
		joinParamDecls(meth.Params, m.Registry), constSuffix(meth), suffix)

	if meth.Synchronized {
		fmt.Fprintf(w, "\tLocker _locker(_this.getReferenceUnsafeStaticCast());\n")
	}

	emitBodyWithSourceComments(w, m, meth.Body.Raw, makeBodyCtx(m, meth.Params))
	fmt.Fprintf(w, "}\n\n")
}

// serializableFields returns the fields that participate in
// readObjectMember/writeObjectMembers and the POD's optional storage.
// Transient fields are skipped — except `_className` on a root class,
// which is special-cased into emit and shouldn't appear in the regular
// loop either.
func serializableFields(c sema.Class) []sema.Field {
	var out []sema.Field

	for _, f := range c.Fields {
		if f.Transient || f.IsConst {
			continue
		}

		out = append(out, f)
	}

	return out
}

// emitBodyWithSourceComments writes each non-empty body line twice:
// once as a `// <idlPath>():  <idl-indent><line>` comment, then as the
// rewritten C++ line itself indented one tab.
//
// Whitespace handling matches the JAR's pattern:
//   - ":  " (colon + 2 spaces) is the fixed prefix
//   - then the original leading whitespace, but with runs of spaces
//     collapsed to a single space (tabs preserved as-is)
//   - then the body content with leading whitespace stripped
//
// Body rewrites applied to the C++ line:
//   - `null` → `NULL` (lowercase IDL idiom → C++ macro)
//   - @dereferenced field: `field.X` → `(&field)->X`, bare `field` → `(&field)`
//   - class-typed field/param: `name.X` → `name->X`
//   - `synchronized (X) { ... }` block: opener replaced by closer-comment + `{` + Locker;
//     closer dropped entirely; matching is brace-counted within the body.
func emitBodyWithSourceComments(w io.Writer, m *sema.Model, raw string, ctx bodyCtx) {
	lines := strings.Split(stripBodyComments(raw), "\n")

	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], "\r")
	}

	syncs := findSynchronizedBlocks(lines)
	ifElses := findIfElseNoBraces(lines)
	locals := findClassLocalVars(lines, &ctx)

	for i, line := range lines {
		leadingWS, body := splitLeadingWhitespace(line)

		if body == "" {
			continue
		}

		if s, ok := syncs[i]; ok && s.role == syncOpener {
			emitSyncOpener(w, m.IDLPath, s, ctx)
			continue
		}

		if s, ok := syncs[i]; ok && s.role == syncCloser {
			// JAR quirk: the synchronized closer emits a bare `}` (no
			// leading tab, no source-comment). Its comment was already
			// emitted by the opener's three-line replacement.
			_ = s
			fmt.Fprintf(w, "}\n")
			continue
		}

		if r, ok := ifElses[i]; ok {
			emitIfElseLine(w, m, ctx, lines, i, r)
			continue
		}

		compactedWS := collapseSpaceRuns(leadingWS)
		fmt.Fprintf(w, "\t// %s():  %s%s\n", m.IDLPath, compactedWS, body)
		rewritten := rewriteBodyLine(body, ctx)
		if loc, ok := locals[i]; ok {
			rewritten = rewriteLocalClassDecl(rewritten, loc)
		}
		fmt.Fprintf(w, "\t%s\n", rewritten)
	}
}

// localClassDecl carries the per-line rewrite info for a class-typed
// local variable declaration in a method body.
type localClassDecl struct {
	typeName string // e.g. "Zone"
	ident    string // e.g. "zone"
	managed  bool   // true → wrap as ManagedReference<T* >, false → raw `T*`
}

// localVarDeclRe matches `<TypeName> <ident> = <expr>;`-style local var
// decls at the top of a body line. Generic types (`Vector<X>`) and
// pointer types (`X*`) are skipped — we only handle the plain
// `Capitalized identifier` form, which is what IDL authors actually
// write.
var localVarDeclRe = regexp.MustCompile(`^(\s*)([A-Z][A-Za-z0-9_]*)(\s+)([a-zA-Z_][a-zA-Z0-9_]*)(\s*=)`)

// findClassLocalVars scans body lines for `<TypeName> <ident> = ...;`
// declarations where TypeName is a known IDL class. For each match it:
//
//  1. Records the line index and decl info in the returned map (so
//     `emitBodyWithSourceComments` can rewrite the type to its wrapped
//     form on the C++ side).
//  2. Adds the local's identifier to ctx.classNames so subsequent
//     `<ident>.method()` references rewrite to `<ident>->method()`.
//
// Engine3 utility types (String, Time, Vector3, ...) live in the
// registry's `externalHeaders` bucket — `IsManagedShortName` /
// `IsNoPODShortName` both return false for them, so they're left alone.
func findClassLocalVars(lines []string, ctx *bodyCtx) map[int]localClassDecl {
	out := map[int]localClassDecl{}

	if ctx.registry == nil {
		return out
	}

	for i, raw := range lines {
		m := localVarDeclRe.FindStringSubmatch(raw)
		if m == nil {
			continue
		}

		typeName := m[2]
		ident := m[4]

		switch {
		case ctx.registry.IsManagedShortName(typeName):
			out[i] = localClassDecl{typeName: typeName, ident: ident, managed: true}
		case ctx.registry.IsNoPODShortName(typeName):
			out[i] = localClassDecl{typeName: typeName, ident: ident, managed: false}
		default:
			continue
		}

		ctx.classNames = append(ctx.classNames, ident)
	}

	return out
}

// rewriteLocalClassDecl applies the type-rewrite for a class-typed
// local-var declaration. Managed locals get `ManagedReference<T* >`;
// noPOD/header locals get raw `T*`. The rewrite operates on the
// already-rewritten C++ line (post-`rewriteBodyLine`), keying off the
// captured TypeName/ident pair from the pre-scan.
func rewriteLocalClassDecl(line string, d localClassDecl) string {
	key := "lv:" + d.typeName + ":" + d.ident
	re, ok := identRewriters[key]
	if !ok {
		re = regexp.MustCompile(`\b` + regexp.QuoteMeta(d.typeName) + `(\s+)` + regexp.QuoteMeta(d.ident) + `\b`)
		identRewriters[key] = re
	}

	if d.managed {
		return re.ReplaceAllString(line, "ManagedReference<"+d.typeName+"* >${1}"+d.ident)
	}
	return re.ReplaceAllString(line, d.typeName+"*${1}"+d.ident)
}


// ifElseRole tags a body line involved in an if/else-without-braces
// JAR quirk. The JAR's emit folds an `if (...)` (or `else`) line and
// the next body line into one physical line `<if> \t// <next-comment>`,
// drops the next body line's own comment, and pre-emits a duplicate
// of the LAST sub-body's comment before the if.
type ifElseRole int

const (
	ifElsePreview  ifElseRole = iota + 1 // emit a duplicate-of-last-body comment before the if
	ifElseIfLine                         // `if (...)` line — glomm with next body's comment
	ifElseBranch                         // an if/else body line — emit body only, no comment
	ifElseElseLine                       // `else` line — glomm with next body's comment, trailing space
)

type ifElseEntry struct {
	role          ifElseRole
	glommedTarget int // for *Line: which line's comment to glom on
	previewTarget int // for Preview: which line's comment to preview
}

var (
	ifNoBraceRe   = regexp.MustCompile(`^\s*if\s*\(.*\)\s*$`)
	elseOnlyRe    = regexp.MustCompile(`^\s*else\s*$`)
	ifNoSpaceRe   = regexp.MustCompile(`^if\(`)
)

// normaliseIfSpace ensures a single space between `if` and its `(` —
// the JAR's emit always uses `if (...)` even when the IDL author wrote
// `if(...)`.
func normaliseIfSpace(line string) string {
	return ifNoSpaceRe.ReplaceAllString(line, "if (")
}

// findIfElseNoBraces scans body lines for `if (...)`-without-braces
// and the optional matching `else`-without-braces, plus their single-
// line bodies. Returns a per-line role map describing the JAR's quirky
// emit shape so the body emitter can dispatch on it.
//
// Only handles the simplest shape: `if (...)` immediately followed by
// a single statement (`return X;`/`X.method();`-style), optionally
// followed by `else` + single statement. Anything more complex falls
// through to the regular per-line emit.
func findIfElseNoBraces(lines []string) map[int]ifElseEntry {
	out := map[int]ifElseEntry{}

	for i, line := range lines {
		if !ifNoBraceRe.MatchString(line) {
			continue
		}

		// next non-blank line is the if's body
		ifBody := nextNonBlank(lines, i+1)

		if ifBody < 0 {
			continue
		}

		elseLine := nextNonBlank(lines, ifBody+1)

		if elseLine >= 0 && elseOnlyRe.MatchString(lines[elseLine]) {
			elseBody := nextNonBlank(lines, elseLine+1)

			if elseBody < 0 {
				continue
			}

			out[i] = ifElseEntry{role: ifElseIfLine, glommedTarget: ifBody, previewTarget: elseBody}
			out[ifBody] = ifElseEntry{role: ifElseBranch}
			out[elseLine] = ifElseEntry{role: ifElseElseLine, glommedTarget: elseBody}
			out[elseBody] = ifElseEntry{role: ifElseBranch}
			continue
		}

		// if-only (no else): preview = the line *after* the if-body in
		// the surrounding scope, NOT the if-body itself. The JAR's body
		// capture includes the closing `}` of the enclosing method, so
		// when the if is the last statement of its method the preview
		// is the synthesized `\t}` line. We don't capture the brace, so
		// we mark the case with a sentinel (`previewTarget < 0`) and
		// emit the synthesized line in `emitIfElseLine`.
		nextLine := nextNonBlank(lines, ifBody+1)
		out[i] = ifElseEntry{role: ifElseIfLine, glommedTarget: ifBody, previewTarget: nextLine}
		out[ifBody] = ifElseEntry{role: ifElseBranch}
	}

	return out
}

func nextNonBlank(lines []string, from int) int {
	for j := from; j < len(lines); j++ {
		_, body := splitLeadingWhitespace(strings.TrimRight(lines[j], "\r"))
		if body != "" {
			return j
		}
	}

	return -1
}

// emitIfElseLine renders one of the JAR's bizarre emissions for if/else-
// without-braces. The if-line and else-line glomm `<line>\t// <next-comment>`,
// the body lines emit only their body (comment was glommed), and the
// if-line is preceded by a duplicate of the LAST sub-body's comment.
func emitIfElseLine(w io.Writer, m *sema.Model, ctx bodyCtx, lines []string, i int, r ifElseEntry) {
	leadingWS, body := splitLeadingWhitespace(lines[i])

	switch r.role {
	case ifElseIfLine:
		// Preview: comment for the last sub-body of the block (if-with-
		// else) or the line after the if-body (if-without-else). When
		// the if is the final statement of its enclosing method, the
		// next "line" is the method's closing `}`, which our body
		// capture excludes — synthesize it here as `\t}`.
		var previewWS, previewBody string
		if r.previewTarget < 0 {
			previewWS = "\t"
			previewBody = "}"
		} else {
			previewWS, previewBody = splitLeadingWhitespace(lines[r.previewTarget])
		}
		fmt.Fprintf(w, "\t// %s():  %s%s\n", m.IDLPath, collapseSpaceRuns(previewWS), previewBody)
		// If line + glommed comment for the next sub-body. JAR quirk:
		// the if and the comment are joined by a single tab on one
		// physical line; the rewritten if body still has its leading tab.
		// The JAR also normalises `if(` → `if (` here even though the
		// IDL author wrote no space.
		glomWS, glomBody := splitLeadingWhitespace(lines[r.glommedTarget])
		fmt.Fprintf(w, "\t%s\t// %s():  %s%s\n",
			normaliseIfSpace(rewriteBodyLine(body, ctx)), m.IDLPath, collapseSpaceRuns(glomWS), glomBody)
		_ = leadingWS
	case ifElseBranch:
		// Body only — its comment was glommed onto the prior if/else line.
		fmt.Fprintf(w, "\t%s\n", rewriteBodyLine(body, ctx))
	case ifElseElseLine:
		// JAR quirk: blank line before the `else`. The else is followed
		// by a literal space then a tab then the comment for its body.
		fmt.Fprintf(w, "\n")
		glomWS, glomBody := splitLeadingWhitespace(lines[r.glommedTarget])
		fmt.Fprintf(w, "\t%s \t// %s():  %s%s\n",
			rewriteBodyLine(body, ctx), m.IDLPath, collapseSpaceRuns(glomWS), glomBody)
	}
}

// Greedy `.*` so nested parens in the args work — `super("X" +
// planet.getZoneName())` and `super(creo, Long.hashCode(buffState), ...)`
// both have inner `()` that the prior `[^)]*` form choked on.
var superCallRe = regexp.MustCompile(`^super\s*\((.*)\)\s*;\s*$`)

// extractSuperCall scans the raw IDL ctor body for a leading
// `super(args);` call and removes that line. Returns the args (between
// the parens, may be empty), the body with that line stripped, and a
// bool indicating whether a super call was found at the start of the
// body.
//
// JAR behavior: when a ctor body's first statement is `super(args);`,
// the JAR converts it to a `: BaseImpl(args)` initializer-list call on
// the C++ ctor signature and drops the line from the body. Body lines
// that come BEFORE the super call (blanks, comments) are kept as-is.
//
// Limitations: only matches single-line super calls — `super(\n a, \n
// b);` would be missed. None of the corpus IDLs span lines.
func extractSuperCall(raw string) (args, rest string, found bool) {
	lines := strings.Split(raw, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") {
			// Skip comments — JAR's comment-stripping pass also runs.
			continue
		}
		if !strings.HasPrefix(trimmed, "super") {
			return "", raw, false
		}
		m := superCallRe.FindStringSubmatch(trimmed)
		if m == nil {
			return "", raw, false
		}
		lines[i] = ""
		return strings.TrimSpace(m[1]), strings.Join(lines, "\n"), true
	}
	return "", raw, false
}

// stripBodyComments removes C-style line and block comments from a
// captured method body before it gets line-emitted. The JAR drops
// these from the autogen output (presumably to avoid double-quoted
// `"...` interfering with the comment-source pass) so we mirror that.
//
// Strings are tracked so that `//` or `/*` inside a "..." literal
// doesn't trigger comment skipping. The implementation isn't a full
// C preprocessor — it just handles the constructs we've seen in the
// corpus's IDL bodies.
func stripBodyComments(raw string) string {
	var out strings.Builder

	out.Grow(len(raw))
	i := 0

	for i < len(raw) {
		c := raw[i]

		// Line comment: skip to (but keep) the trailing newline.
		if c == '/' && i+1 < len(raw) && raw[i+1] == '/' {
			for i < len(raw) && raw[i] != '\n' {
				i++
			}
			continue
		}

		// Block comment: skip everything up to and including */.
		if c == '/' && i+1 < len(raw) && raw[i+1] == '*' {
			i += 2

			for i+1 < len(raw) && !(raw[i] == '*' && raw[i+1] == '/') {
				i++
			}

			if i+1 < len(raw) {
				i += 2
			}

			continue
		}

		// String literal: pass through verbatim, honoring escapes so
		// an embedded `"` doesn't terminate the string early.
		if c == '"' {
			out.WriteByte(c)
			i++

			for i < len(raw) {
				ch := raw[i]
				out.WriteByte(ch)
				i++

				if ch == '\\' && i < len(raw) {
					out.WriteByte(raw[i])
					i++
					continue
				}

				if ch == '"' {
					break
				}
			}
			continue
		}

		out.WriteByte(c)
		i++
	}

	return out.String()
}

// bodyCtx carries the lookup tables the body rewriter needs to produce
// the C++ line for each captured IDL line.
type bodyCtx struct {
	dereferencedFields      []string // @dereferenced field names; trigger `field.X` → `(&field)->X`
	dereferencedClassFields []string // subset of dereferencedFields whose IDL type is a class (not a primitive);
	// these additionally trigger the bare-form `field` → `(&field)` rewrite. Primitive `@dereferenced`
	// fields (e.g. `string jtlZoneName`) skip the bare-form because the field is already by-value
	// and no `&`-coerce is needed.
	weakRefFields   []string       // @weakReference fields in this class — body access uses .get()-> instead of ->
	classNames      []string       // class-typed identifiers (fields + params + locals) — get .X → ->X
	importedClasses []string       // unqualified imported class names — get .X → ::X
	superImpl       string         // BaseImpl name for `super.method` → `BaseImpl::method` rewrite
	superClass      string         // unqualified parent class name; entry point for inherited-field lookups
	registry        *sema.Registry // optional; used to classify local-var class types and walk inheritance
}

// makeBodyCtx builds the rewrite context for a method (or ctor) body.
// Class-typed field names include non-@dereferenced non-primitive fields
// (which the JAR wraps in ManagedReference<T*> and accesses via `->`).
// Class-typed params are non-primitive params (rendered as `T*`).
// Imported class names are taken from the model's `import` directives;
// references like `System.getTime()` are rewritten to `System::getTime()`.
func makeBodyCtx(m *sema.Model, params []sema.Param) bodyCtx {
	c := m.Class
	ctx := bodyCtx{dereferencedFields: c.DereferencedFieldNames, registry: m.Registry}
	if !c.IsRoot() {
		ctx.superImpl = c.ImplBase()
		ctx.superClass = c.Base
	}

	for _, f := range c.Fields {
		if f.IsConst {
			continue
		}

		if f.Dereferenced {
			// Bare-form `field` → `(&field)` only fires for class-typed
			// @dereferenced fields. Primitive ones (string / int / ...)
			// are already by-value in C++ and don't need coercion.
			if !sema.IsPrimitive(f.IDLType.Name) {
				ctx.dereferencedClassFields = append(ctx.dereferencedClassFields, f.Name)
			}
			continue
		}

		if sema.IsPrimitive(f.IDLType.Name) {
			continue
		}

		if f.WeakRef {
			// `@weakReference` field. C++ rendering wraps as
			// `ManagedWeakReference<X*>` (or `WeakReference<X*>`); both
			// require `.get()->` to reach the underlying pointer for
			// member access. Tracked separately from classNames so the
			// rewrite doesn't double-fire.
			ctx.weakRefFields = append(ctx.weakRefFields, f.Name)
			continue
		}

		// Non-primitive fields render as `Reference<X*>` /
		// `ManagedReference<X*>` / `Reference<Vector<X>*>` in C++ (see
		// CppRenderFieldType) — pointer-like. Body access uses `->`, so
		// add the field's identifier to classNames for the `.X → ->X`
		// rewrite. Engine3 utility-type fields are nearly always marked
		// `@dereferenced` and thus already filtered out above.
		ctx.classNames = append(ctx.classNames, f.Name)
	}

	for _, p := range params {
		if sema.IsPrimitive(p.IDLType.Name) {
			continue
		}
		// Mirror `paramDecl`'s pointer-vs-value choice. By-reference
		// renderings (@dereferenced + String/UnicodeString) use `.X`
		// in body, so skip them. Everything else (plain class param,
		// generic param) renders as `T*` in C++ and uses `->X`.
		if p.Dereferenced {
			continue
		}
		if p.IDLType.Generics == "" {
			head := p.IDLType.Name
			if head == "string" || head == "unicode" {
				// Already filtered by IsPrimitive, but keep symmetry.
				continue
			}
			rendered := sema.CppRender(p.IDLType)
			if rendered == "String" || rendered == "UnicodeString" {
				continue
			}
		}
		ctx.classNames = append(ctx.classNames, p.Name)
	}

	addQName := func(qname string) {
		if i := strings.LastIndex(qname, "."); i >= 0 {
			ctx.importedClasses = append(ctx.importedClasses, qname[i+1:])
		} else {
			ctx.importedClasses = append(ctx.importedClasses, qname)
		}
	}

	for _, qname := range m.Imports {
		addQName(qname)
	}

	for _, qname := range m.Includes {
		// `include`-directive types like `system.lang.Math` get the
		// same `Type.X → Type::X` rewrite when used statically in a
		// body (`Math.sqrt(x)` → `Math::sqrt(x)`).
		addQName(qname)
	}

	// Add every class name the registry knows about — engine3
	// utilities like `Logger`, `System`, etc. that appear in the body
	// without an explicit `import` directive. The JAR's body rewriter
	// catches these because it has full classpath visibility; we
	// approximate with the registry's known-short-names set.
	if m.Registry != nil {
		seen := map[string]bool{}
		for _, n := range ctx.importedClasses {
			seen[n] = true
		}
		for _, n := range m.Registry.AllKnownShortNames() {
			if !seen[n] {
				ctx.importedClasses = append(ctx.importedClasses, n)
				seen[n] = true
			}
		}
	}

	return ctx
}

type syncRole int

const (
	syncOpener syncRole = iota + 1
	syncCloser
)

type syncEntry struct {
	role     syncRole
	arg      string // raw `(X)` argument text, for use in `Locker _locker(<rewritten X>);`
	closerWS string // leading whitespace of the matching `}` line, used in the opener's comment
}

// findSynchronizedBlocks scans the body lines for `synchronized (X) {`
// openers and locates each matching `}` via brace counting. Returns a
// per-line map describing how to emit each marked line.
//
// Limitations: assumes `synchronized` opener and matching closer each
// appear at the start of their own line. The IDL corpus we've seen
// satisfies this; if a single-line `synchronized (X) { foo(); }` appears
// in the wild, this will need extending.
func findSynchronizedBlocks(lines []string) map[int]syncEntry {
	out := map[int]syncEntry{}
	// Greedy `.*` so the outermost `)` (just before `{`) is the match
	// end — this handles nested parens like
	//   synchronized (super.getContainerLock()) { ... }
	// which the prior `[^)]*` form couldn't match.
	syncOpenRe := regexp.MustCompile(`^\s*synchronized\s*\((.*)\)\s*\{\s*$`)

	for i, line := range lines {
		m := syncOpenRe.FindStringSubmatch(line)

		if m == nil {
			continue
		}

		// Brace-count from the next line until depth returns to 0.
		depth := 1
		closerIdx := -1

		for j := i + 1; j < len(lines); j++ {
			for _, ch := range lines[j] {
				if ch == '{' {
					depth++
				} else if ch == '}' {
					depth--

					if depth == 0 {
						closerIdx = j
						break
					}
				}
			}

			if closerIdx >= 0 {
				break
			}
		}

		if closerIdx < 0 {
			continue
		}

		closerWS, _ := splitLeadingWhitespace(lines[closerIdx])
		out[i] = syncEntry{role: syncOpener, arg: strings.TrimSpace(m[1]), closerWS: closerWS}
		out[closerIdx] = syncEntry{role: syncCloser}
	}

	return out
}

// emitSyncOpener emits the JAR's three-line replacement for a
// `synchronized (X) {` opener:
//
//	\t// <idlPath>():  <closer-leading-ws>}    ← comment for the matching `}` line
//	{                                            ← bare `{`, no leading tab
//	\tLocker _locker(<rewritten X>);
//
// The matching `}` line is dropped on its own pass; the function's
// auto-emitted final `}` closes the new scope.
//
// The Locker argument runs through `rewriteBodyLine` so a `@dereferenced`
// field gets `(&field)` (ChatRoom's `subRoomsMutex`) and a regular
// class-typed field stays bare (`classField`) — Body probe disambiguated
// this from the previous "always wrap in `(&...)`" rule.
func emitSyncOpener(w io.Writer, idlPath string, s syncEntry, ctx bodyCtx) {
	fmt.Fprintf(w, "\t// %s():  %s}\n", idlPath, s.closerWS)
	fmt.Fprintf(w, "{\n")
	fmt.Fprintf(w, "\tLocker _locker(%s);\n", rewriteSyncArg(s.arg, ctx))
}

// rewriteSyncArg renders the argument of a `synchronized (X) { ... }`
// opener for emission inside `Locker _locker(<arg>);`. If the IDL arg is
// a single identifier matching a `@dereferenced` field, wrap it as
// `(&fieldname)` — this is the only place the JAR emits the bare-form
// `(&X)` for a dereferenced field. Otherwise pass the arg through the
// regular body rewrite chain (this covers cases like
// `synchronized (this) { ... }` → `_this.getReferenceUnsafeStaticCast()`).
func rewriteSyncArg(arg string, ctx bodyCtx) string {
	trimmed := strings.TrimSpace(arg)
	for _, f := range ctx.dereferencedFields {
		if trimmed == f {
			return "(&" + f + ")"
		}
	}
	return rewriteBodyLine(arg, ctx)
}

func splitLeadingWhitespace(s string) (leading, rest string) {
	for i := 0; i < len(s); i++ {
		if s[i] != ' ' && s[i] != '\t' {
			return s[:i], s[i:]
		}
	}

	return s, ""
}

// collapseSpaceRuns replaces every contiguous run of ' ' characters
// with a single ' '. Tabs are preserved as-is.
func collapseSpaceRuns(s string) string {
	var b strings.Builder
	prevSpace := false

	for _, ch := range s {
		if ch == ' ' {
			if !prevSpace {
				b.WriteByte(' ')
			}

			prevSpace = true
			continue
		}

		b.WriteRune(ch)
		prevSpace = false
	}

	return b.String()
}

var identRewriters = map[string]*regexp.Regexp{}

// rewriteBodyLine applies the JAR's body-line rewrites in a fixed order
// to all *code* portions of the line (text outside string literals).
// Rewrites:
//
//  1. `null` → `NULL` (lowercase IDL idiom → C++ macro)
//  2. @dereferenced field: `field.X` → `(&field)->X`, bare `field` → `(&field)`
//  3. class-typed identifier: `name.X` → `name->X` (params + non-@deref fields)
//  4. imported class: `Name.X` → `Name::X` (static-method-call form)
//  5. bare `this` → `_this.getReferenceUnsafeStaticCast()` (impl-side
//     rewrite — `this` in IDL refers to the stub, but on the impl side
//     the back-reference goes through the WeakReference `_this` member)
//
// String literals (delimited by `"..."`, with `\` escape support) are
// passed through verbatim — the JAR doesn't rewrite identifiers inside
// strings (Body probe finding). Production never had a string literal
// containing `null` / `this` / an imported class name / a class field
// name, but the bug would have hit any IDL with logging or formatted
// output.
//
// Order matters: @dereferenced rewrite runs first so `playerList.contains`
// becomes `(&playerList)->contains` BEFORE the class-typed-param pass
// rewrites `player.X` → `player->X`. Imported-class rewrite is last so
// it doesn't compete with field/param names that happen to match.
func rewriteBodyLine(line string, ctx bodyCtx) string {
	var b strings.Builder
	i := 0

	for i < len(line) {
		c := line[i]

		if c == '"' {
			b.WriteByte(c)
			i++
			for i < len(line) {
				ch := line[i]
				b.WriteByte(ch)
				i++

				if ch == '\\' && i < len(line) {
					b.WriteByte(line[i])
					i++
					continue
				}

				if ch == '"' {
					break
				}
			}
			continue
		}

		// Apply rewrites to the next stretch of non-string text.
		j := i
		for j < len(line) && line[j] != '"' {
			j++
		}

		b.WriteString(rewriteCodeSegment(line[i:j], ctx))
		i = j
	}

	return b.String()
}

// rewriteCodeSegment applies the JAR's identifier rewrites to a stretch
// of code text — i.e. text not inside a string literal. Caller is
// responsible for splitting the input around `"..."` regions.
//
// Order matters because some rewrites synthesize text that other rewrites
// would re-match. The phasing is:
//
//  1. Inert text rewrites that produce `).X` patterns we want chained-call
//     to fix up: `rewriteNull`, `rewriteThis` (`this` → `_this.getReferenceUnsafeStaticCast()`).
//  2. `rewriteDereferenced` — produces `(&field)->X` we don't want re-rewritten.
//  3. `rewriteChainedCallDot` — turns every `).X` into `)->X`. After this
//     point, no rewrite should *introduce* a `).X` we want unchanged…
//  4. …except `rewriteSuperDot`, which emits `.getForUpdate().get()->`
//     for `@weakReference` parent fields. Running it AFTER chained-call
//     means its emission survives untouched.
//  5. `rewriteClassDot`, `rewriteImportedClassDot`: `name.X → name->X`
//     and `Name.X → Name::X` for registered identifiers.
//  6. `rewritePrimitiveTypes`: `string → String` etc.
//  7. `rewriteCStyleCast`: `(Class) v → dynamic_cast<Class*>(v)`.
func rewriteCodeSegment(seg string, ctx bodyCtx) string {
	seg = rewriteNull(seg)
	seg = rewriteThis(seg)
	seg = rewriteDereferenced(seg, ctx.dereferencedFields, ctx.dereferencedClassFields)
	seg = rewriteWeakRefFields(seg, ctx.weakRefFields)
	seg = rewriteChainedCallDot(seg)
	seg = rewriteSuperDot(seg, ctx)
	seg = rewriteClassDot(seg, ctx.classNames)
	seg = rewriteImportedClassDot(seg, ctx.importedClasses)
	seg = rewritePrimitiveTypes(seg)
	seg = rewriteCStyleCast(seg, ctx.registry)

	return seg
}

// rewriteWeakRefFields rewrites `field.X` → `field.get()->X` for any
// `@weakReference` field in the current class. The `Managed`?`WeakReference<>`
// wrapper requires `.get()` to extract the underlying pointer before
// member access; plain `field->X` doesn't compile because the wrapper
// lacks `operator->`. Bare references (`return field;` or assignments
// like `field = NULL`) pass through untouched — assignment is handled
// by the wrapper's `operator=`.
func rewriteWeakRefFields(line string, fields []string) string {
	for _, f := range fields {
		key := "wr:" + f
		re, ok := identRewriters[key]
		if !ok {
			re = regexp.MustCompile(`\b` + regexp.QuoteMeta(f) + `\b\.`)
			identRewriters[key] = re
		}
		line = re.ReplaceAllString(line, f+".get()->")
	}
	return line
}

// rewriteChainedCallDot rewrites `).X` → `)->X` for chained method
// calls. IDL bodies use Java-style `.` for both pointer dereference and
// member access; on the C++ side, anything chained off a method call is
// a `T*`-returning method (managed-class methods) so the dot must be
// `->`. This is a textual rewrite; the JAR does not type-check.
//
// Example:
//
//	zone.getPlanetManager().getNearestPlanetTravelPoint(this)
//	-> zone->getPlanetManager()->getNearestPlanetTravelPoint(this)
//
// `rewriteClassDot` already turns the first `.` into `->`; this rewrite
// catches every subsequent chained `.X`.
var chainedCallDotRe = regexp.MustCompile(`\)\.([a-zA-Z_])`)

func rewriteChainedCallDot(seg string) string {
	return chainedCallDotRe.ReplaceAllString(seg, ")->$1")
}

// rewriteCStyleCast rewrites Java-style downcasts on IDL class types:
//
//	(ClassName) ident  →  dynamic_cast<ClassName*>(ident)
//
// where ClassName is a registry-known IDL class (managed or noPOD).
// Engine3 utility types and primitives are ignored — those use
// by-value semantics and don't need pointer-cast rewrites.
var cStyleCastRe = regexp.MustCompile(`\(([A-Z][A-Za-z0-9_]*)\)\s*([a-zA-Z_][a-zA-Z0-9_]*)`)

func rewriteCStyleCast(seg string, reg *sema.Registry) string {
	if reg == nil {
		return seg
	}

	return cStyleCastRe.ReplaceAllStringFunc(seg, func(match string) string {
		m := cStyleCastRe.FindStringSubmatch(match)
		if m == nil {
			return match
		}
		typeName, ident := m[1], m[2]
		if !reg.IsManagedShortName(typeName) && !reg.IsNoPODShortName(typeName) {
			return match
		}
		return "dynamic_cast<" + typeName + "*>(" + ident + ")"
	})
}

// rewritePrimitiveTypes maps IDL primitive type names used as types in
// method-body code to their C++ counterparts. The JAR translates these
// when the user writes a local variable declaration like
// `string guildKey = String.valueOf(...);` — both `string`/`unicode` need
// to become `String`/`UnicodeString` for the C++ to compile. Other
// primitives (int, long, etc.) already match C++.
func rewritePrimitiveTypes(line string) string {
	for idl, cpp := range bodyTypeRewrites {
		re, ok := identRewriters["bt:"+idl]
		if !ok {
			re = regexp.MustCompile(`\b` + idl + `\b`)
			identRewriters["bt:"+idl] = re
		}
		line = re.ReplaceAllString(line, cpp)
	}
	return line
}

var bodyTypeRewrites = map[string]string{
	"string":  "String",
	"unicode": "UnicodeString",
}

// rewriteSuperDot replaces `super.X` and `super.field.X` references
// with their C++ impl-side form. The prefix is the IMMEDIATE parent's
// `*Implementation` class (`<DirectParent>Impl::`); the unwrap form
// for `super.field.X` depends on the parent field's type AND
// annotations. We walk the inheritance chain via the registry's
// classMeta to look up both.
//
// Cases (verified against JAR emit):
//
//	super.method(args)               → BaseImpl::method(args)
//	super.weakManagedField.method()  → BaseImpl::field.getForUpdate().get()->method()  (@weakReference)
//	super.plainManagedField.method() → BaseImpl::field.getForUpdate()->method()        (plain managed → ManagedReference<X*>)
//	super.derefField.method()        → BaseImpl::field->method()                       (@dereferenced — wrapper has operator->)
//	super.plainNonMgmtField.method() → BaseImpl::field->method()                       (plain Reference<X*> — has operator->)
//
// The `getForUpdate()` insertion is the JAR's way of making the
// write-barrier on a `ManagedReference<>` explicit before reading the
// underlying pointer. `Reference<>` doesn't need it because it's a
// plain smart pointer.
//
// `LookupInheritedField` returns false when the field's declaring class
// isn't in the registry (e.g. an engine3-side field we never scanned).
// We fall back to the plain `->` form — the most common case for
// non-managed parent fields.
func rewriteSuperDot(line string, ctx bodyCtx) string {
	if ctx.superImpl == "" {
		return line
	}

	chainedRe, ok := identRewriters["super-chain"]
	if !ok {
		chainedRe = regexp.MustCompile(`\bsuper\.([A-Za-z_][A-Za-z0-9_]*)\.`)
		identRewriters["super-chain"] = chainedRe
	}
	line = chainedRe.ReplaceAllStringFunc(line, func(match string) string {
		m := chainedRe.FindStringSubmatch(match)
		if m == nil {
			return match
		}
		fieldName := m[1]
		fld, found := ctx.registry.LookupInheritedField(ctx.superClass, fieldName)
		switch {
		case found && fld.WeakRef:
			return ctx.superImpl + "::" + fieldName + ".getForUpdate().get()->"
		case found && fld.Dereferenced:
			return ctx.superImpl + "::" + fieldName + "->"
		case found && ctx.registry.IsManagedShortName(fld.TypeName):
			return ctx.superImpl + "::" + fieldName + ".getForUpdate()->"
		default:
			return ctx.superImpl + "::" + fieldName + "->"
		}
	})

	re, ok := identRewriters["super:"+ctx.superImpl]
	if !ok {
		re = regexp.MustCompile(`\bsuper\.`)
		identRewriters["super:"+ctx.superImpl] = re
	}
	return re.ReplaceAllString(line, ctx.superImpl+"::")
}

// rewriteThis replaces every word-boundary `this` with the JAR's impl-
// side back-reference form. The IDL writes `this` to mean "the stub
// object that owns this implementation"; on the impl side that has to
// go through the `_this` WeakReference to get back to the stub pointer.
func rewriteThis(line string) string {
	re, ok := identRewriters["this"]

	if !ok {
		re = regexp.MustCompile(`\bthis\b`)
		identRewriters["this"] = re
	}

	return re.ReplaceAllString(line, "_this.getReferenceUnsafeStaticCast()")
}

func rewriteNull(line string) string {
	re, ok := identRewriters["null"]

	if !ok {
		re = regexp.MustCompile(`\bnull\b`)
		identRewriters["null"] = re
	}

	return re.ReplaceAllString(line, "NULL")
}

// rewriteDereferenced applies the JAR's @dereferenced-field text rewrite:
//
//	field.X    →  (&field)->X    (always — `allFields`)
//	bare field →  (&field)        (only when field's IDL type is a
//	                               non-primitive class — `classFields`)
//
// Primitive @dereferenced fields (e.g. `@dereferenced string jtlZoneName`)
// skip the bare-form because the field is already a by-value `String` /
// `int` / etc. — no `&`-coerce is needed. Class-typed @dereferenced
// fields use the bare-form to convert the by-value field into a `T*`
// when the surrounding context wants a pointer (e.g. assigning the
// field to a `ManagedReference<T*>` field, or returning it from a
// `T*`-returning method).
func rewriteDereferenced(line string, allFields, classFields []string) string {
	classSet := map[string]bool{}
	for _, f := range classFields {
		classSet[f] = true
	}

	for _, f := range allFields {
		re, ok := identRewriters["d:"+f]

		if !ok {
			if classSet[f] {
				re = regexp.MustCompile(`\b` + regexp.QuoteMeta(f) + `\b\.?`)
			} else {
				re = regexp.MustCompile(`\b` + regexp.QuoteMeta(f) + `\b\.`)
			}
			identRewriters["d:"+f] = re
		}

		isClass := classSet[f]
		line = re.ReplaceAllStringFunc(line, func(match string) string {
			if strings.HasSuffix(match, ".") {
				return "(&" + f + ")->"
			}
			if isClass {
				return "(&" + f + ")"
			}
			return match
		})
	}
	return line
}

// rewriteClassDot replaces `name.` with `name->` for known class-typed
// identifiers. Doesn't touch bare `name` (no following `.`) — those
// already render as the right pointer-like value in C++.
func rewriteClassDot(line string, names []string) string {
	for _, n := range names {
		re, ok := identRewriters["c:"+n]

		if !ok {
			re = regexp.MustCompile(`\b` + regexp.QuoteMeta(n) + `\b\.`)
			identRewriters["c:"+n] = re
		}

		line = re.ReplaceAllString(line, n+"->")
	}

	return line
}

// rewriteImportedClassDot rewrites `Name.X` → `Name::X` where `Name`
// is one of the IDL's imported class names — `System.getTime()` →
// `System::getTime()`. Static-method-call form in C++.
func rewriteImportedClassDot(line string, names []string) string {
	for _, n := range names {
		re, ok := identRewriters["i:"+n]

		if !ok {
			re = regexp.MustCompile(`\b` + regexp.QuoteMeta(n) + `\b\.`)
			identRewriters["i:"+n] = re
		}

		line = re.ReplaceAllString(line, n+"::")
	}

	return line
}
