# Build Targets

A **target** is the unit of work in cgpipe. It declares the outputs it produces, the
inputs they depend on, and the shell that builds them:

```
output1 [output2 …] : [input1 input2 …] {{
    … shell body …
}}
```

When you request an output, cgpipe checks whether it is missing or older than its
inputs and, if so, runs the body. Multiple outputs and inputs are allowed;
requesting any one output runs the rule once and produces all of them.

```
sorted.bam: input.bam {{
    samtools sort -o ${output} ${input}
}}
```

Every example here is rendered with `cgpipe -dr` (dry run), which shows the assembled
script without running it. The outputs shown are real fixture output from
[`tests/build/`](../tests/build/).

## The body is a shell template

Inside `{{ }}` the content is **raw shell**, captured verbatim and rendered at
job time. cgpipe does not parse the shell. Only three things are recognized during
the render pass:

1. `${…}` substitution (and `${if …}`, `@{…}`),
2. an optional leading **directive block** (see below),
3. `%`-prefixed cgpipe control lines (see below).

Everything else passes through. Because the body is shell — not a cgpipe string —
its escaping is different from string literals: **only `\$` and `\@` are special**
(they suppress the cgpipe sigils). Every other backslash is shell text, verbatim:

```
out.txt: {{
    echo "x\"y"
    echo back \\ slash
    echo "${host} \${HOME} $HOME"
    echo deferred \$(date)
}}
```

renders to:

```bash
echo "x\"y"
echo back \\ slash
echo "node1 ${HOME} $HOME"
echo deferred $(date)
```

`\${HOME}` and `\$HOME` keep their `$` for the shell; `\$(date)` defers the
command to job runtime instead of running at render.

## Build variables

Inside a body these stand for the target's own files:

| Form | Meaning |
|------|---------|
| `${input}`     | All inputs, space-joined |
| `${output}`    | All outputs, space-joined |
| `${stem}`      | The wildcard stem (see below) |
| `${input[0]}`  | First input (0-based) |
| `${output[1]}` | Second output |

There's no singular/plural distinction — `${input}` is "the inputs as a value,"
space-joined in a string, indexable with `[N]`, and usable as `for f in
${input}`. A two-output rule:

```
%.sorted.bam %.flagstat: %.bam {{
    samtools sort -o ${output[0]} ${input[0]}
    samtools flagstat ${output[0]} > ${output[1]}
    echo "outputs: ${output}"
}}
```

Building `sampleA.sorted.bam` renders:

```bash
samtools sort -o sampleA.sorted.bam sampleA.bam
samtools flagstat sampleA.sorted.bam > sampleA.flagstat
echo "outputs: sampleA.sorted.bam sampleA.flagstat"
```

## Directives and the `--` separator

A body may begin with a **directive block** that sets per-job settings, separated
from the shell by a line containing only `--`. Before `--` is cgpipe code; after `--`
is shell:

```
out.txt: in.txt {{
    job.procs = threads
    job.mem   = "16G"
    --
    sort --parallel=${job.procs} ${input} > ${output}
}}
```

Directive assignments don't emit shell — they configure the job (here `job.procs`
and `job.mem`, which a scheduler turns into CPU and memory requests). The rendered
shell is just:

```bash
sort --parallel=8 in.txt > out.txt
```

Per-job settings always carry the `job.` prefix — `job.mem`, `job.procs`,
`job.name`, and so on — both here in a directive block and when set globally before
your targets. Ordinary cgpipe control flow is allowed in the block too (it's cgpipe
mode). A setting captured here applies to *this* target; set it globally and it
becomes the default for every target defined after it (`job.procs` itself defaults
to 1).

> **`--` is the only thing that opens a directive block.** A body with **no `--`
> is entirely shell** — a line that merely looks like a directive (e.g.
> `job.mem = "16G"`) is passed through to the shell verbatim, not interpreted and
> not warned about. To set per-job settings, you must write the `--`.

The settings you can put here are the `job.*` surface — see
[Running Jobs](08-Running_Jobs.md) and the
[Configuration Reference](14-Configuration_Reference.md).

## Inline conditionals

`${if cond; then; else}` substitutes one fragment or the other; the else-clause is
optional (empty when false). It shines for optional flags. A nested double-quoted
string is fine inside a body's `${if …}`:

```
out.txt: {{
    annotate ${if basic; "--tab BASIC_GENE:${basic},4"} ${output}
}}
```

With `basic = "BRCA1"` this renders `annotate --tab BASIC_GENE:BRCA1,4 out.txt`.

## In-body control flow (`%` lines)

A line whose first non-space character is `%` is **cgpipe control flow inside the
body**. The shell lines between `% for … {` and `% }` are emitted once per
iteration, with `${…}` resolved each time:

```
out.txt: {{
% for p in parts {
    echo ${p} >> ${output}
% }
}}
```

With `parts = ["a", "b", "c"]`:

```bash
echo a >> out.txt
echo b >> out.txt
echo c >> out.txt
```

This is how you generate a variable number of shell lines *within a single job*.
(To generate a variable number of separate *jobs*, use a top-level `for` loop
around the target declaration — see [Tutorial 4](tutorials/04-map-reduce.md).)

## Snippets

A `snippet` is a reusable body fragment, spliced into a body with `@name`:

```
snippet common {{
    set -euo pipefail
    umask 077
}}

out.txt: in.txt {{
    @common
    wc -l ${input} > ${output}
}}
```

