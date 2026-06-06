# Tutorial 13: Restartable pipelines and the ledger

Two things make a long pipeline survivable: it should **skip work already done**
after an interruption, and it should be **safe to re-run while jobs are still
queued**. The first comes for free from timestamps; the second is what the ledger
adds.

## Restart is automatic

cgp rebuilds an output only if it is **missing or older than an input** — the
make-style rule. So if a pipeline dies halfway, just run it again: finished outputs
are skipped, and only the missing or stale ones rebuild. No flags, no state file,
no "resume mode."

```console
$ cgp pipeline.cgp | bash      # dies after producing trimmed.fq
$ cgp pipeline.cgp | bash      # re-run: trimmed.fq is reused, only aligned.bam runs
```

`-force` overrides this and rebuilds the whole goal graph when you want a clean
redo.

## Turn on the ledger for scheduler reuse

On a cluster there's a second hazard: you submit a pipeline, the jobs are still
*queued*, and you re-run the command. Without coordination you'd resubmit
duplicates. Configure a ledger and a scheduler runner, and cgp instead reuses the
in-flight jobs:

```
#!/usr/bin/env cgp
cgp.ledger = "jobs.db"
cgp.runner = "slurm"

trimmed.fq: a.fastq {{
    job.name = "trim"
    --
    cutadapt ${input} > ${output}
}}
aligned.bam: trimmed.fq {{
    job.name = "align"
    job.mem  = "8G"
    --
    bwa mem ${input} > ${output}
}}
@default: aligned.bam
```

The first submission records ownership and the dependency edge:

```console
$ cgp pipeline.cgp
1001
1002
$ cgp ledger dump jobs.db
1001	NAME	trim
1001	OUTPUT	trimmed.fq
1002	NAME	align
1002	DEP	1001
1002	OUTPUT	aligned.bam
1002	SETTING	mem	8000
```

`1002 DEP 1001` — cgp derived the `afterok` from the `output: input` edge. If you
re-run before the queue drains, an output still owned by an active job is reused
rather than resubmitted:

```
# reuse: aligned.bam already owned by active job 1002
```

This is also how a later workflow [stage](12-stage-workflow.md) waits on files an
earlier stage's jobs are still queued to produce.

## Inspecting and maintaining the ledger

```sh
cgp ledger dump jobs.db                  # everything, as TSV
cgp ledger search -o aligned.bam jobs.db # just jobs producing aligned.bam
cgp ledger vacuum jobs.db                # drop jobs that own no current output
cgp ledger unlock jobs.db                # clear a stale lock (no run active)
```

## Next

- **[Tutorial 14: A status report](14-status-report.md)** — see the whole graph's
  state at a glance.

Reference → [The Ledger](../10-The_Ledger.md),
[language-spec.md §10](../language-spec.md#10-the-ledger-job-tracking).
