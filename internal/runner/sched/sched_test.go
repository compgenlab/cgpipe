package sched

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/compgen-io/cgp/internal/eval"
	"github.com/compgen-io/cgp/internal/ledger"
	"github.com/compgen-io/cgp/internal/parser"
)

func program(t *testing.T, src string) *eval.Program {
	t.Helper()
	f, err := parser.Parse(src, "t.cgp")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	p, err := eval.Run(f, eval.Options{Out: io.Discard})
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	return p
}

// renderDry runs a scheduler in dry-run mode and returns the rendered scripts.
func renderDry(t *testing.T, name, src string) string {
	t.Helper()
	sch, ok := For(name)
	if !ok {
		t.Fatalf("unknown scheduler %q", name)
	}
	var buf bytes.Buffer
	if err := Run(program(t, src), sch, Options{DryRun: true, Dir: t.TempDir(), Out: &buf}); err != nil {
		t.Fatalf("run: %v", err)
	}
	return buf.String()
}

const oneTarget = `out.bam: {{
    job.name = "align"
    job.mem = "8G"
    job.procs = 4
    job.walltime = "12:00:00"
    job.queue = "normal"
    --
    echo hi > ${output}
}}
@default: out.bam`

func mustContainAll(t *testing.T, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("rendered script missing %q in:\n%s", w, got)
		}
	}
}

func TestSlurmRender(t *testing.T) {
	mustContainAll(t, renderDry(t, "slurm", oneTarget),
		"#!/bin/bash", "#SBATCH -J align", "#SBATCH -t 12:00:00",
		"#SBATCH -c 4", "#SBATCH -n 1", "#SBATCH --mem=8000", "#SBATCH -p normal",
		"echo hi > out.bam")
}

func TestSgeRender(t *testing.T) {
	// procs>1 needs a parallel environment to emit -pe; h_vmem always emitted.
	mustContainAll(t, renderDry(t, "sge", oneTarget),
		"#$ -terse", "#$ -N align", "#$ -l h_rt=12:00:00", "#$ -l h_vmem=8G", "#$ -q normal")
}

func TestPbsRender(t *testing.T) {
	mustContainAll(t, renderDry(t, "pbs", oneTarget),
		"#PBS -N align", "nodes=1:ppn=4", "walltime=12:00:00", "mem=8gb", "#PBS -q normal")
}

func TestBatchqRender(t *testing.T) {
	mustContainAll(t, renderDry(t, "batchq", oneTarget),
		"#BATCHQ -name align", "#BATCHQ -procs 4", "#BATCHQ -mem 8G",
		"#BATCHQ -walltime 12:00:00", "#BATCHQ -output out.bam")
}

func TestSgeNameSanitized(t *testing.T) {
	src := `out.bam: {{
    job.name = "align/chr1"
    --
    echo hi > ${output}
}}
@default: out.bam`
	mustContainAll(t, renderDry(t, "sge", src), "#$ -N align_chr1")
}

func TestDependencyThreadingDryRun(t *testing.T) {
	src := `a.bam: {{
    job.name = "a"
    --
    echo a > ${output}
}}
b.bam: a.bam {{
    job.name = "b"
    --
    cp ${input} ${output}
}}
@default: b.bam`
	// a is dryrun.1; b depends on it.
	mustContainAll(t, renderDry(t, "slurm", src), "#SBATCH -d afterok:dryrun.1")
	mustContainAll(t, renderDry(t, "pbs", src), "#PBS -W depend=afterok:dryrun.1")
	mustContainAll(t, renderDry(t, "sge", src), "#$ -hold_jid dryrun.1")
	mustContainAll(t, renderDry(t, "batchq", src), "#BATCHQ -afterok dryrun.1")
}

