package spectest

import (
	"strings"
	"testing"
)

// cgpipe has two separate control-flow interpreters: the global statement evaluator
// (eval.execIf/execFor) and the shell-body renderer (body.renderNodes for
// ifNode/forNode). The duplication is acknowledged and deferred (see
// internal/eval/value.go), so this test guards against the two drifting apart:
// each case runs equivalent if/elif/else/for-in/while logic through BOTH paths
// — the global form emits via `print`, the body form via `echo` — and asserts
// they select/repeat the same branches and iterations.

// payloadPrint normalizes global `print` output to its non-empty trimmed lines.
func payloadPrint(s string) []string {
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(ln); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// payloadBody normalizes rendered body text to the same form by stripping the
// `echo ` prefix from each emitted shell line.
func payloadBody(s string) []string {
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		out = append(out, strings.TrimPrefix(t, "echo "))
	}
	return out
}

func TestControlFlowConformance(t *testing.T) {
	cases := []struct {
		name   string
		global string // global statements emitting via print
		body   string // a target whose body emits the same via echo
		want   []string
	}{
		{
			name:   "if true branch",
			global: `if 1 < 2 { print "yes" } else { print "no" }`,
			body: "out.txt: {{\n" +
				"% if 1 < 2 {\n" +
				"echo yes\n" +
				"% } else {\n" +
				"echo no\n" +
				"% }\n}}",
			want: []string{"yes"},
		},
		{
			name:   "if else branch",
			global: `if 1 > 2 { print "yes" } else { print "no" }`,
			body: "out.txt: {{\n" +
				"% if 1 > 2 {\n" +
				"echo yes\n" +
				"% } else {\n" +
				"echo no\n" +
				"% }\n}}",
			want: []string{"no"},
		},
		{
			name:   "elif chain",
			global: `if false { print "a" } elif true { print "b" } else { print "c" }`,
			body: "out.txt: {{\n" +
				"% if false {\n" +
				"echo a\n" +
				"% } elif true {\n" +
				"echo b\n" +
				"% } else {\n" +
				"echo c\n" +
				"% }\n}}",
			want: []string{"b"},
		},
		{
			name:   "for-in over list",
			global: `for x in [1, 2, 3] { print x }`,
			body: "out.txt: {{\n" +
				"% for x in [1, 2, 3] {\n" +
				"echo ${x}\n" +
				"% }\n}}",
			want: []string{"1", "2", "3"},
		},
		{
			name:   "for-in over range",
			global: `for x in 1..3 { print x }`,
			body: "out.txt: {{\n" +
				"% for x in 1..3 {\n" +
				"echo ${x}\n" +
				"% }\n}}",
			want: []string{"1", "2", "3"},
		},
		{
			name:   "while form",
			global: "i = 0\nfor i < 3 { print i\ni = i + 1 }",
			body: "i = 0\nout.txt: {{\n" +
				"% for i < 3 {\n" +
				"echo ${i}\n" +
				"% i = i + 1\n" +
				"% }\n}}",
			want: []string{"0", "1", "2"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gp := payloadPrint(printed(t, tc.global))
			bp := payloadBody(render(t, tc.body))
			if !strings.EqualFold(strings.Join(gp, "|"), strings.Join(tc.want, "|")) {
				t.Errorf("global path = %v, want %v", gp, tc.want)
			}
			if strings.Join(gp, "|") != strings.Join(bp, "|") {
				t.Errorf("interpreters disagree: global=%v body=%v", gp, bp)
			}
		})
	}
}
