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
		// Outer wrap appends `*` to take the inner's address — UNLESS the
		// field's own head is already a smart-pointer (Reference<>,
		// ManagedReference<>, ...). In that case the inner is already
		// pointer-like and an extra `*` would over-indirect.
		ptr := "* "
		if isSmartPointerWrapper(cppHead(f.IDLType.Name)) {
			ptr = " "
		}
		if reg.classifies(f.IDLType.Name) == idlManaged {
			if f.WeakRef {
				return "ManagedWeakReference<" + rendered + ptr + ">"
			}
			return "ManagedReference<" + rendered + ptr + ">"
		}
		if f.WeakRef {
			return "WeakReference<" + rendered + ptr + ">"
		}
		return "Reference<" + rendered + ptr + ">"
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
		// `@weakReference` on a managed-object class wraps as
		// `ManagedWeakReference<X*>`; on a non-managed (engine3 header)
		// type the wrapper is the simpler `WeakReference<X*>` —
		// `ManagedWeakReference` requires the wrapped type to expose
		// `_getObjectID()` which non-managed types don't.
		if idlManagedHead {
			return "ManagedWeakReference<" + head + "* >"
		}
		return "WeakReference<" + head + "* >"
	}
	if idlManagedHead {
		return "ManagedReference<" + head + "* >"
	}
	return "Reference<" + head + "* >"
}

// renderGenericInners renders the inner args of a generic type. The
// rendering of each arg depends on the OUTER head:
//
//   - Smart-pointer wrappers (Reference/ManagedReference/ManagedWeakReference):
//     inner class args become bare `X*`; nested generics get `*` appended
//     after the close `>`. The smart-pointer's job is to pointer-wrap
//     whatever it contains, so no further wrap inside.
//   - Container generics (Vector/VectorMap/SortedVector/etc.): inner class
//     args get wrapped — `ManagedReference<X*>` for IDL-managed, `Reference<X*>`
//     for everything else. Nested generics pass through as-is.
//   - Primitives always pass through unchanged.
//
// Returns just `Head<args>` — the outer wrap (if any) is the caller's
// responsibility (CppRenderFieldType wraps with Reference/ManagedReference
// when the field's head is itself non-primitive).
func renderGenericInners(t parser.Type, reg *Registry) string {
	head := cppHead(t.Name)
	smartPtr := isSmartPointerWrapper(head)
	args := splitGenericArgs(t.Generics)
	for i, a := range args {
		args[i] = renderGenericArg(a, reg, smartPtr)
	}
	joined := strings.Join(args, ", ")
	if strings.HasSuffix(joined, ">") {
		// Avoid `>>` by inserting a separating space (matches JAR /
		// pre-C++14 template-parse convention). Also covers chains like
		// `> >` from a previously-rendered nested generic.
		return head + "<" + joined + " >"
	}
	return head + "<" + joined + ">"
}

// renderGenericArg renders one argument of a generic type. The
// `smartPtrOuter` flag tells the leaf logic which form to emit for
// non-primitive args. Cases (verified against JAR emit):
//
//   - `Reference<ManagedClass>`        → `Reference<ManagedReference<ManagedClass*>>`
//   - `Reference<NonManagedClass>`     → `Reference<NonManagedClass*>`         (bare `*`)
//   - `Vector<ManagedClass>`           → `Vector<ManagedReference<ManagedClass*>>`
//   - `Vector<NonManagedClass>`        → `Vector<Reference<NonManagedClass*>>` (Reference-wrap)
//   - `Reference<Vector<X>>`           → `Reference<Vector<X>*>`               (`*` after container nested)
//   - `Reference<Reference<X>>`        → `Reference<Reference<X> >`            (NO `*` — nested smart-ptr already pointer-like)
//   - `Vector<Reference<X>>`           → `Vector<Reference<X*>>`               (recurse, no outer wrap on the arg)
func renderGenericArg(a string, reg *Registry, smartPtrOuter bool) string {
	a = strings.TrimSpace(a)
	if IsPrimitive(a) {
		return cppHead(a)
	}
	if open := strings.Index(a, "<"); open >= 0 && strings.HasSuffix(a, ">") {
		innerName := a[:open]
		inner := a[open+1 : len(a)-1]
		nested := parser.Type{Name: innerName, Generics: inner}
		rendered := renderGenericInners(nested, reg)
		if smartPtrOuter && !isSmartPointerWrapper(cppHead(innerName)) {
			// Smart-pointer holding a container: container is value-typed,
			// so add `*` to take its address. Nested smart-ptr inside
			// smart-ptr is already pointer-like — no extra `*`.
			return rendered + "*"
		}
		return rendered
	}
	if smartPtrOuter {
		// Inside Reference<>/ManagedReference<>: managed leaf wraps as
		// `ManagedReference<X*>` (NOT bare `X*` — the JAR insists on the
		// nested wrap so the outer smart-pointer stores a managed
		// pointer-pointer chain). Non-managed leaf stays bare.
		if reg.classifies(a) == idlManaged {
			return "ManagedReference<" + cppHead(a) + "* >"
		}
		return cppHead(a) + "*"
	}
	if reg.classifies(a) == idlManaged {
		return "ManagedReference<" + cppHead(a) + "* >"
	}
	return "Reference<" + cppHead(a) + "* >"
}

