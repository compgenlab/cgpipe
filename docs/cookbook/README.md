# cgpipe cookbook

Real, end-to-end pipeline recipes for common bioinformatics workflows. Unlike the
[`examples/`](../../examples/) (which run anywhere with just coreutils), these use
actual tools — **bwa, STAR, GATK, MACS2, …** — so they're **templates to adapt**,
not run as-is. Every recipe is valid cgpipe that renders cleanly with `cgpipe -dr`; drop
in your reference and reads and they're ready to run.

| Recipe | Workflow | cgpipe patterns it shows |
|--------|----------|-----------------------|
| [Reference preparation](reference-preparation.md) | build bwa / faidx / dict indexes | prerequisite targets, an `all` aggregator |
| [FASTQ QC & trimming](fastq-qc-trim.md) | fastp + FastQC | multi-output targets, sample-sheet scatter |
| [DNA-seq variant calling](dnaseq-variant-calling.md) | bwa → markdup → BQSR → HaplotypeCaller | **scatter-gather**, temp files (`^`), opportunistic cleanup |
| [RNA-seq quantification](rnaseq-quantification.md) | STAR → ngsutilsj bam-count | per-sample chains, sample-sheet scatter+gather over a cohort |
| [Cohort joint genotyping](cohort-joint-genotyping.md) | GenomicsDBImport → GenotypeGVCFs | gathering many samples → one, **`stage`/`export` workflows** |
| [ChIP-seq / ATAC-seq peaks](chipseq-atac-peaks.md) | bowtie2 → MACS2 | **multi-input targets** (treatment vs. control) |

## How to use a recipe

1. Copy the `.cgp` block into a file.
2. Make sure the tools it lists are on your `PATH` (or set `cgpipe.container.engine`
   and add `job.container = "..."` directives — see
   [Containers & GPUs](../10-Containers_and_GPUs.md)).
3. Preview it with `cgpipe -dr recipe.cgp <args>` — this renders the exact commands
   without running them.
4. Run locally (`cgpipe recipe.cgp <args> | bash`) or submit to your scheduler
   (`cgpipe -r slurm recipe.cgp <args>`). Configure your cluster once in
   `~/.cgpipe/config` (see the [Configuration Reference](../14-Configuration_Reference.md)).

The resource directives (`job.mem`, `job.procs`, `job.walltime`) are starting points
— tune them for your data and cluster.

## A note on atomic writes

To keep these recipes readable, they write straight to `${output}`. In production
you should **not** — a job killed mid-write can leave a half-written file that looks
fresh and won't rebuild. The recommended idiom is to write to a temp path and `mv`
it into place only on success (`cmd > ${output}.tmp && mv ${output}.tmp ${output}`).
Apply it as you adapt these templates; see
[Write atomically](../05-Build_Targets.md#write-atomically-temp-file-then-rename).

## See also

- The [tutorials](../07-Pipeline_Tutorials.md) teach each cgpipe pattern in isolation.
- The [examples](../../examples/) are runnable (coreutils) versions of the same shapes.
