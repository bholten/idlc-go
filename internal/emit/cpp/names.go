package cpp

import (
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/bholten/tools/idlc-go/internal/parser"
	"github.com/bholten/tools/idlc-go/internal/sema"
)

// importToInclude turns "engine.core.ManagedObject" into
// "engine/core/ManagedObject.h".
func importToInclude(qname string) string {
	parts := strings.Split(qname, ".")
	return path.Join(parts...) + ".h"
}

// guardName returns the include-guard token, e.g. "CHATMESSAGE_H_".
func guardName(className string) string {
	return strings.ToUpper(className) + "_H_"
}

// returnDecl renders the C++ return type for a method, including the
// trailing `*` for pointer-returning class types.
//
// Examples:
//
//	void                  → "void"
//	string                → "String"
//	Vector<unsigned long> → "Vector<unsigned long long>*"
//	ChatRoom (no anns)    → "ChatRoom*"
//	@reference ChatRoom   → "Reference<ChatRoom* >"
func returnDecl(t parser.Type, reg *sema.Registry) string {
	if t.Name == "void" {
		return "void"
	}

	rendered := sema.CppRenderMethodType(t, reg)

	if sema.IsPointerReturn(t) {
		return rendered + "*"
	}

	return rendered
}

// returnDeclForMethod applies @reference (Reference<T*> wrap),
// @weakReference (ManagedWeakReference<T*> wrap), @dereferenced (drop
// the trailing pointer — return by value), and `final` (prepend
// `const`) to the return type. Defers to returnDecl for the plain case.
//
// `final` interaction with the wrapping annotations puts the `const`
// INSIDE the wrap — e.g. `@reference final ManagedObject` →
// `Reference<const ManagedObject* >` (not `const Reference<...>`),
// surfaced by the Returns probe.
func returnDeclForMethod(m sema.Method, reg *sema.Registry) string {
	// @rawTemplate on a method: opaque-paste — `Head<inner >*`. The
	// pointer is always there (return-as-pointer rule), and other
	// wrap annotations are ignored on this path.
	if m.RawTemplate != "" {
		rendered := m.Return.Name + "<" + m.RawTemplate + " >"
		return maybeConst(m.IsFinal, rendered+"*")
	}

	switch {
	case m.IsReference && m.Return.Generics == "" && !sema.IsPrimitive(m.Return.Name):
		inner := sema.CppRenderMethodType(m.Return, reg)
		if m.IsFinal {
			inner = "const " + inner
		}

		return "Reference<" + inner + "* >"

	case m.IsWeakReference && m.Return.Generics == "" && !sema.IsPrimitive(m.Return.Name):
		inner := sema.CppRenderMethodType(m.Return, reg)

		if m.IsFinal {
			inner = "const " + inner
		}

		// Same managed-vs-non-managed split as `CppRenderFieldType`'s
		// `@weakReference` branch: managed → `ManagedWeakReference<X*>`,
		// non-managed → plain `WeakReference<X*>`. Mismatching the two
		// breaks `return weakField;` because the field type and method
		// return type must agree.
		if reg != nil && reg.IsManagedShortName(m.Return.Name) {
			return "ManagedWeakReference<" + inner + "* >"
		}

		return "WeakReference<" + inner + "* >"

	case m.IsDereferenced && m.Return.Name != "void":
		return maybeConst(m.IsFinal, sema.CppRenderMethodType(m.Return, reg))
	}

	return maybeConst(m.IsFinal, returnDecl(m.Return, reg))
}

// maybeConst prepends `const ` to a rendered type when `final` was set
// on the method. The JAR uses this for `final string`/`final unicode`
// returns to signal that the value is read-only at the call site.
func maybeConst(final bool, rendered string) string {
	if !final || rendered == "void" {
		return rendered
	}

	return "const " + rendered
}

