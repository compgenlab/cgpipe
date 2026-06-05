package eval

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/compgen-io/cgp/internal/parser"
)

func runSrc(t *testing.T, src string, vars map[string]Value) (*Program, string) {
	t.Helper()
	f, err := parser.Parse(src, "t.cgp")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var buf bytes.Buffer
	prog, err := Run(f, Options{Out: &buf, Vars: vars})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return prog, buf.String()
}

func testInterp(vars map[string]Value) *interp {
	ip := &interp{sc: newScope(), out: io.Discard, prog: &Program{}}
	for k, v := range vars {
		ip.sc.set(k, v)
	}
	return ip
}

func evalExprStr(t *testing.T, src string, vars map[string]Value) Value {
	t.Helper()
	e, err := parser.ParseExpr(src)
	if err != nil {
		t.Fatalf("parse expr %q: %v", src, err)
	}
	v, err := testInterp(vars).eval(e)
	if err != nil {
		t.Fatalf("eval %q: %v", src, err)
	}
	return v
}

// ---- expressions & operators ----

func TestArithmetic(t *testing.T) {
	cases := map[string]int64{
		"1 + 2 * 3":   7,
		"(1 + 2) * 3": 9,
		"2 ** 3 ** 2": 512, // right assoc
		"17 % 5":      2,
		"20 / 6":      3,
		"-3 + 5":      2,
	}
	for src, want := range cases {
		if got := evalExprStr(t, src, nil); got != IntVal(want) {
			t.Errorf("%s = %v, want %d", src, got, want)
		}
	}
}

func TestStringAndListOps(t *testing.T) {
	if got := evalExprStr(t, `"ab" + "cd"`, nil); got != StrVal("abcd") {
		t.Errorf(`"ab"+"cd" = %v`, got)
	}
	if got := evalExprStr(t, `"x" * 3`, nil); got != StrVal("xxx") {
		t.Errorf(`"x"*3 = %v`, got)
	}
	l := evalExprStr(t, `[1, 2] + [3]`, nil).(ListVal)
	if len(l) != 3 {
		t.Errorf("list concat len = %d", len(l))
	}
}

func TestComparisonsAndLogic(t *testing.T) {
	cases := map[string]bool{
		"1 < 2":            true,
		"2 <= 2":           true,
		"3 > 4":            false,
		`"a" == "a"`:       true,
		`"a" != "b"`:       true,
		"1 == 1 && 2 == 2": true,
		"1 == 2 || 3 == 3": true,
		"!false":           true,
		"!0":               true,
		`!""`:              true,
		"1 == 1.0":         true,
	}
	for src, want := range cases {
		if got := evalExprStr(t, src, nil); got != BoolVal(want) {
			t.Errorf("%s = %v, want %v", src, got, want)
		}
	}
}

func TestIndexSliceEval(t *testing.T) {
	if got := evalExprStr(t, `[10, 20, 30][-1]`, nil); got != IntVal(30) {
		t.Errorf("[-1] = %v", got)
	}
	sl := evalExprStr(t, `[1, 2, 3, 4][1:3]`, nil).(ListVal)
	if len(sl) != 2 || sl[0] != IntVal(2) || sl[1] != IntVal(3) {
		t.Errorf("slice = %v", sl)
	}
}

func TestMethods(t *testing.T) {
	if got := evalExprStr(t, `"chr1.bam".basename().sub("\\.bam$", "")`, nil); got != StrVal("chr1") {
		t.Errorf("basename/sub = %v", got)
	}
	if got := evalExprStr(t, `"a,b,c".split(",").length()`, nil); got != IntVal(3) {
		t.Errorf("split/length = %v", got)
	}
	if got := evalExprStr(t, `[1, 2, 3].join("-")`, nil); got != StrVal("1-2-3") {
		t.Errorf("join = %v", got)
	}
	if got := evalExprStr(t, `(1..5).length()`, nil); got != IntVal(5) {
		t.Errorf("range length = %v", got)
	}
	if got := evalExprStr(t, `"HELLO".lower()`, nil); got != StrVal("hello") {
		t.Errorf("lower = %v", got)
	}
	if got := evalExprStr(t, `"abc".type()`, nil); got != StrVal("string") {
		t.Errorf("type = %v", got)
	}
}

