// Package parser turns an IDL token stream into an AST. It is a hand
// rolled recursive-descent parser; the IDL grammar is small enough that
// a parser library would be overkill.
package parser

import (
	"fmt"
	"strings"

	"github.com/bholten/idlc-go/internal/lexer"
)

type Parser struct {
	lx  *lexer.Lexer
	tok lexer.Token
}

func New(lx *lexer.Lexer) *Parser {
	p := &Parser{lx: lx}
	p.advance()
	return p
}

// Parse parses src into a *File. file is purely for diagnostics.
func Parse(file string, src []byte) (*File, error) {
	return New(lexer.New(file, src)).ParseFile()
}

func (p *Parser) ParseFile() (out *File, err error) {
	defer func() {
		if r := recover(); r != nil {
			// `*parseError` is the parser's own typed error; the lexer
			// panics with a plain `string` (e.g. `"unexpected character
			// '\'' at file.idl:25:23"`) — convert both to a returned
			// error so callers can handle bad input without crashing.
			// Surfaced when running `LoadFromDir` over the full Core3
			// source tree (some IDLs have constructs we don't handle).
			switch e := r.(type) {
			case *parseError:
				err = e
			case error:
				err = e
			case string:
				err = fmt.Errorf("parse panic: %s", e)
			default:
				err = fmt.Errorf("parse panic: %v", r)
			}
		}
	}()

	f := &File{Pos: p.tok.Pos}

	if p.tok.Kind == lexer.KwPackage {
		p.advance()
		f.Package = p.parseQName()
		p.expect(lexer.Semi)
	}

	for {
		switch p.tok.Kind {
		case lexer.KwImport:
			pos := p.tok.Pos
			p.advance()
			f.Imports = append(f.Imports, Import{Pos: pos, Path: p.parseQName()})
			p.expect(lexer.Semi)
			continue
		case lexer.KwInclude:
			pos := p.tok.Pos
			p.advance()
			f.Includes = append(f.Includes, Include{Pos: pos, Path: p.parseQName()})
			p.expect(lexer.Semi)
			continue
		}
		break
	}

	classDoc := p.lx.TakeDocComment()
	classAnns := p.parseAnnotations()

	if classDoc == "" {
		classDoc = p.lx.TakeDocComment()
	}

	cls := p.parseClass()
	cls.Annotations = append(classAnns, cls.Annotations...)
	cls.Doc = classDoc
	f.Class = cls

	p.expect(lexer.EOF)
	return f, nil
}

func (p *Parser) parseClass() *Class {
	pos := p.tok.Pos
	p.expect(lexer.KwClass)
	name := p.expectIdent()

	c := &Class{Pos: pos, Name: name}

	if p.tok.Kind == lexer.KwExtends {
		p.advance()
		c.Base = p.parseQName()
	}

	if p.tok.Kind == lexer.KwImplements {
		p.advance()
		for {
			c.Implements = append(c.Implements, p.parseQName())

			if p.tok.Kind == lexer.Comma {
				p.advance()
				continue
			}

			break
		}
	}

	p.expect(lexer.LBrace)

	for p.tok.Kind != lexer.RBrace && p.tok.Kind != lexer.EOF {
		c.Members = append(c.Members, p.parseMember(name))
	}

	p.expect(lexer.RBrace)

	return c
}

// modifiers is the set of leading keywords on a member declaration.
// Visibility defaults to Protected if no `public/protected/private`
// keyword is seen — see parseVisibility.
type modifiers struct {
	visibility   Visibility
	transient    bool
	static       bool
	native       bool
	abstract     bool
	final        bool
	synchronized bool
}

func (p *Parser) parseModifiers() modifiers {
	m := modifiers{visibility: VisDefault}

	for {
		switch p.tok.Kind {
		case lexer.KwPublic:
			m.visibility = Public
		case lexer.KwProtected:
			m.visibility = Protected
		case lexer.KwPrivate:
			m.visibility = Private
		case lexer.KwTransient:
			m.transient = true
		case lexer.KwStatic:
			m.static = true
		case lexer.KwNative:
			m.native = true
		case lexer.KwAbstract:
			m.abstract = true
		case lexer.KwFinal:
			m.final = true
		case lexer.KwSynchronized:
			m.synchronized = true
		default:
			return m
		}

		p.advance()
	}
}

func (p *Parser) parseMember(className string) Member {
	pos := p.tok.Pos
	doc := p.lx.TakeDocComment()
	anns := p.parseAnnotations()

	// A doc comment may precede *or* sit between annotations. Take any
	// late-arriving one too, so `@a /** doc */ @b void foo()` works.
	if doc == "" {
		doc = p.lx.TakeDocComment()
	}

	mods := p.parseModifiers()

	// Constructor probe: cur token is the class name AND next is '('.
	if p.tok.Kind == lexer.Ident && p.tok.Lit == className {
		nameTok := p.tok
		p.advance()

		if p.tok.Kind == lexer.LParen {
			return p.parseConstructorRest(pos, anns, mods, nameTok.Lit)
		}

		// Not a ctor — the identifier was actually a type matching the
		// class name. Synthesize a Type (with optional generics) and
		// fall through.
		typ := Type{Pos: nameTok.Pos, Name: nameTok.Lit}

		if p.tok.Kind == lexer.LessThan {
			typ.Generics = p.parseGenericArgs()
		}

		return p.parseFieldOrMethod(pos, anns, mods, typ)
	}

	typ := p.parseType()
	mem := p.parseFieldOrMethod(pos, anns, mods, typ)

	if m, ok := mem.(*Method); ok {
		m.Doc = doc
	}

	return mem
}

