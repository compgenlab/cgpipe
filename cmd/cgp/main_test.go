package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunVersion(t *testing.T) {
	if code := run([]string{"--version"}); code != 0 {
		t.Fatalf("run(--version) = %d, want 0", code)
	}
}

func TestRunPipelineEndToEnd(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile("p.cgp", []byte(`hello.txt: {{
    echo hi > ${output}
}}
@default: hello.txt`), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"p.cgp"}); code != 0 {
		t.Fatalf("run(p.cgp) = %d, want 0", code)
	}
	b, err := os.ReadFile(filepath.Join(dir, "hello.txt"))
	if err != nil || string(b) != "hi\n" {
		t.Fatalf("hello.txt = %q, err=%v", string(b), err)
	}
}

func TestRunExitCode(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	os.WriteFile("p.cgp", []byte(`if !required { exit 7 }`), 0o644)
	if code := run([]string{"p.cgp"}); code != 7 {
		t.Fatalf("run = %d, want 7 (exit propagation)", code)
	}
}

func TestRunMissingFile(t *testing.T) {
	if code := run([]string{"does-not-exist.cgp"}); code != 1 {
		t.Fatalf("run(missing) = %d, want 1", code)
	}
}

func TestRunHelp(t *testing.T) {
	if code := run([]string{"-h"}); code != 0 {
		t.Fatalf("run(-h) = %d, want 0", code)
	}
}

func TestRunNoArgs(t *testing.T) {
	if code := run(nil); code != 2 {
		t.Fatalf("run(nil) = %d, want 2", code)
	}
}
