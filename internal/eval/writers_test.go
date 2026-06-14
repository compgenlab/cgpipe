package eval

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/compgen-io/cgp/internal/parser"
)

// runSrcOpts runs src with explicit Options (for dry-run / ErrOut capture) and
// returns stdout. Fails on a run error.
func runSrcOpts(t *testing.T, src string, opts Options) (string, string) {
	t.Helper()
	f, err := parser.Parse(src, "t.cgp")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var out, errBuf bytes.Buffer
	opts.Out = &out
	opts.ErrOut = &errBuf
	if _, err := Run(f, opts); err != nil {
		t.Fatalf("run: %v", err)
	}
	return out.String(), errBuf.String()
}

func TestFileWriteRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "out.txt")
	// write() is verbatim (a "\n" in the string is a real newline); writeln() also
	// appends a trailing newline.
	src := `f = open("` + p + `", "w")
f.write("hello\n")
f.writeln("world")
f.write("tail")
f.close()
print open("` + p + `").read()`
	out, _ := runSrcOpts(t, src, Options{})
	if out != "hello\nworld\ntail\n" { // file is "hello\nworld\ntail"; print adds one newline
		t.Errorf("round-trip out = %q", out)
	}
}

func TestFileAppendMode(t *testing.T) {
	p := filepath.Join(t.TempDir(), "log.txt")
	src := `open("` + p + `", "w").writeln("a").close()
open("` + p + `", "a").writeln("b").close()
print open("` + p + `").read()`
	out, _ := runSrcOpts(t, src, Options{})
	if out != "a\nb\n\n" { // file is "a\nb\n"; print adds one newline
		t.Errorf("append out = %q", out)
	}
}

func TestFileWriteDryRunNoOp(t *testing.T) {
	p := filepath.Join(t.TempDir(), "skip.txt")
	src := `f = open("` + p + `", "w")
f.write("nope\n")
f.close()
print "done"`
	out, errOut := runSrcOpts(t, src, Options{DryRun: true})
	if out != "done\n" {
		t.Errorf("dry-run stdout = %q, want done", out)
	}
	if !strings.Contains(errOut, "not writing to file") {
		t.Errorf("dry-run warning missing: %q", errOut)
	}
	if _, err := os.Stat(p); err == nil {
		t.Errorf("dry-run created the file %q", p)
	}
}

func TestFileWriteDryRunWarnsOncePerPath(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.txt")
	src := `open("` + p + `", "w").write("1").close()
open("` + p + `", "a").write("2").close()
print "ok"`
	_, errOut := runSrcOpts(t, src, Options{DryRun: true})
	if n := strings.Count(errOut, "not writing to file"); n != 1 {
		t.Errorf("warning count = %d, want 1 (deduped per path):\n%s", n, errOut)
	}
}

func TestFileCloseIdempotentAndWriteAfterClose(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.txt")
	// double close is fine
	if _, _, err := runSrcErr(t, `f = open("`+p+`", "w")
f.close()
f.close()`); err != nil {
		t.Errorf("double close errored: %v", err)
	}
	// write after close errors
	_, _, err := runSrcErr(t, `f = open("`+p+`", "w")
f.close()
f.write("x")`)
	if err == nil || !strings.Contains(err.Error(), "closed file") {
		t.Errorf("write-after-close: got %v", err)
	}
}

func TestFileModeMismatch(t *testing.T) {
	p := filepath.Join(t.TempDir(), "m.txt")
	// read method on a write handle
	if _, _, err := runSrcErr(t, `f = open("`+p+`", "w")
f.read()`); err == nil || !strings.Contains(err.Error(), "open for writing") {
		t.Errorf("read on write handle: got %v", err)
	}
	// write method on a read handle
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runSrcErr(t, `open("`+p+`").write("y")`); err == nil || !strings.Contains(err.Error(), "not open for writing") {
		t.Errorf("write on read handle: got %v", err)
	}
}

// runSrcErr runs src and returns (stdout, stderr, runErr) instead of failing.
func runSrcErr(t *testing.T, src string) (string, string, error) {
	t.Helper()
	f, err := parser.Parse(src, "t.cgp")
	if err != nil {
		return "", "", err
	}
	var out, errBuf bytes.Buffer
	_, runErr := Run(f, Options{Out: &out, ErrOut: &errBuf})
	return out.String(), errBuf.String(), runErr
}
