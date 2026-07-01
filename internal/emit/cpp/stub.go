package cpp

import (
	"fmt"
	"io"

	"github.com/bholten/idlc-go/internal/sema"
)

// stubMethodName returns the symbol used for the .cpp definition of a
// stub method. @nativeStub methods get a `__name` companion (the
// public-facing `name` is hand-written native code elsewhere); other
// methods just use `name`.
func stubMethodName(m sema.Method) string {
	if m.IsNativeStub {
		return "__" + m.Name
	}
	return m.Name
}

// methodVisibleOnStub reports whether a method belongs in the stub
// class's public surface. Protected/private methods stay impl-only.
// VisDefault is treated as public (the IDL grammar's package-default
// methods we've seen all show up on the stub).
func methodVisibleOnStub(m sema.Method) bool {
	return m.Visibility == "" || m.Visibility == "public"
}

// emitStubHeader writes the public-facing stub class declaration.
func emitStubHeader(w io.Writer, m *sema.Model) {
	c := m.Class
	emitDocComment(w, c.Doc)
	fmt.Fprintf(w, "class %s : public %s {\n", c.Name, c.StubBase())
	fmt.Fprintf(w, "public:\n")
	emitConstantDecls(w, c)

	if cc := customCtor(c); cc != nil {
		// IDL-declared non-default ctor: replace the public default-ctor
		// declaration with one taking the custom args (with their IDL
		// defaults). `@mock` classes additionally get a `protected:
		// Class() { }` slot so that a gmock subclass can default-
		// construct the parent — non-mock classes don't need it.
		fmt.Fprintf(w, "\t%s(%s);\n\n", c.Name, joinParamDeclsWithDefaults(cc.Params, m.Registry))

		if c.IsMock {
			fmt.Fprintf(w, "protected:\n")
			fmt.Fprintf(w, "\t%s() { }\n", c.Name)
			fmt.Fprintf(w, "public:\n")
		}
	} else if !hasNoIDLCtor(c) {
		// Classes with no IDL ctor: the JAR omits the `Class()`
		// default-ctor declaration entirely. The corresponding body is
		// also skipped in emitStubCtors, and the helper's
		// `instantiateServant()` falls back to Dummy.
		fmt.Fprintf(w, "\t%s();\n\n", c.Name)
	}

	for _, meth := range c.Methods {
		if !methodVisibleOnStub(meth) {
			continue
		}

		if meth.Name == "finalize" {
			// JAR quirk: finalize is not declared on the stub class — it
			// inherits the parent's finalize. Skip both decl and body.
			continue
		}

		emitDocComment(w, meth.Doc)
		virtualPrefix := ""

		if meth.IsVirtualStub || meth.IsMock {
			// `@virtualStub` and `@mock` both add `virtual ` to the
			// stub-class method declaration — `@mock` so the gmock
			// subclass can override, `@virtualStub` for explicit JAR
			// virtualisation.
			virtualPrefix = "virtual "
		}

		fmt.Fprintf(w, "\t%s%s %s(%s)%s;\n\n",
			virtualPrefix,
			returnDeclForMethod(meth, m.Registry), meth.Name, joinParamDeclsWithDefaults(meth.Params, m.Registry), constSuffix(meth))
	}

	fmt.Fprintf(w, "\tDistributedObjectServant* _getImplementation();\n")
	fmt.Fprintf(w, "\tDistributedObjectServant* _getImplementationForRead() const;\n\n")
	fmt.Fprintf(w, "\tvoid _setImplementation(DistributedObjectServant* servant);\n\n")
	fmt.Fprintf(w, "protected:\n")
	fmt.Fprintf(w, "\t%s(DummyConstructorParameter* param);\n\n", c.Name)
	fmt.Fprintf(w, "\tvirtual ~%s();\n\n", c.Name)

	// `@nativeStub` companion declarations: `void __name(...)`.
	// Only public ones — protected/private @nativeStub methods stay
	// impl-only.
	for _, meth := range c.Methods {
		if !meth.IsNativeStub || !methodVisibleOnStub(meth) {
			continue
		}

		fmt.Fprintf(w, "\t%s __%s(%s);\n\n",
			returnDeclForMethod(meth, m.Registry), meth.Name, joinParamDeclsWithDefaults(meth.Params, m.Registry))
	}

	fmt.Fprintf(w, "\tfriend class %s;\n", c.Helper)
	fmt.Fprintf(w, "};\n\n")
}

