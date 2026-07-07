package spectest

import (
	"strings"
	"testing"
)

// §1 Lexical structure.

// §1.2 A leading #! line is ignored by the parser.
func TestShebangIgnored(t *testing.T) {
	if got := printed(t, "#!/usr/bin/env cgp\nprint \"ok\""); got != "ok\n" {
		t.Errorf("shebang not ignored: out = %q", got)
	}
}

// §1.3 The leading run of comment lines (after the shebang) is the help text,
// ended by the first blank or non-comment line.
func TestHelpTextBlock(t *testing.T) {
	prog, _ := build(t, "#!/usr/bin/env cgp\n# Align reads.\n# --ref FILE\n\n# not help (after the blank)\nx = 1", nil)
	mustContain(t, prog.Help, "Align reads.", "--ref FILE")
	if strings.Contains(prog.Help, "not help") {
		t.Errorf("help block did not end at the blank line: %q", prog.Help)
	}
}

// §1.3 `#` begins a comment running to end of line, including after code.
func TestTrailingComment(t *testing.T) {
	if got := printed(t, "x = 1   # this is ignored\nprint x"); got != "1\n" {
		t.Errorf("trailing comment not stripped: out = %q", got)
	}
}

// §1.5 `{{ }}` is raw shell: a lone `}}` line terminates it, but a bare `}`
// (as in a shell function or brace group) does not.
func TestRawShellBodyKeepsBraceLines(t *testing.T) {
	got := render(t, `out.sh: {{
    greet() {
        echo hi > ${output}
    }
    greet
}}
@default: out.sh`)
	mustContain(t, got, "greet() {", "echo hi > out.sh", "greet")
	// the closing brace of the shell function survives into the rendered body
	lines := shellLines(got)
	if !containsLine(lines, "}") {
		t.Errorf("a lone `}` shell line was wrongly consumed as a terminator:\n%s", got)
	}
}

// §1.5 `{ }` delimits cgpipe code (if/for); braces are matched by counting, so a
// nested block parses correctly.
func TestCodeBlockBracesNest(t *testing.T) {
	out := printed(t, `n = 2
if n > 0 {
    if n > 1 {
        print "deep"
    }
}`)
	if out != "deep\n" {
		t.Errorf("nested code blocks: out = %q", out)
	}
}

func containsLine(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}
