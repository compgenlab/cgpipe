# 06 — Cluster resources (one script, any backend)

The same pipeline runs locally or on a cluster — you only change the runner. The
directive block sets resources once; each scheduler maps them to its own headers.

```sh
cgpipe pipeline.cgp | bash          # run locally
cat result.txt

cgpipe -dr pipeline.cgp             # preview the local bash
cgpipe -dr -r slurm pipeline.cgp    # preview the SLURM sbatch script
cgpipe -dr -r sge   pipeline.cgp    # preview the SGE qsub script

cgpipe -r slurm pipeline.cgp        # actually submit (needs sbatch on PATH)
```

The `-r slurm` dry run shows `procs`/`mem`/`walltime` turned into `#SBATCH`
lines; `-r sge` turns the same directives into `#$` lines. You write the
resources once and cgpipe does the per-scheduler mapping.

Concepts: the directive block (before `--`), per-job settings (`procs`, `mem`,
`walltime`, `name`), and runner selection with `-r`. Set a site default once with
`cgpipe.runner = "slurm"` in `~/.cgpipe/config` and your pipelines stay portable.

See [Running Jobs](../../docs/08-Running_Jobs.md) and
[Tutorial 3](../../docs/tutorials/03-resources-and-flags.md).
