package spectest

import (
	"path/filepath"
	"testing"
)

// §9.1 type() on any value returns the type name (covered per-type in
// types_test.go); here we confirm the int/float/bool path.
func TestScalarType(t *testing.T) {
	for _, c := range []struct{ expr, want string }{
		{"(5).type()", "int"},
		{"(0.5).type()", "float"},
		{"true.type()", "bool"},
	} {
		if got := printsExpr(t, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// §9.2 string methods.
func TestStringMethods(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`"a,b,c".split(",").length()`, "3"},
		{`"a b c".split().length()`, "5"}, // omitted delim ⇒ characters? "a b c" → 5 chars
		{`"x.bam".sub("\\.bam$", "")`, "x"},
		{`"abc".upper()`, "ABC"},
		{`"ABC".lower()`, "abc"},
		{`"hello".length()`, "5"},
		{`"hello".contains("ell")`, "true"},
		{`"hello".contains("zzz")`, "false"},
		{`",".join(["a", "b", "c"])`, "a,b,c"},
		{`"/a/b/c.bam".basename()`, "c.bam"},
		{`"/a/b/c.bam".dirname()`, "/a/b"},
	}
	for _, c := range cases {
		if got := printsExpr(t, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// §9.2 abspath resolves against the current directory.
func TestStringAbspath(t *testing.T) {
	dir := chdirTmp(t)
	want, _ := filepath.Abs(filepath.Join(dir, "x.txt"))
	if got := printsExpr(t, `"x.txt".abspath()`); got != want {
		t.Errorf("abspath = %q, want %q", got, want)
	}
}

// §9.2 filesystem tests at evaluation time: exists / isfile / isdir.
func TestStringFilesystemTests(t *testing.T) {
	chdirTmp(t)
	writeFile(t, "real.txt", "x")
	cases := []struct{ expr, want string }{
		{`"real.txt".exists()`, "true"},
		{`"real.txt".isfile()`, "true"},
		{`"real.txt".isdir()`, "false"},
		{`"nope.txt".exists()`, "false"},
		{`".".isdir()`, "true"},
	}
	for _, c := range cases {
		if got := printsExpr(t, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// §9.3 list methods.
func TestListMethods(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`[1, 2, 3].length()`, "3"},
		{`[1, 2, 3].contains(2)`, "true"},
		{`[1, 2, 3].contains(9)`, "false"},
		{`[1, 2, 3].join("-")`, "1-2-3"},
	}
	for _, c := range cases {
		if got := printsExpr(t, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// §9.4 range methods: length from the bounds; iterates/passes as a list.
func TestRangeMethods(t *testing.T) {
	if got := printsExpr(t, "(1..5).length()"); got != "5" {
		t.Errorf("range length = %q, want 5", got)
	}
	if got := printsExpr(t, "(1..5).type()"); got != "range" {
		t.Errorf("range type = %q, want range (not list)", got)
	}
}

// §9.2 split with no delimiter splits into characters; with a delimiter splits
// on it.
func TestSplitWithAndWithoutDelim(t *testing.T) {
	if got := printsExpr(t, `"abc".split().length()`); got != "3" {
		t.Errorf("split() into chars: %q, want 3", got)
	}
	if got := printsExpr(t, `"a:b:c".split(":").length()`); got != "3" {
		t.Errorf("split(\":\"): %q, want 3", got)
	}
}

// §9 Methods chain on the result of the previous call.
func TestChainedMethods(t *testing.T) {
	if got := printsExpr(t, `"/p/CHR1.BAM".basename().lower().sub("\\.bam$", "")`); got != "chr1" {
		t.Errorf("chained basename→lower→sub = %q, want chr1", got)
	}
	if got := printsExpr(t, `"a,b,c".split(",")[1]`); got != "b" {
		t.Errorf("split then index = %q, want b", got)
	}
}

// §9.3 The receiver-flipped join (separator.join(list)) equals list.join(sep).
func TestReceiverFlippedJoin(t *testing.T) {
	if got := printsExpr(t, `",".join(["a", "b"]) == ["a", "b"].join(",")`); got != "true" {
		t.Errorf("receiver-flipped join mismatch: %q", got)
	}
}

// §9.5 An unknown method throws.
func TestUnknownMethodErrors(t *testing.T) {
	wantErr(t, "unknown method", `print (5).frobnicate()`)
}
