// Package lexer tokenizes IDL source. It also exposes CaptureBalancedBlock
// so the parser can grab inline method bodies as opaque text without
// trying to lex C++.
package lexer

import (
	"fmt"
	"unicode"
	"unicode/utf8"
)

type Lexer struct {
	src  []byte
	file string
	off  int
	line int
	col  int

	// Doc-comment carry: a `/** ... */` block (note the double star)
	// stashes its full text here, including the surrounding `/**` and
	// `*/`. The parser drains it at the start of each member parse via
	// TakeDocComment. Plain `/* ... */` comments are skipped silently.
	pendingDoc string
}

// TakeDocComment returns the most recent `/** ... */` doc comment text
// and clears the stash. Returns "" if none is pending.
func (l *Lexer) TakeDocComment() string {
	d := l.pendingDoc
	l.pendingDoc = ""
	return d
}

func New(file string, src []byte) *Lexer {
	return &Lexer{
		src:  src,
		file: file,
		line: 1,
		col:  1,
	}
}

func (l *Lexer) Pos() Pos {
	return Pos{File: l.file, Line: l.line, Col: l.col, Off: l.off}
}

func (l *Lexer) Next() Token {
	l.skipWhitespaceAndComments()

	if l.eof() {
		return Token{Kind: EOF, Pos: l.Pos()}
	}

	pos := l.Pos()
	ch, _ := l.peek()

	switch ch {
	case '@':
		l.advance()
		return Token{Kind: At, Lit: "@", Pos: pos}
	case '.':
		l.advance()
		return Token{Kind: Dot, Lit: ".", Pos: pos}
	case ',':
		l.advance()
		return Token{Kind: Comma, Lit: ",", Pos: pos}
	case ';':
		l.advance()
		return Token{Kind: Semi, Lit: ";", Pos: pos}
	case '(':
		l.advance()
		return Token{Kind: LParen, Lit: "(", Pos: pos}
	case ')':
		l.advance()
		return Token{Kind: RParen, Lit: ")", Pos: pos}
	case '{':
		l.advance()
		return Token{Kind: LBrace, Lit: "{", Pos: pos}
	case '}':
		l.advance()
		return Token{Kind: RBrace, Lit: "}", Pos: pos}
	case '=':
		l.advance()
		return Token{Kind: Equals, Lit: "=", Pos: pos}
	case '[':
		l.advance()
		return Token{Kind: LBracket, Lit: "[", Pos: pos}
	case ']':
		l.advance()
		return Token{Kind: RBracket, Lit: "]", Pos: pos}
	case '<':
		l.advance()
		return Token{Kind: LessThan, Lit: "<", Pos: pos}
	case '>':
		l.advance()
		return Token{Kind: GreaterThan, Lit: ">", Pos: pos}
	case '"':
		return l.stringLit(pos, '"')
	case '\'':
		return l.stringLit(pos, '\'')
	}

	if isIdentStart(ch) {
		return l.identOrKeyword(pos)
	}

	if isDigit(ch) {
		return l.numberLit(pos)
	}

	if ch == '-' && isDigit(l.peekAt(1)) {
		return l.numberLit(pos)
	}

	panic(fmt.Sprintf("unexpected character %q at %s", ch, pos))
}

