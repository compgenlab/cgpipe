# Pipeline Tutorials

Fourteen worked examples, each adding one idea. They build on each other, so
reading in order is the gentlest path — but each stands alone if you want to jump
to a technique.

| # | Tutorial | Covers |
|---|----------|--------|
| 1 | [Hello, target](tutorials/01-hello.md) | help text, a command-line variable, the build/skip cycle |
| 2 | [gzip with a wildcard](tutorials/02-gzip-wildcard.md) | `%` wildcards, `${stem}`, a bodyless `all:` |
| 3 | [Resources and flags](tutorials/03-resources-and-flags.md) | directive blocks, `?=` defaults, inline `${if}`, atomic writes |
| 4 | [Map-reduce across chromosomes](tutorials/04-map-reduce.md) | dynamic target generation, accumulator lists, temp outputs |
| 5 | [Opportunistic cleanup](tutorials/05-opportunistic-cleanup.md) | `: inputs` jobs, guarded deletion of intermediates |
| 6 | [Shared `@pre` / `@post`](tutorials/06-pre-post.md) | per-job preambles, `@setup`/`@teardown`, `shexec` |
| 7 | [Importable snippets](tutorials/07-snippets.md) | `snippet` / `@name` body fragments |
| 8 | [Composing with include](tutorials/08-include.md) | shared defaults and target libraries across files |
| 9 | [Containerized jobs](tutorials/09-containers.md) | Docker/Singularity wrapping, bind mounts, GPUs |
| 10 | [Custom job-submission templates](tutorials/10-custom-templates.md) | site-specific scheduler templates |
| 11 | [Manifest fan-out](tutorials/11-manifest-fanout.md) | run a pipeline once per sample row |
| 12 | [Stage workflows](tutorials/12-stage-workflow.md) | chaining standalone pipelines with `stage`/`export` |
| 13 | [Restartable pipelines and the ledger](tutorials/13-ledger-restart.md) | mtime restarts, `-force`, cross-run reuse |
| 14 | [A status report](tutorials/14-status-report.md) | the `html` and `graphviz` runners |

## Where next

After the tutorials, the reference chapters fill in the details:
[Running Jobs](08-Running_Jobs.md), [Containers & GPUs](10-Containers_and_GPUs.md),
[The Ledger](11-The_Ledger.md), and the
[Configuration Reference](14-Configuration_Reference.md).
