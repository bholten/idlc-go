package sema

import (
	"strings"

	"github.com/bholten/tools/idlc-go/internal/parser"
)

// idlToCpp maps IDL primitive type names to their C++ counterparts.
// Verified against the JAR's autogen output, NOT the design doc — the
// doc's "uint32 / int64 / uint64" was wrong; the JAR keeps native C++
// width-explicit names like `unsigned long long`.
var idlToCpp = map[string]string{
	"string":         "String",
	"unicode":        "UnicodeString",
	"boolean":        "bool",
	"byte":           "byte",
	"unsigned byte":  "byte",
	"short":          "short",
	"unsigned short": "unsigned short",
	"int":            "int",
	"unsigned int":   "unsigned int",
	"long":           "long long",
	"unsigned long":  "unsigned long long",
	"float":          "float",
	"void":           "void",
}

// CppRender returns the full C++ rendering of an IDL Type, including
// any generics (recursively type-mapped).
//
// Examples:
//   string                 → String
//   unsigned long          → unsigned long long
//   Vector<unsigned long>  → Vector<unsigned long long>
//   VectorMap<string, int> → VectorMap<String, int>
//   ChatRoom               → ChatRoom
func CppRender(t parser.Type) string {
	head := cppHead(t.Name)
	if t.Generics == "" {
		return head
	}
	return head + "<" + cppRenderGenericArgs(t.Generics) + ">"
}

// CppRenderMethodType is like CppRender but for method-signature types
// (params/returns): a class-typed argument inside a generic container
// gets pointer treatment. Known-IDL inners wrap in
// `ManagedReference<T* >` (with the smart-pointer trailing-space rule
// triggering an outer ` >`); non-IDL inners render as bare `T*`.
//
//	Vector<BaseMessage>          → Vector<BaseMessage*>          (BaseMessage non-IDL)
//	SortedVector<TreeEntry>      → SortedVector<ManagedReference<TreeEntry* > >
//
// Used by stub/impl/adapter param and return decl emission.
func CppRenderMethodType(t parser.Type, reg *Registry) string {
	head := cppHead(t.Name)
	if t.Generics == "" {
		return head
	}
	parts := strings.Split(t.Generics, ", ")
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if IsPrimitive(p) {
			parts[i] = cppHead(p)
			continue
		}
		if reg.classifies(p) == idlManaged {
			parts[i] = "ManagedReference<" + cppHead(p) + "* >"
			continue
		}
		parts[i] = cppHead(p) + "*"
	}
	joined := strings.Join(parts, ", ")
	if strings.HasSuffix(joined, " >") {
		return head + "<" + joined + " >"
	}
	return head + "<" + joined + ">"
}

