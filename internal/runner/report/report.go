// Package report renders a pipeline's dependency graph as a self-contained HTML
// status report: an inline-SVG DAG (no external JS/CSS, no graphviz binary, so
// it opens offline on any cluster) with each output node colored by its current
// job status. Status comes from the caller (disk existence + the ledger's
// owning job + the scheduler's live state).
package report

import (
	"fmt"
	"html"
	"io"
	"sort"
	"strings"

	"github.com/compgenlab/cgpipe/internal/runner/graphviz"
)

// State is a node's status in the report.
type State string

const (
	Pending State = "pending" // not built, no owning job
	Queued  State = "queued"  // owning job waiting in the scheduler
	Running State = "running" // owning job running
	Done    State = "done"    // output present on disk
	Failed  State = "failed"  // owning job ended without producing the output
)

var fill = map[State]string{
	Pending: "#e9ecef", Queued: "#fff3cd", Running: "#cfe2ff", Done: "#d1e7dd", Failed: "#f8d7da",
}
var stroke = map[State]string{
	Pending: "#adb5bd", Queued: "#ffc107", Running: "#0d6efd", Done: "#198754", Failed: "#dc3545",
}

// layout constants
const (
	colW   = 240
	rowH   = 70
	nodeW  = 190
	nodeH  = 42
	margin = 24
)

// Run renders a self-contained HTML status report of a single graph to out.
func Run(g graphviz.Graph, statusOf func(name string) State, title string, out io.Writer) error {
	var b strings.Builder
	fmt.Fprintf(&b, htmlHead, html.EscapeString(title))
	fmt.Fprintf(&b, "<h1>%s</h1>\n", html.EscapeString(title))
	b.WriteString(legend())
	writeSection(&b, g, statusOf)
	b.WriteString("</body>\n</html>\n")
	_, err := io.WriteString(out, b.String())
	return err
}

// writeSection emits one graph's SVG (status-colored) and its summary table.
func writeSection(b *strings.Builder, g graphviz.Graph, statusOf func(name string) State) {
	pos, w, h := layout(g)
	status := map[string]State{}
	for _, n := range g.Nodes {
		status[n.Name] = statusOf(n.Name)
	}

	fmt.Fprintf(b, "<svg width=\"%d\" height=\"%d\" viewBox=\"0 0 %d %d\" class=\"dag\">\n", w, h, w, h)
	// edges first (so nodes sit on top)
	for _, e := range g.Edges {
		from, to := pos[e.From], pos[e.To]
		x1, y1 := from.x+nodeW, from.y+nodeH/2
		x2, y2 := to.x, to.y+nodeH/2
		mx := (x1 + x2) / 2
		fmt.Fprintf(b, "  <path d=\"M%d,%d C%d,%d %d,%d %d,%d\" class=\"edge\"/>\n", x1, y1, mx, y1, mx, y2, x2, y2)
	}
	// nodes
	for _, n := range g.Nodes {
		p := pos[n.Name]
		st := status[n.Name]
		dash := ""
		if n.Temp {
			dash = " stroke-dasharray=\"5,3\""
		}
		fmt.Fprintf(b, "  <g><rect x=\"%d\" y=\"%d\" width=\"%d\" height=\"%d\" rx=\"6\" fill=\"%s\" stroke=\"%s\" stroke-width=\"2\"%s/>\n",
			p.x, p.y, nodeW, nodeH, fill[st], stroke[st], dash)
		fmt.Fprintf(b, "    <text x=\"%d\" y=\"%d\" class=\"label\">%s</text>\n",
			p.x+nodeW/2, p.y+nodeH/2-2, html.EscapeString(elide(n.Name, 26)))
		fmt.Fprintf(b, "    <text x=\"%d\" y=\"%d\" class=\"state\">%s</text></g>\n",
			p.x+nodeW/2, p.y+nodeH/2+13, st)
	}
	b.WriteString("</svg>\n")

	// a table summary, sorted by name
	b.WriteString("<table><thead><tr><th>output</th><th>status</th></tr></thead><tbody>\n")
	names := make([]string, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		names = append(names, n.Name)
	}
	sort.Strings(names)
	for _, n := range names {
		st := status[n]
		fmt.Fprintf(b, "<tr><td>%s</td><td class=\"s-%s\">%s</td></tr>\n", html.EscapeString(n), st, st)
	}
	b.WriteString("</tbody></table>\n")
}

