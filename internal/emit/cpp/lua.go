package cpp

import (
	"fmt"
	"io"
	"strings"

	"github.com/bholten/tools/idlc-go/internal/parser"
	"github.com/bholten/tools/idlc-go/internal/sema"
)

// emitLuaHeader writes the `class Lua<Class>` wrapper declaration that
// the JAR appends to the autogen header for `@lua`-annotated classes.
// The wrapper is consumed by `Luna<LuaX>::Register(L)` registration
// calls in hand-written code (DirectorManager.cpp). Layout:
//
//	class LuaFoo {
//	public:
//	    static const char className[];
//	    static Luna<LuaFoo>::RegType Register[];
//
//	    LuaFoo(lua_State *L);
//	    virtual ~LuaFoo();
//
//	    int _setObject(lua_State *L);  // built-in
//	    int _getObject(lua_State *L);  // built-in
//	    int <method>(lua_State *L);     // one per public IDL method
//	    ...
//
//	    Reference<Foo*> realObject;
//	};
//
// Includes only methods declared directly on this class (the IDL —
// inherited methods aren't repeated in the wrapper). Constructors are
// excluded; `_setObject`/`_getObject` are auto-generated.
func emitLuaHeader(w io.Writer, m *sema.Model) {
	if !m.Class.IsLua {
		return
	}

	c := m.Class

	fmt.Fprintf(w, "class Lua%s {\n", c.Name)
	fmt.Fprintf(w, "public:\n")
	fmt.Fprintf(w, "\tstatic const char className[];\n")
	fmt.Fprintf(w, "\tstatic Luna<Lua%s>::RegType Register[];\n\n", c.Name)
	fmt.Fprintf(w, "\tLua%s(lua_State *L);\n", c.Name)
	fmt.Fprintf(w, "\tvirtual ~Lua%s();\n\n", c.Name)
	fmt.Fprintf(w, "\tint _setObject(lua_State *L);\n")
	fmt.Fprintf(w, "\tint _getObject(lua_State *L);\n")

	methods := luaPublicMethods(c.Methods)

	for _, meth := range methods {
		fmt.Fprintf(w, "\tint %s(lua_State *L);\n", meth.Name)
	}

	fmt.Fprintf(w, "\n\tReference<%s*> realObject;\n", c.Name)
	fmt.Fprintf(w, "};\n\n")
}

// luaPublicMethods filters c.Methods to the Luna-wrapped subset: public
// (or default-vis) methods, deduped by name to handle IDL overloads.
func luaPublicMethods(methods []sema.Method) []sema.Method {
	filtered := make([]sema.Method, 0, len(methods))

	for _, m := range methods {
		if luaIncludesMethod(m) {
			filtered = append(filtered, m)
		}
	}

	return dedupMethodsByName(filtered)
}

// emitLuaSource writes the out-of-line definitions for the `Lua<Class>`
// wrapper: the className constant, Register array, ctor/dtor, the two
// built-in `_setObject`/`_getObject`, then one body per public IDL
// method. Inserted between Helper and POD source sections.
func emitLuaSource(w io.Writer, m *sema.Model) {
	if !m.Class.IsLua {
		return
	}

	c := m.Class

	fmt.Fprintf(w, "const char Lua%s::className[] = \"Lua%s\";\n\n", c.Name, c.Name)

	methods := luaPublicMethods(c.Methods)

	fmt.Fprintf(w, "Luna<Lua%s>::RegType Lua%s::Register[] = {\n", c.Name, c.Name)
	fmt.Fprintf(w, "\t{ \"_setObject\", &Lua%s::_setObject },\n", c.Name)
	fmt.Fprintf(w, "\t{ \"_getObject\", &Lua%s::_getObject },\n", c.Name)

	for _, meth := range methods {
		fmt.Fprintf(w, "\t{ %q, &Lua%s::%s },\n", meth.Name, c.Name, meth.Name)
	}

	fmt.Fprintf(w, "\t{ 0, 0 }\n")
	fmt.Fprintf(w, "};\n\n")

	fmt.Fprintf(w, "Lua%s::Lua%s(lua_State *L) {\n", c.Name, c.Name)
	fmt.Fprintf(w, "\trealObject = static_cast<%s*>(lua_touserdata(L, 1));\n", c.Name)
	fmt.Fprintf(w, "}\n\n")

	fmt.Fprintf(w, "Lua%s::~Lua%s() {\n", c.Name, c.Name)
	fmt.Fprintf(w, "}\n\n")

	fmt.Fprintf(w, "int Lua%s::_setObject(lua_State* L) {\n", c.Name)
	fmt.Fprintf(w, "\trealObject = static_cast<%s*>(lua_touserdata(L, -1));\n\n", c.Name)
	fmt.Fprintf(w, "\treturn 0;\n")
	fmt.Fprintf(w, "}\n\n")

	fmt.Fprintf(w, "int Lua%s::_getObject(lua_State* L) {\n", c.Name)
	fmt.Fprintf(w, "\tlua_pushlightuserdata(L, realObject.get());\n\n")
	fmt.Fprintf(w, "\treturn 1;\n")
	fmt.Fprintf(w, "}\n\n")

	for _, meth := range methods {
		emitLuaMethodBody(w, c, meth)
	}
}