// A range is its own type and answers type/length/contains/index from its two
// bounds — it must never masquerade as (or be materialized into) a list. See
// §9.4: a range stores only Lo and Hi.
func TestRangeStaysARange(t *testing.T) {
	if got := evalExprStr(t, `(1..100).type()`, nil); got != StrVal("range") {
		t.Errorf("(1..100).type() = %v, want range (a range is not a list)", got)
	}
	if got := evalExprStr(t, `xs.type()`, map[string]Value{"xs": RangeVal{Lo: 1, Hi: 9}}); got != StrVal("range") {
		t.Errorf("assigned range type = %v, want range", got)
	}
	// length/contains/index are computed from the bounds (no materialization).
	if got := evalExprStr(t, `(5..1).length()`, nil); got != IntVal(5) {
		t.Errorf("descending range length = %v, want 5", got)
	}
	if got := evalExprStr(t, `(1..10).contains(7)`, nil); got != BoolVal(true) {
		t.Errorf("range.contains(7) = %v, want true", got)
	}
	if got := evalExprStr(t, `(1..10).contains(99)`, nil); got != BoolVal(false) {
		t.Errorf("range.contains(99) = %v, want false", got)
	}
	if got := evalExprStr(t, `(10..20)[3]`, nil); got != IntVal(13) {
		t.Errorf("(10..20)[3] = %v, want 13", got)
	}
	if got := evalExprStr(t, `(1..100)[-1]`, nil); got != IntVal(100) {
		t.Errorf("(1..100)[-1] = %v, want 100", got)
	}
	// it still passes anywhere a list is accepted (join, slice → list)
	if got := evalExprStr(t, `(1..3).join(",")`, nil); got != StrVal("1,2,3") {
		t.Errorf("range.join = %v, want 1,2,3", got)
	}
}

// ---- interpolation ----

func TestInterpolation(t *testing.T) {
	ip := testInterp(map[string]Value{
		"sample": StrVal("p42"),
		"xs":     ListVal{StrVal("a"), StrVal("b"), StrVal("c")},
		"flag":   BoolVal(true),
	})
	check := func(raw, want string) {
		got, err := ip.interpolate(raw)
		if err != nil {
			t.Fatalf("interpolate %q: %v", raw, err)
		}
		if got != want {
			t.Errorf("interpolate %q = %q, want %q", raw, got, want)
		}
	}
	check("results/${sample}/out.vcf", "results/p42/out.vcf")
	check("${missing?}", "")
	check("${if flag; \"-y\"}", "-y")
	check("${if flag; \"-y\"; \"-n\"}", "-y")
	check("a-@{xs}-b", "a-a-b a-b-b a-c-b") // @{} expands the whole string, space-joined
	check("\\${sample}", "${sample}")       // escaped: literal, no interpolation
}

// A bad ${…} names the offending expression and the character it choked on,
// instead of a bare "<expr>:1:N: unexpected ILLEGAL".
func TestBadExpressionErrorIsHelpful(t *testing.T) {
	_, err := testInterp(nil).interpolate("x=${a?b}")
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	if !strings.Contains(msg, `bad expression "a?b"`) {
		t.Errorf("error should name the expression: %q", msg)
	}
	if !strings.Contains(msg, `ILLEGAL("?")`) {
		t.Errorf("error should name the offending char: %q", msg)
	}
}

func TestInterpolationAtBraceWords(t *testing.T) {
	ip := testInterp(map[string]Value{"xs": ListVal{StrVal("a"), StrVal("b")}})
	got, err := ip.expandTemplate("p_@{xs}.txt")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "p_a.txt" || got[1] != "p_b.txt" {
		t.Errorf("expand = %v", got)
	}
}

func TestUndefinedVarErrors(t *testing.T) {
	if _, err := testInterp(nil).interpolate("${nope}"); err == nil {
		t.Fatal("expected error for ${nope}")
	}
}

func TestCommandSubstitution(t *testing.T) {
	got, err := testInterp(nil).interpolate("$(printf hello)")
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello" {
		t.Errorf("$(printf hello) = %q", got)
	}
}

func TestDoubleEval(t *testing.T) {
	ip := testInterp(map[string]Value{
		"tmpl": StrVal("hi ${name}"),
		"name": StrVal("bob"),
	})
	got, err := ip.interpolate("${{tmpl}}")
	if err != nil {
		t.Fatal(err)
	}
	if got != "hi bob" {
		t.Errorf("double-eval = %q, want %q", got, "hi bob")
	}
}

