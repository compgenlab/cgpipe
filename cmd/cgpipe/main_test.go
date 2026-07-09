package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/compgenlab/cgpipe/internal/ledger"
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

// -h and --help are equivalent and context-aware: with a pipeline file (in any
// position) they print that script's help; with no file they print cgpipe's own help.
func TestHelpFlagIsScriptAware(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	os.WriteFile("p.cgp", []byte("#!/usr/bin/env cgp\n# Does a thing.\n# --ref FILE\nx: {{\n  true\n}}"), 0o644)

	scriptHelp := "Does a thing." // first line of the script's help text
	cgpipeHelp := "cgp — run a .cgp pipeline"

	// --help after the file → the script's help (the bug this fixes: it used to
	// be swallowed as a `help` variable and the pipeline ran instead).
	if out := captureStdout(t, func() int { return run([]string{"p.cgp", "--help"}) }); !strings.Contains(out, scriptHelp) {
		t.Errorf("p.cgp --help did not show script help; got:\n%s", out)
	}
	// --help before the file → still the script's help (position-independent).
	if out := captureStdout(t, func() int { return run([]string{"--help", "p.cgp"}) }); !strings.Contains(out, scriptHelp) {
		t.Errorf("--help p.cgp did not show script help; got:\n%s", out)
	}
	// -h after the file → the script's help.
	if out := captureStdout(t, func() int { return run([]string{"p.cgp", "-h"}) }); !strings.Contains(out, scriptHelp) {
		t.Errorf("p.cgp -h did not show script help; got:\n%s", out)
	}
	// --help with no file → cgpipe's own help.
	if out := captureStdout(t, func() int { return run([]string{"--help"}) }); !strings.Contains(out, cgpipeHelp) {
		t.Errorf("--help (no file) did not show cgp help; got:\n%s", out)
	}
	// --help=value remains an ordinary script variable, not a help request.
	if out := captureStdout(t, func() int { return run([]string{"p.cgp", "--help=x", "-dr"}) }); strings.Contains(out, scriptHelp) {
		t.Errorf("--help=x should set a variable, not trigger help; got:\n%s", out)
	}
}

func TestSubShellCreatesFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if code := run([]string{"sub", "-o", "out.txt", "echo hi > ${output}"}); code != 0 {
		t.Fatalf("cgp sub = %d, want 0", code)
	}
	b, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil || string(b) != "hi\n" {
		t.Fatalf("out.txt = %q, err=%v", string(b), err)
	}
}

// CGP_DRYRUN=1 makes `cgp sub` render the job instead of running it, matching
// the pipeline path. Regression: sub previously honored only the -dr flag.
func TestSubDryRunEnv(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("CGP_DRYRUN", "1")
	if code := run([]string{"sub", "-o", "out.txt", "echo hi > ${output}"}); code != 0 {
		t.Fatalf("cgp sub = %d, want 0", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "out.txt")); !os.IsNotExist(err) {
		t.Fatalf("CGP_DRYRUN=1 should not run the job; out.txt err=%v", err)
	}
}

// cgp sub fan-out: files after -- submit one job each, with `{}` substitution.
func TestSubFanout(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	os.WriteFile("a.in", []byte("AAA\n"), 0o644)
	os.WriteFile("b.in", []byte("BBB\n"), 0o644)
	// {} -> each file, {@.in} -> basename minus the .in suffix.
	if code := run([]string{"sub", "-n", "cp{#}", "-o", "{@.in}.out", "cp {} {@.in}.out", "--", "a.in", "b.in"}); code != 0 {
		t.Fatalf("cgp sub fan-out = %d, want 0", code)
	}
	for _, c := range []struct{ f, want string }{{"a.out", "AAA\n"}, {"b.out", "BBB\n"}} {
		b, err := os.ReadFile(filepath.Join(dir, c.f))
		if err != nil || string(b) != c.want {
			t.Errorf("%s = %q err=%v; want %q", c.f, string(b), err, c.want)
		}
	}
}

// cgp sub --files-from: the fan-out list can come from a file (no -- needed).
func TestSubFilesFrom(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	os.WriteFile("a.in", []byte("AAA\n"), 0o644)
	os.WriteFile("b.in", []byte("BBB\n"), 0o644)
	os.WriteFile("list.txt", []byte("a.in\n\nb.in\n"), 0o644) // blank line ignored
	if code := run([]string{"sub", "--files-from", "list.txt", "-o", "{@.in}.out", "cp {} {@.in}.out"}); code != 0 {
		t.Fatalf("cgp sub --files-from = %d, want 0", code)
	}
	for _, c := range []struct{ f, want string }{{"a.out", "AAA\n"}, {"b.out", "BBB\n"}} {
		b, err := os.ReadFile(filepath.Join(dir, c.f))
		if err != nil || string(b) != c.want {
			t.Errorf("%s = %q err=%v; want %q", c.f, string(b), err, c.want)
		}
	}
}