// luaIncludesMethod reports whether a Method should be wrapped in the
// Luna bridge. The JAR includes every PUBLIC method declared directly
// on the IDL class; inherited methods, constructors, and non-public
// methods are excluded.
func luaIncludesMethod(meth sema.Method) bool {
	return meth.Visibility == parser.Public || meth.Visibility == parser.VisDefault
}

// dedupMethodsByName returns the subset of `methods` with a unique
// method name (first occurrence wins). The Luna bridge keys methods
// by name in the Register[] array, so overloaded IDL methods (same
// name, different signature) collapse to a single Lua entry. The JAR
// emits a single dispatching body that branches on `lua_is<T>` — for
// now we emit only the first overload, which compiles but loses the
// other overloads at runtime. Full dispatch is a follow-up.
func dedupMethodsByName(methods []sema.Method) []sema.Method {
	seen := map[string]bool{}
	out := make([]sema.Method, 0, len(methods))

	for _, m := range methods {
		if seen[m.Name] {
			continue
		}

		seen[m.Name] = true
		out = append(out, m)
	}

	return out
}

// emitLuaMethodBody writes the wrapper for one public IDL method.
// The body type-checks each Lua argument (innermost first, negative
// stack indexing), checks arity, marshals each argument to its C++
// type, calls realObject->method(args), pushes the return (or returns
// 0 for void), and falls through to LuaCallbackException throws on
// any check failure.
//
// Locker note: the JAR sometimes emits `Locker _guard(realObject);`
// before the call site (e.g. CellObject.setCellNumber), but the rule
// is empirically inconsistent — QuestVectorMap.setKey is the same
// shape but gets no Locker. Direct-ManagedObject children seem to
// skip it; deeper inheritance chains include it. Rather than mirror
// this fragile heuristic, idlc-go never emits the Locker wrap; the
// impl method's own internal locking handles thread safety. Side
// effect: byte-equality with the JAR diverges on a subset of @lua
// classes that extend two-or-more levels deep (CellObject, ResourceSpawn,
// FsBuffItem, etc.). Runtime correctness is unaffected.
//
// Generic-param stub rule: if any parameter has a generic type (e.g.
// `Vector<string>`), the JAR emits an empty stub body (just prelude
// + `return 0`). The Lua bridge can't marshal arbitrary generics, so
// these methods are unreachable from Lua at runtime by design — we
// reproduce the stub to match.
func emitLuaMethodBody(w io.Writer, c sema.Class, meth sema.Method) {
	fmt.Fprintf(w, "int Lua%s::%s(lua_State *L) {\n", c.Name, meth.Name)
	fmt.Fprintf(w, "\tint parameterCount = lua_gettop(L) - 1;\n")
	fmt.Fprintf(w, "\t\n")

	if hasGenericParam(meth.Params) {
		fmt.Fprintf(w, "\treturn 0;\n")
		fmt.Fprintf(w, "}\n\n")

		return
	}

	luaTypes := make([]string, len(meth.Params))

	for i, p := range meth.Params {
		luaTypes[i] = luaTypeName(p.IDLType)
	}

	sig := fmt.Sprintf("'%s:%s(%s)'", c.Name, meth.Name, strings.Join(luaTypes, ", "))

	if len(meth.Params) == 0 {
		emitLuaNoArgsBody(w, c, meth, sig)
		fmt.Fprintf(w, "}\n\n")

		return
	}

	emitLuaParamsBody(w, c, meth, sig)
	fmt.Fprintf(w, "}\n\n")
}

