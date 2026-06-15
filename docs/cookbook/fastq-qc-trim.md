# FASTQ QC & trimming

The usual first step: trim adapters and low-quality bases, and produce a QC
report. This recipe runs `fastp` (trim + report in one pass) and `FastQC` on the
trimmed reads, looping over every sample in a sample sheet so one run QCs the
whole cohort.

> Requires: `fastp`, `fastqc`.

```
#!/usr/bin/env cgp
#
# Read QC and adapter/quality trimming for a whole cohort. Reads a sample sheet
# and emits one fastp + FastQC chain per row:
#
#     cgp fastq-qc-trim.cgp --sheet samples.tsv
#
# samples.tsv has columns: sample, r1, r2

if !sheet { print "ERROR: --sheet required"; exit 1 }

samples = open(sheet).read_tsv(header=true)
qc = []
for row in samples {
    sample = row["sample"]
    qc += "${sample}.qc"

    # fastp does the trimming and emits a QC report in one pass.
    ${sample}.trim.R1.fq.gz ${sample}.trim.R2.fq.gz ${sample}.fastp.json: ${row["r1"]} ${row["r2"]} {{
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
}
@default: @{qc}
```

## What it shows

- **A multi-output target.** `fastp` produces two trimmed FASTQs *and* a JSON report
  in one job; the rule lists all three outputs and indexes them with
  `${output[0]}` … `${output[2]}`.
- **Sample-sheet scatter.** `open(sheet).read_tsv(header=true)` reads the sheet at
  eval time and returns one map per row; the `for` loop emits a fastp + FastQC chain
  per sample. A row column is read by name — `row["r1"]`, written as-is inside the
  target declaration — while `sample` is bound to a plain var first so it can be used
  inside `"..."` strings. See the [Sample Sheets chapter](../13-Sample_Sheets.md).
- **A sentinel aggregator** (`${sample}.qc`) groups each sample's two QC outputs into
  one goal; `@{qc}` gathers every sample's sentinel into the default goal.

## Run it

A cohort — `samples.tsv` with columns `sample`, `r1`, `r2`:

```
sample	r1	r2
s1	fq/s1_R1.fq.gz	fq/s1_R2.fq.gz
s2	fq/s2_R1.fq.gz	fq/s2_R2.fq.gz
```
```sh
cgp -r slurm fastq-qc-trim.cgp --sheet samples.tsv
```

## Adapt it

- Single-end reads: drop the `-I`/`-O` second pair and the `r2` input.
- Swap `fastp` for `Trim Galore` or `cutadapt` — the rule shape is unchanged.
- The trimmed FASTQs feed straight into the
  [DNA-seq](dnaseq-variant-calling.md) or [RNA-seq](rnaseq-quantification.md)
  recipes — point their reads at `${sample}.trim.R*.fq.gz`.

See [Tutorial 11: Sample sheets](../tutorials/11-sample-sheets.md).
