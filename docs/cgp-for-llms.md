# cgp in one page (LLM / quick reference)

A dense, complete reference for writing **cgp** pipelines. cgp is a small language
that generates and submits job scripts: you declare `output: inputs` rules with
shell bodies, and cgp builds a dependency graph and runs the stale parts locally
or on a scheduler (SLURM/SGE/PBS/BatchQ). If you can read `make` and bash, you can
read cgp. This file is self-contained — read it top to bottom and you can author
valid cgp. The normative reference is [`language-spec.md`](language-spec.md); when
in doubt, that and the `tests/` fixtures win.

## Mental model
- A `.cgp` file is read top-to-bottom as **cgp code** (global context).
- `{ ... }` = a block of cgp code (`if`/`for`). `{{ ... }}` = a **shell body**
  (raw shell, captured verbatim, rendered at job time).
- A **target** declares `outputs : inputs {{ body }}`. Requesting an output runs
  the body if the output is missing or older than an input.

## Hello
```
#!/usr/bin/env cgp
#
# Help text: this comment block is shown by `cgp file.cgp -h`.

greeting ?= "world"

hello.txt: {{
    echo "Hello, ${greeting}!" > ${output}
}}
@default: hello.txt
```
Run: `cgp hello.cgp` prints the assembled bash to stdout (pipe to `bash` to run);
`cgp hello.cgp --greeting Sam` overrides the variable; `cgp -dr ...` previews;
`cgp -r slurm ...` submits to SLURM instead.

## Types
`bool` (`true`/`false`), `int`, `float`, `string` (always `"..."`),
`list` (`["a","b"]`, may mix types), `range` (`1..100`, inclusive, stores only
bounds). `.type()` returns the type name.

## Variables
```
x = 4            # set
x ?= 9           # set only if unset (defaults; respects CLI/env/config)
x += 2           # append (promotes scalar -> list)
unset x          # remove
```