// cgp sub -d/--deps is both comma-separated AND repeatable: the two accumulate.
func TestSubDepsCSVAndRepeatable(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	// batchq renders `#BATCHQ -afterok <deps joined by ",">`, so the dry run
	// shows the accumulated dependency list.
	out := captureStdout(t, func() int {
		return run([]string{"sub", "-dr", "-r", "batchq", "-d", "a,b", "-d", "c", "echo hi"})
	})
	if !strings.Contains(out, "#BATCHQ -afterok a,b,c") {
		t.Errorf("deps not accumulated (csv + repeatable); got:\n%s", out)
	}
}

// cgp sub --files-from may be given only once (unlike the repeatable list flags).
func TestSubFilesFromOnlyOnce(t *testing.T) {
	if code := run([]string{"sub", "-f", "a.txt", "-f", "b.txt", "echo hi"}); code != 2 {
		t.Errorf("repeated --files-from = %d, want 2", code)
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
	if code := run([]string{"sub", "-m", "8G"}); code != 2 {
		t.Fatalf("cgp sub with no command = %d, want 2", code)
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
	os.WriteFile("old.cgp", []byte("#!/usr/bin/env cgp\nif !bam\n    exit 1\nendif\nout.bam: in.bam\n    sort -o $> $<\n"), 0o644)
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
// underscores so it's a usable cgpipe identifier.
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

// §15 cgpipe options may appear before the pipeline file (cgpipe [options] <file>),
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

// The removed -manifest* fan-out flags are no longer recognized options (sample
// sheets are read in-language via open().read_tsv() now).
func TestManifestFlagsRemoved(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	os.WriteFile("p.cgp", []byte("out.txt: {{\n    echo hi > ${output}\n}}\n@default: out.txt"), 0o644)
	for _, flag := range []string{"-manifest", "-manifest-tsv", "-manifest-csv", "-manifest-json", "-manifest-cgpipe"} {
		if code := run([]string{"p.cgp", flag, "samples.tsv"}); code != 2 {
			t.Errorf("run(p.cgp %s …) = %d, want 2 (unknown option)", flag, code)
		}
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
	if code := run([]string{"sub", "-dr", "-o", "out.txt", "echo hi > ${output}"}); code != 0 {
		t.Fatalf("cgp sub -dr = %d", code)
	}
	if fileThere(dir, "out.txt") {
		t.Error("cgp sub -dr should not create out.txt")
	}
}

// cgp sub --array submits the fan-out as ONE job array: a single --array=1-N
// header and a `case` over the scheduler's task-id var, one branch per file.
func TestSubArray(t *testing.T) {
	t.Chdir(t.TempDir())
	out := captureStdout(t, func() int {
		return run([]string{"sub", "-r", "slurm", "-dr", "-n", "qc", "--array", "echo {}", "--", "a", "b", "c"})
	})
	for _, want := range []string{
		"#SBATCH --array=1-3",
		`case "$SLURM_ARRAY_TASK_ID" in`,
		"1) echo a ;;", "2) echo b ;;", "3) echo c ;;",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("cgp sub --array output missing %q\n--- got ---\n%s", want, out)
		}
	}
}

// --array with a {}-expanded --after is rejected: a per-element dependency cannot
// be expressed by a single array submission's one dependency directive.
func TestSubArrayPerElementAfterRejected(t *testing.T) {
	t.Chdir(t.TempDir())
	if code := run([]string{"sub", "-r", "slurm", "-dr", "--array", "-a", "{@}.done", "echo {}", "--", "a", "b"}); code == 0 {
		t.Fatal("cgp sub --array with a {}-expanded --after should fail")
	}
}

// --array on a runner without array support falls back to one job per file.
func TestSubArrayUnsupportedRunnerFallsBack(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	os.WriteFile("a.in", []byte("A\n"), 0o644)
	os.WriteFile("b.in", []byte("B\n"), 0o644)
	// default shell runner has no arrays → per-file fallback still produces outputs.
	if code := run([]string{"sub", "--array", "-o", "{@.in}.out", "cp {} {@.in}.out", "--", "a.in", "b.in"}); code != 0 {
		t.Fatalf("cgp sub --array shell fallback = %d, want 0", code)
	}
	for _, f := range []string{"a.out", "b.out"} {
		if !fileThere(dir, f) {
			t.Errorf("array fallback did not produce %s", f)
		}
	}
}

// touchNewer sets path's mtime a minute in the future so it counts as newer than
// files just created alongside it — used to mark a task's output as up to date.
func touchNewer(t *testing.T, path string) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	newer := fi.ModTime().Add(time.Minute)
	if err := os.Chtimes(path, newer, newer); err != nil {
		t.Fatal(err)
	}
}

// captureOutErr runs fn with both stdout and stderr redirected, returning them
// plus the exit code (unlike captureStdout it does not fail on a non-zero code).
func captureOutErr(t *testing.T, fn func() int) (string, string, int) {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout, os.Stderr = wOut, wErr
	code := fn()
	wOut.Close()
	wErr.Close()
	os.Stdout, os.Stderr = oldOut, oldErr
	var bo, be bytes.Buffer
	bo.ReadFrom(rOut)
	be.ReadFrom(rErr)
	return bo.String(), be.String(), code
}

// cgp sub --array skips tasks whose -o output is already up to date, so only the
// missing indices land in the --array spec and the case body.
func TestSubArraySparseSkipsUpToDate(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, f := range []string{"a", "b", "c"} {
		os.WriteFile(f, []byte("x\n"), 0o644)
	}
	// tasks 2 (b) and 3 (c) already done: output newer than the input.
	for _, o := range []string{"b.out", "c.out"} {
		os.WriteFile(o, []byte("done\n"), 0o644)
		touchNewer(t, o)
	}
	out, errOut, code := captureOutErr(t, func() int {
		return run([]string{"sub", "-r", "slurm", "-dr", "--array", "-o", "{}.out", "cmd {}", "--", "a", "b", "c"})
	})
	if code != 0 {
		t.Fatalf("cgp sub --array = %d", code)
	}
	if !strings.Contains(out, "#SBATCH --array=1") || strings.Contains(out, "--array=1-3") {
		t.Errorf("expected sparse --array=1, got:\n%s", out)
	}
	if !strings.Contains(out, "1) cmd a ;;") {
		t.Errorf("expected task 1 branch, got:\n%s", out)
	}
	if strings.Contains(out, "2) cmd b") || strings.Contains(out, "3) cmd c") {
		t.Errorf("up-to-date tasks 2/3 should be dropped, got:\n%s", out)
	}
	for _, want := range []string{"# skip: array task 2 (b)", "# skip: array task 3 (c)"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("expected stderr skip line %q, got:\n%s", want, errOut)
		}
	}
}

// When every array task is already up to date, nothing is submitted.
func TestSubArrayAllUpToDate(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, f := range []string{"a", "b", "c"} {
		os.WriteFile(f, []byte("x\n"), 0o644)
		os.WriteFile(f+".out", []byte("done\n"), 0o644)
		touchNewer(t, f+".out")
	}
	out, errOut, code := captureOutErr(t, func() int {
		return run([]string{"sub", "-r", "slurm", "-dr", "--array", "-o", "{}.out", "cmd {}", "--", "a", "b", "c"})
	})
	if code != 0 {
		t.Fatalf("cgp sub --array = %d", code)
	}
	if strings.Contains(out, "#SBATCH") || strings.Contains(out, "case ") {
		t.Errorf("nothing should be submitted, got stdout:\n%s", out)
	}
	if !strings.Contains(errOut, "all 3 array tasks already up to date") {
		t.Errorf("expected all-up-to-date note on stderr, got:\n%s", errOut)
	}
}

// The plain per-file fan-out skips a file whose -o output is already up to date.
func TestSubFanoutSkipsUpToDate(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	os.WriteFile("a.in", []byte("A\n"), 0o644)
	os.WriteFile("b.in", []byte("B\n"), 0o644)
	// a is already done (a.out newer than a.in); b still needs to run.
	os.WriteFile("a.out", []byte("A\n"), 0o644)
	touchNewer(t, "a.out")
	_, errOut, code := captureOutErr(t, func() int {
		return run([]string{"sub", "-o", "{@.in}.out", "cp {} {@.in}.out", "--", "a.in", "b.in"})
	})
	if code != 0 {
		t.Fatalf("cgp sub fan-out = %d", code)
	}
	if !strings.Contains(errOut, "# skip: a.in") {
		t.Errorf("expected skip line for a.in on stderr, got:\n%s", errOut)
	}
	if !fileThere(dir, "b.out") {
		t.Error("fan-out did not produce b.out")
	}
}

// §15.2 cgp ledger vacuum runs against a ledger db.
func TestLedgerVacuumCLI(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "l.db")
	// create the ledger by submitting a one-off job that records an output.
	t.Chdir(dir)
	if code := run([]string{"sub", "-l", db, "-o", "out.txt", "echo hi > ${output}"}); code != 0 {
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
