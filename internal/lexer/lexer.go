package lexer

import (
	"strings"

	"github.com/compgenlab/cgpipe/internal/token"
)

// Lexer turns cgpipe source into a stream of tokens via repeated Next() calls,
// ending with a token.EOF.
//
// cgpipe is line-oriented: NEWLINE (and ';') separate statements, so the lexer
// emits NEWLINE tokens. The defining behavior is the mode flip on `{{`: the
// lexer captures the shell body verbatim until a lone `}}` line and returns it
// as a single token.BODY, rather than tokenizing it as cgpipe.
type Lexer struct {
	src  string
	file string
	off  int // byte offset of the next unread rune
	line int // 1-based line of off
	col  int // 1-based column of off

	inBody       bool      // next Next() should capture a shell body
	pendingRBody bool      // next Next() should emit the RBODY that closed a body
	rbodyPos     token.Pos // position recorded for that RBODY
	parenDepth   int       // open ( and [ nesting; a NEWLINE inside is insignificant
}

// New returns a Lexer over src; file is used only for token positions.
func New(src, file string) *Lexer {
	return &Lexer{src: src, file: file, off: 0, line: 1, col: 1}
}

func (l *Lexer) pos() token.Pos {
	return token.Pos{File: l.file, Line: l.line, Col: l.col, Off: l.off}
}

func (l *Lexer) tok(k token.Kind, lit string, p token.Pos) token.Token {
	return token.Token{Kind: k, Lit: lit, Pos: p}
}

// at returns the byte n positions ahead of off, or 0 at/after end of input.
func (l *Lexer) at(n int) byte {
	i := l.off + n
	if i < 0 || i >= len(l.src) {
		return 0
	}
	return l.src[i]
}

// advance consumes one byte, updating line/col.
func (l *Lexer) advance() byte {
	c := l.src[l.off]
	l.off++
	if c == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	return c
}

// Next returns the next token. After end of input it returns token.EOF.
func (l *Lexer) Next() token.Token {
	if l.pendingRBody {
		l.pendingRBody = false
		return l.tok(token.RBODY, "}}", l.rbodyPos)
	}
	if l.inBody {
		return l.lexBody()
	}
	return l.lexCode()
}

func (l *Lexer) lexCode() token.Token {
	l.skipInlineSpaceAndComments()

	if l.off >= len(l.src) {
		return l.tok(token.EOF, "", l.pos())
	}

	start := l.pos()
	c := l.at(0)

	switch {
	case c == '\n':
		l.advance()
		// Implicit line continuation: a newline inside ( ) or [ ] is insignificant,
		// so an expression (call args, a list literal) may span lines. Inside { }
		// newlines still separate statements, so brace depth is not counted here.
		if l.parenDepth > 0 {
			return l.lexCode()
		}
		return l.tok(token.NEWLINE, "", start)
	case isIdentStart(c):
		return l.lexIdent(start)
	case isDigit(c):
		return l.lexNumber(start)
	case c == '"':
		return l.lexString(start)
	}

	return l.lexOperator(start)
}

func (l *Lexer) skipInlineSpaceAndComments() {
	for l.off < len(l.src) {
		c := l.at(0)
		switch {
		case c == ' ' || c == '\t' || c == '\r':
			l.advance()
		case c == '#':
			// comment to end of line; leave the newline for NEWLINE
			for l.off < len(l.src) && l.at(0) != '\n' {
				l.advance()
			}
		default:
			return
		}
	}
}

func (l *Lexer) lexIdent(start token.Pos) token.Token {
	from := l.off
	for l.off < len(l.src) && isIdentPart(l.at(0)) {
		l.advance()
	}
	text := l.src[from:l.off]
	return l.tok(token.Lookup(text), text, start)
}

func (l *Lexer) lexNumber(start token.Pos) token.Token {
	from := l.off
	for l.off < len(l.src) && isDigit(l.at(0)) {
		l.advance()
	}
	kind := token.INT
	// a single '.' followed by a digit is a fractional part; ".." is a range,
	// so don't consume it into the number.
	if l.at(0) == '.' && l.at(1) != '.' && isDigit(l.at(1)) {
		kind = token.FLOAT
		l.advance() // '.'
		for l.off < len(l.src) && isDigit(l.at(0)) {
			l.advance()
		}
	}
	return l.tok(kind, l.src[from:l.off], start)
}

// lexString lexes a double-quoted string and returns its raw inner text
// (without the surrounding quotes), verbatim. A backslash escapes the next byte
// for the purpose of finding the closing quote, but the backslash is kept in the
// literal: escape processing and ${…}/@{…} interpolation are deferred to eval.
func (l *Lexer) lexString(start token.Pos) token.Token {
	l.advance() // opening quote
	from := l.off
	for l.off < len(l.src) {
		switch l.at(0) {
		case '\\':
			l.advance()
			if l.off < len(l.src) {
				l.advance()
			}
		case '"':
			lit := l.src[from:l.off]
			l.advance() // closing quote
			return l.tok(token.STRING, lit, start)
		case '\n':
			return l.tok(token.ILLEGAL, l.src[from:l.off], start) // unterminated
		default:
			l.advance()
		}
	}
	return l.tok(token.ILLEGAL, l.src[from:l.off], start) // unterminated
}

