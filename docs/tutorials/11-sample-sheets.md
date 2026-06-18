# Tutorial 11: Sample sheets — scatter and gather

Process a whole cohort from a sample sheet in **one** pipeline: read the sheet,
scatter a per-sample job for each row, then gather every sample's output into a
single cohort summary.

## The sample sheet

`samples.tsv` — the header row names the columns:

```
sample	input
s1	data/s1.txt
s2	data/s2.txt
```

## Read it, scatter, gather

`open("samples.tsv").read_tsv()` returns one ordered **map** per row; read a column
with `row["..."]`. The `for` loop emits a `${name}.sum` target per row and collects
the output names; `cohort.txt` then depends on `@{sums}`.

```
#!/usr/bin/env cgpipe
samples = open("samples.tsv").read_tsv(header=true)
sums = []

for row in samples {
    name = row["sample"]
    out  = name + ".sum"
    sums += out
    ${out}: ${row["input"]} {{
        wc -w < ${input} > ${output}
    }}
}

cohort.txt: @{sums} {{
    cat ${input} > ${output}
}}
@default: cohort.txt
```

## Render it

```console
$ cgpipe -dr pipeline.cgp
#!/usr/bin/env bash
set -euo pipefail

# ---- s1.sum ----
wc -w < data/s1.txt > s1.sum

# ---- s2.sum ----
wc -w < data/s2.txt > s2.sum

# ---- cohort.txt ----
cat s1.sum s2.sum > cohort.txt
```

Scatter (one `*.sum` per row) **and** gather (`cohort.txt` over all of them) in a
single graph. `${input}` in the gather body expands to every `*.sum` on one line.
Drop `-dr` to run; with `-r slurm` the per-sample jobs submit in parallel and the
gather waits for all of them.

> A column read inside a `"…"` string must be bound to a plain variable first
> (`name = row["sample"]`) — a nested `"` would close the string. In a target line
> or a `{{ }}` body, `${row["col"]}` is fine as-is.

## Group by a column

Bucket rows into a map of lists, then emit one gather per group:

```
groups = {}
for row in samples { groups[row["category"]] += row["sample"] + ".sum" }
for cat in groups {
    ${cat}.report: @{groups[cat]} {{
        cat ${input} > ${output}
    }}
}
```

## Other formats

`read_csv()`, `read_json()` (a JSON array of objects), and `read_lines()` (a plain
list of identifiers) read the same way. See
[Sample Sheets](../13-Sample_Sheets.md).

## Next

- **[Tutorial 12: Stage workflows](12-stage-workflow.md)** — chain standalone
  pipelines; a sample-sheet loop can live inside any stage.

Reference → [Sample Sheets](../13-Sample_Sheets.md),
[language-spec.md §14](../language-spec.md#14-reading-files-sample-sheets-scatter-and-gather).
