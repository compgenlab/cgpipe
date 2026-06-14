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

Eight types: `bool`, `int`, `float`, `string`, `list`, `range`, `map`, `file`.

    flag    = true            # bool (case-sensitive: true / false)
    count   = 10              # int
    rate    = 0.5             # float
    name    = "sample-1"      # string (always double-quoted)
    samples = []              # list
    samples = [1, 2, "x"]     # lists may mix types
    chunks  = 1..100          # range (1, 2, …, 100 when iterated)
    row     = {"a": 1, "b": 2} # map: ordered, string-keyed
    f       = open("s.tsv")   # file: a handle (read; open(p,"w"|"a") to write) — see §14

Typing is dynamic; a value's type is mostly invisible. `.type()` returns the type name as a string. CLI argument values arrive as strings and are parsed to `int`/`float`/`bool` when they look like one.

A **map** is an ordered, string-keyed collection — the literal `{}` / `{"k": v, …}`, and the value `read_tsv()`/`read_json()` produce per row (§14). Read a value by key with `m["k"]` (a missing key is unset, i.e. empty), or by position with `m[0]` (an `int` index selects the i-th key in insertion order). Keys are always strings; a string index is a key lookup, an int index is positional. Assign with `m["k"] = v`; `m["k"] += v` accumulates a list (and auto-creates the map if it is unset). Iterating a map (`for k in m`) yields its keys in order. Map methods are in §9.

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
A double-hyphen `--name value` on the command line sets the script variable `name` before the script runs. (Single-hyphen arguments like `-dr` are cgp's own options; double-hyphen arguments are script variables — the one exception is a bare `--help`, which is cgp's help request, equivalent to `-h`; `--help=value` is still a variable.)

    $ cgp pipeline.cgp --sample patient_42 --threads 16

The value is typed like any literal (`16` → int, `0.5` → float, `true`/`false` → bool, otherwise string). Three more conventions:

- **Boolean flags.** A bare `--name` — the next token is another option, or there is none — sets `name = true`. So `--adaptive` ⇒ `adaptive = true`.
- **Hyphens → underscores in the name.** Identifiers can't contain hyphens, so `--hp-dist` sets `hp_dist` (and `--hp_dist` is the same). Values are untouched.
- **Repeat to build a list.** Giving the same flag more than once makes a list: `--x a --x b` ⇒ `x = ["a", "b"]`.

Two edge cases: a value that starts with `-` needs the explicit form (`--offset=-5`, since `--offset -5` reads `-5` as an option and leaves `offset = true`); and a boolean flag placed immediately before the pipeline file would consume it as a value, so put the file first (`cgp p.cgp --adaptive`) or write `--adaptive=true`.

Because CLI values are applied first, `?=` defaults do not override them.

---

## 4. Expressions

### 4.1 Operators
- **Arithmetic:** `+ - * / % **` (power). Standard precedence (highest to lowest: `**`, then `* / %`, then `+ -`); `**` is right-associative. Unary minus binds looser than `**`, so `-2**2` is `-(2**2)` = `-4` (as in Python/Ruby); parenthesize for clarity.
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

Escaping: inside a `"…"` string literal a backslash escapes the next character — `\X` resolves to `X` — so `\$`/`\@` give a literal `$`/`@` (suppressing substitution) and `\"` gives a literal quote. This resolution applies across the whole string **including inside a `${…}`**, so an expression that needs a nested string literal escapes its quotes to survive the outer ones: `"${x.sub(\".bam\", \"\")}"`. A string nested inside a `${…}` inside a string is two escape layers deep, so a backslash that must reach the inner string (e.g. a regex `\.`) is written `\\\\`. If the whole string will be evaluated again, escape the substitution sigil twice (`\\$`).

`${{var}}` (double-eval) is for when a variable's *content* is itself a template; `$(cmd)` runs at parse time and its command string is variable-substituted first.

> **Note — `$(cmd)` and dry runs.** Because `$(cmd)` runs while the body is *rendered*, it also runs under `cgp -dr` (the script is rendered to be shown, which evaluates the substitution). This is intentional: a dry run reports the script that would run, including the resolved output of any render-time `$(cmd)`. When you want the command deferred to the job's own shell at runtime, write `\$(cmd)` (see [§6.1](#61-the-body-is-a-template)) — that form is emitted verbatim and never runs at render time.

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
    for s in xs with i  { ... }            # `with i` binds a 1-based counter
    for cond            { ... }            # while-style: runs while cond is true

