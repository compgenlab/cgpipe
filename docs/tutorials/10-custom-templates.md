# Tutorial 10: Custom job submission

Real clusters have local requirements — an account to bill, a partition to target,
a QoS, a mandatory `--constraint`. cgp gives you two levers for these: **named
settings** that the scheduler templates already understand, and a **`job.custom`**
escape hatch for anything they don't.

## Named settings

The SLURM template understands more than the common
`job.name`/`job.mem`/`job.procs`/`job.walltime`. Set any of these in a directive
block (the `job.` prefix is the same whether you set them per target or globally
in config):

| Setting | SLURM line |
|---------|-----------|
| `job.account` | `#SBATCH -A <account>` |
| `job.queue` | `#SBATCH -p <queue>` (partition) |
| `job.qos` | `#SBATCH --qos=<qos>` |
| `job.nice` | `#SBATCH --nice=<nice>` |
| `job.mail` | `#SBATCH --mail-type=… --mail-user=…` |
| `job.stdout` / `job.stderr` | `#SBATCH -o` / `-e` |

```
out.bam: {{
    job.name    = "j"
    job.account = "lab123"
    job.queue   = "highmem"
    job.qos     = "long"
    --
    run > ${output}
}}
```

```console
$ cgp -dr -r slurm pipeline.cgp
...
#SBATCH -J j
#SBATCH --qos=long
#SBATCH -p highmem
#SBATCH -A lab123
...
```

These are best set **once** in `~/.cgp/config`, so every pipeline inherits your
cluster's account and partition without mentioning them:

```
# ~/.cgp/config
cgp.runner   = "slurm"
job.account  = "lab123"
job.queue    = "highmem"
```

## The `job.custom` escape hatch

For directives cgp doesn't model, `job.custom` is a list of lines emitted verbatim
as that scheduler's directives:

```
out.bam: {{
    job.name   = "j"
    job.custom = ["--exclusive", "--constraint=haswell"]
    --
    run ${output}
}}
```

```console
$ cgp -dr -r slurm pipeline.cgp
...
#SBATCH -J j
#SBATCH --exclusive
#SBATCH --constraint=haswell
...
```

Because `job.custom` lines are passed straight through, you can express any
site-specific `#SBATCH`/`#$`/`#PBS` directive without waiting for cgp to grow a
setting for it. Set it globally (`job.custom = ["--account=lab123"]`) to apply it
to every job.

## How the templates work

Each scheduler renders its submission script from a template — itself written in
cgp's body language (`${...}` substitution and `%`-control lines). The SLURM
template, for example, contains:

```
#!${job.shell}
#SBATCH -J ${job.name}
% if job.walltime {
#SBATCH -t ${job.walltime}
% }
% if job.account {
#SBATCH -A ${job.account}
% }
% for c in job.custom {
#SBATCH ${c}
% }

set -eo pipefail
${_body}
```

The `% if job.account { … }` blocks are why a setting only appears when you set it. The
built-in templates (SLURM, SGE, PBS, BatchQ) live with the binary and cover the
common cluster setups.

## Replacing the whole template

When the named settings and `custom` aren't enough — your site needs a different
script structure, a module-load preamble, or directives cgp doesn't model — supply
your **own** template. Start from the built-in:

```sh
cgp show-template -r slurm > ~/.cgp/custom_template.cgp
```

Edit that file (it's the body language: `${job.name}`, `${job.mem}`, `${job.procs}`,
`${job.walltime}`, `${job.custom}`, `${job.depids}`, the rendered job as `${_body}`,
etc.), and
cgp uses it for the active scheduler runner. Two ways to point at a template:

| Source | Scope |
|--------|-------|
| `~/.cgp/custom_template.cgp` | A single file, applied to whichever scheduler runner is active (most people target one cluster) |
| `cgp.runner.<name>.template = "<path>"` | Explicit and per-scheduler — set in `~/.cgp/config`, a site config, or the pipeline |

**Precedence:** the explicit config key wins, then the convention file, then the
built-in. Only the *template* is replaced — the submit command, the status/`squeue`
probes, and mem normalization stay as configured. A config key pointing at a
missing file is a hard error (so a typo fails loudly).

```
# ~/.cgp/config — force a vetted site template for SLURM
cgp.runner.slurm.template = "/etc/cgp/slurm.template.cgp"
```

## Next

- **[Tutorial 11: Sample sheets](11-sample-sheets.md)** — read a sample sheet and
  scatter/gather over its rows in one pipeline.

Reference → [Running Jobs](../08-Running_Jobs.md),
[Configuration Reference](../14-Configuration_Reference.md).
