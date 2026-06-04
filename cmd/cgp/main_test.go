package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunVersion(t *testing.T) {
	if code := run([]string{"version"}); code != 0 {
		t.Fatalf("run(version) = %d, want 0", code)
	}
}

func TestRunDoubleHyphenVariable(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	// --greeting sets the script variable; -dr just renders (no file written)
	os.WriteFile("p.cgp", []byte(`out.txt: {{
    echo ${greeting} > ${output}
}}
@default: out.txt`), 0o644)
	if code := run([]string{"p.cgp", "--greeting", "hiya"}); code != 0 {
		t.Fatalf("run = %d", code)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "out.txt"))
	if string(b) != "hiya\n" {
		t.Fatalf("out.txt = %q, want %q", string(b), "hiya\n")
	}
}

func TestRunUnknownSingleHyphenOption(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	os.WriteFile("p.cgp", []byte("x: {{\n  true\n}}\n@default: x"), 0o644)
	if code := run([]string{"p.cgp", "-zzz"}); code != 2 {
		t.Fatalf("run(-zzz) = %d, want 2 (unknown option)", code)
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

func TestRunPipelineHelp(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	os.WriteFile("p.cgp", []byte("#!/usr/bin/env cgp\n# Does a thing.\n# --ref FILE\nx: {{\n  true\n}}"), 0o644)
	if code := run([]string{"p.cgp", "-h"}); code != 0 {
		t.Fatalf("run(p.cgp -h) = %d, want 0", code)
	}
}

func TestSubShellCreatesFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if code := run([]string{"sub", "-o", "out.txt", "--", "echo hi > ${output}"}); code != 0 {
		t.Fatalf("cgp sub = %d, want 0", code)
	}
	b, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil || string(b) != "hi\n" {
		t.Fatalf("out.txt = %q, err=%v", string(b), err)
	}
}

func TestConfigFileLoaded(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".cgp"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(home, ".cgp", "config"), []byte(`greeting ?= "from-config"`), 0o644)
	t.Setenv("HOME", home)

	dir := t.TempDir()
	t.Chdir(dir)
	os.WriteFile("p.cgp", []byte("out.txt: {{\n    echo ${greeting} > ${output}\n}}\n@default: out.txt"), 0o644)
	if code := run([]string{"p.cgp"}); code != 0 {
		t.Fatalf("run = %d", code)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "out.txt"))
	if string(b) != "from-config\n" {
		t.Fatalf("out.txt = %q, want config-provided default", string(b))
	}
}

func TestSubNoCommand(t *testing.T) {
	if code := run([]string{"sub", "-mem", "8G"}); code != 2 {
		t.Fatalf("cgp sub with no command = %d, want 2", code)
	}
}

func TestManifestTSVFanout(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	os.WriteFile("samples.tsv", []byte("sample\tgreeting\nP001\thello\nP002\thej\n"), 0o644)
	os.WriteFile("p.cgp", []byte("out.${sample}.txt: {{\n    echo ${greeting} > ${output}\n}}\n@default: out.${sample}.txt"), 0o644)

	if code := run([]string{"p.cgp", "-manifest-tsv", "samples.tsv"}); code != 0 {
		t.Fatalf("run = %d", code)
	}
	for _, c := range []struct{ f, want string }{{"out.P001.txt", "hello\n"}, {"out.P002.txt", "hej\n"}} {
		b, err := os.ReadFile(filepath.Join(dir, c.f))
		if err != nil || string(b) != c.want {
			t.Errorf("%s = %q, err=%v; want %q", c.f, string(b), err, c.want)
		}
	}
}

func TestManifestCGPFanout(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	os.MkdirAll("P001", 0o755)
	os.MkdirAll("P002", 0o755)
	os.WriteFile("P001/m.cgp", []byte("sample = \"P001\"\ngreeting = \"one\""), 0o644)
	os.WriteFile("P002/m.cgp", []byte("sample = \"P002\"\ngreeting = \"two\""), 0o644)
	os.WriteFile("p.cgp", []byte("out.${sample}.txt: {{\n    echo ${greeting} > ${output}\n}}\n@default: out.${sample}.txt"), 0o644)

	if code := run([]string{"p.cgp", "-manifest", "P*/m.cgp"}); code != 0 {
		t.Fatalf("run = %d", code)
	}
	for _, c := range []struct{ f, want string }{{"out.P001.txt", "one\n"}, {"out.P002.txt", "two\n"}} {
		b, err := os.ReadFile(filepath.Join(dir, c.f))
		if err != nil || string(b) != c.want {
			t.Errorf("%s = %q, err=%v; want %q", c.f, string(b), err, c.want)
		}
	}
}

func writeWorkflowFixtures(t *testing.T) {
	t.Helper()
	os.WriteFile("a.cgp", []byte("a.txt: {{\n    echo from-a > ${output}\n}}\n@default: a.txt\nexport f = \"a.txt\""), 0o644)
	os.WriteFile("b.cgp", []byte("b.txt: ${bam} {{\n    cat ${input} > ${output}\n    echo plus-b >> ${output}\n}}\n@default: b.txt"), 0o644)
}

func TestWorkflowShell(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeWorkflowFixtures(t)
	os.WriteFile("wf.cgp", []byte("stage a a.cgp\nstage b b.cgp --bam ${a.f}"), 0o644)

	if code := run([]string{"wf.cgp"}); code != 0 {
		t.Fatalf("run = %d", code)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "b.txt")); string(b) != "from-a\nplus-b\n" {
		t.Fatalf("b.txt = %q (stage b should consume stage a's output)", string(b))
	}
}

func TestWorkflowStaticTypo(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeWorkflowFixtures(t)
	os.WriteFile("wf.cgp", []byte("stage a a.cgp\nstage b b.cgp --bam ${a.nope}"), 0o644)
	if code := run([]string{"wf.cgp"}); code == 0 {
		t.Fatal("workflow with a typo'd ${a.nope} should fail fast")
	}
}

func TestWorkflowRuntimeMissingExport(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	// a.cgp could export f (so static passes) but only under a false guard, so at
	// runtime f is never set and ${a.f} must error.
	os.WriteFile("a.cgp", []byte("a.txt: {{\n    echo x > ${output}\n}}\n@default: a.txt\nif false { export f = \"a.txt\" }"), 0o644)
	os.WriteFile("b.cgp", []byte("b.txt: ${bam} {{\n    cp ${input} ${output}\n}}\n@default: b.txt"), 0o644)
	os.WriteFile("wf.cgp", []byte("stage a a.cgp\nstage b b.cgp --bam ${a.f}"), 0o644)
	if code := run([]string{"wf.cgp"}); code == 0 {
		t.Fatal("workflow should fail when a conditional export didn't fire at runtime")
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
