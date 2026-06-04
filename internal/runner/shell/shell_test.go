package shell

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/compgen-io/cgp/internal/eval"
	"github.com/compgen-io/cgp/internal/parser"
)

func build(t *testing.T, dir, src string, goals ...string) error {
	t.Helper()
	f, err := parser.Parse(src, "t.cgp")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	prog, err := eval.Run(f, eval.Options{Out: io.Discard})
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	return Run(prog, Options{Dir: dir, Goals: goals, Out: io.Discard, Stdout: io.Discard, Stderr: io.Discard})
}

func mustBuild(t *testing.T, dir, src string, goals ...string) {
	t.Helper()
	if err := build(t, dir, src, goals...); err != nil {
		t.Fatalf("build: %v", err)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return strings.Count(strings.TrimRight(string(b), "\n")+"\n", "\n")
}

func setMtime(t *testing.T, path string, mt time.Time) {
	t.Helper()
	if err := os.Chtimes(path, mt, mt); err != nil {
		t.Fatal(err)
	}
}

func mtimeOf(t *testing.T, path string) time.Time {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.ModTime()
}

func exists2(path string) bool { _, err := os.Stat(path); return err == nil }

func TestEndToEndCreatesFiles(t *testing.T) {
	dir := t.TempDir()
	mustBuild(t, dir, `hello.txt: {{
    echo hi > ${output}
}}
world.txt: hello.txt {{
    cat ${input} > ${output}
    echo world >> ${output}
}}
@default: world.txt`)
	if got := read(t, filepath.Join(dir, "world.txt")); got != "hi\nworld\n" {
		t.Errorf("world.txt = %q", got)
	}
}

func TestSkipWhenCurrent(t *testing.T) {
	dir := t.TempDir()
	src := `out.txt: src.txt {{
    echo ran >> runs.log
    cp ${input} ${output}
}}`
	write(t, filepath.Join(dir, "src.txt"), "data\n")
	setMtime(t, filepath.Join(dir, "src.txt"), time.Now().Add(-time.Hour))

	mustBuild(t, dir, src, "out.txt")
	mustBuild(t, dir, src, "out.txt")
	if n := countLines(t, filepath.Join(dir, "runs.log")); n != 1 {
		t.Errorf("ran %d times, want 1 (second run should skip)", n)
	}
}

func TestRebuildWhenInputNewer(t *testing.T) {
	dir := t.TempDir()
	src := `out.txt: src.txt {{
    echo ran >> runs.log
    cp ${input} ${output}
}}`
	write(t, filepath.Join(dir, "src.txt"), "data\n")
	mustBuild(t, dir, src, "out.txt")
	// make the input newer than the output
	setMtime(t, filepath.Join(dir, "src.txt"), time.Now().Add(time.Hour))
	mustBuild(t, dir, src, "out.txt")
	if n := countLines(t, filepath.Join(dir, "runs.log")); n != 2 {
		t.Errorf("ran %d times, want 2 (newer input should rebuild)", n)
	}
}

// TestTempLookThrough exercises A -> ^B -> C: a deleted temp is transparent.
func TestTempLookThrough(t *testing.T) {
	dir := t.TempDir()
	src := `^B: A {{
    echo ran >> B.runs
    cp ${input} ${output}
}}
C: B {{
    echo ran >> C.runs
    cp ${input} ${output}
}}
@default: C`
	A := filepath.Join(dir, "A")
	B := filepath.Join(dir, "B")
	C := filepath.Join(dir, "C")

	write(t, A, "a\n")
	mustBuild(t, dir, src)
	if !exists2(B) || !exists2(C) {
		t.Fatal("first build should create B and C")
	}
	if countLines(t, filepath.Join(dir, "C.runs")) != 1 {
		t.Fatal("C should have run once")
	}

	// delete the temp intermediate
	os.Remove(B)

	// Case 1: A older than C -> nothing rebuilt (deleted temp stays gone).
	cTime := mtimeOf(t, C)
	setMtime(t, A, cTime.Add(-time.Hour))
	mustBuild(t, dir, src)
	if exists2(B) {
		t.Error("case1: deleted temp B should NOT be regenerated when everything downstream is current")
	}
	if n := countLines(t, filepath.Join(dir, "C.runs")); n != 1 {
		t.Errorf("case1: C ran %d times, want 1 (should be skipped)", n)
	}

	// Case 2: A newer than C -> chain rebuilds through the temp.
	setMtime(t, A, mtimeOf(t, C).Add(time.Hour))
	mustBuild(t, dir, src)
	if !exists2(B) {
		t.Error("case2: B should be regenerated when the source is newer")
	}
	if n := countLines(t, filepath.Join(dir, "C.runs")); n != 2 {
		t.Errorf("case2: C ran %d times, want 2 (should rebuild)", n)
	}
}

func TestDynamicPerChromMerge(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "base.txt"), "")
	mustBuild(t, dir, `chroms = ["1", "2"]
acc = []
for c in chroms {
    acc += "p.${c}.txt"
    ^p.${c}.txt: base.txt {{
        echo chr${c} > ${output}
    }}
}
merged.txt: @{acc} {{
    cat ${input} > ${output}
}}
@default: merged.txt`)
	if got := read(t, filepath.Join(dir, "merged.txt")); got != "chr1\nchr2\n" {
		t.Errorf("merged.txt = %q", got)
	}
}

func TestWildcardRule(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "report.txt"), "data\n")
	// %.gz : %  — the stem is "report.txt"; build it via a wildcard rule.
	mustBuild(t, dir, `%.gz: % {{
    echo ${stem} > ${output}
}}
@default: report.txt.gz`)
	if got := read(t, filepath.Join(dir, "report.txt.gz")); got != "report.txt\n" {
		t.Errorf("report.txt.gz = %q, want %q (wildcard stem)", got, "report.txt\n")
	}
}

func TestNoRuleToMake(t *testing.T) {
	dir := t.TempDir()
	err := build(t, dir, "out.txt: dep.txt {{\n  cp ${input} ${output}\n}}", "out.txt")
	if err == nil {
		t.Fatal("expected error: no rule to make dep.txt")
	}
	if !strings.Contains(err.Error(), "dep.txt") {
		t.Errorf("error = %v, want mention of dep.txt", err)
	}
}