// CppRenderFieldType renders a field's C++ type, applying the JAR's
// class-wrap rules:
//
//	* a non-generic IDL-managed class becomes `ManagedReference<T* >`
//	  (or `ManagedWeakReference<T* >` for `@weakReference`),
//	* a non-generic non-IDL class becomes `Reference<T* >`,
//	* a generic-typed field, when NOT `@dereferenced`, gets an outer
//	  wrap: `ManagedReference<Head<args>* >` if HEAD is IDL-managed,
//	  `Reference<Head<args>* >` otherwise (Vector / VectorMap),
//	* a generic-typed field WITH `@dereferenced` renders bare
//	  `Head<args>` (matches PendingMessageList's `pendingMessages`,
//	  ChatManager's `gameRooms`),
//	* class-typed args inside generic containers always apply the
//	  IDL-vs-non-IDL split (`ManagedReference<T* >` vs bare `T*`),
//	* `@dereferenced` of an IDL-managed *non-generic* class wraps as
//	  `ManagedReference<T >` (no `*` inside) — `@weakReference` switches
//	  the wrap to `ManagedWeakReference<T >`,
//	* `@dereferenced` of a non-IDL non-generic class renders bare
//	  (TreeEntry's `Coordinate`/`Logger` precedent).
//
// IsPrimitive types and primitives-only generics render plain.
func CppRenderFieldType(f Field, reg *Registry) string {
	// @rawTemplate on a field is an opaque-paste: render as
	// `TypeName<<inner> >` with trailing space, ignoring all other
	// wrap rules. Used by ShipObject's DeltaAutoVariable fields.
	if f.RawTemplate != "" {
		return f.IDLType.Name + "<" + f.RawTemplate + " >"
	}
	if f.IDLType.Generics != "" {
		rendered := renderGenericInners(f.IDLType, reg)
		if f.Dereferenced {
			return rendered
		}
		if reg.classifies(f.IDLType.Name) == idlManaged {
			if f.WeakRef {
				return "ManagedWeakReference<" + rendered + "* >"
			}
			return "ManagedReference<" + rendered + "* >"
		}
		if f.WeakRef {
			return "ManagedWeakReference<" + rendered + "* >"
		}
		return "Reference<" + rendered + "* >"
	}
	if IsPrimitive(f.IDLType.Name) {
		return CppRender(f.IDLType)
	}
	idlManagedHead := reg.classifies(f.IDLType.Name) == idlManaged
	head := cppHead(f.IDLType.Name)
	if f.Dereferenced {
		// @dereferenced + IDL-managed: `ManagedReference<T >` (no `*`).
		// @dereferenced + non-IDL: bare T (TreeEntry's `Coordinate`).
		if !idlManagedHead {
			return head
		}
		if f.WeakRef {
			return "ManagedWeakReference<" + head + " >"
		}
		return "ManagedReference<" + head + " >"
	}
	if f.WeakRef {
		return "ManagedWeakReference<" + head + "* >"
	}
	if idlManagedHead {
		return "ManagedReference<" + head + "* >"
	}
	return "Reference<" + head + "* >"
}

// renderGenericInners renders the inner of a generic type with class-arg
// wrapping (`ManagedReference<X* >` for IDL-managed args, bare `X*` for
// non-IDL class args, plain for primitives). Returns just `Head<args>`
// — the outer wrap (if any) is the caller's responsibility.
func renderGenericInners(t parser.Type, reg *Registry) string {
	head := cppHead(t.Name)
	args := strings.Split(t.Generics, ", ")
	for i, a := range args {
		a = strings.TrimSpace(a)
		if IsPrimitive(a) {
			args[i] = cppHead(a)
			continue
		}
		if reg.classifies(a) == idlManaged {
			args[i] = "ManagedReference<" + cppHead(a) + "* >"
			continue
		}
		args[i] = cppHead(a) + "*"
	}
	joined := strings.Join(args, ", ")
	if strings.HasSuffix(joined, " >") {
		return head + "<" + joined + " >"
	}
	return head + "<" + joined + ">"
}

