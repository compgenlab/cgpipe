package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- block scoping + var ----

// TestBlockScopeNewNameIsLocal: a bare assignment to a name unbound anywhere creates
// it in the current block, so it is gone after the block (Ruby-style block locals).
func TestBlockScopeNewNameIsLocal(t *testing.T) {
	_, out := runSrc(t, `for i in [1,2,3] { last = i }
print last`, nil)
	if out != "\n" { // 'last' was block-local; unset after the loop -> prints empty
		t.Errorf("block-local after loop = %q, want empty (unset)", out)
	}
}

// TestVarHoistsThroughBlock: `var` in the enclosing scope lets a deeper block write
// through to it, so the value survives the block.
func TestVarHoistsThroughBlock(t *testing.T) {
	_, out := runSrc(t, `var last
for i in [1,2,3] { last = i }
print last`, nil)
	if out != "3\n" {
		t.Errorf("var-hoisted = %q, want 3", out)
	}
}

// TestBareAssignWritesThrough: an accumulator declared outside the loop keeps growing
// because += resolves up the chain to the existing binding.
func TestBareAssignWritesThrough(t *testing.T) {
	_, out := runSrc(t, `sums = []
for x in [1,2,3] { out = x * 10
sums += out }
print sums`, nil)
	if out != "10 20 30\n" {
		t.Errorf("accumulator = %q, want '10 20 30'", out)
	}
}

// TestVarShadows: `var` inside a block shadows an outer binding; the outer one is
// unchanged after the block.
func TestVarShadows(t *testing.T) {
	_, out := runSrc(t, `x = "outer"
if true { var x = "inner"
print x }
print x`, nil)
	if out != "inner\nouter\n" {
		t.Errorf("shadow = %q, want 'inner' then 'outer'", out)
	}
}

// TestJobAndCgpHoistFromBlock: reserved job.*/cgp.* settings assigned inside an if
// land at the root, so they survive the block (a conditional job/config setting).
func TestJobAndCgpHoistFromBlock(t *testing.T) {
	prog, out := runSrc(t, `if true { cgp.runner = "slurm"
job.mem = "32G" }
print cgp.runner, job.mem`, nil)
	if out != "slurm 32G\n" {
		t.Errorf("hoisted settings = %q, want 'slurm 32G'", out)
	}
	if v, ok := prog.Get("cgp.runner"); !ok || Stringify(v) != "slurm" {
		t.Errorf("cgp.runner not in program scope: %v %v", v, ok)
	}
}

// ---- write-handle auto-close on scope exit ----

// TestHandleClosesAtIterationEnd: a handle opened with `var` in a loop body closes at
// the end of each iteration, so the previous iteration's file is fully flushed and
// readable mid-loop.
func TestHandleClosesAtIterationEnd(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b.txt")
	src := `for p in ["` + a + `", "` + b + `"] {
    var f = open(p, "w")
    f.writeln(p)
}
print open("` + a + `").read()`
	out, _ := runSrcOpts(t, src, Options{})
	if out != a+"\n\n" { // file is "<a>\n"; print adds one newline
		t.Errorf("iteration-close read = %q, want %q", out, a+"\n\n")
	}
	// both files exist and have content (closed/flushed by end of eval)
	for _, p := range []string{a, b} {
		if bs, err := os.ReadFile(p); err != nil || strings.TrimSpace(string(bs)) != p {
			t.Errorf("file %q = %q (err %v)", p, string(bs), err)
		}
	}
}

// TestVarHandleClosesAtOuterScope: a handle is owned by the frame its variable binds
// in. `var f` outer + assignment inside an if means the handle survives the if and is
// usable after it (closed only when the outer scope ends).
func TestVarHandleClosesAtOuterScope(t *testing.T) {
	p := filepath.Join(t.TempDir(), "log.txt")
	src := `var f = open("` + p + `", "w")
if true { f.writeln("a") }
f.writeln("b")
f.close()
print open("` + p + `").read()`
	out, _ := runSrcOpts(t, src, Options{})
	if out != "a\nb\n\n" {
		t.Errorf("outer-owned handle = %q, want 'a\\nb\\n\\n'", out)
	}
}

// ---- dry-run honored during body rendering ----

// TestBodyDirectiveWriteDryRun: a body directive that opens a file for writing is a
// no-op under dry-run (the file is never created) but writes when not dry-run.
func TestBodyDirectiveWriteDryRun(t *testing.T) {
	dir := t.TempDir()
	side := filepath.Join(dir, "side.txt")
	src := `out.txt: in.txt {{
    f = open("` + side + `", "w")
    f.writeln("sidecar")
    f.close()
    --
    cp ${input} ${output}
}}`
	prog, _ := runSrc(t, src, nil)
	prog.DryRun = true
	if _, err := prog.RenderTarget(prog.Targets[0]); err != nil {
		t.Fatalf("render (dry-run): %v", err)
	}
	if _, err := os.Stat(side); err == nil {
		t.Errorf("dry-run render wrote the sidecar file %q", side)
	}
	// not dry-run: the sidecar is written and closed
	prog.DryRun = false
	if _, err := prog.RenderTarget(prog.Targets[0]); err != nil {
		t.Fatalf("render: %v", err)
	}
	if bs, err := os.ReadFile(side); err != nil || strings.TrimSpace(string(bs)) != "sidecar" {
		t.Errorf("sidecar after render = %q (err %v)", string(bs), err)
	}
}

// ---- scope-chain mechanics ----

func TestScopeChain(t *testing.T) {
	root := newScope()
	root.set("a", IntVal(1))
	child := root.child()
	// read resolves up the chain
	if v, ok := child.get("a"); !ok || v != IntVal(1) {
		t.Errorf("chain get = %v %v", v, ok)
	}
	// assign to an existing outer name writes through
	child.assign("a", IntVal(2))
	if v, _ := root.get("a"); v != IntVal(2) {
		t.Errorf("write-through failed: root a = %v", v)
	}
	// assign to a new name lands in the current (child) frame
	child.assign("b", IntVal(9))
	if _, ok := root.vars["b"]; ok {
		t.Error("new name leaked to root")
	}
	// reserved settings hoist to root even from a child
	child.assign("job.x", StrVal("y"))
	if v, ok := root.vars["job.x"]; !ok || v != StrVal("y") {
		t.Errorf("job.* did not hoist to root: %v %v", v, ok)
	}
	// clone flattens with inner shadowing outer
	flat := child.clone()
	if v, _ := flat.get("a"); v != IntVal(2) {
		t.Errorf("clone a = %v, want 2", v)
	}
	if flat.parent != nil {
		t.Error("clone should be a detached root")
	}
}
