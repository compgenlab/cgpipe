package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// --- unit tests for the building blocks ---

func TestSemanticTokensSimpleAssignment(t *testing.T) {
	// x = 1  -> variable, operator, number on line 0 at chars 0, 2, 4.
	got := semanticTokens("x = 1\n", "t.cgp")
	want := []uint32{
		0, 0, 1, stVariable, 0,
		0, 2, 1, stOperator, 0,
		0, 2, 1, stNumber, 0,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("semantic tokens mismatch:\n got %v\nwant %v", got, want)
	}
}

func TestSemanticTokensBuiltinAndComment(t *testing.T) {
	// Line 0: a comment. Line 1: `print "hi"` -> function + string.
	src := "# note\nprint \"hi\"\n"
	got := semanticTokens(src, "t.cgp")
	want := []uint32{
		0, 0, 6, stComment, 0, // "# note" = 6 UTF-16 units
		1, 0, 5, stFunction, 0, // print
		0, 6, 4, stString, 0, // "hi" including quotes = 4
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("semantic tokens mismatch:\n got %v\nwant %v", got, want)
	}
}

func TestCommentInsideStringNotFlagged(t *testing.T) {
	// The '#' is inside a string literal, so no comment token should appear;
	// only the string token (plus the assignment) should be emitted.
	src := "x = \"a#b\"\n"
	got := semanticTokens(src, "t.cgp")
	for i := 3; i < len(got); i += 5 {
		if got[i] == stComment {
			t.Fatalf("a '#' inside a string was treated as a comment: %v", got)
		}
	}
}

func TestDiagnosticsCleanVsError(t *testing.T) {
	if d := diagnostics("x = 1\n", "t.cgp"); len(d) != 0 {
		t.Fatalf("expected no diagnostics for valid source, got %v", d)
	}
	d := diagnostics("x = \n", "t.cgp") // assignment with no value -> parse error
	if len(d) != 1 {
		t.Fatalf("expected one diagnostic, got %d: %v", len(d), d)
	}
	if d[0].Severity != 1 || d[0].Source != "cgpipe" {
		t.Fatalf("unexpected diagnostic shape: %+v", d[0])
	}
	if d[0].Range.Start.Line != 0 {
		t.Fatalf("expected error on line 0, got %+v", d[0].Range)
	}
}

func TestUTF16Char(t *testing.T) {
	// "é" is 2 bytes in UTF-8 but 1 UTF-16 unit; "𝄞" is 4 bytes, 2 units.
	src := "é𝄞x"
	starts := lineStarts(src)
	// byte offset of 'x' is 2 (é) + 4 (𝄞) = 6
	if c := utf16Char(src, starts, 0, 6); c != 3 {
		t.Fatalf("utf16Char = %d, want 3", c)
	}
}

// --- frame-driven integration test ---

// frame wraps a JSON-RPC payload in the LSP Content-Length envelope.
func frame(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(b), b)
}

func TestServerHandshakeAndSemanticTokens(t *testing.T) {
	uri := "file:///t.cgp"
	text := "x = 1\n"

	var in strings.Builder
	in.WriteString(frame(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{},
	}))
	in.WriteString(frame(t, map[string]any{"jsonrpc": "2.0", "method": "initialized", "params": map[string]any{}}))
	in.WriteString(frame(t, map[string]any{
		"jsonrpc": "2.0", "method": "textDocument/didOpen",
		"params": map[string]any{"textDocument": map[string]any{"uri": uri, "text": text}},
	}))
	in.WriteString(frame(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "textDocument/semanticTokens/full",
		"params": map[string]any{"textDocument": map[string]any{"uri": uri}},
	}))
	in.WriteString(frame(t, map[string]any{"jsonrpc": "2.0", "id": 3, "method": "shutdown"}))
	in.WriteString(frame(t, map[string]any{"jsonrpc": "2.0", "method": "exit"}))

	var out bytes.Buffer
	if err := Run(strings.NewReader(in.String()), &out); err != nil {
		t.Fatalf("Run: %v", err)
	}

	msgs := readAllFrames(t, out.Bytes())

	// initialize result advertises the semantic-tokens legend.
	initRes := findByID(msgs, float64(1))
	if initRes == nil {
		t.Fatal("no initialize response")
	}
	caps := initRes["result"].(map[string]any)["capabilities"].(map[string]any)
	legend := caps["semanticTokensProvider"].(map[string]any)["legend"].(map[string]any)
	types := legend["tokenTypes"].([]any)
	if len(types) != len(semanticTokenTypes) || types[stVariable].(string) != "variable" {
		t.Fatalf("unexpected legend: %v", types)
	}

	// semanticTokens/full result matches the direct computation.
	stRes := findByID(msgs, float64(2))
	if stRes == nil {
		t.Fatal("no semanticTokens response")
	}
	dataAny := stRes["result"].(map[string]any)["data"].([]any)
	var data []uint32
	for _, v := range dataAny {
		data = append(data, uint32(v.(float64)))
	}
	want := semanticTokens(text, uri)
	if !reflect.DeepEqual(data, want) {
		t.Fatalf("semanticTokens over the wire = %v, want %v", data, want)
	}

	// didOpen should have produced a publishDiagnostics notification (empty for
	// valid source).
	if note := findByMethod(msgs, "textDocument/publishDiagnostics"); note == nil {
		t.Fatal("expected a publishDiagnostics notification")
	}
}

