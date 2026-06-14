# Sample Sheets: scatter and gather

Process a whole cohort from one sample sheet, in a **single** pipeline. `open(path)`
reads the sheet at evaluation time; you loop over its rows to scatter a per-sample
target, then **gather** every sample's output into a downstream target. Because it
is all one dependency graph, the gather knows exactly when the scatter is done — the
thing a per-row fan-out could never express.

## Reading a sample sheet

`open("samples.tsv").read_tsv()` returns a **list of maps**, one per row, keyed by
the header columns:

```
samples = open("samples.tsv").read_tsv(header=true)
```

| Reader | Returns | Notes |
|--------|---------|-------|
| `read_tsv(...)` / `read_csv(...)` | list of map | Header names the columns; cells auto-typed |
| `read_json()` | list of map | A JSON array of objects |
| `read_lines(...)` | list of string | Raw lines (comment- and blank-aware) |
| `read()` | string | The whole file |

Read a column by name, `row["sample"]`, or position, `row[0]`. A field keeps its
type, so it chains: `row["fastq"].basename()`, `row["n"] + 1`.

Reader options are keyword arguments: `read_tsv(header=false, sep="|", comment="#",
skip=0, raw=false)`. With `header=false` columns are keyed `c0`, `c1`, …; with
`raw=true` cells stay strings instead of being auto-typed (`"3"` → int).

## Scatter and gather

Accumulate the per-sample outputs into a list as you emit each target, then make a
final target depend on `@{…}` of that list:

```
#!/usr/bin/env cgp
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

# gather: depends on every per-sample output, so it runs once they're all built
cohort.txt: @{sums} {{ cat ${input} > ${output} }}
@default: cohort.txt
```

```console
$ cgp -dr pipeline.cgp
#!/usr/bin/env bash
set -euo pipefail

# ---- s1.sum ----
wc -w < data/s1.txt > s1.sum

# ---- s2.sum ----
wc -w < data/s2.txt > s2.sum

# ---- cohort.txt ----
cat s1.sum s2.sum > cohort.txt
```

`${input}` in the gather body expands to all of that target's inputs (every
`*.sum`) on one command line. Drop `-dr` to run; on a scheduler the per-sample jobs
submit in parallel and the gather job is wired to wait for all of them.

> **Quoting.** A column read inside a `"…"` string must be bound to a plain
> variable first (`name = row["sample"]`), because a nested `"` would close the
> string. In a target declaration or a `{{ }}` body, `${row["col"]}` is written
> as-is.

## Grouping by a column

A `map` doubles as a lookup/grouping structure: bucket rows into lists, then emit
one gather per group. `groups[key] += value` appends (creating the list, and the
map, on first use):

```
groups = {}
for row in samples {
    out = row["sample"] + ".sum"
    groups[row["category"]] += out      # bucket outputs by category
}
for cat in groups {                     # iterate the keys
    ${cat}.report: @{groups[cat]} {{ cat ${input} > ${output} }}
}
```

## Other formats

`read_csv()` for comma-delimited sheets, `read_json()` for a JSON array of objects,
and `read_lines()` when you just need a list of identifiers:

```
for s in open("samples.txt").read_lines(comment="#") {
    ${s}.done: ${s}.in {{ process ${input} > ${output} }}
}
```

## One graph, one report

Since the whole cohort is a single dependency graph, `-r graphviz` and `-r html`
already render it — scatter **and** gather — as one document. `cgp -r html
pipeline.cgp > cohort.html` is a status page for the entire cohort.

## Next

- **[Tutorial: Sample sheets](tutorials/11-sample-sheets.md)** — a worked scatter +
  gather.
- **[Workflows](12-Workflows.md)** — chain multi-stage workflows; a sample-sheet
  loop can live inside any stage.

Reference → [language-spec.md §14](language-spec.md#14-reading-files-sample-sheets-scatter-and-gather).
