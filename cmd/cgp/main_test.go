package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/compgen-io/cgp/internal/ledger"
)

// TestMain enables the shell runner's autoexec for the end-to-end tests below,
// which assert on executed output. The shell runner now *emits* a script by
// default; that default is covered explicitly by TestShellEmitsScriptByDefault.
func TestMain(m *testing.M) {
	os.Setenv("CGP_ENV", "cgp.runner.shell.autoexec = true")
	os.Exit(m.Run())
}

func TestRunVersion(t *testing.T) {
	if code := run([]string{"version"}); code != 0 {
		t.Fatalf("run(version) = %d, want 0", code)
	}
}

func TestShowTemplateSubcommand(t *testing.T) {
	if code := run([]string{"show-template", "-r", "slurm"}); code != 0 {
		t.Errorf("run(show-template -r slurm) = %d, want 0", code)
	}
	if code := run([]string{"show-template", "-r", "bogus"}); code != 2 {
		t.Errorf("run(show-template -r bogus) = %d, want 2", code)
	}
	if code := run([]string{"show-template"}); code != 2 {
		t.Errorf("run(show-template) with no -r = %d, want 2", code)
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

func TestConvertToStdout(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	os.WriteFile("old.cgp", []byte("#!/usr/bin/env cgpipe\nif !bam\n    exit 1\nendif\nout.bam: in.bam\n    sort -o $> $<\n"), 0o644)
	if code := run([]string{"convert", "old.cgp"}); code != 0 {
		t.Fatalf("cgp convert = %d, want 0", code)
	}
}

func TestConvertToFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	os.WriteFile("old.cgp", []byte("out.bam: in.bam\n    sort -o $> $<\n"), 0o644)
	if code := run([]string{"convert", "old.cgp", "-o", "new.cgp"}); code != 0 {
		t.Fatalf("cgp convert -o = %d, want 0", code)
	}
	b, err := os.ReadFile(filepath.Join(dir, "new.cgp"))
	if err != nil || !strings.Contains(string(b), "out.bam: in.bam {{") {
		t.Fatalf("converted file = %q, err=%v", string(b), err)
	}
}

func TestConvertNoInput(t *testing.T) {
	if code := run([]string{"convert"}); code != 2 {
		t.Fatalf("cgp convert with no input = %d, want 2", code)
	}
}

// §14 Manifest fan-out: CSV format, one run per row.
func TestManifestCSVFanout(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	os.WriteFile("samples.csv", []byte("sample,greeting\nP001,hi\nP002,yo\n"), 0o644)
	os.WriteFile("p.cgp", []byte("out.${sample}.txt: {{\n    echo ${greeting} > ${output}\n}}\n@default: out.${sample}.txt"), 0o644)
	if code := run([]string{"p.cgp", "-manifest-csv", "samples.csv"}); code != 0 {
		t.Fatalf("run = %d", code)
	}
	for _, c := range []struct{ f, want string }{{"out.P001.txt", "hi\n"}, {"out.P002.txt", "yo\n"}} {
		if b, err := os.ReadFile(filepath.Join(dir, c.f)); err != nil || string(b) != c.want {
			t.Errorf("%s = %q, err=%v; want %q", c.f, string(b), err, c.want)
		}
	}
}

// §14 Manifest fan-out: JSON array of objects, one run per object.
func TestManifestJSONFanout(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	os.WriteFile("samples.json", []byte(`[{"sample":"P001","greeting":"hi"},{"sample":"P002","greeting":"yo"}]`), 0o644)
	os.WriteFile("p.cgp", []byte("out.${sample}.txt: {{\n    echo ${greeting} > ${output}\n}}\n@default: out.${sample}.txt"), 0o644)
	if code := run([]string{"p.cgp", "-manifest-json", "samples.json"}); code != 0 {
		t.Fatalf("run = %d", code)
	}
	for _, c := range []struct{ f, want string }{{"out.P001.txt", "hi\n"}, {"out.P002.txt", "yo\n"}} {
		if b, err := os.ReadFile(filepath.Join(dir, c.f)); err != nil || string(b) != c.want {
			t.Errorf("%s = %q, err=%v; want %q", c.f, string(b), err, c.want)
		}
	}
}

// §14 An explicit --name value on the command line overrides a manifest column.
func TestManifestCLIOverridesColumn(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	os.WriteFile("samples.tsv", []byte("sample\tgreeting\nP001\tfrom-file\n"), 0o644)
	os.WriteFile("p.cgp", []byte("out.${sample}.txt: {{\n    echo ${greeting} > ${output}\n}}\n@default: out.${sample}.txt"), 0o644)
	if code := run([]string{"p.cgp", "-manifest-tsv", "samples.tsv", "--greeting", "from-cli"}); code != 0 {
		t.Fatalf("run = %d", code)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "out.P001.txt")); string(b) != "from-cli\n" {
		t.Errorf("out.P001.txt = %q, want CLI value to override the column", string(b))
	}
}