// emitStubSource writes the .cpp definitions for the stub class.
func emitStubSource(w io.Writer, m *sema.Model) {
	c := m.Class
	fmt.Fprintf(w, "/*\n")
	fmt.Fprintf(w, " *\t%sStub\n", c.Name)
	fmt.Fprintf(w, " */\n\n")

	emitConstantDefs(w, c.Name, c)
	emitRPCEnum(w, c)

	emitStubCtors(w, m)

	for _, meth := range c.Methods {
		if !methodVisibleOnStub(meth) {
			continue
		}

		if meth.Name == "finalize" {
			continue
		}
		emitStubMethod(w, m, meth)
	}

	fmt.Fprintf(w, "DistributedObjectServant* %s::_getImplementation() {\n\n", c.Name)
	fmt.Fprintf(w, "\t if (!_updated) _updated = true;\n")
	fmt.Fprintf(w, "\treturn _impl;\n")
	fmt.Fprintf(w, "}\n\n")

	fmt.Fprintf(w, "DistributedObjectServant* %s::_getImplementationForRead() const {\n", c.Name)
	fmt.Fprintf(w, "\treturn _impl;\n")
	fmt.Fprintf(w, "}\n\n")

	fmt.Fprintf(w, "void %s::_setImplementation(DistributedObjectServant* servant) {\n", c.Name)
	fmt.Fprintf(w, "\t_impl = servant;\n")
	fmt.Fprintf(w, "}\n\n")
}

// emitStubCtors writes the public ctor (default-args or IDL-custom),
// the protected DummyConstructorParameter ctor, and the destructor.
// Root classes get no initializer-list calls (DistributedObjectStub
// has its own ctor path); non-root classes thread `: <Base>(...)` through.
func emitStubCtors(w io.Writer, m *sema.Model) {
	c := m.Class
	cc := customCtor(c)
	if cc == nil && !hasNoIDLCtor(c) {
		// Default-args IDL ctor path. Classes with no IDL ctor at all
		// skip this body — the JAR routes construction through
		// DummyConstructorParameter (helper's instantiateServant uses
		// Dummy too).
		if c.IsRoot() {
			fmt.Fprintf(w, "%s::%s() {\n", c.Name, c.Name)
		} else {
			fmt.Fprintf(w, "%s::%s() : %s(DummyConstructorParameter::instance()) {\n", c.Name, c.Name, c.Base)
		}

		fmt.Fprintf(w, "\t%s* _implementation = new %s();\n", c.ImplName, c.ImplName)
		fmt.Fprintf(w, "\t_impl = _implementation;\n")

		// Native default ctor (`public native Class();`): the impl-side
		// ctor body is hand-written and may not call
		// `_initializeImplementation()` itself, so the stub injects it
		// explicitly. Mirrors the native-custom-ctor branch below.
		// Verified against the NativeCtor probe and Account.
		if hasNativeDefaultCtor(c) {
			fmt.Fprintf(w, "\t_implementation->_initializeImplementation();\n")
		}

		fmt.Fprintf(w, "\t_impl->_setStub(this);\n")
		fmt.Fprintf(w, "\t_setClassName(\"%s\");\n", c.Name)
		fmt.Fprintf(w, "}\n\n")
	} else if cc != nil {
		// Custom-args ctor path: build the impl with the user args. For
		// `native` ctors (no IDL body) the explicit
		// `_initializeImplementation()` call is needed because the user-
		// provided native impl ctor doesn't run it. For non-native custom
		// ctors, the impl ctor body itself calls `_initializeImplementation()`
		// at its top, so the stub side skips that line.
		fmt.Fprintf(w, "%s::%s(%s) : %s(DummyConstructorParameter::instance()) {\n",
			c.Name, c.Name, joinParamDecls(cc.Params, m.Registry), c.Base)
		fmt.Fprintf(w, "\t%s* _implementation = new %s(%s);\n",
			c.ImplName, c.ImplName, joinArgs(cc.Params))
		fmt.Fprintf(w, "\t_impl = _implementation;\n")

		if cc.Body == nil {
			fmt.Fprintf(w, "\t_implementation->_initializeImplementation();\n")
		}

		fmt.Fprintf(w, "\t_impl->_setStub(this);\n")
		fmt.Fprintf(w, "\t_setClassName(\"%s\");\n", c.Name)
		fmt.Fprintf(w, "}\n\n")
	}

	if c.IsRoot() {
		fmt.Fprintf(w, "%s::%s(DummyConstructorParameter* param) {\n", c.Name, c.Name)
	} else {
		fmt.Fprintf(w, "%s::%s(DummyConstructorParameter* param) : %s(param) {\n", c.Name, c.Name, c.Base)
	}

	fmt.Fprintf(w, "\t_setClassName(\"%s\");\n", c.Name)
	fmt.Fprintf(w, "}\n\n")

	fmt.Fprintf(w, "%s::~%s() {\n}\n\n\n\n", c.Name, c.Name)
}

