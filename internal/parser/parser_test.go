package parser

import (
	"testing"

	"github.com/compgen-io/cgp/internal/ast"
	"github.com/compgen-io/cgp/internal/token"
)

func mustParse(t *testing.T, src string) *ast.File {
	t.Helper()
	f, err := Parse(src, "test.cgp")
	if err != nil {
		t.Fatalf("parse error: %v\nsource:\n%s", err, src)
	}
	return f
}

func only(t *testing.T, src string) ast.Stmt {
	t.Helper()
	f := mustParse(t, src)
	if len(f.Stmts) != 1 {
		t.Fatalf("got %d statements, want 1: %#v", len(f.Stmts), f.Stmts)
	}
	return f.Stmts[0]
}

func TestAssign(t *testing.T) {
	a, ok := only(t, `x = 8`).(*ast.Assign)
	if !ok || a.Name != "x" || a.Op != token.ASSIGN {
		t.Fatalf("got %#v", only(t, `x = 8`))
	}
	if lit, ok := a.Value.(*ast.IntLit); !ok || lit.Val != 8 {
		t.Fatalf("value = %#v", a.Value)
	}
}

func TestDottedAssign(t *testing.T) {
	a := only(t, `cgp.runner = "slurm"`).(*ast.Assign)
	if a.Name != "cgp.runner" {
		t.Fatalf("name = %q, want cgp.runner", a.Name)
	}
	if s, ok := a.Value.(*ast.StringLit); !ok || s.Raw != "slurm" {
		t.Fatalf("value = %#v", a.Value)
	}
}

func TestAssignForms(t *testing.T) {
	if only(t, `a ?= 1`).(*ast.Assign).Op != token.QASSIGN {
		t.Fatal("?= not parsed")
	}
	if only(t, `a += 1`).(*ast.Assign).Op != token.PLUSASSIGN {
		t.Fatal("+= not parsed")
	}
}

func TestArithmeticPrecedence(t *testing.T) {
	// 1 + 2 * 3  =>  (+ 1 (* 2 3))
	a := only(t, `x = 1 + 2 * 3`).(*ast.Assign)
	bin := a.Value.(*ast.Binary)
	if bin.Op != token.PLUS {
		t.Fatalf("top op = %s, want +", bin.Op)
	}
	if _, ok := bin.L.(*ast.IntLit); !ok {
		t.Fatalf("left = %#v, want IntLit", bin.L)
	}
	r, ok := bin.R.(*ast.Binary)
	if !ok || r.Op != token.STAR {
		t.Fatalf("right = %#v, want (* ..)", bin.R)
	}
}

func TestPowRightAssoc(t *testing.T) {
	// 2 ** 3 ** 2 => (** 2 (** 3 2))
	a := only(t, `x = 2 ** 3 ** 2`).(*ast.Assign)
	top := a.Value.(*ast.Binary)
	if top.Op != token.POW {
		t.Fatalf("op = %s", top.Op)
	}
	if _, ok := top.R.(*ast.Binary); !ok {
		t.Fatalf("not right-assoc: %#v", top.R)
	}
}

func TestUnary(t *testing.T) {
	u := only(t, `x = !y`).(*ast.Assign).Value.(*ast.Unary)
	if u.Op != token.NOT {
		t.Fatalf("op = %s", u.Op)
	}
}

func TestListAndRange(t *testing.T) {
	l := only(t, `x = [1, 2, 3]`).(*ast.Assign).Value.(*ast.ListLit)
	if len(l.Elems) != 3 {
		t.Fatalf("list len = %d", len(l.Elems))
	}
	r := only(t, `x = 1..n`).(*ast.Assign).Value.(*ast.RangeLit)
	if _, ok := r.Hi.(*ast.Ident); !ok {
		t.Fatalf("range hi = %#v", r.Hi)
	}
}

func TestIndexAndSlice(t *testing.T) {
	if _, ok := only(t, `x = a[0]`).(*ast.Assign).Value.(*ast.Index); !ok {
		t.Fatal("a[0] not an Index")
	}
	s := only(t, `x = a[1:3]`).(*ast.Assign).Value.(*ast.Slice)
	if s.Lo == nil || s.Hi == nil {
		t.Fatal("a[1:3] should have lo and hi")
	}
	s2 := only(t, `x = a[:2]`).(*ast.Assign).Value.(*ast.Slice)
	if s2.Lo != nil || s2.Hi == nil {
		t.Fatal("a[:2] lo should be nil, hi set")
	}
	s3 := only(t, `x = a[1:]`).(*ast.Assign).Value.(*ast.Slice)
	if s3.Lo == nil || s3.Hi != nil {
		t.Fatal("a[1:] lo set, hi nil")
	}
}

func TestMethodChain(t *testing.T) {
	// name.basename().sub("a","b")
	c := only(t, `x = name.basename().sub("a", "b")`).(*ast.Assign).Value.(*ast.Call)
	if c.Method != "sub" || len(c.Args) != 2 {
		t.Fatalf("outer call = %s/%d args", c.Method, len(c.Args))
	}
	inner, ok := c.Recv.(*ast.Call)
	if !ok || inner.Method != "basename" {
		t.Fatalf("inner = %#v", c.Recv)
	}
}

func TestIfElifElse(t *testing.T) {
	s := only(t, `if a > 1 {
  print "big"
} elif a > 0 {
  print "some"
} else {
  print "none"
}`).(*ast.If)
	if len(s.Conds) != 2 || len(s.Blocks) != 2 || s.Else == nil {
		t.Fatalf("if shape: conds=%d blocks=%d else=%v", len(s.Conds), len(s.Blocks), s.Else != nil)
	}
}

