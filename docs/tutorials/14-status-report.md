# Tutorial 14: A status report

Two runners turn a pipeline into a picture instead of a submission: `graphviz`
emits the dependency graph as DOT, and `html` renders a self-contained status page
colored by each output's state. Neither runs any jobs.

## The pipeline

```
#!/usr/bin/env cgp
trimmed.fq: a.fastq {{
    cutadapt ${input} > ${output}
}}
aligned.bam: trimmed.fq {{
    bwa mem ${input} > ${output}
}}
@default: aligned.bam
```

## The graph as DOT

`-r graphviz` writes a DOT description to stdout — pipe it to Graphviz to render:

```console
$ cgp -r graphviz pipeline.cgp
digraph cgp {
  rankdir=LR;
  node [shape=box, style=rounded];
  "a.fastq" [shape=note];
  "aligned.bam";
  "trimmed.fq";
  "trimmed.fq" -> "aligned.bam";
  "a.fastq" -> "trimmed.fq";
}
```

```sh
cgp -r graphviz pipeline.cgp | dot -Tsvg > pipeline.svg
```

Source files (`a.fastq`) are drawn as notes; targets as boxes; edges follow the
`output: input` dependencies.

## A status page

`-r html` produces a **self-contained** HTML page (no external assets) with the
graph drawn as inline SVG and a legend, each node colored by status:

```sh
cgp -r html pipeline.cgp > status.html
```

The legend covers the five states — **done**, **running**, **queued**, **failed**,
**pending** — and each output is colored by where it stands. The state is resolved
from disk existence first, then, when a [ledger](../11-The_Ledger.md) and scheduler
are configured, from the owning job's live scheduler state. So an output present on
disk shows **done**, one whose job is still queued shows **queued**, and one not
yet started shows **pending**.

A sample-sheet pipeline that scatters over rows and gathers
([Tutorial 11](11-sample-sheets.md)) is already a single graph, so `-r html` renders
the whole cohort as one page — a single cohort-wide status view:

```sh
cgp -r html align.cgp > cohort.html
```

## When to use which

- **graphviz** — documentation and review: a static picture of the pipeline's
  shape.
- **html** — operations: where a run stands right now, especially with a ledger so
  the colors reflect live scheduler state.

## Next

You've reached the end of the tutorials. For the full reference, see
[The Ledger](../11-The_Ledger.md), [Running Jobs](../08-Running_Jobs.md), and the
[Configuration Reference](../14-Configuration_Reference.md).

Reference → [Running Jobs § Runners](../08-Running_Jobs.md#runners).
