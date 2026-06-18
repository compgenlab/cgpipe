# Tutorial 3: Resources and flags

Real jobs need CPUs, memory, and walltime — and often an *optional* flag that's
only present sometimes. This tutorial covers the directive block, `?=` defaults,
the inline `${if}` conditional, and the atomic-write idiom.

## The script

`align.cgp`:

```
#!/usr/bin/env cgpipe
#
# Align reads, with tunable resources and an optional read-group.
#
# Options:
#     --reads FILE    input FASTQ
#     --ref FILE      reference FASTA
#     --threads N     alignment threads (default: 4)
#     --rg STRING     optional read-group line

threads ?= 4

aligned.bam: ${reads} ${ref} {{
    job.procs    = threads
    job.mem      = "8G"
    job.walltime = "12:00:00"
    --
    bwa mem -t ${job.procs} ${if rg; "-R " + rg} ${ref} ${reads} \
        | samtools sort -o ${output}.tmp - && mv ${output}.tmp ${output}
}}
@default: aligned.bam
```

Four things to notice:

- **`threads ?= 4`** sets a default that a `--threads` on the command line
  overrides.
- **The directive block** (everything before `--`) sets `job.procs`, `job.mem`,
  and `job.walltime`. These don't emit shell — they configure the job.
- **`${if rg; "-R " + rg}`** adds `-R <rg>` *only when* `--rg` was given, and
  nothing otherwise. No `if` block, no duplicated command.
- **`${output}.tmp && mv`** writes to a temp name and renames on success, so a
  killed job never leaves a half-written `aligned.bam` that looks complete.

## Locally, the directives are invisible

Under the shell runner, resources don't apply — you just get the command:

```console
$ cgpipe -dr align.cgp --reads reads.fq --ref ref.fa
#!/usr/bin/env bash
set -euo pipefail

# ---- aligned.bam ----
bwa mem -t 4  ref.fa reads.fq \
| samtools sort -o aligned.bam.tmp - && mv aligned.bam.tmp aligned.bam
```

With no `--rg`, the `${if}` expands to nothing (the empty gap is harmless).

## On a scheduler, they become resource requests

The *same target*, submitted to SLURM with overrides, turns the directives into
`#SBATCH` lines — `job.mem = "8G"` becomes `--mem=8000`, `job.procs` becomes `-c`,
`job.walltime` becomes `-t`:

```console
$ cgpipe -dr -r slurm align.cgp --reads reads.fq --ref ref.fa --threads 16 --rg "@RG\tID:lib1"
# [dryrun.1] aligned.bam
#!/bin/bash
#SBATCH -J aligned.bam
#SBATCH -t 12:00:00
#SBATCH -c 16
#SBATCH -n 1
#SBATCH --mem=8000

set -eo pipefail
bwa mem -t 16 -R @RG\tID:lib1 ref.fa reads.fq \
| samtools sort -o aligned.bam.tmp - && mv aligned.bam.tmp aligned.bam
```

`--threads 16` flowed through `job.procs = threads` to `-c 16`, and the read-group
appeared because `--rg` was set. You wrote the resources once; the scheduler
mapping is cgpipe's job.

## Next

- **[Tutorial 4: Map-reduce across chromosomes](04-map-reduce.md)** — generate many
  jobs from a loop and merge their outputs.

Reference → [Build Targets § Directives](../05-Build_Targets.md#directives-and-the----separator),
[Running Jobs](../08-Running_Jobs.md).