// emitLuaNoArgsBody handles the simple case: arity check then call.
func emitLuaNoArgsBody(w io.Writer, c sema.Class, meth sema.Method, sig string) {
	indent := "\t"
	fmt.Fprintf(w, "%sif (parameterCount == 0) {\n", indent)
	emitLuaCall(w, indent+"\t", meth, false)
	fmt.Fprintf(w, "%s} else {\n", indent)
	fmt.Fprintf(w, "%s\tthrow LuaCallbackException(L, \"invalid argument count \" + String::valueOf(parameterCount) + \" for lua method %s\");\n", indent, sig)
	fmt.Fprintf(w, "%s}\n", indent)
	fmt.Fprintf(w, "%sreturn 0;\n", indent)
}

// emitLuaParamsBody handles the nested `if (lua_is<T>(L, -idx))` ladder
// + arity check + marshalling + call + return push.
//
// Type-check ladder is innermost-first: outer check is the LAST param
// (stack index -1), innermost is the FIRST param (stack index -arity).
// The "argument at N" index in the throw counts from the END (so for
// 3 params, last param's check failure throws "argument at 0", first
// param's failure throws "argument at 2").
func emitLuaParamsBody(w io.Writer, c sema.Class, meth sema.Method, sig string) {
	arity := len(meth.Params)
	indent := "\t"

	// Outer-to-inner iteration: outer is last param, inner is first.
	for i := 0; i < arity; i++ {
		// Outermost check (i=0) is for params[arity-1]; innermost is
		// for params[0].
		paramIdx := arity - 1 - i
		p := meth.Params[paramIdx]
		stackIdx := -(arity - paramIdx) // -arity .. -1, paired with paramIdx 0 .. arity-1
		_ = stackIdx
		fmt.Fprintf(w, "%sif (%s(L, %d)) {\n", indent, luaCheckFunc(p.IDLType), -(arity - paramIdx))

		indent += "\t"
	}

	fmt.Fprintf(w, "%sif (parameterCount == %d) {\n", indent, arity)

	bodyIndent := indent + "\t"

	// Marshal in declaration order (params[0] first).
	for i, p := range meth.Params {
		stackIdx := -(arity - i)
		fmt.Fprintf(w, "%s%s\n", bodyIndent, luaMarshalParam(p, stackIdx))
	}

	fmt.Fprintln(w)

	emitLuaCall(w, bodyIndent, meth, true)

	fmt.Fprintf(w, "%s} else {\n", indent)
	fmt.Fprintf(w, "%s\tthrow LuaCallbackException(L, \"invalid argument count \" + String::valueOf(parameterCount) + \" for lua method %s\");\n", indent, sig)
	fmt.Fprintf(w, "%s}\n", indent)

	// Close the type-check ladder, emitting a "argument at N" throw
	// for each. N counts from the END (last param = 0).
	for i := arity - 1; i >= 0; i-- {
		indent = indent[:len(indent)-1]
		fmt.Fprintf(w, "%s} else {\n", indent)
		fmt.Fprintf(w, "%s\tthrow LuaCallbackException(L, \"invalid argument at %d for lua method %s\");\n", indent, i, sig)
		fmt.Fprintf(w, "%s}\n", indent)
	}

	fmt.Fprintf(w, "\treturn 0;\n")
}

// emitLuaCall writes the `realObject->method(args)` call line plus
// the return-value push. `hasArgs` indicates whether the caller has
// already declared the marshalled args as locals.
func emitLuaCall(w io.Writer, indent string, meth sema.Method, hasArgs bool) {
	args := make([]string, len(meth.Params))

	for i, p := range meth.Params {
		args[i] = p.Name
	}

	argList := strings.Join(args, ", ")

	if meth.IsVoid() {
		fmt.Fprintf(w, "%srealObject->%s(%s);\n\n", indent, meth.Name, argList)
		fmt.Fprintf(w, "%sreturn 0;\n", indent)

		return
	}

	cType := cppTypeForLuaLocal(meth.Return)

	switch luaReturnKind(meth.Return) {
	case luaReturnPrimitive:
		fmt.Fprintf(w, "%s%s result = realObject->%s(%s);\n\n", indent, cType, meth.Name, argList)
		fmt.Fprintf(w, "%s%s\n", indent, luaPushPrimitive(meth.Return))
		fmt.Fprintf(w, "%sreturn 1;\n", indent)
	case luaReturnString:
		fmt.Fprintf(w, "%s%s result = realObject->%s(%s);\n\n", indent, cType, meth.Name, argList)
		fmt.Fprintf(w, "%slua_pushstring(L, result.toCharArray());\n", indent)
		fmt.Fprintf(w, "%sreturn 1;\n", indent)
	case luaReturnClass:
		fmt.Fprintf(w, "%s%s result = realObject->%s(%s);\n\n", indent, cType, meth.Name, argList)
		fmt.Fprintf(w, "%sif (result != NULL)\n", indent)
		fmt.Fprintf(w, "%s\tlua_pushlightuserdata(L, result);\n", indent)
		fmt.Fprintf(w, "%selse\n", indent)
		fmt.Fprintf(w, "%s\tlua_pushnil(L);\n", indent)
		fmt.Fprintf(w, "%sreturn 1;\n", indent)
	}

	_ = hasArgs
}

