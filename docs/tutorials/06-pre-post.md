# Tutorial 6: Shared `@pre` and `@post`

You usually want the *same* preamble on every job — log the output name, stamp the
start time, set up an environment. Rather than copy it into each body, define it
once with the `@pre` and `@post` reserved targets.

## The script

`wrapped.cgp`:

```
#!/usr/bin/env cgp

@pre {{
    echo "==> ${output}"
}}
@post {{
    echo "<== ${output}"
}}

out.txt: {{
    echo hi > ${output}
}}
@default: out.txt
```

`@pre` is prepended, and `@post` appended, to every target body. Inside them
`${output}` refers to the *wrapped* job's output, so each job logs its own files.

## Render it

```console
$ cgp -dr wrapped.cgp
#!/usr/bin/env bash
set -euo pipefail

# ---- out.txt ----
echo "==> out.txt"
echo hi > out.txt
echo "<== out.txt"
```

The body is sandwiched between the two hooks. Add a hundred targets and they all
get the same wrapper for free.

## Opting a job out

Some jobs shouldn't be wrapped — a quick `mkdir`, or a job whose logging would be
noise. Opt out per target with the `job.nopre` / `job.nopost` directives:

```
bare.txt: {{
    job.nopre  = true
    job.nopost = true
    --
    echo b > ${output}
}}
```

`bare.txt` renders as just `echo b > bare.txt`.

## Once-per-pipeline work: `@setup` / `@teardown`

`@pre`/`@post` run per job. For work that should happen **once** — before anything
else, or after everything — use `@setup` and `@teardown`. These usually run on the
submit host rather than as submitted jobs, via `job.shexec = true`:

```
@setup {{
    job.shexec = true
    --
    mkdir -p logs
}}
@teardown {{
    job.shexec = true
    --
    echo "pipeline complete"
}}
out.txt: {{
    echo hi > ${output}
}}
@default: out.txt
```

renders:

```bash
# ---- @setup ----
mkdir -p logs

# ---- out.txt ----
echo hi > out.txt

# ---- @teardown ----
echo "pipeline complete"
```

`@setup` is first, `@teardown` last, and the `shexec` bodies run directly on the
submit host (so a `mkdir` happens before any jobs are queued).

## Deferring evaluation

A subtlety with `@pre`: if you want a command to run *when the job runs* rather than
when cgp renders the script, defer it with `\$(…)`:

```
@pre {{
    echo "Start: \$(date)"
}}
```

Without the backslash, `$(date)` would run once at render time and bake the same
timestamp into every job. See
[Build Targets § the body is a shell template](../05-Build_Targets.md#the-body-is-a-shell-template).

## Next

- **[Tutorial 7: Importable snippets](07-snippets.md)** — share a body *fragment*
  without wrapping every job.

Reference → [Reserved Targets](../06-Reserved_Targets.md),
[language-spec.md §8](../language-spec.md#8-reserved-targets--prefixed).