`@common` is replaced by the snippet's lines wherever it appears. Snippets share
*body* text; to share *targets and variables*, use `include`
([Tutorial 8](tutorials/08-include.md)).

## Wildcards

`%` in a declaration matches one or more characters; the stem is reused on the
input side and is available as `${stem}`:

```
%.sai: %.fastq {{
    bwa aln ref.fa ${input} > ${output}   # stem=${stem}
}}
```

Requesting `reads.sai` matches with `stem = reads` and uses `reads.fastq` as the
input. `%` is valid only in the declaration line.

## List expansion `@{list}`

`@{var}` expands a list into **separate declaration items** at parse time — one
rule listing every output and every input. Contrast `${var}`, which joins a list
into a single space-separated value inside a body.

```
samples = ["s1", "s2", "s3"]
@{samples}.done: @{samples}.bam {{
    touch ${output}
}}
```

renders one rule:

```bash
# ---- s1.done s2.done s3.done ----
touch s1.done s2.done s3.done
```

`@{1..N}` expands a range; `"prefix_@{list}_suffix"` expands inside a string.

## Multiple definitions for one output

The same output may be defined more than once with different inputs. cgpipe tries
each in source order and uses the **first whose inputs are all satisfiable**:

```
out.bam: reads.fastq {{ align ${input} > ${output} }}
out.bam: reads.sam   {{ samtools view -b ${input} > ${output} }}
```

If only `reads.sam` exists, the second definition wins. If none can be satisfied,
it's a "no build path" error.

## Temporary outputs (`^`)

Prefix an output with `^` to mark it **temporary** — an intermediate that exists
only to satisfy downstream rules:

```
^stage1.txt: src.txt {{ cp ${input} ${output} }}
final.txt:    stage1.txt {{ cp ${input} ${output} }}
```

A temp is special **only in how its absence is handled**:

- **Missing temp ⇒ transparent.** Its absence does not force its own rebuild;
  staleness looks *through* it to its inputs. So if `src.txt` is updated after
  `stage1.txt` was deleted, `final.txt` still rebuilds correctly.
- **Present temp ⇒ a normal file,** mtime-checked like any other output.

| On disk | Change | Decision |
|---------|--------|----------|
| A, C (B deleted) | A newer than C | look through missing B → C stale → rebuild B then C |
| A, B, C | B newer than C | B present → C stale → rebuild C |
| A, C (B deleted) | A older than C | look through missing B → C current → skip all |

**cgpipe never auto-deletes temp files.** `^` documents *why* a file was made, not
permission to remove it — deletion is always explicit (next section).

## Write atomically: temp file, then rename

cgpipe decides staleness from the filesystem — an output is current when it exists
and is newer than its inputs. It does **not** know whether the job that wrote it
*succeeded*; only the scheduler knows that. So if a job is killed, runs out of
disk, or crashes halfway, it can leave a **half-written output** that is newer
than its inputs. On the next run cgpipe sees a present, fresh-looking file and skips
the rebuild — silently propagating a truncated, corrupt result downstream.

The fix is a one-line discipline that costs nothing: **never write the final
output directly.** Write to a temp path, and only `mv` it into place once the
command has succeeded. Because a rename on the same filesystem is atomic, the
real output filename never exists in a partial state — it appears, complete, or
not at all:

```
%.md5: % {{
    md5sum ${input} > ${output}.tmp && mv ${output}.tmp ${output}
}}
```

The `&&` is load-bearing: if `md5sum` fails, the `mv` never runs, so `${output}`
stays absent and the target is correctly seen as still needing to be built.

This is a recommended idiom, **not** a built-in: cgpipe doesn't wrap your command in
an implicit temp-and-rename because correctness depends on details only you know —
that the tmp and final paths share a filesystem (a cross-device `mv` is a
non-atomic copy), and that a partial write is even meaningful for the format. Apply
it wherever a tool writes a single output by redirection; tools that already write
atomically, or that produce a directory of parts, may need a different guard.

Pair it with `^` for intermediates and with [opportunistic cleanup](#opportunistic-jobs)
to remove the temps once the final result lands.

## Opportunistic jobs

A target with **no outputs** — a leading `:` and a list of inputs — is
*opportunistic*. It runs after the rest of the pipeline is submitted, never forces
its inputs to be built, and runs only if every input is already available. If any
is missing and nothing will produce it, it's silently skipped. The canonical use
is **guarded cleanup** of temp files:

```
: final.vcf ^calls.1.vcf ^calls.2.vcf {{
    if [ -e final.vcf ]; then
        rm -v calls.1.vcf calls.2.vcf
    fi
}}
```

See [Tutorial 5](tutorials/05-opportunistic-cleanup.md) for the full pattern.

## Bodyless (aggregator) targets

A target may omit the `{{ }}` entirely. It then has no recipe and is a pure
**grouping rule** — a Make-style phony target whose name is never expected on disk:

```
all: a.txt b.txt
a.txt: {{ echo a > ${output} }}
b.txt: {{ echo b > ${output} }}
@default: all
```

Requesting `all` builds both files. `@default` (next chapter) is the special,
build-by-default form of this idea.

## Next

- **[Reserved Targets](06-Reserved_Targets.md)** — `@pre`/`@post` hooks and the
  `@default` goal.
- **[Tutorial 2: gzip with a wildcard](tutorials/02-gzip-wildcard.md)** — wildcards
  in practice.

Reference → [language-spec.md §6](language-spec.md#6-target-bodies),
[§7](language-spec.md#7-target-declaration-features).
