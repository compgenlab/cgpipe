# 04 — Manifest cohort fan-out

Write the pipeline for **one** sample, then run it across a whole cohort with a
manifest. Each row of `samples.tsv` becomes one run, its columns supplied as
variables.

```sh
cgp pipeline.cgp -manifest-tsv samples.tsv | bash
cat s1.sum s2.sum s3.sum
#   s1: 25
#   s2: 20
#   s3: 42
```

`samples.tsv` has columns `sample` and `input`; inside the pipeline they're just
`${sample}` and `${input}`. Override a column for every row from the command line
(e.g. `--ref /refs/hg38.fa`), and combine with `-r html` for a single
cohort-wide status page.

Concepts: `-manifest-tsv` fan-out; a per-sample target named with `${sample}`. Also
available: `-manifest-csv`, `-manifest-json`, and `-manifest` (a glob of `.cgp`
files) for richer rows.

**Adapt it:** point `input` at FASTQs and replace the `tr`/`awk` body with an
alignment command — one manifest then drives the whole cohort.

See [Tutorial 11](../../docs/tutorials/11-manifest-fanout.md).