// When several inputs come from one upstream multi-output job, the dependency is
// wired once, not repeated (batchq rejects a duplicate afterok).
func TestDuplicateDepsDeduped(t *testing.T) {
	src := `a.bam a.bam.bai: {{
    job.name = "a"
    --
    echo a > a.bam
    echo i > a.bam.bai
}}
b.bam: a.bam a.bam.bai {{
    job.name = "b"
    --
    cp a.bam ${output}
}}
@default: b.bam`
	for _, sch := range []string{"slurm", "pbs", "sge", "batchq"} {
		out := renderDry(t, sch, src)
		if strings.Contains(out, "dryrun.1:dryrun.1") || strings.Contains(out, "dryrun.1 dryrun.1") {
			t.Errorf("%s: duplicate dependency not deduped:\n%s", sch, out)
		}
	}
	// the single dependency is still present
	mustContainAll(t, renderDry(t, "slurm", src), "#SBATCH -d afterok:dryrun.1")
}

// TestLedgerReuse runs the same pipeline twice with a ledger and a still-active
// mock job: the second run must reuse the existing job, not resubmit.
func TestLedgerReuse(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	scripts := filepath.Join(dir, "scripts")
	os.MkdirAll(bin, 0o755)
	os.MkdirAll(scripts, 0o755)
	counter := filepath.Join(dir, "counter")

	sbatch := "#!/bin/bash\n" +
		"n=$(cat \"$CGP_COUNTER\" 2>/dev/null || echo 1000); n=$((n+1)); echo $n > \"$CGP_COUNTER\"\n" +
		"cat > \"$CGP_SCRIPTS/job.$n\"\n" +
		"echo $n\n"
	if err := os.WriteFile(filepath.Join(bin, "sbatch"), []byte(sbatch), 0o755); err != nil {
		t.Fatal(err)
	}
	// scontrol reports the job as RUNNING (active) so the second run reuses it.
	scontrol := "#!/bin/bash\necho 'JobState=RUNNING Reason=None'\n"
	if err := os.WriteFile(filepath.Join(bin, "scontrol"), []byte(scontrol), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CGP_COUNTER", counter)
	t.Setenv("CGP_SCRIPTS", scripts)

	ledgerPath := filepath.Join(dir, "ledger.db")
	src := "cgp.ledger = \"" + ledgerPath + "\"\n" +
		"a.bam: {{\n    job.name = \"a\"\n    --\n    echo a > ${output}\n}}\n@default: a.bam"
	sch, _ := For("slurm")

	// Run 1: nothing in the ledger -> submit (job 1001).
	var out1 bytes.Buffer
	if err := Run(program(t, src), sch, Options{Dir: dir, Out: &out1}); err != nil {
		t.Fatalf("run 1: %v", err)
	}
	if c := readFile(t, counter); c != "1001" {
		t.Fatalf("after run 1 counter = %q, want 1001 (one submit)", c)
	}

	// Run 2: a.bam still doesn't exist (mock didn't run the body), so it's stale,
	// but job 1001 is still active -> reuse, no new submit.
	var out2 bytes.Buffer
	if err := Run(program(t, src), sch, Options{Dir: dir, Out: &out2}); err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if c := readFile(t, counter); c != "1001" {
		t.Fatalf("after run 2 counter = %q, want 1001 (should reuse, not resubmit)", c)
	}
	if !strings.Contains(out2.String(), "reuse") {
		t.Errorf("run 2 output should note reuse:\n%s", out2.String())
	}
}

// TestSubmitOneAfterDep checks that `cgp sub`-style submission resolves an
// -after output to the active owning job in the ledger and wires it as afterok.
func TestSubmitOneAfterDep(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	scripts := filepath.Join(dir, "scripts")
	os.MkdirAll(bin, 0o755)
	os.MkdirAll(scripts, 0o755)
	counter := filepath.Join(dir, "counter")
	sbatch := "#!/bin/bash\n" +
		"n=$(cat \"$CGP_COUNTER\" 2>/dev/null || echo 1000); n=$((n+1)); echo $n > \"$CGP_COUNTER\"\n" +
		"cat > \"$CGP_SCRIPTS/job.$n\"\n" +
		"echo $n\n"
	os.WriteFile(filepath.Join(bin, "sbatch"), []byte(sbatch), 0o755)
	os.WriteFile(filepath.Join(bin, "scontrol"), []byte("#!/bin/bash\necho 'JobState=RUNNING Reason=None'\n"), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CGP_COUNTER", counter)
	t.Setenv("CGP_SCRIPTS", scripts)

	ledgerPath := filepath.Join(dir, "ledger.db")
	// Pre-record an existing job that owns dep.bam.
	lg, err := ledger.Open(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	lg.Record(ledger.Job{JobID: "777", Outputs: []string{"dep.bam"}})
	lg.Close()

	prog := eval.NewJob(eval.JobSpec{
		Command:  "echo > ${output}",
		Name:     "j",
		Outputs:  []string{"out.bam"},
		Settings: map[string]eval.Value{"cgp.ledger": eval.StrVal(ledgerPath)},
	})
	sch, _ := For("slurm")
	var out bytes.Buffer
	id, err := SubmitOne(prog, sch, prog.Targets[0], nil, []string{"dep.bam"}, Options{Dir: dir, Out: &out})
	if err != nil {
		t.Fatalf("SubmitOne: %v", err)
	}
	if id != "1001" {
		t.Fatalf("job id = %q, want 1001", id)
	}
	script, err := os.ReadFile(filepath.Join(scripts, "job.1001"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), "afterok:777") {
		t.Errorf("submitted script missing afterok:777 (the -after owner):\n%s", script)
	}
}

// TestExternalDepWiresAfterok: a target whose input is produced by an active
// ledger job (an earlier stage still queued) depends on it via afterok.
func TestExternalDepWiresAfterok(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	scripts := filepath.Join(dir, "scripts")
	os.MkdirAll(bin, 0o755)
	os.MkdirAll(scripts, 0o755)
	counter := filepath.Join(dir, "counter")
	os.WriteFile(filepath.Join(bin, "sbatch"), []byte("#!/bin/bash\n"+
		"n=$(cat \"$CGP_COUNTER\" 2>/dev/null || echo 1000); n=$((n+1)); echo $n > \"$CGP_COUNTER\"\n"+
		"cat > \"$CGP_SCRIPTS/job.$n\"\necho $n\n"), 0o755)
	os.WriteFile(filepath.Join(bin, "scontrol"), []byte("#!/bin/bash\necho 'JobState=RUNNING Reason=None'\n"), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CGP_COUNTER", counter)
	t.Setenv("CGP_SCRIPTS", scripts)

	ledgerPath := filepath.Join(dir, "ledger.db")
	lg, err := ledger.Open(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	lg.Record(ledger.Job{JobID: "777", Outputs: []string{"dep.bam"}}) // earlier stage owns dep.bam
	lg.Close()

	// out.bam depends on dep.bam, which is not on disk (still queued as job 777).
	prog := eval.NewJob(eval.JobSpec{
		Command:  "process ${input} > ${output}",
		Outputs:  []string{"out.bam"},
		Inputs:   []string{"dep.bam"},
		Settings: map[string]eval.Value{"cgp.ledger": eval.StrVal(ledgerPath)},
	})
	sch, _ := For("slurm")
	var out bytes.Buffer
	if err := Run(prog, sch, Options{Dir: dir, Out: &out}); err != nil {
		t.Fatalf("run: %v", err)
	}
	script, err := os.ReadFile(filepath.Join(scripts, "job.1001"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), "afterok:777") {
		t.Errorf("out.bam's job should depend on dep.bam's owner 777:\n%s", script)
	}
}

// TestLedgerReuseChecksAllOutputs: a multi-output target whose first output is
// unowned but whose second output is owned by an active job must reuse that job
// rather than resubmit (the reuse check considers every output, not just [0]).
func TestLedgerReuseChecksAllOutputs(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	os.MkdirAll(bin, 0o755)
	counter := filepath.Join(dir, "counter")
	// sbatch records a submit by creating/bumping the counter; if reuse works it
	// is never called, so the counter file stays absent.
	os.WriteFile(filepath.Join(bin, "sbatch"), []byte("#!/bin/bash\n"+
		"n=$(cat \"$CGP_COUNTER\" 2>/dev/null || echo 1000); n=$((n+1)); echo $n > \"$CGP_COUNTER\"\necho $n\n"), 0o755)
	os.WriteFile(filepath.Join(bin, "scontrol"), []byte("#!/bin/bash\necho 'JobState=RUNNING Reason=None'\n"), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CGP_COUNTER", counter)

	ledgerPath := filepath.Join(dir, "ledger.db")
	lg, err := ledger.Open(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	lg.Record(ledger.Job{JobID: "777", Outputs: []string{"second.bam"}}) // owns only the 2nd output
	lg.Close()

	prog := eval.NewJob(eval.JobSpec{
		Command:  "echo > ${output}",
		Outputs:  []string{"first.bam", "second.bam"},
		Settings: map[string]eval.Value{"cgp.ledger": eval.StrVal(ledgerPath)},
	})
	sch, _ := For("slurm")
	var out bytes.Buffer
	id, err := SubmitOne(prog, sch, prog.Targets[0], nil, nil, Options{Dir: dir, Out: &out})
	if err != nil {
		t.Fatalf("SubmitOne: %v", err)
	}
	if id != "777" {
		t.Fatalf("job id = %q, want 777 (reuse of the 2nd output's active owner)", id)
	}
	if _, err := os.Stat(counter); err == nil {
		t.Errorf("sbatch was called (counter exists) — should have reused, not resubmitted")
	}
	if !strings.Contains(out.String(), "reuse") {
		t.Errorf("output should note reuse:\n%s", out.String())
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

// TestSubmitWithMock submits to a mock `sbatch` on PATH, verifying job-id capture
// and that the dependent job's script carries the upstream id as afterok:.
func TestSubmitWithMock(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	scripts := filepath.Join(dir, "scripts")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(scripts, 0o755); err != nil {
		t.Fatal(err)
	}
	counter := filepath.Join(dir, "counter")
	mock := "#!/bin/bash\n" +
		"n=$(cat \"$CGP_COUNTER\" 2>/dev/null || echo 1000); n=$((n+1)); echo $n > \"$CGP_COUNTER\"\n" +
		"cat > \"$CGP_SCRIPTS/job.$n\"\n" +
		"echo $n\n"
	if err := os.WriteFile(filepath.Join(bin, "sbatch"), []byte(mock), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CGP_COUNTER", counter)
	t.Setenv("CGP_SCRIPTS", scripts)

	src := `a.bam: {{
    job.name = "a"
    --
    echo a > ${output}
}}
b.bam: a.bam {{
    job.name = "b"
    --
    cp ${input} ${output}
}}
@default: b.bam`
	sch, _ := For("slurm")
	var out bytes.Buffer
	if err := Run(program(t, src), sch, Options{Dir: dir, Out: &out}); err != nil {
		t.Fatalf("run: %v", err)
	}

	ids := strings.Fields(out.String())
	if len(ids) != 2 || ids[0] != "1001" || ids[1] != "1002" {
		t.Fatalf("job ids = %v, want [1001 1002]", ids)
	}
	// the second job (1002) must depend on the first (1001)
	job2, err := os.ReadFile(filepath.Join(scripts, "job.1002"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(job2), "afterok:1001") {
		t.Errorf("job.1002 missing afterok:1001:\n%s", job2)
	}
}

// --- custom submission templates --------------------------------------------

const customTmpl = "#!${job.shell}\n# SITE TEMPLATE\n#SBATCH -J ${job.name}\n${_body}\n"

// writeTmpl writes a custom template file and returns its path.
func writeTmpl(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "my.template.cgp")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestCustomTemplateExplicitKey: cgp.runner.<name>.template overrides the
// built-in submission script while keeping the rest of the runner wiring.
func TestCustomTemplateExplicitKey(t *testing.T) {
	tmpl := writeTmpl(t, t.TempDir(), customTmpl)
	src := "cgp.runner.slurm.template = \"" + tmpl + "\"\n" + oneTarget
	got := renderDry(t, "slurm", src)
	mustContainAll(t, got, "# SITE TEMPLATE", "#SBATCH -J align", "echo hi > out.bam")
	if strings.Contains(got, "set -eo pipefail") {
		t.Errorf("custom template should replace the built-in boilerplate, but rendered:\n%s", got)
	}
}

// TestCustomTemplateConventionFile: ~/.cgp/custom_template.cgp is honored with no
// config key, applied to whichever scheduler runner is active.
func TestCustomTemplateConventionFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".cgp"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".cgp", "custom_template.cgp"), []byte(customTmpl), 0o644); err != nil {
		t.Fatal(err)
	}
	got := renderDry(t, "slurm", oneTarget)
	mustContainAll(t, got, "# SITE TEMPLATE", "#SBATCH -J align")
}

// TestCustomTemplatePrecedence: the explicit config key wins over the convention
// file.
func TestCustomTemplatePrecedence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.MkdirAll(filepath.Join(home, ".cgp"), 0o755)
	os.WriteFile(filepath.Join(home, ".cgp", "custom_template.cgp"),
		[]byte("#!${job.shell}\n# CONVENTION\n${_body}\n"), 0o644)
	tmpl := writeTmpl(t, t.TempDir(), "#!${job.shell}\n# EXPLICIT\n${_body}\n")
	src := "cgp.runner.slurm.template = \"" + tmpl + "\"\n" + oneTarget
	got := renderDry(t, "slurm", src)
	if !strings.Contains(got, "# EXPLICIT") || strings.Contains(got, "# CONVENTION") {
		t.Errorf("explicit key should win over the convention file, rendered:\n%s", got)
	}
}

// TestCustomTemplateMissingFileErrors: a config key pointing at a missing file is
// a loud error naming the path and scheduler.
func TestCustomTemplateMissingFileErrors(t *testing.T) {
	src := "cgp.runner.slurm.template = \"/no/such/template.cgp\"\n" + oneTarget
	sch, _ := For("slurm")
	err := Run(program(t, src), sch, Options{DryRun: true, Dir: t.TempDir(), Out: io.Discard})
	if err == nil {
		t.Fatal("expected an error for a missing custom template, got nil")
	}
	if !strings.Contains(err.Error(), "/no/such/template.cgp") || !strings.Contains(err.Error(), "slurm") {
		t.Errorf("error should name the path and scheduler, got: %v", err)
	}
}

// TestDefaultTemplateAccessor backs `cgp show-template`.
func TestDefaultTemplateAccessor(t *testing.T) {
	if tmpl, ok := DefaultTemplate("slurm"); !ok || !strings.Contains(tmpl, "#SBATCH") {
		t.Errorf("DefaultTemplate(slurm) = %q, %v; want a non-empty slurm template", tmpl, ok)
	}
	if _, ok := DefaultTemplate("nope"); ok {
		t.Error("DefaultTemplate(nope) should report not-ok")
	}
}

// TestSchedulerNamesMatchMap guards against drift between the Names() display
// list and the schedulers definition map.
func TestSchedulerNamesMatchMap(t *testing.T) {
	if len(schedulerNames) != len(schedulers) {
		t.Fatalf("schedulerNames has %d entries, schedulers map has %d", len(schedulerNames), len(schedulers))
	}
	for _, n := range schedulerNames {
		if _, ok := schedulers[n]; !ok {
			t.Errorf("Names() lists %q but it is not in the schedulers map", n)
		}
	}
}