// TestServerDocumentLifecycleHover drives didOpen → didChange → didClose and
// hovers after each, confirming the server tracks document state: hover sees the
// changed text, and after close the document is gone (null hover).
func TestServerDocumentLifecycleHover(t *testing.T) {
	uri := "file:///t.cgp"
	hoverReq := func(id int) map[string]any {
		return map[string]any{
			"jsonrpc": "2.0", "id": id, "method": "textDocument/hover",
			"params": map[string]any{
				"textDocument": map[string]any{"uri": uri},
				"position":     map[string]any{"line": 0, "character": 0},
			},
		}
	}

	var in strings.Builder
	in.WriteString(frame(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{}}))
	// Open with "y = 1": hover at 0:0 is a plain variable -> null.
	in.WriteString(frame(t, map[string]any{
		"jsonrpc": "2.0", "method": "textDocument/didOpen",
		"params": map[string]any{"textDocument": map[string]any{"uri": uri, "text": "y = 1\n"}},
	}))
	in.WriteString(frame(t, hoverReq(2)))
	// Change to "print y": hover at 0:0 now lands on the built-in `print`.
	in.WriteString(frame(t, map[string]any{
		"jsonrpc": "2.0", "method": "textDocument/didChange",
		"params": map[string]any{
			"textDocument":   map[string]any{"uri": uri},
			"contentChanges": []any{map[string]any{"text": "print y\n"}},
		},
	}))
	in.WriteString(frame(t, hoverReq(3)))
	// Close: the document is forgotten, so hover is null again.
	in.WriteString(frame(t, map[string]any{
		"jsonrpc": "2.0", "method": "textDocument/didClose",
		"params": map[string]any{"textDocument": map[string]any{"uri": uri}},
	}))
	in.WriteString(frame(t, hoverReq(4)))
	in.WriteString(frame(t, map[string]any{"jsonrpc": "2.0", "method": "exit"}))

	var out bytes.Buffer
	if err := Run(strings.NewReader(in.String()), &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	msgs := readAllFrames(t, out.Bytes())

	if m := findByID(msgs, float64(2)); m == nil || m["result"] != nil {
		t.Fatalf("hover #2 (over a variable) should be null, got %+v", m)
	}
	m3 := findByID(msgs, float64(3))
	if m3 == nil || m3["result"] == nil {
		t.Fatalf("hover #3 (over `print` after didChange) should be non-null, got %+v", m3)
	}
	contents := m3["result"].(map[string]any)["contents"].(map[string]any)
	if v, _ := contents["value"].(string); !strings.Contains(v, "print") {
		t.Errorf("hover #3 value = %q, want it to mention `print`", v)
	}
	if m := findByID(msgs, float64(4)); m == nil || m["result"] != nil {
		t.Fatalf("hover #4 (after didClose) should be null, got %+v", m)
	}
}

func readAllFrames(t *testing.T, b []byte) []map[string]any {
	t.Helper()
	r := bufio.NewReader(bytes.NewReader(b))
	var out []map[string]any
	for {
		data, err := readMessage(r)
		if err != nil {
			break
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("bad frame: %v", err)
		}
		out = append(out, m)
	}
	return out
}

func findByID(msgs []map[string]any, id any) map[string]any {
	for _, m := range msgs {
		if m["id"] == id {
			return m
		}
	}
	return nil
}

func findByMethod(msgs []map[string]any, method string) map[string]any {
	for _, m := range msgs {
		if m["method"] == method {
			return m
		}
	}
	return nil
}
