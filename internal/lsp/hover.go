package lsp

import (
	"github.com/compgenlab/cgpipe/internal/lexer"
	"github.com/compgenlab/cgpipe/internal/token"
)

// hoverAt returns hover documentation for the keyword, built-in, or reserved
// target under the cursor, or nil when there is nothing to describe.
func hoverAt(src, file string, pos Position) *hoverResult {
	toks := lexer.Tokenize(src, file)
	starts := lineStarts(src)
	off := offsetForPosition(src, starts, pos)

	idx := tokenIndexAt(src, toks, off)
	if idx < 0 {
		return nil
	}
	t := toks[idx]

	var doc string
	switch t.Kind {
	case token.IF, token.ELIF, token.ELSE, token.FOR, token.IN, token.WITH, token.TRUE, token.FALSE:
		doc = keywordDocs[t.Kind.String()]
	case token.IDENT:
		// A reserved target reads as AT followed by the name (e.g. @default).
		if idx > 0 && toks[idx-1].Kind == token.AT {
			doc = reservedTargetDocs[t.Lit]
		}
		if doc == "" {
			doc = builtinDocs[t.Lit]
		}
	}
	if doc == "" {
		return nil
	}

	line0 := t.Pos.Line - 1
	char0 := utf16Char(src, starts, line0, t.Pos.Off)
	length := utf16Len(tokenText(src, t))
	return &hoverResult{
		Contents: markupContent{Kind: "markdown", Value: doc},
		Range: &Range{
			Start: Position{Line: line0, Character: char0},
			End:   Position{Line: line0, Character: char0 + length},
		},
	}
}

// tokenIndexAt returns the index of the token whose source span contains the
// byte offset off, ignoring newlines, EOF, and shell-body markers, or -1.
func tokenIndexAt(src string, toks []token.Token, off int) int {
	for i, t := range toks {
		switch t.Kind {
		case token.NEWLINE, token.EOF, token.LBODY, token.RBODY, token.BODY:
			continue
		}
		start := t.Pos.Off
		end := start + len(tokenText(src, t))
		if off >= start && off < end {
			return i
		}
	}
	return -1
}
