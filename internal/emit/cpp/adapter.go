package cpp

import (
	"fmt"
	"io"

	"github.com/bholten/idlc-go/internal/parser"
	"github.com/bholten/idlc-go/internal/sema"
)

// emitAdapterHeader writes the FooAdapter class declaration.
// `@local` methods are excluded — adapters only handle the RPC dispatch
// surface, and @local methods skip RPC entirely.
func emitAdapterHeader(w io.Writer, m *sema.Model) {
	c := m.Class

	fmt.Fprintf(w, "class %s : public %s {\n", c.Adapter, c.AdapterBase())
	fmt.Fprintf(w, "public:\n")
	fmt.Fprintf(w, "\t%s(%s* impl);\n\n", c.Adapter, c.Name)
	fmt.Fprintf(w, "\tvoid invokeMethod(sys::uint32 methid, DistributedMethod* method);\n\n")

	for _, meth := range c.Methods {
		if meth.IsLocal || !methodVisibleOnStub(meth) {
			continue
		}

		// Adapter declarations omit default values (JAR rule).
		fmt.Fprintf(w, "\t%s %s(%s)%s;\n\n",
			returnDeclForMethod(meth, m.Registry), meth.Name, joinParamDecls(meth.Params, m.Registry), constSuffix(meth))
	}

	fmt.Fprintf(w, "};\n\n")
}

// emitAdapterSource writes the FooAdapter .cpp definitions.
func emitAdapterSource(w io.Writer, m *sema.Model) {
	c := m.Class

	fmt.Fprintf(w, "/*\n")
	fmt.Fprintf(w, " *\t%s\n", c.Adapter)
	fmt.Fprintf(w, " */\n\n\n")

	fmt.Fprintf(w, "#include \"engine/orb/messages/InvokeMethodMessage.h\"\n\n\n")

	// Root classes need an explicit static_cast to DistributedObjectStub*
	// because the implicit conversion path doesn't apply when the stub
	// IS DistributedObjectStub. Non-root classes use the parent adapter ctor.
	if c.IsRoot() {
		fmt.Fprintf(w, "%s::%s(%s* obj) : %s(static_cast<DistributedObjectStub*>(obj)) {\n",
			c.Adapter, c.Adapter, c.Name, c.AdapterBase())
	} else {
		fmt.Fprintf(w, "%s::%s(%s* obj) : %s(obj) {\n",
			c.Adapter, c.Adapter, c.Name, c.AdapterBase())
	}

	fmt.Fprintf(w, "}\n\n")

	emitAdapterInvokeMethod(w, m)

	for _, meth := range c.Methods {
		if meth.IsLocal || !methodVisibleOnStub(meth) {
			continue
		}

		emitAdapterDelegate(w, m, meth)
	}
}

func emitAdapterInvokeMethod(w io.Writer, m *sema.Model) {
	c := m.Class
	fmt.Fprintf(w, "void %s::invokeMethod(uint32 methid, DistributedMethod* inv) {\n", c.Adapter)
	fmt.Fprintf(w, "\tDOBMessage* resp = inv->getInvocationMessage();\n\n")
	fmt.Fprintf(w, "\tswitch (methid) {\n")

	for _, meth := range c.Methods {
		if meth.IsLocal || !methodVisibleOnStub(meth) {
			continue
		}

		emitAdapterCase(w, meth, m.Registry)
	}

	fmt.Fprintf(w, "\tdefault:\n")

	if c.IsRoot() {
		// No parent adapter to delegate the unknown method to.
		fmt.Fprintf(w, "\t\tthrow Exception(\"Method does not exists\");\n")
	} else {
		fmt.Fprintf(w, "\t\t%s::invokeMethod(methid, inv);\n", c.AdapterBase())
	}

	fmt.Fprintf(w, "\t}\n")
	fmt.Fprintf(w, "}\n\n")
}