// CppRenderPODFieldType renders a POD class's `Optional<...>` inner.
//
// Rules (verified across corpus + Fields probe):
//
//   - Plain IDL-managed class field: `ManagedReference<XPOD* >`
//     (`ManagedWeakReference<XPOD* >` for `@weakReference`).
//   - Plain non-IDL class field: `Reference<X* >` (no `POD` suffix; no
//     POD class is generated for non-IDL types — LambdaObserver finding).
//   - `@dereferenced` IDL-managed class: `ManagedReference<X >` (NO
//     `POD` suffix, NO `*` inside) — `@weakReference` swaps the wrap to
//     `ManagedWeakReference<X >`.
//   - `@dereferenced` non-IDL class: bare X (TreeEntry's `Coordinate`).
//   - Generic with IDL-managed head: `ManagedReference<Head<args>POD* >`
//     — POD suffix is appended directly to the rendered generic
//     (`Vector<int>POD*`). Inner class args nest with their own `POD`
//     suffix (`Vector<ManagedReference<XPOD* > >POD*`).
//   - Generic with non-IDL head: `Head<args>` (no outer wrap, args
//     wrapped per IDL-class-ness — same as impl-side).
//   - Primitives and primitive-only generics: bare.
func CppRenderPODFieldType(f Field, reg *Registry) string {
	if f.RawTemplate != "" {
		return f.IDLType.Name + "<" + f.RawTemplate + " >"
	}
	if f.IDLType.Generics != "" {
		rendered := renderPODGenericInners(f.IDLType, reg)
		if f.Dereferenced {
			return rendered
		}
		if reg.classifies(f.IDLType.Name) == idlManaged {
			if f.WeakRef {
				return "ManagedWeakReference<" + rendered + "POD* >"
			}
			return "ManagedReference<" + rendered + "POD* >"
		}
		if f.WeakRef {
			return "ManagedWeakReference<" + rendered + "* >"
		}
		return "Reference<" + rendered + "* >"
	}
	if IsPrimitive(f.IDLType.Name) {
		return CppRender(f.IDLType)
	}
	idlManagedHead := reg.classifies(f.IDLType.Name) == idlManaged
	head := cppHead(f.IDLType.Name)
	if f.Dereferenced {
		if !idlManagedHead {
			return head
		}
		if f.WeakRef {
			return "ManagedWeakReference<" + head + " >"
		}
		return "ManagedReference<" + head + " >"
	}
	if idlManagedHead {
		if f.WeakRef {
			return "ManagedWeakReference<" + head + "POD* >"
		}
		return "ManagedReference<" + head + "POD* >"
	}
	return "Reference<" + head + "* >"
}

// renderPODGenericInners is the POD analogue of renderGenericInners:
// inner class args wrap as `ManagedReference<XPOD* >` for IDL-managed,
// bare `X*` for non-IDL.
func renderPODGenericInners(t parser.Type, reg *Registry) string {
	head := cppHead(t.Name)
	args := strings.Split(t.Generics, ", ")
	for i, a := range args {
		a = strings.TrimSpace(a)
		if IsPrimitive(a) {
			args[i] = cppHead(a)
			continue
		}
		if reg.classifies(a) == idlManaged {
			args[i] = "ManagedReference<" + cppHead(a) + "POD* >"
			continue
		}
		args[i] = cppHead(a) + "*"
	}
	joined := strings.Join(args, ", ")
	if strings.HasSuffix(joined, " >") {
		return head + "<" + joined + " >"
	}
	return head + "<" + joined + ">"
}

func cppHead(idlName string) string {
	if t, ok := idlToCpp[idlName]; ok {
		return t
	}
	return lastQNamePart(idlName)
}

// cppRenderGenericArgs maps each comma-separated arg in a Type.Generics
// string through cppHead. We don't recurse into nested generics here
// because the corpus only nests at most one level (e.g. `Vector<int>`)
// and parser.Type.Generics is the *rendered* form, not a structured AST.
// If/when we see deeper nesting in the wild, this can be tightened.
func cppRenderGenericArgs(generics string) string {
	parts := strings.Split(generics, ", ")
	for i, p := range parts {
		parts[i] = cppHead(strings.TrimSpace(p))
	}
	return strings.Join(parts, ", ")
}

// IsPrimitive returns true for IDL types that are stored and passed by
// value in C++ (no `*`, no Reference<>).
func IsPrimitive(idlName string) bool {
	switch idlName {
	case "string", "unicode", "boolean", "byte", "unsigned byte",
		"short", "unsigned short",
		"int", "unsigned int", "long", "unsigned long",
		"float", "void":
		return true
	}
	return false
}

// IsPointerReturn reports whether a method returning t emits its return
// type as `T*` rather than bare `T` in the generated C++. The rule we've
// observed: any non-primitive (i.e. user class or generic container)
// becomes a pointer return. String/UnicodeString stay by value.
func IsPointerReturn(t parser.Type) bool {
	if t.Generics != "" {
		return true
	}
	return !IsPrimitive(t.Name)
}

func lastQNamePart(qname string) string {
	if i := strings.LastIndex(qname, "."); i >= 0 {
		return qname[i+1:]
	}
	return qname
}
