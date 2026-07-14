# Migrating from cgpipe-jvm to cgp

This is the one place these docs compare the new `cgp` (the Go rewrite) to the legacy
JVM `cgpipe`: a migration guide for people with existing `.cgp` scripts written for
the old runner. Everywhere else the docs describe `cgp` on its own terms. If you are
writing a *new* pipeline, skip this and read [`cgpipe-for-llms.md`](cgpipe-for-llms.md)
or the [chapter guide](README.md) instead.

Most changes are **mechanical and line-level** — the *model* (output-first rules, a
shell body per target, mtime-based staleness, scheduler submission) is unchanged. Much
of the rewrite is automated: run

```sh
cgp convert old.cgp -o new.cgp
```

It rewrites everything below and annotates anything it can't safely translate with an
inline `# cgpipe-convert:` comment for you to finish by hand. This document explains
what it changes and why, so you can review its output and hand-convert the rest.

---

## 1. At a glance — the syntax that changed

| Concern | Legacy cgpipe-jvm | cgp (Go) |
|---|---|---|
| Shebang | `#!/usr/bin/env cgpipe` | `#!/usr/bin/env cgp` |
| Command / binary | `cgpipe` | `cgp` |
| Config namespace | `cgpipe.*` | `cgp.*` |
| Job-log / ledger setting | `cgpipe.joblog` | `cgp.ledger` |
| Block delimiter | **indentation** (Python-style) | **braces** `{ }` (code) / `{{ }}` (shell body) |
| Control flow terminators | `if … endif`, `for … done` | `if { … }`, `for { … }` (no terminators) |
| Per-job settings in a body | `<% job.x = … %>` region | directive block above a `--` separator |
| Control flow inside a body | `<% if … %>` / `<% for … %>` region | `%`-prefixed control lines |
| Snippet import in a body | `<% import name %>` | `@name` |
| Build variables | `$<` `$>` `$%` `$<N` `$>N` | `${input}` `${output}` `${stem}` `${input[N]}` `${output[N]}` (0-based) |
| Special targets | `__pre__::` `__post__::` … | `@pre` `@post` `@setup` `@teardown` `@postsubmit` |
| Snippet definition | `name::` (indented body) | `snippet name {{ … }}` |
| Command substitution in code | bare `$(cmd)` | `"$(cmd)"` — inside a string literal |
| Cohort fan-out | `-manifest file.tsv` (CLI, one run per row) | `open("file.tsv").read_tsv()` + a `for` loop (one graph) |

The two conceptually larger changes — **braces instead of indentation** and
**sample-sheet readers instead of `-manifest`** — get their own sections (§3, §9).

---

## 2. Shebang, command, and settings namespace

The interpreter is now `cgp`, and the whole `cgpipe.*` settings namespace became
`cgp.*`. The old job-log setting was also renamed.

```diff
-#!/usr/bin/env cgpipe
+#!/usr/bin/env cgp

-cgpipe.runner = "slurm"
-cgpipe.joblog = "jobs.log"
+cgp.runner = "slurm"
+cgp.ledger = "jobs.ledger"
```

`cgp.ledger` is more than a rename: the ledger is now a **directory** of append-only
JSONL files (not a single log file), recording only *which job owns which output*.
See [The Ledger](11-The_Ledger.md).

Per-*job* settings still live under the **`job.*`** namespace (`job.mem`, `job.procs`,
`job.walltime`, `job.name`, …) — that did not change.

---

## 3. Blocks: indentation → braces (the biggest change)

Legacy cgpipe used **indentation** to delimit blocks, with `endif`/`done` terminators.
cgp uses **braces**, and draws a hard line between two kinds of block:

- **`{ … }`** — a block of **cgpipe code** (`if`, `for`).
- **`{{ … }}`** — a **shell body** (target recipes, snippets, special targets), read
  as raw text and rendered at job time. A shell body ends at a lone **`}}` on its own
  line**.

Control-flow keywords lose their terminators; the closing brace replaces `endif`/`done`:

```diff
-if threads > 8
-    print "many threads"
-elif threads > 0
-    print "some"
-else
-    print "none"
-endif
+if threads > 8 {
+    print "many threads"
+} elif threads > 0 {
+    print "some"
+} else {
+    print "none"
+}

-for c in chroms
-    print c
-done
+for c in chroms {
+    print c
+}
```

A target's recipe, previously an indented block, becomes an explicit `{{ … }}` body:

```diff
-sorted.bam: input.bam
-    samtools sort -o $> $<
+sorted.bam: input.bam {{
+    samtools sort -o ${output} ${input}
+}}
```

> **While loops:** legacy `while cond … done` becomes cgp's `for cond { … }` (the
> `for` with a bare condition is the while-style form).

---

## 4. Build variables: `$<` `$>` `$%` → `${…}`

The make-style build variables are replaced by named substitutions, and **indexing is
now 0-based** (legacy `$<1` was the first input; cgp `${input[0]}` is the first input).

| Legacy | cgp | Meaning |
|---|---|---|
| `$<` | `${input}` | all inputs (space-joined) |
| `$>` | `${output}` | all outputs (space-joined) |
| `$%` | `${stem}` | wildcard stem |
| `$<1`, `$<2`, … | `${input[0]}`, `${input[1]}`, … | the N-th input (**1-based → 0-based**) |
| `$>1`, `$>2`, … | `${output[0]}`, `${output[1]}`, … | the N-th output |

```diff
-aligned.bam: reads.fq ref.fa
-    bwa mem $<2 $<1 > $>
+aligned.bam: reads.fq ref.fa {{
+    bwa mem ${input[1]} ${input[0]} > ${output}
+}}
```

`cgp convert` does this rewrite automatically, including the 1-based → 0-based
decrement of indexed forms.

---

## 5. Per-job settings in a body: `<% %>` region → directive block + `--`

Legacy scripts set per-job resources with a `<% … %>` region inside the body. cgp
introduces a **directive block**: cgpipe code at the top of the body, separated from
the shell by a line containing only `--`.

```diff
 aligned.bam: reads.fq ref.fa
-    <%
-    job.mem   = "16G"
-    job.procs = threads
-    %>
-    bwa mem -t ${job.procs} $<2 $<1 > $>
+aligned.bam: reads.fq ref.fa {{
+    job.mem   = "16G"
+    job.procs = threads
+    --
+    bwa mem -t ${job.procs} ${input[1]} ${input[0]} > ${output}
+}}
```

Rules to remember:

- **`--` is what creates a directive block.** With no `--`, the *entire* body is shell,
  and a line like `job.mem = "16G"` is passed through to the shell verbatim (not
  interpreted). This is a common post-conversion mistake — if resources seem ignored,
  check that the `--` separator is present.
- Settings must appear **before** `--`. `cgp convert` moves a leading all-assignment
  region into the directive block; a settings assignment that appeared *after* the
  shell already started is flagged with a warning for you to relocate.
- Read a setting back in the shell as `${job.procs}`; a bare `${procs}` is an ordinary
  user variable.

---

## 6. Control flow inside a body: `<% %>` region → `%`-lines

When control flow must wrap *shell* lines (emit shell per loop iteration), the legacy
`<% if %>` / `<% for %>` regions become **`%`-prefixed control lines**. The rule is
simple: a body line whose first non-whitespace character is `%` is cgpipe code;
everything else is shell. Brace syntax applies (no `endif`/`done`):

```diff
 : cleanup.done @{tmpfiles}
-    <% for o in tmpfiles %>
-    rm -f ${o}
-    <% done %>
+: cleanup.done @{tmpfiles} {{
+% for o in tmpfiles {
+    rm -f ${o}
+% }
+}}
```

An **inline** `<% … %>` mixed into the middle of a shell line (not on its own line)
has no direct equivalent — cgp's `${if cond; a; b}` inline conditional usually replaces
it. `cgp convert` cannot safely translate these and flags each one with a
`# cgpipe-convert: review inline <% %>` comment.

---

## 7. Special targets and snippets

Special targets moved from the `__name__::` convention to the `@`-sigil (which now
uniformly marks every reserved, never-a-file target):

| Legacy | cgp |
|---|---|
| `__pre__::` | `@pre {{ … }}` |
| `__post__::` | `@post {{ … }}` |
| `__setup__::` | `@setup {{ … }}` |
| `__teardown__::` | `@teardown {{ … }}` |
| `__postsubmit__::` | `@postsubmit {{ … }}` |

Snippets change on both ends — definition and use:

```diff
-common::
-    set -euo pipefail
-    umask 077
+snippet common {{
+    set -euo pipefail
+    umask 077
+}}

 out.txt: input.txt
-    <% import common %>
-    wc -l $< > $>
+out.txt: input.txt {{
+    @common
+    wc -l ${input} > ${output}
+}}
```

An unknown `__foo__::` special target is converted to `@foo` and flagged, since cgp
only recognizes the five reserved bodies above (plus `@default:` for the default goal).

