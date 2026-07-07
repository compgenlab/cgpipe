package spectest

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/compgenlab/cgpipe/internal/runner/sched"
)

// pkgDir is the package directory (where testdata/ lives), captured before any
// test chdirs elsewhere.
var pkgDir string

func init() { pkgDir, _ = os.Getwd() }

// installMocks lays out the mock scheduler binaries for sched on a temp PATH and
// points the capture env at a fresh directory. It returns the capture directory,
// where each call writes submit-<n>.argv / submit-<n>.stdin / status-<n>.argv /
// release-<n>.argv.
func installMocks(t *testing.T, scheduler string) string {
	t.Helper()
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	capture := filepath.Join(root, "capture")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(capture, 0o755); err != nil {
		t.Fatal(err)
	}
	// lib.sh sits one level above binDir (the mocks source ../lib.sh).
	copyExec(t, filepath.Join(pkgDir, "testdata", "mocks", "lib.sh"), filepath.Join(root, "lib.sh"))
	srcDir := filepath.Join(pkgDir, "testdata", "mocks", scheduler)
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		t.Fatalf("read mocks for %s: %v", scheduler, err)
	}
	for _, e := range entries {
		copyExec(t, filepath.Join(srcDir, e.Name()), filepath.Join(binDir, e.Name()))
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CGP_TEST_CAPTURE", capture)
	t.Setenv("CGP_TEST_JOBID_BASE", "1001")
	return capture
}

func copyExec(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	if err := os.WriteFile(dst, b, 0o755); err != nil {
		t.Fatalf("write %s: %v", dst, err)
	}
}

// captured reads a capture artifact, e.g. captured(t, dir, "submit-1.stdin").
func captured(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read capture %s: %v", name, err)
	}
	return string(b)
}

// submitChain is a two-target pipeline: b.bam depends on a.bam, so a submits
// first (job 1001) and b second (job 1002, depending on 1001).
const submitChain = `a.bam: {{
    job.name = "align"
    job.mem  = "8G"
    job.procs = 4
    job.walltime = "12:00:00"
    --
    echo a > ${output}
}}
b.bam: a.bam {{
    job.name = "post"
    --
    cp ${input} ${output}
}}
@default: b.bam`

// §10/§15 Submitting a pipeline to each scheduler: the rendered script reaches
// the submit tool on stdin, the job id is captured, and a dependent job carries
// the upstream id in the scheduler's dependency directive.
func TestSchedulerSubmissionMatrix(t *testing.T) {
	cases := []struct {
		sched     string
		directive string // appears in a.bam's submission script
		dep       string // appears in b.bam's script, wiring the 1001 dependency
	}{
		{"slurm", "#SBATCH -J align", "#SBATCH -d afterok:1001"},
		{"sge", "#$ -N align", "#$ -hold_jid 1001"},
		{"pbs", "#PBS -N align", "depend=afterok:1001.cluster1"},
		{"batchq", "#BATCHQ -name align", "#BATCHQ -afterok 1001"},
	}
	for _, c := range cases {
		t.Run(c.sched, func(t *testing.T) {
			capture := installMocks(t, c.sched)
			workdir := t.TempDir()
			prog, _ := build(t, submitChain, nil)
			sch, ok := sched.For(c.sched)
			if !ok {
				t.Fatalf("unknown scheduler %q", c.sched)
			}
			var out bytes.Buffer
			if err := sched.Run(prog, sch, sched.Options{Dir: workdir, Out: &out, Pipeline: "spec.cgp"}); err != nil {
				t.Fatalf("submit: %v", err)
			}
			// a.bam submitted first; its rendered script (stdin) carries directives.
			aScript := captured(t, capture, "submit-1.stdin")
			mustContain(t, aScript, c.directive, "echo a > a.bam")
			// b.bam submitted second; it depends on a's job id 1001.
			bScript := captured(t, capture, "submit-2.stdin")
			mustContain(t, bScript, c.dep)
			// both job ids are reported on stdout.
			if !strings.Contains(out.String(), "1001") || !strings.Contains(out.String(), "1002") {
				t.Errorf("job ids not reported: %q", out.String())
			}
		})
	}
}

