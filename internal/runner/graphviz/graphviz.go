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

	"github.com/compgenlab/cgpipe/internal/eval"
	"github.com/compgenlab/cgpipe/internal/runner"
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

// Build extracts the concrete dependency graph reachable from goals (empty ⇒ the
// program's @default, or its first target). It walks producer → inputs the same
// way the build driver does, instantiating wildcard rules (`%.bam.bai: %.bam`)
// for the goals that reach them. Reserved targets (@pre/@post/…) and
// opportunistic (no-output) targets are not file nodes; a file produced by no
// reached target is a source.
func Build(p *eval.Program, goals []string) Graph {
	explicit := map[string]*eval.Target{}
	var wild []*eval.Target
	for _, t := range p.Targets {
		if t.Special != "" || len(t.Outputs) == 0 {
			continue
		}
		if hasWildcard(t.Outputs) {
			wild = append(wild, t)
			continue
		}
		for _, o := range t.Outputs {
			if _, dup := explicit[o]; !dup {
				explicit[o] = t
			}
		}
	}
	producerFor := func(out string) *eval.Target {
		if t, ok := explicit[out]; ok {
			return t
		}
		for _, rule := range wild {
			if stem, ok := runner.MatchWildcard(rule, out); ok {
				return runner.Instantiate(rule, stem)
			}
		}
		return nil
	}

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
	done := map[string]bool{} // output paths whose producer has been expanded

	var visit func(out string)
	visit = func(out string) {
		note(out)
		if done[out] {
			return
		}
		done[out] = true
		t := producerFor(out)
		if t == nil {
			return // a source file (no producer reached)
		}
		for _, o := range t.Outputs {
			produced[o] = true
			if t.Temp[o] {
				temp[o] = true
			}
			note(o)
			done[o] = true
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
			visit(in)
		}
	}
	for _, g := range resolveGoals(p, goals) {
		visit(g)
	}

	sort.Strings(names)
	g := Graph{Edges: edges}
	for _, n := range names {
		g.Nodes = append(g.Nodes, Node{Name: n, Temp: temp[n], Source: !produced[n]})
	}
	return g
}

// resolveGoals mirrors the driver: explicit goals, else @default, else the first
// defined target.
func resolveGoals(p *eval.Program, goals []string) []string {
	if len(goals) > 0 {
		return goals
	}
	if len(p.Default) > 0 {
		return p.Default
	}
	if p.FirstOutput != "" {
		return []string{p.FirstOutput}
	}
	return nil
}

// Run writes the DOT representation of the goal-reachable dependency graph to out.
func Run(p *eval.Program, goals []string, out io.Writer) error {
	_, err := io.WriteString(out, DOT(Build(p, goals)))
	return err
}

// DOT renders a Graph as Graphviz DOT text.
func DOT(g Graph) string {
	var b strings.Builder
	b.WriteString("digraph cgpipe {\n")
	b.WriteString("  rankdir=LR;\n")
	b.WriteString("  node [shape=box, style=rounded];\n")
	writeNodes(&b, "  ", g.Nodes)
	writeEdges(&b, "  ", g.Edges)
	b.WriteString("}\n")
	return b.String()
}

// writeNodes emits a graph's node declarations at the given indent: temp outputs
// are dashed, source files (no producer) are notes, others are plain boxes.
func writeNodes(b *strings.Builder, indent string, nodes []Node) {
	for _, n := range nodes {
		switch {
		case n.Temp:
			fmt.Fprintf(b, "%s%s [style=\"rounded,dashed\"];\n", indent, quote(n.Name))
		case n.Source:
			fmt.Fprintf(b, "%s%s [shape=note];\n", indent, quote(n.Name))
		default:
			fmt.Fprintf(b, "%s%s;\n", indent, quote(n.Name))
		}
	}
}

// writeEdges emits a graph's input → output edges at the given indent.
func writeEdges(b *strings.Builder, indent string, edges []Edge) {
	for _, e := range edges {
		fmt.Fprintf(b, "%s%s -> %s;\n", indent, quote(e.From), quote(e.To))
	}
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
