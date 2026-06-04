package spectest

import (
	"strings"
	"testing"

	"github.com/compgen-io/cgp/internal/runner/graphviz"
	"github.com/compgen-io/cgp/internal/runner/report"
)

// The HTML status report is self-contained (inline SVG + CSS, no external refs)
// and colors each node by the status the caller supplies.
func TestHTMLReport(t *testing.T) {
	prog, _ := build(t, `a.txt: in.txt {{
    cp ${input} ${output}
}}
^b.txt: a.txt {{
    cp ${input} ${output}
}}
final.txt: b.txt {{
    cp ${input} ${output}
}}
@default: final.txt`, nil)

	statusOf := func(name string) report.State {
		switch name {
		case "in.txt":
			return report.Done
		case "a.txt":
			return report.Running
		case "b.txt":
			return report.Queued
		case "final.txt":
			return report.Failed
		}
		return report.Pending
	}

	var b strings.Builder
	if err := report.Run(graphviz.Build(prog), statusOf, "my-pipeline", &b); err != nil {
		t.Fatal(err)
	}
	out := b.String()

	// self-contained shell + the title + an inline SVG (no external scripts)
	mustContain(t, out, "<!doctype html>", "<title>my-pipeline</title>", "<svg ", "</svg>")
	mustNotContain(t, out, "http://", "https://", "<script")
	// status colors: running=blue, queued=amber, failed=red
	mustContain(t, out, `fill="#cfe2ff"`, `fill="#fff3cd"`, `fill="#f8d7da"`)
	// edges between the chain
	mustContain(t, out, "<path d=\"M")
	// summary table rows with status classes
	mustContain(t, out,
		`<td class="s-running">running</td>`,
		`<td class="s-queued">queued</td>`,
		`<td class="s-failed">failed</td>`,
		"final.txt", "a.txt", "b.txt")
	// legend present
	mustContain(t, out, "class=\"legend\"")
}

// A node's status defaults to Pending when statusOf says so, and a long name is
// elided in the SVG label (but kept whole in the table).
func TestHTMLReportPendingAndElide(t *testing.T) {
	prog, _ := build(t, `this_is_a_really_long_output_filename.txt: in.txt {{
    cp ${input} ${output}
}}
@default: this_is_a_really_long_output_filename.txt`, nil)
	var b strings.Builder
	if err := report.Run(graphviz.Build(prog), func(string) report.State { return report.Pending }, "p", &b); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	mustContain(t, out, `fill="#e9ecef"`)                            // pending grey
	mustContain(t, out, "…")                                         // elided label in the SVG
	mustContain(t, out, "this_is_a_really_long_output_filename.txt") // full name in the table
}
