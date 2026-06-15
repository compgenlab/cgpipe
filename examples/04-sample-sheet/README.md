# 04 — Sample sheet: scatter **and** gather

Drive a whole cohort from a sample sheet, in **one** pipeline: scatter a
per-sample job for each row, then gather every sample's output into a single
cohort summary.

```sh
cgp pipeline.cgp | bash
cat cohort.txt
#   s1: 25
#   s2: 20
#   s3: 42
```

`open("samples.tsv").read_tsv()` reads the sheet at evaluation time and returns
one ordered **map** per row; read a column with `row["sample"]` (or by position,
`row[0]`). The `for` loop emits a `${name}.sum` target per row and accumulates the
output names into `sums`; the final `cohort.txt` target depends on `@{sums}`, so it
runs once every sample is done. Because it is all one dependency graph, `cgp -dr`
shows the scatter and the gather together, and a re-run rebuilds only what changed.

**Group by a column:** build a map of lists and emit one gather per group —

```
groups = {}
for row in samples {
    out = row["sample"] + ".sum"
    groups[row["category"]] += out      # bucket outputs by category
}
for cat in groups {
    ${cat}.report: @{groups[cat]} {{ cat ${input} > ${output} }}
}
```

**Adapt it:** point `input` at FASTQs and replace the `tr`/`awk` body with an
alignment command, then make `cohort.txt` an R/multiqc step over `${input}`.

Concepts: `open().read_tsv()`; the `map` type (`row["col"]`, `groups[k] += …`);
list accumulation + `@{…}` gather. See
[the sample-sheets tutorial](../../docs/tutorials/11-sample-sheets.md).