// isSmartPointerWrapper reports whether `head` names an engine3
// pointer-wrapper template (Reference / ManagedReference / WeakReference
// / ManagedWeakReference / TemplateReference / etc.). The convention in
// the engine3 codebase is that any such wrapper's class name ends in
// `Reference`, and inside any such wrapper the inner type is always
// pointer-like — i.e. `Reference<X>` means `Reference<X*>`, not a
// re-wrapped `Reference<Reference<X*>>`.
func isSmartPointerWrapper(head string) bool {
	return strings.HasSuffix(head, "Reference")
}

// splitGenericArgs splits a generic-args string by top-level commas,
// preserving nested `<...>` groups. Example:
//
//	splitGenericArgs("unsigned int, Reference<GalaxyBanEntry>")
//	  ⟶ ["unsigned int", "Reference<GalaxyBanEntry>"]
func splitGenericArgs(s string) []string {
	var out []string
	depth := 0
	start := 0
	for i, ch := range s {
		switch ch {
		case '<':
			depth++
		case '>':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	if start < len(s) {
		out = append(out, strings.TrimSpace(s[start:]))
	}
	return out
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
		ptr := "* "
		podSuffix := "POD"
		if isSmartPointerWrapper(cppHead(f.IDLType.Name)) {
			ptr = " "
			podSuffix = ""
		}
		if reg.classifies(f.IDLType.Name) == idlManaged {
			if f.WeakRef {
				return "ManagedWeakReference<" + rendered + podSuffix + ptr + ">"
			}
			return "ManagedReference<" + rendered + podSuffix + ptr + ">"
		}
		if f.WeakRef {
			return "WeakReference<" + rendered + ptr + ">"
		}
		return "Reference<" + rendered + ptr + ">"
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
	if f.WeakRef {
		return "WeakReference<" + head + "* >"
	}
	return "Reference<" + head + "* >"
}

// renderPODGenericInners is the POD analogue of renderGenericInners.
// Same head-aware logic, but the leaf-class wrap uses the POD class name
// for IDL-managed args.
func renderPODGenericInners(t parser.Type, reg *Registry) string {
	head := cppHead(t.Name)
	smartPtr := isSmartPointerWrapper(head)
	args := splitGenericArgs(t.Generics)
	for i, a := range args {
		args[i] = renderPODGenericArg(a, reg, smartPtr)
	}
	joined := strings.Join(args, ", ")
	if strings.HasSuffix(joined, ">") {
		return head + "<" + joined + " >"
	}
	return head + "<" + joined + ">"
}

func renderPODGenericArg(a string, reg *Registry, smartPtrOuter bool) string {
	a = strings.TrimSpace(a)
	if IsPrimitive(a) {
		return cppHead(a)
	}
	if open := strings.Index(a, "<"); open >= 0 && strings.HasSuffix(a, ">") {
		innerName := a[:open]
		inner := a[open+1 : len(a)-1]
		nested := parser.Type{Name: innerName, Generics: inner}
		rendered := renderPODGenericInners(nested, reg)
		if smartPtrOuter && !isSmartPointerWrapper(cppHead(innerName)) {
			return rendered + "*"
		}
		return rendered
	}
	if smartPtrOuter {
		if reg.classifies(a) == idlManaged {
			return "ManagedReference<" + cppHead(a) + "POD* >"
		}
		return cppHead(a) + "*"
	}
	if reg.classifies(a) == idlManaged {
		return "ManagedReference<" + cppHead(a) + "POD* >"
	}
	return "Reference<" + cppHead(a) + "* >"
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
