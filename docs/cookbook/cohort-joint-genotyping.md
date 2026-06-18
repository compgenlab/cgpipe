# Cohort joint genotyping

Combine many per-sample gVCFs (from the [DNA-seq recipe](dnaseq-variant-calling.md))
into one jointly-genotyped cohort VCF. This shows two cgpipe patterns: **gathering many
inputs into one output**, and chaining the per-sample calling and the joint step
into a single command with a **`stage` workflow**.

> Requires: `gatk`.

## The joint step

```
#!/usr/bin/env cgpipe
#
# Joint genotyping: combine many per-sample gVCFs into one cohort VCF. Pass each
# gVCF with a repeated flag, which cgpipe collects into a list:
#
#     cgpipe joint.cgp --ref genome.fa --gvcf A.g.vcf.gz --gvcf B.g.vcf.gz --gvcf C.g.vcf.gz

if !ref  { print "ERROR: --ref required"; exit 1 }
if !gvcf { print "ERROR: at least one --gvcf required"; exit 1 }

intervals ?= "chr1 chr2 chr3 chrX".split(" ")

# Gather: import all sample gVCFs into a GenomicsDB, then joint-genotype.
cohort.vcf.gz: @{gvcf} ${ref} {{
    job.name = "joint-genotype"
    job.mem  = "32G"
    --
    gatk GenomicsDBImport ${if gvcf; "-V " + gvcf.join(" -V ")} \
        --genomicsdb-workspace-path cohort_db \
        -L ${intervals.join(" -L ")}
    gatk GenotypeGVCFs -R ${ref} -V gendb://cohort_db -O ${output}
}}
@default: cohort.vcf.gz
```

```sh
cgpipe -r slurm joint.cgp --ref genome.fa \
    --gvcf A.g.vcf.gz --gvcf B.g.vcf.gz --gvcf C.g.vcf.gz
```

### What it shows

- **Gather many → one.** `@{gvcf}` lists every sample gVCF as an input, and
  `${if gvcf; "-V " + gvcf.join(" -V ")}` renders them as `-V A -V B -V C` for
  `GenomicsDBImport`. A **repeated `--gvcf` flag** on the command line builds the
  list. The interval list collapses the same way (`-L chr1 -L chr2 …`).
- The cohort VCF depends on every gVCF, so cgpipe won't run the joint step until all of
  them exist — or, with a ledger, until the jobs producing them have finished.

## Wiring it after per-sample calling (a `stage` workflow)

To call every sample and joint-genotype in one command, chain them with stages.
Each calling stage **exports** its gVCF path, which the joint stage gathers:

```
#!/usr/bin/env cgpipe
#
# Cohort workflow: call each sample, then joint-genotype.
#     cgpipe cohort.cgp --ref genome.fa
#
# For a large cohort, generate the call stages from a sample sheet rather than
# listing them by hand. On a scheduler, set cgpipe.ledger so the joint stage waits on
# the still-queued call jobs.

if !ref { print "ERROR: --ref required"; exit 1 }

stage callA call-one.cgp --sample A --r1 A_R1.fq.gz --r2 A_R2.fq.gz --ref ${ref}
stage callB call-one.cgp --sample B --r1 B_R1.fq.gz --r2 B_R2.fq.gz --ref ${ref}
stage joint joint.cgp --ref ${ref} --gvcf ${callA.gvcf} --gvcf ${callB.gvcf}
```

where each per-sample pipeline ends with an `export`:

```
# call-one.cgp (use the full DNA-seq recipe in practice)
${sample}.g.vcf.gz: ${r1} ${r2} ${ref} {{
    job.name = "call-${sample}"
    --
    bwa mem ${ref} ${r1} ${r2} | samtools sort -o ${sample}.bam -
    gatk HaplotypeCaller -R ${ref} -I ${sample}.bam -ERC GVCF -O ${output}
}}
@default: ${sample}.g.vcf.gz
export gvcf = "${sample}.g.vcf.gz"
```

### What it shows

- **`stage` / `export`.** Each `call-one.cgp` is a normal, independently runnable
  pipeline; `export gvcf = "..."` exposes its output as `${callA.gvcf}` to the
  workflow without changing standalone behavior.
- **Cross-stage dependencies.** On a scheduler, configure `cgpipe.ledger` so the joint
  stage's `afterok` is wired onto the still-queued calling jobs (see
  [The Ledger](../11-The_Ledger.md)).

## Adapt it

- For large cohorts, generate the `stage` lines (or the `--gvcf` flags) from your
  sample sheet rather than hand-listing them — loop over
  `open(sheet).read_tsv(header=true)` and emit one `stage` per row, accumulating each
  exported gVCF into a list to gather (see the [Sample Sheets chapter](../13-Sample_Sheets.md)).
- Add `VariantRecalibrator`/`ApplyVQSR` (VQSR) or hard-filtering as a downstream
  target on `cohort.vcf.gz`.

See [Workflows](../12-Workflows.md) and
[Tutorial 12: Stage workflows](../tutorials/12-stage-workflow.md).
