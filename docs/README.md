# cgpipe documentation

cgpipe is a small language and runner for generating and submitting job scripts. You
write `output: input` rules with shell bodies; cgpipe builds the dependency graph and
runs the work locally or on a scheduler (SLURM, SGE, PBS, BatchQ).

New here? Start with **[Getting Started](02-Getting_Started.md)**.

## Contents

| # | Chapter | What's in it |
|---|---------|--------------|
| 1 | [Introduction](01-Introduction.md) | What cgpipe is, where it fits, the mental model |
| 2 | [Getting Started](02-Getting_Started.md) | Why cgpipe, install, your first pipeline |
| 3 | [Language Syntax](03-Language_Syntax.md) | Types, variables, operators, control flow, statements |
| 4 | [Methods Reference](04-Methods_Reference.md) | Per-type methods (string, list, range) |
| 5 | [Build Targets](05-Build_Targets.md) | Target syntax, directives, wildcards, temp outputs, opportunistic jobs |
| 6 | [Reserved Targets](06-Reserved_Targets.md) | `@pre`/`@post`/`@setup`/`@teardown`/`@postsubmit`, the `@default` goal |
| 7 | [Pipeline Tutorials](07-Pipeline_Tutorials.md) | Worked examples, start to finish |
| 8 | [Running Jobs](08-Running_Jobs.md) | Runners, `job.*` settings, dependencies, `cgpipe sub` |
| 9 | [Array Jobs](09-Array_Jobs.md) | Submit a fan-out as one scheduler array; `for … with i`, `cgpipe sub --array` |
| 10 | [Containers and GPUs](10-Containers_and_GPUs.md) | Docker/Singularity wrapping, `job.gpu` requests |
| 11 | [The Ledger](11-The_Ledger.md) | Restarts, cross-run reuse, `cgpipe ledger` |
| 12 | [Workflows](12-Workflows.md) | Chaining pipelines with `stage` and `export` |
| 13 | [Sample Sheets](13-Sample_Sheets.md) | Read a TSV/CSV/JSON sheet, scatter per sample, gather a cohort |
| 14 | [Configuration Reference](14-Configuration_Reference.md) | Every `cgpipe.*` / `job.*` setting; precedence; env vars |
| 15 | [The `convert` Tool](15-The_convert_Tool.md) | Bring an older script forward |
| 16 | [Glossary](16-Glossary.md) | Terminology used throughout |
| 17 | [Troubleshooting](17-Troubleshooting.md) | Debugging tools, common errors |
| 18 | [Comparisons](18-Comparisons.md) | How cgpipe compares to Snakemake, Nextflow, WDL |
| — | [Language Specification](language-spec.md) | The precise, normative reference |
| — | [Tutorials index](07-Pipeline_Tutorials.md) | The fourteen worked tutorials |
| — | [Cookbook](cookbook/) | End-to-end recipes for real workflows (DNA-seq, RNA-seq, ChIP/ATAC, …) |
| — | [cgpipe in one page](cgpipe-for-llms.md) | Dense single-file reference (paste into an LLM, or skim as a cheatsheet) |
| — | [Examples](../examples/) | Runnable, self-contained pipelines to run and adapt |

## I want to…

- **…write my first pipeline.** → [Getting Started](02-Getting_Started.md) → [Tutorial 1: Hello, target](tutorials/01-hello.md).
- **…start from a real-workflow template.** → the [Cookbook](cookbook/) (DNA-seq, RNA-seq, ChIP/ATAC, joint genotyping, …).
- **…look up a syntax detail.** → [Language Syntax](03-Language_Syntax.md), [Build Targets](05-Build_Targets.md), [Methods Reference](04-Methods_Reference.md).
- **…fan out work over chromosomes / samples / lanes.** → [Tutorial 4: Map-reduce](tutorials/04-map-reduce.md) (in-file) and [Tutorial 11: Sample sheets](tutorials/11-sample-sheets.md) (scatter + gather from a TSV).
- **…submit a fan-out as one scheduler array job.** → [Array Jobs](09-Array_Jobs.md) (`cgpipe sub --array`, `for … with i`).
- **…clean up intermediates without breaking restarts.** → [Tutorial 5: Opportunistic cleanup](tutorials/05-opportunistic-cleanup.md).
- **…run jobs in Docker or Singularity, or request GPUs.** → [Containers and GPUs](10-Containers_and_GPUs.md), [Tutorial 9](tutorials/09-containers.md).
- **…submit to SLURM / SGE / PBS / BatchQ.** → [Running Jobs](08-Running_Jobs.md), [Configuration Reference](14-Configuration_Reference.md).
- **…chain several pipelines together.** → [Workflows](12-Workflows.md), [Tutorial 12](tutorials/12-stage-workflow.md).
- **…restart without redoing finished work, or reuse in-flight jobs.** → [The Ledger](11-The_Ledger.md), [Tutorial 13](tutorials/13-ledger-restart.md).
- **…see what's done / running / failed at a glance.** → the `html` runner, [Tutorial 14](tutorials/14-status-report.md).
- **…submit one quick one-off job.** → `cgpipe sub` in [Running Jobs](08-Running_Jobs.md).
- **…bring an older script forward.** → [The `convert` Tool](15-The_convert_Tool.md).
- **…set up my editor (highlighting, diagnostics).** → [Getting Started § Editor support](02-Getting_Started.md#editor-support), [`editor/vscode/`](../editor/vscode/).
- **…debug a misbehaving pipeline.** → [Troubleshooting](17-Troubleshooting.md) (start with `-dr`).
- **…decide cgpipe vs. Snakemake / Nextflow / WDL.** → [Comparisons](18-Comparisons.md).

## Source of truth

The language is defined precisely in [`language-spec.md`](language-spec.md), and
every feature has an executable example under [`tests/`](../tests/). When a doc
page conflicts with a test fixture, **the fixture is correct** — it is run on every
build. These chapters are the friendly guide; the spec and fixtures are the
authority.
