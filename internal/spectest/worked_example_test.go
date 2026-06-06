package spectest

import (
	"strings"
	"testing"
)

// §16 An end-to-end capstone mirroring the worked example: a loop generates
// per-chromosome temp targets, a merge consumes them via @{list}, and a guarded
// opportunistic job cleans the temps up. Real tools (bcftools) are replaced with
// echo/cat so it runs anywhere, but the structure is the documented one.
func TestWorkedExampleEndToEnd(t *testing.T) {
	chdirTmp(t)
	writeFile(t, "in.bam", "reads")
	src := `chroms = "1 2 3".split(" ")
per_chrom = []
for c in chroms {
    per_chrom += "calls.${c}.vcf"

    ^calls.${c}.vcf: in.bam {{
        job.name = "call-chr${c}"
        --
        echo "variants on chr${c}" > ${output}
    }}
}

merged.vcf: @{per_chrom} {{
    job.name = "merge"
    --
    cat ${input} > ${output}
}}

# Guarded cleanup of the per-chromosome temp files.
: merged.vcf @{per_chrom} {{
    rm -f ${per_chrom}
}}

@default: merged.vcf`

	runReal(t, src, "merged.vcf")

	// the merge produced all three per-chrom results, in order
	got := readFile(t, "merged.vcf")
	for _, want := range []string{"variants on chr1", "variants on chr2", "variants on chr3"} {
		if !strings.Contains(got, want) {
			t.Errorf("merged.vcf missing %q in:\n%s", want, got)
		}
	}
	// the opportunistic cleanup removed the temps
	for _, tmp := range []string{"calls.1.vcf", "calls.2.vcf", "calls.3.vcf"} {
		if exists(tmp) {
			t.Errorf("temp %s should have been cleaned up", tmp)
		}
	}
}

// §16 The same pipeline renders cleanly in dry-run for a scheduler, producing one
// submission per per-chromosome job plus the merge, with the merge depending on
// all three.
func TestWorkedExampleSchedulerDryRun(t *testing.T) {
	chdirTmp(t)
	writeFile(t, "in.bam", "reads")
	src := `chroms = "1 2 3".split(" ")
per_chrom = []
for c in chroms {
    per_chrom += "calls.${c}.vcf"
    ^calls.${c}.vcf: in.bam {{
        job.name = "call-chr${c}"
        --
        echo x > ${output}
    }}
}
merged.vcf: @{per_chrom} {{
    job.name = "merge"
    --
    cat ${input} > ${output}
}}
@default: merged.vcf`
	out := dryRunShell(t, src) // shell dry-run is enough to confirm the graph renders
	for _, want := range []string{"calls.1.vcf", "calls.2.vcf", "calls.3.vcf", "cat calls.1.vcf calls.2.vcf calls.3.vcf > merged.vcf"} {
		if !strings.Contains(out, want) {
			t.Errorf("worked-example render missing %q in:\n%s", want, out)
		}
	}
}
