package lsp

import (
	"strings"
	"testing"
)

func hoverAtPos(src string, line, char int) *hoverResult {
	return hoverAt(src, "test.cgp", Position{Line: line, Character: char})
}

func TestHoverKeyword(t *testing.T) {
	h := hoverAtPos("if x {\n}", 0, 0) // cursor on "if"
	if h == nil {
		t.Fatal("expected hover for keyword `if`, got nil")
	}
	if h.Contents.Kind != "markdown" || h.Contents.Value == "" {
		t.Errorf("hover contents = %+v, want non-empty markdown", h.Contents)
	}
	if h.Range == nil || h.Range.Start.Line != 0 || h.Range.Start.Character != 0 {
		t.Errorf("hover range = %+v, want start at 0:0", h.Range)
	}
	if h.Range.End.Character != 2 { // "if" is two UTF-16 units
		t.Errorf("hover range end char = %d, want 2", h.Range.End.Character)
	}
}

func TestHoverBuiltin(t *testing.T) {
	h := hoverAtPos("print x", 0, 0) // cursor on "print"
	if h == nil || !strings.Contains(h.Contents.Value, "print") {
		t.Fatalf("expected hover doc for built-in `print`, got %+v", h)
	}
}

func TestHoverReservedTarget(t *testing.T) {
	h := hoverAtPos("@default: out.bam", 0, 1) // cursor on "default" (after @)
	if h == nil || !strings.Contains(h.Contents.Value, "@default") {
		t.Fatalf("expected hover doc for reserved target @default, got %+v", h)
	}
}

func TestHoverWithKeyword(t *testing.T) {
	h := hoverAtPos("for s in xs with i {\n}", 0, 12) // cursor on "with"
	if h == nil || !strings.Contains(h.Contents.Value, "with") {
		t.Fatalf("expected hover doc for keyword `with`, got %+v", h)
	}
}

func TestHoverVarDeclaration(t *testing.T) {
	h := hoverAtPos("var last", 0, 0) // cursor on "var"
	if h == nil || !strings.Contains(h.Contents.Value, "var") {
		t.Fatalf("expected hover doc for `var`, got %+v", h)
	}
}

func TestHoverPlainVariableIsNil(t *testing.T) {
	if h := hoverAtPos("x = 1", 0, 0); h != nil { // "x" is a user variable, not documented
		t.Errorf("expected nil hover for a plain variable, got %+v", h)
	}
}

func TestHoverWhitespaceIsNil(t *testing.T) {
	if h := hoverAtPos("x = 1", 0, 1); h != nil { // cursor on the space between x and =
		t.Errorf("expected nil hover over whitespace, got %+v", h)
	}
}
