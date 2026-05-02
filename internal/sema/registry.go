package sema

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/bholten/tools/idlc-go/internal/lexer"
	"github.com/bholten/tools/idlc-go/internal/parser"
)

// Registry tracks which qualified `package.Class` names are defined as
// IDL classes elsewhere in the corpus. Used by the C++ emitter to
// distinguish forward-declarable imports (IDL classes get a `class X;`
// + `class XPOD;` block in the header) from imports that need a real
// `#include` (utility headers, the parent class, non-IDL types).
type Registry struct {
	idlClasses        map[string]bool // qualified "engine.core.ManagedObject" → true
	idlNoPOD          map[string]bool // subset of idlClasses with no POD companion
	nonManagedParents map[string]bool // class names (qualified or unqualified) whose subclasses skip forward-decl layout

	// externalHeaders holds qnames of `-cp` (engine3) C++ utility
	// headers we know exist (e.g. `engine.log.Logger` from
	// `engine/log/Logger.h`). They are NOT IDL classes — they don't
	// forward-decl, don't get wrapped in `Reference<>`, and don't
	// participate in classifies(). Tracked separately so the body
	// rewriter knows about them for `Class.X → Class::X` static-call
	// rewrites (`Logger.setLoggingName` → `Logger::setLoggingName`).
	externalHeaders map[string]bool

	// classMeta tracks per-class IDL metadata indexed by UNQUALIFIED
	// class name (e.g. "SceneObject") — populated by `LoadFromDir`
	// when it parses each .idl. Used to walk the inheritance chain
	// for rules that depend on transitive properties (e.g. "does any
	// ancestor declare finalize()?" — drives the POD dtor's
	// `finalize();` line).
	classMeta map[string]ClassMeta
}

// ClassMeta is per-class IDL metadata the registry tracks for chain
// walks (parent lookups, transitive finalize, body-rewriter resolution
// of inherited field annotations, etc.).
type ClassMeta struct {
	Parent           string // unqualified parent name; "" for root classes
	DeclaresFinalize bool
	Fields           []ClassMetaField
}

// ClassMetaField records the parts of a field declaration the body
// rewriter needs when resolving `super.field.X` or bare `field.X` to
// the right C++ unwrap form. We deliberately don't store the full
// `Field` here — only the bits that change body emit. Annotations on
// parent fields don't propagate to subclasses but the rewriter uses
// them when chasing inherited names.
type ClassMetaField struct {
	Name         string
	TypeName     string // unqualified IDL type name; the rewriter consults the registry for managed-vs-non-managed
	WeakRef      bool   // @weakReference  → ManagedWeakReference<T*>; body needs `.getForUpdate().get()->`
	Dereferenced bool   // @dereferenced   → by-value; body uses `(&field)->` for member access
}

func NewRegistry() *Registry {
	return &Registry{idlClasses: map[string]bool{}}
}

// Add registers an IDL class qname (e.g.
// "server.zone.objects.creature.CreatureObject"). Use when the IDL
// for the class isn't part of the source-dir scan but is known to be
// IDL-defined — for instance, when matching against goldens that were
// produced from a larger upstream corpus than the test fixtures cover.
func (r *Registry) Add(qname string) {
	r.idlClasses[qname] = true
}

// IsIDLClass reports whether the given qualified name corresponds to a
// known IDL-defined class.
func (r *Registry) IsIDLClass(qname string) bool {
	if r == nil {
		return false
	}
	return r.idlClasses[qname]
}

// classification of a class name for emitter rendering.
type classKind int

const (
	classUnknown   classKind = iota // not a known IDL class — render as Reference<>/T*
	idlManaged                      // managed-object IDL class — render as ManagedReference<>
	idlNoPOD                        // IDL class without POD generation (forward-decl only, no XPOD)
)

// classifies looks up a name (unqualified or qualified) and returns
// how it should be rendered. Lookup tries the exact qname first, then
// trailing-segment match — IDL bodies typically use the unqualified
// name (e.g. `SceneObject`) while the registry stores qnames
// (`server.zone.objects.scene.SceneObject`).
//
// Exact-qname lookup: idlNoPOD takes priority over idlManaged because
// AddNoPOD inserts into BOTH maps (noPOD is the more specific mark,
// triggering `Reference<>` wraps rather than `ManagedReference<>`).
//
// Trailing-segment lookup: managed wins over noPOD when the same
// unqualified name resolves to qnames in both buckets — surfaced when
// running over the full Core3 source tree, where multiple paths can
// host same-named .h files (e.g. `server/zone/objects/scene/
// SceneObject.idl` IS the managed class, while `client/zone/objects/
// scene/SceneObject.h` is a separate stub that gets registered as
// noPOD via header scanning. The IDL author writing `SceneObject`
// in a server-side .idl means the managed one).
func (r *Registry) classifies(name string) classKind {
	if r == nil {
		return classUnknown
	}

	// Exact-qname match first: noPOD wins (more specific).
	if r.idlNoPOD[name] {
		return idlNoPOD
	}
	if r.idlClasses[name] {
		return idlManaged
	}

	// Trailing-segment fallback: managed wins over noPOD when both
	// have qnames ending in `.<name>`.
	hasManaged := false
	hasNoPOD := false
	for q := range r.idlClasses {
		i := strings.LastIndex(q, ".")
		if i < 0 || q[i+1:] != name {
			continue
		}
		if r.idlNoPOD[q] {
			hasNoPOD = true
		} else {
			hasManaged = true
		}
	}
	if hasManaged {
		return idlManaged
	}
	if hasNoPOD {
		return idlNoPOD
	}
	return classUnknown
}