## Command-line variables (double hyphen)
`cgp p.cgp --sample s1 --threads 16` sets `sample`, `threads`. Rules:
`--flag` (bare) → `flag = true`; `--hp-dist` → `hp_dist` (hyphens→underscores);
repeat → list (`--x a --x b` → `["a","b"]`). Put the file before a trailing bare
flag. Guard required vars: `if !sample { print "ERROR: --sample required"; exit 1 }`.
(Single-hyphen args like `-dr`, `-r`, `-force`, `-manifest*` are cgp's own options.)

## Operators & expressions
Arithmetic `+ - * / % **` (`**` power, right-assoc; `/` is int division unless a
float is involved). `+` concatenates strings; `*` repeats strings/lists. Compare
`== != < <= > >=`. Logic `&& || !`. `!x` is also "unset or empty/false" (guards).
Index/slice 0-based, negative from end, half-open clamped: `f[0] f[-1] f[1:3] f[:2] f[-2:]`.

## String substitution (inside "...")
```
${var}     # substitute; ERROR if unset; a list joins with spaces
${var?}    # like ${var} but "" when unset
${expr}    # any expression: ${input[0]}, ${name.basename()}
${if c; a; b}   # inline conditional; else optional (empty when false)
@{list}    # list expansion -> one copy per element (in declarations/strings)
${{var}}   # double-eval: substitute, then evaluate result as cgp source
$(cmd)     # run cmd in shell AT PARSE/RENDER TIME; substitute stdout
```
Escaping in a string literal: `\$` `\@` literal sigils, `\"` literal quote.
Nested string arg escapes its quotes: `"${name.sub(\".bam\",\"\")}"`.

## Control flow & statements
```
if a > 1 { ... } elif b { ... } else { ... }
for i in 1..3 { ... }          # range
for s in ["a","b"] { ... }     # list
for s in xs with i { ... }     # `with i` = 1-based loop counter (alongside the element)
for cond { ... }               # while-style
print a, b            # stdout (comma args space-joined); inside a body, appends to the script
include "other.cgp"   # inline another file (global context) — shared defaults/targets
eval "x = 6*7"        # evaluate a string as cgp source
exit 1                # stop; code becomes cgp's exit status
```

## Methods
- string: `.split(d)` (no-arg → chars), `.sub(re,repl)` (Go RE2; `$1` groups),
  `.upper() .lower() .length() .contains(s) .join(list) .basename() .dirname()
  .abspath() .exists() .isfile() .isdir()`.
- list: `.length() .contains(v) .join(sep)` (+ index/slice/`+=`).
- range: `.length() .contains(v)`; iterates/indexes like a list.

## Targets
```
out1 out2 : in1 in2 {{
    <directive block, optional>
    --
    <shell body>
}}
```
- **Build vars** (in body): `${input}` `${output}` `${stem}` (+ `${input[0]}`,
  `${output[1]}`). `${input}` joins all inputs with spaces; index for one.
- **Directive block** (before `--`): set per-job settings under the `job.` namespace
  (`job.mem`, `job.procs`, `job.walltime`, `job.name`, `job.stdout`, `job.stderr`,
  `job.gpu`, `job.mail`, `job.account`, `job.queue`, `job.qos`, `job.container`,
  `job.custom=[...]`, `job.shexec`, `job.nopre`, `job.nopost`). Read them back in the
  body/template as `${job.procs}` etc. A bare name is always a user variable, never a
  job setting, so `--name foo` (`name`) and `job.name` never collide. `job.procs`
  defaults to 1; a setting is the default for targets defined after it.
  **No `--` ⇒ no directive block; the whole body is shell.**
- **Array jobs**: set `job.array = <int>` (the element's task index, e.g. `with i`)
  on a fan-out rule and cgp submits all its elements as ONE scheduler array
  (slurm/batchq; sge/pbs → one job per element). Elements must be
  submission-compatible (same `job.*` but the index) with unique indices, else error.
  A gather depends on the exact tasks (`afterok:<id>_<i>`); restarts submit only the
  stale indices. Element-wise array→array (needs aftercorr) isn't supported yet.
- Body is raw shell: only `\$`/`\@` are special. `\$(cmd)` and `\${VAR}` defer to
  the job's shell (vs `$(cmd)`/`${var}` which cgp evaluates at render time).
- **Inline conditional** for optional flags: `bwa ${if rg; "-R " + rg} ...`.
- **`%`-control lines** in a body emit shell per iteration:
  `out: {{ \n% for p in parts {\n echo ${p}\n% }\n}}`.

## Declaration features
- **Wildcard** `%`: `%.gz: % {{ gzip -c ${input} > ${output} }}` — `%` matches the
  stem, reused on the input side, available as `${stem}`.
- **List expansion** `@{list}` in a declaration makes ONE rule over all items:
  `@{samples}.bam: @{samples}.fq {{ ... }}`. (Contrast `${input}` which joins in a body.)
- **Multiple defs**: same output defined twice → first whose inputs are satisfiable wins.
- **Temp output** `^out`: intermediate; when ABSENT, staleness looks through it to
  its inputs; cgp never auto-deletes it.
- **Opportunistic** `: in1 in2 {{ ... }}` (no outputs): runs only if all inputs
  already exist; never forces them. Use for guarded cleanup of temps.
- **Bodyless aggregator**: `all: a.txt b.txt` (no body) — phony grouping target.

## Reserved targets (@-prefixed, never a file)
```
@pre {{ ... }}        # prepended to every body (unless nopre)
@post {{ ... }}       # appended to every body (unless nopost)
@setup {{ shexec=true\n--\n mkdir -p logs }}   # once, first; shexec runs on submit host
@teardown {{ ... }}   # once, last
@postsubmit {{ echo ${output} ${jobid} }}      # per submitted job; ${jobid} = scheduler id
@default: a b         # goals to build with no target named (else first target)
```

## Runners & scheduling
`-r shell` (default; prints bash) | `slurm` `sge` `pbs` `batchq` (submit) |
`graphviz` (DOT) | `html` (status page). Set via `-r` or `cgp.runner`. Directives
map per scheduler (`mem="8G"` → SLURM `--mem=8000`, `procs=4` → `-c 4`).
Dependencies are derived from `output: input` edges (SLURM `afterok:<id>`).
One-off: `cgp sub -r slurm -m 8G -o out.bam -i in.bam 'samtools sort -o ${output} ${input}'`.
Fan-out one job per file with `{}` (`{@}`=basename, `{^.gz}`/`{@.gz}`=suffix-strip, `{#}`=index): `cgp sub -m 4G -o '{@.fastq.gz}.bam' 'bwa mem ref.fa {} > {@.fastq.gz}.bam' -- *.fastq.gz` (or `--files-from list.txt`).
Add `--array` to submit the fan-out as ONE scheduler array (slurm/batchq/pbs; one task per file, dispatched by the task-id var): `cgp sub -r slurm --array 'fastqc {} -o qc/' -- *.fastq`. Fixed `-d`/`-a` apply to the whole array; a `{}`-expanded `--after` is rejected (per-element dep).

## Ledger (optional), workflows, manifests
- **Ledger** (`cgp.ledger = "jobs.ledger"`, a directory): records which job owns which output;
  enables cross-run reuse of still-queued jobs (scheduler runners). Restart is
  mtime-based regardless; `-force` rebuilds all. Inspect: `cgp ledger dump/search/vacuum`.
- **Workflow** (chain pipelines): `stage NAME FILE --arg ...`; a stage exposes a
  value with top-level `export name = expr`, used as `${NAME.name}` in later stages.
- **Manifest fan-out**: `cgp p.cgp -manifest-tsv samples.tsv` runs the pipeline once
  per row, columns → variables. Also `-manifest-csv/-json`, and `-manifest` (glob of
  `.cgp` files). CLI `--name` overrides a column.

## Worked example (per-chromosome calling + merge + cleanup)
```
#!/usr/bin/env cgp
#
# Options: --bam FILE  --ref FILE  --out PREFIX
if !bam { print "ERROR: --bam required"; exit 1 }
if !ref { print "ERROR: --ref required"; exit 1 }
if !out { print "ERROR: --out required"; exit 1 }

chroms = "1 2 3".split(" ")
parts = []
for c in chroms {
    parts += "${out}.${c}.vcf.gz"
    ^${out}.${c}.vcf.gz: ${bam} ${ref} {{
        job.name = "call-chr${c}"
        job.mem  = "8G"
        --
        bcftools mpileup -r chr${c} -f ${ref} ${bam} | bcftools call -mv -O z -o ${output}
    }}
}
${out}.vcf.gz: @{parts} {{
    job.name = "merge"
    --
    bcftools concat -O z -o ${output} ${input}
}}
@default: ${out}.vcf.gz

: ${out}.vcf.gz @{parts} {{
    if [ -e ${out}.vcf.gz ]; then
% for v in parts {
        rm -f ${v}
% }
    fi
}}
```

## Common mistakes (for generation)
- A `{{ }}` body must end with `}}` **on its own line** (no single-line bodies).
- To set job settings you MUST include the `--` separator; without it the lines
  are shell.
- `$(cmd)` runs at render time (even under `-dr`); use `\$(cmd)` for the job's shell.
- `${var}` on an unset variable is an error; use `${var?}` for empty.
- Use `@{list}` in declarations (separate items); `${list}`/`${input}` in bodies
  (space-joined).
- Reserved/`@`-names never name files; `%` wildcards only in the declaration line.
- Write atomically: a killed job can leave a half-written `${output}` that looks
  fresh and won't rebuild. Prefer `cmd > ${output}.tmp && mv ${output}.tmp ${output}`
  (cgp tracks mtime, not exit status).
