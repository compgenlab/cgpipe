# Array Jobs

When you submit "the same command over many inputs" — one per file, per
chromosome, per lane — a scheduler **array job** packs all of those tasks into a
*single* submission. Instead of N calls to `sbatch`, cgp sends one
`--array=1-N`; the scheduler runs N tasks, each picking its slice of the work from
a task-id variable. That means far fewer submission round-trips at scale, one job
id to track, and one entry in the queue — which is exactly what clusters with
per-user job-count or submission-rate limits want.

This chapter covers:

- **`for … with i`** — a loop counter, handy for indexing array tasks (and useful
  on its own).
- **`cgp sub --array`** — submit a `cgp sub` fan-out as one array job.
- **Pipeline array jobs** — mark a fan-out rule with `job.array` and cgp coalesces
  it into one array submission, wiring downstream dependencies per task.

## `for … with i` — a loop counter

A normal `for` binds the element. Add `with <name>` to also bind a **1-based**
counter that advances each iteration:

```
samples = ["NA12878", "NA12891", "NA12892"]
for s in samples with i {
    print i, s
}
# 1 NA12878
# 2 NA12891
# 3 NA12892
```

It works over lists and ranges, and — like the element variable — the counter
remains set after the loop. The index is 1-based because scheduler arrays
conventionally start at 1 (SGE in particular disallows task 0).

## `cgp sub --array`

[`cgp sub`](08-Running_Jobs.md#one-off-jobs-cgp-sub) already fans a command out
over a list of files — one job per file, with `{}` placeholders expanded against
each. Add `--array` and cgp submits that fan-out as **one** array job instead of N
separate ones:

```sh
# one sbatch --array=1-3 instead of three sbatch calls
cgp sub -r slurm --array -n qc 'fastqc {} -o qc/' -- a.fastq b.fastq c.fastq
```

The rendered submission carries the array header, and the command becomes a
dispatch table keyed by the scheduler's task-id variable — one branch per file:

```bash
#SBATCH -J qc
#SBATCH --array=1-3

case "$SLURM_ARRAY_TASK_ID" in
  1) fastqc a.fastq -o qc/ ;;
  2) fastqc b.fastq -o qc/ ;;
  3) fastqc c.fastq -o qc/ ;;
  *) echo "cgp: no array task $SLURM_ARRAY_TASK_ID" >&2; exit 1 ;;
esac
```

All the placeholders (`{}`, `{@}`, `{^SUF}`, `{#}`, …) work exactly as in a normal
fan-out — each `case` branch is just that file's fully-rendered command. The big
list works the same way through `--files-from`:

```sh
cgp sub -r slurm --array --files-from samples.txt -m 4G \
    'bwa mem ref.fa {} > {@.fastq.gz}.bam'
```

### Scheduler support

| Runner | `--array` |
|--------|-----------|
| `slurm` | ✓ `#SBATCH --array=`, task var `$SLURM_ARRAY_TASK_ID` |
| `batchq` | ✓ `#BATCHQ -array`, task var `$BATCHQ_ARRAY_TASK_ID` |
| `pbs` | ✓ `#PBS -J`, task var `$PBS_ARRAY_INDEX` |
| `sge`, `shell` | falls back to one job per file (a note is printed) |

### Dependencies

A fixed dependency applies to the whole array, so `--deps` and a fixed `--after`
work as usual — every task waits on the named job(s):

```sh
cgp sub -r slurm --array --deps 4242 'process {}' -- *.dat   # whole array waits on 4242
```

A **`{}`-expanded `--after`** is rejected, because it asks each task to depend on a
*different* job — a per-element dependency that a single array submission (which
carries one dependency directive for all its tasks) cannot express:

```sh
cgp sub -r slurm --array -a '{@}.bam' 'index {}' -- *.bam
# error: --array cannot use a {}-expanded --after (per-element dependency);
#        use a fixed --after, or drop --array
```

If you need per-element dependencies, drop `--array` (submit one job per file).

## Pipeline array jobs

Inside a pipeline, a fan-out is a `for` loop that emits one target per unit of work
(see [Tutorial 4](tutorials/04-map-reduce.md)). Mark the rule with `job.array` set
to the element's **task index** and cgp coalesces the whole fan-out into one array
submission:

```
chroms = ["chr1", "chr2", "chr3"]
calls  = []
for c in chroms with i {
    calls += "calls.${c}.vcf"
    ^calls.${c}.vcf: aligned.bam {{
        job.array = i          # this element's array task index
        job.name  = "call"
        job.mem   = "8G"
        --
        call_variants --region ${c} ${input} > ${output}
    }}
}
merged.vcf: @{calls} {{ bcftools concat ${input} > ${output} }}
@default: merged.vcf
```

cgp submits the three `calls.*.vcf` targets as one `--array=1-3` (the same Form-1
`case` dispatch shown above), and `merged.vcf` waits on the array.

**`job.array` is the task index, not a flag.** The integer you supply — typically
the `with i` counter — *is* the scheduler task id and the `case` branch key, so the
element→task mapping is explicit and stable across runs. Two rules:

- **Unique within the rule.** Two elements resolving to the same index is an error.
- **Submission-compatible.** All elements must share their `job.*` settings (mem,
  procs, name, …); only the command differs. A divergent element (e.g. one asking
  for more memory) is an error — cgp won't silently submit it under the wrong
  resources. Split it out into its own rule, or drop `job.array`.

### Restarts are sparse

On a restart only the stale elements are submitted. If `calls.chr2.vcf` is already
up to date, cgp submits `--array=1,3` and the gather depends only on those two
tasks (`afterok:<id>_1:<id>_3`); chr2 is read from disk. The task indices never
shift, so a dependency recorded for one run still lines up on the next.

### Dependencies

A downstream **gather** that consumes the array depends on exactly the tasks it
needs, by per-task id (`afterok:<arrayid>_<i>`, comma-joined on BatchQ). This is
wired from the `output: input` edges as usual — you don't write it.

A dependent that is *itself an element-wise array* (task *i* depends on the matching
upstream task *i*) needs the scheduler's `aftercorr`, which cgp does **not** wire
yet — that case is a clear error today, with the fix being to drop `job.array` on
one of the two rules (submitting it per-element). Whole-array→array and
array→gather edges work now.

### Scheduler support

| Runner | Pipeline `job.array` |
|--------|----------------------|
| `slurm`, `batchq` | one array submission, per-task dependency ids |
| `sge`, `pbs` | submitted as one job **per element** (correct, just not packed into an array — their per-task id formats differ) |
| `shell` | one function per element, as normal |

### Not yet wired

`aftercorr` for element-wise array→array edges, auto-deconstructing a mismatched
dependent array on restart, and coalescing a [manifest](13-Manifests_and_Fanout.md)
fan-out across rows are planned follow-ups. For embarrassingly-parallel work today,
`job.array` (with a gather) and `cgp sub --array` cover the common cases.

## Next

- **[Containers and GPUs](10-Containers_and_GPUs.md)** — run bodies in images,
  request GPUs.
- **[The Ledger](11-The_Ledger.md)** — restarts and cross-run job reuse.

Reference → [language-spec.md §5.1](language-spec.md#5-statements-and-control-flow),
[§15.1](language-spec.md#151-cgp-sub--one-off-submission).