// §3.1 / §2 A CLI value that looks numeric arrives parsed (int), not as a string.
func TestCLIVarNumericTyping(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	os.WriteFile("p.cgp", []byte("out.txt: {{\n    echo ${threads.type()} > ${output}\n}}\n@default: out.txt"), 0o644)
	if code := run([]string{"p.cgp", "--threads", "16"}); code != 0 {
		t.Fatalf("run = %d", code)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "out.txt")); string(b) != "int\n" {
		t.Errorf("threads.type() = %q, want int (numeric CLI value is parsed)", string(b))
	}
}

// §3.1 A bare --name is a boolean flag (name=true); hyphens in the name become
// underscores so it's a usable cgp identifier.
func TestBooleanFlagAndHyphen(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	os.WriteFile("p.cgp", []byte("out.txt: {{\n    echo \"${hp_dist} ${hp_dist.type()} ${adaptive}\" > ${output}\n}}\n@default: out.txt"), 0o644)
	if code := run([]string{"p.cgp", "--hp-dist", "--adaptive"}); code != 0 {
		t.Fatalf("run = %d", code)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "out.txt")); string(b) != "true bool true\n" {
		t.Errorf("out.txt = %q, want \"true bool true\\n\"", string(b))
	}
	// --hp_dist (underscore form) is equivalent to --hp-dist
	if code := run([]string{"p.cgp", "--hp_dist", "--adaptive"}); code != 0 {
		t.Fatalf("run (underscore) = %d", code)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "out.txt")); string(b) != "true bool true\n" {
		t.Errorf("underscore form differs: %q", string(b))
	}
}

// §3.1 A boolean flag immediately before a value flag stays boolean (the value
// flag still takes its own value).
func TestBooleanFlagBeforeValueFlag(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	os.WriteFile("p.cgp", []byte("out.txt: {{\n    echo \"${hp_dist} ${threads} ${threads.type()}\" > ${output}\n}}\n@default: out.txt"), 0o644)
	if code := run([]string{"p.cgp", "--hp-dist", "--threads", "4"}); code != 0 {
		t.Fatalf("run = %d", code)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "out.txt")); string(b) != "true 4 int\n" {
		t.Errorf("out.txt = %q, want \"true 4 int\\n\"", string(b))
	}
}

// §3.1 A repeated --name builds a list; --name=value sets an explicit value (with
// hyphen→underscore on the name).
func TestRepeatedFlagAndEqualsForm(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	os.WriteFile("p.cgp", []byte("out.txt: {{\n    echo \"${x} n=${x.length()} mf=${my_flag} ${my_flag.type()}\" > ${output}\n}}\n@default: out.txt"), 0o644)
	if code := run([]string{"p.cgp", "--x", "a", "--x", "b", "--my-flag=5"}); code != 0 {
		t.Fatalf("run = %d", code)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "out.txt")); string(b) != "a b n=2 mf=5 int\n" {
		t.Errorf("out.txt = %q, want \"a b n=2 mf=5 int\\n\"", string(b))
	}
}

