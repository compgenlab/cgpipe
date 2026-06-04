package sched

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/compgen-io/cgp/internal/eval"
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
    name = "align"
    mem = "8G"
    procs = 4
    walltime = "12:00:00"
    queue = "normal"
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
    name = "align/chr1"
    --
    echo hi > ${output}
}}
@default: out.bam`
	mustContainAll(t, renderDry(t, "sge", src), "#$ -N align_chr1")
}

func TestDependencyThreadingDryRun(t *testing.T) {
	src := `a.bam: {{
    name = "a"
    --
    echo a > ${output}
}}
b.bam: a.bam {{
    name = "b"
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
    name = "a"
    --
    echo a > ${output}
}}
b.bam: a.bam {{
    name = "b"
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