// emitRPCEnum: only public, non-@local methods participate. When zero
// methods participate, the JAR omits the `enum {};` line entirely
// (Fields probe surfaced this — class with no public-non-local methods).
func emitRPCEnum(w io.Writer, c sema.Class) {
	hasAny := false

	for _, meth := range c.Methods {
		if meth.IsLocal || !methodVisibleOnStub(meth) {
			continue
		}
		hasAny = true
		break
	}

	if !hasAny {
		return
	}

	fmt.Fprintf(w, "enum {")
	first := true

	for _, meth := range c.Methods {
		if meth.IsLocal || !methodVisibleOnStub(meth) {
			continue
		}

		if !first {
			fmt.Fprintf(w, ",")
		}

		fmt.Fprintf(w, "%s", meth.RPCName)

		if meth.RPCSeed != nil {
			fmt.Fprintf(w, " = %d", *meth.RPCSeed)
		}

		first = false
	}
	// JAR quirk: the RPC enum gets a trailing comma when the IDL has
	// methods declared *after* the last enum entry — i.e. when the LAST
	// IDL method is excluded from the enum (`@local` or non-public
	// visibility). The probe `Locking.idl` was the disambiguator: 0
	// abstract methods, but its trailing two methods are `@local`, and
	// the JAR emits the trailing comma. Verified across the 13-IDL
	// corpus: the comma appears iff `c.Methods[last]` is excluded.
	if !first && lastMethodExcludedFromRPCEnum(c) {
		fmt.Fprintf(w, ",")
	}

	fmt.Fprintf(w, "};\n\n")
}

