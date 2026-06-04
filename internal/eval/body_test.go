package eval

import (
	"strings"
	"testing"
)

// renderFirst runs src (which must define at least one target) and renders the
// first target's body.
func renderFirst(t *testing.T, src string) string {
	t.Helper()
	prog, _ := runSrc(t, src, nil)
	if len(prog.Targets) == 0 {
		t.Fatal("no targets defined")
	}
	out, err := prog.RenderTarget(prog.Targets[0])
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return out
}

func TestRenderInputOutputSubstitution(t *testing.T) {
	got := renderFirst(t, "out.txt: a.txt b.txt {{\n    cat ${input} > ${output}\n}}")
	if strings.TrimSpace(got) != "cat a.txt b.txt > out.txt" {
		t.Errorf("render = %q", got)
	}
}

func TestRenderIndexedInputOutput(t *testing.T) {
	got := renderFirst(t, "x y: a b c {{\n    use ${input[0]} ${output[1]}\n}}")
	if strings.TrimSpace(got) != "use a y" {
		t.Errorf("render = %q", got)
	}
}

func TestRenderDirectivesStripped(t *testing.T) {
	got := renderFirst(t, "out: in {{\n    mem = \"16G\"\n    procs = 4\n    --\n    process ${input} > ${output}\n}}")
	if strings.TrimSpace(got) != "process in > out" {
		t.Errorf("render = %q (directives must not appear in shell)", got)
	}
}

func TestRenderForControlLine(t *testing.T) {
	got := renderFirst(t, `xs = ["a", "b", "c"]
out: in {{
% for f in xs {
    rm ${f}
% }
}}`)
	want := []string{"rm a", "rm b", "rm c"}
	lines := nonEmptyLines(got)
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Errorf("render lines = %v, want %v", lines, want)
	}
}

func TestRenderIfElseControlLine(t *testing.T) {
	mk := func(flag string) string {
		return `use = ` + flag + `
out: in {{
% if use {
    echo on
% } else {
    echo off
% }
}}`
	}
	if got := nonEmptyLines(renderFirst(t, mk("true"))); len(got) != 1 || got[0] != "echo on" {
		t.Errorf("true branch = %v", got)
	}
	if got := nonEmptyLines(renderFirst(t, mk("false"))); len(got) != 1 || got[0] != "echo off" {
		t.Errorf("false branch = %v", got)
	}
}

func TestRenderNestedControl_TempCleanupIdiom(t *testing.T) {
	// The documented guarded-cleanup idiom: nested % for / shell if.
	got := renderFirst(t, `tmps = ["x.bam", "y.bam"]
: out.bam @{tmps} {{
    if [ -e out.bam ]; then
% for o in tmps {
        if [ -e ${o} ]; then
% }
            rm -v ${tmps}
% for o in tmps {
        fi
% }
    fi
}}`)
	lines := nonEmptyLines(got)
	want := []string{
		"if [ -e out.bam ]; then",
		"if [ -e x.bam ]; then",
		"if [ -e y.bam ]; then",
		"rm -v x.bam y.bam",
		"fi",
		"fi",
		"fi",
	}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Errorf("render =\n%s\nlines=%v", got, lines)
	}
}

func TestRenderPrePostWrapping(t *testing.T) {
	prog, _ := runSrc(t, `@pre {{
    echo START
}}
@post {{
    echo END
}}
out: in {{
    work
}}`, nil)
	out, err := prog.RenderTarget(prog.Targets[0])
	if err != nil {
		t.Fatal(err)
	}
	lines := nonEmptyLines(out)
	want := []string{"echo START", "work", "echo END"}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Errorf("render = %v, want %v", lines, want)
	}
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		if strings.TrimSpace(ln) != "" {
			out = append(out, strings.TrimSpace(ln))
		}
	}
	return out
}
