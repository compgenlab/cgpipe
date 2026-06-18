# DNA-seq variant calling

Align reads, mark duplicates, recalibrate base qualities, then call variants on
each chromosome **in parallel** and merge the results. This is the recipe that best
shows off cgpipe's scatter-gather model — and how temporary files and opportunistic
cleanup keep the working directory tidy without breaking restarts.

> Requires: `bwa`, `samtools`, `gatk`. Needs an indexed reference (see
> [Reference preparation](reference-preparation.md)).

```
#!/usr/bin/env cgpipe
#
# DNA-seq: align, mark duplicates, recalibrate, then call variants per
# chromosome in parallel and merge. Intermediate BAMs/VCFs are marked temporary
# (^) and an opportunistic job removes them once the final gVCF exists.
#
# Options:
#     --sample NAME   sample id
#     --r1 FILE       read 1 FASTQ          --r2 FILE   read 2 FASTQ
#     --ref FILE      indexed reference FASTA (see the reference-prep recipe)

if !sample { print "ERROR: --sample required"; exit 1 }
if !ref    { print "ERROR: --ref required";    exit 1 }

chroms ?= "chr1 chr2 chr3 chrX".split(" ")   # list all your intervals here

# 1. Align to a coordinate-sorted BAM (temp: only needed to mark duplicates).
^${sample}.sorted.bam: ${r1} ${r2} ${ref} {{
    job.name  = "bwa-${sample}"
    job.procs = 8
    job.mem   = "16G"
    --
    bwa mem -t ${job.procs} -R "@RG\tID:${sample}\tSM:${sample}\tPL:ILLUMINA" \
        ${ref} ${r1} ${r2} \
        | samtools sort -@ ${job.procs} -o ${output} -
    samtools index ${output}
}}

# 2. Mark duplicates (temp: consumed by BQSR).
^${sample}.markdup.bam ${sample}.markdup.metrics: ${sample}.sorted.bam {{
    job.name = "markdup-${sample}"
    job.mem  = "16G"
    --
    gatk MarkDuplicates -I ${input} -O ${output[0]} -M ${output[1]}
    samtools index ${output[0]}
}}

# 3. Base-quality recalibration -> the analysis-ready BAM (kept).
${sample}.recal.bam: ${sample}.markdup.bam ${ref} {{
    job.name = "bqsr-${sample}"
    job.mem  = "16G"
    --
    gatk BaseRecalibrator -I ${input[0]} -R ${ref} -O ${sample}.recal.table
    gatk ApplyBQSR -I ${input[0]} -R ${ref} --bqsr-recal-file ${sample}.recal.table -O ${output}
    samtools index ${output}
}}

# 4. Scatter: call variants on each chromosome independently (temp per-chrom gVCF).
per_chrom = []
for c in chroms {
    per_chrom += "${sample}.${c}.g.vcf.gz"
    ^${sample}.${c}.g.vcf.gz: ${sample}.recal.bam ${ref} {{
        job.name  = "call-${sample}-${c}"
        job.procs = 2
        job.mem   = "8G"
        --
        gatk HaplotypeCaller -R ${ref} -I ${input[0]} -L ${c} -ERC GVCF -O ${output}
    }}
}

# 5. Gather the per-chromosome gVCFs into one (kept).
${sample}.g.vcf.gz: @{per_chrom} {{
    job.name = "gather-${sample}"
    job.mem  = "4G"
    --
    gatk MergeVcfs ${if per_chrom; "-I " + per_chrom.join(" -I ")} -O ${output}
}}
@default: ${sample}.g.vcf.gz

# 6. Opportunistic cleanup: once the final gVCF exists, drop the temp files.
: ${sample}.g.vcf.gz ${sample}.sorted.bam ${sample}.markdup.bam @{per_chrom} {{
    if [ -e ${sample}.g.vcf.gz ]; then
        rm -f ${sample}.sorted.bam ${sample}.sorted.bam.bai \
              ${sample}.markdup.bam ${sample}.markdup.bam.bai
% for v in per_chrom {
        rm -f ${v} ${v}.tbi
% }
    fi
}}
```

## What it shows

- **Scatter-gather.** The `for` loop (step 4) emits one `HaplotypeCaller` job per
  chromosome — the job count follows your interval list — and step 5 merges them.
  On a scheduler the per-chromosome jobs run in parallel and the gather waits on all
  of them, wired automatically from the `@{per_chrom}` inputs.
- **Temporary outputs (`^`).** The sorted BAM, the markdup BAM, and the per-chrom
  gVCFs are intermediates. Marking them `^` means that if you delete them, cgpipe's
  staleness check looks *through* them to the reads — so a restart rebuilds
  correctly instead of being confused by missing files. The recalibrated BAM and
  the final gVCF are kept (no `^`).
- **Opportunistic cleanup (step 6).** A target with no outputs (`: inputs`) runs only
  after everything else and only if its inputs exist. Guarded by
  `if [ -e ${sample}.g.vcf.gz ]`, it removes the temps **once the final gVCF is
  there** — never prematurely. cgpipe never auto-deletes files; cleanup is always this
  explicit.
- **Building a flag list.** `${if per_chrom; "-I " + per_chrom.join(" -I ")}` turns
  the gVCF list into `-I a -I b -I c` for `MergeVcfs`.

## Run it

```sh
# preview the whole DAG without running anything
cgpipe -dr dnaseq-variant-calling.cgp --sample NA12878 --r1 r1.fq.gz --r2 r2.fq.gz --ref genome.fa

# submit to SLURM (per-chromosome jobs fan out in parallel)
cgpipe -r slurm dnaseq-variant-calling.cgp --sample NA12878 --r1 r1.fq.gz --r2 r2.fq.gz --ref genome.fa
```

Override the interval list with `--chroms` (a repeated flag builds a list), or set
a default for your genome build in `~/.cgpipe/config`.

## Adapt it

- Add known-sites VCFs to `BaseRecalibrator` (`--known-sites`).
- For a single-sample callset, swap `-ERC GVCF` + `MergeVcfs` for direct VCF output;
  for a cohort, keep the gVCFs and feed them to
  [Cohort joint genotyping](cohort-joint-genotyping.md).
- Run it across many samples by wrapping the per-sample targets in a `for` loop over
  a [sample sheet](../13-Sample_Sheets.md) (`open(sheet).read_tsv(header=true)`).

See [Tutorial 4: Map-reduce](../tutorials/04-map-reduce.md) and
[Tutorial 5: Opportunistic cleanup](../tutorials/05-opportunistic-cleanup.md).
