package spectest

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/compgen-io/cgp/internal/eval"
	"github.com/compgen-io/cgp/internal/parser"
	"github.com/compgen-io/cgp/internal/runner"
	"github.com/compgen-io/cgp/internal/runner/shell"
)

// build parses and evaluates src (capturing global `print` output), failing the
// test on any parse/eval error. The captured stdout is returned alongside the
// program so feature tests can assert on either.
func build(t *testing.T, src string, v map[string]eval.Value) (*eval.Program, string) {
	t.Helper()
	f, err := parser.Parse(src, "spec.cgp")
	if err != nil {
		t.Fatalf("parse error: %v\n--- source ---\n%s", err, src)
	}
	var out bytes.Buffer
	prog, err := eval.Run(f, eval.Options{Out: &out, Vars: v})
	if err != nil {
		t.Fatalf("eval error: %v\n--- source ---\n%s", err, src)
	}
	return prog, out.String()
}

// printed returns the stdout produced by global `print`/`dumpvars`/`showhelp`
// statements in src.
func printed(t *testing.T, src string) string {
	t.Helper()
	_, out := build(t, src, nil)
	return out
}

// printsVar evaluates "print (expr)" and returns the single printed line,
// trimmed. A convenience for testing expressions and methods.
func printsExpr(t *testing.T, expr string) string {
	t.Helper()
	return strings.TrimRight(printed(t, "print "+expr), "\n")
}

// render evaluates src and returns the first target's rendered body (with any
// @pre/@post wrapping and snippet expansion applied). It does not resolve the
// dependency graph, so inputs need not exist on disk — this is the right level
// for asserting on body-template behavior.
func render(t *testing.T, src string) string {
	t.Helper()
	prog, _ := build(t, src, nil)
	if len(prog.Targets) == 0 {
		t.Fatalf("no targets defined\n--- source ---\n%s", src)
	}
	out, err := prog.RenderTarget(prog.Targets[0])
	if err != nil {
		t.Fatalf("render error: %v\n--- source ---\n%s", err, src)
	}
	return out
}

// renderNamed renders the body of the target that produces output.
func renderNamed(t *testing.T, src, output string) string {
	t.Helper()
	prog, _ := build(t, src, nil)
	for _, tg := range prog.Targets {
		for _, o := range tg.Outputs {
			if o == output {
				out, err := prog.RenderTarget(tg)
				if err != nil {
					t.Fatalf("render %s: %v", output, err)
				}
				return out
			}
		}
	}
	t.Fatalf("no target produces %q", output)
	return ""
}

// dryRunShell evaluates src and returns the shell backend's dry-run output —
// "# ---- label ----\n<script>" per submitted target — after resolving the
// dependency graph for goals. Use it (not render) when the assertion is about
// which targets run, not about one body's text. Inputs must exist or have a rule.
func dryRunShell(t *testing.T, src string, goals ...string) string {
	t.Helper()
	prog, _ := build(t, src, nil)
	var buf bytes.Buffer
	if err := shell.Run(prog, shell.Options{Goals: goals, DryRun: true, Out: &buf, Cache: runner.NewCache()}); err != nil {
		t.Fatalf("dry-run error: %v\n--- source ---\n%s", err, src)
	}
	return buf.String()
}

// runReal builds src for real in the test's working directory (set the cwd with
// chdirTmp first). Job stdout/stderr are discarded.
func runReal(t *testing.T, src string, goals ...string) {
	t.Helper()
	prog, _ := build(t, src, nil)
	err := shell.Run(prog, shell.Options{
		Goals: goals, Cache: runner.NewCache(),
		Out: io.Discard, Stdout: io.Discard, Stderr: io.Discard,
	})
	if err != nil {
		t.Fatalf("run error: %v\n--- source ---\n%s", err, src)
	}
}

