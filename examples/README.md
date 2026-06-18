# cgpipe examples

Real, **runnable** pipelines you can run as-is and adapt. Every example here uses
only coreutils (`echo`, `wc`, `gzip`, `awk`, …) so it runs anywhere with no
bioinformatics tools to install — but each is structured exactly like the
real-world pipeline it stands in for. Swap `wc`/`gzip` for `bwa`/`samtools` and the
shape is identical.

| Example | Shows | Run |
|---------|-------|-----|
| [01-hello](01-hello/) | the smallest pipeline: one target, a variable, `@default` | `cgpipe pipeline.cgp \| bash` |
| [02-batch-compress](02-batch-compress/) | a `%` wildcard rule over many files; an `all` aggregator | `cgpipe pipeline.cgp \| bash` |
| [03-scatter-gather](03-scatter-gather/) | a `for` loop emitting one job per unit, temp outputs, a merge (map-reduce) | `cgpipe pipeline.cgp \| bash` |
| [04-sample-sheet](04-sample-sheet/) | read a TSV sample sheet, scatter per sample, then gather a cohort summary — in one script | `cgpipe pipeline.cgp \| bash` |
| [05-stage-workflow](05-stage-workflow/) | chaining two standalone pipelines with `stage` / `export` | `cgpipe workflow.cgp --raw data/raw.txt` |
| [06-cluster-resources](06-cluster-resources/) | the same pipeline rendered for local bash *or* a scheduler | `cgpipe -dr -r slurm pipeline.cgp` |

## Running

Each example is self-contained — `cd` into its directory and run the command from
its README. The default (shell) runner **prints** an assembled bash script to
stdout, so pipe it to `bash` to actually run:

```sh
cd examples/03-scatter-gather
cgpipe pipeline.cgp | bash
cat total.txt
```

(Use `cgpipe -dr pipeline.cgp` to preview the script without running it.)

## Verify them all

```sh
examples/check.sh            # builds cgpipe if needed, runs every example
CGPIPE=bin/cgpipe examples/check.sh # use a prebuilt binary
```

`check.sh` runs each example in a temp dir and asserts it produced output — a quick
guard that the examples still work.

## Learning path

These examples pair with the [tutorials](../docs/07-Pipeline_Tutorials.md), which
walk through the same ideas step by step. New to cgpipe? Start with
[Getting Started](../docs/02-Getting_Started.md).
