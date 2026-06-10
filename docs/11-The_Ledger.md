# The Ledger

The **ledger** is an optional SQLite database that records **which job last
produced which output file**. A pipeline runs correctly without one — restarts work
off file timestamps alone. The ledger adds one capability: **safe coordination
across separate runs and stages**, so you can re-run a pipeline whose jobs are
still queued without resubmitting or colliding.

Enable it with a path:

```
cgp.ledger = "/scratch/me/jobs.db"
```

## What it does and doesn't track

The ledger deliberately keeps three concerns separate:

- **The filesystem decides staleness.** "Is this output older than its inputs?" is
  answered by `stat`, not the ledger. The ledger stores **no mtimes**.
- **The ledger records ownership.** Its core fact is *"job 1002 owns
  `aligned.bam`"*, plus that job's inputs, dependency edges, settings, and rendered
  script (for audit).
- **The scheduler owns live state.** Whether a job is queued, running, or done is
  answered by `squeue`/`qstat`. The ledger stores **no job state**.

That separation is why the ledger stays small and never goes stale: it's a
ownership map, not a cache of the world.

## Restart is timestamp-based

Restarting is make-style and needs no ledger: an output rebuilds if it is **missing
or older than any input**. `-force` rebuilds everything in the goal graph
regardless. There are no "restart modes."

The performance win at scale is a **run-scoped stat cache**: within one invocation
each path is `stat`-ed once. A shared `ref.fa` referenced by a thousand
manifest-fan-out runs is checked once, not a thousand times.

## Cross-run and cross-stage reuse

This is what the ledger is *for*. With a ledger configured **and a scheduler
runner**, when a job needs an input that has no producer in this run and isn't on
disk yet, cgp looks the path up in the ledger. If its owning job is still active
(per `squeue`/`qstat`), the new work is wired as a scheduler dependency
(`afterok:<id>`) of that in-flight job — instead of erroring with "no rule to make"
or submitting a duplicate.

So re-running a pipeline before it has finished is safe: already-submitted outputs
are reused, and new downstream work attaches to them. The same mechanism lets a
later workflow [stage](12-Workflows.md) wait on a file an earlier stage's jobs are
still queued to produce. (Under the shell runner every job has already completed
and the file exists, so the lookup is unnecessary.)

A second run that finds an output still owned by an active job reports the reuse
rather than resubmitting:

```
# reuse: a.bam already owned by active job 1001
```

## Inspecting the ledger: `cgp ledger`

```
cgp ledger dump <db>                    dump all jobs as key/value TSV
cgp ledger search [filters] <db>        dump jobs matching the filters
cgp ledger vacuum <db>                  drop jobs that own no current output
cgp ledger unlock <db>                  remove a stale lockfile
```

`dump` prints every job as key/value TSV — provenance you can grep:

```console
$ cgp ledger dump jobs.db
1001	PIPELINE	led.cgp
1001	NAME	trim
1001	OUTPUT	trimmed.fq
1001	INPUT	a.fastq
1001	SRC	cutadapt a.fastq > trimmed.fq
1002	PIPELINE	led.cgp
1002	NAME	align
1002	DEP	1001
1002	OUTPUT	aligned.bam
1002	INPUT	trimmed.fq
1002	SRC	bwa mem trimmed.fq > aligned.bam
1002	SETTING	mem	8000
```

Note `1002 DEP 1001` — the recorded dependency edge. `search` narrows by filter
(all AND-combined, substring match unless noted):

| Filter | Matches |
|--------|---------|
| `-o PATH` | an output path contains `PATH` |
| `-i PATH` | an input path contains `PATH` |
| `-g PATTERN` | a job-script line contains `PATTERN` (grep) |
| `-name NAME` | the job name contains `NAME` |
| `-id JOBID` | the job id (exact) |

```console
$ cgp ledger search -o aligned.bam jobs.db
1002	NAME	align
1002	OUTPUT	aligned.bam
...
```

### Vacuum and unlock

The ledger keeps full history. `vacuum` drops every job that no longer owns a
current output (the last owner of each path always survives, even if it failed),
reclaiming space in one transaction.

The database is guarded by an NFS-safe lockfile so it's safe on network
filesystems. A stale lock from a dead process on the same host is stolen
automatically; otherwise cgp waits briefly, then errors. `cgp ledger unlock <db>`
removes a lock by hand if you're sure no run is active.

## Next

- **[Tutorial 13: Restartable pipelines and the ledger](tutorials/13-ledger-restart.md)**
- **[Workflows](12-Workflows.md)** — where cross-stage reuse comes in.

Reference → [language-spec.md §10](language-spec.md#10-the-ledger-job-tracking),
[§15.2](language-spec.md#152-cgp-ledger).
