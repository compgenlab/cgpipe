# The `convert` Tool

`cgp convert` brings an older `cgpipe`-era script forward to the current cgp
language. It does the mechanical rewriting for you and flags anything it can't
translate safely, so you can move an existing script over without retyping it.

```
cgp convert <old.cgp> [-o out.cgp]
```

It reads the old script and writes the cgp-equivalent to stdout, or to a file with
`-o`. It is **best-effort**: review the result before running it.

## What it rewrites

Given a legacy script:

```
#!/usr/bin/env cgpipe
threads ?= 4
out.bam: in.bam
    <%
    job.mem = "8G"
    job.procs = threads
    %>
    samtools sort -@ ${job.procs} -o $> $<
```

`cgp convert` produces:

```console
$ cgp convert old.cgp
#!/usr/bin/env cgp
threads ?= 4
out.bam: in.bam {{
    job.mem = "8G"
    job.procs = threads
    --
    samtools sort -@ ${job.procs} -o ${output} ${input}
}}
```

It handled, mechanically:

- the **shebang** (`cgpipe` → `cgp`),
- the **body delimiters** — the indented recipe becomes a `{{ }}` block,
- the **settings block** `<% … %>` → a directive block ending in `--`, with each
  per-job setting keeping its `job.` prefix,
- the **build variables** `$>` / `$<` → `${output}` / `${input}` (and `$%` →
  `${stem}`).

More broadly it rewrites the known structural differences: `<% if … %>` / `<% for …
%>` → `%`-control lines, `if … endif` / `for … done` → brace blocks, `__pre__::`
and friends → `@pre` etc., `name::` snippets → `snippet name { }`, `import` → `@name`,
and `cgpipe.*` settings → `cgp.*`.

## What needs your review

The tool annotates anything it can't safely convert with a `# cgp-convert:`
comment, so the conversion never silently changes meaning — those markers are your
to-do list. It rewrites *structure* faithfully, but it can't reason about every
shell reference, so always:

1. `grep cgp-convert` the output for flagged lines.
2. Read the bodies for stale references.
3. Dry-run it (`cgp -dr converted.cgp …`) and compare the rendered scripts to what
   the original produced.

Treat the output as a strong first draft, not a finished port.

## Next

- **[Language Syntax](03-Language_Syntax.md)** · **[Build Targets](05-Build_Targets.md)** — the cgp forms `convert` targets.

Reference → [language-spec.md §15.3](language-spec.md#153-cgp-convert--migrate-an-older-script).
