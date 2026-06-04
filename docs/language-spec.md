# cgp v1 — language specification

> **Status:** Phase 0 draft. This is the reference the Phase 1 implementation is built against. It restates the cgpipe (JVM) language as it behaves today, with the deliberate refinements settled for the Go rewrite called out inline and collected under [Deliberate differences from JVM cgpipe](#deliberate-differences-from-jvm-cgpipe).
>
> `cgp` is the Go rewrite of cgpipe. The historical JVM implementation lives at `compgen-io/cgpipe-jvm`. Where this document says "cgpipe," it means the JVM tool's established behavior; "cgp" means the new tool specified here.

`cgp` is a small interpreted language whose purpose is to generate and submit job scripts. A pipeline file is read top to bottom in *global* context — every uncommented line is cgp code. Target definitions open a separate *body* context (raw shell with interpolation). Files use the `.cgp` extension.

---

## 1. Lexical structure

### 1.1 Source and encoding
A pipeline is a UTF-8 text file. Lines are significant: the language is line-oriented at the statement level (one statement per line; no statement separator), though brace blocks span lines.

### 1.2 Shebang
A leading `#!` line is ignored by the parser:

    #!/usr/bin/env cgp

### 1.3 Comments and help text
`#` begins a comment running to end of line.

The leading run of comment lines at the top of a script (after the shebang) is the script's **help text**, shown for `-h`:

    #!/usr/bin/env cgp
    #
    # Align reads and call variants.
    #
    # Options:
    #     --reads FILE   input FASTQ
    #     --ref FILE     reference FASTA
    #

The first blank or non-comment line ends the help block.

### 1.4 Tokens
Identifiers, numeric literals, string literals, operators, and the structural tokens `{`, `}`, `{{`, `}}`, `:`, `--`, `(`, `)`, `[`, `]`, `,`, `..`. Whitespace separates tokens and is otherwise insignificant except for (a) leading comment runs (help text) and (b) leading whitespace inside shell bodies, which is stripped on render.

### 1.5 The two block delimiters — the lexer's mode flip
This is the central lexical rule of cgp and the biggest change from cgpipe:

- **`{ ... }`** delimits a block of **cgp code** (`if`, `for`). The lexer is in token mode; braces are matched by counting.
- **`{{ ... }}`** delimits a **shell body** (target bodies, `snippet`, and the special targets). The lexer switches to **capture mode**: the body is read as raw text and is *not* tokenized as cgp. A shell body is terminated by a **lone `}}` on its own line** (after leading-whitespace strip).

The doubling is deliberate: `{{` is a reliable "raw shell ahead" cue for both the reader and the lexer. A lone `}}` line was chosen over a lone `}` because a bare `}` *does* appear on its own line in real shell (function definitions, brace groups), whereas a lone `}}` line does not. This makes termination unambiguous with no escape rule and no dependence on indentation.

See [§6 Target bodies](#6-target-bodies) for what happens inside `{{ }}`.

---

## 2. Data types

Six types: `bool`, `int`, `float`, `string`, `list`, `range`.

    flag    = true            # bool (case-sensitive: true / false)
    count   = 10              # int
    rate    = 0.5             # float
    name    = "sample-1"      # string (always double-quoted)
    samples = []              # list
    samples = [1, 2, "x"]     # lists may mix types
    chunks  = 1..100          # range (1, 2, …, 100 when iterated)

Typing is dynamic; a value's type is mostly invisible. `.type()` returns the type name as a string. CLI argument values arrive as strings and are parsed to `int`/`float`/`bool` when they look like one.

---

## 3. Variables

Set with `=`. No declarations; the only scopes are global and the per-target body closure ([§6.5](#65-scoping)).

| Form | Meaning |
|------|---------|
| `foo = expr`  | Set `foo` |
| `foo ?= expr` | Set `foo` only if not already set (defaults) |
| `foo += expr` | Append to `foo` (promotes a scalar to a list) |
| `unset foo`   | Remove `foo` from scope |

`?=` is the defaults workhorse and respects upstream overrides (CLI, env, config):

    threads ?= 4
    method  ?= "fast"

### 3.1 Command-line variables
`-name value` on the command line is equivalent to `name = "value"` set before the script runs:

    $ cgp pipeline.cgp -sample patient_42 -threads 16

Because CLI values are applied first, `?=` defaults do not override them.

---

## 4. Expressions

### 4.1 Operators
- **Arithmetic:** `+ - * / % **` (power). Standard precedence; parenthesize for clarity.
- `+` also concatenates strings; `*` repeats strings and lists (`"x" * 3` → `xxx`).
- **Comparison:** `== != < <= > >=`
- **Logic:** `&& || !`
- `!foo` doubles as "unset or false" — the idiom for argument guards.

### 4.2 Indexing and slicing
Lists and ranges are zero-indexed; negative indices count from the end; Python-style slices:

    foo = ["one","two","three"]
    foo[0]    # one
    foo[-1]   # three
    foo[1:]   # two three
    foo[:2]   # one two

### 4.3 String substitution
Inside a string literal:

| Form | Behavior |
|------|----------|
| `${var}`   | Substitute `var`; error if unset; a list joins with spaces |
| `${var?}`  | Like `${var}` but yields `""` when unset |
| `${expr}`  | Any expression, incl. method calls and indexing: `${input[0]}`, `${name.basename()}` |
| `${if c; a; b}` | Inline conditional — see [§6.3](#63-inline-conditionals) |
| `@{list}`  | List expansion — one copy per element ([§7.4](#74-list-expansion-list)) |
| `@{N..M}`  | Range expansion |
| `${{var}}` | Double evaluation — substitute, then evaluate the result as source |
| `$(cmd)`   | Run `cmd` in the shell at parse time; substitute its stdout |

Escaping: prefix `$` or `@` with `\` for a literal. If the string will be evaluated again, escape twice (`\\$`).

`${{var}}` (double-eval) is for when a variable's *content* is itself a template; `$(cmd)` runs at parse time and its command string is variable-substituted first.

---

## 5. Statements and control flow

### 5.1 Control flow uses brace blocks
`endif`/`done` are removed; `{ }` delimits the block.

    if count > 100 {
        print "many"
    } elif count > 0 {
        print "some"
    } else {
        print "none"
    }

    for i in 1..10      { print i }       # range
    for sample in samples { print sample } # list
    for cond            { ... }            # while-style: runs while cond is true

Loop variables remain set after the loop (no separate scope).

### 5.2 Statement keywords

| Statement | Purpose |
|-----------|---------|
| `print expr [, expr …]` | Write to stdout. **Inside a body**, appends to the rendered script instead. |
| `log filename`  | Open/replace the cgp log file (also `-l`). |
| `include "path"` | Inline another `.cgp` file (global context). Resolved relative to the current file, then the cwd. |
| `eval expr`     | Evaluate a string-valued expr as cgp source at run time. |
| `unset name`    | Remove a variable. |
| `exit [code]`   | Stop the pipeline (`exit` ⇒ `exit 0`). |
| `dumpvars`      | Print all in-scope variables (debug). |
| `showhelp`      | Print the help-text block (same as `-h`). |
| `sleep seconds` | Pause. Rarely needed. |

`include` runs in global context — the included file's statements and targets become part of the current pipeline. It's the primary composition mechanism for shared defaults and target libraries. (cgpipe's body-only `import` is replaced by `snippet`/`@name` — see [§6.6](#66-snippets).)

---

## 6. Target bodies

A target declares outputs, the inputs they depend on, and the shell that builds them:

    output1 [output2 …] : [input1 input2 …] {{
        … shell body …
    }}

Example:

    sorted.bam: input.bam {{
        samtools sort -o ${output} ${input}
    }}

When `sorted.bam` is requested, cgp checks whether it is missing or older than `input.bam` and, if so, submits the body to the configured runner. Multiple outputs and multiple inputs are allowed; requesting any one output runs the rule once and produces all outputs.

### 6.1 The body is a template
Inside `{{ }}` the content is **raw shell text**, captured verbatim and rendered at job-submission time. The parser does not parse the shell. Three things are recognized during the render pass:

1. **`${…}` substitution** of cgp values (and `${if …}`, `@{…}`).
2. **Directives** (an optional leading block — see [§6.2](#62-directives-and-the----separator)).
3. **`%`-prefixed cgp control lines** (see [§6.4](#64-in-body-control-flow--lines)).

Everything else is shell, passed through after leading-whitespace stripping. The body ends at a lone `}}` line.

### 6.2 Directives and the `--` separator
A target body may begin with a **directive block** that sets per-job settings, separated from the shell by a line containing only `--`:

    aligned.bam: reads.fq ref.fa {{
        mem      = "16G"
        procs    = threads
        walltime = "12:00:00"
        container = "biocontainers/bwa:0.7.17"
        --
        bwa mem -t ${procs} ${ref} ${reads} > ${output}
    }}

- Before `--`: **cgp code**. Bare `IDENT = expr` assignments set per-job settings (the `job.` prefix is dropped — `mem` means the old `job.mem`). Ordinary cgp control flow is allowed here (it's cgp mode, no `%` prefix needed).
- After `--`: the **shell template**.
- `--` is **optional**: with no directives, the entire body is shell:

      copy.txt: input.txt {{
          cp ${input} ${output}
      }}

### 6.3 Inline conditionals
`${if cond; true_value; false_value}` substitutes one fragment or the other; the else-clause may be omitted (`${if cond; true_value}` ⇒ empty when false). This replaces cgpipe's inline `<% if %>…<% endif %>`:

    bwa mem -t ${procs} ${if rg; "-R " + rg} ${ref} ${reads} > ${output}

### 6.4 In-body control flow (`%` lines)
For control flow that must wrap *shell* lines (loops/conditionals that emit shell), a line whose first non-whitespace character is `%` is a **cgp code line**. The rule is simply: **`%` at line start ⇒ cgp; otherwise ⇒ shell.** `%`-lines use the same brace syntax as anywhere else — there is no `done`/`endif`:

    : ${out} @{tmpfiles} {{
        if [ -e ${out} ]; then
    % for o in tmpfiles {
            if [ -e ${o} ]; then
    % }
                rm -v ${tmpfiles}
    % for o in tmpfiles {
            fi
    % }
        fi
    }}

The non-`%` lines between `% for … {` and `% }` are shell, emitted once per loop iteration with `${…}` resolved each time. This replaces cgpipe's `<% for %>…<% done %>`. (`%` as a control-line marker is distinct from `%` wildcards, which appear only in target declaration lines — see [§7.3](#73-wildcards).)

### 6.5 Scoping
A target captures the surrounding global context at definition time, like a closure. The body may *read* any variable in scope at definition; assignments inside the directive block are target-local and do not leak to the global scope. The loop variable in a body-defining `for` is captured per target.

### 6.6 Snippets
Shared body fragments are defined with `snippet name {{ }}` and invoked with `@name` inside a body (replacing cgpipe's `name::` definition and `<% import name %>`):

    snippet common {{
        set -euo pipefail
        umask 077
    }}

    out.txt: input.txt {{
        @common
        wc -l ${input} > ${output}
    }}

---

## 7. Target declaration features

### 7.1 Make-style substitutions, modernized
cgpipe's terse `$<`/`$>`/`$%` are replaced by named forms (old forms accepted as deprecated aliases during migration):

| cgpipe | cgp | Meaning |
|--------|-----|---------|
| `$<`  | `${input}`     | All inputs, space-joined |
| `$>`  | `${output}`    | All outputs, space-joined |
| `$%`  | `${stem}`      | Wildcard stem |
| `$<1` | `${input[0]}`  | First input (0-based index) |
| `$>1` | `${output[0]}` | First output |

There is no singular/plural distinction: `${input}` is always "the inputs as a value" (joins with spaces when substituted into a string); index with `[N]` for one element. `${input.length()}`, `${input.join(",")}`, and shell `for f in ${input}` all follow naturally.

### 7.2 Multiple definitions for one output
The same output may be defined more than once with different inputs; cgp tries each in source order and uses the first whose inputs are all satisfiable. If none can be satisfied, it's a "no build path" error.

### 7.3 Wildcards
`%` matches one or more characters in an output name; the stem is reused on the input side and is available as `${stem}`:

    %.gz: % {{
        gzip -c ${input} > ${output}
    }}

`%` is valid only in the declaration line.

### 7.4 List expansion `@{list}`
`@{var}` expands a list into multiple items at parse time, in three places:

- Declaration lines: `@{outs}: @{ins}` lists each output and input separately.
- String literals: `"prefix_@{list}_suffix"` → one string per element.
- Range form: `@{1..N}`.

Contrast `${var}` (single value, lists space-joined) — used inside bodies where you want one argument listing everything.

### 7.5 Temporary outputs (`^`)
Prefix an output with `^` to mark it **temporary** — an intermediate that only exists to satisfy downstream rules:

    ^calls.${chrom}.vcf: aligned.bam ref.fa {{ … }}

A temp output is treated specially **only in how its absence is handled**. When it is present, it is mtime-checked exactly like a normal output.

1. **Absence does not force a rebuild.** A missing temp does not, by itself, trigger its own job. If everything downstream is current, the temp job is skipped even though the file is gone.
2. **When present, it is mtime-checked like any file.** If the temp exists and is **newer than a downstream output**, that downstream rebuilds. If it exists and is older than its own inputs, it rebuilds.
3. **When absent, staleness looks *through* it.** A downstream target is stale iff it is missing or older than the temp's *effective input timestamps* — i.e. cgp propagates the comparison up the chain to the temp's inputs (recursively). So an updated ultimate source re-triggers the whole chain even after the intermediate was deleted.
4. **Tracked separately** (shown as `TEMP`, recorded `is_temp` in the ledger).

Put simply: a **missing** temp is *transparent* (it passes staleness through from its inputs); a **present** temp is a *normal file*.

Worked through, for `A → B → C` with `^B`:

| On disk | Change | Decision |
|---------|--------|----------|
| A, C (B deleted) | A updated, newer than C | look through missing B to A ⇒ C stale ⇒ rebuild B then C |
| A, B, C | B newer than C | B present ⇒ C stale ⇒ rebuild C (possible only because B exists to stat) |
| A, C (B deleted) | A older than C | look through missing B to A ⇒ C current ⇒ skip all |

`^` is a marker only; it's stripped before the filename reaches the shell. **cgp never auto-deletes temp files** — deletion is always explicit and user-written ([§7.6](#76-opportunistic-jobs)). "Temp" describes why a file was made, not permission to remove it.

### 7.6 Opportunistic jobs
A target with **no outputs** — a leading `:` and a list of inputs — is *opportunistic*. It runs after the rest of the pipeline is submitted, never forces its inputs to be built, and runs only if all inputs are already available (on disk, submitted earlier this run, or recorded in the ledger). If any input is missing and nothing will produce it, it's silently skipped. The canonical use is guarded cleanup of temp files (see the [§6.4](#64-in-body-control-flow--lines) example).

---

## 8. Special targets (keywords)

cgpipe's `__pre__`/`__post__`/etc. become keywords with `{{ }}` bodies:

| Keyword | When it runs |
|---------|--------------|
| `pre {{ }}`        | Prepended to every other target's body (unless `nopre`) |
| `post {{ }}`       | Appended to every other target's body (unless `nopost`) |
| `setup {{ }}`      | Once, as the first job in the pipeline |
| `teardown {{ }}`   | Once, as the last job |
| `postsubmit {{ }}` | Once per submitted job, synchronously, on the submit host, right after submission |

    pre {{
        echo "Inputs:  ${input}"
        echo "Start:   $(date)"
    }}

    setup {{
        shexec = true
        --
        mkdir -p output logs
    }}

`shexec = true` runs the body directly on the submission host instead of submitting it (the usual choice for `mkdir`-style setup); only `setup`/`teardown` may be shexec, and `postsubmit` always is. Per-target opt-out of `pre`/`post` via `nopre = true` / `nopost = true` directives.

---

## 9. Methods on built-in types

Dot syntax, on variables, literals, or chained results. Argument counts are checked at runtime.

### 9.1 Any type
`type()` → the type name (`"string"`, `"int"`, …).

### 9.2 string

| Method | Args | Returns | Description |
|--------|------|---------|-------------|
| `split(delim)` | string, optional | list | Split on `delim`; omitted ⇒ individual characters |
| `sub(pattern, repl)` | string, string | string | Regex replace-all (Go `regexp` syntax in cgp) |
| `upper()` / `lower()` | — | string | Case |
| `length()` | — | int | Character count |
| `contains(s)` | string | bool | Substring test |
| `join(list)` | list | string | Receiver is the separator |
| `basename()` | — | string | `/a/b/c.bam` → `c.bam` |
| `dirname()` | — | string | `/a/b/c.bam` → `/a/b` |
| `abspath()` | — | string | Resolved absolute path |
| `exists()` / `isfile()` / `isdir()` | — | bool | Filesystem test at evaluation time |

> **Refinement:** `sub` uses Go's `regexp` (RE2) syntax rather than Java regex. To strip a literal `.bam`: `name.sub("\\.bam$","")`. The migration tool flags regex patterns that differ between the two engines.

### 9.3 list

| Method | Args | Returns |
|--------|------|---------|
| `length()` | — | int |
| `contains(value)` | any | bool |
| `join(separator)` | string | string |

Also indexed, sliced, and appended with `+=`. `",".join(list)` (receiver-flipped) is equivalent to `list.join(",")`.

### 9.4 range
`length()` → number of values. Ranges iterate, index, and pass anywhere a list is accepted.

### 9.5 int / float / bool
Only `type()`. No implicit coercion; an unknown method throws `Method not found`.

---

## 10. The ledger (job tracking)

> The ledger is cgpipe's "joblog," renamed and re-grounded. It is **optional** — a pipeline runs correctly without one.

### 10.1 Purpose and scope
The ledger is a record of **which job owns (last produced) which output file**, plus that job's inputs and dependencies. Its core query is "who owns output path `X`?" It enables cross-run composition: cgp won't resubmit a job whose output is already pending in the scheduler, even across separate invocations, and it wires new downstream work as a scheduler dependency (`afterok:<id>`) of the in-flight job.

Three responsibilities are kept strictly separate:

- **Filesystem (`stat`)** decides staleness ("is this output current relative to its inputs?").
- **Ledger** records identity/ownership and dependency edges.
- **Scheduler** owns live job state (queued/running/done). cgp asks `squeue`/`qstat`; the ledger stores **no** job state.

The ledger therefore stores **no file metadata (no mtimes)** and **no job state**. Enabled via `cgp.ledger` (a path).

### 10.2 Storage
SQLite (`modernc.org/sqlite`, pure Go), single-writer per ledger. Schema (v1):

    CREATE TABLE jobs (
        job_id      TEXT PRIMARY KEY,   -- scheduler id (or shell-runner synthetic id)
        run_id      TEXT,               -- optional, e.g. "align-20260604"
        name        TEXT,               -- job/target name
        pipeline    TEXT,               -- the pipeline filename run, e.g. "align.cgp"
        working_dir TEXT,
        user        TEXT,
        submit_time INTEGER,
        start_time  INTEGER,            -- reserved for an external updater; core never writes/reads
        end_time    INTEGER,            --   "
        return_code INTEGER             --   "
    );
    CREATE INDEX jobs_by_run ON jobs(run_id);

    CREATE TABLE output_owner (         -- authoritative "who owns this path now"
        path   TEXT PRIMARY KEY,
        job_id TEXT NOT NULL REFERENCES jobs(job_id)
    );

    CREATE TABLE job_outputs (          -- full history; provenance + vacuum source
        job_id  TEXT NOT NULL REFERENCES jobs(job_id),
        path    TEXT NOT NULL,
        is_temp INTEGER NOT NULL DEFAULT 0,
        PRIMARY KEY (job_id, path)
    );
    CREATE INDEX job_outputs_by_path ON job_outputs(path);

    CREATE TABLE job_inputs   ( job_id TEXT NOT NULL REFERENCES jobs(job_id), path   TEXT NOT NULL, PRIMARY KEY (job_id, path) );
    CREATE TABLE job_deps     ( job_id TEXT NOT NULL REFERENCES jobs(job_id), dep_id TEXT NOT NULL, PRIMARY KEY (job_id, dep_id) );
    CREATE TABLE job_settings ( job_id TEXT NOT NULL REFERENCES jobs(job_id), key    TEXT NOT NULL, value TEXT );
    CREATE TABLE job_src      ( job_id TEXT NOT NULL REFERENCES jobs(job_id), lineno INTEGER NOT NULL, line TEXT, PRIMARY KEY (job_id, lineno) );

### 10.3 Ownership and vacuum
- **Lookup:** `SELECT job_id FROM output_owner WHERE path = ?`.
- **Claim (last job wins):** on submit, each output runs `INSERT INTO output_owner(path, job_id) VALUES(?,?) ON CONFLICT(path) DO UPDATE SET job_id = excluded.job_id`. The overwrite encodes recency — no ordering column needed. This covers both "previous owner failed" and "previous owner succeeded but an input changed, so a new job re-produces the output."
- **Vacuum** (`cgp ledger vacuum`): keep every job referenced by `output_owner`, drop the rest (cascade to child tables), in one transaction. The last owner of each path survives even if it failed.

### 10.4 Restart
Restart is **mtime-based**, make-style: an output is rebuilt if it is missing or older than any input. A `--force` flag rebuilds regardless. There are no "restart modes." The performance win at scale is a **run-scoped stat cache**: within one invocation each path is `stat`-ed once and reused (e.g. a shared `ref.fa` across many manifest-fan-out runs is stat-ed once, not per run).

---

## 11. Configuration

### 11.1 Namespace and locations
The configuration namespace is `cgp.*` (was `cgpipe.*`). User-scoped state lives under a single root, `~/.cgp/`:

| Path | Purpose |
|------|---------|
| `~/.cgp/config`    | User config (itself a cgp script) |
| `~/.cgp/templates/`| Custom runner templates |
| `~/.cgp/cache/`    | Cache / state |
| `/etc/cgp/config`  | System (site-wide) config |

### 11.2 Resolution order (later wins)
1. Built-in defaults
2. System config (`/etc/cgp/config`)
3. User config (`~/.cgp/config`)
4. Environment (`CGP_ENV` evaluated as cgp; `CGP_RUN_ID`, `CGP_DRYRUN`)
5. Command-line `-name value` / `-t name=value`
6. The pipeline script (`=` always wins; `?=` respects upstream)

### 11.3 Selected `cgp.*` settings

| Variable | Purpose |
|----------|---------|
| `cgp.log` | cgp log file path (`-l`) |
| `cgp.loglevel` | Verbosity (`-v`/`-vv`/`-vvv`) |
| `cgp.ledger` | Ledger path; enables cross-run job tracking |
| `cgp.run_id` | Run identifier (also `CGP_RUN_ID`) |
| `cgp.runner` | `shell`, `slurm`, `sge`, `pbs`, `batchq`, `graphviz` |
| `cgp.runner.<name>.<setting>` | Runner-specific |
| `cgp.shell` | Default shell for rendered bodies |
| `cgp.dryrun` | Set by `-dr` / `CGP_DRYRUN` |
| `cgp.container.engine` | `docker`, `singularity`/`apptainer`; unset disables container wrapping |
| `cgp.container.*` | Bind mounts, env passthrough, engine opts, etc. |

> **Refinements vs cgpipe config defaults:** `global_hold` (hold all jobs until the pipeline submits cleanly) and host-env capture (cgpipe's `job.env`) are **not** defaults in cgp — they are opt-in via `~/.cgp/config`. This keeps the core small; belt-and-suspenders behavior is a choice.

### 11.4 Per-job directives (the `job.*` surface, prefix dropped in bodies)
Set globally as `job.<name>` for defaults, or as a bare `<name>` directive inside a target body's directive block. Common: `name`, `procs`, `mem`, `walltime`, `stdout`, `stderr`, `container`, `gpu`, plus the assembly flags `shexec`, `nopre`, `nopost`. (Full runner/job table belongs in a future Running Jobs chapter.)

---

## 12. Worked example (cgp v1)

    #!/usr/bin/env cgp
    #
    # Per-chromosome variant calling with a merge step.

    runid ?= "run.$(date +%Y%m%d-%H%M)"
    log = "logs/call-${runid}.log"

    if !bam { print "ERROR: --bam required"; exit 1 }
    if !ref { print "ERROR: --ref required"; exit 1 }

    chroms   = "1 2 3 4 5 X Y".split(" ")
    per_chrom = []

    for c in chroms {
        per_chrom += "${out}.${c}.vcf"

        ^${out}.${c}.vcf: ${bam} ${ref} {{
            name = "call-chr${c}"
            mem  = "8G"
            --
            bcftools mpileup -r chr${c} -f ${ref} ${bam} ${extra_flags?} \
                | bcftools call -mv - > ${output}.tmp && mv ${output}.tmp ${output}
        }}
    }

    ${out}: @{per_chrom} {{
        name = "merge-${out.basename()}"
        mem  = "4G"
        --
        bcftools concat -O z -o ${output}.tmp ${input} && mv ${output}.tmp ${output}
    }}

    # Explicit, guarded cleanup of the per-chromosome temp files.
    : ${out} @{per_chrom} {{
        if [ -e ${out} ]; then
    % for v in per_chrom {
            if [ -e ${v} ]; then
    % }
                rm -v ${per_chrom}
    % for v in per_chrom {
            fi
    % }
        fi
    }}

---

## Deliberate differences from JVM cgpipe

A consolidated list of every intentional change this spec makes. The migration tool (`cgpipe-to-cgp`, Phase 3) automates the syntactic ones.

| Area | cgpipe (JVM) | cgp (this spec) |
|------|--------------|-----------------|
| Code blocks | indentation-delimited bodies; `if … endif`, `for … done` | `{ }` for cgp code (no `endif`/`done`); `{{ }}` for shell bodies, terminated by a lone `}}` line |
| Per-job settings | `<% job.x = … %>` block in the body | directive block before `--`, `job.` prefix dropped |
| Inline shell conditional | `<% if c %>frag<% endif %>` | `${if c; frag}` |
| In-body control flow | `<% for … %> … <% done %>` | `%`-prefixed cgp lines (`% for … {` / `% }`) |
| Make substitutions | `$<` `$>` `$%` `$<1` `$>1` | `${input}` `${output}` `${stem}` `${input[0]}` `${output[0]}` (old forms = deprecated aliases) |
| Special targets | `__pre__`, `__post__`, `__setup__`, `__teardown__`, `__postsubmit__` | `pre`, `post`, `setup`, `teardown`, `postsubmit` keywords |
| Snippets | `name::` def + `<% import name %>` | `snippet name {{ }}` + `@name` |
| Job tracking | flat-file joblog; recursive staleness walk | SQLite **ledger** (optional; ownership only, no state, no mtimes); mtime restart + run-scoped stat cache |
| Restart | recursive graph walk per invocation | mtime-based + `--force`; no restart "modes" |
| Temp cleanup | hand-rolled (`^` is a marker; no auto-delete) | unchanged — explicit, never automatic |
| Config namespace | `cgpipe.*`, `~/.cgpiperc` | `cgp.*`, `~/.cgp/` (config, templates, cache) |
| `global_hold` / env capture | on/available | opt-in via `~/.cgp/config`, not defaults |
| regex (`sub`) | Java regex | Go `regexp` (RE2) |
| Binary / files | `cgpipe`, `.cgp`/`.cgpipe`/`.mvp` | `cgp` (+ `cgsub` for one-off submits), `.cgp` |

### Deferred / open
- The directive/shell disambiguation **when `--` is omitted** (whole-body-is-shell vs. a space-around-`=` heuristic) is deferred until the lexer exists and can be tested against real scripts. Canonical mechanism is the explicit `--`.
- `${{var}}` double-evaluation and `$(cmd)` parse-time substitution are carried over as-is; their interaction with deferred body rendering will be pinned down during implementation.
