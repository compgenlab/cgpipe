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

// §11.3 The graph is goal-driven: a wildcard rule reached from a goal is
// instantiated, so its concrete output/input appear; an unreached wildcard does
// not.
func TestGraphvizGoalDrivenWildcard(t *testing.T) {
	got := dot(t, `%.bam.bai: %.bam {{
    index ${input}
}}
x.bam: reads.fq {{
    align ${input} > ${output}
}}
@default: x.bam.bai`)
	// the wildcard instantiated for the goal x.bam.bai
	mustContain(t, got,
		`"x.bam" -> "x.bam.bai"`,
		`"reads.fq" -> "x.bam"`,
	)
	// and the template form never appears as a literal node
	mustNotContain(t, got, "%.bam.bai", `"%"`)
}

// §11.3 Only the goal-reachable subgraph is shown: a target not reached from the
// goal is omitted.
func TestGraphvizGoalScoped(t *testing.T) {
	got := dot(t, `wanted.txt: a.txt {{
    cp ${input} ${output}
}}
unrelated.txt: b.txt {{
    cp ${input} ${output}
}}
@default: wanted.txt`)
	mustContain(t, got, `"a.txt" -> "wanted.txt"`)
	mustNotContain(t, got, "unrelated.txt", "b.txt")
}

// A manifest run combines into ONE digraph with a labeled cluster per row.
func TestGraphvizCombinedClusters(t *testing.T) {
	pa, _ := build(t, "out.A.txt: in.A.txt {{\n    cp ${input} ${output}\n}}\n@default: out.A.txt", nil)
	pb, _ := build(t, "out.B.txt: in.B.txt {{\n    cp ${input} ${output}\n}}\n@default: out.B.txt", nil)
	dotg := graphviz.DOTCombined([]graphviz.Labeled{
		{Label: "P001", Graph: graphviz.Build(pa, nil)},
		{Label: "P002", Graph: graphviz.Build(pb, nil)},
	})
	if strings.Count(dotg, "digraph") != 1 {
		t.Errorf("want one digraph, got:\n%s", dotg)
	}
	mustContain(t, dotg,
		"subgraph cluster_0", `label="P001"`, `"in.A.txt" -> "out.A.txt"`,
		"subgraph cluster_1", `label="P002"`, `"in.B.txt" -> "out.B.txt"`,
	)
}

// §11.3 Reserved (@pre/@post) and unreached wildcard-template targets are not
// nodes in the concrete file DAG.
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