// §15 cgp options may appear before the pipeline file (cgp [options] <file>),
// not only after it.
func TestOptionsBeforeFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	os.WriteFile("p.cgp", []byte("out.txt: {{\n    echo ${greeting} > ${output}\n}}\n@default: out.txt"), 0o644)
	// -dr before the file (the reported bug: "open -dr: no such file")
	if code := run([]string{"-dr", "p.cgp", "--greeting", "hi"}); code != 0 {
		t.Fatalf("run(-dr p.cgp …) = %d, want 0", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "out.txt")); err == nil {
		t.Error("-dr should not have produced out.txt")
	}
	// the same options after the file still execute for real
	if code := run([]string{"p.cgp", "--greeting", "hi"}); code != 0 {
		t.Fatalf("run = %d", code)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "out.txt")); string(b) != "hi\n" {
		t.Errorf("out.txt = %q", string(b))
	}
}

// §15 a run with no pipeline file is an error.
func TestNoPipelineFile(t *testing.T) {
	if code := run([]string{"-dr"}); code != 2 {
		t.Fatalf("run(-dr) with no file = %d, want 2", code)
	}
}

// §15 The shell runner emits a runnable script to stdout by default and does
// NOT execute (no autoexec).
func TestShellEmitsScriptByDefault(t *testing.T) {
	t.Setenv("CGP_ENV", "") // undo TestMain's autoexec
	dir := t.TempDir()
	t.Chdir(dir)
	os.WriteFile("p.cgp", []byte("out.txt: {{\n    echo hi > ${output}\n}}\n@default: out.txt"), 0o644)
	out := captureStdout(t, func() int { return run([]string{"p.cgp"}) })
	if fileThere(dir, "out.txt") {
		t.Error("shell runner should not execute by default (out.txt was created)")
	}
	mustHave(t, out, "#!/usr/bin/env bash", "echo hi > out.txt")
}

// §11.3 cgp.runner.shell.autoexec = true makes the shell runner execute.
func TestShellAutoexecRuns(t *testing.T) {
	t.Setenv("CGP_ENV", "")
	dir := t.TempDir()
	t.Chdir(dir)
	os.WriteFile("p.cgp", []byte("cgp.runner.shell.autoexec = true\nout.txt: {{\n    echo hi > ${output}\n}}\n@default: out.txt"), 0o644)
	if code := run([]string{"p.cgp"}); code != 0 {
		t.Fatalf("run = %d", code)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "out.txt")); string(b) != "hi\n" {
		t.Errorf("autoexec did not run: out.txt = %q", string(b))
	}
}

func mustHave(t *testing.T, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in:\n%s", w, got)
		}
	}
}

// §15 -dr renders instead of executing: no output file is produced.
func TestDryRunDoesNotExecute(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	os.WriteFile("p.cgp", []byte("out.txt: {{\n    echo hi > ${output}\n}}\n@default: out.txt"), 0o644)
	if code := run([]string{"p.cgp", "-dr"}); code != 0 {
		t.Fatalf("run -dr = %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "out.txt")); err == nil {
		t.Error("-dr should not have produced out.txt")
	}
}

// §15 / §8.1 An explicit goal on the command line overrides @default.
func TestExplicitGoalOverridesDefault(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	os.WriteFile("p.cgp", []byte("a.txt: {{\n    echo a > ${output}\n}}\nb.txt: {{\n    echo b > ${output}\n}}\n@default: a.txt"), 0o644)
	if code := run([]string{"p.cgp", "b.txt"}); code != 0 {
		t.Fatalf("run = %d", code)
	}
	if exists := fileThere(dir, "b.txt"); !exists {
		t.Error("explicit goal b.txt was not built")
	}
	if fileThere(dir, "a.txt") {
		t.Error("@default a.txt should not build when an explicit goal is given")
	}
}

// §15.1 cgp sub -dr renders the one-off job instead of running it.
func TestSubDryRun(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if code := run([]string{"sub", "-dr", "-o", "out.txt", "--", "echo hi > ${output}"}); code != 0 {
		t.Fatalf("cgp sub -dr = %d", code)
	}
	if fileThere(dir, "out.txt") {
		t.Error("cgp sub -dr should not create out.txt")
	}
}

// §15.2 cgp ledger vacuum runs against a ledger db.
func TestLedgerVacuumCLI(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "l.db")
	// create the ledger by submitting a one-off job that records an output.
	t.Chdir(dir)
	if code := run([]string{"sub", "-ledger", db, "-o", "out.txt", "--", "echo hi > ${output}"}); code != 0 {
		t.Fatalf("seed ledger = %d", code)
	}
	if code := run([]string{"ledger", "vacuum", db}); code != 0 {
		t.Fatalf("cgp ledger vacuum = %d, want 0", code)
	}
}

