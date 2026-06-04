// Package graphviz renders a pipeline's dependency graph as Graphviz DOT instead
// of running it — `cgp -r graphviz pipeline.cgp` writes the DOT to stdout, ready
// for `dot -Tsvg`. Nodes are files (a target's outputs and inputs), edges point
// input → output. Temp outputs (^) are dashed; source files (produced by no
// target) are notes.
package graphviz

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/compgen-io/cgp/internal/eval"
)

// Run writes the DOT representation of p's dependency graph to out. goals is
// accepted for interface symmetry with the other runners but the whole defined
// graph is emitted.
func Run(p *eval.Program, goals []string, out io.Writer) error {
	g := build(p)
	_, err := io.WriteString(out, g)
	return err
}

// build returns the DOT text for the program's concrete dependency graph.
func build(p *eval.Program) string {
	produced := map[string]bool{}
	temp := map[string]bool{}
	seen := map[string]bool{}
	var files []string
	note := func(f string) {
		if !seen[f] {
			seen[f] = true
			files = append(files, f)
		}
	}

	type edge struct{ from, to string }
	var edges []edge
	edgeSeen := map[string]bool{}

	for _, t := range p.Targets {
		// reserved targets (@pre/@post/…) and opportunistic (no outputs) and
		// wildcard rule templates (% in an output) are not part of the file DAG.
		if t.Special != "" || len(t.Outputs) == 0 || hasWildcard(t.Outputs) {
			continue
		}
		for _, o := range t.Outputs {
			produced[o] = true
			if t.Temp[o] {
				temp[o] = true
			}
			note(o)
		}
		for _, in := range t.Inputs {
			note(in)
			for _, o := range t.Outputs {
				key := in + "\x00" + o
				if !edgeSeen[key] {
					edgeSeen[key] = true
					edges = append(edges, edge{in, o})
				}
			}
		}
	}

	sort.Strings(files)

	var b strings.Builder
	b.WriteString("digraph cgp {\n")
	b.WriteString("  rankdir=LR;\n")
	b.WriteString("  node [shape=box, style=rounded];\n")
	for _, f := range files {
		var attrs []string
		switch {
		case temp[f]:
			attrs = append(attrs, "style=\"rounded,dashed\"")
		case !produced[f]:
			// a source file: produced by nothing in the pipeline
			attrs = append(attrs, "shape=note")
		}
		if len(attrs) > 0 {
			fmt.Fprintf(&b, "  %s [%s];\n", quote(f), strings.Join(attrs, ", "))
		} else {
			fmt.Fprintf(&b, "  %s;\n", quote(f))
		}
	}
	for _, e := range edges {
		fmt.Fprintf(&b, "  %s -> %s;\n", quote(e.from), quote(e.to))
	}
	b.WriteString("}\n")
	return b.String()
}

func hasWildcard(outs []string) bool {
	for _, o := range outs {
		if strings.Contains(o, "%") {
			return true
		}
	}
	return false
}

// quote renders a DOT-safe quoted node id.
func quote(s string) string {
	return "\"" + strings.ReplaceAll(s, "\"", "\\\"") + "\""
}
