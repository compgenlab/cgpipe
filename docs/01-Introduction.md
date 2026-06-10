# Introduction

cgp is a small language and runner for **generating and submitting job scripts**.
You write rules that say *"this output file is produced from these input files by
this shell command,"* and cgp turns a collection of those rules into a dependency
graph, figures out what is out of date, and runs the necessary jobs — locally
through your shell, or on a cluster through a batch scheduler.

If you have used `make`, the shape will feel familiar. cgp keeps make's best idea
— **describe outputs and let the tool schedule the work** — and adds the things a
real compute pipeline needs: a proper expression language, first-class job
resources (CPUs, memory, walltime), scheduler submission, containers, and a job
ledger for cheap restarts.

## Where cgp fits

cgp is aimed at pipelines that run as batches of jobs — bioinformatics and
scientific computing especially — where you want one description of the work to
run unchanged on a laptop and on an HPC cluster. A single cgp script can target:

- **the local shell** — assembled into one bash script you can read, pipe to
  `bash`, or have cgp execute;
- **a scheduler** — SLURM, SGE, PBS, or BatchQ — submitting each job with its
  resource requests and dependency edges wired up for you;
- **a diagram or report** — DOT via graphviz, or a self-contained HTML status
  page.

You choose the backend at run time. The pipeline doesn't change.

## The mental model

There are two kinds of context in a cgp file, and keeping them straight is the key
to reading the language:

- **Global context** — everything at the top level of the file is cgp *code*,
  read top to bottom. Variables, `if`/`for`, includes, and target *declarations*
  all live here.
- **Body context** — the text inside a target's `{{ }}` is a *shell template*,
  not cgp code. It is raw shell with `${...}` substitution. cgp captures it
  verbatim and renders it when the job runs.

This is why the language has two kinds of braces:

- `{ ... }` delimits a block of **cgp code** (the body of an `if` or `for`).
- `{{ ... }}` delimits a **shell body** (a target's recipe).

A target ties them together:

```
output.bam: input.fq reference.fa {{
    bwa mem ${reference} ${input} | samtools sort -o ${output}
}}
```

The line before `{{` is cgp code — the output, a colon, and the inputs. The text
between `{{` and `}}` is shell. cgp reads the declaration to build the graph, and
emits the body when the target needs to run.

## Map of the documentation

| Start here | If you want to… |
|------------|-----------------|
| [Getting Started](02-Getting_Started.md) | install cgp and write your first pipeline |
| [Language Syntax](03-Language_Syntax.md) · [Methods](04-Methods_Reference.md) | look up types, variables, operators, methods |
| [Build Targets](05-Build_Targets.md) · [Reserved Targets](06-Reserved_Targets.md) | write real rules: wildcards, temp files, hooks |
| [Pipeline Tutorials](07-Pipeline_Tutorials.md) | learn by worked example |
| [Running Jobs](08-Running_Jobs.md) · [Containers & GPUs](10-Containers_and_GPUs.md) | submit to a scheduler, request resources, run in containers |
| [The Ledger](11-The_Ledger.md) | understand restarts and cross-run reuse |
| [Workflows](12-Workflows.md) · [Manifests & Fan-out](13-Manifests_and_Fanout.md) | chain pipelines and run them across many samples |
| [Configuration Reference](14-Configuration_Reference.md) | every `cgp.*` and `job.*` setting |
| [Troubleshooting](17-Troubleshooting.md) · [Glossary](16-Glossary.md) | debug a problem, look up a term |
| [language-spec.md](language-spec.md) | the precise, normative language reference |

Reference → [language specification](language-spec.md).