func (l *Lexer) numberLit(pos Pos) Token {
	start := l.off

	if ch, _ := l.peek(); ch == '-' {
		l.advance()
	}

	for !l.eof() {
		ch, _ := l.peek()

		if isDigit(ch) || ch == '.' || ch == 'x' || ch == 'X' ||
			(ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F') ||
			ch == 'L' || ch == 'l' || ch == 'U' || ch == 'u' || ch == 'f' || ch == 'F' {
			l.advance()
			continue
		}

		break
	}

	return Token{Kind: NumberLit, Lit: string(l.src[start:l.off]), Pos: pos}
}

func isDigit(r rune) bool { return r >= '0' && r <= '9' }

// CaptureBalancedBlock reads from the current offset (which must be one
// byte past an opening '{') until the matching '}' and returns the raw
// content (not including either brace). The lexer is left positioned
// just past the closing brace, so the caller's next Next() call returns
// the token following the block.
//
// Strings, line comments, and block comments inside the body are handled
// — braces inside them don't count toward depth.
func (l *Lexer) CaptureBalancedBlock() (string, error) {
	start := l.off
	depth := 1

	for !l.eof() {
		ch, w := l.peek()
		switch {
		case ch == '{':
			depth++
			l.advance()

		case ch == '}':
			depth--

			if depth == 0 {
				body := string(l.src[start:l.off])
				l.advance() // consume the closing brace
				return body, nil
			}

			l.advance()

		case ch == '"':
			if err := l.skipStringInBody(); err != nil {
				return "", err
			}

		case ch == '/' && l.peekAt(1) == '/':
			l.skipLineComment()

		case ch == '/' && l.peekAt(1) == '*':
			if err := l.skipBlockComment(); err != nil {
				return "", err
			}

		case ch == '\n':
			l.advance()

		default:
			_ = w
			l.advance()
		}
	}

	return "", fmt.Errorf("%s: unterminated block starting", l.Pos())
}

func (l *Lexer) skipStringInBody() error {
	l.advance() // opening quote

	for !l.eof() {
		ch, _ := l.peek()

		if ch == '\\' {
			l.advance()

			if l.eof() {
				return fmt.Errorf("%s: unterminated string", l.Pos())
			}

			l.advance()
			continue
		}

		if ch == '"' {
			l.advance()
			return nil
		}

		l.advance()
	}

	return fmt.Errorf("%s: unterminated string", l.Pos())
}

func (l *Lexer) skipLineComment() {
	for !l.eof() {
		ch, _ := l.peek()
		l.advance()

		if ch == '\n' {
			return
		}
	}
}

func (l *Lexer) skipBlockComment() error {
	start := l.off // position of the leading '/'
	l.advance()    // /
	l.advance()    // *
	// `/**` (note the second `*`) marks a doc comment. We stash the
	// raw text — including the surrounding `/**` and `*/` — for the
	// parser to attach to the next member.
	isDoc := false

	if ch, _ := l.peek(); ch == '*' && l.peekAt(1) != '/' {
		isDoc = true
	}

	for !l.eof() {
		ch, _ := l.peek()

		if ch == '*' && l.peekAt(1) == '/' {
			l.advance()
			l.advance()

			if isDoc {
				l.pendingDoc = string(l.src[start:l.off])
			}

			return nil
		}

		l.advance()
	}

	return fmt.Errorf("%s: unterminated block comment", l.Pos())
}

func (l *Lexer) skipWhitespaceAndComments() {
	for !l.eof() {
		ch, _ := l.peek()

		switch {
		case ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n':
			l.advance()
		case ch == '/' && l.peekAt(1) == '/':
			l.skipLineComment()
		case ch == '/' && l.peekAt(1) == '*':
			_ = l.skipBlockComment()
		default:
			return
		}
	}
}

func (l *Lexer) stringLit(pos Pos, quote rune) Token {
	l.advance() // opening quote
	start := l.off

	for !l.eof() {
		ch, _ := l.peek()

		if ch == '\\' {
			l.advance()

			if !l.eof() {
				l.advance()
			}

			continue
		}

		if ch == quote {
			lit := string(l.src[start:l.off])
			l.advance()
			return Token{Kind: StringLit, Lit: lit, Pos: pos}
		}

		l.advance()
	}

	panic(fmt.Sprintf("unterminated string at %s", pos))
}

func (l *Lexer) identOrKeyword(pos Pos) Token {
	start := l.off

	for !l.eof() {
		ch, _ := l.peek()

		if !isIdentContinue(ch) {
			break
		}

		l.advance()
	}

	lit := string(l.src[start:l.off])

	if kw, ok := keywords[lit]; ok {
		return Token{Kind: kw, Lit: lit, Pos: pos}
	}

	return Token{Kind: Ident, Lit: lit, Pos: pos}
}

func (l *Lexer) eof() bool { return l.off >= len(l.src) }

func (l *Lexer) peek() (rune, int) {
	if l.eof() {
		return 0, 0
	}

	return utf8.DecodeRune(l.src[l.off:])
}

func (l *Lexer) peekAt(delta int) rune {
	if l.off+delta >= len(l.src) {
		return 0
	}

	r, _ := utf8.DecodeRune(l.src[l.off+delta:])

	return r
}

func (l *Lexer) advance() {
	if l.eof() {
		return
	}

	ch, w := utf8.DecodeRune(l.src[l.off:])
	l.off += w

	if ch == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
}

func isIdentStart(r rune) bool {
	return r == '_' || unicode.IsLetter(r)
}

func isIdentContinue(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
