package convert

import (
	"strings"
	"testing"
)

// convertOf is a test helper that returns just the converted text.
func convertOf(t *testing.T, src string) string {
	t.Helper()
	out, _ := Convert(src)
	return out
}

func TestShebang(t *testing.T) {
	got := convertOf(t, "#!/usr/bin/env cgpipe\n")
	if !strings.HasPrefix(got, "#!/usr/bin/env cgpipe\n") {
		t.Fatalf("shebang not rewritten:\n%s", got)
	}
}

func TestControlToBraces(t *testing.T) {
	src := "if !bam\n    print \"no bam\"\n    exit 1\nendif\n"
	got := convertOf(t, src)
	want := "if !bam {\n    print \"no bam\"\n    exit 1\n}\n"
	if got != want {
		t.Fatalf("control flow:\ngot:\n%q\nwant:\n%q", got, want)
	}
}

func TestElifElse(t *testing.T) {
	src := "if a\n    print 1\nelif b\n    print 2\nelse\n    print 3\nendif\n"
	got := convertOf(t, src)
	for _, frag := range []string{"if a {", "} elif b {", "} else {", "}"} {
		if !strings.Contains(got, frag) {
			t.Errorf("missing %q in:\n%s", frag, got)
		}
	}
}

func TestForToBraces(t *testing.T) {
	got := convertOf(t, "for c in chroms\n    print c\ndone\n")
	if !strings.Contains(got, "for c in chroms {") || !strings.Contains(got, "}") {
		t.Fatalf("for loop:\n%s", got)
	}
}

func TestSettingsRename(t *testing.T) {
	got := convertOf(t, "cgpipe.joblog = \"j.log\"\ncgpipe.log = \"x.log\"\ncgpipe.loglevel = 2\n")
	if !strings.Contains(got, "cgpipe.ledger = \"j.log\"") {
		t.Errorf("joblog->ledger missing:\n%s", got)
	}
	// Keys that were not renamed keep the cgpipe.* prefix unchanged.
	if !strings.Contains(got, "cgpipe.log = \"x.log\"") || !strings.Contains(got, "cgpipe.loglevel = 2") {
		t.Errorf("unrenamed cgpipe.* keys missing:\n%s", got)
	}
	if strings.Contains(got, "cgpipe.joblog") {
		t.Errorf("leftover legacy cgpipe.joblog:\n%s", got)
	}
}

func TestTargetBodyAndMakeVars(t *testing.T) {
	src := "out.bam: in.bam\n    samtools sort -o $> $<\n"
	got := convertOf(t, src)
	if !strings.Contains(got, "out.bam: in.bam {{") {
		t.Errorf("target header not wrapped:\n%s", got)
	}
	if !strings.Contains(got, "samtools sort -o ${output} ${input}") {
		t.Errorf("make vars not substituted:\n%s", got)
	}
	if !strings.Contains(got, "}}") {
		t.Errorf("body not closed:\n%s", got)
	}
}

func TestMakeVarIndices(t *testing.T) {
	src := "out: a b\n    cat $<1 $<2 > $>1\n"
	got := convertOf(t, src)
	if !strings.Contains(got, "${input[0]} ${input[1]} > ${output[0]}") {
		t.Fatalf("indexed make vars:\n%s", got)
	}
}

func TestDirectiveBlock(t *testing.T) {
	src := "out: in\n    <%\n        job.mem = \"8G\"\n        job.name = \"x\"\n    %>\n    do-thing $< > $>\n"
	got := convertOf(t, src)
	if !strings.Contains(got, "job.mem = \"8G\"") {
		t.Errorf("job. prefix not preserved:\n%s", got)
	}
	if !strings.Contains(got, "\n    --\n") {
		t.Errorf("directive separator -- missing:\n%s", got)
	}
}

func TestInBodyConditional(t *testing.T) {
	src := "out: in\n    cmd \\\n    <% if bed %>\n    --bed ${bed} \\\n    <% endif %>\n    done\n"
	got := convertOf(t, src)
	if !strings.Contains(got, "% if bed {") || !strings.Contains(got, "% }") {
		t.Fatalf("in-body conditional not converted to %%-lines:\n%s", got)
	}
}

func TestImportToSnippetCall(t *testing.T) {
	src := "out: in\n    <% import common %>\n    do $<\n"
	got := convertOf(t, src)
	if !strings.Contains(got, "@common") {
		t.Fatalf("import not converted to @name:\n%s", got)
	}
}

func TestSpecialTargets(t *testing.T) {
	src := "__pre__::\n    echo start\n\n__setup__::\n    <% job.shexec=true %>\n    mkdir x\n"
	got := convertOf(t, src)
	if !strings.Contains(got, "@pre {{") {
		t.Errorf("__pre__ not converted:\n%s", got)
	}
	if !strings.Contains(got, "@setup {{") {
		t.Errorf("__setup__ not converted:\n%s", got)
	}
	if !strings.Contains(got, "shexec=true") {
		t.Errorf("setup directive lost:\n%s", got)
	}
}

func TestSnippetDefinition(t *testing.T) {
	src := "common::\n    set -e\n    umask 077\n"
	got := convertOf(t, src)
	if !strings.Contains(got, "snippet common {{") || !strings.Contains(got, "}}") {
		t.Fatalf("snippet not converted:\n%s", got)
	}
}

