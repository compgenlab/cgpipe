# RNA-seq quantification

Align RNA-seq reads with STAR and count reads per gene with `ngsutilsj bam-count`.
A short per-sample chain that loops over a sample sheet to quantify a whole cohort
in one run.

> Requires: `STAR`, `samtools`, `ngsutilsj`. Needs a STAR genome index and a GTF
> annotation.

```
#!/usr/bin/env cgp
#
# RNA-seq quantification: align with STAR, then count reads per gene with
# ngsutilsj. Reads a sample sheet and emits an align+count chain per row:
#
#     cgp rnaseq-quantification.cgp --sheet samples.tsv \
#                    --index star_index --gtf genes.gtf
#
# samples.tsv has columns: sample, r1, r2

if !sheet { print "ERROR: --sheet required"; exit 1 }
if !index { print "ERROR: --index (STAR genome dir) required"; exit 1 }
if !gtf   { print "ERROR: --gtf required"; exit 1 }

samples = open(sheet).read_tsv(header=true)
counts = []
for row in samples {
    sample = row["sample"]
    counts += "${sample}.counts.txt"

    # Align to a coordinate-sorted BAM. STAR writes
    # <prefix>Aligned.sortedByCoord.out.bam; rename it to a tidy ${sample}.bam.
    ${sample}.bam: ${row["r1"]} ${row["r2"]} {{
        job.name  = "star-${sample}"
        job.procs = 8
        job.mem   = "32G"
        --
        STAR --genomeDir ${index} --readFilesIn ${input[0]} ${input[1]} --readFilesCommand zcat \
             --runThreadN ${job.procs} --outSAMtype BAM SortedByCoordinate \
             --outFileNamePrefix ${sample}.
        mv ${sample}.Aligned.sortedByCoord.out.bam ${output}
        samtools index ${output}
    }}

    # Count reads per gene against the annotation.
    ${sample}.counts.txt: ${sample}.bam {{
        job.name = "count-${sample}"
        job.mem  = "4G"
        --
        ngsutilsj bam-count --gtf ${gtf} ${input} > ${output}
    }}
}
@default: @{counts}
```

## What it shows

- **A clean per-sample chain.** Align → count, with the BAM the only intermediate.
  `${input}`/`${output}` keep the bodies free of hard-coded names.
- **Renaming a tool's fixed output.** STAR insists on its own output name; a `mv`
  inside the body normalizes it to `${sample}.bam` so the rest of the pipeline (and
  the next recipe) can refer to it predictably.
- **Sample-sheet scatter.** `open(sheet).read_tsv(header=true)` reads the sheet at
  eval time into one map per row; the `for` loop emits an align+count chain per
  sample. Per-sample columns come from `row["r1"]`/`row["r2"]`, while `--index` and
  `--gtf` are shared across every row — cgp's stat cache checks the shared index once,
  not once per sample.

## Run it

A cohort — `samples.tsv` with columns `sample`, `r1`, `r2`:

```sh
cgp -r slurm rnaseq-quantification.cgp --sheet samples.tsv \
    --index star_index --gtf genes.gtf
```

(`--index`/`--gtf` on the command line apply to every row; the sample sheet supplies
the per-sample columns.)

## Adapt it

- Build the STAR index once with a small reference-prep target
  (`STAR --runMode genomeGenerate`); have `${sample}.bam` depend on its sentinel so
  it's built first.
- Swap the counter for `featureCounts`, `salmon quant`, or `htseq-count` — only the
  counting body changes.
- Collate the per-sample `counts.txt` into one matrix with a final gather target
  over `@{counts}` (the list built in the loop), then feed a differential-expression
  script:

  ```
  cohort.counts.matrix: @{counts} {{
      ngsutilsj count-merge ${input} > ${output}
  }}
  @default: cohort.counts.matrix
  ```

See [Tutorial 11: Sample sheets](../tutorials/11-sample-sheets.md) and the
[Sample Sheets chapter](../13-Sample_Sheets.md).
