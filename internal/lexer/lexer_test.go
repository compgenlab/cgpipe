package lexer

import (
	"testing"

	"github.com/compgenlab/cgpipe/internal/token"
)

// kinds lexes src and returns the token kinds (dropping the trailing EOF).
func kinds(src string) []token.Kind {
	toks := Tokenize(src, "test.cgp")
	out := make([]token.Kind, 0, len(toks))
	for _, t := range toks[:len(toks)-1] { // drop EOF
		out = append(out, t.Kind)
	}
	return out
}

func eq(t *testing.T, got, want []token.Kind) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("kind count: got %d %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("kind[%d]: got %s, want %s (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestAssignment(t *testing.T) {
	eq(t, kinds(`x = 8`), []token.Kind{token.IDENT, token.ASSIGN, token.INT})
}

func TestAssignmentForms(t *testing.T) {
	eq(t, kinds(`a ?= 1
b += 2`), []token.Kind{
		token.IDENT, token.QASSIGN, token.INT, token.NEWLINE,
		token.IDENT, token.PLUSASSIGN, token.INT,
	})
}

func TestOperators(t *testing.T) {
	eq(t, kinds(`== != <= >= < > && || ! ** + - * / % .. .`), []token.Kind{
		token.EQ, token.NEQ, token.LE, token.GE, token.LT, token.GT,
		token.AND, token.OR, token.NOT, token.POW,
		token.PLUS, token.MINUS, token.STAR, token.SLASH, token.PERCENT,
		token.DOTDOT, token.DOT,
	})
}

func TestStructural(t *testing.T) {
	eq(t, kinds(`: ; , @ ( ) [ ] { }`), []token.Kind{
		token.COLON, token.SEMI, token.COMMA, token.AT,
		token.LPAREN, token.RPAREN, token.LBRACK, token.RBRACK,
		token.LBRACE, token.RBRACE,
	})
}

func TestKeywordsAndIdents(t *testing.T) {
	eq(t, kinds(`if elif else for in with true false foo`), []token.Kind{
		token.IF, token.ELIF, token.ELSE, token.FOR, token.IN, token.WITH,
		token.TRUE, token.FALSE, token.IDENT,
	})
}

func TestNumbers(t *testing.T) {
	toks := Tokenize(`1 0.5 1..10`, "t")
	want := []struct {
		k   token.Kind
		lit string
	}{
		{token.INT, "1"},
		{token.FLOAT, "0.5"},
		{token.INT, "1"},
		{token.DOTDOT, ""},
		{token.INT, "10"},
		{token.EOF, ""},
	}
	if len(toks) != len(want) {
		t.Fatalf("got %d tokens %v, want %d", len(toks), toks, len(want))
	}
	for i, w := range want {
		if toks[i].Kind != w.k || (w.lit != "" && toks[i].Lit != w.lit) {
			t.Fatalf("tok[%d]: got %s, want %s(%q)", i, toks[i], w.k, w.lit)
		}
	}
}

func TestStringRawInner(t *testing.T) {
	toks := Tokenize(`x = "a\"b ${y}"`, "t")
	// IDENT ASSIGN STRING EOF
	if toks[2].Kind != token.STRING {
		t.Fatalf("tok[2] = %s, want STRING", toks[2])
	}
	if got, want := toks[2].Lit, `a\"b ${y}`; got != want {
		t.Fatalf("string lit = %q, want %q (escapes + interpolation must be preserved raw)", got, want)
	}
}

func TestComments(t *testing.T) {
	// shebang + comment lines are skipped; only the assignment survives
	eq(t, kinds("#!/usr/bin/env cgpipe\n# a comment\nx = 1 # trailing\n"), []token.Kind{
		token.NEWLINE, token.NEWLINE,
		token.IDENT, token.ASSIGN, token.INT, token.NEWLINE,
	})
}

// A newline inside ( ) or [ ] is insignificant (implicit line continuation), so
// an expression may span lines; newlines inside { } still separate statements.
func TestImplicitLineContinuation(t *testing.T) {
	// a list literal broken across lines lexes as one statement (no NEWLINE inside)
	eq(t, kinds("x = [\n  1,\n  2,\n]\n"), []token.Kind{
		token.IDENT, token.ASSIGN,
		token.LBRACK, token.INT, token.COMMA, token.INT, token.COMMA, token.RBRACK,
		token.NEWLINE,
	})
	// a parenthesized expression broken across lines
	eq(t, kinds("y = (\n  2 +\n  3\n)\n"), []token.Kind{
		token.IDENT, token.ASSIGN,
		token.LPAREN, token.INT, token.PLUS, token.INT, token.RPAREN,
		token.NEWLINE,
	})
	// braces are NOT continued: statements inside { } stay newline-separated
	eq(t, kinds("if c {\n  x = 1\n}\n"), []token.Kind{
		token.IF, token.IDENT, token.LBRACE, token.NEWLINE,
		token.IDENT, token.ASSIGN, token.INT, token.NEWLINE,
		token.RBRACE, token.NEWLINE,
	})
}

func TestControlFlowBraces(t *testing.T) {
	eq(t, kinds(`if !bam { exit }`), []token.Kind{
		token.IF, token.NOT, token.IDENT, token.LBRACE, token.IDENT, token.RBRACE,
	})
}

// --- the headline behavior: the {{ }} shell-body mode flip ---

func TestBodyCapture(t *testing.T) {
	// note: `in` is a keyword, so use a non-keyword input name here.
	toks := Tokenize("out: src {{\n  echo hi\n}}\n", "t")
	wantKinds := []token.Kind{
		token.IDENT, token.COLON, token.IDENT,
		token.LBODY, token.BODY, token.RBODY,
		token.EOF,
	}
	if len(toks) != len(wantKinds) {
		t.Fatalf("got %d tokens %v, want %d", len(toks), toks, len(wantKinds))
	}
	for i, k := range wantKinds {
		if toks[i].Kind != k {
			t.Fatalf("tok[%d] = %s, want %s", i, toks[i], k)
		}
	}
	if got, want := toks[4].Lit, "\n  echo hi\n"; got != want {
		t.Fatalf("body lit = %q, want %q", got, want)
	}
}

func TestEmptyBody(t *testing.T) {
	toks := Tokenize("x {{\n}}", "t")
	// IDENT LBODY BODY("\n") RBODY EOF — the newline after `{{` is captured raw.
	if toks[2].Kind != token.BODY || toks[2].Lit != "\n" {
		t.Fatalf("tok[2] = %s, want BODY(%q)", toks[2], "\n")
	}
	if toks[3].Kind != token.RBODY {
		t.Fatalf("tok[3] = %s, want RBODY", toks[3])
	}
}

// A body's inner braces, %-control lines, and -- separator are captured raw and
// must NOT terminate the body or be tokenized as cgpipe.
func TestBodyKeepsShellAndControlRaw(t *testing.T) {
	body := "  name = \"j\"\n  --\n  awk '{print $1}'\n% for o in xs {\n  rm ${o}\n% }\n"
	src := "t: a {{\n" + body + "}}\n"
	toks := Tokenize(src, "t")
	var b *token.Token
	for i := range toks {
		if toks[i].Kind == token.BODY {
			b = &toks[i]
			break
		}
	}
	if b == nil {
		t.Fatalf("no BODY token in %v", toks)
	}
	if b.Lit != "\n"+body {
		t.Fatalf("body lit = %q, want %q", b.Lit, "\n"+body)
	}
	// exactly one LBODY and one RBODY (inner braces didn't create extras)
	var nL, nR int
	for _, tk := range toks {
		switch tk.Kind {
		case token.LBODY:
			nL++
		case token.RBODY:
			nR++
		}
	}
	if nL != 1 || nR != 1 {
		t.Fatalf("got %d LBODY / %d RBODY, want 1/1", nL, nR)
	}
}

func TestTwoTargetsTokenizeIndependently(t *testing.T) {
	src := "hello.txt: {{\n  echo hello > ${output}\n}}\n\nworld.txt: hello.txt {{\n  cat ${input} > ${output}\n}}\n"
	toks := Tokenize(src, "t")
	var bodies int
	for _, tk := range toks {
		if tk.Kind == token.BODY {
			bodies++
		}
	}
	if bodies != 2 {
		t.Fatalf("got %d BODY tokens, want 2 (full: %v)", bodies, toks)
	}
	if toks[len(toks)-1].Kind != token.EOF {
		t.Fatalf("last token = %s, want EOF", toks[len(toks)-1])
	}
}
