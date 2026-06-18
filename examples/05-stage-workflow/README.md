# 05 — Stage workflow

Chain two standalone pipelines into one command, passing a value from the first to
the second. Each stage is itself a normal, independently runnable pipeline.

```sh
# the whole workflow
cgpipe workflow.cgp --raw data/raw.txt
cat clean.txt summary.txt

# either stage on its own
cgpipe normalize.cgp --raw data/raw.txt
cgpipe summarize.cgp --text clean.txt
```

How it fits together:
- `normalize.cgp` cleans the text and **`export cleaned = "clean.txt"`** exposes the
  result.
- `workflow.cgp` declares two `stage`s; the second references the first's export as
  **`${normalize.cleaned}`**.
- `export` is a no-op when a stage runs standalone, so each file stays usable on
  its own.

The stage files set `cgpipe.runner.shell.autoexec = true` so they **execute** rather
than print a script — that's what lets stage 2 read the file stage 1 produced.
(Without it, the default shell runner only emits the script, so you'd pipe each to
`bash` yourself.) On a scheduler, configure `cgpipe.ledger` so cross-stage
dependencies wire up even when the first stage's jobs are still queued.

See [Tutorial 12](../../docs/tutorials/12-stage-workflow.md).
