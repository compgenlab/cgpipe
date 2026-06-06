# FASTQ QC & trimming

The usual first step: trim adapters and low-quality bases, and produce a QC
report. This recipe runs `fastp` (trim + report in one pass) and `FastQC` on the
trimmed reads, per sample — and scales to a whole cohort with a manifest.

> Requires: `fastp`, `fastqc`.

```
#!/usr/bin/env cgp
#
# Per-sample read QC and adapter/quality trimming. Run one sample, or a whole
# cohort with a manifest:
#
#     cgp fastq-qc-trim.cgp --sample s1 --r1 fq/s1_R1.fq.gz --r2 fq/s1_R2.fq.gz
#     cgp fastq-qc-trim.cgp -manifest-tsv samples.tsv          # one run per row

if !sample { print "ERROR: --sample required"; exit 1 }

# fastp does the trimming and emits a QC report in one pass.
${sample}.trim.R1.fq.gz ${sample}.trim.R2.fq.gz ${sample}.fastp.json: ${r1} ${r2} {{
    job.name  = "fastp-${sample}"
    job.procs = 4
    job.mem   = "4G"
    --
    fastp -i ${input[0]} -I ${input[1]} \
          -o ${output[0]} -O ${output[1]} \
          --json ${output[2]} --thread ${job.procs}
}}

# FastQC on the trimmed reads (a sentinel HTML marks completion).
${sample}.trim.R1_fastqc.html: ${sample}.trim.R1.fq.gz {{
    job.name = "fastqc-${sample}"
    --
    fastqc ${input}
}}

${sample}.qc: ${sample}.fastp.json ${sample}.trim.R1_fastqc.html
@default: ${sample}.qc
```

## What it shows

- **A multi-output target.** `fastp` produces two trimmed FASTQs *and* a JSON report
  in one job; the rule lists all three outputs and indexes them with
  `${output[0]}` … `${output[2]}`.
- **Manifest fan-out.** Write the pipeline for one sample, then run it across a
  cohort with `-manifest-tsv` — each row's `sample`/`r1`/`r2` columns become the
  variables. See the [Manifests chapter](../12-Manifests_and_Fanout.md).
- **A sentinel aggregator** (`${sample}.qc`) groups the two QC outputs into one goal.

## Run it

One sample:

```sh
cgp fastq-qc-trim.cgp --sample s1 --r1 fq/s1_R1.fq.gz --r2 fq/s1_R2.fq.gz | bash
```

A cohort — `samples.tsv` with columns `sample`, `r1`, `r2`:

```
sample	r1	r2
s1	fq/s1_R1.fq.gz	fq/s1_R2.fq.gz
s2	fq/s2_R1.fq.gz	fq/s2_R2.fq.gz
```
```sh
cgp -r slurm fastq-qc-trim.cgp -manifest-tsv samples.tsv
```

## Adapt it

- Single-end reads: drop the `-I`/`-O` second pair and the `r2` input.
- Swap `fastp` for `Trim Galore` or `cutadapt` — the rule shape is unchanged.
- The trimmed FASTQs feed straight into the
  [DNA-seq](dnaseq-variant-calling.md) or [RNA-seq](rnaseq-quantification.md)
  recipes — point their `--r1`/`--r2` at `${sample}.trim.R*.fq.gz`.

See [Tutorial 11: Manifest fan-out](../tutorials/11-manifest-fanout.md).
