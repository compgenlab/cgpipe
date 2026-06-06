# Glossary

Terms used throughout the cgp documentation. Each links to where it's covered in
depth.

**Aggregator (bodyless target).** A target with no `{{ }}` body — a phony grouping
rule whose name is never a file on disk. Requesting it builds its inputs.
[Build Targets](05-Build_Targets.md#bodyless-aggregator-targets).

**Body.** The shell template inside a target's `{{ }}` — raw shell with `${…}`
substitution, captured verbatim and rendered at job time.
[Build Targets](05-Build_Targets.md#the-body-is-a-shell-template).

**Build variable.** `${input}`, `${output}`, `${stem}` (and indexed forms) — stand
for a target's own files inside its body.
[Build Targets](05-Build_Targets.md#build-variables).

**Default goal.** `@default` — the target(s) built when no target is named on the
command line; falls back to the first defined target.
[Reserved Targets](06-Reserved_Targets.md#the-default-goal-default).

**Directive.** A per-job setting (`mem`, `procs`, `name`, …) assigned in a body's
**directive block**, before the `--` separator.
[Build Targets](05-Build_Targets.md#directives-and-the----separator).

**Directive block.** The optional leading section of a body, before `--`, written
in cgp code. No `--` means no directive block — the whole body is shell.

**Dry run.** `-dr` — render the scripts a real run would produce, without executing
or submitting. [Running Jobs](08-Running_Jobs.md#dry-runs).

**Export.** `export name = expr` — a stage pipeline exposing a value to its
workflow as `${stage.name}`; a no-op when the pipeline runs standalone.
[Workflows](11-Workflows.md#exposing-values-with-export).

**Fan-out.** Running one pipeline once per row of a manifest (across a cohort).
[Manifests and Fan-out](12-Manifests_and_Fanout.md).

**Goal.** A target requested to be built — on the command line, or via `@default`.

**Ledger.** The optional SQLite database recording which job owns (last produced)
which output. Enables cross-run reuse; stores no mtimes and no job state.
[The Ledger](10-The_Ledger.md).

**Manifest.** A table (TSV/CSV/JSON) or glob of `.cgp` files whose rows each supply
variables for one run of a pipeline.
[Manifests and Fan-out](12-Manifests_and_Fanout.md).

**Opportunistic job.** A target with no outputs (`: inputs`) that runs only if its
inputs already exist, never forcing them — the pattern for guarded cleanup.
[Build Targets](05-Build_Targets.md#opportunistic-jobs).

**Owner.** The job that last produced a given output path, per the ledger.
[The Ledger](10-The_Ledger.md#what-it-does-and-doesnt-track).

**Pipeline.** A `.cgp` file describing a dependency graph of targets. (A file with
`stage` statements is a *workflow* instead.)

**Reserved target.** A target whose name begins with `@` (`@pre`, `@default`, …) —
built into cgp and never a file on disk. [Reserved Targets](06-Reserved_Targets.md).

**Runner.** The backend that carries out a pipeline: `shell`, `slurm`, `sge`, `pbs`,
`batchq`, `graphviz`, or `html`. Chosen with `-r` or `cgp.runner`.
[Running Jobs](08-Running_Jobs.md#runners).

**Shexec.** `shexec = true` — run a body directly on the submit host instead of
submitting it (for `@setup`/`@teardown`).
[Reserved Targets](06-Reserved_Targets.md#setup-and-teardown).

**Snippet.** A reusable body fragment defined with `snippet name {{ }}` and spliced
into bodies with `@name`. [Build Targets](05-Build_Targets.md#snippets).

**Stage.** One pipeline within a workflow, declared with `stage NAME FILE ARGS…`.
[Workflows](11-Workflows.md#declaring-stages).

**Staleness.** Whether an output needs rebuilding — true if it's missing or older
than an input. Decided by file timestamps, not the ledger.
[The Ledger](10-The_Ledger.md#restart-is-timestamp-based).

**Stem.** The text a wildcard `%` matched, available as `${stem}`.
[Build Targets](05-Build_Targets.md#wildcards).

**Target.** The unit of work: outputs, the inputs they depend on, and a shell body
that builds them. [Build Targets](05-Build_Targets.md).

**Temporary output (`^`).** An intermediate output marked `^` — when absent,
staleness looks *through* it to its inputs. cgp never auto-deletes it.
[Build Targets](05-Build_Targets.md#temporary-outputs-).

**Wildcard.** `%` in a declaration, matching a stem reused on the input side.
[Build Targets](05-Build_Targets.md#wildcards).

**Workflow.** A `.cgp` file that chains pipelines with `stage` statements, threading
exported values between them. [Workflows](11-Workflows.md).