// §7.6 On a scheduler an opportunistic job depends (afterok) on the jobs that
// produced its inputs this run, so guarded cleanup runs after them.
func TestOpportunisticSchedulerDep(t *testing.T) {
	capture := installMocks(t, "slurm")
	workdir := t.TempDir()
	src := `prod.bam: {{
    job.name = "prod"
    --
    echo x > ${output}
}}
: prod.bam {{
    job.name = "cleanup"
    --
    rm -f prod.bam
}}
@default: prod.bam`
	prog, _ := build(t, src, nil)
	sch, _ := sched.For("slurm")
	var out bytes.Buffer
	if err := sched.Run(prog, sch, sched.Options{Dir: workdir, Out: &out, Pipeline: "spec.cgp"}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	// prod submits first (1001); the opportunistic cleanup (1002) depends on it.
	cleanup := captured(t, capture, "submit-2.stdin")
	mustContain(t, cleanup, "#SBATCH -d afterok:1001", "rm -f prod.bam")
}

// §8 @postsubmit runs on the submit host after each submission, with the
// scheduler-assigned job id available as ${jobid}.
func TestPostsubmitSeesJobID(t *testing.T) {
	installMocks(t, "slurm")
	workdir := t.TempDir()
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
@postsubmit {{
    echo "${output} -> ${jobid}" >> ` + workdir + `/ps.log
}}
@default: b.bam`
	prog, _ := build(t, src, nil)
	sch, _ := sched.For("slurm")
	var out bytes.Buffer
	if err := sched.Run(prog, sch, sched.Options{Dir: workdir, Out: &out, Pipeline: "spec.cgp"}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	log := captured0(t, filepath.Join(workdir, "ps.log"))
	mustContain(t, log, "a.bam -> 1001", "b.bam -> 1002")
}

func captured0(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// §8 shexec runs a target's body on the submission host instead of submitting
// it: the @setup body executes locally (no sbatch call), while the normal target
// is still submitted.
func TestShexecRunsOnSubmitHost(t *testing.T) {
	capture := installMocks(t, "slurm")
	workdir := t.TempDir()
	src := `@setup {{
    job.shexec = true
    --
    echo ok > setup_marker.txt
}}
out.bam: {{
    job.name = "j"
    --
    echo x > ${output}
}}
@default: out.bam`
	prog, _ := build(t, src, nil)
	sch, _ := sched.For("slurm")
	var out bytes.Buffer
	if err := sched.Run(prog, sch, sched.Options{Dir: workdir, Out: &out, Pipeline: "spec.cgp"}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	// @setup ran locally (in the working dir), not through sbatch.
	if !fileExists(filepath.Join(workdir, "setup_marker.txt")) {
		t.Error("shexec @setup did not run on the submit host")
	}
	// exactly one job submitted — the normal target, not @setup.
	if n := submitCount(t, capture); n != 1 {
		t.Errorf("%d submits, want 1 (@setup is shexec, not submitted)", n)
	}
	mustContain(t, captured(t, capture, "submit-1.stdin"), "#SBATCH -J j")
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// The slurm scheduler exposes a normalized live State for the status report,
// parsed from `scontrol show job` JobState.
func TestSlurmState(t *testing.T) {
	resp := t.TempDir()
	installMocks(t, "slurm")
	// canned scontrol responses keyed by job id (the mock serves these when
	// CGP_TEST_RESPONSES is set)
	t.Setenv("CGP_TEST_RESPONSES", resp)
	if err := os.MkdirAll(filepath.Join(resp, "scontrol"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(id, body string) {
		if err := os.WriteFile(filepath.Join(resp, "scontrol", id), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("1", "JobState=PENDING Reason=Resources")
	write("2", "JobState=RUNNING Reason=None")
	write("3", "JobState=COMPLETED Reason=None")
	write("4", "JobState=FAILED Reason=NonZeroExitCode")
	// id 5 has no response file ⇒ scontrol exits non-zero ⇒ unknown ("")

	sch, _ := sched.For("slurm")
	if sch.State == nil {
		t.Fatal("slurm scheduler has no State func")
	}
	cases := []struct{ id, want string }{
		{"1", "queued"}, {"2", "running"}, {"3", "done"}, {"4", "failed"}, {"5", ""},
	}
	for _, c := range cases {
		if got := sch.State(c.id); got != c.want {
			t.Errorf("State(%s) = %q, want %q", c.id, got, c.want)
		}
	}
}

// §10.5 Cross-run reuse: with a ledger, a second run that finds the output still
// owned by an active job reuses it instead of resubmitting.
func TestLedgerReuseAcrossRuns(t *testing.T) {
	capture := installMocks(t, "slurm")
	workdir := t.TempDir()
	ledgerPath := filepath.Join(workdir, "ledger.db")
	src := "cgp.ledger = \"" + ledgerPath + "\"\n" +
		"a.bam: {{\n    job.name = \"a\"\n    --\n    echo a > ${output}\n}}\n@default: a.bam"
	sch, _ := sched.For("slurm")

	// Run 1: submits job 1001.
	prog, _ := build(t, src, nil)
	var out1 bytes.Buffer
	if err := sched.Run(prog, sch, sched.Options{Dir: workdir, Out: &out1, Pipeline: "spec.cgp"}); err != nil {
		t.Fatalf("run 1: %v", err)
	}
	if n := submitCount(t, capture); n != 1 {
		t.Fatalf("after run 1: %d submits, want 1", n)
	}

	// Run 2: a.bam still absent (mock didn't run the body) so it's stale, but job
	// 1001 is still active (scontrol reports RUNNING) ⇒ reuse, no new submit.
	prog2, _ := build(t, src, nil)
	var out2 bytes.Buffer
	if err := sched.Run(prog2, sch, sched.Options{Dir: workdir, Out: &out2, Pipeline: "spec.cgp"}); err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if n := submitCount(t, capture); n != 1 {
		t.Errorf("after run 2: %d submits, want 1 (should reuse the active job)", n)
	}
	if !strings.Contains(out2.String(), "reuse") {
		t.Errorf("run 2 should note reuse: %q", out2.String())
	}
}

// submitCount returns how many submit calls the mock recorded.
func submitCount(t *testing.T, capture string) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(capture, ".seq.submit"))
	if err != nil {
		return 0
	}
	return atoi(strings.TrimSpace(string(b)))
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return n
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// §11.3 global_hold submits every job held, then releases each after the whole
// pipeline submits cleanly.
func TestGlobalHoldReleases(t *testing.T) {
	capture := installMocks(t, "slurm")
	workdir := t.TempDir()
	src := "cgp.runner.slurm.global_hold = true\n" +
		"a.bam: {{\n    job.name = \"a\"\n    --\n    echo a > ${output}\n}}\n@default: a.bam"
	prog, _ := build(t, src, nil)
	sch, _ := sched.For("slurm")
	var out bytes.Buffer
	if err := sched.Run(prog, sch, sched.Options{Dir: workdir, Out: &out, Pipeline: "spec.cgp"}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	// submitted held (-H present in the submit argv)
	mustContain(t, captured(t, capture, "submit-1.argv"), "-H")
	// released afterwards (scontrol release 1001)
	rel := captured(t, capture, "release-1.argv")
	mustContain(t, rel, "release", "1001")
}

// §15 Submitting to a real batchq when one is installed — the point of batchq is
// to exercise cgpipe's submission path for real. Skipped when batchq is absent.
func TestRealBatchqIfPresent(t *testing.T) {
	if _, err := exec.LookPath("batchq"); err != nil {
		t.Skip("no real batchq on PATH; mock-backed coverage in TestSchedulerSubmissionMatrix")
	}
	workdir := t.TempDir()
	src := "a.txt: {{\n    job.name = \"a\"\n    --\n    echo hi > ${output}\n}}\n@default: a.txt"
	prog, _ := build(t, src, nil)
	sch, _ := sched.For("batchq")
	var out bytes.Buffer
	if err := sched.Run(prog, sch, sched.Options{Dir: workdir, Out: &out, Pipeline: "spec.cgp"}); err != nil {
		t.Fatalf("real batchq submit: %v", err)
	}
	if strings.TrimSpace(out.String()) == "" {
		t.Error("real batchq submission returned no job id")
	}
}
