# Reserved Targets

Some target names begin with `@`. **The rule: a target name beginning with `@` is
a reserved cgpipe target and never names a file on disk.** That is what lets reserved
names coexist with real filenames — a pipeline can still produce a file literally
called `pre`; only `@pre` is reserved.

(The `@` in a target *header* is distinct from `@{…}` list expansion and from
`@name` snippet invocation inside a body — same sigil, three positions.)

| Target | When it runs |
|--------|--------------|
| `@pre {{ }}` | Prepended to every other target's body (unless `nopre`) |
| `@post {{ }}` | Appended to every other target's body (unless `nopost`) |
| `@setup {{ }}` | Once, as the first job in the pipeline |
| `@teardown {{ }}` | Once, as the last job |
| `@postsubmit {{ }}` | Once per submitted job, on the submit host, right after submission |
| `@default: …` | The goals to build when none are named (no body) |

All examples are rendered with `cgpipe -dr` and match fixtures under
[`tests/build/`](../tests/build/).

## `@pre` and `@post`

`@pre` is prepended, and `@post` appended, to **every** target body. Use them for
shared per-job preambles — logging, timing, environment setup:

```
@pre {{
    echo "==> ${output}"
}}
@post {{
    echo "<== ${output}"
}}
out.txt: {{
    echo hi > ${output}
}}
```

renders, for `out.txt`:

```bash
echo "==> out.txt"
echo hi > out.txt
echo "<== out.txt"
```

Note `${output}` inside `@pre`/`@post` refers to the *wrapped* target's output —
the hooks see each job's own files.

### Opting out: `nopre` / `nopost`

A target opts out of wrapping with the `job.nopre` / `job.nopost` directives:

```
bare.txt: {{
    job.nopre  = true
    job.nopost = true
    --
    echo b > ${output}
}}
```

`bare.txt` renders with just `echo b > bare.txt`, no wrappers.

## `@setup` and `@teardown`

`@setup` runs once as the first job; `@teardown` once as the last. They are the
place for one-time work like creating directories. Typically they run **on the
submit host** rather than as submitted jobs — set `job.shexec = true`:

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
```

`job.shexec = true` runs the body directly on the submission host instead of
submitting it (the usual choice for `mkdir`-style setup). Only `@setup` and
`@teardown` may be `shexec`; `@postsubmit` always is. Under `-dr`, shexec bodies
still render so you can see them.

## `@postsubmit`

`@postsubmit` runs once for **each** submitted job, synchronously, on the submit
host, immediately after that job is submitted. Its body sees the just-submitted
job's `${input}` / `${output}` / `${stem}`, plus **`${jobid}`** — the
scheduler-assigned id (empty under the shell runner, which has no ids). A typical
use is recording submissions:

```
@postsubmit {{
    echo "submitted ${output} as ${jobid}"
}}
```

Submitting a two-job pipeline to SLURM prints, as each job is submitted:

```
submitted a.bam as 1001
submitted b.bam as 1002
```

## The default goal: `@default`

`@default` declares what cgpipe builds when invoked with no explicit target. Its
**inputs are the goals**; it has no body:

```
@default: final.vcf report.html
```

- **No phony file** — `@default` is never stat-ed or expected on disk.
- **Forces its goals to build**, exactly as if requested on the command line.
- **Fallback:** with no `@default`, cgpipe builds the **first defined target**, so
  trivial pipelines need nothing.
- **CLI overrides:** `cgpipe p.cgp` builds the `@default` goals; `cgpipe p.cgp final.vcf`
  builds the named target(s) instead.
- **Accumulates:** multiple `@default:` lines (across the file, `include`s, or a
  loop) add to the goal set — so `@default: @{all_outputs}` after a `for` loop
  works.

## Next

- **[Tutorial 6: Shared `@pre`/`@post`](tutorials/06-pre-post.md)** — these hooks in
  a real pipeline.
- **[Running Jobs](08-Running_Jobs.md)** — what the directives configure.

Reference → [language-spec.md §8](language-spec.md#8-reserved-targets--prefixed).
