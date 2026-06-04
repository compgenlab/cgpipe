package spectest

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/compgen-io/cgp/internal/eval"
	"github.com/compgen-io/cgp/internal/parser"
)

// §5.1 if / elif / else.
func TestIfElifElse(t *testing.T) {
	mk := func(n int) string {
		return "n = " + strconv.Itoa(n) + `
if n > 100 { print "many" } elif n > 0 { print "some" } else { print "none" }`
	}
	cases := []struct {
		n    int
		want string
	}{{150, "many\n"}, {5, "some\n"}, {0, "none\n"}}
	for _, c := range cases {
		if got := printed(t, mk(c.n)); got != c.want {
			t.Errorf("n=%d: out = %q, want %q", c.n, got, c.want)
		}
	}
}

// §5.1 for over a range and over a list.
func TestForRangeAndList(t *testing.T) {
	if got := printed(t, "for i in 1..3 { print i }"); got != "1\n2\n3\n" {
		t.Errorf("for range: %q", got)
	}
	if got := printed(t, `for s in ["a", "b"] { print s }`); got != "a\nb\n" {
		t.Errorf("for list: %q", got)
	}
}

// §5.1 for with a bare condition is while-style.
func TestForWhile(t *testing.T) {
	if got := printed(t, "i = 0\nfor i < 3 { print i\ni = i + 1 }"); got != "0\n1\n2\n" {
		t.Errorf("while-style for: %q", got)
	}
}

// §5.1 The loop variable remains set after the loop.
func TestLoopVarPersists(t *testing.T) {
	if got := printed(t, "for i in 1..3 { }\nprint i"); got != "3\n" {
		t.Errorf("loop var after loop = %q, want 3", got)
	}
}

// §5.2 print writes to stdout; multiple args are space-joined.
func TestPrintMultipleArgs(t *testing.T) {
	if got := printed(t, `print "a", 1, true`); got != "a 1 true\n" {
		t.Errorf("print multi-arg: %q", got)
	}
}

// §5.2 exit stops the pipeline with the given code (default 0).
func TestExit(t *testing.T) {
	if got := exitCode(t, "print \"x\"\nexit 7\nprint \"y\""); got != 7 {
		t.Errorf("exit 7: code = %d", got)
	}
	if got := exitCode(t, "exit"); got != 0 {
		t.Errorf("bare exit: code = %d, want 0", got)
	}
}

// §5.2 exit halts execution: statements after it do not run.
func TestExitHalts(t *testing.T) {
	_, out := buildExpectExit(t, `print "before"`+"\nexit 1\n"+`print "after"`)
	if out != "before\n" {
		t.Errorf("exit did not halt: out = %q", out)
	}
}

// §5.2 eval evaluates a string as cgp source at run time.
func TestEvalStatement(t *testing.T) {
	if got := printed(t, "eval \"x = 41\"\nprint x + 1"); got != "42\n" {
		t.Errorf("eval: %q", got)
	}
}

// §5.2 log sets the cgp log path.
func TestLogStatement(t *testing.T) {
	prog, _ := build(t, `log "logs/run.log"`, nil)
	if prog.Log != "logs/run.log" {
		t.Errorf("log path = %q", prog.Log)
	}
}

// §5.2 dumpvars prints in-scope variables.
func TestDumpvars(t *testing.T) {
	mustContain(t, printed(t, "a = 1\nb = \"two\"\ndumpvars"), "a = 1", "b = two")
}

// §5.2 showhelp prints the help-text block.
func TestShowhelp(t *testing.T) {
	mustContain(t, printed(t, "# Helpful line.\nshowhelp"), "Helpful line.")
}

// §5.2 sleep pauses; sleep 0 is a no-op that does not disturb execution.
func TestSleep(t *testing.T) {
	if got := printed(t, "print \"a\"\nsleep 0\nprint \"b\""); got != "a\nb\n" {
		t.Errorf("sleep 0: out = %q", got)
	}
}

// §5.2 include inlines another file in global context.
func TestInclude(t *testing.T) {
	dir := chdirTmp(t)
	if err := os.WriteFile(filepath.Join(dir, "inc.cgp"), []byte(`shared = "yes"`), 0o644); err != nil {
		t.Fatal(err)
	}
	// build() uses file "spec.cgp"; include resolves relative to the cwd we set.
	f, err := parser.Parse("include \"inc.cgp\"\nprint shared", filepath.Join(dir, "main.cgp"))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := eval.Run(f, eval.Options{File: filepath.Join(dir, "main.cgp"), Out: &buf}); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "yes\n" {
		t.Errorf("include: out = %q", buf.String())
	}
}

// buildExpectExit runs src that is expected to exit, returning captured stdout
// up to the exit.
func buildExpectExit(t *testing.T, src string) (*eval.Program, string) {
	t.Helper()
	f, err := parser.Parse(src, "spec.cgp")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var buf bytes.Buffer
	if _, err = eval.Run(f, eval.Options{Out: &buf}); err == nil {
		t.Fatalf("expected an exit, got none")
	}
	return nil, buf.String()
}
