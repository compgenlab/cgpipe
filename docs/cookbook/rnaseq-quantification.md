# RNA-seq quantification

Align RNA-seq reads with STAR and count reads per gene with `ngsutilsj bam-count`.
A short per-sample chain that's the natural hook for running a whole cohort from a
manifest.

> Requires: `STAR`, `samtools`, `ngsutilsj`. Needs a STAR genome index and a GTF
> annotation.

```
#!/usr/bin/env cgp
#
# RNA-seq quantification: align with STAR, then count reads per gene with
# ngsutilsj. Run one sample, or a whole cohort with a manifest:
#
#     cgp rnaseq-quantification.cgp --sample s1 --r1 s1_R1.fq.gz --r2 s1_R2.fq.gz \
#                    --index star_index --gtf genes.gtf
#     cgp rnaseq-quantification.cgp -manifest-tsv samples.tsv --index star_index --gtf genes.gtf

if !sample { print "ERROR: --sample required"; exit 1 }
if !index  { print "ERROR: --index (STAR genome dir) required"; exit 1 }
if !gtf    { print "ERROR: --gtf required"; exit 1 }

# Align to a coordinate-sorted BAM. STAR writes <prefix>Aligned.sortedByCoord.out.bam;
# rename it to a tidy ${sample}.bam.
${sample}.bam: ${r1} ${r2} {{
    job.name  = "star-${sample}"
    job.procs = 8
    job.mem   = "32G"
    --
    STAR --genomeDir ${index} --readFilesIn ${r1} ${r2} --readFilesCommand zcat \
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
@default: ${sample}.counts.txt
```

## What it shows

- **A clean per-sample chain.** Align → count, with the BAM the only intermediate.
  `${input}`/`${output}` keep the bodies free of hard-coded names.
- **Renaming a tool's fixed output.** STAR insists on its own output name; a `mv`
  inside the body normalizes it to `${sample}.bam` so the rest of the pipeline (and
  the next recipe) can refer to it predictably.
- **Cohort fan-out.** The same one-sample pipeline runs over an entire cohort with
  `-manifest-tsv`, with the shared `--index` and `--gtf` passed once on the command
  line — cgp's stat cache checks the shared index once, not once per sample.

## Run it

One sample:

```sh
cgp rnaseq-quantification.cgp --sample liver1 --r1 liver1_R1.fq.gz --r2 liver1_R2.fq.gz \
    --index star_index --gtf genes.gtf | bash
```

A cohort — `samples.tsv` with columns `sample`, `r1`, `r2`:

```sh
cgp -r slurm rnaseq-quantification.cgp -manifest-tsv samples.tsv \
    --index star_index --gtf genes.gtf
```

(`--index`/`--gtf` on the command line apply to every row; the manifest supplies
the per-sample columns.)

## Adapt it

- Build the STAR index once with a small reference-prep target
  (`STAR --runMode genomeGenerate`); have `${sample}.bam` depend on its sentinel so
  it's built first.
- Swap the counter for `featureCounts`, `salmon quant`, or `htseq-count` — only the
  counting body changes.
- Collate the per-sample `counts.txt` into one matrix with a final gather target
  over `@{samples}.counts.txt`, then feed a differential-expression script.

See [Tutorial 11: Manifest fan-out](../tutorials/11-manifest-fanout.md) and the
[Manifests chapter](../12-Manifests_and_Fanout.md).