// emitStubMethod writes one stub method definition. The shape forks on
// `@local` (no RPC marshalling — just throw-or-delegate).
func emitStubMethod(w io.Writer, model *sema.Model, m sema.Method) {
	c := model.Class

	if m.IsLocal {
		emitStubMethodLocal(w, model, m)
		return
	}

	// JAR rule: @read AND @dirty methods both use _getImplementationForRead.
	// Anything else uses _getImplementation.
	getImpl := "_getImplementation()"

	if m.IsRead || m.IsDirty {
		getImpl = "_getImplementationForRead()"
	}

	fmt.Fprintf(w, "%s %s::%s(%s)%s {\n",
		returnDeclForMethod(m, model.Registry), c.Name, stubMethodName(m), joinParamDecls(m.Params, model.Registry), constSuffix(m))
	fmt.Fprintf(w, "\t%s* _implementation = static_cast<%s*>(%s);\n", c.ImplName, c.ImplName, getImpl)
	fmt.Fprintf(w, "\tif (unlikely(_implementation == NULL)) {\n")
	fmt.Fprintf(w, "\t\tif (!deployed)\n")
	fmt.Fprintf(w, "\t\t\tthrow ObjectNotDeployedException(this);\n\n")
	fmt.Fprintf(w, "\t\tDistributedMethod method(this, %s);\n", m.RPCName)

	for _, p := range m.Params {
		mangle := sema.WireMangle(p.IDLType)

		if p.Dereferenced && !sema.IsPrimitive(p.IDLType.Name) {
			// @dereferenced non-primitive params marshal via the
			// "DereferencedSerializable" wire form: pass-by-reference
			// of a known-non-null serializable rather than an object id.
			// `@dereferenced` on a primitive (e.g. `@dereferenced final
			// string s`) keeps the normal Ascii/Unicode/etc. wire —
			// surfaced by the Params probe.
			mangle = "DereferencedSerializable"
		}

		fmt.Fprintf(w, "\t\tmethod.add%sParameter(%s);\n", mangle, p.Name)
	}

	fmt.Fprintf(w, "\n")

	switch {
	case m.IsVoid():
		fmt.Fprintf(w, "\t\tmethod.executeWithVoidReturn();\n")
	case !sema.IsPrimitive(m.Return.Name):
		// Class- or generic-typed return: static_cast pattern. Cast type
		// uses CppRenderMethodType so generic-class args wrap as
		// `ManagedReference<T* >`. `final` prepends `const`.
		// `@dereferenced` returns by value — drop the trailing `*`,
		// including for generic returns (Generics probe confirmed this
		// applies to `@dereferenced Vector<X>` too, not just plain
		// classes). Even when the declared return is wrapped
		// (Reference / ManagedWeakReference), the cast targets the
		// bare class pointer; implicit conversion happens at the
		// return site.
		//
		// `@rawTemplate(value="X*")` substitutes the opaque template
		// inner verbatim — `static_cast<Head<X* >[*]>` rather than the
		// usual class render. Mirrors the return-type render in
		// returnDeclForMethod.
		var castType string

		if m.RawTemplate != "" {
			castType = m.Return.Name + "<" + m.RawTemplate + " >"
		} else {
			castType = sema.CppRenderMethodType(m.Return, model.Registry)
		}

		if m.IsFinal {
			castType = "const " + castType
		}

		if !m.IsDereferenced {
			castType += "*"
		}
		fmt.Fprintf(w, "\t\treturn static_cast<%s>(method.executeWithObjectReturn());\n", castType)
	case sema.WireIsByRef(m.Return):
		// String/Unicode primitives: allocate a temp, pass it in by
		// reference, return the temp.
		retVar := "_return_" + m.Name
		fmt.Fprintf(w, "\t\t%s %s;\n", sema.CppRender(m.Return), retVar)
		fmt.Fprintf(w, "\t\tmethod.executeWith%sReturn(%s);\n", sema.WireMangle(m.Return), retVar)
		fmt.Fprintf(w, "\t\treturn %s;\n", retVar)
	default:
		// Byval primitives: inline return.
		fmt.Fprintf(w, "\t\treturn method.executeWith%sReturn();\n", sema.WireMangle(m.Return))
	}

	fmt.Fprintf(w, "\t} else {\n")

	// JAR rule: the @preLocked `this`-lock assert is suppressed ONLY when the
	// method carries its OWN @dirty annotation. @dirty means "I don't hold a
	// lock, dirty read", so asserting this is locked would be self-contradictory.
	// Crucially this is the method-level @dirty (IsDirtyOwn), NOT the effective
	// IsDirty that also picks up a class-level `@dirty class` — in a @dirty
	// class the JAR still emits the assert for @preLocked-only methods (e.g.
	// ChatManager::createPersistentRoomByFullPath). @read/@reference/plain
	// @preLocked all KEEP the assert. (Verified against the JAR: getWeapon
	// @dirty→no assert; FrsRank::getRank @read→assert; createPersistentRoomByFullPath
	// @preLocked in a @dirty class→assert.) Arg-preLocked asserts concern a
	// *different* object (a parameter), independent of this's lock, so they
	// are always emitted.
	if m.IsPreLocked && !m.IsDirtyOwn {
		fmt.Fprintf(w, "\t\tassert(this->isLockedByCurrentThread());\n")
	}

	if m.IsArg1PreLocked && len(m.Params) >= 1 {
		fmt.Fprintf(w, "\t\tassert((%s == NULL) || %s->isLockedByCurrentThread());\n",
			m.Params[0].Name, m.Params[0].Name)
	}

	if m.IsArg2PreLocked && len(m.Params) >= 2 {
		fmt.Fprintf(w, "\t\tassert((%s == NULL) || %s->isLockedByCurrentThread());\n",
			m.Params[1].Name, m.Params[1].Name)
	}

	if m.IsVoid() {
		fmt.Fprintf(w, "\t\t_implementation->%s(%s);\n", m.Name, joinArgs(m.Params))
	} else {
		fmt.Fprintf(w, "\t\treturn _implementation->%s(%s);\n", m.Name, joinArgs(m.Params))
	}

	fmt.Fprintf(w, "\t}\n}\n\n")
}

