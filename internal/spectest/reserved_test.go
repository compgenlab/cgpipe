package spectest

import (
	"strings"
	"testing"
)

// §8 @pre is prepended and @post appended to every target's body.
func TestPrePostWrapping(t *testing.T) {
	got := render(t, `@pre {{
    echo START
}}
@post {{
    echo END
}}
out: in {{
    work
}}`)
	lines := shellLines(got)
	if strings.Join(lines, "|") != "echo START|work|echo END" {
		t.Errorf("pre/post wrapping: %v", lines)
	}
}

// §8 A target opts out of @pre / @post with nopre / nopost directives.
func TestPrePostOptOut(t *testing.T) {
	src := `@pre {{
    echo START
}}
out: in {{
    nopre = true
    --
    work
}}`
	lines := shellLines(render(t, src))
	if containsLine(lines, "echo START") {
		t.Errorf("nopre did not suppress @pre: %v", lines)
	}
	if !containsLine(lines, "work") {
		t.Errorf("body missing: %v", lines)
	}
}

// §8 nopost suppresses @post for that target only.
func TestPostOptOut(t *testing.T) {
	src := `@post {{
    echo END
}}
out: in {{
    nopost = true
    --
    work
}}`
	lines := shellLines(render(t, src))
	if containsLine(lines, "echo END") {
		t.Errorf("nopost did not suppress @post: %v", lines)
	}
}

// §8 nopre on one target does not affect another target's @pre.
func TestOptOutIsPerTarget(t *testing.T) {
	src := `@pre {{
    echo START
}}
skip: in {{
    nopre = true
    --
    work-skip
}}
keep: in {{
    work-keep
}}`
	if containsLine(shellLines(renderNamed(t, src, "skip")), "echo START") {
		t.Error("nopre target wrongly got @pre")
	}
	if !containsLine(shellLines(renderNamed(t, src, "keep")), "echo START") {
		t.Error("the other target lost its @pre")
	}
}

// §8 @setup may run on the submit host with shexec; it's collected as the setup
// target.
func TestSetupCollected(t *testing.T) {
	prog, _ := build(t, "@setup {{\n    shexec = true\n    --\n    mkdir -p out logs\n}}\nx: {{\n    true\n}}", nil)
	if prog.Setup == nil || !prog.Setup.HasBody {
		t.Fatal("@setup target not collected")
	}
}

// §8 @setup runs before the goals (it's the first job).
func TestSetupRunsFirst(t *testing.T) {
	chdirTmp(t)
	// @setup makes a dir; the target writes into it. If setup didn't run first,
	// the target's redirect would fail.
	src := `@setup {{
    shexec = true
    --
    mkdir -p sub
}}
sub/out.txt: {{
    echo hi > ${output}
}}
@default: sub/out.txt`
	runReal(t, src, "sub/out.txt")
	if got := strings.TrimSpace(readFile(t, "sub/out.txt")); got != "hi" {
		t.Errorf("sub/out.txt = %q (setup mkdir should have run first)", got)
	}
}

// §8.1 @default declares the build-by-default goals.
func TestDefaultGoal(t *testing.T) {
	prog, _ := build(t, "a.txt: {{\n  true\n}}\nb.txt: {{\n  true\n}}\n@default: b.txt", nil)
	if len(prog.Default) != 1 || prog.Default[0] != "b.txt" {
		t.Errorf("default = %v, want [b.txt]", prog.Default)
	}
}

// §8.1 Fallback: with no @default, the first defined target is built.
func TestDefaultFallbackToFirst(t *testing.T) {
	chdirTmp(t)
	runReal(t, "first.txt: {{\n    echo 1 > ${output}\n}}\nsecond.txt: {{\n    echo 2 > ${output}\n}}")
	if !exists("first.txt") {
		t.Error("no @default: the first target should build")
	}
	if exists("second.txt") {
		t.Error("only the first target should build without an explicit goal")
	}
}

// §8.1 @default accumulates across multiple declarations.
func TestDefaultAccumulates(t *testing.T) {
	prog, _ := build(t, "@default: a.txt\n@default: b.txt c.txt", nil)
	if strings.Join(prog.Default, " ") != "a.txt b.txt c.txt" {
		t.Errorf("default accumulation = %v", prog.Default)
	}
}

// §8 A reserved @-name never names a file: a real target literally called
// "default" coexists with @default.
func TestReservedNamesAreVirtual(t *testing.T) {
	prog, _ := build(t, "default: in {{\n    work\n}}\n@default: other.txt", nil)
	// "default" is an ordinary file target; @default is the reserved goal list.
	if prog.Targets[0].Outputs[0] != "default" {
		t.Errorf("a file literally named 'default' should be a normal target: %v", prog.Targets[0].Outputs)
	}
	if len(prog.Default) != 1 || prog.Default[0] != "other.txt" {
		t.Errorf("@default goals = %v", prog.Default)
	}
}
