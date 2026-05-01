package lexer

import "fmt"

type Kind int

const (
	EOF Kind = iota
	Ident
	StringLit
	NumberLit

	At          // @
	Dot         // .
	Comma       // ,
	Semi        // ;
	LParen      // (
	RParen      // )
	LBrace      // {
	RBrace      // }
	Equals      // =
	LBracket    // [
	RBracket    // ]
	LessThan    // <
	GreaterThan // >

	KwPackage
	KwImport
	KwInclude
	KwClass
	KwExtends
	KwImplements
	KwInterface
	KwPublic
	KwProtected
	KwPrivate
	KwFinal
	KwVoid
	KwTransient
	KwNative
	KwAbstract
	KwStatic
	KwSynchronized
	KwReturn
	KwNew
	KwIf
	KwElse
	KwWhile
	KwFor
	KwTrue
	KwFalse
	KwNull
)

var keywords = map[string]Kind{
	"package":      KwPackage,
	"import":       KwImport,
	"include":      KwInclude,
	"class":        KwClass,
	"extends":      KwExtends,
	"implements":   KwImplements,
	"interface":    KwInterface,
	"public":       KwPublic,
	"protected":    KwProtected,
	"private":      KwPrivate,
	"final":        KwFinal,
	"void":         KwVoid,
	"transient":    KwTransient,
	"native":       KwNative,
	"abstract":     KwAbstract,
	"static":       KwStatic,
	"synchronized": KwSynchronized,
}

type Token struct {
	Kind Kind
	Lit  string
	Pos  Pos
}

type Pos struct {
	File string
	Line int
	Col  int
	Off  int
}

func (p Pos) String() string {

	return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Col)
}
