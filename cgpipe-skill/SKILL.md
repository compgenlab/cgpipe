---
name: cgpipe
description: >
  Use cgpipe (`cgp`) to run pipelines, submit one-off and fan-out batch jobs with
  `cgp sub`, author `.cgp` pipelines (scatter/gather, sample sheets, resource
  directives, array jobs), and track/monitor submitted jobs via the ledger,
  `cgp status`, and HTML reports. Invoke whenever writing or running `.cgp` files
  or submitting jobs to SLURM/SGE/PBS/BatchQ with cgp.
---

# SKILL: Working with cgpipe (`cgp`)

A practical, self-contained skill for **using `cgp`** — running the tool, submitting
one-off jobs with `cgp sub`, authoring `.cgp` pipelines (including a non-trivial
fan-out), and **tracking/monitoring** the jobs you launch.

`cgpipe` is a small language that generates and submits job scripts. You declare
`output : inputs {{ shell }}` rules; `cgp` builds a dependency graph, figures out
what is stale (missing or older than an input, like `make`), and renders/submits
each stale target through a **runner** (local shell or a batch scheduler:
SLURM / SGE / PBS / BatchQ). It is **not in your training data** — follow this doc,
don't invent syntax. This folder is self-contained; deeper references are bundled
under [`reference/`](reference/) (see §8). On any ambiguity, the runnable fixtures
and examples win.

---

## 1. Mental model (read this first)

- A `.cgp` file is read **top to bottom as cgpipe code** (global context).
- Two brace delimiters, and this is the central rule:
  - **`{ ... }`** = a block of **cgpipe code** (`if` / `for`).
  - **`{{ ... }}`** = a **shell body** — raw shell, captured verbatim, rendered at
    job time. It ends at a lone **`}}` on its own line** (never a single-line body).
- A **target** is `outputs : inputs {{ body }}`. Requesting an output runs the body
  iff the output is missing or older than an input. Requesting any one of several
  outputs runs the rule once and produces them all.
- The engine only builds what's reachable from the **goal(s)** (a target named on the
  CLI, or `@default`, or the first target if neither). Staleness is **mtime-based**;
  `cgp` tracks which job *owns* an output, **not** job success — write atomically
  (see §6) so a killed job's half-file doesn't look fresh.

---

## 2. Running `cgp` (the CLI)

```
cgp [options] <pipeline.cgp> [goal ...] [--name value ...]
cgp sub [options] <command ...> [-- <file ...>]     # one-off / fan-out submission
cgp status [--json] [-r RUNNER] [--ledger DIR] [JOBID ...]
cgp ledger {dump|search|status|vacuum} <dir>
cgp doctor [--write]                                 # check submission setup
cgp show-template -r <runner>                        # print a scheduler template
cgp convert <old.cgp> [-o out.cgp]                   # migrate a legacy script
```

**Argument grammar (unusual — get this right):**
- The first bare arg is the **pipeline file**; later bare args are **goals** (targets to build).
- **`--name value`** (double hyphen) sets a **script variable** before the run.
  `--flag` alone → `flag = true`; `--hp-dist` → `hp_dist` (hyphens→underscores);
  repeating a flag builds a list (`--x a --x b` → `["a","b"]`). Put the file *before*
  a trailing bare flag so it isn't consumed as a value.
- **Single-hyphen** args are `cgp`'s own options:

| Option | Meaning |
|--------|---------|
| `-dr` | Dry run — render scripts, don't execute/submit. Your main preview tool. |
| `-force` | Rebuild every target in the goal graph, ignoring staleness. |
| `-r NAME` | Runner: `shell` (default), `slurm`, `sge`, `pbs`, `batchq`, `graphviz`, `html`. |
| `-debug N` | Trace to stderr, `N`=1–5 (higher = more: phases → DAG → submits/probes → interp). |
| `-h` | Help. With a pipeline file, prints *that script's* help text (its top comment block). |