// IsManagedShortName reports whether an unqualified class name resolves
// to an IDL-managed class (registered via `Add`, not `AddNoPOD`). Used
// by the body rewriter to decide whether `Type ident = expr;` should be
// wrapped as `ManagedReference<Type* > ident = expr;`.
func (r *Registry) IsManagedShortName(name string) bool {
	return r.classifies(name) == idlManaged
}

// IsNoPODShortName reports whether an unqualified class name resolves
// to an IDL no-POD class (registered via `AddNoPOD`).
func (r *Registry) IsNoPODShortName(name string) bool {
	return r.classifies(name) == idlNoPOD
}

// AddNoPOD registers an IDL class that has no POD companion — its
// forward decl in headers omits the `class XPOD;` line.
func (r *Registry) AddNoPOD(qname string) {
	if r.idlNoPOD == nil {
		r.idlNoPOD = map[string]bool{}
	}
	r.idlNoPOD[qname] = true
	r.idlClasses[qname] = true
}

// IsNoPOD reports whether the given IDL class was registered as
// no-POD. Looks up by exact qname.
func (r *Registry) IsNoPOD(qname string) bool {
	if r == nil {
		return false
	}
	return r.idlNoPOD[qname]
}

// AddNonManagedParent registers a class name (typically a utility
// base class like `engine.util.Observable` or `engine.util.Observer`)
// whose subclasses skip the forward-decl layout — every IDL import
// becomes a regular `#include`. The JAR uses this layout when the
// class doesn't ultimately descend from a managed-object root.
func (r *Registry) AddNonManagedParent(name string) {
	if r.nonManagedParents == nil {
		r.nonManagedParents = map[string]bool{}
	}
	r.nonManagedParents[name] = true
}

// IsNonManagedParent reports whether the given parent name (usually
// the unqualified `Class.Base` value) is registered as non-managed.
// Matches by exact name and by trailing-segment of any registered
// qname.
func (r *Registry) IsNonManagedParent(name string) bool {
	if r == nil || name == "" {
		return false
	}
	if r.nonManagedParents[name] {
		return true
	}
	for q := range r.nonManagedParents {
		if i := strings.LastIndex(q, "."); i >= 0 && q[i+1:] == name {
			return true
		}
	}
	return false
}

// LoadFromDir scans dir recursively for .idl files, parses each, and
// records `package.Class` in the registry. For each class it also
// records `ClassMeta` (parent name, declares-finalize) keyed by
// unqualified class name — used by inheritance-chain walks. Files
// that fail to parse are silently skipped (best-effort).
func (r *Registry) LoadFromDir(dir string) error {
	if r.classMeta == nil {
		r.classMeta = map[string]ClassMeta{}
	}
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".idl") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		f, err := parser.New(lexer.New(path, src)).ParseFile()
		if err != nil || f == nil || f.Class == nil {
			return nil
		}
		qname := f.Package + "." + f.Class.Name
		r.idlClasses[qname] = true

		meta := ClassMeta{Parent: f.Class.Base}
		for _, mem := range f.Class.Members {
			switch v := mem.(type) {
			case *parser.Method:
				if v.Name == "finalize" {
					meta.DeclaresFinalize = true
				}
			case *parser.Field:
				meta.Fields = append(meta.Fields, ClassMetaField{
					Name:         v.Name,
					TypeName:     v.Type.Name,
					WeakRef:      hasAnnotation(v.Annotations, "weakReference"),
					Dereferenced: hasAnnotation(v.Annotations, "dereferenced"),
				})
			}
		}
		r.classMeta[f.Class.Name] = meta
		return nil
	})
}

// IsAncestor reports whether `candidate` (unqualified) is anywhere in
// `startClass`'s inheritance chain — including `startClass` itself.
// Walks via `classMeta[]Parent`; stops when the chain reaches a class
// the registry doesn't know.
//
// Used by the header emitter to decide whether an `import` should be
// rendered as a regular `#include` (ancestor — already pulled in
// transitively via the parent's header) or as a forward-decl block
// (not an ancestor — only forward-needs).
func (r *Registry) IsAncestor(startClass, candidate string) bool {
	if r == nil || startClass == "" || candidate == "" {
		return false
	}
	visited := map[string]bool{}
	for cur := startClass; cur != ""; {
		if cur == candidate {
			return true
		}
		if visited[cur] {
			return false
		}
		visited[cur] = true
		meta, ok := r.classMeta[cur]
		if !ok {
			return false
		}
		cur = meta.Parent
	}
	return false
}

