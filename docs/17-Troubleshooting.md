# Troubleshooting

When a pipeline misbehaves, these are the tools and the common errors, with what
each one means.

## Start with `-dr`

The first move, almost always, is a dry run. `-dr` renders exactly what a real run
would do — the assembled shell script, or the scheduler submission scripts — without
running anything:

```sh
cgp -dr pipeline.cgp
cgp -dr -r slurm pipeline.cgp
```

If a variable resolved wrong, a path came out malformed, or a directive didn't take
effect, you'll see it in the rendered output. Reach for `-dr` before guessing.

## `$(cmd)` runs at render time — even under `-dr`

The single most surprising behavior. cgpipe's own `$(cmd)` command substitution is
evaluated *while the body is rendered* — which means it runs during a dry run too,
because rendering is what evaluates it. So:

```
out.txt: {{
    echo "built $(date)" > ${output}
}}
```

bakes the render-time date into the script. If you wanted the command to run in the
**job's** shell at run time, escape it:

```
out.txt: {{
    echo "built \$(date)" > ${output}
}}
```

`\$(date)` is emitted verbatim and runs when the job runs. The same applies to
`\${VAR}` for shell variables you don't want cgpipe to interpret.

## Common errors

### `no rule to make "X" (needed by [Y])`

A target needs input `X`, but `X` doesn't exist on disk and no rule produces it.
Either the file is genuinely missing, or you have a typo in an output/input name so
two rules don't connect. Under a scheduler with a [ledger](11-The_Ledger.md), `X`
is also looked up there — if its owning job is still active, the dependency wires
up instead of erroring.

### `undefined variable in ${nope}`

A `${nope}` referenced a variable that was never set, which is an error. If empty is
acceptable, use `${nope?}` (yields `""` when unset). If it's required, guard it up
front so the failure is clear:

```
if !ref { print "ERROR: --ref required"; exit 1 }
```

### `expected }}, got EOF`

A `{{ }}` body wasn't closed by a **lone `}}` on its own line**. Single-line bodies
aren't allowed — put the closing `}}` on its own line:

```
# wrong
out.txt: {{ echo hi > ${output} }}

# right
out.txt: {{
    echo hi > ${output}
}}
```

### A directive seems to be ignored

If you set `job.mem` or `job.procs` but it had no effect, check for the `--`
separator. A body with **no `--` is entirely shell** — a `job.mem = "8G"` line with
no `--` above it is passed through to the shell as a (broken) command, not
interpreted as a directive. Open the directive block:

```
out.bam: in.bam {{
    job.mem = "8G"
    --
    ...
}}
```

### A boolean flag swallowed the filename

`cgp --adaptive pipeline.cgp` reads `pipeline.cgp` as the value of `--adaptive`.
Put the pipeline file first (`cgp pipeline.cgp --adaptive`), or write
`--adaptive=true`.

### A failed job left a corrupt output that won't rebuild

cgpipe judges staleness by mtime, not by exit status — only the scheduler knows if a
job succeeded. A job killed mid-write can leave a **half-written output** that
looks newer than its inputs, so the next run skips it. Write atomically to avoid
this: send output to `${output}.tmp` and `mv` it into place only on success
(`cmd > ${output}.tmp && mv ${output}.tmp ${output}`). See
[Write atomically](05-Build_Targets.md#write-atomically-temp-file-then-rename).
To force a rebuild now, delete the bad file (or run with `-force`).

## Inspecting state

- **`cgp ledger dump <dir>`** — the full provenance of every recorded job; grep it
  for an output to see who produced it and with what command.
  [The Ledger](11-The_Ledger.md#inspecting-the-ledger-cgpipe-ledger).
- **`cgp ledger status [-r RUNNER] [-output] <dir>`** — ask the scheduler what is
  happening with the recorded jobs right now (native status per job, or per output
  reconciled against the file on disk).
- **`cgp ledger vacuum <dir>`** — compact the ledger to a single `snapshot.jsonl`,
  dropping jobs that no longer own a current output.
- **`-r graphviz` / `-r html`** — visualize the dependency graph and (with a ledger)
  each output's live state. [Tutorial 14](tutorials/14-status-report.md).
- **`dumpvars`** — drop this statement into a script to print all in-scope variables
  at that point.

## Containers: a bind-mount surprise

If a containerized job can't see a file, it's usually outside the auto-mounted
working directory. cgpipe mounts the workdir (and `/tmp`) but not arbitrary paths — add
the others explicitly:

```
job.container.bind = ["/data", "/refs"]
```

[Containers and GPUs](10-Containers_and_GPUs.md#tuning-the-invocation).

## Next

- **[Glossary](16-Glossary.md)** — look up a term.
- **[language-spec.md](language-spec.md)** — the precise behavior when a doc is
  ambiguous.
