package token

import "fmt"

// Kind enumerates the lexical token kinds of the cgpipe language.
type Kind int

const (
	ILLEGAL Kind = iota
	EOF
	NEWLINE // significant line break (statement separator)

	IDENT
	INT
	FLOAT
	STRING // double-quoted; Lit holds the raw inner text (interpolation deferred to eval)

	// keywords
	IF
	ELIF
	ELSE
	FOR
	IN
	WITH
	TRUE
	FALSE

	// assignment
	ASSIGN     // =
	QASSIGN    // ?=
	PLUSASSIGN // +=

	// comparison / logic
	EQ  // ==
	NEQ // !=
	LT  // <
	LE  // <=
	GT  // >
	GE  // >=
	AND // &&
	OR  // ||
	NOT // !

	// arithmetic
	PLUS    // +
	MINUS   // -
	STAR    // *
	SLASH   // /
	PERCENT // %
	POW     // **

	// structural
	COLON  // :
	SEMI   // ;
	COMMA  // ,
	DOT    // .
	DOTDOT // ..
	AT     // @
	CARET  // ^   (temporary-output marker on target outputs)
	LPAREN // (
	RPAREN // )
	LBRACK // [
	RBRACK // ]
	LBRACE // {
	RBRACE // }
	LBODY  // {{   opens a raw shell body
	RBODY  // }}   closes a raw shell body

	// BODY carries the raw, uninterpreted text of a {{ }} shell body (everything
	// between LBODY and the terminating lone-}} line). Directives, the -- separator,
	// %-control lines, and shell are all inside it; the parser splits it later.
	BODY
)

var names = [...]string{
	ILLEGAL:    "ILLEGAL",
	EOF:        "EOF",
	NEWLINE:    "NEWLINE",
	IDENT:      "IDENT",
	INT:        "INT",
	FLOAT:      "FLOAT",
	STRING:     "STRING",
	IF:         "if",
	ELIF:       "elif",
	ELSE:       "else",
	FOR:        "for",
	IN:         "in",
	WITH:       "with",
	TRUE:       "true",
	FALSE:      "false",
	ASSIGN:     "=",
	QASSIGN:    "?=",
	PLUSASSIGN: "+=",
	EQ:         "==",
	NEQ:        "!=",
	LT:         "<",
	LE:         "<=",
	GT:         ">",
	GE:         ">=",
	AND:        "&&",
	OR:         "||",
	NOT:        "!",
	PLUS:       "+",
	MINUS:      "-",
	STAR:       "*",
	SLASH:      "/",
	PERCENT:    "%",
	POW:        "**",
	COLON:      ":",
	SEMI:       ";",
	COMMA:      ",",
	DOT:        ".",
	DOTDOT:     "..",
	AT:         "@",
	CARET:      "^",
	LPAREN:     "(",
	RPAREN:     ")",
	LBRACK:     "[",
	RBRACK:     "]",
	LBRACE:     "{",
	RBRACE:     "}",
	LBODY:      "{{",
	RBODY:      "}}",
	BODY:       "BODY",
}

// String returns a human-readable name for the kind, used in error messages.
func (k Kind) String() string {
	if int(k) < len(names) && names[k] != "" {
		return names[k]
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// Pos is a source position. Line and Col are 1-based; Off is a 0-based byte
// offset into the source (used for slicing raw target-line path words).
type Pos struct {
	File string
	Line int
	Col  int
	Off  int
}

func (p Pos) String() string {
	return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Col)
}

// Token is a single lexical token.
type Token struct {
	Kind Kind
	Lit  string // literal text (identifier name, number text, raw string/body content)
	Pos  Pos
}

func (t Token) String() string {
	switch t.Kind {
	case IDENT, INT, FLOAT, STRING, BODY, ILLEGAL:
		// ILLEGAL carries the offending character(s) in Lit; show them so the
		// error names what it choked on rather than a bare "ILLEGAL".
		return fmt.Sprintf("%s(%q)", t.Kind, t.Lit)
	default:
		return t.Kind.String()
	}
}

var keywords = map[string]Kind{
	"if":    IF,
	"elif":  ELIF,
	"else":  ELSE,
	"for":   FOR,
	"in":    IN,
	"with":  WITH,
	"true":  TRUE,
	"false": FALSE,
}

// Lookup maps an identifier to its keyword kind, or IDENT if it isn't a keyword.
func Lookup(ident string) Kind {
	if k, ok := keywords[ident]; ok {
		return k
	}
	return IDENT
}
