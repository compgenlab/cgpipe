# 03 — Scatter-gather (map-reduce)

Count the words in each document in parallel, then total them. This is the exact
shape of per-chromosome variant calling, per-lane alignment, or any
split-process-combine workflow.

```sh
cgpipe -dr pipeline.cgp             # preview the generated jobs
cgpipe pipeline.cgp | bash
cat total.txt                    # 11 words total
```

Concepts:
- A top-level **`for` loop emits one target per unit of work** (dynamic
  generation) — the number of jobs follows the data.
- An **accumulator list** (`counts += ...`) collects the per-unit outputs.
- The per-document counts are **temporary** (`^`) — intermediates that only exist
  to feed the merge; staleness looks through them when they're absent.
- The merge target depends on **`@{counts}`** (list expansion → one input per
  document) and consumes them as `${input}` (space-joined).

On a scheduler the per-document jobs submit in parallel and the merge is wired to
wait for all of them — cgpipe derives that from the `@{counts}` inputs.

**Adapt it:** `wc -w` → `bcftools call` per chromosome; the merge `awk` →
`bcftools concat`.

See [Tutorial 4](../../docs/tutorials/04-map-reduce.md) and
[Tutorial 5](../../docs/tutorials/05-opportunistic-cleanup.md).
