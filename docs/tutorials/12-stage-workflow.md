# Tutorial 12: Stage workflows

Chain two standalone pipelines into one command, passing a value from the first to
the second. Each stage stays independently runnable, so you build and test them
separately and compose them with `stage` and `export`.

## Two standalone pipelines

`align.cgp` produces a BAM and **exports** its name:

```
#!/usr/bin/env cgp
cgp.runner.shell.autoexec = true

aligned.bam: ${reads} {{
    echo "aligned from ${input}" > ${output}
}}
@default: aligned.bam
export bam = "aligned.bam"
```

`call.cgp` takes a BAM and produces a VCF:

```
#!/usr/bin/env cgp
cgp.runner.shell.autoexec = true

calls.vcf: ${bam} {{
    echo "called from ${input}" > ${output}
}}
@default: calls.vcf
```

Each runs on its own — `cgp align.cgp --reads reads.fq` builds `aligned.bam`, and
the `export` line is a no-op standalone.

## The workflow

`wgs.cgp` ties them together:

```
#!/usr/bin/env cgp
#
# Two-stage workflow: align, then call.

if !reads { print "ERROR: --reads required"; exit 1 }

stage align align.cgp --reads ${reads}
stage call  call.cgp  --bam ${align.bam}
```

The `call` stage's `--bam ${align.bam}` references the `bam` value the `align`
stage exported.

## Run it

```console
$ cgp wgs.cgp --reads reads.fq
$ cat aligned.bam
aligned from reads.fq
$ cat calls.vcf
called from aligned.bam
```

The stages ran in order; `${align.bam}` resolved to `aligned.bam`, which `call.cgp`
consumed. The value threaded from one pipeline to the next without either pipeline
knowing about the other.

## On a scheduler

Under the shell runner each stage finishes before the next starts, so `call` simply
finds the file `align` wrote. On a scheduler, `align`'s jobs may still be queued
when `call` submits — cgp wires the cross-stage `afterok` dependency through the
[ledger](../10-The_Ledger.md), so configure `cgp.ledger` for scheduler workflows.

## Next

- **[Tutorial 13: Restartable pipelines and the ledger](13-ledger-restart.md)**

Reference → [Workflows](../11-Workflows.md),
[language-spec.md §13](../language-spec.md#13-workflows-stage-and-export).
