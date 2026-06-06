# ChIP-seq / ATAC-seq peak calling

Align the treatment (IP) and the input control, then call peaks. The peak-calling
job depends on **both** BAMs — a multi-input target — which is the pattern this
recipe shows off.

> Requires: `bowtie2`, `samtools`, `macs2`. Needs a bowtie2 index.

```
#!/usr/bin/env cgp
#
# ChIP-seq / ATAC-seq peak calling: align the treatment and the input control,
# then call peaks. The peak-calling job depends on BOTH BAMs — a multi-input
# target (${input[0]} = treatment, ${input[1]} = control).
#
# Options:
#     --sample NAME     output prefix
#     --treat FILE      treatment/IP reads (FASTQ)
#     --control FILE    input/control reads (FASTQ)
#     --index PREFIX    bowtie2 index prefix
#     --genome-size S   MACS2 effective genome size (default: hs)

if !sample { print "ERROR: --sample required"; exit 1 }
if !index  { print "ERROR: --index (bowtie2 prefix) required"; exit 1 }
genome_size ?= "hs"

# One wildcard rule aligns any FASTQ to a sorted, indexed BAM.
%.bam: %.fq.gz {{
    job.name  = "align-${stem}"
    job.procs = 4
    --
    bowtie2 -p ${job.procs} -x ${index} -U ${input} \
        | samtools sort -@ ${job.procs} -o ${output} -
    samtools index ${output}
}}

# Call peaks from the treatment against the control (multi-input target).
${sample}_peaks.narrowPeak: ${treat.sub("\\.fq\\.gz$", "")}.bam ${control.sub("\\.fq\\.gz$", "")}.bam {{
    job.name = "macs2-${sample}"
    job.mem  = "8G"
    --
    macs2 callpeak -t ${input[0]} -c ${input[1]} \
        -g ${genome_size} -n ${sample} -f BAM
}}
@default: ${sample}_peaks.narrowPeak
```

## What it shows

- **Multi-input targets.** The peak-calling rule lists two inputs — the treatment
  BAM and the control BAM — and refers to them positionally as `${input[0]}` and
  `${input[1]}`. cgp builds both before the job runs (and on a scheduler, makes the
  job depend on both).
- **One wildcard rule, reused.** A single `%.bam: %.fq.gz` rule aligns *both* the
  treatment and the control; cgp instantiates it once per requested BAM. The
  `${treat.sub(...)}` / `${control.sub(...)}` expressions derive each BAM name from
  its FASTQ.
- **Job settings live under `job.`** Per-job knobs are namespaced — `job.name`,
  `job.procs`, `job.mem` — so they never collide with your own script variables. A
  variable called `name` (or `mem`, `procs`, …) coexists freely with the job setting
  of the same name; the directive `job.name = "macs2-${sample}"` sets the scheduler
  job name without touching the `sample` variable.

## Run it

```sh
cgp -r slurm chipseq-atac-peaks.cgp --sample H3K27ac \
    --treat h3k27ac.fq.gz --control input.fq.gz --index bowtie2/hg38
```

## Adapt it

- **ATAC-seq:** there's no input control — call peaks on the treatment alone
  (`macs2 callpeak -t ${input} ... --nomodel --shift -100 --extsize 200`) and drop
  the control input.
- **Paired-end:** change the aligner rule to take `R1`/`R2` and use `-f BAMPE` in
  MACS2.
- Run a cohort of marks/samples with a [manifest](../12-Manifests_and_Fanout.md)
  whose columns are `sample`, `treat`, `control`.

See [Build Targets](../05-Build_Targets.md) (multiple inputs and wildcards).
