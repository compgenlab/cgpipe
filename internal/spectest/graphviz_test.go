package spectest

import (
	"strings"
	"testing"

	"github.com/compgen-io/cgp/internal/runner/graphviz"
)

// dot renders a program's dependency graph as Graphviz DOT.
func dot(t *testing.T, src string) string {
	t.Helper()
	prog, _ := build(t, src, nil)
	var b strings.Builder
	if err := graphviz.Run(prog, nil, &b); err != nil {
		t.Fatalf("graphviz: %v", err)
	}
	return b.String()
}

// §11.3 The graphviz runner emits a DOT digraph: files are nodes, edges point
// input → output, and a temp (^) output is dashed.
func TestGraphvizDOT(t *testing.T) {
	got := dot(t, `raw.bam: reads.fq {{
    align ${input} > ${output}
}}
^sorted.bam: raw.bam {{
    sort ${input} > ${output}
}}
final.txt: sorted.bam {{
    wc -l ${input} > ${output}
}}
@default: final.txt`)
	mustContain(t, got,
		"digraph cgp {",
		`"reads.fq" -> "raw.bam"`,
		`"raw.bam" -> "sorted.bam"`,
		`"sorted.bam" -> "final.txt"`,
	)
	// the temp output sorted.bam is dashed
	if !strings.Contains(got, `"sorted.bam" [style="rounded,dashed"]`) {
		t.Errorf("temp output not dashed:\n%s", got)
	}
	// reads.fq is a source (no producer) → note shape
	if !strings.Contains(got, `"reads.fq" [shape=note]`) {
		t.Errorf("source file not marked:\n%s", got)
	}
}

// §11.3 Multi-input/output edges and @{list} expansion appear in the graph.
func TestGraphvizListExpansion(t *testing.T) {
	got := dot(t, `parts = ["a.vcf", "b.vcf"]
merged.vcf: @{parts} {{
    concat ${input} > ${output}
}}
@default: merged.vcf`)
	mustContain(t, got, `"a.vcf" -> "merged.vcf"`, `"b.vcf" -> "merged.vcf"`)
}

// §11.3 Reserved (@pre/@post) and wildcard-template targets are not nodes in the
// concrete file DAG.
func TestGraphvizSkipsReservedAndWildcards(t *testing.T) {
	got := dot(t, `@pre {{
    echo start
}}
%.gz: % {{
    gzip ${input}
}}
out.txt: in.txt {{
    cp ${input} ${output}
}}
@default: out.txt`)
	mustNotContain(t, got, "@pre", "%.gz", `"%"`)
	mustContain(t, got, `"in.txt" -> "out.txt"`)
}
