# Manifests and Fan-out

A single pipeline (or workflow) can be run **once per row of a manifest**, with
the row's columns supplied as variables. This is how you process a cohort: write
the pipeline for one sample, then point it at a table of samples.

## The four formats

The format is always explicit — cgp never guesses from the extension:

| Flag | Manifest format |
|------|-----------------|
| `-manifest FILE` (alias `-manifest-cgp`) | A shell glob of `.cgp` manifest files; each matched file's variables become one run |
| `-manifest-tsv FILE` | Tab-separated; the header row names the columns |
| `-manifest-csv FILE` | Comma-separated; the header row names the columns |
| `-manifest-json FILE` | A JSON array of objects; each object's keys become variables |

## Fan-out over a TSV

Given `samples.tsv`:

```
sample	fastq
A	A.fq
B	B.fq
```

and a one-sample pipeline that reads `sample` and `fastq` as variables:

```
#!/usr/bin/env cgp
${sample}.bam: ${fastq} {{
    bwa mem ${input} > ${output}
}}
@default: ${sample}.bam
```

`-manifest-tsv` runs the whole pipeline once per data row, the columns becoming
variables each time:

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

One render per row, each with that row's `sample` and `fastq`. On a scheduler each
row's jobs submit independently.

## Overriding columns

Explicit `--name value` variables on the command line **override** columns of the
same name — handy to re-run one cohort against a different reference without
editing the manifest:

```sh
cgp align.cgp -manifest-tsv samples.tsv --ref /refs/hg38.fa
```

## Shared work is done once

A single **stat cache is shared across all runs**, so an invariant input — a
reference genome every row aligns against — is `stat`-ed once, not once per row.
The file is also *parsed* once; only the per-row graph is re-evaluated, since
per-row variables can legitimately change which targets and branches exist.

## CGP manifests for richer rows

When a row needs more than flat columns — lists, computed values — use a glob of
`.cgp` manifest files. Each matched file is an ordinary cgp script that just sets
variables:

```sh
cgp wgs.cgp -manifest "/data/P*/manifest.cgp"   # one workflow run per patient
```

Each `manifest.cgp` sets that patient's variables (and can use the full language to
compute them); cgp runs the pipeline once per matched file.

## Combining with graph and report runners

`-r graphviz` and `-r html` produce a *single* document for a manifest run — one
cluster (graphviz) or section (html) per row — rather than one document per row
concatenated. So `cgp -r html pipeline.cgp -manifest-tsv samples.tsv` gives you one
status page covering the whole cohort.

## Next

- **[Tutorial 11: Manifest fan-out](tutorials/11-manifest-fanout.md)** — a worked
  per-sample run.
- **[Workflows](11-Workflows.md)** — fan a whole multi-stage workflow over a cohort.

Reference → [language-spec.md §14](language-spec.md#14-manifests-and-fan-out).
