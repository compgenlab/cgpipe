# cgp documentation

cgp is a small language and runner for generating and submitting job scripts. You
write `output: input` rules with shell bodies; cgp builds the dependency graph and
runs the work locally or on a scheduler (SLURM, SGE, PBS, BatchQ).

New here? Start with **[Getting Started](02-Getting_Started.md)**.

## Contents

| # | Chapter | What's in it |
|---|---------|--------------|
| 1 | [Introduction](01-Introduction.md) | What cgp is, where it fits, the mental model |
| 2 | [Getting Started](02-Getting_Started.md) | Why cgp, install, your first pipeline |
| 3 | [Language Syntax](03-Language_Syntax.md) | Types, variables, operators, control flow, statements |
| 4 | [Methods Reference](04-Methods_Reference.md) | Per-type methods (string, list, range) |
| 5 | [Build Targets](05-Build_Targets.md) | Target syntax, directives, wildcards, temp outputs, opportunistic jobs |
| 6 | [Reserved Targets](06-Reserved_Targets.md) | `@pre`/`@post`/`@setup`/`@teardown`/`@postsubmit`, the `@default` goal |
| 7 | [Pipeline Tutorials](07-Pipeline_Tutorials.md) | Worked examples, start to finish |
| 8 | [Running Jobs](08-Running_Jobs.md) | Runners, `job.*` settings, dependencies, `cgp sub` |
| 9 | [Containers and GPUs](09-Containers_and_GPUs.md) | Docker/Singularity wrapping, `gpu` requests |
| 10 | [The Ledger](10-The_Ledger.md) | Restarts, cross-run reuse, `cgp ledger` |
| 11 | [Workflows](11-Workflows.md) | Chaining pipelines with `stage` and `export` |
| 12 | [Manifests and Fan-out](12-Manifests_and_Fanout.md) | Run a pipeline once per sample/row |
| 13 | [Configuration Reference](13-Configuration_Reference.md) | Every `cgp.*` / `job.*` setting; precedence; env vars |
| 14 | [The `convert` Tool](14-The_convert_Tool.md) | Bring an older script forward |
| 15 | [Glossary](15-Glossary.md) | Terminology used throughout |
| 16 | [Troubleshooting](16-Troubleshooting.md) | Debugging tools, common errors |
| 17 | [Comparisons](17-Comparisons.md) | How cgp compares to Snakemake, Nextflow, WDL |
| — | [Language Specification](language-spec.md) | The precise, normative reference |
| — | [Tutorials index](07-Pipeline_Tutorials.md) | The fourteen worked tutorials |

## I want to…

- **…write my first pipeline.** → [Getting Started](02-Getting_Started.md) → [Tutorial 1: Hello, target](tutorials/01-hello.md).
- **…look up a syntax detail.** → [Language Syntax](03-Language_Syntax.md), [Build Targets](05-Build_Targets.md), [Methods Reference](04-Methods_Reference.md).
- **…fan out work over chromosomes / samples / lanes.** → [Tutorial 4: Map-reduce](tutorials/04-map-reduce.md) (in-file) and [Tutorial 11: Manifest fan-out](tutorials/11-manifest-fanout.md) (one run per sample).
- **…clean up intermediates without breaking restarts.** → [Tutorial 5: Opportunistic cleanup](tutorials/05-opportunistic-cleanup.md).
- **…run jobs in Docker or Singularity, or request GPUs.** → [Containers and GPUs](09-Containers_and_GPUs.md), [Tutorial 9](tutorials/09-containers.md).
- **…submit to SLURM / SGE / PBS / BatchQ.** → [Running Jobs](08-Running_Jobs.md), [Configuration Reference](13-Configuration_Reference.md).
- **…chain several pipelines together.** → [Workflows](11-Workflows.md), [Tutorial 12](tutorials/12-stage-workflow.md).
- **…restart without redoing finished work, or reuse in-flight jobs.** → [The Ledger](10-The_Ledger.md), [Tutorial 13](tutorials/13-ledger-restart.md).
- **…see what's done / running / failed at a glance.** → the `html` runner, [Tutorial 14](tutorials/14-status-report.md).
- **…submit one quick one-off job.** → `cgp sub` in [Running Jobs](08-Running_Jobs.md).
- **…bring an older script forward.** → [The `convert` Tool](14-The_convert_Tool.md).
- **…debug a misbehaving pipeline.** → [Troubleshooting](16-Troubleshooting.md) (start with `-dr`).
- **…decide cgp vs. Snakemake / Nextflow / WDL.** → [Comparisons](17-Comparisons.md).

## Source of truth

The language is defined precisely in [`language-spec.md`](language-spec.md), and
every feature has an executable example under [`tests/`](../tests/). When a doc
page conflicts with a test fixture, **the fixture is correct** — it is run on every
build. These chapters are the friendly guide; the spec and fixtures are the
authority.