// emitStubMethodLocal handles `@local` methods on the stub side. They
// throw ObjectNotLocalException when there's no local impl, otherwise
// just delegate. No DistributedMethod, no `if (!deployed)` guard.
func emitStubMethodLocal(w io.Writer, model *sema.Model, m sema.Method) {
	c := model.Class
	// JAR rule: @read AND @dirty methods both use _getImplementationForRead.
	// Anything else uses _getImplementation.
	getImpl := "_getImplementation()"

	if m.IsRead || m.IsDirty {
		getImpl = "_getImplementationForRead()"
	}

	fmt.Fprintf(w, "%s %s::%s(%s)%s {\n",
		returnDeclForMethod(m, model.Registry), c.Name, stubMethodName(m), joinParamDecls(m.Params, model.Registry), constSuffix(m))
	fmt.Fprintf(w, "\t%s* _implementation = static_cast<%s*>(%s);\n", c.ImplName, c.ImplName, getImpl)
	fmt.Fprintf(w, "\tif (unlikely(_implementation == NULL)) {\n")
	fmt.Fprintf(w, "\t\tthrow ObjectNotLocalException(this);\n\n")
	fmt.Fprintf(w, "\t} else {\n")

	// JAR rule: the @preLocked `this`-lock assert is suppressed ONLY when the
	// method carries its OWN @dirty annotation. @dirty means "I don't hold a
	// lock, dirty read", so asserting this is locked would be self-contradictory.
	// Crucially this is the method-level @dirty (IsDirtyOwn), NOT the effective
	// IsDirty that also picks up a class-level `@dirty class` — in a @dirty
	// class the JAR still emits the assert for @preLocked-only methods (e.g.
	// ChatManager::createPersistentRoomByFullPath). @read/@reference/plain
	// @preLocked all KEEP the assert. (Verified against the JAR: getWeapon
	// @dirty→no assert; FrsRank::getRank @read→assert; createPersistentRoomByFullPath
	// @preLocked in a @dirty class→assert.) Arg-preLocked asserts concern a
	// *different* object (a parameter), independent of this's lock, so they
	// are always emitted.
	if m.IsPreLocked && !m.IsDirtyOwn {
		fmt.Fprintf(w, "\t\tassert(this->isLockedByCurrentThread());\n")
	}

	if m.IsArg1PreLocked && len(m.Params) >= 1 {
		fmt.Fprintf(w, "\t\tassert((%s == NULL) || %s->isLockedByCurrentThread());\n",
			m.Params[0].Name, m.Params[0].Name)
	}

	if m.IsArg2PreLocked && len(m.Params) >= 2 {
		fmt.Fprintf(w, "\t\tassert((%s == NULL) || %s->isLockedByCurrentThread());\n",
			m.Params[1].Name, m.Params[1].Name)
	}

	if m.IsVoid() {
		fmt.Fprintf(w, "\t\t_implementation->%s(%s);\n", m.Name, joinArgs(m.Params))
	} else {
		fmt.Fprintf(w, "\t\treturn _implementation->%s(%s);\n", m.Name, joinArgs(m.Params))
	}

	fmt.Fprintf(w, "\t}\n}\n\n")
}
