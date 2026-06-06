# Workflows: stage and export

A **pipeline** builds one dependency graph. A **workflow** chains several
standalone pipelines into one command, passing values between them. A `.cgp` file
is a workflow if it contains `stage` statements; otherwise it's a pipeline. Each
stage is itself an ordinary, independently runnable pipeline — which means you can
develop and test each piece on its own, then compose them.

## Declaring stages

`stage NAME FILE ARGS…` declares one stage. The args use the same `--name value`
convention as [command-line variables](03-Language_Syntax.md#command-line-variables),
and become the variables the stage pipeline receives:

```
#!/usr/bin/env cgp
#
# Two-stage workflow: align, then call.

if !reads { print "ERROR: --reads required"; exit 1 }

stage align align.cgp --reads ${reads}
stage call  call.cgp  --bam ${align.bam}
```

Stages run in **declaration order**. `NAME`, `FILE`, and each arg are interpolated
against the workflow's variables first — so a stage's args can reference earlier
stages' exported values (`${align.bam}` above).

## Exposing values with `export`

A stage pipeline hands a value back to the workflow with a top-level
`export name = expr`:

```
# align.cgp — also runnable on its own
aligned.bam: ${reads} {{
    bwa mem ${reads} > ${output}
}}
@default: aligned.bam
export bam = "aligned.bam"
```

When `align.cgp` runs **standalone**, `export` does nothing. When it runs as the
`align` stage, its exported `bam` becomes **`${align.bam}`** — available to later
stages. `export` is non-invasive: adding the line doesn't change standalone
behavior, so a stage stays a normal pipeline you can run and debug by itself.

## Running a workflow

With runnable bodies, the stages execute in order and the value threads through:

```console
$ cgp wgs.cgp --reads reads.fq
$ cat aligned.bam
aligned from reads.fq
$ cat calls.vcf
called from aligned.bam      # call.cgp received --bam aligned.bam via ${align.bam}
```

The `call` stage's `--bam ${align.bam}` resolved to `aligned.bam` — the file the
`align` stage produced — and `call.cgp` built `calls.vcf` from it.

## Cross-stage dependencies

How a later stage waits for an earlier one depends on the runner:

- **Shell runner:** each stage completes before the next begins, so the later stage
  simply reads the earlier stage's files. No coordination needed.
- **Scheduler runner:** an earlier stage's jobs may still be *queued* when a later
  stage submits. The cross-stage `afterok` wiring is resolved through the
  [ledger](10-The_Ledger.md#cross-run-and-cross-stage-reuse) — so a scheduler
  workflow wants `cgp.ledger` configured.

## Export validation catches typos

References to stage exports are checked two ways:

- **Statically, at startup (best-effort):** cgp scans each stage file for every
  *possible* export name (including ones exported only inside `if`/`for` branches).
  A `${NAME.X}` reference to a stage that exports no `X` anywhere fails fast as a
  typo.
- **At runtime (authoritative):** if an export was conditional and didn't fire this
  run, the unset `${NAME.X}` errors when the stage's args are interpolated, naming
  the missing export.

## Next

- **[Tutorial 12: Stage workflows](tutorials/12-stage-workflow.md)** — a worked
  multi-stage example.
- **[Manifests and Fan-out](12-Manifests_and_Fanout.md)** — run a whole workflow
  once per sample.

Reference → [language-spec.md §13](language-spec.md#13-workflows-stage-and-export).
