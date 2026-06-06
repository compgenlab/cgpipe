# Configuration Reference

cgp reads layered configuration before your pipeline runs. This is where
site-wide and personal defaults live — a scheduler choice, an account, container
settings — so your pipelines stay portable and cluster specifics stay out of
them. Every config file is itself an ordinary cgp script.

## Where config lives

User state lives under one root, `~/.cgp/`:

| Path | Purpose |
|------|---------|
| `<cgp dir>/.cgprc` | Server-wide config next to the installed `cgp` binary (lowest priority) |
| `/etc/cgp/config` | System (site-wide) config |
| `~/.cgp/config` | User config (a cgp script) |
| `~/.cgp/custom_template.cgp` | Custom submission template for the active scheduler runner |
| `~/.cgp/cache/` | Cache / state |

## Resolution order

Each layer overrides the ones before it. A plain `=` in the pipeline always wins;
a `?=` respects whatever an earlier layer set.

1. Built-in defaults
2. Global config next to the binary (`<cgp dir>/.cgprc`)
3. System config (`/etc/cgp/config`)
4. User config (`~/.cgp/config`)
5. Environment (`CGP_ENV`, `CGP_RUN_ID`, `CGP_DRYRUN`)
6. Command-line `--name value`
7. The pipeline script

This is why `threads ?= 4` in a pipeline yields to a `--threads 16` on the command
line (layer 6 beats layer 7's `?=`), while `cgp.runner = "slurm"` in a pipeline
overrides a config default.

## Environment variables

| Variable | Effect |
|----------|--------|
| `CGP_ENV` | Evaluated as a cgp config layer (e.g. `CGP_ENV='cgp.runner="slurm"'`) |
| `CGP_RUN_ID` | Sets the run identifier (same as `cgp.run_id`) |
| `CGP_DRYRUN` | Any value turns on dry-run mode (same as `-dr`) |

## Selected `cgp.*` settings

| Variable | Purpose |
|----------|---------|
| `cgp.runner` | `shell`, `slurm`, `sge`, `pbs`, `batchq`, `graphviz`, `html` |
| `cgp.runner.<name>.<setting>` | Runner-specific options |
| `cgp.runner.<name>.template` | Path to a custom submission template, replacing the built-in for that scheduler |
| `cgp.runner.shell.autoexec` | Shell runner: execute the assembled script instead of printing it (default off) |
| `cgp.ledger` | Ledger database path; enables [cross-run tracking](10-The_Ledger.md) |
| `cgp.run_id` | Run identifier |
| `cgp.shell` | Default shell for rendered bodies |
| `cgp.dryrun` | Set by `-dr` / `CGP_DRYRUN` |
| `cgp.container.engine` | `docker`, `singularity`/`apptainer`; unset disables container wrapping |
| `cgp.container.*` | Bind mounts, env passthrough, engine opts (see [Containers & GPUs](09-Containers_and_GPUs.md)) |
| `cgp.gpu` | Default GPU count for all targets |

Some belt-and-suspenders behaviors are **opt-in**, not default — for instance
`global_hold` (submit every job held until the whole pipeline submits cleanly) and
host-environment capture. Enable them in `~/.cgp/config` if you want them; the core
stays small.

## Per-job settings: the `job.*` surface

Per-job settings are `job.*`. Set a default globally with the prefix; drop the
prefix inside a target's directive block:

```
# global default in ~/.cgp/config
job.mem = "4G"
```
```
# per-target override, prefix dropped
out.bam: in.bam {{
    mem = "16G"
    --
    ...
}}
```

Common settings: `name`, `procs`, `mem`, `walltime`, `stdout`, `stderr`,
`container`, `gpu`, plus the assembly flags `shexec`, `nopre`, `nopost`. See
[Running Jobs](08-Running_Jobs.md) for how each maps onto a scheduler.

## Custom runner templates

The scheduler runners render each job from a template. For most site requirements
the named settings (`account`, `queue`, `qos`, …) and the `custom` directive are
enough. To replace the submission script wholesale, scaffold from the built-in and
edit it:

```sh
cgp show-template -r slurm > ~/.cgp/custom_template.cgp
```

cgp then uses `~/.cgp/custom_template.cgp` for the active scheduler runner, or set
`cgp.runner.<name>.template = "<path>"` for explicit, per-scheduler control. The
explicit key wins over the convention file, which wins over the built-in; only the
template is replaced (submit command and status probes are unchanged). See
[Tutorial 10](tutorials/10-custom-templates.md).

## A typical `~/.cgp/config`

```
# Default to our cluster, with an account and sane resource floors.
cgp.runner = "slurm"
job.account = "lab123"
job.mem ?= "4G"

# Use containers by default.
cgp.container.engine = "singularity"

# Keep a ledger so restarts are cheap and cross-run reuse works.
cgp.ledger = "/scratch/${USER}/cgp-ledger.db"
```

Pipelines then start clean — often just `include "defaults.cgp"` for project
specifics on top of this personal baseline.

## Next

- **[Running Jobs](08-Running_Jobs.md)** · **[Containers & GPUs](09-Containers_and_GPUs.md)** · **[The Ledger](10-The_Ledger.md)**

Reference → [language-spec.md §11](language-spec.md#11-configuration).
