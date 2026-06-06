# 02 — Batch compress (wildcards)

One wildcard rule compresses *any* file; the `all` aggregator requests several at
once. This is the pattern for "run the same per-file tool over a directory of
inputs."

```sh
cgp pipeline.cgp | bash
ls data/*.gz                     # a.txt.gz  b.txt.gz  c.txt.gz
```

Concepts: a `%` wildcard rule (`%.txt.gz: %.txt`), `${input}`/`${output}`, and a
bodyless `all:` aggregator as the `@default`. cgp expands the one rule into a job
per matching file and skips any whose output is already current.

**Adapt it:** replace `gzip -c ${input} > ${output}` with `samtools index ${input}`,
`fastqc ${input}`, etc. — the rule shape doesn't change.

See [Tutorial 2](../../docs/tutorials/02-gzip-wildcard.md).
