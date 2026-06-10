# Running Jobs

A cgp pipeline describes work; a **runner** carries it out. You choose the runner
at run time, and the same targets become a bash script, scheduler submissions, a
graph, or a status page — without editing the pipeline. This chapter covers the
runners, the per-job settings they consume, how dependencies are wired, dry runs,
and one-off submission with `cgp sub`.

## Runners

Select with `-r NAME`, or set `cgp.runner` in the script or config.

| Runner | What it does |
|--------|--------------|
| `shell` (default) | Assemble one bash script — printed to stdout, or executed if `cgp.runner.shell.autoexec` is set |
| `slurm` | Submit each job with `sbatch`, wiring dependencies |
| `sge` | Submit with `qsub` (Sun Grid Engine / OGE) |
| `pbs` | Submit with `qsub` (PBS / Torque) |
| `batchq` | Submit with `batchq submit` |
| `graphviz` | Emit the dependency graph as DOT |
| `html` | A self-contained HTML status report |

The scheduler runners share one model: each job is rendered to a submission
script from a per-scheduler template, piped to the submit command, and its
assigned id captured and printed.

## Per-job settings

Per-job settings live under the `job.` namespace. Inside a target's directive
block (before `--`), `job.name = value` assignments set them; each scheduler
translates them into its own header lines. The common ones:

| Setting | Meaning |
|---------|---------|
| `job.name` | Job name |
| `job.procs` | CPUs / cores (defaults to 1) |
| `job.mem` | Memory (e.g. `"8G"`) |
| `job.walltime` | Wall-time limit (e.g. `"12:00:00"`) |
| `job.stdout` / `job.stderr` | Redirect job output |
| `job.queue` | Queue / partition |
| `job.account` | Accounting/billing project |
| `job.mail` | Notification address |
| `job.gpu` | GPUs to request (see [Containers & GPUs](09-Containers_and_GPUs.md)) |
| `job.env` | Capture the submit-host environment into the job (SLURM `--export=ALL`, SGE/PBS `-V`, BatchQ `-env`) |
| `job.hold` | Submit this job held (release it later by hand) |
| `job.setup` | A list of shell lines emitted at the top of the submission script, before the body |
| `job.custom` | Extra directive lines, emitted verbatim |

A few settings are scheduler-specific (silently ignored elsewhere):

| Setting | Scheduler | Meaning |
|---------|-----------|---------|
| `job.qos` | SLURM, PBS | Quality-of-service |
| `job.nice` | SLURM | Scheduling priority adjustment (`--nice`) |
| `parallelenv` | SGE | Parallel-environment name, required for `-pe` when `job.procs > 1` (usually set once as `cgp.runner.sge.parallelenv` in config) |

The same `job.` prefix applies whether you set a default globally before your
targets (`job.mem = "4G"`) or inside a body's directive block (`job.mem = "4G"`).
A setting is captured per target at definition time, so a global `job.mem = "8G"`
near the top becomes the default for every target defined after it, unless a
target overrides it. To hold the *entire* pipeline until it submits cleanly, see
`global_hold` in the [Configuration Reference](13-Configuration_Reference.md).

### The same job, four schedulers

This target —

```
out.bam: {{
    job.name     = "align"
    job.mem      = "8G"
    job.procs    = 4
    job.walltime = "12:00:00"
    --
    echo aligning > ${output}
}}
```

— renders different headers per scheduler. SLURM (`-r slurm`):

```bash
#SBATCH -J align
#SBATCH -t 12:00:00
#SBATCH -c 4
#SBATCH -n 1
#SBATCH --mem=8000
```

SGE (`-r sge`):

```bash
#$ -N align
#$ -l h_rt=12:00:00
#$ -l h_vmem=8G
```

PBS (`-r pbs`):

```bash
#PBS -N align
#PBS -l nodes=1:ppn=4,walltime=12:00:00,mem=8gb
```

BatchQ (`-r batchq`):

```bash
#BATCHQ -name align
#BATCHQ -procs 4
#BATCHQ -mem 8G
#BATCHQ -walltime 12:00:00
```

You write `job.mem`, `job.procs`, `job.walltime` once; the per-scheduler mapping is
cgp's job.

### `job.mail`, `job.custom`

`job.mail` adds the scheduler's notification directives (defaulting the mail type —
on SLURM, `END,FAIL`):

```
job.mail = "user@example.com"
```
```bash
#SBATCH --mail-type=END,FAIL
#SBATCH --mail-user=user@example.com
```

`job.custom` passes through extra directive lines verbatim, for site-specific
options cgp doesn't model:

```
job.custom = ["--exclusive", "--constraint=haswell"]
```
```bash
#SBATCH --exclusive
#SBATCH --constraint=haswell
```

