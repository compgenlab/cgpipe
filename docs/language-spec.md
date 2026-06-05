# cgp — language specification

`cgp` is a small interpreted language for generating and running job scripts. A pipeline file is read top to bottom in *global* context — every uncommented line is cgp code. Target definitions open a separate *body* context (raw shell with interpolation). Files use the `.cgp` extension.

---

## 1. Lexical structure

### 1.1 Source and encoding
A pipeline is a UTF-8 text file. Lines are significant: the language is line-oriented at the statement level (one statement per line; no statement separator), though brace blocks span lines. A newline **inside `( )` or `[ ]` is insignificant** (implicit line continuation), so a single expression — a call's arguments, a list literal — may be broken across lines; `{ }` is not continued (statements inside a block stay newline-separated).

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

### 1.5 The two block delimiters
This is the central lexical rule of cgp:

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
A double-hyphen `--name value` on the command line sets the script variable `name` before the script runs. (Single-hyphen arguments like `-dr` are cgp's own options; double-hyphen arguments are always script variables.)

    $ cgp pipeline.cgp --sample patient_42 --threads 16

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
`{ }` delimits the block.

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
| `include "path"` | Inline another `.cgp` file (global context). Resolved relative to the current file, then the cwd. |
| `export name = expr` | Expose a value to a calling workflow as `${stage.name}` ([§13](#13-workflows-stage-and-export)). A no-op when the pipeline runs standalone. |
| `eval expr`     | Evaluate a string-valued expr as cgp source at run time. |
| `unset name`    | Remove a variable. |
| `exit [code]`   | Stop the pipeline (`exit` ⇒ `exit 0`). |
| `dumpvars`      | Print all in-scope variables (debug). |
| `showhelp`      | Print the help-text block (same as `-h`). |
| `sleep seconds` | Pause. Rarely needed. |

`include` runs in global context — the included file's statements and targets become part of the current pipeline. It's the primary composition mechanism for shared defaults and target libraries. (For sharing *body* fragments, use `snippet`/`@name` — see [§6.6](#66-snippets).)

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
`${if cond; true_value; false_value}` substitutes one fragment or the other; the else-clause may be omitted (`${if cond; true_value}` ⇒ empty when false):

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

The non-`%` lines between `% for … {` and `% }` are shell, emitted once per loop iteration with `${…}` resolved each time. (`%` as a control-line marker is distinct from `%` wildcards, which appear only in target declaration lines — see [§7.3](#73-wildcards).)

A `%` statement whose expression has an open `(` or `[` continues onto the following `%` lines until the brackets balance (per [§1.1](#11-source-and-encoding)), so a list or call on `%` lines may span lines; consecutive `% for`/`% if` headers each open their own block.

### 6.5 Scoping
A target captures the surrounding global context at definition time, like a closure. The body may *read* any variable in scope at definition; assignments inside the directive block are target-local and do not leak to the global scope. The loop variable in a body-defining `for` is captured per target.

### 6.6 Snippets
Shared body fragments are defined with `snippet name {{ }}` and invoked with `@name` inside a body:

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

### 7.1 Build-variable substitutions
Inside a body, these stand for the target's inputs and outputs:

| Form | Meaning |
|------|---------|
| `${input}`     | All inputs, space-joined |
| `${output}`    | All outputs, space-joined |
| `${stem}`      | Wildcard stem |
| `${input[0]}`  | First input (0-based index) |
| `${output[0]}` | First output |

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

### 7.7 Bodyless (aggregator) targets
A target may omit the `{{ }}` body entirely. It then has no recipe and is a pure **aggregation rule** — it declares that its output depends on its inputs but contributes nothing to build it. The output name is virtual (never stat-ed, never expected on disk), making it a Make-style phony grouping target:

    all: final.vcf report.html qc.html

Requesting `all` builds the three goals. (An empty `{{ }}` is also grammatically valid — a no-op recipe — but bodyless is the clean form for grouping.) `@default` ([§8.1](#81-the-default-goal-default)) is the special, build-by-default form of this idea.

---

## 8. Reserved targets (`@`-prefixed)

cgp's built-in/virtual targets all share one sigil. **The rule: a target name beginning with `@` is a reserved cgp target and never names a file on disk.** This is what lets reserved names coexist with real filenames — a pipeline can still produce a file literally called `pre` or `default`; only `@pre` / `@default` are reserved. (`@` here is in *target-header* position; it is distinct from `@{…}` list expansion and from `@name` snippet invocation inside a `{{ }}` body — see [§6.6](#66-snippets).)

The reserved targets:

| Target | When it runs |
|--------|--------------|
| `@pre {{ }}`        | Prepended to every other target's body (unless `nopre`) |
| `@post {{ }}`       | Appended to every other target's body (unless `nopost`) |
| `@setup {{ }}`      | Once, as the first job in the pipeline |
| `@teardown {{ }}`   | Once, as the last job |
| `@postsubmit {{ }}` | Once per submitted job, synchronously, on the submit host, right after submission |

    @pre {{
        echo "Inputs:  ${input}"
        echo "Start:   $(date)"
    }}

    @setup {{
        shexec = true
        --
        mkdir -p output logs
    }}

`shexec = true` runs the body directly on the submission host instead of submitting it (the usual choice for `mkdir`-style setup); only `@setup`/`@teardown` may be shexec, and `@postsubmit` always is. Per-target opt-out of `@pre`/`@post` via `nopre = true` / `nopost = true` directives.

`@postsubmit` runs once for **each** submitted job, on the submit host, immediately after that job is submitted. Its body sees the just-submitted job's `${input}` / `${output}` / `${stem}`, plus **`${jobid}`** — the scheduler-assigned job id (empty under the shell runner, which has no ids). A typical use is recording submissions:

    @postsubmit {{
        echo "${output}	${jobid}" >> submissions.tsv
    }}

### 8.1 The default goal (`@default`)
`@default` declares what cgp builds when invoked with no explicit target. It is a reserved target whose **inputs are the goals**; it has **no body** (and therefore no `{{ }}`):

    @default: final.vcf report.html

- **No phony file.** Because `@default` can never be a filename, nothing is stat-ed or expected on disk.
- **Forces its goals to build**, exactly as if they were requested on the command line (unlike an opportunistic `: inputs` job, which never forces its inputs).
- **Fallback:** if no `@default` is declared, cgp builds the **first defined target**, so trivial pipelines need nothing.
- **CLI overrides:** `cgp p.cgp` builds the `@default` goals; `cgp p.cgp final.vcf` builds the named target(s) instead.
- **Accumulates:** multiple `@default:` lines (across the file, `include`s, or dynamic generation) add to the goal set, so `@default: @{all_outputs}` after a loop works.

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

`sub` uses Go's `regexp` (RE2) syntax. To strip a literal `.bam`: `name.sub("\\.bam$","")`.

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

> The ledger is **optional** — a pipeline runs correctly without one.

### 10.1 Purpose and scope
The ledger is a record of **which job owns (last produced) which output file**, plus that job's inputs, dependencies, settings, and rendered job script (for audit and `cgp ledger search`/`dump` — see [§15.2](#152-cgp-ledger)). Its core query is "who owns output path `X`?" It enables cross-run composition: cgp won't resubmit a job whose output is already pending in the scheduler, even across separate invocations, and it wires new downstream work as a scheduler dependency (`afterok:<id>`) of the in-flight job.

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
Restart is **mtime-based**, make-style: an output is rebuilt if it is missing or older than any input. The `-force` option rebuilds every target in the goal graph regardless. There are no "restart modes." The performance win at scale is a **run-scoped stat cache**: within one invocation each path is `stat`-ed once and reused (e.g. a shared `ref.fa` across many manifest-fan-out runs is stat-ed once, not per run).

### 10.5 Cross-run and cross-stage reuse
When a ledger is configured and a scheduler runner is in use, an input that has **no in-run producer** and **isn't on disk yet** is looked up in the ledger: if its owning job is still active (per `squeue`/`qstat`), the new work is wired as a scheduler dependency (`afterok:<id>`) of that in-flight job instead of being treated as a "no rule to make" error or duplicated. This is what makes re-running a pipeline before it has finished safe, and it is also how a later workflow [stage](#13-workflows-stage-and-export) waits on a file an earlier stage's jobs are still queued to produce. With the shell runner each job has already completed (the file exists), so the lookup is unnecessary.

### 10.6 Lockfile
The ledger SQLite database is opened with `nolock=1` and guarded by a separate NFS-safe lockfile (`O_CREAT|O_EXCL`) so it is safe on network filesystems without a client/server process. A stale lock left by a dead process on the same host is stolen automatically; otherwise cgp waits briefly and then errors. `cgp ledger unlock <db>` removes a lock by hand.

---

## 11. Configuration

### 11.1 Namespace and locations
The configuration namespace is `cgp.*`. User-scoped state lives under a single root, `~/.cgp/`:

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
5. Command-line `--name value`
6. The pipeline script (`=` always wins; `?=` respects upstream)

### 11.3 Selected `cgp.*` settings

| Variable | Purpose |
|----------|---------|
| `cgp.ledger` | Ledger path; enables cross-run job tracking |
| `cgp.run_id` | Run identifier (also `CGP_RUN_ID`) |
| `cgp.runner` | `shell`, `slurm`, `sge`, `pbs`, `batchq`, `graphviz`, `html` |
| `cgp.runner.<name>.<setting>` | Runner-specific |
| `cgp.shell` | Default shell for rendered bodies |
| `cgp.dryrun` | Set by `-dr` / `CGP_DRYRUN` |
| `cgp.container.engine` | `docker`, `singularity`/`apptainer`; unset disables container wrapping |
| `cgp.container.*` | Bind mounts, env passthrough, engine opts, etc. |

`global_hold` (hold all jobs until the pipeline submits cleanly) and host-environment capture are **not** defaults — enable them in `~/.cgp/config` if you want them. This keeps the core small; belt-and-suspenders behavior is opt-in.

### 11.4 Per-job directives (the `job.*` surface, prefix dropped in bodies)
Set globally as `job.<name>` for defaults, or as a bare `<name>` directive inside a target body's directive block. Common: `name`, `procs`, `mem`, `walltime`, `stdout`, `stderr`, `container`, `gpu`, plus the assembly flags `shexec`, `nopre`, `nopost`. (Full runner/job table belongs in a future Running Jobs chapter.)

---

## 12. Containers and GPUs

A target's body can be wrapped to run inside a container without changing the body itself. Wrapping is enabled when **both** a container engine and a per-target image are set:

- `cgp.container.engine` — `docker`, `singularity`, or `apptainer` (set in config or the script). Unset disables all wrapping.
- `container = "<image>"` — a per-target directive naming the image. A target with no `container` runs unwrapped even when an engine is configured.

      aligned.bam: reads.fq ref.fa {{
          container = "biocontainers/bwa:0.7.17"
          mem       = "16G"
          --
          bwa mem ${ref} ${reads} > ${output}
      }}

When wrapping is active, cgp writes the rendered body to a temp file and executes it inside the image, bind-mounting the input and output paths automatically, setting the working directory, and (for Docker) mapping the host user. Additional settings, available globally as `cgp.container.<name>` and/or per target as `container.<name>`:

| Setting | Purpose |
|---------|---------|
| `container.bind` / `cgp.container.bind` | Extra bind mounts (repeatable / list) |
| `container.env` / `cgp.container.env` | Environment variables to pass through |
| `container.opts` (or `cgp.container.docker_opts` / `cgp.container.singularity_opts`) | Raw extra flags for the engine |
| `container.body_dir` / `cgp.container.body_dir` | Where the temp body file is written/mounted (default `/tmp`) |
| `container.shell` / `cgp.container.shell` | Shell used to run the body inside the image (default `sh`) |
| `cgp.container.user_map` | Docker only: add `-u $(id -u):$(id -g)` (default on) |

### 12.1 GPUs
`gpu` requests GPUs for a target and drives both layers at once:

    train.model: data.tfrecord {{
        gpu = 2
        --
        train.py --data ${input} --out ${output}
    }}

- `gpu = true` ⇒ one GPU; `gpu = N` ⇒ N GPUs; `gpu = false`/unset ⇒ none. A global default is `cgp.gpu`.
- On a scheduler it emits the resource request (e.g. SLURM `--gres=gpu:N`).
- In a container it adds the engine's GPU flag (Docker `--gpus`, Singularity/Apptainer `--nv`).

---

## 13. Workflows: `stage` and `export`

A **workflow** chains several standalone pipelines into one command, threading values between them. A `.cgp` file is a workflow if it contains `stage` statements; otherwise it is a pipeline. Each stage is itself an ordinary, independently runnable pipeline.

    # wgs.cgp — a workflow
    if !fastqs { print "ERROR: --fastqs required"; exit 1 }
    if !ref    { print "ERROR: --ref required";    exit 1 }

    stage align  align.cgp  --fastq ${fastqs} --ref ${ref}
    stage post   post.cgp   --bam ${align.bam}
    stage call   call.cgp   --bam ${post.cleaned_bam} --ref ${ref}

### 13.1 Declaring stages
`stage NAME FILE ARGS...` declares one stage. `ARGS` use the same `--name value` (or `--name=value`) convention as command-line variables ([§3.1](#31-command-line-variables)) — they are the variables the stage pipeline receives. `NAME`, `FILE`, and each arg are interpolated against the workflow's variables before the stage runs.

Stages run in **declaration order**. Each stage's args may reference earlier stages' exports.

### 13.2 Exposing values with `export`
A stage pipeline exposes values to the workflow with a top-level `export name = expr` ([§5.2](#52-statement-keywords)):

    # align.cgp — also runnable on its own
    aligned.bam: ${fastq} ${ref} {{
        bwa mem ${ref} ${fastq} > ${output}
    }}
    @default: aligned.bam
    export bam = "aligned.bam"

When `align.cgp` runs standalone, `export` does nothing. When it runs as the `align` stage, its exported `bam` becomes `${align.bam}` in the workflow, available to later stages. `export` is therefore non-invasive: adding the lines does not change standalone behavior.

### 13.3 Cross-stage dependencies
With the shell runner each stage completes before the next begins, so a later stage simply reads the earlier stage's files. With a scheduler runner an earlier stage's jobs may still be queued when a later stage submits; the cross-stage `afterok` wiring is resolved through the ledger ([§10.5](#105-cross-run-and-cross-stage-reuse)), so a scheduler workflow wants `cgp.ledger` configured.

### 13.4 Export validation
References to stage exports are checked two ways:

- **Statically (best-effort):** at startup cgp scans each stage file for every *possible* `export` name (including names exported only inside `if`/`for` branches). A `${NAME.X}` reference to a declared stage `NAME` that exports no `X` anywhere is a typo and fails fast.
- **At runtime (authoritative):** if an export was conditional and did not fire this run, the unset `${NAME.X}` reference errors when the stage's args are interpolated, naming the missing export.

---

## 14. Manifests and fan-out

A single pipeline (or workflow) can be run once per row of a **manifest**, with the row's columns supplied as variables. The format is always explicit — cgp never guesses from the extension:

| Flag | Manifest format |
|------|-----------------|
| `-manifest FILE` (alias `-manifest-cgp`) | A shell glob of `.cgp` manifest files; each matched file's variables become one run |
| `-manifest-tsv FILE` | Tab-separated; the header row names the columns |
| `-manifest-csv FILE` | Comma-separated; the header row names the columns |
| `-manifest-json FILE` | A JSON array of objects; each object's keys become variables |

    $ cgp align.cgp -manifest-tsv samples.tsv          # one run per data row
    $ cgp wgs.cgp   -manifest /data/P*/manifest.cgp    # one workflow run per patient file

Each row runs the whole pipeline (or, for a workflow, all of its stages). Explicit command-line `--name value` variables override columns of the same name. A single **stat cache is shared across all runs**, so an invariant input like a shared `ref.fa` is `stat`-ed once rather than once per row. (Each row's pipeline graph is re-evaluated per row — per-row variables legitimately change which targets and branches exist — but the *parse* of the file happens once.)

---

## 15. Command-line interface

    cgp [options] <pipeline.cgp> [goal ...] [--name value ...]
    cgp sub [options] -- <command ...>
    cgp ledger {dump|search|vacuum|unlock} <db>
    cgp convert <old.cgp> [-o out.cgp]
    cgp version

A bare argument is a **goal** (a target to build); with none, cgp builds `@default` (or the first target). `--name value` sets a script variable; single-hyphen flags are cgp's own options.

| Option | Meaning |
|--------|---------|
| `-h` | Help. After a pipeline file, prints that script's help text ([§1.3](#13-comments-and-help-text)). |
| `-dr` | Dry run — render the scripts instead of executing/submitting. |
| `-force` | Rebuild every target in the goal graph, ignoring staleness ([§10.4](#104-restart)). |
| `-r NAME` | Runner: `shell` (default), `slurm`, `sge`, `pbs`, `batchq`, `graphviz`, `html` (also `cgp.runner`). |
| `-manifest*` | Manifest fan-out ([§14](#14-manifests-and-fan-out)). |

`-r graphviz` writes the dependency graph as Graphviz DOT to stdout (pipe to `dot -Tsvg`). `-r html` writes a **self-contained HTML status report** of the DAG to stdout: each output is colored by status — *done* (on disk), *running*/*queued* (its owning job is active in the scheduler, per the ledger), *failed* (owning job ended without producing it), or *pending* (not built). The report reads the ledger read-only, so it is safe to run while the pipeline is in flight.

Both build the graph reachable from the goals (instantiating any wildcard rules along the way), not every declared target. Combined with a manifest ([§14](#14-manifests-and-fan-out)), they produce **one** document covering all rows — graphviz a single `digraph` with a `subgraph cluster` per row, html a single page with a section per row (labeled by the row's `sample`/`id`/`name` column, else `row N`).

### 15.1 `cgp sub` — one-off submission
Submits a single command as a job, using the same runners, settings, and ledger as a pipeline. The command after `--` is treated as a body (`${input}`/`${output}` substitute):

    cgp sub -mem 8G -o out.bam -i in.bam -- 'samtools sort -o ${output} ${input}'

Options: `-name`, `-mem`, `-procs`, `-walltime`, `-o PATH` (declared output, repeatable), `-i PATH` (declared input), `-d JOBID` (depend on a job id), `-after PATH` (depend on the active ledger owner of `PATH`), `-r`, `-ledger`, `-dr`.

### 15.2 `cgp ledger`
- `cgp ledger dump <db>` writes every recorded job as a **key/value TSV** — one `<jobid>\t<KEY>\t<value>` line per fact (`PIPELINE`, `WORKINGDIR`, `RUNID`, `NAME`, `USER`, `SUBMIT`/`START`/`END`, `RETCODE`, `DEP`, `OUTPUT`, `TEMP`, `INPUT`, `SRC` for each job-script line, and `SETTING\t<key>\t<value>`).
- `cgp ledger search [filters] <db>` writes the same TSV for the jobs matching the filters (combined with AND; substring match except `-id`): `-i PATH` (an input contains), `-o PATH` (an output contains), `-g PATTERN` (a job-script line contains — grep), `-name NAME` (job name contains), `-id JOBID` (exact). A non-matching search prints nothing.
- `cgp ledger vacuum <db>` keeps only the last owner of each path and drops the rest ([§10.3](#103-ownership-and-vacuum)); `cgp ledger unlock <db>` removes a stale lockfile ([§10.6](#106-lockfile)).

`dump` and `search` open the ledger read-only, so they are safe to run while a pipeline is in flight.

### 15.3 `cgp convert` — migrate an older script
`cgp convert <old.cgp>` reads a legacy (JVM-cgpipe-era) script and prints the cgp-equivalent to stdout (or to `-o FILE`). It is a best-effort aid: it rewrites the mechanical differences — `<% … %>` setting blocks into directive blocks, `<% if … %>`/`<% for … %>` into `%`-control lines, `$<`/`$>`/`$%` into `${input}`/`${output}`/`${stem}`, `if … endif` / `for … done` into brace blocks, `__pre__::`/etc. into `@pre`, `name::` snippets into `snippet name { }`, `import` into `@name`, and `cgpipe.*` settings into `cgp.*` — and annotates anything it cannot safely convert with a `# cgp-convert:` comment for you to review.

---

## 16. Worked example (cgp v1)

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

## Open items

These language details are intentionally not yet pinned down:

- **Directive/shell disambiguation when `--` is omitted.** A body with no `--` is entirely shell; the explicit `--` is the canonical way to introduce a directive block. Whether to additionally warn on a directive-looking line in a no-`--` body is undecided.