// runForce builds an already-evaluated program for real with the force flag set
// to the given value (job output discarded).
func runForce(t *testing.T, prog *eval.Program, force bool) {
	t.Helper()
	err := shell.Run(prog, shell.Options{
		Force: force, Cache: runner.NewCache(),
		Out: io.Discard, Stdout: io.Discard, Stderr: io.Discard,
	})
	if err != nil {
		t.Fatalf("run (force=%v): %v", force, err)
	}
}

// runRealErr builds src for real and returns the build error (nil on success).
// Use it to assert on build-time failures like "no rule to make"/"no build path".
func runRealErr(t *testing.T, src string, goals ...string) error {
	t.Helper()
	prog, _ := build(t, src, nil)
	return shell.Run(prog, shell.Options{
		Goals: goals, Cache: runner.NewCache(),
		Out: io.Discard, Stdout: io.Discard, Stderr: io.Discard,
	})
}

// chdirTmp switches the test into a fresh temp directory and returns its path.
func chdirTmp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	return dir
}

// evalErr returns the parse-or-eval error for src (nil if it succeeds). Used to
// assert that a malformed or guard-failing pipeline is rejected.
func evalErr(t *testing.T, src string) error {
	t.Helper()
	f, err := parser.Parse(src, "spec.cgp")
	if err != nil {
		return err
	}
	_, err = eval.Run(f, eval.Options{Out: io.Discard})
	return err
}

// wantErr fails unless src is rejected at parse or eval time.
func wantErr(t *testing.T, what, src string) {
	t.Helper()
	if err := evalErr(t, src); err == nil {
		t.Fatalf("%s: expected an error, got none\n--- source ---\n%s", what, src)
	}
}

// exitCode runs src and returns the exit code from an `exit` statement (0 if it
// completes without one). Fails on any non-exit error.
func exitCode(t *testing.T, src string) int {
	t.Helper()
	f, err := parser.Parse(src, "spec.cgp")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	_, err = eval.Run(f, eval.Options{Out: io.Discard})
	if err == nil {
		return 0
	}
	var ex *eval.ExitError
	if errors.As(err, &ex) {
		return ex.Code
	}
	t.Fatalf("unexpected (non-exit) error: %v", err)
	return -1
}

// --- filesystem helpers for target-graph tests -----------------------------

// writeFile writes content to name in the current directory.
func writeFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// readFile returns the content of name in the current directory.
func readFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

// exists reports whether name exists in the current directory.
func exists(name string) bool {
	_, err := os.Stat(name)
	return err == nil
}

// touch sets name's mtime to now+delta (delta is usually negative to make a
// file "older"). Used to drive staleness decisions deterministically.
func touch(t *testing.T, name string, delta time.Duration) {
	t.Helper()
	when := time.Now().Add(delta)
	if err := os.Chtimes(name, when, when); err != nil {
		t.Fatalf("chtimes %s: %v", name, err)
	}
}

// mtime returns name's modification time.
func mtime(t *testing.T, name string) time.Time {
	t.Helper()
	fi, err := os.Stat(name)
	if err != nil {
		t.Fatalf("stat %s: %v", name, err)
	}
	return fi.ModTime()
}

// mustContain fails unless got contains every want substring.
func mustContain(t *testing.T, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in:\n%s", w, got)
		}
	}
}

// mustNotContain fails if got contains any of the substrings.
func mustNotContain(t *testing.T, got string, bad ...string) {
	t.Helper()
	for _, b := range bad {
		if strings.Contains(got, b) {
			t.Errorf("unexpected %q in:\n%s", b, got)
		}
	}
}

// shellLines returns the non-empty, whitespace-trimmed lines of a rendered
// target body, dropping the shell harness's "# ---- label ----" markers.
func shellLines(rendered string) []string {
	var out []string
	for _, ln := range strings.Split(rendered, "\n") {
		s := strings.TrimSpace(ln)
		if s == "" || strings.HasPrefix(s, "# ---- ") {
			continue
		}
		out = append(out, s)
	}
	return out
}
