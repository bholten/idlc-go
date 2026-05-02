package parser

import "github.com/bholten/tools/idlc-go/internal/lexer"

type Visibility string

const (
	// VisDefault means no `public`/`protected`/`private` keyword was
	// written. The IDL grammar's default is package-scope, which the
	// JAR's emit treats as "use the C++ class-default access level"
	// (i.e. emit no access specifier line).
	VisDefault Visibility = ""

	Public    Visibility = "public"
	Protected Visibility = "protected"
	Private   Visibility = "private"
)

type File struct {
	Pos      lexer.Pos
	Package  string // dotted: "server.chat"
	Imports  []Import
	Includes []Include
	Class    *Class
}

// Import is a top-level `import a.b.C;` directive — usually triggers an
// `#include "a/b/C.h"` in the generated header.
type Import struct {
	Pos  lexer.Pos
	Path string
}

// Include is a top-level `include a.b.C;` directive — semantically
// distinct from import in the JAR (forward-decl vs full include) but
// syntactically identical. We keep them separate so emit can decide.
type Include struct {
	Pos  lexer.Pos
	Path string
}

type Annotation struct {
	Pos  lexer.Pos
	Name string            // without the leading '@'
	Args map[string]string // value of @name(key = "...") — may be nil/empty
}

type Class struct {
	Pos         lexer.Pos
	Annotations []Annotation
	Doc         string // raw `/** ... */` block preceding the `class` keyword, "" if none
	Name        string
	Base        string   // unqualified type from `extends`; "" if none
	Implements  []string // unqualified types from `implements`
	Members     []Member
}

// Member is one of *Field, *Constructor, *Method.
type Member interface{ memberNode() }

type Field struct {
	Pos         lexer.Pos
	Annotations []Annotation
	Visibility  Visibility
	Transient   bool
	Static      bool
	Final       bool
	Type        Type
	Name        string
	Default     string // raw text after `=`, "" if none (e.g. "0", "null")
}

func (*Field) memberNode() {}

type Constructor struct {
	Pos         lexer.Pos
	Annotations []Annotation
	Visibility  Visibility
	Native      bool
	Name        string
	Params      []Param
	Body        *Body // nil if `;`-terminated (e.g. native ctor)
}

func (*Constructor) memberNode() {}

type Method struct {
	Pos          lexer.Pos
	Doc          string // raw `/** ... */` block, "" if none
	Annotations  []Annotation
	Visibility   Visibility
	Native       bool
	Abstract     bool
	Static       bool
	Final        bool
	Synchronized bool
	Return       Type
	Name         string
	Params       []Param
	Body         *Body // nil if abstract / native / forward-decl
}

func (*Method) memberNode() {}

type Param struct {
	Pos         lexer.Pos
	Annotations []Annotation
	Final       bool
	Type        Type
	Name        string
	Default     string // raw text after `=`, "" if none
}

// Type is an IDL type reference.
//
//	Name     — the head of the type name (e.g. "string", "Vector",
//	           "engine.core.ManagedObject"). May be dotted.
//	Generics — raw text between '<' and '>' for generic types
//	           (e.g. "unsigned long" for "Vector<unsigned long>"),
//	           "" when the type is not generic. Brackets are *not*
//	           included in the field. Nested generics are captured
//	           verbatim (e.g. "Reference<Behavior*>").
type Type struct {
	Pos      lexer.Pos
	Name     string
	Generics string
}

// Render returns the type as it would appear in C++-ish source
// (Name<Generics> or just Name).
func (t Type) Render() string {
	if t.Generics == "" {
		return t.Name
	}
	return t.Name + "<" + t.Generics + ">"
}

type Body struct {
	Pos lexer.Pos
	Raw string
}
