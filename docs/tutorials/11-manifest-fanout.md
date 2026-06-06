# Tutorial 11: Manifest fan-out

Process a whole cohort by writing the pipeline for *one* sample and pointing it at
a table of samples. cgp runs the pipeline once per row, the columns becoming
variables.

## The per-sample pipeline

Write `align.cgp` as if there were only one sample — `sample` and `fastq` are just
variables:

```
#!/usr/bin/env cgp
${sample}.bam: ${fastq} {{
    bwa mem ${input} > ${output}
}}
@default: ${sample}.bam
```

## The manifest

`samples.tsv` — the header row names the columns:

```
sample	fastq
A	A.fq
B	B.fq
```

## Run it once per row

```console
$ cgp -dr align.cgp -manifest-tsv samples.tsv
#!/usr/bin/env bash
set -euo pipefail

# ---- A.bam ----
bwa mem A.fq > A.bam

#!/usr/bin/env bash
set -euo pipefail

# ---- B.bam ----
bwa mem B.fq > B.bam
```

Each row produced its own run with `sample` and `fastq` set from that row. Drop
`-dr` to actually run them; with `-r slurm` each row's jobs submit independently,
so the cohort fans out across the cluster.

## Override a column from the command line

A `--name value` on the command line overrides the matching column for every row —
useful to re-run the cohort against a different reference without touching the
manifest:

```sh
cgp align.cgp -manifest-tsv samples.tsv --ref /refs/hg38.fa
```

## Other manifest formats

The same pipeline works with CSV (`-manifest-csv`), JSON (`-manifest-json`, an
array of objects), or a glob of `.cgp` manifest files (`-manifest`) when a row
needs computed or list-valued variables. See
[Manifests and Fan-out](../12-Manifests_and_Fanout.md).

## One report for the whole cohort

`-r html` over a manifest gives a single status page with a section per row:

```sh
cgp -r html align.cgp -manifest-tsv samples.tsv > cohort.html
```

## Next

- **[Tutorial 12: Stage workflows](12-stage-workflow.md)** — fan a multi-stage
  workflow over a cohort.

Reference → [Manifests and Fan-out](../12-Manifests_and_Fanout.md),
[language-spec.md §14](../language-spec.md#14-manifests-and-fan-out).
