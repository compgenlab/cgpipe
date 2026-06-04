package eval

import (
	"bytes"
	"io"
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