// LookupInheritedField walks the parent chain starting at `startClass`
// (typically the immediate parent's unqualified name) and returns the
// first field matching `name`. Stops walking when the chain reaches a
// class the registry doesn't know — e.g. `ManagedObject` if engine3
// wasn't scanned, which is fine because engine3 root classes don't
// declare the kinds of `@weakReference` / `@dereferenced` fields we
// care about for body rewrite.
func (r *Registry) LookupInheritedField(startClass, name string) (ClassMetaField, bool) {
	if r == nil {
		return ClassMetaField{}, false
	}
	visited := map[string]bool{}
	for cur := startClass; cur != ""; {
		if visited[cur] {
			return ClassMetaField{}, false
		}
		visited[cur] = true
		meta, ok := r.classMeta[cur]
		if !ok {
			return ClassMetaField{}, false
		}
		for _, fld := range meta.Fields {
			if fld.Name == name {
				return fld, true
			}
		}
		cur = meta.Parent
	}
	return ClassMetaField{}, false
}

// HasTransitiveFinalize reports whether the named class — or any
// ancestor in its IDL inheritance chain — declares an IDL `finalize()`
// method. Used by the POD-dtor emit (which calls `finalize();` when
// any ancestor in the chain has the method).
//
// The chain walk uses unqualified parent names from `ClassMeta`. Stops
// walking when the chain reaches a class the registry doesn't know
// (e.g. `ManagedObject` if engine3 wasn't scanned — typical for the
// hand-curated test fixtures, which is fine: those don't trigger
// transitive finalize anyway).
func (r *Registry) HasTransitiveFinalize(name string) bool {
	if r == nil {
		return false
	}
	visited := map[string]bool{}
	for cur := name; cur != ""; {
		if visited[cur] {
			return false
		}
		visited[cur] = true
		meta, ok := r.classMeta[cur]
		if !ok {
			return false
		}
		if meta.DeclaresFinalize {
			return true
		}
		cur = meta.Parent
	}
	return false
}

// LoadExternalHeadersFromDir scans dir recursively for plain `.h` files
// and records them in the `externalHeaders` bucket — separate from the
// IDL class buckets. These headers come from the `-cp` classpath
// (engine3 source); the JAR uses them only for "is this name a known
// type?" lookups (driving the body-rewriter's `Class.X → Class::X`
// pass) and for include-resolution. They are NOT forward-declared and
// NOT wrapped in `Reference<>` / `ManagedReference<>`.
//
// Order: call AFTER `LoadFromDir`, so .idl-backed engine3 classes
// (Observable, Observer, ManagedObject) are categorised as IDL first
// and skipped here.
func (r *Registry) LoadExternalHeadersFromDir(dir string) error {
	if r.externalHeaders == nil {
		r.externalHeaders = map[string]bool{}
	}
	return filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "autogen" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".h") {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return nil
		}
		qname := strings.ReplaceAll(strings.TrimSuffix(rel, ".h"), "/", ".")
		if r.IsIDLClass(qname) {
			return nil
		}
		r.externalHeaders[qname] = true
		return nil
	})
}

// AllKnownShortNames returns the set of unqualified class names
// the registry knows about — IDL classes plus external `-cp` headers.
// Used by the body rewriter to identify `Class.X → Class::X` static-
// call candidates beyond the IDL author's explicit `import` /
// `include` directives. (Engine3's `Logger`, `System`, etc. typically
// aren't explicitly imported but the body still references them.)
func (r *Registry) AllKnownShortNames() []string {
	if r == nil {
		return nil
	}
	seen := map[string]bool{}
	collect := func(q string) {
		i := strings.LastIndex(q, ".")
		if i >= 0 {
			seen[q[i+1:]] = true
		} else {
			seen[q] = true
		}
	}
	for q := range r.idlClasses {
		collect(q)
	}
	for q := range r.externalHeaders {
		collect(q)
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	return out
}

// LoadHeadersFromDir scans dir recursively for plain `.h` files and
// registers each as a forward-decl-only IDL class (`AddNoPOD`). The
// JAR uses this same lookup to resolve `import system.util.Vector;`
// against `system/util/Vector.h` — the header acts as a "this type
// exists" marker for non-IDL utility classes.
//
// Order: call this AFTER `LoadFromDir`, so .idl-backed classes win
// over .h-only ones. The autogen output dir (if present) is skipped
// to avoid registering generated headers.
func (r *Registry) LoadHeadersFromDir(dir string) error {
	return filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Skip autogen output trees so we don't register generated
			// headers as if they were primary class definitions.
			if d.Name() == "autogen" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".h") {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return nil
		}
		qname := strings.ReplaceAll(strings.TrimSuffix(rel, ".h"), "/", ".")

		// Skip if already registered (e.g. as a managed IDL class
		// via LoadFromDir). Otherwise add as forward-decl-only.
		if r.IsIDLClass(qname) {
			return nil
		}
		r.AddNoPOD(qname)
		return nil
	})
}