// paramDecl renders a Param as a C++ function-parameter declaration
// without a default-value suffix.
//
//	@rawTemplate(value=X)→ "TypeName<X >* name" (opaque template paste,
//	                       always trailing-space-then-`>` regardless of
//	                       whether X ends with `*` — verified against
//	                       the Params probe)
//	@dereferenced final → "const Type& name" (by-reference, const)
//	@dereferenced       → "Type& name" (by-reference)
//	final               → prepend `const ` to the rendered type
//	String/Unicode      → "Type& name" (by-reference; const ONLY when final)
//	other class types   → "Type* name" (pointer, by-value)
//	primitives          → "Type name" (by-value)
//
// `final` adds `const` for any type — primitives, classes, generics,
// strings, etc. The Params probe disambiguated this from the previous
// "always const String" rule.
func paramDecl(p sema.Param, reg *sema.Registry) string {
	if p.RawTemplate != "" {
		return p.IDLType.Name + "<" + p.RawTemplate + " >* " + p.Name
	}

	rendered := sema.CppRenderMethodType(p.IDLType, reg)

	constPrefix := ""

	if p.Final {
		constPrefix = "const "
	}

	if p.Dereferenced {
		return constPrefix + rendered + "& " + p.Name
	}

	switch rendered {
	case "String", "UnicodeString":
		return constPrefix + rendered + "& " + p.Name
	}

	if !sema.IsPrimitive(p.IDLType.Name) {
		// Smart-pointer-headed params (`Reference<X>`, `ManagedReference<X>`,
		// ...) are passed by value — they already wrap a pointer, so an
		// extra `*` would be `Reference<X>*`, breaking VectorMap.put / etc.
		// which expect `const Reference<X>&`.
		if p.IDLType.Generics != "" && sema.IsSmartPointerWrapper(p.IDLType.Name) {
			return constPrefix + rendered + " " + p.Name
		}

		return constPrefix + rendered + "* " + p.Name
	}

	return constPrefix + rendered + " " + p.Name
}

// paramDeclWithDefault is paramDecl plus ` = <default>` if the IDL had one.
// The default expression is rewritten by paramDefaultExpr to translate
// IDL idioms (e.g. `null`) into their C++ equivalents (`NULL`).
func paramDeclWithDefault(p sema.Param, reg *sema.Registry) string {
	d := paramDecl(p, reg)

	if p.Default != "" {
		return d + " = " + paramDefaultExpr(p.Default)
	}

	return d
}

// paramDefaultExpr rewrites an IDL default-value expression for use in
// a C++ parameter declaration. For now this is the literal `null` →
// `NULL` swap (IDL's null literal vs C++'s macro). Other expressions
// pass through unchanged.
func paramDefaultExpr(s string) string {
	if s == "null" {
		return "NULL"
	}

	return s
}

func joinParamDecls(params []sema.Param, reg *sema.Registry) string {
	return joinParamDeclsX(params, false, reg)
}

func joinParamDeclsWithDefaults(params []sema.Param, reg *sema.Registry) string {
	return joinParamDeclsX(params, true, reg)
}

func joinParamDeclsX(params []sema.Param, withDefaults bool, reg *sema.Registry) string {
	if len(params) == 0 {
		return ""
	}

	out := make([]string, len(params))

	for i, p := range params {
		if withDefaults {
			out[i] = paramDeclWithDefault(p, reg)
		} else {
			out[i] = paramDecl(p, reg)
		}
	}

	return strings.Join(out, ", ")
}

func joinArgs(params []sema.Param) string {
	if len(params) == 0 {
		return ""
	}

	out := make([]string, len(params))

	for i, p := range params {
		out[i] = p.Name
	}

	return strings.Join(out, ", ")
}

// constSuffix returns " const" if the method should be marked const in C++.
// `@read` methods get const.
func constSuffix(m sema.Method) string {
	if m.IsRead {
		return " const"
	}

	return ""
}

// emitDocComment writes a captured `/** ... */` doc-comment block before
// a member declaration. The captured text already includes the
// surrounding `/**` and `*/`; we just prefix the first line with one
// tab. Subsequent lines retain their source indentation. No-op if doc
// is empty.
func emitDocComment(w io.Writer, doc string) {
	if doc == "" {
		return
	}

	fmt.Fprintf(w, "\t%s\n", doc)
}