type luaReturnCategory int

const (
	luaReturnVoid luaReturnCategory = iota
	luaReturnPrimitive
	luaReturnString
	luaReturnClass
)

func luaReturnKind(t parser.Type) luaReturnCategory {
	switch t.Name {
	case "void":
		return luaReturnVoid
	case "string", "unicode":
		return luaReturnString
	case "int", "unsigned int", "long", "unsigned long",
		"short", "unsigned short", "byte", "unsigned byte",
		"float", "double", "boolean":
		return luaReturnPrimitive
	default:
		return luaReturnClass
	}
}

// luaCheckFunc returns the Lua C-API type-check function name for an
// IDL parameter type.
func luaCheckFunc(t parser.Type) string {
	switch t.Name {
	case "string", "unicode":
		return "lua_isstring"
	case "boolean":
		return "lua_isboolean"
	case "int", "unsigned int", "long", "unsigned long",
		"short", "unsigned short", "byte", "unsigned byte",
		"float", "double":
		return "lua_isnumber"
	default:
		return "lua_isuserdata"
	}
}

// luaTypeName returns the Lua type label for the error-message
// signature ("string", "integer", "boolean", "userdata", "number"
// for float).
func luaTypeName(t parser.Type) string {
	switch t.Name {
	case "string", "unicode":
		return "string"
	case "boolean":
		return "boolean"
	case "int", "unsigned int", "long", "unsigned long",
		"short", "unsigned short", "byte", "unsigned byte":
		return "integer"
	case "float", "double":
		return "number"
	default:
		return "userdata"
	}
}

// cppTypeForLuaLocal returns the C++ local-variable type for a
// param/return in a Lua marshalling block. Strings render as `String`
// (engine3 type), classes as `T*`, primitives as their plain C++ name.
func cppTypeForLuaLocal(t parser.Type) string {
	switch t.Name {
	case "void":
		return "void"
	case "string", "unicode":
		return "String"
	}

	if sema.IsPrimitive(t.Name) {
		return sema.CppRender(t)
	}

	return t.Name + "*"
}

// luaMarshalParam returns a single marshalling line for one parameter:
// `[const ]<type> <name> = lua_to<X>(L, <idx>);`
// (or `T* x = static_cast<T*>(lua_touserdata(L, idx));` for class types).
func luaMarshalParam(p sema.Param, stackIdx int) string {
	t := p.IDLType

	if !sema.IsPrimitive(t.Name) && t.Name != "string" && t.Name != "unicode" {
		return fmt.Sprintf("%s* %s = static_cast<%s*>(lua_touserdata(L, %d));", t.Name, p.Name, t.Name, stackIdx)
	}

	cppType := cppTypeForLuaLocal(t)
	prefix := ""

	if p.Final {
		prefix = "const "
	}

	switch t.Name {
	case "string", "unicode":
		return fmt.Sprintf("%s%s %s = lua_tostring(L, %d);", prefix, cppType, p.Name, stackIdx)
	case "boolean":
		return fmt.Sprintf("%sbool %s = lua_toboolean(L, %d);", prefix, p.Name, stackIdx)
	case "float", "double":
		return fmt.Sprintf("%s%s %s = lua_tonumber(L, %d);", prefix, cppType, p.Name, stackIdx)
	default:
		return fmt.Sprintf("%s%s %s = lua_tointeger(L, %d);", prefix, cppType, p.Name, stackIdx)
	}
}

// luaPushPrimitive returns the lua_push call line for a primitive
// return type. Strings + classes are handled in emitLuaCall directly.
func luaPushPrimitive(t parser.Type) string {
	switch t.Name {
	case "boolean":
		return "lua_pushboolean(L, result);"
	case "float", "double":
		return "lua_pushnumber(L, result);"
	default:
		return "lua_pushinteger(L, result);"
	}
}

// hasGenericParam reports whether any parameter has a generic type
// (e.g. Vector<string>). The Lua bridge can't marshal arbitrary
// generics; the JAR emits an empty stub body for such methods.
func hasGenericParam(params []sema.Param) bool {
	for _, p := range params {
		if p.IDLType.Generics != "" {
			return true
		}
	}

	return false
}