func TestForForms(t *testing.T) {
	fin := only(t, `for c in chroms { print c }`).(*ast.For)
	if fin.Var != "c" || fin.Iter == nil || fin.Cond != nil {
		t.Fatalf("for-in: %#v", fin)
	}
	fc := only(t, `for !done { print "x" }`).(*ast.For)
	if fc.Var != "" || fc.Cond == nil {
		t.Fatalf("for-cond: %#v", fc)
	}
}

func TestPrintExitUnset(t *testing.T) {
	if len(only(t, `print "a", b, 3`).(*ast.Print).Args) != 3 {
		t.Fatal("print args")
	}
	if only(t, `exit 1`).(*ast.Exit).Code == nil {
		t.Fatal("exit code")
	}
	if only(t, `exit`).(*ast.Exit).Code != nil {
		t.Fatal("bare exit should have nil code")
	}
	if only(t, `unset foo`).(*ast.Unset).Name != "foo" {
		t.Fatal("unset name")
	}
}

func TestTargetSimple(t *testing.T) {
	tg := only(t, "sorted.bam: input.bam {{\n  samtools sort ${input} > ${output}\n}}").(*ast.Target)
	if len(tg.Outputs) != 1 || tg.Outputs[0] != "sorted.bam" {
		t.Fatalf("outputs = %v", tg.Outputs)
	}
	if len(tg.Inputs) != 1 || tg.Inputs[0] != "input.bam" {
		t.Fatalf("inputs = %v", tg.Inputs)
	}
	if tg.Body == nil {
		t.Fatal("body nil")
	}
}

func TestTargetMultipleOutputsInputs(t *testing.T) {
	tg := only(t, "a.fq b.fq: raw.fq adapters.fa {{\n  cutadapt\n}}").(*ast.Target)
	if len(tg.Outputs) != 2 || len(tg.Inputs) != 2 {
		t.Fatalf("got %v / %v", tg.Outputs, tg.Inputs)
	}
}

func TestTargetTempCaret(t *testing.T) {
	tg := only(t, "^tmp.bam: raw.bam {{\n  sort\n}}").(*ast.Target)
	if len(tg.Outputs) != 1 || tg.Outputs[0] != "^tmp.bam" {
		t.Fatalf("outputs = %v (caret must be preserved for eval)", tg.Outputs)
	}
}

func TestOpportunisticTarget(t *testing.T) {
	tg := only(t, ": out.bam report.html {{\n  zip\n}}").(*ast.Target)
	if len(tg.Outputs) != 0 {
		t.Fatalf("opportunistic should have no outputs, got %v", tg.Outputs)
	}
	if len(tg.Inputs) != 2 {
		t.Fatalf("inputs = %v", tg.Inputs)
	}
}

func TestBodylessTarget(t *testing.T) {
	tg := only(t, "all: final.vcf report.html").(*ast.Target)
	if tg.Body != nil {
		t.Fatal("bodyless target should have nil Body")
	}
	if len(tg.Inputs) != 2 || tg.Outputs[0] != "all" {
		t.Fatalf("got %v / %v", tg.Outputs, tg.Inputs)
	}
}

func TestTargetInterpolatedWords(t *testing.T) {
	tg := only(t, "${out}.vcf: ${bam} ${ref} {{\n  call\n}}").(*ast.Target)
	if len(tg.Outputs) != 1 || tg.Outputs[0] != "${out}.vcf" {
		t.Fatalf("outputs = %v (interpolation must survive raw to eval)", tg.Outputs)
	}
	if len(tg.Inputs) != 2 || tg.Inputs[0] != "${bam}" || tg.Inputs[1] != "${ref}" {
		t.Fatalf("inputs = %v", tg.Inputs)
	}
}

func TestReservedTargetPre(t *testing.T) {
	tg := only(t, "@pre {{\n  echo start\n}}").(*ast.Target)
	if tg.Special != "pre" || tg.Body == nil {
		t.Fatalf("got special=%q body=%v", tg.Special, tg.Body != nil)
	}
}

func TestDefaultGoal(t *testing.T) {
	tg := only(t, "@default: final.vcf report.html").(*ast.Target)
	if tg.Special != "default" {
		t.Fatalf("special = %q", tg.Special)
	}
	if len(tg.Inputs) != 2 || tg.Body != nil {
		t.Fatalf("inputs=%v body=%v", tg.Inputs, tg.Body != nil)
	}
}

func TestUnknownReservedTargetErrors(t *testing.T) {
	if _, err := Parse("@bogus {{\n}}", "t"); err == nil {
		t.Fatal("expected error for @bogus")
	}
}

func TestDynamicTargetInForLoop(t *testing.T) {
	src := `chroms = ["1", "2"]
acc = []
for c in chroms {
    acc += "calls.${c}.vcf"
    ^calls.${c}.vcf: aligned.bam {{
        call -r ${c} > ${output}
    }}
}
calls.merged.vcf: acc {{
    concat ${input} > ${output}
}}`
	f := mustParse(t, src)
	// statements: assign, assign, for, target
	if len(f.Stmts) != 4 {
		t.Fatalf("top-level stmts = %d, want 4", len(f.Stmts))
	}
	forStmt, ok := f.Stmts[2].(*ast.For)
	if !ok {
		t.Fatalf("stmt[2] = %T, want *For", f.Stmts[2])
	}
	// for body: assign (+=) and a temp target
	var foundTarget bool
	for _, s := range forStmt.Body {
		if tg, ok := s.(*ast.Target); ok {
			foundTarget = true
			if tg.Outputs[0] != "^calls.${c}.vcf" {
				t.Fatalf("dynamic target output = %q", tg.Outputs[0])
			}
		}
	}
	if !foundTarget {
		t.Fatal("no target found inside for loop")
	}
}
