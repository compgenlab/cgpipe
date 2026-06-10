# Array Jobs

When you submit "the same command over many inputs" — one per file, per
chromosome, per lane — a scheduler **array job** packs all of those tasks into a
*single* submission. Instead of N calls to `sbatch`, cgp sends one
`--array=1-N`; the scheduler runs N tasks, each picking its slice of the work from
a task-id variable. That means far fewer submission round-trips at scale, one job
id to track, and one entry in the queue — which is exactly what clusters with
per-user job-count or submission-rate limits want.

This chapter covers the two pieces that are available today:

- **`for … with i`** — a loop counter, handy for indexing array tasks (and useful
  on its own).
- **`cgp sub --array`** — submit a `cgp sub` fan-out as one array job.

Pipeline-level array jobs (marking a target rule with `job.array`) are
[planned](#pipeline-array-jobs-planned) — see the end of the chapter.

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

If you need per-element dependencies, drop `--array` (submit one job per file) or
wait for pipeline array jobs below.

## Pipeline array jobs (planned)

> **Status: planned — not yet implemented.** The design is settled; this section
> describes where it is headed so the surface is predictable.

Inside a pipeline, a fan-out is a `for` loop that emits one target per unit of
work (see [Tutorial 4](tutorials/04-map-reduce.md)). The plan is to coalesce such
a fan-out into one array submission by marking the rule with `job.array` set to
the element's **task index**:

```
for c in chroms with i {
    ^calls.${c}.vcf: aligned.bam {{
        job.array = i          # this element's array task index
        job.mem   = "8G"
        --
        call_variants --region ${c} ${input} > ${output}
    }}
}
merged.vcf: @{calls.*.vcf} {{ bcftools concat ${input} > ${output} }}
```

The intent, in brief:

- `job.array` is the **task index**, not a flag — the integer you supply (e.g.
  `with i`) becomes the scheduler task id, so the mapping from element to task is
  explicit and stable. Indices must be unique within a rule and all elements must
  be submission-compatible (same resources/shape), else cgp errors rather than
  guessing.
- On a restart, only the stale elements are submitted — `--array=2,4` — and a
  downstream **gather** that consumes the whole array depends on it with a normal
  whole-array `afterok`.
- A dependent that is *itself* an element-wise array needs per-task dependencies
  (`aftercorr`); that, and submitting a divergent fan-out as plain per-job
  submissions via a `-no-array` switch, are part of the same upcoming work.

Until then, use `cgp sub --array` for embarrassingly-parallel fan-outs, or submit
one job per target as today.

## Next

- **[Containers and GPUs](10-Containers_and_GPUs.md)** — run bodies in images,
  request GPUs.
- **[The Ledger](11-The_Ledger.md)** — restarts and cross-run job reuse.

Reference → [language-spec.md §5.1](language-spec.md#5-statements-and-control-flow),
[§15.1](language-spec.md#151-cgp-sub--one-off-submission).
