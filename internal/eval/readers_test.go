package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/compgen-io/cgp/internal/parser"
)

func tmpFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// evalErr parses and evaluates src, returning whichever error (parse or eval)
// occurs — for asserting on rejection cases.
func evalErr(t *testing.T, src string) error {
	t.Helper()
	e, err := parser.ParseExpr(src)
	if err != nil {
		return err
	}
	_, err = testInterp(nil).eval(e)
	return err
}

// ---- map type ----

func TestMapLiteralAndAccess(t *testing.T) {
	if got := stringify(evalExprStr(t, `{"a": 1, "b": 2}["a"]`, nil)); got != "1" {
		t.Errorf(`map["a"] = %q, want 1`, got)
	}
	// int index is positional into the ordered keys
	if got := stringify(evalExprStr(t, `{"a": 1, "b": 2}[1]`, nil)); got != "2" {
		t.Errorf("map[1] = %q, want 2 (second key)", got)
	}
	if got := stringify(evalExprStr(t, `{"a": 1, "b": 2}[-1]`, nil)); got != "2" {
		t.Errorf("map[-1] = %q, want 2", got)
	}
	// later key wins on duplicate
	if got := stringify(evalExprStr(t, `{"a": 1, "a": 9}["a"]`, nil)); got != "9" {
		t.Errorf("dup key = %q, want 9", got)
	}
	// missing key -> unset (empty string)
	if _, ok := evalExprStr(t, `{"a": 1}["z"]`, nil).(UnsetVal); !ok {
		t.Error("missing key should be unset")
	}
}

func TestMapMethods(t *testing.T) {
	cases := map[string]string{
		`{"a": 1, "b": 2}.keys()`:   "a b",
		`{"a": 1, "b": 2}.values()`: "1 2",
		`{"a": 1, "b": 2}.length()`: "2",
		`{"a": 1, "b": 2}.type()`:   "map",
		`{"a": 1}.has("a")`:         "true",
		`{"a": 1}.has("z")`:         "false",
		`{"a": 5}.get("a")`:         "5",
		`{"a": 5, "b": 6}.get(1)`:   "6",
	}
	for src, want := range cases {
		if got := stringify(evalExprStr(t, src, nil)); got != want {
			t.Errorf("%s = %q, want %q", src, got, want)
		}
	}
}

func TestMapGetItemsAndStringify(t *testing.T) {
	// items() -> list of [key, value] pairs
	if got := stringify(evalExprStr(t, `{"a": 1, "b": 2}.items()`, nil)); got != "a 1 b 2" {
		t.Errorf("items() = %q, want 'a 1 b 2'", got)
	}
	// get with a default for a missing key / out-of-range position
	if got := stringify(evalExprStr(t, `{"a": 1}.get("z", "fallback")`, nil)); got != "fallback" {
		t.Errorf("get default = %q", got)
	}
	if got := stringify(evalExprStr(t, `{"a": 1}.get(9, "x")`, nil)); got != "x" {
		t.Errorf("get out-of-range default = %q", got)
	}
	if _, ok := evalExprStr(t, `{"a": 1}.get("z")`, nil).(UnsetVal); !ok {
		t.Error("get missing without default should be unset")
	}
	// stringify of a whole map is k=v pairs in order
	if got := stringify(evalExprStr(t, `{"a": 1, "b": 2}`, nil)); got != "a=1 b=2" {
		t.Errorf("map stringify = %q", got)
	}
	// empty map is falsy, non-empty is truthy
	if truthy(newMap()) {
		t.Error("empty map should be falsy")
	}
	if got := evalExprStr(t, `{"a": 1}`, nil); !truthy(got) {
		t.Error("non-empty map should be truthy")
	}
}

func TestMapIndexAssignOpsAndErrors(t *testing.T) {
	// ?= only sets a missing key
	_, out := runSrc(t, `m = {"a": 1}
m["a"] ?= 99
m["b"] ?= 2
print m["a"], m["b"]`, nil)
	if out != "1 2\n" {
		t.Errorf("?= index assign = %q, want '1 2'", out)
	}
	// int (positional) read out of range errors
	if err := evalErr(t, `{"a": 1}[5]`); err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Errorf("map int OOB: got %v", err)
	}
	// index-assign with a non-string key errors
	if _, _, err := runSrcErr(t, `m = {}
m[1] = "x"`); err == nil || !strings.Contains(err.Error(), "key must be a string") {
		t.Errorf("non-string assign key: got %v", err)
	}
	// index-assign into a non-map scalar errors
	if _, _, err := runSrcErr(t, `s = "x"
s["k"] = 1`); err == nil || !strings.Contains(err.Error(), "cannot index-assign") {
		t.Errorf("index-assign into string: got %v", err)
	}
}