**The default `shell` runner does not execute** — it assembles the stale targets into
one bash script (in dependency order) and writes it to **stdout**. You choose what to do:

```sh
cgp pipeline.cgp                       # print the assembled bash
cgp pipeline.cgp | bash                # ...and run it locally
cgp -dr pipeline.cgp                   # preview only (renders, runs no $() side effects that write)
cgp -r slurm pipeline.cgp              # submit stale targets to SLURM
cgp -dr -r slurm pipeline.cgp          # preview the sbatch scripts without submitting
cgp pipeline.cgp final.vcf             # build a specific goal
cgp pipeline.cgp --sample s1 --ref hg38.fa   # set script variables
```

Set `cgp.runner.shell.autoexec = true` (in `~/.cgp/config`) if you want the shell
runner to execute directly instead of printing.

> **Dry run is a preview the *user* reads, not just a self-check.** `-dr` renders the
> **exact** scripts that would be submitted — the per-target bash, or the full `sbatch`/
> `qsub`/BatchQ script with its `#SBATCH`/directive lines and dependency wiring — and
> writes them to stdout **without executing or submitting anything**. Before you submit
> on the user's behalf (any scheduler run, any `cgp sub`), run the same command with
> `-dr` first and **show the user the rendered output so they can see and approve what
> will be submitted**. Nothing reaches the scheduler until they've seen it. Prefer this
> over describing what a script *would* contain — let them read the real thing.

**One-time setup check:** `cgp doctor` reports the version, which config files were
found, the resolved runner/ledger, and which schedulers are usable on this machine.
`cgp doctor --write` appends `cgp.runner = "<name>"` to `~/.cgp/config` — but only
when exactly one scheduler is unambiguously detected. `cgp` never auto-selects a
scheduler at run time; setup is always explicit.

---

## 3. Submitting one-off jobs: `cgp sub`

`cgp sub` submits a single command (or a fan-out of them) as a job, using the same
runners, settings, and ledger as a pipeline. Everything from the first non-option
token until a bare `--` is the **command** (a shell body — `${input}`/`${output}`
substitute):

```sh
cgp sub -m 8G -o out.bam -i in.bam samtools sort -o ${output} ${input}
```

**Options:**