func TestBodylessAggregator(t *testing.T) {
	src := "all: a.txt b.txt\n\nnext = 1\n"
	got := convertOf(t, src)
	if strings.Contains(got, "all: a.txt b.txt {{") {
		t.Fatalf("aggregator wrongly given a body:\n%s", got)
	}
	if !strings.Contains(got, "all: a.txt b.txt") {
		t.Fatalf("aggregator target lost:\n%s", got)
	}
}

func TestNestedTargetInForLoop(t *testing.T) {
	// A target indented inside a for loop: its body ends at the dedented `done`.
	src := "for c in chroms\n    out.${c}: in.${c}\n        do $< > $>\ndone\nx = 1\n"
	got := convertOf(t, src)
	if !strings.Contains(got, "for c in chroms {") {
		t.Errorf("for not converted:\n%s", got)
	}
	if !strings.Contains(got, "out.${c}: in.${c} {{") {
		t.Errorf("nested target not wrapped:\n%s", got)
	}
	// the `done` must become a brace, and `x = 1` must survive at top level
	if !strings.Contains(got, "\n}\nx = 1\n") {
		t.Errorf("loop close / following statement wrong:\n%s", got)
	}
}

func TestBareCmdSubstWrapped(t *testing.T) {
	// bare $(...) in a condition / assignment is wrapped into a cgpipe string
	got := convertOf(t, "if $(which bgzip) == \"\"\n    exit 1\nendif\nx = $(date +%Y)\n")
	if !strings.Contains(got, `if "$(which bgzip)" == "" {`) {
		t.Errorf("bare $() in condition not wrapped:\n%s", got)
	}
	if !strings.Contains(got, `x = "$(date +%Y)"`) {
		t.Errorf("bare $() in assignment not wrapped:\n%s", got)
	}
	// an existing $() already inside a string must not be double-wrapped
	got2 := convertOf(t, "runid = \"run.$(date)\"\n")
	if strings.Contains(got2, `""`) || !strings.Contains(got2, `"run.$(date)"`) {
		t.Errorf("$() inside a string wrongly altered:\n%s", got2)
	}
}

func TestBareCmdSubstNotInShellBody(t *testing.T) {
	// $(...) inside a target's shell body must stay bare (real shell subst)
	got := convertOf(t, "out: in\n    echo $(date) > $>\n")
	if !strings.Contains(got, "echo $(date) > ${output}") {
		t.Fatalf("shell-body $() should stay bare:\n%s", got)
	}
}

func TestInlineFlagged(t *testing.T) {
	src := "out: in\n    cmd <% if x %>--flag<% endif %> $<\n"
	_, warns := Convert(src)
	if len(warns) == 0 {
		t.Fatal("inline <% %> on a shell line should produce a warning")
	}
	out := convertOf(t, src)
	if !strings.Contains(out, "# cgpipe-convert:") {
		t.Errorf("inline case not annotated:\n%s", out)
	}
}

// A multi-line <% … %> directive region (all assignments) becomes the directive
// block before "--".
func TestMultiLineDirectiveRegion(t *testing.T) {
	src := "out.bam: in.bam\n" +
		"    <%\n" +
		"    job.mem = \"8G\"\n" +
		"    job.procs = 4\n" +
		"    %>\n" +
		"    process $< > $>\n"
	got := convertOf(t, src)
	for _, frag := range []string{"out.bam: in.bam {{", "mem = \"8G\"", "procs = 4", "--", "process ${input} > ${output}", "}}"} {
		if !strings.Contains(got, frag) {
			t.Errorf("missing %q in:\n%s", frag, got)
		}
	}
}

// A comment sitting at column 0 inside an indented body must stay part of the
// body (the body must not end early at the dedented comment).
func TestColumnZeroCommentInsideBody(t *testing.T) {
	src := "out.txt: in.txt\n" +
		"    echo a\n" +
		"# mid-body comment\n" +
		"    echo b\n"
	got := convertOf(t, src)
	for _, frag := range []string{"echo a", "# mid-body comment", "echo b", "}}"} {
		if !strings.Contains(got, frag) {
			t.Errorf("missing %q (body ended early at the comment?) in:\n%s", frag, got)
		}
	}
	if strings.Count(got, "{{") != 1 {
		t.Errorf("expected a single body block, got:\n%s", got)
	}
}

// A tab counts as indentation (indentWidth rounds a tab up to 8), so a
// tab-indented line belongs to the target body.
func TestTabIndentedBody(t *testing.T) {
	src := "out.txt: in.txt\n\techo hello\n"
	got := convertOf(t, src)
	for _, frag := range []string{"out.txt: in.txt {{", "echo hello", "}}"} {
		if !strings.Contains(got, frag) {
			t.Errorf("missing %q (tab not treated as body indent?) in:\n%s", frag, got)
		}
	}
}

// A legacy multi-variable for loop can't be mechanically converted; it is passed
// through with a warning and an inline cgpipe-convert note.
func TestMultiVarForLoopWarns(t *testing.T) {
	src := "for a, b in xs, ys\n    echo $a $b\ndone\n"
	got, warnings := Convert(src)
	if !strings.Contains(got, "# cgpipe-convert: rewrite this multi-variable for loop") {
		t.Errorf("missing inline cgpipe-convert note in:\n%s", got)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "multi-variable") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a multi-variable warning, got %v", warnings)
	}
}