func TestMapKeyMustBeString(t *testing.T) {
	if err := evalErr(t, `{1: "a"}`); err == nil || !strings.Contains(err.Error(), "map key must be a string") {
		t.Errorf("non-string literal key: got %v", err)
	}
	if err := evalErr(t, `{"a": 1}[true]`); err == nil || !strings.Contains(err.Error(), "map index") {
		t.Errorf("bad index type: got %v", err)
	}
}

func TestMapIndexAssignAndVivify(t *testing.T) {
	_, out := runSrc(t, `g = {}
g["x"] += "a"
g["x"] += "b"
g["y"] += "c"
print g["x"], "|", g["y"]
h["k"] = 5
print h["k"]
print g.keys()`, nil)
	want := "a b | c\n5\nx y\n"
	if out != want {
		t.Errorf("index-assign output = %q, want %q", out, want)
	}
}

// TestMapIsReferenceType pins the reference semantics: assigning a map to another
// variable aliases the same backing data, so a later mutation through one name is
// fully visible (values AND key order/length) through the other.
func TestMapIsReferenceType(t *testing.T) {
	_, out := runSrc(t, `m = {"a": 1}
n = m
m["b"] = 2
print n["b"], n.length(), n.keys()`, nil)
	if out != "2 2 a b\n" {
		t.Errorf("map alias = %q, want %q", out, "2 2 a b\n")
	}
}

func TestMapIterationKeys(t *testing.T) {
	_, out := runSrc(t, `m = {"a": 1, "b": 2}
for k in m with i {
    print i, k, m[k]
}`, nil)
	if out != "1 a 1\n2 b 2\n" {
		t.Errorf("map iteration = %q", out)
	}
}

// ---- file object + readers ----

func TestOpenAndReadTSV(t *testing.T) {
	p := tmpFile(t, "s.tsv", "# a comment\nsample\tinput\tn\ns1\tdata/s1.bam\t3\ns2\tdata/s2.bam\t5\n")
	q := func(s string) string { return `open("` + p + `").` + s }

	if got := stringify(evalExprStr(t, q(`read_tsv().length()`), nil)); got != "2" {
		t.Errorf("rows = %q, want 2", got)
	}
	// field by name, by position, and typed-value chaining
	if got := stringify(evalExprStr(t, q(`read_tsv()[0]["sample"]`), nil)); got != "s1" {
		t.Errorf("[0][sample] = %q", got)
	}
	if got := stringify(evalExprStr(t, q(`read_tsv()[0][0]`), nil)); got != "s1" {
		t.Errorf("[0][0] = %q", got)
	}
	if got := stringify(evalExprStr(t, q(`read_tsv()[0]["input"].basename()`), nil)); got != "s1.bam" {
		t.Errorf("basename chain = %q, want s1.bam", got)
	}
	// auto-typed numeric field keeps int type
	if got := evalExprStr(t, q(`read_tsv()[0]["n"]`), nil); got.typeName() != "int" {
		t.Errorf("n type = %s, want int", got.typeName())
	}
}

func TestReadTSVOptions(t *testing.T) {
	// raw=true keeps the numeric cell a string
	p := tmpFile(t, "s.tsv", "id\tn\n01\t7\n")
	if got := evalExprStr(t, `open("`+p+`").read_tsv(raw=true)[0]["id"]`, nil); got.typeName() != "string" {
		t.Errorf("raw id type = %s, want string", got.typeName())
	}
	if got := evalExprStr(t, `open("`+p+`").read_tsv()[0]["id"]`, nil); got.typeName() != "int" {
		t.Errorf("typed id = %s, want int", got.typeName())
	}
	// header=false -> positional c0,c1 keys
	p2 := tmpFile(t, "h.tsv", "a\tb\nc\td\n")
	if got := stringify(evalExprStr(t, `open("`+p2+`").read_tsv(header=false)[0]["c0"]`, nil)); got != "a" {
		t.Errorf("headerless c0 = %q, want a", got)
	}
	// custom separator
	p3 := tmpFile(t, "p.txt", "x|y\n1|2\n")
	if got := stringify(evalExprStr(t, `open("`+p3+`").read_tsv(sep="|")[0]["y"]`, nil)); got != "2" {
		t.Errorf("sep override = %q, want 2", got)
	}
}

func TestReadTSVExplicitTabSep(t *testing.T) {
	// `sep="\t"` now resolves to a real tab (C-style escape), so an explicit
	// tab separator parses correctly (previously "\t" was the letters \t).
	p := tmpFile(t, "s.tsv", "a\tb\n1\t2\n")
	if got := stringify(evalExprStr(t, `open("`+p+`").read_tsv(sep="\t")[0]["b"]`, nil)); got != "2" {
		t.Errorf(`sep="\t" = %q, want 2`, got)
	}
}