| Flag | Meaning |
|------|---------|
| `-n, --name` | Job name |
| `-m, --mem` | Memory (e.g. `8G`) |
| `-p, --procs` | CPU/threads |
| `-t, --walltime` | Wall-clock limit |
| `-o, --output PATH` | Declared output (repeatable) |
| `-i, --input PATH` | Declared input (repeatable) |
| `-d, --deps IDS` | Depend on existing job ids (comma-separated; repeatable) |
| `-a, --after PATH` | Depend on the active ledger owner of `PATH` (repeatable) |
| `-f, --files-from F` | Read fan-out files from `F`, one per line (`-` = stdin) |
| `-r, --runner` / `-l, --ledger` / `-dr` | Runner / ledger dir / dry run |
| `--account`, `--queue`, `--gpu`, `--mail` | Portable scheduler settings (→ each scheduler's flag) |
| `-c, --custom S` | Verbatim extra directive line (repeatable), e.g. `--custom '-A foo'` → `#SBATCH -A foo` |

**Fan-out — one job per file.** Files after `--` (or via `--files-from`) each submit
one independent job; `{}` placeholders expand against each file in the command, name,
and `-o`/`-i`/`-a`:

| Placeholder | Expands to |
|-------------|------------|
| `{}` `{^}` | full input path |
| `{@}` | basename (dir stripped) |
| `{^SUF}` / `{@SUF}` | full path / basename with trailing `SUF` removed |
| `{#}` | 1-based fan-out index |
| `{{}}` | a literal `{}` |

```sh
# One SLURM job per FASTQ, output named from the (suffix-stripped) basename:
cgp sub -r slurm -m 4G -o '{@.fastq.gz}.bam' \
    'bwa mem ref.fa {} > {@.fastq.gz}.bam' -- *.fastq.gz
```

**Array fan-out (`--array`).** Submit the fan-out as ONE scheduler job array instead
of N independent jobs (slurm / batchq / pbs; sge / shell fall back to one job per file).
The body becomes a `case` over the task-id variable, one branch per file:

```sh
cgp sub -r slurm --array -o 'qc/{@.fastq}.html' 'fastqc {} -o qc/' -- *.fastq
```

A fixed `-d`/`-a` applies to the whole array; a `{}`-expanded `--after` is rejected
(a single array submission carries one dependency directive). **Make-like skipping:**
a task whose `-o` output is already newer than its inputs is skipped (logged to
stderr), so a restart resubmits only the missing indices (`--array=1,3,6`). The same
skip rule applies to the plain per-file fan-out.

**Always dry-run first, and show the user the result.** Add `-dr` to render the exact
script/array that would be submitted; put that rendered output in front of the user so
they can review the command, resources, and directives *before* anything hits the
scheduler. Only drop `-dr` to actually submit once they've seen it.

---

## 4. Writing pipelines — the constructs you need

### Variables & CLI args
```
x = 4            # assign existing binding in scope, else create in current block
var x = 4        # declare in this scope (block-local; can shadow an outer x)
threads ?= 4     # set only if unset — the defaults idiom; respects CLI/env/config
outs += x        # append (promotes a scalar to a list)
```
Every `{ }` is a lexical scope; a bare `x =` inside a loop does **not** survive the
loop. To carry a value out, `var last` *before* the loop, then assign through it.
`job.*`/`cgp.*` settings assigned inside a block still take effect outside it.

Guard required CLI vars at the top (`!x` = "unset or false/empty"):
```
if !bam { print "ERROR: --bam required"; exit 1 }
```

### Targets & directives
```
out1 out2 : in1 in2 {{
    job.mem   = "8G"        # directive block: per-job settings, before the -- separator
    job.procs = threads
    --
    samtools sort -@ ${job.procs} -o ${output} ${input}   # shell body, after --
}}
```
- **Build vars in a body:** `${input}` `${output}` `${stem}` (all inputs/outputs,
  space-joined), plus `${input[0]}`, `${output[1]}` to index one.
- **`--` is mandatory to open a directive block.** With no `--`, the *whole* body is
  shell and a `job.mem = ...` line is passed through to the shell verbatim.
- Common `job.*` settings: `job.name`, `job.procs` (default 1), `job.mem`,
  `job.walltime`, `job.queue`, `job.account`, `job.gpu`, `job.mail`, `job.stdout`,
  `job.stderr`, `job.container`, `job.custom` (verbatim directive lines),
  `job.array` (see §5). A setting set globally becomes the default for later targets.
- Read settings back as `${job.procs}`. A **bare** `${procs}` is a *user* variable
  (and errors if unset) — `job.name` and a `--name` variable never collide.

### Substitution & optional flags (inside `"…"` and bodies)
```
${var}      # substitute; ERROR if unset; a list joins with spaces
${var?}     # "" when unset
${expr}     # any expression: ${input[0]}, ${name.basename()}, ${row["bam"]}
${if c; a; b}          # inline conditional (else optional) — great for optional flags
@{list}     # LIST EXPANSION: one copy per element — used in DECLARATIONS
$(cmd)      # runs at RENDER time (even under -dr); use \$(cmd) to defer to the job's shell
```
`@{list}` vs `${list}`: use `@{...}` on a **declaration line** to spread items into
separate outputs/inputs; use `${...}` / `${input}` in a **body** to get one
space-joined argument. This distinction is the #1 thing to get right in a fan-out.

Body escaping is *not* the string-literal rule: only `\$` and `\@` are special, so
`\${HOME}`, `\$HOME`, and `\$(cmd)` keep their `$` for the *job's* shell.

### Declaration features you'll reach for
- **Wildcard** `%` (declaration line only), stem reused on the input side as `${stem}`:
  ```
  %.gz: % {{ gzip -c ${input} > ${output}
  }}
  ```
- **Temp output** `^out` — an intermediate: when **absent** it doesn't force its own
  rebuild and staleness looks *through* it to its inputs; when present it's a normal
  file. `cgp` never auto-deletes it.
- **Opportunistic** `: in1 in2 {{ ... }}` (no outputs) — runs only if all inputs
  already exist; never forces them. Canonical use: guarded cleanup of temp files.
- **Bodyless aggregator** `all: a.txt b.txt` — a phony grouping target (no body).
- `@default: final.vcf report.html` — the goals built when no target is named.

### `%`-control lines (emit shell per iteration, inside a body)
A line whose first non-whitespace char is `%` is cgpipe code; everything else is shell:
```
: ${out} @{parts} {{
    if [ -e ${out} ]; then
% for p in parts {
        rm -f ${p}
% }
    fi
}}
```

### Sample sheets: scatter and gather in ONE pipeline
`open(path)` returns a `file` handle; a reader turns a sheet into data you loop over
at **eval time**, so the whole cohort is one graph (scatter *and* gather together):
```
samples = open("samples.tsv").read_tsv(header=true)   # list of maps, one per row
```
`read_tsv(...)`/`read_csv(...)` keyword args (defaults): `header=false, sep="\t",
comment="#", skip=0, raw=false`. With `header=true` each row is a **map** keyed by
column; fields keep their type and chain: `row["bam"].basename()`, `row["n"] + 1`.
Other readers: `read_json()`, `read_lines(...)`, `read()`. **Quoting gotcha:** inside
a `"…"` string, bind a column to a plain var first (`name = row["sample"]`); on a
target line or in a `{{ }}` body, `${row["col"]}` is fine as-is.

---

## 5. Worked example — a reasonably complex fan-out pipeline

Per-sample alignment scattered from a sample sheet, then **grouped by a column** and
gathered per group, then one final report over all groups. Shows: sample-sheet fan-out,
scatter→gather, group-by-column, temp intermediates, resource directives, atomic
writes, `@default`, and guarded cleanup. (Verified to render — dry-run it yourself
with a small sheet before trusting any generated pipeline.)

```
#!/usr/bin/env cgp
#
# Align each sample, merge BAMs per cohort group, then summarize all groups.
#
# Options:
#     --sheet FILE   TSV: columns sample, group, fastq   (required)
#     --ref FILE     reference FASTA                       (required)
#     --out DIR      output directory                      (default: results)

if !sheet { print "ERROR: --sheet required"; exit 1 }
if !ref   { print "ERROR: --ref required";   exit 1 }
out ?= "results"
threads ?= 8

samples = open(sheet).read_tsv(header=true)
groups  = {}          # group name -> list of per-sample BAMs (the gather inputs)

# --- SCATTER: one alignment job per row ---
for row in samples {
    name  = row["sample"]
    grp   = row["group"]
    bam   = "${out}/${name}.bam"
    groups[grp] += bam                       # bucket this BAM under its group

    ^${bam}: ${row["fastq"]} ${ref} {{        # ^ = temp intermediate (fed to the merge)
        job.name  = "align-${name}"
        job.procs = threads
        job.mem   = "16G"
        --
        bwa mem -t ${job.procs} ${ref} ${input[0]} \
            | samtools sort -@ ${job.procs} -o ${output}.tmp - \
            && mv ${output}.tmp ${output}      # atomic: final name never half-written
    }}
}

# --- GATHER per group: merge each group's BAMs ---
merged = []
for grp in groups {                           # iterating a map yields its keys
    mbam = "${out}/${grp}.merged.bam"
    merged += mbam
    ${mbam}: @{groups[grp]} {{                 # @{...}: depend on every BAM in the group
        job.name = "merge-${grp}"
        job.mem  = "8G"
        --
        samtools merge -f ${output}.tmp ${input} && mv ${output}.tmp ${output}
    }}
}

# --- FINAL: one summary over all merged group BAMs ---
${out}/summary.tsv: @{merged} {{
    job.name = "summarize"
    --
    printf 'group\treads\n' > ${output}.tmp
    for b in ${input} ; do
        printf '%s\t%s\n' "\$(basename \$b .merged.bam)" "\$(samtools view -c \$b)" >> ${output}.tmp
    done
    mv ${output}.tmp ${output}
}}
@default: ${out}/summary.tsv

# --- Guarded cleanup of the per-sample temp BAMs, only once summary exists ---
: ${out}/summary.tsv @{merged} {{
    if [ -e ${out}/summary.tsv ]; then
% for b in merged {
        rm -f ${b}
% }
    fi
}}
```

Key moves to reuse for any fan-out:
1. **Read the sheet once** into a list of maps; loop to emit one target per unit.
2. **Accumulate outputs into a list** (`groups[grp] += bam`, `merged += mbam`) as you
   scatter, so the gather can depend on `@{that_list}`.
3. **Bucket with a map of lists** to get a per-group gather (`for grp in groups`).
4. `@{list}` on declaration lines (spread), `${input}` in bodies (joined).
5. **Atomic writes** (`> ${output}.tmp && mv`) everywhere; `\$(...)` / `\$b` defer to
   the job's shell (unescaped `$(...)` would run at render time).

Preview it before running — and show the user: `cgp -dr pipeline.cgp --sheet
samples.tsv --ref hg38.fa` (local bash) or `cgp -dr -r slurm ... ` (the actual sbatch
scripts). The `-dr` output *is* what would be submitted, so surface it for the user to
approve before you drop `-dr` and submit. `-r graphviz` / `-r html` render the whole
cohort's DAG in one document.

---

## 6. Tracking & monitoring jobs

### Enable the ledger (do this for any scheduler run you want to track)
The **ledger** records *which job owns (last produced) which output* — it does **not**
store job state (the scheduler owns liveness) or file mtimes (the filesystem owns
staleness). It's a directory of append-only JSONL files, safe on NFS/Lustre, no lock.
Enable it with a directory path:

```
cgp.ledger = "jobs.ledger"          # in the pipeline, or ~/.cgp/config, or -l on the CLI
```

With a ledger + a scheduler runner, re-running a pipeline before it finishes is safe:
`cgp` looks up an output with no in-run producer that isn't on disk yet, and if its
owning job is still queued/running it wires the new work as an `afterok:<id>`
dependency instead of resubmitting. It's also how a later workflow **stage** waits on
files an earlier stage is still producing. (Restart itself is always mtime-based;
`-force` rebuilds everything.)

### Live status — `cgp status` (normalized, the one to script against)
Reports each job's **normalized** live state by probing the scheduler. Needs a
scheduler runner and a ledger dir.

```sh
cgp status -r slurm -l jobs.ledger                 # every job that currently owns an output
cgp status -r slurm -l jobs.ledger 1002 1003       # just these job ids (ledger optional)
cgp status -r slurm -l jobs.ledger --json          # JSON array, for a dashboard/monitor
```
- `state` ∈ `queued | running | done | failed | cancelled | unknown`. An aged-out job
  is reconciled from disk to `done` when every owned output exists and is new enough.
- Default output is a table `<jobid>\t<state>\t<name>`; `--json` emits one object per
  job with `job_id`, `name`, `state`, `native_state`, `reason`, `exit_code` (when
  finished), array `array_id`/`task_index`, plus best-effort detail the scheduler
  exposes (`start_time`, `elapsed`, `mem_used`, `stdout_path`, `outputs`, …).

### Raw scheduler words / auditing — `cgp ledger`
```sh
cgp ledger status -r slurm jobs.ledger             # native status per job (PENDING, qw, …)
cgp ledger status -r slurm -output jobs.ledger     # per OWNED OUTPUT: <output> <jobid> <STATUS>
cgp ledger dump   jobs.ledger                      # every recorded job as key/value TSV
cgp ledger search -o aligned.bam jobs.ledger       # jobs whose output/-i/-name/-g/-id matches
cgp ledger vacuum jobs.ledger                      # compact to snapshot.jsonl (run when quiet)
```
`-output` mode reconciles finished jobs against the file: `COMPLETE` (aged out, file
present and current) or `DIRTY` (missing/stale). `dump`/`search`/`status` open the
ledger **read-only**, so they're safe to run while the pipeline is in flight.

### A visual status page — `-r html`
```sh
cgp -r html pipeline.cgp > status.html             # self-contained page, reads ledger read-only
```
Each output is colored by status: *done* (on disk), *running*/*queued* (owning job
active per the ledger), *failed* (job ended without producing it), *pending*. Because
the cohort is one graph, this renders the whole scatter+gather in one document.

### Recording your own submission log — `@postsubmit`
Runs once per submitted job, on the submit host, right after submission; it sees the
job's `${input}`/`${output}`/`${stem}` plus **`${jobid}`** (the scheduler id):
```
@postsubmit {{
    echo "${output}	${jobid}" >> submissions.tsv
}}
```

### Quick monitoring loop
```sh
cgp -r slurm pipeline.cgp --sheet s.tsv --ref hg38.fa   # submit
watch -n 30 'cgp status -r slurm -l jobs.ledger'        # poll normalized state
# or, for a dashboard: cgp status -r slurm -l jobs.ledger --json | your-tool
```

---

## 7. Common mistakes (avoid when generating cgpipe)

- A `{{ }}` body **must end with `}}` on its own line** — no single-line bodies.
- To set `job.*` settings you **must** include the `--` separator; without it those
  lines are passed through as shell.
- `$(cmd)` runs at **render time** (even under `-dr`); use `\$(cmd)` to defer to the
  job's shell. Same for `\${VAR}` / `\$VAR`.
- `${var}` on an unset variable is an **error**; use `${var?}` for "" when unset.
- Use `@{list}` in **declarations** (separate items) vs `${list}`/`${input}` in
  **bodies** (space-joined).
- Reserved `@`-names and `%` wildcards never name real files; `%` is only valid on the
  declaration line.
- Bind a column to a plain var before using it inside a `"…"` string
  (`name = row["sample"]`); a nested `"` would close the string.
- **Write atomically** (`cmd > ${output}.tmp && mv ${output}.tmp ${output}`): `cgp`
  tracks mtime, not exit status, so a killed job's partial output looks fresh and
  won't rebuild.
- `open(...,"w")` writes happen at eval time and are **no-ops under `-dr`**; a file a
  *job consumes* should be a target body, not an eval-time write.

---

## 8. Bundled references (this folder)

Everything below ships inside this skill folder — no external lookups needed:

- [`reference/docs/cgpipe-for-llms.md`](reference/docs/cgpipe-for-llms.md) — dense single-page language reference.
- [`reference/docs/language-spec.md`](reference/docs/language-spec.md) — the normative spec (design authority).
- [`reference/docs/README.md`](reference/docs/README.md) — the friendly chapter guide (Running Jobs, Array
  Jobs, Sample Sheets, Containers/GPUs, The Ledger, Workflows, …), with numbered chapters alongside it.
- [`reference/docs/cookbook/`](reference/docs/cookbook/) — full worked bioinformatics pipelines
  (DNA-seq variant calling, RNA-seq, ChIP/ATAC peaks, cohort joint genotyping, FASTQ QC/trim, reference prep).
- [`reference/examples/`](reference/examples/) — small runnable pipelines, each with a README:
  `01-hello`, `02-batch-compress`, `03-scatter-gather`, `04-sample-sheet`, `05-stage-workflow`, `06-cluster-resources`.

On any ambiguity, the runnable examples and the `tests/` fixtures in the main repo are correct.
