# Comparisons

cgp covers ground that Snakemake, Nextflow, and WDL also cover — building a
dependency graph of jobs and running them on a cluster. This chapter is a candid
look at where cgp is different, to help you decide whether it fits. It is not a
scorecard; each of these tools is good at what it was designed for.

## At a glance

| | cgp | Snakemake | Nextflow | WDL |
|---|-----|-----------|----------|-----|
| Model | output-first rules | output-first rules | dataflow channels | task graph |
| Recipe language | shell, verbatim | shell + Python | shell + Groovy | shell + WDL |
| Host language | small custom DSL | Python | Groovy/DSL2 | WDL + a runner (Cromwell/miniwdl) |
| Runtime | one static binary | Python env | JVM | JVM (Cromwell) / Python (miniwdl) |
| Scheduler submit | built in | built in | built in | via the runner |
| Restart | file timestamps | file timestamps | `-resume` cache | call-caching |

## What's distinctive about cgp

**The recipe is shell, and only shell.** A cgp body is bash you could paste into a
terminal — no embedded Python, no Groovy closures, no string-templating language
wrapped around your command. What you read in the body is what runs. If you can
write the command line, you can write the rule.

**Output-first, like make.** You declare `output: inputs`, and cgp figures out the
order and what's stale. If you've used `make`, the model transfers directly —
including wildcards and a phony default goal. Nextflow's channel/dataflow model is
powerful but is a different way of thinking; cgp asks you to learn less.

**One binary, no runtime.** cgp is a single static executable. There's no Python
environment to pin, no JVM to provision on the cluster, no separate workflow engine
to run. That keeps deployment and "works on my login node" problems small.

**Structure follows the data, in the language you already have.** Because the whole
file is evaluated as code, a `for` loop over chromosomes *is* how you generate
per-chromosome jobs, and reading a sample sheet with `open(...).read_tsv()` and
looping over its rows *is* how you fan out over a cohort — no special operators. The same expression language guards required arguments and computes
output names.

**A focused ledger, not a global cache.** cgp's [ledger](11-The_Ledger.md) records
exactly one thing — which job owns which output — to make cross-run and cross-stage
reuse safe. It isn't a content-addressed cache of every result; restarts are plain
file-timestamp checks. Less magic, fewer surprises about *why* something did or
didn't re-run.

## When another tool may fit better

- You want a **large existing library of community pipelines** today — Nextflow's
  nf-core and Snakemake's wrapper ecosystem are mature.
- Your team already lives in **Python** (Snakemake) or wants **Groovy/dataflow**
  composition (Nextflow) and values that over a small dedicated DSL.
- You need **portability across many execution backends defined by a standard**
  (WDL + Cromwell) for cross-institution sharing.

## When cgp fits well

- You want pipelines that are **readable as shell** by anyone on the team.
- You value a **single binary** and minimal runtime footprint on the cluster.
- You think in **make-style output rules** and want fan-out, containers, and
  scheduler submission without a new execution model.

Reference → [Introduction](01-Introduction.md) for the model in full.