func TestReadTSVEdgeCases(t *testing.T) {
	// ragged row: a short row pads missing trailing columns with ""
	p := tmpFile(t, "r.tsv", "a\tb\tc\n1\t2\n")
	if got := evalExprStr(t, `open("`+p+`").read_tsv()[0]["c"]`, nil); got != StrVal("") {
		t.Errorf("ragged short row [c] = %q, want empty", stringify(got))
	}
	// skip drops leading lines before the header
	p2 := tmpFile(t, "s.tsv", "junk line\na\tb\n1\t2\n")
	if got := stringify(evalExprStr(t, `open("`+p2+`").read_tsv(skip=1)[0]["a"]`, nil)); got != "1" {
		t.Errorf("skip=1 [a] = %q, want 1", got)
	}
	// missing file is an error (open is lazy; the read surfaces it)
	if err := evalErr(t, `open("/no/such/file.tsv").read_tsv()`); err == nil {
		t.Error("read_tsv on a missing file should error")
	}
}

func TestReadCSVAndJSON(t *testing.T) {
	pc := tmpFile(t, "s.csv", "sample,greeting\nP001,\"hi, there\"\n")
	if got := stringify(evalExprStr(t, `open("`+pc+`").read_csv()[0]["greeting"]`, nil)); got != "hi, there" {
		t.Errorf("quoted csv = %q", got)
	}
	pj := tmpFile(t, "s.json", `[{"sample":"a","n":3},{"sample":"b","n":5}]`)
	if got := stringify(evalExprStr(t, `open("`+pj+`").read_json()[1]["sample"]`, nil)); got != "b" {
		t.Errorf("json = %q, want b", got)
	}
	if got := evalExprStr(t, `open("`+pj+`").read_json()[0]["n"]`, nil); got.typeName() != "int" {
		t.Errorf("json n type = %s, want int", got.typeName())
	}
	// non-array JSON is an error
	po := tmpFile(t, "o.json", `{"not":"an array"}`)
	if err := evalErr(t, `open("`+po+`").read_json()`); err == nil || !strings.Contains(err.Error(), "array of objects") {
		t.Errorf("non-array json: got %v", err)
	}
}

func TestReadLinesAndWhole(t *testing.T) {
	p := tmpFile(t, "l.txt", "one\n# skip me\ntwo\n\nthree\n")
	if got := stringify(evalExprStr(t, `open("`+p+`").read_lines(comment="#")`, nil)); got != "one two  three" {
		t.Errorf("read_lines = %q", got)
	}
	if got := stringify(evalExprStr(t, `open("`+p+`").read_lines(comment="#", blank=false).length()`, nil)); got != "3" {
		t.Errorf("read_lines blank=false length = %q, want 3", got)
	}
	if got := stringify(evalExprStr(t, `open("`+p+`").read()`, nil)); !strings.HasPrefix(got, "one\n") {
		t.Errorf("read() = %q", got)
	}
}

func TestOpenExists(t *testing.T) {
	p := tmpFile(t, "x", "hi")
	if got := stringify(evalExprStr(t, `open("`+p+`").exists()`, nil)); got != "true" {
		t.Errorf("exists = %q", got)
	}
	if got := stringify(evalExprStr(t, `open("/no/such/file").exists()`, nil)); got != "false" {
		t.Errorf("missing exists = %q", got)
	}
}

// ---- call grammar: kwargs + builtins ----

func TestUnknownKwargRejected(t *testing.T) {
	if err := evalErr(t, `open("/x").read_tsv(bogus=1)`); err == nil || !strings.Contains(err.Error(), "unknown keyword argument") {
		t.Errorf("unknown kwarg: got %v", err)
	}
}

func TestPositionalAfterKeywordIsParseError(t *testing.T) {
	if err := evalErr(t, `open("x").read_tsv(header=true, 5)`); err == nil || !strings.Contains(err.Error(), "positional argument after keyword") {
		t.Errorf("positional after kwarg: got %v", err)
	}
}

func TestEqualsArgIsNotKwarg(t *testing.T) {
	// `a == b` inside an argument must not be misread as a keyword argument.
	if got := stringify(evalExprStr(t, `"x,y".split(",").contains(1 == 1)`, nil)); got != "false" {
		t.Errorf("== arg = %q", got)
	}
}

func TestOpenArity(t *testing.T) {
	if err := evalErr(t, `open()`); err == nil || !strings.Contains(err.Error(), "open() takes a path and an optional mode") {
		t.Errorf("open() no-arg: got %v", err)
	}
	if err := evalErr(t, `open("a", "b", "c")`); err == nil || !strings.Contains(err.Error(), "open() takes a path and an optional mode") {
		t.Errorf("open() too-many-args: got %v", err)
	}
	if err := evalErr(t, `open("x", "z")`); err == nil || !strings.Contains(err.Error(), "unknown mode") {
		t.Errorf("open() bad mode: got %v", err)
	}
	if err := evalErr(t, `nope("x")`); err == nil || !strings.Contains(err.Error(), "unknown function") {
		t.Errorf("unknown builtin: got %v", err)
	}
}
