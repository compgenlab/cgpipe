// Package graphviz turns a pipeline's dependency graph into a structured Graph
// and renders it as Graphviz DOT — `cgp -r graphviz pipeline.cgp` writes the DOT
// to stdout, ready for `dot -Tsvg`. The same Graph backs the HTML status report.
// Nodes are files (a target's outputs and inputs), edges point input → output;
// temp outputs (^) are dashed, source files (produced by no target) are notes.
package graphviz

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/compgen-io/cgp/internal/eval"
)

// Node is a file in the dependency graph.
type Node struct {
	Name   string
	Temp   bool // a ^ temp output
	Source bool // produced by no target in the pipeline (a pipeline input)
}

// Edge points from an input file to an output file that depends on it.
type Edge struct{ From, To string }

// Graph is the concrete file dependency graph (loops/@{} already expanded).
type Graph struct {
	Nodes []Node
	Edges []Edge
}

// Build extracts the concrete dependency graph from a program. Reserved targets
// (@pre/@post/…), opportunistic (no-output) targets, and wildcard rule templates
// (% in an output) are excluded.
func Build(p *eval.Program) Graph {
	produced := map[string]bool{}
	temp := map[string]bool{}
	seen := map[string]bool{}
	var names []string
	note := func(f string) {
		if !seen[f] {
			seen[f] = true
			names = append(names, f)
		}
	}
	var edges []Edge
	edgeSeen := map[string]bool{}

	for _, t := range p.Targets {
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
					edges = append(edges, Edge{in, o})
				}
			}
		}
	}
	sort.Strings(names)
	g := Graph{Edges: edges}
	for _, n := range names {
		g.Nodes = append(g.Nodes, Node{Name: n, Temp: temp[n], Source: !produced[n]})
	}
	return g
}

// Run writes the DOT representation of p's dependency graph to out. goals is
// accepted for interface symmetry with the other runners.
func Run(p *eval.Program, goals []string, out io.Writer) error {
	_, err := io.WriteString(out, DOT(Build(p)))
	return err
}

// DOT renders a Graph as Graphviz DOT text.
func DOT(g Graph) string {
	var b strings.Builder
	b.WriteString("digraph cgp {\n")
	b.WriteString("  rankdir=LR;\n")
	b.WriteString("  node [shape=box, style=rounded];\n")
	for _, n := range g.Nodes {
		switch {
		case n.Temp:
			fmt.Fprintf(&b, "  %s [style=\"rounded,dashed\"];\n", quote(n.Name))
		case n.Source:
			fmt.Fprintf(&b, "  %s [shape=note];\n", quote(n.Name))
		default:
			fmt.Fprintf(&b, "  %s;\n", quote(n.Name))
		}
	}
	for _, e := range g.Edges {
		fmt.Fprintf(&b, "  %s -> %s;\n", quote(e.From), quote(e.To))
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
