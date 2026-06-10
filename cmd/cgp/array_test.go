package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

// writeCGP writes a pipeline file in the current (temp) dir.
func writeCGP(t *testing.T, name, body string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

const arrayPipeline = `chroms = ["chr1", "chr2", "chr3"]
calls = []
for c in chroms with i {
    calls += "calls.${c}.vcf"
    ^calls.${c}.vcf: aligned.bam {{
        job.array = i
        job.name  = "call"
        --
        call_variants --region ${c} ${input} > ${output}
    }}
}
merged.vcf: @{calls} {{
    --
    bcftools concat ${input} > ${output}
}}
@default: merged.vcf
`

// A for-loop marked with job.array submits as one scheduler array (--array=1-N)
// and the gather depends on each element's per-task id (afterok:<id>_<i>).
func TestPipelineArraySubmitsOneArray(t *testing.T) {
	t.Chdir(t.TempDir())
	os.WriteFile("aligned.bam", []byte("x\n"), 0o644)
	writeCGP(t, "p.cgp", arrayPipeline)
	out := captureStdout(t, func() int { return run([]string{"-dr", "-r", "slurm", "p.cgp"}) })
	for _, want := range []string{
		"#SBATCH --array=1,2,3",
		`case "$SLURM_ARRAY_TASK_ID" in`,
		"call_variants --region chr2 aligned.bam > calls.chr2.vcf",
		"#SBATCH -d afterok:dryrun.1_1:dryrun.1_2:dryrun.1_3", // gather → per-task ids
	} {
		if !strings.Contains(out, want) {
			t.Errorf("array dry-run missing %q\n--- got ---\n%s", want, out)
		}
	}
}

// On a restart, only the stale elements are submitted (sparse --array), and the
// gather depends only on the resubmitted tasks; up-to-date elements are on disk.
func TestPipelineArraySparseRestart(t *testing.T) {
	t.Chdir(t.TempDir())
	os.WriteFile("aligned.bam", []byte("x\n"), 0o644)
	writeCGP(t, "p.cgp", arrayPipeline)
	// chr2 already up to date (output newer than the input).
	os.WriteFile("calls.chr2.vcf", []byte("done\n"), 0o644)
	fi, err := os.Stat("aligned.bam")
	if err != nil {
		t.Fatal(err)
	}
	newer := fi.ModTime().Add(time.Minute)
	os.Chtimes("calls.chr2.vcf", newer, newer)
	out := captureStdout(t, func() int { return run([]string{"-dr", "-r", "slurm", "p.cgp"}) })
	if !strings.Contains(out, "#SBATCH --array=1,3") {
		t.Errorf("expected sparse --array=1,3, got:\n%s", out)
	}
	if !strings.Contains(out, "#SBATCH -d afterok:dryrun.1_1:dryrun.1_3") {
		t.Errorf("expected gather dep on tasks 1,3 only, got:\n%s", out)
	}
}

// An element-wise array→array edge (each downstream task depends on the matching
// upstream task) needs aftercorr, which isn't supported yet → a clear error.
func TestPipelineArrayElementWiseErrors(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, f := range []string{"in.chr1", "in.chr2"} {
		os.WriteFile(f, []byte("x\n"), 0o644)
	}
	writeCGP(t, "p.cgp", `chroms = ["chr1", "chr2"]
for c in chroms with i {
    ^a.${c}: in.${c} {{
        job.array = i
        job.name = "a"
        --
        echo ${input} > ${output}
    }}
}
outs = []
for c in chroms with i {
    outs += "b.${c}"
    ^b.${c}: a.${c} {{
        job.array = i
        job.name = "b"
        --
        cp ${input} ${output}
    }}
}
done: @{outs} {{
    --
    cat ${input} > ${output}
}}
@default: done
`)
	if code := run([]string{"-dr", "-r", "slurm", "p.cgp"}); code == 0 {
		t.Fatal("element-wise array→array should error (needs aftercorr)")
	}
}

// Array elements must be submission-compatible: differing per-element resources
// can't share one array submission → error.
func TestPipelineArrayDivergingResourcesError(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, f := range []string{"in.chr1", "in.chr2"} {
		os.WriteFile(f, []byte("x\n"), 0o644)
	}
	writeCGP(t, "p.cgp", `chroms = ["chr1", "chr2"]
for c in chroms with i {
    ^x.${c}: in.${c} {{
        job.array = i
        job.name = "x"
        job.procs = i
        --
        echo ${input} > ${output}
    }}
}
all: x.chr1 x.chr2 {{
    --
    cat ${input} > ${output}
}}
@default: all
`)
	if code := run([]string{"-dr", "-r", "slurm", "p.cgp"}); code == 0 {
		t.Fatal("diverging per-element resources should error")
	}
}

// Two elements resolving to the same job.array index is a duplicate → error.
func TestPipelineArrayDuplicateIndexError(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, f := range []string{"in.chr1", "in.chr2"} {
		os.WriteFile(f, []byte("x\n"), 0o644)
	}
	writeCGP(t, "p.cgp", `chroms = ["chr1", "chr2"]
for c in chroms {
    ^y.${c}: in.${c} {{
        job.array = 1
        job.name = "y"
        --
        echo ${input} > ${output}
    }}
}
all: y.chr1 y.chr2 {{
    --
    cat ${input} > ${output}
}}
@default: all
`)
	if code := run([]string{"-dr", "-r", "slurm", "p.cgp"}); code == 0 {
		t.Fatal("duplicate job.array index should error")
	}
}
