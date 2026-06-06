# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`cgp` is a small interpreted language and runner for generating and submitting job scripts — the Go rewrite of [cgpipe-jvm](https://github.com/compgen-io/cgpipe-jvm). It reads a `.cgp` pipeline, builds a dependency graph of *outputs*, decides what is stale, and renders/submits each target through a backend (local shell or a batch scheduler). It ships as a single static binary (pure Go, no CGO) with an optional SQLite-backed job ledger for fast restarts at scale.

The language is specified in [`docs/language-spec.md`](docs/language-spec.md) — this is the design authority. **On a spec-vs-test discrepancy, default to the test being correct** (fix the spec, or fix the test first if the test itself is wrong). Behavior changes should update the spec in the same change.

## Commands

```sh
make                  # build host binary -> bin/cgp.<os>_<arch> (default goal)
make all              # cross-build all release targets (linux/macos × amd64/arm64)
make test             # go test ./...  (includes the fixture suite via TestFixtures)
make spec             # run the .cgp fixture suite against a freshly built host binary
make spec FLAGS=-v    # ...with a diff for every failure
make spec FLAGS=-u    # ...updating golden files from actual output
make run              # go run ./cmd/cgp
make clean
```

Lint (must pass in CI):

```sh
gofmt -l .            # must print nothing
go vet ./...
```

Run a single Go test: `go test ./internal/eval/ -run TestName`.

Run a single fixture: `tests/run.sh tests/build/simple/…​.cgp` (add `-v` for the diff, `-k` to keep the temp dir, `-u` to update goldens). `CGP_BIN=<path>` reuses a prebuilt binary instead of `go build`-ing one.

### Build gotchas

- **`GOWORK=off` is required** and the Makefile sets it. A parent `go.work` does not list this module, so plain `go build`/`go test` from a shell that inherits the workspace will break — run via `make`, or export `GOWORK=off` yourself.
- Binaries are labeled `macos`, not `darwin`. The Makefile normalizes `go env GOOS` (`darwin` → `macos`) so the host-binary target resolves on macOS; keep that mapping if you touch target names.
- CI (`.github/workflows/ci.yml`) runs `go build`, `go test`, and `make spec` on Linux (amd64/arm64) and macOS arm64, plus a gofmt/vet job.

## Testing model

Two layers, both run by `make test`:

- **Go unit tests** (`*_test.go`) — per-package.
- **Fixture suite** (`tests/run.sh`, wrapped by `internal/spectest` / `TestFixtures`) — real `.cgp` scripts run through the binary and diffed against checked-in golden files, so the language surface stays visible and every feature has an executable example. Three kinds:
  - `tests/lang/**` — scripts that `exit`; assert on `print` output (`.out`, optional `.args`/`.rc`/`.err`/`.env`).
  - `tests/build/**` — pass `-dr` and assert on the *rendered* shell script.
  - `tests/runners/<scheduler>/**` — submit to a **mock scheduler** (`internal/spectest/testdata/mocks/{slurm,sge,pbs,batchq}`) and compare the captured `submit-N.argv` / `submit-N.stdin` etc. against `<file>.expected/`.

When adding a language feature, add a fixture, not just a Go test.

## Architecture

The pipeline is a classic front-end → evaluator → backend flow. Source text flows through:

```
lexer → parser → ast → eval (→ Program: dependency graph) → runner.Build(Backend)
```

- **`internal/token`, `internal/lexer`** — tokens + source positions. The lexer has a special **capture mode** for `{{ … }}` shell bodies (raw text, not tokenized as cgp), terminated by a lone `}}`. The two block delimiters are the central lexical rule: `{ }` = cgp code (if/for), `{{ }}` = shell body.
- **`internal/parser`** — hand-rolled recursive-descent parser (keep it hand-rolled; keep error messages good). Produces `internal/ast`.
- **`internal/eval`** — walks the AST in *global* context: scope and assignment (`=`, `?=`, `+=`), control flow, expression/method evaluation, and **target collection**. `for` loops emit targets at eval time, so dynamic target generation happens here. Output is an `eval.Program` (the concrete dependency graph, plus vars, exports, stages). `body.go`/`interp.go` render shell bodies with `${…}` interpolation.
- **`internal/runner`** — `driver.go` is the shared engine: it resolves goals, computes staleness (mtime-based, with temp-output look-through), and in dependency order hands each stale target to a **`Backend`**, threading returned job ids as dependencies. Backends:
  - `runner/shell` — default; renders the graph to one bash script (a function per target) and optionally executes it.
  - `runner/sched` — the template-based scheduler backends (SLURM/SGE/PBS/BatchQ). Each `Scheduler` is a struct of submit command + template + dependency wiring; templates live in `runner/sched/templates`. A user can override just the template via `cgp.runner.<name>.template` or `~/.cgp/custom_template.cgp` (resolved in `newBackend`); `cgp show-template -r <name>` prints the built-in to scaffold one.
  - `runner/graphviz` — emits DOT; `runner/report` — HTML status report read from the ledger.
- **`internal/ledger`** — optional SQLite ledger (modernc.org/sqlite, no CGO). Records only **which job owns (last produced) which output**, plus inputs/edges. It stores **no job state** (the scheduler owns liveness) and **no file metadata** (the filesystem owns staleness). Core query: "who owns output X?" — used to wire dependencies on jobs from prior runs/earlier stages still in the queue. Single-writer per ledger (`lock.go`).

### Cross-cutting flows (start in `cmd/cgp/main.go`)

The CLI argument grammar is unusual and handled in `main.go` + `args.go`: single-hyphen `-dr/-force/-r/-manifest*` are **cgp options**; double-hyphen `--name value` are **script variables** (`-` → `_`; repeated flag builds a list; bare `--name` = `true`). The first bare arg is the pipeline file; later bare args are goals.

- **Config layering** (`loadConfigs`) — system `.cgprc` next to the binary, `/etc/cgp/config`, `~/.cgp/config`, then `CGP_ENV` / `CGP_RUN_ID`. Each layer is itself a cgp script, applied in priority order before the pipeline.
- **Manifest fan-out** (`-manifest*`) — run the pipeline once per row/file; columns/keys become variables (CLI vars override them). A shared stat cache stats common inputs once. graphviz/html runners emit one combined document instead of one per row.
- **Stage orchestration** (`orchestrate`) — a workflow whose `Program.Stages` is non-empty runs each stage as a standalone pipeline in declaration order, threading each stage's `export`s into later stages as `${name.export}`. `validateStageRefs` statically rejects a `${stage.x}` whose stage never exports `x`.

### Other packages

`internal/convert` (best-effort migrator from legacy cgpipe scripts; `cgp convert`), `internal/container` (container/GPU command wrapping), `internal/manifest` (tsv/csv/json/cgp loaders), `internal/lsp` (zero-dep language server, `cgp lsp`, used by the VSCode extension in `editor/vscode`), `internal/buildinfo` (version string, injected via `-ldflags` from `scripts/version.sh`).

## Conventions

- Standard library only where practical; the one external dependency is `modernc.org/sqlite` (pure Go) for the ledger.
- `cgp`'s own `$(…)` command substitution runs at *render* time (even under `-dr`); use `\$(…)` to defer to the job's shell.