func (p *Parser) parseConstructorRest(pos lexer.Pos, anns []Annotation, mods modifiers, name string) *Constructor {
	c := &Constructor{
		Pos:         pos,
		Annotations: anns,
		Visibility:  mods.visibility,
		Native:      mods.native,
		Name:        name,
	}

	c.Params = p.parseParamList()

	switch p.tok.Kind {
	case lexer.Semi:
		// `native` ctors with no body terminate with a semicolon.
		p.advance()
	case lexer.LBrace:
		b := p.parseBody()
		c.Body = &b
	default:
		p.errorf("expected ';' or '{' after constructor signature, got %s", p.tok.Lit)
	}
	return c
}

func (p *Parser) parseFieldOrMethod(pos lexer.Pos, anns []Annotation, mods modifiers, typ Type) Member {
	name := p.expectIdent()
	switch p.tok.Kind {
	case lexer.Semi:
		p.advance()
		return &Field{
			Pos:         pos,
			Annotations: anns,
			Visibility:  mods.visibility,
			Transient:   mods.transient,
			Static:      mods.static,
			Final:       mods.final,
			Type:        typ,
			Name:        name,
		}
	case lexer.Equals:
		p.advance()
		def := p.parseAtomicExpr()
		p.expect(lexer.Semi)
		return &Field{
			Pos:         pos,
			Annotations: anns,
			Visibility:  mods.visibility,
			Transient:   mods.transient,
			Static:      mods.static,
			Final:       mods.final,
			Type:        typ,
			Name:        name,
			Default:     def,
		}
	case lexer.LParen:
		return p.parseMethodRest(pos, anns, mods, typ, name)
	default:
		p.errorf("expected ';', '=' or '(' after member name, got %v (%q)", p.tok.Kind, p.tok.Lit)
	}
	return nil
}

func (p *Parser) parseMethodRest(pos lexer.Pos, anns []Annotation, mods modifiers, ret Type, name string) *Method {
	m := &Method{
		Pos:          pos,
		Annotations:  anns,
		Visibility:   mods.visibility,
		Native:       mods.native,
		Abstract:     mods.abstract,
		Static:       mods.static,
		Final:        mods.final,
		Synchronized: mods.synchronized,
		Return:       ret,
		Name:         name,
	}

	m.Params = p.parseParamList()

	switch p.tok.Kind {
	case lexer.Semi:
		// Forward decl / native / abstract — no body.
		p.advance()
	case lexer.LBrace:
		body := p.parseBody()
		m.Body = &body
	default:
		p.errorf("expected ';' or '{' after method signature, got %v (%q)", p.tok.Kind, p.tok.Lit)
	}
	return m
}

func (p *Parser) parseParamList() []Param {
	p.expect(lexer.LParen)
	var params []Param

	if p.tok.Kind == lexer.RParen {
		p.advance()
		return params
	}

	for {
		params = append(params, p.parseParam())

		if p.tok.Kind == lexer.Comma {
			p.advance()
			continue
		}

		break
	}

	p.expect(lexer.RParen)
	return params
}

func (p *Parser) parseParam() Param {
	pos := p.tok.Pos
	anns := p.parseAnnotations()
	final := false

	if p.tok.Kind == lexer.KwFinal {
		final = true
		p.advance()
	}

	// Annotations may also appear *after* `final` in the corpus
	// (e.g. `final @dereferenced Foo bar`). Be permissive.
	anns = append(anns, p.parseAnnotations()...)

	// `@final` as a param annotation is equivalent to the `final` modifier.
	for _, a := range anns {
		if a.Name == "final" {
			final = true
		}
	}

	typ := p.parseType()
	name := p.expectIdent()
	def := ""

	if p.tok.Kind == lexer.Equals {
		p.advance()
		def = p.parseAtomicExpr()
	}

	return Param{Pos: pos, Annotations: anns, Final: final, Type: typ, Name: name, Default: def}
}

// parseType handles plain identifiers, qualified names (a.b.c), `void`,
// `unsigned int|long`, and a trailing `<args>` for generic types.
func (p *Parser) parseType() Type {
	pos := p.tok.Pos

	if p.tok.Kind == lexer.KwVoid {
		p.advance()
		return Type{Pos: pos, Name: "void"}
	}

	var name string

	// `unsigned int` / `unsigned long`
	if p.tok.Kind == lexer.Ident && p.tok.Lit == "unsigned" {
		p.advance()

		if p.tok.Kind != lexer.Ident {
			p.errorf("expected primitive type after 'unsigned', got %s", p.tok.Lit)
		}

		second := p.tok.Lit
		p.advance()
		name = "unsigned " + second
	} else {
		name = p.parseQName()
	}

	t := Type{Pos: pos, Name: name}

	if p.tok.Kind == lexer.LessThan {
		t.Generics = p.parseGenericArgs()
	}

	return t
}