When even `custom` isn't enough and you need a different submission *script*,
replace the whole template: `cgp show-template -r slurm > ~/.cgp/custom_template.cgp`,
edit it, and cgp uses it (or set `cgp.runner.<name>.template`). See
[Tutorial 10](tutorials/10-custom-templates.md).

## Dependencies are wired for you

When one target's output is another's input, cgp submits them in order and passes
the upstream job id into the downstream submission. For:

```
a.bam: {{ job.name = "align"; -- ; echo a > ${output} }}
b.bam: a.bam {{ job.name = "post"; -- ; cp ${input} ${output} }}
```

`a.bam` submits first (id `1001`); `b.bam` submits second carrying the dependency:

```bash
#SBATCH -J post
#SBATCH -d afterok:1001
```

You never write `afterok` yourself — it follows from the `output: input` edges.
Cross-*run* and cross-*stage* dependencies are resolved through the
[ledger](10-The_Ledger.md).

## Dry runs

`-dr` renders what a real run *would* submit — the full submission scripts — without
submitting. It is the first tool to reach for when something looks wrong:

```sh
cgp -dr -r slurm pipeline.cgp
```

> Remember that cgp's own `$(cmd)` substitution runs at render time, so it executes
> under `-dr` too. Use `\$(cmd)` to defer to the job's shell. See
> [Troubleshooting](16-Troubleshooting.md).

## One-off jobs: `cgp sub`

Sometimes you just want to submit a single command with resources and
dependencies, no pipeline file. `cgp sub` does that. The first token that is not a
recognized option begins the command; everything from there until a bare `--` is
the command, treated as a cgp body (so `${input}`/`${output}` substitute):

```sh
cgp sub -r slurm -n sort -m 8G -p 4 \
    -o sorted.bam -i in.bam \
    samtools sort -o ${output} ${input}
```

Options:

| Option | Meaning |
|--------|---------|
| `-n, --name S` | Job name |
| `-m, --mem S` / `-p, --procs N` / `-t, --walltime S` | Resources |
| `-o, --output PATH` | Declared output (repeatable; recorded in the ledger) |
| `-i, --input PATH` | Declared input (repeatable) |
| `-d, --deps IDS` | Depend on existing job ids (comma-separated; repeatable) |
| `-a, --after PATH` | Depend on the active job that owns `PATH` in the ledger (repeatable) |
| `-f, --files-from F` | Read fan-out files from `F`, one per line (`-` = stdin; only once) |
| `-r, --runner NAME` | Runner (`shell` default, or a scheduler) |
| `-l, --ledger PATH` | Ledger database |
| `-dr` | Dry run |
| `-h, --help` | Show help |

`-a`/`--after` is the interesting one: it looks up which still-running job last
produced a file (via the ledger) and depends on it — letting you attach a one-off
job to a pipeline that's already in flight.

> **Quote your redirects.** Pipes and redirects in the command run *before* `--`,
> so an unquoted `>` or `|` would be applied to `cgp` itself by your shell. Quote
> the affected part — `'sort {} > {@.txt}.sorted'` — so it reaches the job.

### Fan-out: one job per file

List files *after* `--` and `cgp sub` submits one independent job per file. Inside
the command (and in `-o`/`-i`/`-a` and the job name) `{}` placeholders expand to the
current file:

```sh
cgp sub -r slurm -m 4G -o '{@.fastq.gz}.bam' \
    'bwa mem ref.fa {} > {@.fastq.gz}.bam' \
    -- sampleA.fastq.gz sampleB.fastq.gz
```

| Placeholder | Expands to |
|-------------|------------|
| `{}` `{^}` | the full input path |
| `{@}` | the basename (directory stripped) |
| `{^SUF}` | the full path with a trailing `SUF` removed (if it ends with `SUF`) |
| `{@SUF}` | the basename with a trailing `SUF` removed (if it ends with `SUF`) |
| `{#}` | the 1-based fan-out index |
| `{{}}` | a literal `{}` |

For long file lists that would overflow the command line, read them from a file (or
stdin) with `--files-from` instead of globbing on the command line — no `--` needed:

```sh
find data -name '*.fastq.gz' > inputs.txt
cgp sub -m 4G --files-from inputs.txt -o '{@.fastq.gz}.bam' \
    'bwa mem ref.fa {} > {@.fastq.gz}.bam'
```

Fan-out jobs are independent siblings: each fan-out file is its job's primary input,
`-d`/`--deps` applies to every job, and `-a`/`--after` is resolved per file (after
`{}` expansion). Use `-dr` to preview every rendered job before submitting.

## Next

- **[Containers and GPUs](09-Containers_and_GPUs.md)** — run bodies in images,
  request GPUs.
- **[Configuration Reference](13-Configuration_Reference.md)** — every setting,
  precedence, and where to put cluster defaults.

Reference → [language-spec.md §11.4](language-spec.md#114-per-job-settings-the-job-namespace),
[§15.1](language-spec.md#151-cgp-sub--one-off-submission).
