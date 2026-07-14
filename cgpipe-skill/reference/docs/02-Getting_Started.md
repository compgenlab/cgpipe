# Getting Started

This chapter makes the case for cgpipe and then walks you from an empty directory to
a working pipeline. If you just want the language reference, that lives in
[`language-spec.md`](language-spec.md); everything here is the friendly version.

> Throughout the docs, when an example disagrees with a test fixture under
> [`tests/`](../tests/), the fixture is correct — it is run on every build.

---

## Why cgpipe?

cgpipe is a small language for **generating and submitting job scripts**. You
describe the files you want and the shell commands that produce them; cgpipe works
out what needs to run, in what order, and hands each job to a backend — your
local shell, or a cluster scheduler (SLURM, SGE, PBS, BatchQ).

The pitch, in five points:

- **Output-first, like `make`.** You write `output: inputs` rules. cgpipe builds a
  dependency graph, skips work whose outputs are already up to date, and runs the
  rest in order.
- **The body is just shell.** No new execution model to learn — a target body is
  ordinary bash with `${...}` substitution. What you read is what runs.
- **One script, any backend.** The *same* pipeline runs locally or submits to a
  scheduler by changing a single setting. You don't rewrite anything to move from
  your laptop to a cluster.
- **Fan out without a DSL.** Read a sample sheet in-language with
  `open("samples.tsv").read_tsv()` and loop over its rows, or generate
  per-chromosome jobs with an ordinary `for` loop. No channels, no operators.
- **A single static binary.** `cgp` is one file with no runtime to install. Fast
  startup, and an optional *ledger* that makes restarts cheap at scale.

### The same script, two backends

Here is a one-target pipeline that aligns reads:

```
#!/usr/bin/env cgp
#
# Align reads to a reference.

aligned.bam: ${reads} ${ref} {{
    job.procs = 4
    --
    bwa mem -t ${job.procs} ${ref} ${reads} | samtools sort -o ${output}
}}

@default: aligned.bam
```

Run it with the default (shell) runner and cgpipe prints a bash script:

```console
$ cgp -dr align.cgp --reads reads.fq --ref ref.fa
#!/usr/bin/env bash
set -euo pipefail

# ---- aligned.bam ----
bwa mem -t 4 ref.fa reads.fq | samtools sort -o aligned.bam
```

Add `-r slurm` — *nothing else changes* — and the very same target becomes an
`sbatch` submission, with `job.procs = 4` mapped to the scheduler's CPU request:

```console
$ cgp -dr -r slurm align.cgp --reads reads.fq --ref ref.fa
# [dryrun.1] aligned.bam
#!/bin/bash
#SBATCH -J aligned.bam
#SBATCH -c 4
#SBATCH -n 1

set -eo pipefail
bwa mem -t 4 ref.fa reads.fq | samtools sort -o aligned.bam
```

That portability — write once, run locally or on any supported scheduler — is the
core of what cgpipe gives you.

---

## How to use cgpipe

### Install

cgpipe is a single static binary with no runtime to install — no Python, no JVM, no
shared libraries.

**Download a release (recommended).** Grab the binary for your platform from the
project's [Releases page](https://github.com/compgenlab/cgpipe/releases) — they're
named `cgp-<version>-<os>-<arch>` (e.g. `cgp-v0.1.0-linux-amd64`,
`cgp-v0.1.0-darwin-arm64` for Apple Silicon). It's a single uncompressed binary;
make it executable and put it on your `PATH`:

```sh
chmod +x cgp-v0.1.0-linux-amd64
install cgp-v0.1.0-linux-amd64 ~/bin/cgp     # somewhere on your PATH
cgp version
```

**Build from source.** If you have Go (1.25+), build it directly — it's pure Go, so
no C toolchain or CGO is involved:

```sh
go build -o ~/bin/cgp ./cmd/cgpipe
```

Cross-compiling for another platform is a plain `GOOS`/`GOARCH` build:

```sh
GOOS=linux GOARCH=arm64 go build -o cgpipe-linux-arm64 ./cmd/cgpipe
```

(`make all` cross-builds every supported target into `bin/cgp.<os>_<arch>`.)

### Write your first pipeline

A pipeline is a `.cgp` file. Make one called `copy.cgp`:

```
#!/usr/bin/env cgp
#
# Copy a file. (Replace this with real work.)

out.txt: in.txt {{
    cp ${input} ${output}
}}

@default: out.txt
```

Three things to notice:

- The leading `#!` line lets you `chmod +x copy.cgp && ./copy.cgp` and run the
  pipeline as a script.
