package spectest

import (
	"strings"
	"testing"
)

// §4.3 A cgpipe string literal is one escape domain: every `\X` resolves to `X`,
// including inside a ${…}. So an escaped nested string argument parses — the
// outer quotes are escaped (\") only so they survive the enclosing "…".
func TestStringEscapesIncludingInsideSubstitution(t *testing.T) {
	// top-level escapes
	if got := printsExpr(t, `"a\"b"`); got != `a"b` {
		t.Errorf(`"a\"b" => %q, want a"b`, got)
	}
	if got := printsExpr(t, `"\${x}"`); got != "${x}" {
		t.Errorf(`"\${x}" => %q, want ${x} (escaped $ suppresses interpolation)`, got)
	}
	// escaped quotes inside ${…}: the nested string argument is parsed
	src := `name = "reads.bam"` + "\n" + `print "stem=${name.sub(\".bam\", \"\")}"`
	if got := strings.TrimRight(printed(t, src), "\n"); got != "stem=reads" {
		t.Errorf("nested-quote substitution => %q, want stem=reads", got)
	}
	// …and inside a ${if …} branch
	src = `n = "x"` + "\n" + `print "${if n; \"have:\" + n; \"none\"}"`
	if got := strings.TrimRight(printed(t, src), "\n"); got != "have:x" {
		t.Errorf("${if} with escaped-string branches => %q, want have:x", got)
	}
}

// §6.1 A {{ }} body is raw shell, not a cgpipe string literal: only `\$` and `\@`
// are special (they suppress ${…}/@{…}/$(…)); every other backslash passes
// through verbatim so valid shell is not corrupted.
func TestBodyKeepsShellBackslashesVerbatim(t *testing.T) {
	got := render(t, `h = "v"`+"\n"+`out.txt: {{
    echo "x\"y"
    echo back \\ slash
    echo "${h} \${HOME} $HOME"
    echo run \$(date)
}}`)
	mustContain(t, got,
		`echo "x\"y"`,            // \" stays — valid shell
		`echo back \\ slash`,     // \\ stays
		`echo "v ${HOME} $HOME"`, // cgpipe ${h}=v; \${ -> shell ${; $HOME passthrough
		`echo run $(date)`,       // \$( -> shell command substitution, deferred
	)
}

// §4.3 cgpipe's own $(cmd) runs while the body is rendered, so it is substituted
// even under a dry run; \$(cmd) is deferred to the job shell verbatim. This pins
// the (intentional) "-dr still runs $(…)" behavior against silent regression.
func TestDryRunEvaluatesCommandSubstitution(t *testing.T) {
	got := dryRunShell(t, `out.txt: {{
    echo now=$(printf RENDERED)
    echo later=\$(printf DEFERRED)
}}`, "out.txt")
	mustContain(t, got,
		"echo now=RENDERED",             // $(printf …) ran at render time, even in -dr
		"echo later=$(printf DEFERRED)", // \$(…) deferred verbatim to the job shell
	)
	mustNotContain(t, got, "now=$(printf", "DEFERRED\n")
}