type xy struct{ x, y int }

// layout assigns each node a position by layering it at its longest distance
// from a source (left→right), stacking nodes within a layer top→bottom.
func layout(g graphviz.Graph) (pos map[string]xy, width, height int) {
	order := make([]string, len(g.Nodes)) // stable order = Graph.Nodes order
	idx := map[string]int{}
	for i, n := range g.Nodes {
		order[i] = n.Name
		idx[n.Name] = i
	}
	parents := map[string][]string{}
	indeg := map[string]int{}
	children := map[string][]string{}
	for _, n := range order {
		indeg[n] = 0
	}
	for _, e := range g.Edges {
		parents[e.To] = append(parents[e.To], e.From)
		children[e.From] = append(children[e.From], e.To)
		indeg[e.To]++
	}
	// Kahn topological order (stable by node index)
	var queue []string
	for _, n := range order {
		if indeg[n] == 0 {
			queue = append(queue, n)
		}
	}
	layer := map[string]int{}
	var topo []string
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		topo = append(topo, n)
		l := 0
		for _, p := range parents[n] {
			if layer[p]+1 > l {
				l = layer[p] + 1
			}
		}
		layer[n] = l
		var ready []string
		for _, c := range children[n] {
			indeg[c]--
			if indeg[c] == 0 {
				ready = append(ready, c)
			}
		}
		sort.Slice(ready, func(i, j int) bool { return idx[ready[i]] < idx[ready[j]] })
		queue = append(queue, ready...)
	}
	// any nodes left (cycle — shouldn't happen for a build DAG) get layer 0
	for _, n := range order {
		if _, ok := layer[n]; !ok {
			layer[n] = 0
		}
	}

	// stack within each layer in stable order
	perLayer := map[int][]string{}
	maxLayer := 0
	for _, n := range order {
		perLayer[layer[n]] = append(perLayer[layer[n]], n)
		if layer[n] > maxLayer {
			maxLayer = layer[n]
		}
	}
	pos = map[string]xy{}
	maxRows := 0
	for l := 0; l <= maxLayer; l++ {
		col := perLayer[l]
		if len(col) > maxRows {
			maxRows = len(col)
		}
		for i, n := range col {
			pos[n] = xy{x: margin + l*colW, y: margin + i*rowH}
		}
	}
	width = margin*2 + maxLayer*colW + nodeW
	height = margin*2 + (maxRows-1)*rowH + nodeH
	if maxRows == 0 {
		height = margin*2 + nodeH
	}
	return pos, width, height
}

func elide(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func legend() string {
	var b strings.Builder
	b.WriteString("<div class=\"legend\">")
	for _, st := range []State{Done, Running, Queued, Failed, Pending} {
		fmt.Fprintf(&b, "<span class=\"chip\" style=\"background:%s;border-color:%s\">%s</span>", fill[st], stroke[st], st)
	}
	b.WriteString("</div>\n")
	return b.String()
}

const htmlHead = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>%s</title>
<style>
 body { font: 14px -apple-system, system-ui, sans-serif; margin: 24px; color: #212529; }
 h1 { font-size: 20px; }
 .legend { margin: 8px 0 16px; }
 .chip { display: inline-block; padding: 2px 10px; margin-right: 6px; border: 2px solid; border-radius: 12px; font-size: 12px; }
 svg.dag { border: 1px solid #dee2e6; background: #fff; max-width: 100%%; height: auto; }
 .edge { fill: none; stroke: #adb5bd; stroke-width: 1.5; }
 .label { font-size: 12px; font-weight: 600; text-anchor: middle; }
 .state { font-size: 10px; text-anchor: middle; fill: #495057; }
 table { border-collapse: collapse; margin-top: 16px; }
 th, td { border: 1px solid #dee2e6; padding: 4px 10px; text-align: left; }
 .s-done { color: #198754; } .s-running { color: #0d6efd; } .s-queued { color: #b8860b; }
 .s-failed { color: #dc3545; } .s-pending { color: #6c757d; }
</style>
</head>
<body>
`