// ---- new statements ----

func TestEvalStatement(t *testing.T) {
	_, out := runSrc(t, "eval \"x = 41\"\nprint x + 1", nil)
	if out != "42\n" {
		t.Errorf("eval statement: out = %q", out)
	}
}

func TestDumpvars(t *testing.T) {
	_, out := runSrc(t, "a = 1\nb = \"two\"\ndumpvars", nil)
	if !strings.Contains(out, "a = 1") || !strings.Contains(out, "b = two") {
		t.Errorf("dumpvars out = %q", out)
	}
}

func TestShowhelp(t *testing.T) {
	f, err := parser.Parse("# Hello help line\nshowhelp\n", "t.cgp")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := Run(f, Options{Out: &buf}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "Hello help line") {
		t.Errorf("showhelp out = %q", buf.String())
	}
}

func TestIncludeStatement(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "inc.cgp"), []byte(`shared = "yes"`), 0o644); err != nil {
		t.Fatal(err)
	}
	main := "include \"inc.cgp\"\nprint shared"
	f, err := parser.Parse(main, filepath.Join(dir, "main.cgp"))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := Run(f, Options{File: filepath.Join(dir, "main.cgp"), Out: &buf}); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "yes\n" {
		t.Errorf("include: out = %q, want %q", buf.String(), "yes\n")
	}
}

func TestSnippetDefinitionAndInvocation(t *testing.T) {
	prog, _ := runSrc(t, `snippet common {{
    set -euo pipefail
}}
out: in {{
    @common
    work ${input}
}}`, nil)
	if _, ok := prog.Snippets["common"]; !ok {
		t.Fatal("snippet 'common' not registered")
	}
	rendered, err := prog.RenderTarget(prog.Targets[0])
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"set -euo pipefail", "work in"}
	if got := nonEmptyLines(rendered); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("rendered = %v, want %v", got, want)
	}
}

// ---- statements / control flow ----