// parseGenericArgs parses `<T1, T2, ...>` and returns the comma-joined
// rendered text (without the surrounding angle brackets).
func (p *Parser) parseGenericArgs() string {
	p.expect(lexer.LessThan)
	var parts []string

	for {
		t := p.parseType()
		parts = append(parts, t.Render())
		if p.tok.Kind == lexer.Comma {
			p.advance()
			continue
		}

		break
	}

	p.expect(lexer.GreaterThan)

	return strings.Join(parts, ", ")
}

// parseAtomicExpr captures one default-value expression as raw text.
// We only need atoms (literal, identifier, dotted identifier) — no
// operators, no calls. Sufficient for the IDL corpus.
func (p *Parser) parseAtomicExpr() string {
	switch p.tok.Kind {
	case lexer.NumberLit, lexer.StringLit:
		v := p.tok.Lit
		if p.tok.Kind == lexer.StringLit {
			v = "\"" + v + "\""
		}
		p.advance()
		return v
	case lexer.Ident:
		// Allow dotted identifier (e.g. `Foo.BAR`).
		parts := []string{p.tok.Lit}
		p.advance()
		for p.tok.Kind == lexer.Dot {
			p.advance()
			parts = append(parts, p.expectIdent())
		}
		return strings.Join(parts, ".")
	}

	p.errorf("expected default-value atom, got %v (%q)", p.tok.Kind, p.tok.Lit)

	return ""
}

func (p *Parser) parseQName() string {
	parts := []string{p.expectIdent()}

	for p.tok.Kind == lexer.Dot {
		p.advance()
		parts = append(parts, p.expectIdent())
	}

	return strings.Join(parts, ".")
}

func (p *Parser) parseAnnotations() []Annotation {
	var out []Annotation

	for p.tok.Kind == lexer.At {
		out = append(out, p.parseAnnotation())
	}

	return out
}

func (p *Parser) parseAnnotation() Annotation {
	pos := p.tok.Pos
	p.expect(lexer.At)
	var name string

	if p.tok.Kind == lexer.KwFinal {
		name = p.tok.Lit
		p.advance()
	} else {
		name = p.expectIdent()
	}

	a := Annotation{Pos: pos, Name: name}

	if p.tok.Kind != lexer.LParen {
		return a
	}

	p.advance() // (
	a.Args = map[string]string{}

	if p.tok.Kind == lexer.RParen {
		p.advance()
		return a
	}

	for {
		key := p.expectIdent()
		p.expect(lexer.Equals)

		if p.tok.Kind != lexer.StringLit {
			p.errorf("expected string literal in annotation arg, got %s", p.tok.Lit)
		}

		a.Args[key] = p.tok.Lit
		p.advance()

		if p.tok.Kind == lexer.Comma {
			p.advance()
			continue
		}
		break
	}
	p.expect(lexer.RParen)

	return a
}

func (p *Parser) parseBody() Body {
	pos := p.tok.Pos

	if p.tok.Kind != lexer.LBrace {
		p.errorf("expected '{', got %v (%q)", p.tok.Kind, p.tok.Lit)
	}
	// Don't advance: the lexer has already consumed past the '{' when
	// it produced the LBrace token, so the next byte to lex would be
	// the first byte of the body. Calling p.advance() here would skip
	// over the body's first token. Instead, hand control directly to
	// CaptureBalancedBlock, which scans bytes until the matching '}'.
	raw, err := p.lx.CaptureBalancedBlock()

	if err != nil {
		p.errorf("%v", err)
	}

	// CaptureBalancedBlock consumed through the closing brace, so prime
	// the next token now.
	p.advance()

	return Body{Pos: pos, Raw: raw}
}

// --- token helpers ---

func (p *Parser) advance() {
	p.tok = p.lx.Next()
}

func (p *Parser) expect(k lexer.Kind) {
	if p.tok.Kind != k {
		p.errorf("expected %v, got %v (%q)", k, p.tok.Kind, p.tok.Lit)
	}

	p.advance()
}

func (p *Parser) expectIdent() string {
	if p.tok.Kind != lexer.Ident {
		p.errorf("expected identifier, got %v (%q)", p.tok.Kind, p.tok.Lit)
	}

	lit := p.tok.Lit
	p.advance()

	return lit
}

type parseError struct {
	pos lexer.Pos
	msg string
}

func (e *parseError) Error() string {
	return fmt.Sprintf("%s: %s", e.pos, e.msg)
}

func (p *Parser) errorf(format string, args ...any) {
	panic(&parseError{pos: p.tok.Pos, msg: fmt.Sprintf(format, args...)})
}