- The comment block right after the shebang is the script's **help text** (see
  below). Writing it is a good habit — it documents the pipeline's inputs for the
  next person, including you.
- `out.txt: in.txt {{ ... }}` is a **target**: it produces `out.txt` from
  `in.txt`. Inside the `{{ }}` body, `${input}` and `${output}` stand for the
  declared files. The body is plain shell.
- `@default: out.txt` names the target to build when you don't ask for a specific
  one. (Without it, cgpipe builds the first target defined.)

### Run it

Create the input, then run cgpipe. The default runner is the local shell, which
**prints** the assembled script to stdout — it does not execute it:

```console
$ echo hello > in.txt
$ cgp copy.cgp
#!/usr/bin/env bash
set -euo pipefail

# ---- out.txt ----
cp in.txt out.txt
```

To actually run the work, pipe it to a shell:

```sh
cgp copy.cgp | bash
```

(If you would rather cgpipe execute directly instead of printing, set
`cgp.runner.shell.autoexec = true` in the script or your config.)

### Preview without running: `-dr`

`-dr` ("dry run") renders the scripts for whatever a *real* run would do, so you
can read exactly what will execute before committing to it. It works for every
runner — under `-r slurm` it shows you the submission scripts instead of
submitting them.

```sh
cgp -dr copy.cgp
```

> **One sharp edge:** cgpipe's own `$(...)` command substitution is evaluated *while
> rendering* — so it runs even under `-dr`. Use `\$(...)` to defer a command to
> the job's shell. See [Troubleshooting](17-Troubleshooting.md).

### Pass variables

Anything not hard-coded in the script can come from the command line as a
**double-hyphen** variable:

```sh
cgp align.cgp --reads sample.fq --ref hg38.fa
```

Inside the script, `--reads sample.fq` sets the variable `reads`. A few rules
worth knowing up front:

- A bare flag sets a boolean: `--verbose` makes `verbose` true.
- Hyphens become underscores: `--hp-dist 0.1` sets `hp_dist`.
- A repeated flag builds a list: `--x a --x b` gives `x = ["a", "b"]`.

Guard required variables at the top of the script so a missing one fails loudly:

```
if !reads { print "ERROR: --reads required"; exit 1 }
```

### Choose a runner

`-r NAME` selects the backend (or set `cgp.runner` in the script/config):

| Runner | What it does |
|--------|--------------|
| `shell` (default) | Assemble one bash script (printed, or executed with autoexec) |
| `slurm`, `sge`, `pbs`, `batchq` | Submit each job to that scheduler, wiring dependencies |
| `graphviz` | Emit the dependency graph as DOT (`cgp -r graphviz … | dot -Tsvg`) |
| `html` | A self-contained HTML status report of the pipeline |

### See the help text

Because you wrote a help block, `-h` *after the filename* prints it:

```console
$ cgp copy.cgp -h
Copy a file. (Replace this with real work.)
```

(`cgp -h` on its own — before any file — prints cgpipe's own usage instead.)

---

## Configuration in one paragraph

cgpipe reads layered config before your pipeline: a `.cgprc` next to the binary,
`/etc/cgp/config`, then `~/.cgp/config`, then the `CGP_ENV` environment variable —
each one itself a cgpipe script, later layers winning. This is where you set
site-wide defaults like `cgp.runner = "slurm"` or a scheduler account, so your
pipelines stay portable and your cluster details stay out of them. The full list
of settings is in the [Configuration Reference](14-Configuration_Reference.md).

---

## Editor support

A [VSCode extension](../editor/vscode/) gives `.cgp` files syntax highlighting out
of the box (a TextMate grammar — no setup beyond installing it). When the `cgp`
binary is on your `PATH`, the extension also starts cgpipe's built-in language server
(`cgp lsp`) for parse-error diagnostics, hover, completion, and semantic
highlighting. The server speaks the standard [Language Server
Protocol](https://microsoft.github.io/language-server-protocol/), so any
LSP-capable editor can launch `cgp lsp` over stdio. See
[`editor/vscode/README.md`](../editor/vscode/README.md) for installation.

---

## Where next

- **[Tutorial 1: Hello, target](tutorials/01-hello.md)** — the smallest real
  pipeline, step by step.
- **[Language Syntax](03-Language_Syntax.md)** — types, variables, control flow.
- **[Build Targets](05-Build_Targets.md)** — wildcards, temp files, dependencies,
  the things that make targets powerful.
- **[Running Jobs](08-Running_Jobs.md)** — schedulers, resources, `cgp sub`.

Reference → [language specification](language-spec.md).