Loop variables remain set after the loop (no separate scope). The optional
`with <name>` clause (on the `for…in` form only) binds `<name>` to the **1-based**
loop index, advancing each iteration; it is set alongside the element variable and
likewise persists after the loop.

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

### 5.3 Call statements

A **call** on its own line — `f.write("x")`, `f.close()` — is a statement, evaluated for its side effect (the return value is discarded). Only a call qualifies, so a line like `out.txt: …` is still a target. This is how the file-writing methods (§14) are invoked.

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

A body is **raw shell, not a cgp string literal**, so its escape rule differs from [§4.3](#43-string-substitution): only `\$` and `\@` are special — they suppress the cgp sigils, so `\${HOME}` and `\$HOME` keep their `$` for the shell, and `\$(cmd)` defers command substitution to job runtime instead of running at render time. Every other backslash is shell text and passes through verbatim (`echo "x\"y"` stays `echo "x\"y"`).

### 6.2 Directives and the `--` separator
A target body may begin with a **directive block** that sets per-job settings, separated from the shell by a line containing only `--`:

    aligned.bam: reads.fq ref.fa {{
        job.mem      = "16G"
        job.procs    = threads
        job.walltime = "12:00:00"
        job.container = "biocontainers/bwa:0.7.17"
        --
        bwa mem -t ${job.procs} ${ref} ${reads} > ${output}
    }}

- Before `--`: **cgp code**. Per-job settings are assigned under the **`job.` namespace** (`job.mem`, `job.procs`, …); a bare `IDENT = expr` sets an ordinary user variable, never a job setting (see [§11.4](#114-per-job-settings-the-job-namespace)). Ordinary cgp control flow is allowed here (it's cgp mode, no `%` prefix needed).
- After `--`: the **shell template**. Job settings are read back with the prefix, e.g. `${job.procs}`; a bare `${procs}` is the user variable (and errors if unset).
- `--` is **optional**, and it is the *only* thing that introduces a directive block. A body with **no `--` is entirely shell** — there is no directive section, and a line that happens to look like a directive (e.g. `job.mem = "16G"`) is passed through to the shell verbatim, not interpreted by cgp and not warned about. To set per-job settings you must open a directive block with `--`.

      copy.txt: input.txt {{
          cp ${input} ${output}
      }}

### 6.3 Inline conditionals
`${if cond; true_value; false_value}` substitutes one fragment or the other; the else-clause may be omitted (`${if cond; true_value}` ⇒ empty when false):

    bwa mem -t ${job.procs} ${if rg; "-R " + rg} ${ref} ${reads} > ${output}

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
        job.shexec = true
        --
        mkdir -p output logs
    }}

`job.shexec = true` runs the body directly on the submission host instead of submitting it (the usual choice for `mkdir`-style setup); only `@setup`/`@teardown` may be shexec, and `@postsubmit` always is. Per-target opt-out of `@pre`/`@post` via `job.nopre = true` / `job.nopost = true` directives.

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

### 9.6 map

| Method | Args | Returns | Description |
|--------|------|---------|-------------|
| `get(key)` | string or int | any | Value for `key` (or i-th by position); missing ⇒ unset |
| `has(key)` | string | bool | Is `key` present |
| `keys()` | — | list | Keys, in insertion/column order |
| `values()` | — | list | Values, in key order |
| `items()` | — | list | One `[key, value]` pair per entry |
| `length()` | — | int | Number of entries |

Also read/written by index: `m["k"]`, `m[0]`, `m["k"] = v`, `m["k"] += v` (§2). A field read keeps its type, so it chains: `row["bam"].basename()`, `row["n"] + 1`.

### 9.7 file

A file handle from `open(path[, mode])` — `mode` is `"r"` (default, read), `"w"`
(create/truncate), or `"a"` (create/append). Read methods require an `"r"` handle;
`write`/`writeln`/`close` a `"w"`/`"a"` handle. Reads happen when a reader method is
called; writes happen at evaluation time (§14).

| Method | Args | Returns | Description |
|--------|------|---------|-------------|
| `read_tsv(...)` | kw: `header=true`, `sep="\t"`, `comment="#"`, `skip=0`, `raw=false` | list of map | Tab-delimited rows as maps keyed by header |
| `read_csv(...)` | kw: same, `sep=","` | list of map | Comma-delimited rows |
| `read_json()` | — | list of map | A JSON array of objects |
| `read_lines(...)` | kw: `comment=""`, `skip=0`, `blank=true` | list of string | Raw lines |
| `read()` | — | string | The whole file |
| `write(s)` | any | file | Write `s` verbatim; returns the handle (chains) |
| `writeln(s)` | any | file | Write `s` followed by a newline |
| `close()` | — | — | Flush and close (idempotent) |
| `exists()` / `path()` | — | bool / string | Handle introspection |

With `header=false`, delimited columns are keyed positionally as `c0`, `c1`, …. Cells are auto-typed (`"3"`→int) unless `raw=true`. See §14. (cgp string escapes are `\X`→`X`, so a newline comes from `writeln`, not `"\n"`.)

### 9.8 Keyword arguments

A call may take keyword arguments after its positional ones: `f.read_tsv(header=false, sep="|")`. Used by the reader methods (above) to configure parsing; an unknown keyword is an error. A positional argument may not follow a keyword one.

---

## 10. The ledger (job tracking)

> The ledger is **optional** — a pipeline runs correctly without one.

### 10.1 Purpose and scope
The ledger is a record of **which job owns (last produced) which output file**, plus that job's inputs, dependencies, settings, and rendered job script (for audit and `cgp ledger search`/`dump` — see [§15.2](#152-cgp-ledger)). Its core query is "who owns output path `X`?" It enables cross-run composition: cgp won't resubmit a job whose output is already pending in the scheduler, even across separate invocations, and it wires new downstream work as a scheduler dependency (`afterok:<id>`) of the in-flight job.

Three responsibilities are kept strictly separate:

- **Filesystem (`stat`)** decides staleness ("is this output current relative to its inputs?").
- **Ledger** records identity/ownership and dependency edges.
- **Scheduler** owns live job state (queued/running/done). cgp asks `squeue`/`qstat`; the ledger stores **no** job state.

The ledger therefore stores **no file metadata (no mtimes)** and **no job state**. Enabled via `cgp.ledger` (a directory path).

### 10.2 Storage
The ledger is a **directory** of append-only JSON-lines (JSONL) files; `cgp.ledger` names the directory (created if absent). There is no shared database file and **no cross-process lock**.

- **One line = one job record.** Each submission appends a single JSON object (a complete job, with `outputs`/`inputs`/`deps`/`settings`/`script` nested in it) to a file, then `fsync`s it. A record carries an ordering header — `ts` (write time, Unix ns), `seq` (per-writer counter), `host`, `pid` — followed by the job fields:

      {"ts":1733356800123456789,"seq":1,"host":"node01","pid":4823,
       "job_id":"1002","run_id":"align-20260604","name":"align","pipeline":"align.cgp",
       "working_dir":"/scratch/me","user":"me","submit_time":1733356800,
       "deps":["1001"],"outputs":["aligned.bam"],"inputs":["trimmed.fq"],
       "settings":{"mem":"8000"},"script":"bwa mem trimmed.fq > aligned.bam"}

- **One file per writer process,** named `<host>-<pid>-<nanos>-<n>.jsonl`. Because each process appends only to its own file, concurrent runs never write the same file and need no lock — robust on NFS/Lustre, where shared-file locking is unreliable.
- **Reading folds the directory** into an in-memory view: every record is read and the **latest one wins**, per job id and per output path, ordered by the total order `(ts, host, pid, seq)`. Within one writer this is exact append order; across writers `ts` decides, with host/pid/seq as deterministic tie-breakers. A malformed trailing line (a writer that crashed mid-append) is skipped, never fatal.
- **`snapshot.jsonl`** is the compacted baseline written by `vacuum` (§10.3); it is read like any other file, just first.

The `submit_time` field (whole seconds) is the job's recorded submission time, used to order `cgp ledger dump`/`search` output; the `ts` header (nanoseconds) is the separate fold-ordering key.

### 10.3 Ownership and vacuum
- **Lookup:** the folded view maps each output path to the job id of the latest record that produced it.
- **Claim (last job wins):** a submitted job appends a record listing its outputs; on the next fold, that record's ordering key supersedes any earlier claim of the same path. Recency is encoded by the `(ts, host, pid, seq)` order — no ordering column or in-place update needed. This covers both "previous owner failed" and "previous owner succeeded but an input changed, so a new job re-produces the output."
- **Vacuum** (`cgp ledger vacuum`): re-fold the directory, write the jobs that still own at least one output to a fresh `snapshot.jsonl` (temp file + atomic rename), then remove the per-process logs that were folded. The last owner of each path survives even if it failed. Logs still being appended by a live local process are left in place and reclaimed by a later vacuum once idle, so run it when the ledger is otherwise quiet.

### 10.4 Restart
Restart is **mtime-based**, make-style: an output is rebuilt if it is missing or older than any input. The `-force` option rebuilds every target in the goal graph regardless. There are no "restart modes." The performance win at scale is a **run-scoped stat cache**: within one invocation each path is `stat`-ed once and reused (e.g. a shared `ref.fa` referenced by every sample's target is stat-ed once, not per target).

Because staleness is mtime-based and cgp tracks ownership, **not** job success (only the scheduler knows that), a job killed mid-write can leave a half-written output that looks current and is skipped on restart. The recommended user-level guard is to write atomically — emit to a temp path and `mv` into place only on success (`cmd > ${output}.tmp && mv ${output}.tmp ${output}`) — so the final filename never exists in a partial state. This is an idiom, not a built-in: correctness depends on the temp and final paths sharing a filesystem and on a partial write being meaningful for the format, neither of which cgp can assume generically.

### 10.5 Cross-run and cross-stage reuse
When a ledger is configured and a scheduler runner is in use, an input that has **no in-run producer** and **isn't on disk yet** is looked up in the ledger: if its owning job is still active (per `squeue`/`qstat`), the new work is wired as a scheduler dependency (`afterok:<id>`) of that in-flight job instead of being treated as a "no rule to make" error or duplicated. This is what makes re-running a pipeline before it has finished safe, and it is also how a later workflow [stage](#13-workflows-stage-and-export) waits on a file an earlier stage's jobs are still queued to produce. With the shell runner each job has already completed (the file exists), so the lookup is unnecessary.

### 10.6 Concurrency
The ledger takes **no lock**. Each process appends only to its own file, so concurrent runs sharing one ledger directory simply each write a separate file; reads fold them together (§10.2). A reader loads the directory once at open time, so a peer's records written during a run are seen on the next open, not mid-run — at worst this resubmits an already-queued job (a performance hiccup), never corruption. There is no `unlock` subcommand: with no lock there is never anything to clear.

---

## 11. Configuration

### 11.1 Namespace and locations
The configuration namespace is `cgp.*`. User-scoped state lives under a single root, `~/.cgp/`:

| Path | Purpose |
|------|---------|
| `<cgp dir>/.cgprc` | Server-wide global config, next to the installed `cgp` binary (lowest priority) |
| `/etc/cgp/config`  | System (site-wide) config |
| `~/.cgp/config`    | User config (itself a cgp script) |
| `~/.cgp/custom_template.cgp` | Custom submission template applied to the active scheduler runner |
| `~/.cgp/cache/`    | Cache / state |

### 11.2 Resolution order (later wins)
1. Built-in defaults
2. Global config next to the binary (`<cgp dir>/.cgprc`)
3. System config (`/etc/cgp/config`)
4. User config (`~/.cgp/config`)
5. Environment (`CGP_ENV` evaluated as cgp; `CGP_RUN_ID`, `CGP_DRYRUN`)
6. Command-line `--name value`
7. The pipeline script (`=` always wins; `?=` respects upstream)

### 11.3 Selected `cgp.*` settings

| Variable | Purpose |
|----------|---------|
| `cgp.ledger` | Ledger directory path; enables cross-run job tracking |
| `cgp.run_id` | Run identifier (also `CGP_RUN_ID`) |
| `cgp.runner` | `shell`, `slurm`, `sge`, `pbs`, `batchq`, `graphviz`, `html` |
| `cgp.runner.<name>.<setting>` | Runner-specific |
| `cgp.runner.<name>.template` | Path to a custom submission template, replacing the built-in for that scheduler (else `~/.cgp/custom_template.cgp`; scaffold with `cgp show-template -r <name>`) |
| `cgp.runner.<name>.global_hold` | Submit every job held until the pipeline submits cleanly, then release (off by default) |
| `cgp.runner.sge.parallelenv` | SGE parallel-environment name for `-pe <pe> <procs>` when `procs > 1` |
| `cgp.runner.shell.autoexec` | Shell runner: execute the assembled script instead of emitting it (default off) |
| `cgp.shell` | Default shell for rendered bodies |
| `cgp.dryrun` | Set by `-dr` / `CGP_DRYRUN` |
| `cgp.container.engine` | `docker`, `singularity`/`apptainer`; unset disables container wrapping |
| `cgp.container.*` | Bind mounts, env passthrough, engine opts, etc. |

`global_hold` (hold all jobs until the pipeline submits cleanly) and host-environment capture are **not** defaults — enable them in `~/.cgp/config` if you want them. This keeps the core small; belt-and-suspenders behavior is opt-in.

### 11.4 Per-job settings (the `job.*` namespace)
Per-job settings live under a single **`job.` namespace** — written the same way everywhere: as a global default (`job.<name> = …`), as a directive inside a target body's directive block, and read back in bodies/templates as `${job.<name>}`. A bare name is always an ordinary user variable, never a job setting, so a user variable and a job setting may share a base name without colliding (`--name foo` sets `name`; `job.name = …` sets the job's name). Settings are captured per target at definition time, so a `job.*` set earlier (globally or in an enclosing scope) is the default for every target defined after it; `job.procs` is seeded to `1`.

Resource/identity: `job.name`, `job.procs`, `job.mem`, `job.walltime`, `job.stdout`, `job.stderr`, `job.queue`, `job.account`, `job.mail`, `job.gpu`, `job.container`. Submission control: `job.env` (capture the submit-host environment — SLURM `--export=ALL`, SGE/PBS `-V`, BatchQ `-env`), `job.hold` (submit this job held), `job.setup` (a list of shell lines emitted before the body in the submission script), `job.custom` (extra directive lines, verbatim). Assembly flags: `job.shexec`, `job.nopre`, `job.nopost`. Scheduler-specific (ignored elsewhere): `job.qos` (SLURM/PBS), `job.nice` (SLURM); SGE's `-pe` needs `cgp.runner.sge.parallelenv` when `job.procs > 1`. The friendly reference with per-scheduler mapping is the [Running Jobs chapter](README.md).

`job.array` is special: set to a **positive integer** (the element's task index, e.g. the `with i` counter), it marks the target as a member of a **job array**. cgp coalesces all targets from one declaration that carry `job.array` into a single scheduler array submission (`--array=<indices>`, a `case` over the task-id variable); the supplied integer is the scheduler task id. Members must be submission-compatible (identical `job.*` apart from the index) and have unique indices, else it is an error. A downstream that consumes the array depends on the exact tasks it needs (`afterok:<arrayid>_<index>`); an element-wise array→array dependency (needing `aftercorr`) is not yet supported and errors. SLURM/BatchQ pack the array; SGE/PBS submit one job per element. See the [Array Jobs chapter](README.md). (`cgp sub --array` exposes the same as a string index spec — [§15.1](#151-cgp-sub--one-off-submission).)

---

## 12. Containers and GPUs

A target's body can be wrapped to run inside a container without changing the body itself. Wrapping is enabled when **both** a container engine and a per-target image are set:

- `cgp.container.engine` — `docker`, `singularity`, or `apptainer` (set in config or the script). Unset disables all wrapping.
- `job.container = "<image>"` — a per-target directive naming the image. A target with no `job.container` runs unwrapped even when an engine is configured.

      aligned.bam: reads.fq ref.fa {{
          job.container = "biocontainers/bwa:0.7.17"
          job.mem       = "16G"
          --
          bwa mem ${ref} ${reads} > ${output}
      }}

When wrapping is active, cgp writes the rendered body to a temp file and executes it inside the image, bind-mounting the input and output paths automatically, setting the working directory, and (for Docker) mapping the host user. Additional settings, available globally as `cgp.container.<name>` and/or per target as `job.container.<name>`:

| Setting | Purpose |
|---------|---------|
| `job.container.bind` / `cgp.container.bind` | Extra bind mounts (repeatable / list) |
| `job.container.env` / `cgp.container.env` | Environment variables to pass through |
| `job.container.opts` (or `cgp.container.docker_opts` / `cgp.container.singularity_opts`) | Raw extra flags for the engine |
| `job.container.body_dir` / `cgp.container.body_dir` | Where the temp body file is written/mounted (default `/tmp`) |
| `job.container.shell` / `cgp.container.shell` | Shell used to run the body inside the image (default `sh`) |
| `cgp.container.user_map` | Docker only: add `-u $(id -u):$(id -g)` (default on) |

### 12.1 GPUs
`job.gpu` requests GPUs for a target and drives both layers at once:

    train.model: data.tfrecord {{
        job.gpu = 2
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
`stage NAME FILE ARGS...` declares one stage. `ARGS` use the same `--name value` convention as command-line variables ([§3.1](#31-command-line-variables)) — including boolean flags, hyphen→underscore names, and repeat-to-list — and are the variables the stage pipeline receives. `NAME`, `FILE`, and each arg are interpolated against the workflow's variables before the stage runs.

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

## 14. Reading files: sample sheets, scatter and gather

`open(path)` returns a **file** handle; its reader methods turn a sample sheet into data the pipeline can loop over. Reading happens at evaluation time, so the whole cohort lives in **one** dependency graph — you can scatter a per-sample target and then **gather** every sample's output into a downstream target, all in one pipeline.

    samples = open("samples.tsv").read_tsv(header=true)   # list of maps, one per row

| Reader | Returns | Notes |
|--------|---------|-------|
| `read_tsv(...)` / `read_csv(...)` | list of map | Header row names the columns; cells auto-typed (§9.7) |
| `read_json()` | list of map | A JSON array of objects |
| `read_lines(...)` | list of string | Raw lines (comment- and blank-aware) |
| `read()` | string | The whole file |

Each row is a `map`: read a column by name (`row["sample"]`) or position (`row[0]`). A field keeps its type, so it chains — `row["bam"].basename()`, `row["n"] + 1`.

**Scatter and gather** — accumulate per-sample outputs into a list, then depend on `@{…}`:

    samples = open("samples.tsv").read_tsv(header=true)
    sums = []
    for row in samples {
        name = row["sample"]
        out  = name + ".sum"
        sums += out
        ${out}: ${row["input"]} {{
            wc -w < ${input} > ${output}
        }}
    }
    cohort.txt: @{sums} {{ cat ${input} > ${output} }}   # the gather
    @default: cohort.txt

**Group by a column** — a map of lists buckets rows, then one gather per group:

    groups = {}
    for row in samples { groups[row["category"]] += row["sample"] + ".sum" }
    for cat in groups {
        ${cat}.report: @{groups[cat]} {{ cat ${input} > ${output} }}
    }

> A column used inside a `"…"` string must be bound to a plain variable first (e.g. `name = row["sample"]`), because a nested `"` would close the string. In a target declaration or a `{{ }}` body, `${row["col"]}` is written as-is.

### 14.1 Writing files

`open(path, "w")` (truncate) or `open(path, "a")` (append) returns a write handle;
`write(s)` writes verbatim, `writeln(s)` adds a newline, `close()` flushes. A
newline comes from `writeln` (cgp string escapes are `\X`→`X`, so `"\n"` is the
letter `n`).

    f = open("params.txt", "w")
    f.writeln("sample=${sample}")
    f.writeln("ref=${ref}")
    f.close()

Writes happen at **evaluation time** (like `$(…)` and reads), so they occur whenever
the script is evaluated. **Under `-dr` they are no-ops** — `open`-for-write, `write`,
and `close` do nothing and cgp prints `dry-run: not writing to file "…"` once per
path. (Dry-run is the only safe-preview signal known before evaluation; to inspect a
write-bearing pipeline with no side effects, add `-dr`.)

Because an eval-time write is not a graph node, a file a **job consumes** is usually
better produced by a **target body** (`params.txt: {{ printf … > ${output} }}`) so it
is stale-checked, scheduled, and present under `-dr`. Reserve `open(…,"w")` for files
*outside* the dependency graph — logs, sidecar metadata, a derived list.

---

## 15. Command-line interface

    cgp [options] <pipeline.cgp> [goal ...] [--name value ...]
    cgp sub [options] <command ...> [-- <file ...>]
    cgp ledger {dump|search|status|vacuum} <dir>
    cgp convert <old.cgp> [-o out.cgp]
    cgp show-template -r <runner>
    cgp lsp
    cgp version

A bare argument is a **goal** (a target to build); with none, cgp builds `@default` (or the first target). `--name value` sets a script variable; single-hyphen flags are cgp's own options.

The default runner is `shell`, which **assembles the stale targets into one runnable bash script (dependency order) and writes it to stdout — it does not execute.** Pipe it to `bash`, redirect it to a file, or set `cgp.runner.shell.autoexec = true` (e.g. in `~/.cgp/config`) to have cgp run it directly. The scheduler runners (`slurm`/`sge`/`pbs`/`batchq`) submit; `-dr` makes any runner render without executing/submitting.

| Option | Meaning |
|--------|---------|
| `-h`, `--help` | Help. With a pipeline file (in any position), prints that script's help text ([§1.3](#13-comments-and-help-text)); with no file, prints cgp's own help. (`--help=value` is still an ordinary script variable.) |
| `-dr` | Dry run — render the scripts instead of executing/submitting. |
| `-force` | Rebuild every target in the goal graph, ignoring staleness ([§10.4](#104-restart)). |
| `-r NAME` | Runner: `shell` (default), `slurm`, `sge`, `pbs`, `batchq`, `graphviz`, `html` (also `cgp.runner`). |

`-r graphviz` writes the dependency graph as Graphviz DOT to stdout (pipe to `dot -Tsvg`). `-r html` writes a **self-contained HTML status report** of the DAG to stdout: each output is colored by status — *done* (on disk), *running*/*queued* (its owning job is active in the scheduler, per the ledger), *failed* (owning job ended without producing it), or *pending* (not built). The report reads the ledger read-only, so it is safe to run while the pipeline is in flight.

Both build the graph reachable from the goals (instantiating any wildcard rules along the way), not every declared target. Because a sample-sheet cohort ([§14](#14-reading-files-sample-sheets-scatter-and-gather)) is one graph, `-r graphviz`/`-r html` already render the whole cohort — scatter and gather — in a single document.

### 15.1 `cgp sub` — one-off submission
Submits a single command as a job, using the same runners, settings, and ledger as a pipeline. The first token that is not a recognized option begins the command; everything from there until a bare `--` is the command, treated as a body (`${input}`/`${output}` substitute):

    cgp sub -m 8G -o out.bam -i in.bam samtools sort -o ${output} ${input}

Options: `-n, --name`, `-m, --mem`, `-p, --procs`, `-t, --walltime`, `-o, --output PATH` (declared output, repeatable), `-i, --input PATH` (declared input, repeatable), `-d, --deps IDS` (depend on existing job ids, comma-separated; repeatable), `-a, --after PATH` (depend on the active ledger owner of `PATH`; repeatable), `-f, --files-from F` (read fan-out files from `F`, one per line; `-` = stdin; only once), `-r, --runner`, `-l, --ledger`, `-dr`, `-h, --help`.

**Fan-out.** Files listed after `--` (or supplied via `--files-from`) each submit one independent job, with `{}` placeholders expanded against the file in the command, the job name, and the `-o`/`-i`/`-a` values:

| Placeholder | Expands to |
|-------------|------------|
| `{}` `{^}` | the full input path |
| `{@}` | the basename (directory stripped) |
| `{^SUF}` | the full path with a trailing `SUF` removed (if it ends with `SUF`) |
| `{@SUF}` | the basename with a trailing `SUF` removed (if it ends with `SUF`) |
| `{#}` | the 1-based fan-out index |
| `{{}}` | a literal `{}` |

Each fan-out file becomes its job's primary declared input; fan-out jobs are independent siblings (`-d` applies to every job, `-a` is resolved per file after `{}` expansion). With no files, a single job is submitted and `{}` is not substituted.

    cgp sub -r slurm -m 4G -o '{@.fastq.gz}.bam' 'bwa mem ref.fa {} > {@.fastq.gz}.bam' -- *.fastq.gz

**Array fan-out (`--array`).** With `--array`, the fan-out is submitted as a single scheduler **job array** (`--array=1-N`) instead of N independent jobs. Each file becomes one array task: the rendered body is a `case` over the scheduler's task-id variable (`$SLURM_ARRAY_TASK_ID`, `$BATCHQ_ARRAY_TASK_ID`, `$PBS_ARRAY_INDEX`) with one branch per file, each the file's fully `{}`-expanded command. Supported on `slurm`/`batchq`/`pbs`; `sge` and `shell` fall back to one job per file. A fixed `-d`/`-a` applies to the whole array; a `{}`-expanded `-a/--after` is rejected, because a single array submission carries one dependency directive and so cannot express a per-element dependency. See the [Array Jobs chapter](README.md).

### 15.2 `cgp ledger`
- `cgp ledger dump <dir>` writes every recorded job as a **key/value TSV** — one `<jobid>\t<KEY>\t<value>` line per fact (`PIPELINE`, `WORKINGDIR`, `RUNID`, `NAME`, `USER`, `SUBMIT`, `DEP`, `OUTPUT`, `TEMP`, `INPUT`, `SRC` for each job-script line, and `SETTING\t<key>\t<value>`).
- `cgp ledger search [filters] <dir>` writes the same TSV for the jobs matching the filters (combined with AND; substring match except `-id`): `-i PATH` (an input contains), `-o PATH` (an output contains), `-g PATTERN` (a job-script line contains — grep), `-name NAME` (job name contains), `-id JOBID` (exact). A non-matching search prints nothing.
- `cgp ledger status [-r RUNNER] [-output] <dir>` probes the scheduler for the live status of recorded jobs. It needs a scheduler runner: `-r RUNNER` (slurm/sge/pbs/batchq), else `cgp.runner` from config; a non-scheduler runner is rejected. `<dir>` likewise falls back to `cgp.ledger`. The displayed status is the scheduler's **native** word (e.g. `PENDING`, `PROXYQUEUED`, `qw`), not the normalized report vocabulary.
  - **Job mode** (default) writes `<jobid>\t<STATUS>\t<name>` per recorded job; `STATUS` is `UNKNOWN` once a job has aged out of the scheduler.
  - **Output mode** (`-output`) writes `<output>\t<jobid>\t<STATUS>` per owned output (the most recent owning job, [§10.3](#103-ownership-and-vacuum)). A still-active job shows its live native status; a finished or aged-out job is reconciled against the file's mtime: `COMPLETE` (aged out, file present and not older than `submit_time`) or `DIRTY` (missing, older than `submit_time`, or — when the scheduler reports an end time — modified more than five minutes after it). The end-time upper bound is best-effort and applied only where the scheduler exposes a completion time (SLURM via `scontrol`; batchq via `batchq status -e`).
- `cgp ledger vacuum <dir>` compacts the ledger to a single `snapshot.jsonl`, keeping only the last owner of each path and dropping the rest ([§10.3](#103-ownership-and-vacuum)). There is no `unlock` subcommand — the ledger takes no lock ([§10.6](#106-concurrency)).

`dump`, `search`, and `status` open the ledger read-only, so they are safe to run while a pipeline is in flight.

### 15.3 `cgp convert` — migrate an older script
`cgp convert <old.cgp>` reads a legacy (JVM-cgpipe-era) script and prints the cgp-equivalent to stdout (or to `-o FILE`). It is a best-effort aid: it rewrites the mechanical differences — `<% … %>` setting blocks into directive blocks, `<% if … %>`/`<% for … %>` into `%`-control lines, `$<`/`$>`/`$%` into `${input}`/`${output}`/`${stem}`, `if … endif` / `for … done` into brace blocks, `__pre__::`/etc. into `@pre`, `name::` snippets into `snippet name { }`, `import` into `@name`, and `cgpipe.*` settings into `cgp.*` — and annotates anything it cannot safely convert with a `# cgp-convert:` comment for you to review.

### 15.4 `cgp show-template` — print a scheduler's default template
`cgp show-template -r <slurm|sge|pbs|batchq>` prints that scheduler's built-in submission template to stdout, as a starting point for a custom one. Save it (`> ~/.cgp/custom_template.cgp`, or any path named by `cgp.runner.<name>.template`) and edit it to replace the built-in submission script — see [§11.3](#113-selected-cgp-settings). The rest of the runner's wiring (submit command, status probes, mem normalization) is unchanged; only the rendered script is overridden.

### 15.5 `cgp lsp` — language server
`cgp lsp` runs a [Language Server Protocol](https://microsoft.github.io/language-server-protocol/) server over stdin/stdout, providing diagnostics (parse errors), semantic tokens, hover, and completion for `.cgp` files. It is launched by an editor, not used interactively; the bundled VSCode extension (`editor/vscode/`) starts it automatically when `cgp` is on `PATH`.

---

## 16. Worked example (cgp v1)

    #!/usr/bin/env cgp
    #
    # Per-chromosome variant calling with a merge step.
    #
    # Options:
    #     --bam FILE   input alignments (indexed BAM)
    #     --ref FILE   reference FASTA
    #     --out FILE   merged, bgzip-compressed output VCF (.vcf.gz)

    if !bam { print "ERROR: --bam required"; exit 1 }
    if !ref { print "ERROR: --ref required"; exit 1 }
    if !out { print "ERROR: --out required"; exit 1 }

    chroms   = "1 2 3 4 5 X Y".split(" ")
    per_chrom = []

    for c in chroms {
        per_chrom += "${out}.${c}.vcf.gz"

        ^${out}.${c}.vcf.gz: ${bam} ${ref} {{
            job.name = "call-chr${c}"
            job.mem  = "8G"
            --
            bcftools mpileup -r chr${c} -f ${ref} ${bam} ${extra_flags?} \
                | bcftools call -mv -O z -o ${output}.tmp - \
                && mv ${output}.tmp ${output}
        }}
    }

    ${out}: @{per_chrom} {{
        job.name = "merge-${out.basename()}"
        job.mem  = "4G"
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