func (l *Lexer) lexOperator(start token.Pos) token.Token {
	c := l.at(0)
	switch c {
	case '=':
		l.advance()
		if l.at(0) == '=' {
			l.advance()
			return l.tok(token.EQ, "", start)
		}
		return l.tok(token.ASSIGN, "", start)
	case '?':
		l.advance()
		if l.at(0) == '=' {
			l.advance()
			return l.tok(token.QASSIGN, "", start)
		}
		return l.tok(token.ILLEGAL, "?", start)
	case '!':
		l.advance()
		if l.at(0) == '=' {
			l.advance()
			return l.tok(token.NEQ, "", start)
		}
		return l.tok(token.NOT, "", start)
	case '<':
		l.advance()
		if l.at(0) == '=' {
			l.advance()
			return l.tok(token.LE, "", start)
		}
		return l.tok(token.LT, "", start)
	case '>':
		l.advance()
		if l.at(0) == '=' {
			l.advance()
			return l.tok(token.GE, "", start)
		}
		return l.tok(token.GT, "", start)
	case '&':
		l.advance()
		if l.at(0) == '&' {
			l.advance()
			return l.tok(token.AND, "", start)
		}
		return l.tok(token.ILLEGAL, "&", start)
	case '|':
		l.advance()
		if l.at(0) == '|' {
			l.advance()
			return l.tok(token.OR, "", start)
		}
		return l.tok(token.ILLEGAL, "|", start)
	case '+':
		l.advance()
		if l.at(0) == '=' {
			l.advance()
			return l.tok(token.PLUSASSIGN, "", start)
		}
		return l.tok(token.PLUS, "", start)
	case '-':
		l.advance()
		return l.tok(token.MINUS, "", start)
	case '*':
		l.advance()
		if l.at(0) == '*' {
			l.advance()
			return l.tok(token.POW, "", start)
		}
		return l.tok(token.STAR, "", start)
	case '/':
		l.advance()
		return l.tok(token.SLASH, "", start)
	case '%':
		l.advance()
		return l.tok(token.PERCENT, "", start)
	case '.':
		l.advance()
		if l.at(0) == '.' {
			l.advance()
			return l.tok(token.DOTDOT, "", start)
		}
		return l.tok(token.DOT, "", start)
	case ':':
		l.advance()
		return l.tok(token.COLON, "", start)
	case ';':
		l.advance()
		return l.tok(token.SEMI, "", start)
	case ',':
		l.advance()
		return l.tok(token.COMMA, "", start)
	case '@':
		l.advance()
		return l.tok(token.AT, "", start)
	case '^':
		l.advance()
		return l.tok(token.CARET, "", start)
	case '(':
		l.advance()
		l.parenDepth++
		return l.tok(token.LPAREN, "", start)
	case ')':
		l.advance()
		if l.parenDepth > 0 {
			l.parenDepth--
		}
		return l.tok(token.RPAREN, "", start)
	case '[':
		l.advance()
		l.parenDepth++
		return l.tok(token.LBRACK, "", start)
	case ']':
		l.advance()
		if l.parenDepth > 0 {
			l.parenDepth--
		}
		return l.tok(token.RBRACK, "", start)
	case '{':
		l.advance()
		if l.at(0) == '{' {
			l.advance()
			l.inBody = true // capture the shell body on the next Next()
			return l.tok(token.LBODY, "{{", start)
		}
		return l.tok(token.LBRACE, "", start)
	case '}':
		// A lone '}' in code mode closes an if/for block. The body terminator
		// '}}' is consumed by lexBody and never reaches here.
		l.advance()
		return l.tok(token.RBRACE, "", start)
	}

	// unknown byte
	l.advance()
	return l.tok(token.ILLEGAL, string(c), start)
}

// lexBody captures a raw shell body: everything after `{{` up to (not including)
// the first line whose trimmed content is exactly `}}`. It returns that text as a
// token.BODY and arranges for the closing token.RBODY to be returned next.
func (l *Lexer) lexBody() token.Token {
	l.inBody = false
	start := l.pos()
	bodyStart := l.off

	for {
		if l.off >= len(l.src) {
			// EOF before a closing `}}`. Return what we have; the parser reports
			// the missing terminator.
			return l.tok(token.BODY, l.src[bodyStart:l.off], start)
		}
		lineStart := l.off
		lineStartPos := l.pos()
		line := l.readLine()
		if strings.TrimSpace(line) == "}}" {
			l.pendingRBody = true
			l.rbodyPos = lineStartPos
			return l.tok(token.BODY, l.src[bodyStart:lineStart], start)
		}
	}
}

// readLine consumes the rest of the current line and its trailing newline,
// returning the line's content without the newline (a trailing '\r' is trimmed).
func (l *Lexer) readLine() string {
	from := l.off
	for l.off < len(l.src) && l.at(0) != '\n' {
		l.advance()
	}
	end := l.off
	if l.off < len(l.src) {
		l.advance() // consume the '\n'
	}
	line := l.src[from:end]
	return strings.TrimSuffix(line, "\r")
}

// Tokenize lexes src to completion, returning all tokens including the final EOF.
// Convenience for tests and tools.
func Tokenize(src, file string) []token.Token {
	l := New(src, file)
	var out []token.Token
	for {
		t := l.Next()
		out = append(out, t)
		if t.Kind == token.EOF {
			return out
		}
	}
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool { return isIdentStart(c) || isDigit(c) }
