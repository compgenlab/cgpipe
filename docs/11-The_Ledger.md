# The Ledger

The **ledger** is an optional on-disk record of **which job last produced which
output file**. A pipeline runs correctly without one — restarts work off file
timestamps alone. The ledger adds one capability: **safe coordination across
separate runs and stages**, so you can re-run a pipeline whose jobs are still
queued without resubmitting or colliding.

Enable it with a path — the ledger is a **directory**, created if it doesn't exist:

```
cgp.ledger = "/scratch/me/jobs.ledger"
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

That separation is why the ledger stays small and never goes stale: it's an
ownership map, not a cache of the world.

## How it's stored

The ledger directory holds **append-only JSON-lines files** — one line per
recorded job. Each running process appends to **its own file**
(`<host>-<pid>-…​.jsonl`); no file is ever shared between processes, and **no lock
is taken**. Reading folds every file together, newest record winning per output.

This layout is deliberately robust on shared filesystems (NFS, Lustre): appending
to a private file is the safest operation such filesystems offer, a half-written
record can never corrupt more than its own trailing line, and two runs touching
the same ledger at once simply each write their own file. The files are plain text
— you can `cat`, `grep`, or `jq` them directly.

## Restart is timestamp-based

Restarting is make-style and needs no ledger: an output rebuilds if it is **missing
or older than any input**. `-force` rebuilds everything in the goal graph
regardless. There are no "restart modes."

The performance win at scale is a **run-scoped stat cache**: within one invocation
each path is `stat`-ed once. A shared `ref.fa` referenced by a thousand
sample-sheet rows is checked once, not a thousand times.

## Cross-run and cross-stage reuse

This is what the ledger is *for*. With a ledger configured **and a scheduler
runner**, when a job needs an input that has no producer in this run and isn't on
disk yet, cgpipe looks the path up in the ledger. If its owning job is still active
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
cgp ledger dump <dir>                   dump all jobs as key/value TSV
cgp ledger search [filters] <dir>       dump jobs matching the filters
cgp ledger status [-r RUNNER] [-output] <dir>   live scheduler status per job (or output)
cgp ledger vacuum <dir>                 compact the ledger, dropping jobs that own no current output
```

`dump` prints every job as key/value TSV — provenance you can grep:

```console
$ cgp ledger dump jobs.ledger
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
$ cgp ledger search -o aligned.bam jobs.ledger
1002	NAME	align
1002	OUTPUT	aligned.bam
...
```

### Status

`status` asks the scheduler what is happening with the recorded jobs right now —
a pipeline-free view of the queue. It needs a scheduler runner to query: pass
`-r <runner>` or let it pick up `cgp.runner` (and, if you omit `<dir>`,
`cgp.ledger`) from your config.

The status word shown is the scheduler's **native** one, so cluster-specific
states stay visible (SLURM `PENDING`/`COMPLETED`, batchq `PROXYQUEUED`, SGE `qw`,
…). A job the scheduler no longer knows about reads `UNKNOWN`.

```console
$ cgp ledger status -r batchq jobs.ledger
1001	SUCCESS	trim
1002	RUNNING	align
1003	PROXYQUEUED	merge
```

With `-output`, each row is an **output file** mapped to the most recent job that
claims it, and the status is reconciled against the file on disk:

```console
$ cgp ledger status -r batchq -output jobs.ledger
trimmed.fq	1001	SUCCESS
aligned.bam	1002	RUNNING
old.bam	1009	COMPLETE
stray.bam	1010	DIRTY
```

For a job still queued or running, the row is just that live status. For a
finished or aged-out job the file's modification time is checked against the
job's submit time (and, where the scheduler reports one, its end time):

- **COMPLETE** — the owning job has aged out of the scheduler but the file exists
  and is at least as new as the job's submission.
- **DIRTY** — the file is missing, older than the job's submission, or was
  modified well after the job ended (more than five minutes) — i.e. it does not
  line up with the job that is supposed to have produced it.

The end-time upper bound is best-effort: it is applied only for schedulers that
report a completion time (SLURM via `scontrol`, batchq via `batchq status -e`), so
on others only the submit-time lower bound is enforced.

### Vacuum

The ledger keeps full history, accumulating one record per submission. `vacuum`
rewrites the directory as a single compacted `snapshot.jsonl` containing only the
jobs that still own a current output — the last owner of each path always
survives, even if it failed. Per-process logs still being appended by a live run
are left in place and reclaimed by a later vacuum once idle. Run it when nothing
else is writing the ledger.

The ledger takes no cross-process lock — each run appends only to its own file —
so there is no `unlock` subcommand and never a stale lock to clear.

## Next

- **[Tutorial 13: Restartable pipelines and the ledger](tutorials/13-ledger-restart.md)**
- **[Workflows](12-Workflows.md)** — where cross-stage reuse comes in.

Reference → [language-spec.md §10](language-spec.md#10-the-ledger-job-tracking),
[§15.2](language-spec.md#152-cgpipe-ledger).
