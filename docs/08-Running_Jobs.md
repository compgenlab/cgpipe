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

Inside a target's directive block (before `--`), bare `name = value` assignments
set per-job settings. Each scheduler translates them into its own header lines.
The common ones:

| Setting | Meaning |
|---------|---------|
| `name` | Job name |
| `procs` | CPUs / cores |
| `mem` | Memory (e.g. `"8G"`) |
| `walltime` | Wall-time limit (e.g. `"12:00:00"`) |
| `stdout` / `stderr` | Redirect job output |
| `mail` | Notification address |
| `gpu` | GPUs to request (see [Containers & GPUs](09-Containers_and_GPUs.md)) |
| `custom` | Extra directive lines, emitted verbatim |

Set defaults globally with the `job.` prefix (`job.mem = "4G"`); drop the prefix
inside a body's directive block (`mem = "4G"`).

### The same job, four schedulers

This target —

```
out.bam: {{
    name     = "align"
    mem      = "8G"
    procs    = 4
    walltime = "12:00:00"
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

You write `mem`, `procs`, `walltime` once; the per-scheduler mapping is cgp's job.

### `mail`, `custom`

`mail` adds the scheduler's notification directives (defaulting the mail type — on
SLURM, `END,FAIL`):

```
mail = "user@example.com"
```
```bash
#SBATCH --mail-type=END,FAIL
#SBATCH --mail-user=user@example.com
```

`custom` passes through extra directive lines verbatim, for site-specific options
cgp doesn't model:

```
custom = ["--exclusive", "--constraint=haswell"]
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
a.bam: {{ name = "align"; -- ; echo a > ${output} }}
b.bam: a.bam {{ name = "post"; -- ; cp ${input} ${output} }}
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
dependencies, no pipeline file. `cgp sub` does that — everything after `--` is the
command, treated as a cgp body (so `${input}`/`${output}` substitute):

```sh
cgp sub -r slurm -name sort -mem 8G -procs 4 \
    -o sorted.bam -i in.bam \
    -- samtools sort -o ${output} ${input}
```

Options:

| Option | Meaning |
|--------|---------|
| `-name S` | Job name |
| `-mem S` / `-procs N` / `-walltime S` | Resources |
| `-o PATH` | Declared output (repeatable; recorded in the ledger) |
| `-i PATH` | Declared input (repeatable) |
| `-d JOBID` | Depend on an existing job id (repeatable) |
| `-after PATH` | Depend on the active job that owns `PATH` in the ledger (repeatable) |
| `-r NAME` | Runner (`shell` default, or a scheduler) |
| `-ledger PATH` | Ledger database |
| `-dr` | Dry run |

`-after` is the interesting one: it looks up which still-running job last produced
a file (via the ledger) and depends on it — letting you attach a one-off job to a
pipeline that's already in flight.

## Next

- **[Containers and GPUs](09-Containers_and_GPUs.md)** — run bodies in images,
  request GPUs.
- **[Configuration Reference](13-Configuration_Reference.md)** — every setting,
  precedence, and where to put cluster defaults.

Reference → [language-spec.md §11.4](language-spec.md#114-per-job-directives-the-job-surface-prefix-dropped-in-bodies),
[§15.1](language-spec.md#151-cgp-sub--one-off-submission).