---

## 8. Command substitution in code context

Legacy scripts used bare `$(cmd)` in assignments and conditions. In cgp, `$(cmd)` is a
**render-time substitution that lives inside a string literal**, so a bare `$(cmd)` in
cgpipe-code position must be wrapped in quotes:

```diff
-today = $(date +%F)
+today = "$(date +%F)"
```

`cgp convert` wraps bare `$(…)` in code contexts automatically (escaping any internal
quotes). Inside a **shell body**, `$(cmd)` stays bare — it's real shell command
substitution. Note the render-time timing: `$(cmd)` in a body runs when the script is
*rendered* (even under `-dr`); write `\$(cmd)` to defer it to the job's own shell at
run time.

---

## 9. Cohort fan-out: `-manifest` → sample-sheet readers

The legacy way to run a pipeline once per row of a sample sheet was the `-manifest`
CLI option, which invoked the pipeline repeatedly (one process per row) with the
columns bound as variables. **cgp removes `-manifest`.** Instead you read the sheet
*inside* the script and loop over its rows, so the whole cohort is a single dependency
graph — which means you can **scatter and gather in one pipeline** (impossible when
each row was a separate process):

```diff
-# legacy: cgp -manifest samples.tsv pipeline.cgp   (one run per row; ${sample} etc. bound per row)
-${sample}.sum: ${input}
-    wc -w < $< > $>
+# cgp: read the sheet, loop, and gather — all in one graph
+samples = open("samples.tsv").read_tsv(header=true)
+sums = []
+for row in samples {
+    name = row["sample"]
+    out  = name + ".sum"
+    sums += out
+    ${out}: ${row["input"]} {{
+        wc -w < ${input} > ${output}
+    }}
+}
+cohort.txt: @{sums} {{        # the gather — not possible with per-row -manifest
+    cat ${input} > ${output}
+}}
+@default: cohort.txt
```

`open(path)` returns a file handle; `.read_tsv(header=true)` / `.read_csv(...)` /
`.read_json()` / `.read_lines()` turn a sheet into data (a **list of maps**, one per
row) that a `for` loop scatters into targets. This is a genuine rewrite, not a
mechanical substitution — `cgp convert` does **not** attempt it. See
[Sample Sheets](13-Sample_Sheets.md) and the `map` type in the
[language spec](language-spec.md).

---

## 10. What `cgp convert` does *not* do (hand-convert these)

The converter is best-effort and line-oriented. Review its output for:

- **Inline `<% … %>`** on a shell line — flagged; usually becomes `${if cond; a; b}`.
- **Multi-variable / zip `for a, b in xs, ys`** — cgp's `for` takes a single variable
  (use `with i` for a counter, or restructure); flagged.
- **`-manifest` fan-out** — rewrite by hand with sample-sheet readers (§9).
- **Settings after the body started** — moved to a `%`-line and flagged; relocate them
  into the directive block before `--`.
- **Legacy statement keywords** that cgp doesn't have (e.g. `println`, `log`) — replace
  with `print`; `while` becomes `for cond`.
- **Make-vars in a control condition** (`$<`/`$>` inside `% if`/`% for`) — flagged;
  in code context use `input[N]`, `input.length()`, etc.

After converting, always **dry-run** the result before trusting it:

```sh
cgp -dr new.cgp [--vars …]        # render locally, no execution
cgp -dr -r slurm new.cgp          # preview the submission scripts
```

and diff the rendered output against what the old pipeline produced.

---

## 11. Model things that did *not* change

To reassure: the core is the same, so most of your pipeline logic ports directly.

- **Output-first rules** (`output: inputs {{ recipe }}`), first-defined-target or
  `@default` as the goal.
- **mtime-based staleness** (rebuild if missing or older than an input); `-force`
  rebuilds everything.
- **Wildcards** (`%.gz: %`), **temp outputs** (now spelled `^out`), **opportunistic**
  no-output cleanup targets, **bodyless aggregators**.
- **Scheduler submission** to SLURM/SGE/PBS (BatchQ is new), with `job.*` resources
  mapped to each scheduler's flags, and dependencies derived from the graph.
- **Config layering** across system / user / env / CLI / script.

Reference → [`cgpipe-for-llms.md`](cgpipe-for-llms.md) for the full current language on
one page, [`language-spec.md`](language-spec.md) for the normative spec, and
[The convert Tool](15-The_convert_Tool.md) for the migrator's details.