// emitAdapterCase mirrors the JAR's case shape, including its quirks:
//
//   - opening brace on its own line, indented two tabs;
//   - by-ref simple types (String, Unicode) use a 3-tab + leading-SPACE
//     `Type x; inv->getXParameter(x);` line;
//   - by-value primitives use 3-tab `Type x = inv->getXParameter();`;
//   - class types use 3-tab `Type* x = static_cast<Type*>(inv->getObjectParameter());`;
//   - empty `\t\t\t\n` lines bracket the call (params block + before
//     closing brace, only on void path).
func emitAdapterCase(w io.Writer, m sema.Method, reg *sema.Registry) {
	fmt.Fprintf(w, "\tcase %s:\n", m.RPCName)
	fmt.Fprintf(w, "\t\t{\n")

	for _, p := range m.Params {
		emitAdapterCaseParam(w, p, reg)
	}

	fmt.Fprintf(w, "\t\t\t\n")

	switch {
	case m.IsVoid():
		fmt.Fprintf(w, "\t\t\t%s(%s);\n", m.Name, joinArgs(m.Params))
		fmt.Fprintf(w, "\t\t\t\n")
	case !sema.IsPrimitive(m.Return.Name):
		// Class- or generic-typed return: serialize object identity over
		// the wire. The Returns probe confirmed that generic returns
		// (`Vector<T>`, `SortedVector<T>`, etc.) follow the same path as
		// plain class returns — `DistributedObject* _m_res = method();
		// resp->insertLong(_m_res == NULL ? 0 : _m_res->_getObjectID());`.
		// `@weakReference` wraps as `ManagedWeakReference<T*>` — call
		// `.get()` to extract the raw pointer for the `_getObjectID()`
		// chain. `@reference` wraps as `Reference<T*>` but the JAR
		// relies on Reference<>'s implicit conversion to a raw `T*`
		// and skips `.get()`.
		call := m.Name + "(" + joinArgs(m.Params) + ")"

		if m.IsWeakReference {
			call += ".get()"
		}

		fmt.Fprintf(w, "\t\t\tDistributedObject* _m_res = %s;\n", call)
		fmt.Fprintf(w, "\t\t\tresp->insertLong(_m_res == NULL ? 0 : _m_res->_getObjectID());\n")
	default:
		retType := sema.CppRender(m.Return)

		if m.IsFinal && m.Return.Name != "void" {
			retType = "const " + retType
		}

		fmt.Fprintf(w, "\t\t\t%s _m_res = %s(%s);\n", retType, m.Name, joinArgs(m.Params))
		fmt.Fprintf(w, "\t\t\tresp->insert%s(_m_res);\n", sema.WireInsertMangle(m.Return))
	}

	fmt.Fprintf(w, "\t\t}\n")
	fmt.Fprintf(w, "\t\tbreak;\n")
}

func emitAdapterCaseParam(w io.Writer, p sema.Param, reg *sema.Registry) {
	mangle := sema.WireMangle(p.IDLType)

	// JAR quirk: `final` adds a leading space after the tabs (mirroring
	// the byref-string form). Surfaced by the Params probe — applies to
	// every param shape (primitive / class / generic / @dereferenced /
	// non-final string).
	leadingSpace := ""

	if p.Final {
		leadingSpace = " "
	}

	if p.RawTemplate != "" {
		// @rawTemplate(value="X") param: variable type uses the literal
		// template paste (`Reference<X >`); cast type strips template
		// args (`Reference*`).
		varType := p.IDLType.Name + "<" + p.RawTemplate + " >"
		castType := sema.CppRender(parser.Type{Name: p.IDLType.Name})
		fmt.Fprintf(w, "\t\t\t%s%s* %s = static_cast<%s*>(inv->getObjectParameter());\n",
			leadingSpace, varType, p.Name, castType)

		return
	}

	if p.Dereferenced && !sema.IsPrimitive(p.IDLType.Name) {
		// @dereferenced non-primitive param: byval value, deserialized
		// via the templated `getDereferencedSerializableParameter<T >()`.
		// CppRenderMethodType so generic class args wrap as ManagedReference.
		typ := sema.CppRenderMethodType(p.IDLType, reg)
		fmt.Fprintf(w, "\t\t\t%s%s %s = inv->getDereferencedSerializableParameter<%s >();\n",
			leadingSpace, typ, p.Name, typ)

		return
	}

	switch p.IDLType.Name {
	case "string", "unicode":
		// Byref simple types: `Type x; inv->getXParameter(x);`. Leading
		// space is gated on `final` (Params probe disambiguated this —
		// the corpus only had final string params).
		typ := sema.CppRender(p.IDLType)
		fmt.Fprintf(w, "\t\t\t%s%s %s; inv->get%sParameter(%s);\n",
			leadingSpace, typ, p.Name, mangle, p.Name)

		return
	}

	if sema.IsPrimitive(p.IDLType.Name) {
		// Byval primitives: `Type x = inv->getXParameter();`
		typ := sema.CppRender(p.IDLType)
		fmt.Fprintf(w, "\t\t\t%s%s %s = inv->get%sParameter();\n", leadingSpace, typ, p.Name, mangle)

		return
	}

	// Class or generic type: pointer + static_cast(getObjectParameter()).
	// Variable type is the full rendered form (CppRenderMethodType — so
	// generic class args wrap as ManagedReference). Cast type strips
	// template args entirely (`Vector*` not `Vector<X>*`) — JAR quirk.
	varType := sema.CppRenderMethodType(p.IDLType, reg)
	castType := sema.CppRender(parser.Type{Name: p.IDLType.Name})
	fmt.Fprintf(w, "\t\t\t%s%s* %s = static_cast<%s*>(inv->getObjectParameter());\n",
		leadingSpace, varType, p.Name, castType)
}

func emitAdapterDelegate(w io.Writer, model *sema.Model, meth sema.Method) {
	c := model.Class

	fmt.Fprintf(w, "%s %s::%s(%s)%s {\n",
		returnDeclForMethod(meth, model.Registry), c.Adapter, meth.Name, joinParamDecls(meth.Params, model.Registry), constSuffix(meth))

	if meth.IsVoid() {
		fmt.Fprintf(w, "\t(static_cast<%s*>(stub))->%s(%s);\n", c.Name, meth.Name, joinArgs(meth.Params))
	} else {
		fmt.Fprintf(w, "\treturn (static_cast<%s*>(stub))->%s(%s);\n", c.Name, meth.Name, joinArgs(meth.Params))
	}

	fmt.Fprintf(w, "}\n\n")
}