func TestPrintAndControlFlow(t *testing.T) {
	_, out := runSrc(t, `
n = 3
if n > 2 { print "big" } elif n > 0 { print "some" } else { print "none" }
for i in 1..3 { print i }
`, nil)
	want := "big\n1\n2\n3\n"
	if out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

func TestDefaultDoesNotOverrideCLIVar(t *testing.T) {
	_, out := runSrc(t, `
threads ?= 4
print threads
`, map[string]Value{"threads": IntVal(16)})
	if out != "16\n" {
		t.Errorf("?= overrode CLI var: %q", out)
	}
}

func TestExitPropagates(t *testing.T) {
	f, _ := parser.Parse(`print "before"; exit 3; print "after"`, "t")
	var buf bytes.Buffer
	_, err := Run(f, Options{Out: &buf})
	ex, ok := err.(*ExitError)
	if !ok || ex.Code != 3 {
		t.Fatalf("err = %v, want ExitError{3}", err)
	}
	if buf.String() != "before\n" {
		t.Errorf("output = %q (should stop at exit)", buf.String())
	}
}

// ---- config layering ----

func configFrom(t *testing.T, src string) ConfigFile {
	t.Helper()
	f, err := parser.Parse(src, "config")
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	return ConfigFile{Dir: ".", File: f}
}

func TestConfigEvaluatedBeforeScript(t *testing.T) {
	main, _ := parser.Parse(`x ?= 2`, "main")
	prog, err := Run(main, Options{Out: io.Discard, Configs: []ConfigFile{configFrom(t, `x = 1`)}})
	if err != nil {
		t.Fatal(err)
	}
	// config set x=1 first; the script's ?= must not override it.
	if v, _ := prog.Get("x"); v != IntVal(1) {
		t.Fatalf("x = %v, want 1 (config beats script ?=)", v)
	}
}

func TestCLIVarBeatsConfig(t *testing.T) {
	main, _ := parser.Parse(``, "main")
	prog, err := Run(main, Options{
		Out:     io.Discard,
		Configs: []ConfigFile{configFrom(t, `x = 1`)},
		Vars:    map[string]Value{"x": IntVal(5)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := prog.Get("x"); v != IntVal(5) {
		t.Fatalf("x = %v, want 5 (CLI beats config)", v)
	}
}

func TestScriptAssignBeatsConfig(t *testing.T) {
	main, _ := parser.Parse(`x = 3`, "main")
	prog, err := Run(main, Options{Out: io.Discard, Configs: []ConfigFile{configFrom(t, `x = 1`)}})
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := prog.Get("x"); v != IntVal(3) {
		t.Fatalf("x = %v, want 3 (script = is last, wins)", v)
	}
}

// ---- target collection ----

func TestTargetCollection(t *testing.T) {
	prog, _ := runSrc(t, `
sorted.bam: raw.bam {{
    sort ${input} > ${output}
}}
final.txt: sorted.bam {{
    wc -l ${input} > ${output}
}}
@default: final.txt
`, nil)
	if len(prog.Targets) != 2 {
		t.Fatalf("targets = %d, want 2", len(prog.Targets))
	}
	if prog.FirstOutput != "sorted.bam" {
		t.Errorf("first output = %q", prog.FirstOutput)
	}
	if len(prog.Default) != 1 || prog.Default[0] != "final.txt" {
		t.Errorf("default = %v", prog.Default)
	}
}

func TestTempOutputFlag(t *testing.T) {
	prog, _ := runSrc(t, "^inter.bam: raw.bam {{\n  sort\n}}", nil)
	tg := prog.Targets[0]
	if len(tg.Outputs) != 1 || tg.Outputs[0] != "inter.bam" {
		t.Fatalf("outputs = %v (caret should be stripped)", tg.Outputs)
	}
	if !tg.Temp["inter.bam"] {
		t.Errorf("inter.bam should be marked temp")
	}
}

func TestExportAndStageCollected(t *testing.T) {
	prog, _ := runSrc(t, "export f = \"a.txt\"\nstage b b.cgp --bam ${x}", map[string]Value{"x": StrVal("v")})
	if prog.Exports["f"] != StrVal("a.txt") {
		t.Fatalf("exports = %v", prog.Exports)
	}
	if len(prog.Stages) != 1 || prog.Stages[0].Name != "b" || prog.Stages[0].File != "b.cgp" {
		t.Fatalf("stages = %#v", prog.Stages)
	}
	if len(prog.Stages[0].Args) != 2 || prog.Stages[0].Args[1] != "${x}" {
		t.Fatalf("stage args = %v (should be raw, not interpolated)", prog.Stages[0].Args)
	}
}

func TestExportNamesWalksConditionals(t *testing.T) {
	f, err := parser.Parse("export a = 1\nif cond { export b = 2 }\nfor x in [1] { export c = 3 }", "t")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, n := range ExportNames(f) {
		got[n] = true
	}
	for _, want := range []string{"a", "b", "c"} {
		if !got[want] {
			t.Errorf("ExportNames missing %q (got %v)", want, got)
		}
	}
}

func TestInterpolateHelper(t *testing.T) {
	got, err := Interpolate("${a.b}-x", map[string]Value{"a.b": StrVal("V")})
	if err != nil || got != "V-x" {
		t.Fatalf("Interpolate = %q, err=%v", got, err)
	}
	// missing stage export => a clear error
	if _, err := Interpolate("${a.b}", nil); err == nil {
		t.Fatal("expected error for missing ${a.b}")
	}
}

func TestDynamicTargetGeneration(t *testing.T) {
	prog, _ := runSrc(t, `
chroms = ["1", "2", "3"]
acc = []
for c in chroms {
    acc += "calls.${c}.vcf"
    ^calls.${c}.vcf: in.bam {{
        call -r ${c} > ${output}
    }}
}
merged.vcf: @{acc} {{
    concat ${input} > ${output}
}}
`, nil)
	// 3 per-chrom + 1 merge
	if len(prog.Targets) != 4 {
		t.Fatalf("targets = %d, want 4", len(prog.Targets))
	}
	merge := prog.Targets[3]
	if len(merge.Inputs) != 3 || merge.Inputs[0] != "calls.1.vcf" || merge.Inputs[2] != "calls.3.vcf" {
		t.Errorf("merge inputs = %v", merge.Inputs)
	}
	// per-chrom outputs are concrete and temp
	if prog.Targets[0].Outputs[0] != "calls.1.vcf" || !prog.Targets[0].Temp["calls.1.vcf"] {
		t.Errorf("per-chrom target[0] = %v temp=%v", prog.Targets[0].Outputs, prog.Targets[0].Temp)
	}
}
