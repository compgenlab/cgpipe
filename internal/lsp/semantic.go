package lsp

import (
	"sort"

	"github.com/compgenlab/cgpipe/internal/ast"
	"github.com/compgenlab/cgpipe/internal/lexer"
	"github.com/compgenlab/cgpipe/internal/token"
)

// Semantic token legend. The index of each name is the token type id used in
// the encoded data; it must match the legend advertised at initialize.
var semanticTokenTypes = []string{
	"keyword",
	"string",
	"number",
	"operator",
	"comment",
	"function",
	"variable",
}

const (
	stKeyword = iota
	stString
	stNumber
	stOperator
	stComment
	stFunction
	stVariable
)

// builtinStmts are the statement-leading built-in words. They are plain IDENTs
// to the lexer; we color them as functions only when they begin a statement.
// Built from the canonical ast.BuiltinStmts list.
var builtinStmts = func() map[string]bool {
	m := make(map[string]bool, len(ast.BuiltinStmts))
	for _, n := range ast.BuiltinStmts {
		m[n] = true
	}
	return m
}()

// semTok is an absolute (pre-delta-encoding) semantic token.
type semTok struct {
	line   int // 0-based
	char   int // 0-based UTF-16 offset within the line
	length int // UTF-16 code units
	typ    int // index into semanticTokenTypes
}

// semanticTokens lexes src and returns LSP semantic tokens in the flat,
// delta-encoded [deltaLine, deltaChar, length, tokenType, tokenModifiers]
// form. file is only used for token positions.
func semanticTokens(src, file string) []uint32 {
	toks := lexer.Tokenize(src, file)
	starts := lineStarts(src)

	var items []semTok

	prevSignificant := token.NEWLINE // treat file start as a statement boundary
	for _, t := range toks {
		typ, ok := semanticType(t, atStmtStart(prevSignificant))
		if ok {
			line0 := t.Pos.Line - 1
			char0 := utf16Char(src, starts, line0, t.Pos.Off)
			items = append(items, semTok{
				line:   line0,
				char:   char0,
				length: utf16Len(tokenText(src, t)),
				typ:    typ,
			})
		}
		// Track the previous meaningful token so we can tell when an IDENT
		// begins a statement (and is therefore a candidate builtin).
		switch t.Kind {
		case token.EOF:
			// nothing
		default:
			prevSignificant = t.Kind
		}
	}

	// Comments are dropped by the lexer, so recover them from the source,
	// skipping any '#' that falls inside a string or shell-body token span.
	items = append(items, commentTokens(src, toks, starts)...)

	sort.Slice(items, func(i, j int) bool {
		if items[i].line != items[j].line {
			return items[i].line < items[j].line
		}
		return items[i].char < items[j].char
	})

	return deltaEncode(items)
}

// atStmtStart reports whether a token following prev begins a statement.
func atStmtStart(prev token.Kind) bool {
	switch prev {
	case token.NEWLINE, token.SEMI, token.LBRACE:
		return true
	}
	return false
}

// semanticType maps a token to its semantic type, or ok=false to skip it.
func semanticType(t token.Token, stmtStart bool) (int, bool) {
	switch t.Kind {
	case token.IF, token.ELIF, token.ELSE, token.FOR, token.IN, token.WITH, token.TRUE, token.FALSE:
		return stKeyword, true
	case token.STRING:
		return stString, true
	case token.INT, token.FLOAT:
		return stNumber, true
	case token.IDENT:
		if stmtStart && builtinStmts[t.Lit] {
			return stFunction, true
		}
		return stVariable, true
	case token.ASSIGN, token.QASSIGN, token.PLUSASSIGN,
		token.EQ, token.NEQ, token.LT, token.LE, token.GT, token.GE,
		token.AND, token.OR, token.NOT,
		token.PLUS, token.MINUS, token.STAR, token.SLASH, token.PERCENT, token.POW,
		token.DOTDOT, token.AT, token.CARET:
		return stOperator, true
	default:
		// Structural punctuation (parens, brackets, braces, comma, colon, dot,
		// semicolon) and the body markers are left to the TextMate grammar.
		return 0, false
	}
}

// tokenText returns the source text the token spans, used to measure its length.
func tokenText(src string, t token.Token) string {
	switch t.Kind {
	case token.STRING:
		// Pos.Off is the opening quote; Lit excludes both quotes.
		end := t.Pos.Off + len(t.Lit) + 2
		if end > len(src) {
			end = len(src)
		}
		return src[t.Pos.Off:end]
	case token.IDENT, token.INT, token.FLOAT:
		return t.Lit
	default:
		// Keywords and operators: Kind.String() is the literal spelling
		// ("if", "==", "+=", …); operator tokens carry an empty Lit.
		return t.Kind.String()
	}
}

// commentTokens scans src line by line and emits a comment token for the first
// '#' on each line that is not inside a string literal or a {{ }} body.
func commentTokens(src string, toks []token.Token, starts []int) []semTok {
	type span struct{ lo, hi int }
	var spans []span
	for _, t := range toks {
		switch t.Kind {
		case token.STRING:
			spans = append(spans, span{t.Pos.Off, t.Pos.Off + len(t.Lit) + 2})
		case token.BODY:
			spans = append(spans, span{t.Pos.Off, t.Pos.Off + len(t.Lit)})
		}
	}
	covered := func(off int) bool {
		for _, s := range spans {
			if off >= s.lo && off < s.hi {
				return true
			}
		}
		return false
	}

	var out []semTok
	for line0 := 0; line0 < len(starts); line0++ {
		ls := starts[line0]
		le := len(src)
		if line0+1 < len(starts) {
			le = starts[line0+1] - 1 // exclude the '\n'
		}
		for off := ls; off < le; off++ {
			if src[off] == '#' && !covered(off) {
				char0 := utf16Len(src[ls:off])
				length := utf16Len(trimCR(src[off:le]))
				out = append(out, semTok{line: line0, char: char0, length: length, typ: stComment})
				break
			}
		}
	}
	return out
}

func trimCR(s string) string {
	if n := len(s); n > 0 && s[n-1] == '\r' {
		return s[:n-1]
	}
	return s
}

// deltaEncode converts absolute, position-sorted tokens into the LSP flat
// delta-encoded representation.
func deltaEncode(items []semTok) []uint32 {
	data := make([]uint32, 0, len(items)*5)
	prevLine, prevChar := 0, 0
	for _, it := range items {
		deltaLine := it.line - prevLine
		deltaChar := it.char
		if deltaLine == 0 {
			deltaChar = it.char - prevChar
		}
		data = append(data, uint32(deltaLine), uint32(deltaChar), uint32(it.length), uint32(it.typ), 0)
		prevLine, prevChar = it.line, it.char
	}
	return data
}