// §10.4 -force rebuilds an up-to-date output from the CLI.
func TestForceFlagRebuilds(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	os.WriteFile("p.cgp", []byte("out.txt: {{\n    date +%N >> ${output}\n}}\n@default: out.txt"), 0o644)
	if code := run([]string{"p.cgp"}); code != 0 {
		t.Fatalf("run 1 = %d", code)
	}
	first, _ := os.ReadFile(filepath.Join(dir, "out.txt"))
	if code := run([]string{"p.cgp", "-force"}); code != 0 {
		t.Fatalf("run 2 -force = %d", code)
	}
	second, _ := os.ReadFile(filepath.Join(dir, "out.txt"))
	if len(second) <= len(first) {
		t.Errorf("-force did not re-run the recipe (out.txt unchanged: %q)", string(second))
	}
}

// §14/§11.3 A manifest run with -r graphviz/html emits ONE combined document
// (a cluster/section per row), not one per row concatenated.
func TestManifestCombinedGraphviz(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	os.WriteFile("samples.tsv", []byte("sample\nP001\nP002\n"), 0o644)
	os.WriteFile("p.cgp", []byte("out.${sample}.txt: in.${sample}.txt {{\n    cp ${input} ${output}\n}}\n@default: out.${sample}.txt"), 0o644)

	var buf bytes.Buffer
	stdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	code := run([]string{"p.cgp", "-manifest-tsv", "samples.tsv", "-r", "graphviz"})
	w.Close()
	os.Stdout = stdout
	buf.ReadFrom(r)
	if code != 0 {
		t.Fatalf("run = %d", code)
	}
	out := buf.String()
	if strings.Count(out, "digraph") != 1 {
		t.Errorf("want a single combined digraph, got:\n%s", out)
	}
	if !strings.Contains(out, `label="P001"`) || !strings.Contains(out, `label="P002"`) {
		t.Errorf("missing per-sample clusters:\n%s", out)
	}
}

// §15.2 cgp ledger dump / search read the ledger and print the joblog TSV.
func TestLedgerDumpAndSearchCLI(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "l.db")
	lg, err := ledger.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	lg.Record(ledger.Job{JobID: "1001", Name: "align", Outputs: []string{"out.bam"},
		Inputs: []string{"reads.fq"}, Script: "bwa mem reads.fq > out.bam"})
	lg.Record(ledger.Job{JobID: "1002", Name: "sort", Outputs: []string{"sorted.bam"},
		Inputs: []string{"out.bam"}, Script: "samtools sort out.bam > sorted.bam"})
	lg.Close()

	dump := captureStdout(t, func() int { return run([]string{"ledger", "dump", db}) })
	if !strings.Contains(dump, "1001\tNAME\talign") || !strings.Contains(dump, "1002\tNAME\tsort") {
		t.Errorf("dump missing jobs:\n%s", dump)
	}
	// grep the script: only the samtools job matches
	got := captureStdout(t, func() int { return run([]string{"ledger", "search", "-g", "samtools", db}) })
	if !strings.Contains(got, "1002") || strings.Contains(got, "1001\t") {
		t.Errorf("search -g samtools = %q, want only job 1002", got)
	}
	// a non-matching search prints nothing (not everything)
	none := captureStdout(t, func() int { return run([]string{"ledger", "search", "-g", "zzznope", db}) })
	if strings.TrimSpace(none) != "" {
		t.Errorf("non-matching search should print nothing, got:\n%s", none)
	}
}

// captureStdout runs fn with os.Stdout redirected and returns what it wrote.
func captureStdout(t *testing.T, fn func() int) string {
	t.Helper()
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	code := fn()
	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	buf.ReadFrom(r)
	if code != 0 {
		t.Fatalf("command exited %d", code)
	}
	return buf.String()
}

func fileThere(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
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
