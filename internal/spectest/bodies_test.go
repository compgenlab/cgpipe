package spectest

import (
	"strings"
	"testing"
)

// §6 / §6.1 The body is a template: ${input}/${output} substitute, the rest is
// passed through as shell.
func TestBodyTemplateSubstitution(t *testing.T) {
	got := render(t, "out.txt: a.txt b.txt {{\n    cat ${input} > ${output}\n}}\n@default: out.txt")
	if want := "cat a.txt b.txt > out.txt"; !strings.Contains(got, want) {
		t.Errorf("body render = %q, want it to contain %q", got, want)
	}
}

// §6.2 A directive block before `--` sets per-job settings and is stripped from
// the rendered shell.
func TestDirectivesStripped(t *testing.T) {
	got := render(t, "out: in {{\n    mem = \"16G\"\n    procs = 4\n    --\n    process ${input} > ${output}\n}}\n@default: out")
	lines := shellLines(got)
	if len(lines) != 1 || lines[0] != "process in > out" {
		t.Errorf("directives leaked into shell: %v", lines)
	}
}

// §6.2 `--` is optional: with no directives the whole body is shell.
func TestNoDirectiveSeparator(t *testing.T) {
	got := render(t, "copy.txt: input.txt {{\n    cp ${input} ${output}\n}}\n@default: copy.txt")
	mustContain(t, got, "cp input.txt copy.txt")
}

// §6.3 Inline conditional ${if cond; a; b} in a body, else optional.
func TestInlineConditionalInBody(t *testing.T) {
	withRG := render(t, `rg = "@RG\\tID:1"
out: in {{
    align ${if rg; "-R " + rg} ${input} > ${output}
}}
@default: out`)
	mustContain(t, withRG, "-R @RG")
	// else-less form yields empty when false
	noRG := render(t, `rg = false
out: in {{
    align ${if rg; "-R " + rg} ${input} > ${output}
}}
@default: out`)
	lines := shellLines(noRG)
	if len(lines) != 1 || strings.Contains(lines[0], "-R") {
		t.Errorf("else-less inline conditional should vanish: %v", lines)
	}
}

// §6.4 In-body `%` control lines: a line starting with `%` is cgp; it wraps the
// surrounding shell, which is emitted once per iteration.
func TestPercentControlLines(t *testing.T) {
	got := render(t, `xs = ["a", "b", "c"]
out: in {{
% for f in xs {
    rm ${f}
% }
}}
@default: out`)
	want := []string{"rm a", "rm b", "rm c"}
	if got := shellLines(got); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("%% for control: lines = %v, want %v", got, want)
	}
}

// §6.4 % if/else inside a body selects which shell lines are emitted.
func TestPercentIfElse(t *testing.T) {
	mk := func(flag string) string {
		return "use = " + flag + `
out: in {{
% if use {
    echo on
% } else {
    echo off
% }
}}
@default: out`
	}
	if got := shellLines(render(t, mk("true"))); len(got) != 1 || got[0] != "echo on" {
		t.Errorf("true branch = %v", got)
	}
	if got := shellLines(render(t, mk("false"))); len(got) != 1 || got[0] != "echo off" {
		t.Errorf("false branch = %v", got)
	}
}

// §6.5 Scoping: directive-block assignments are target-local and do not leak to
// the global scope.
func TestDirectiveAssignmentsDoNotLeak(t *testing.T) {
	prog, _ := build(t, "out: in {{\n    mem = \"16G\"\n    --\n    work\n}}", nil)
	if _, ok := prog.Get("mem"); ok {
		t.Error("a directive-block `mem =` leaked into the global scope")
	}
}

// §6.6 Snippets: shared body fragments defined with `snippet` and invoked with
// `@name` inside a body.
func TestSnippets(t *testing.T) {
	got := render(t, `snippet common {{
    set -euo pipefail
    umask 077
}}
out.txt: input.txt {{
    @common
    wc -l ${input} > ${output}
}}
@default: out.txt`)
	mustContain(t, got, "set -euo pipefail", "umask 077", "wc -l input.txt > out.txt")
}
