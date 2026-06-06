# cgp

`cgp` is a small language and runner for generating and submitting job scripts —
the Go rewrite of [cgpipe](https://github.com/compgen-io/cgpipe-jvm). It keeps
cgpipe's spirit (shebang scripts, output-first targets, a tiny shell-friendly
DSL, a persistent job ledger) while shipping as a single static binary with no
JVM, fast startup, and an SQLite-backed ledger designed for fast restarts at
scale.

> **Status: early development.** The language is specified in
> [`docs/language-spec.md`](docs/language-spec.md). `cgp` runs pipelines with the
> local shell or a batch scheduler — the full language, dependency resolution
> (mtime staleness with temp look-through), the SLURM/SGE/PBS/BatchQ runners, and
> the optional SQLite ledger (cross-run reuse of still-queued jobs) are
> implemented, plus `cgp sub` for one-off job submission, config-file loading,
> container/GPU wrapping, `-manifest*` fan-out (run a pipeline once per manifest
> row/file), and multi-pipeline `stage` composition (chain standalone pipelines
> via `export` / `${stage.x}`). `cgp convert` migrates a legacy cgpipe script to
> the cgp language. The JVM version remains available and supported at
> `compgen-io/cgpipe-jvm`.

```sh
cgp pipeline.cgp                 # build @default (or the first target)
cgp pipeline.cgp out.bam         # build a specific target
cgp pipeline.cgp --sample p42    # set a pipeline variable
cgp pipeline.cgp -dr             # dry run: print the rendered shell scripts
cgp convert old.cgp -o new.cgp   # migrate a legacy cgpipe script
```

## Install

cgp is a single static binary. Download the tarball for your platform from the
[Releases page](https://github.com/compgen-io/cgp/releases)
(`cgp-<version>-<os>-<arch>.tar.gz`), extract it, and put `cgp` on your `PATH`. See
[Getting Started](docs/02-Getting_Started.md#install) for details.

## Documentation

The full guide lives in **[`docs/`](docs/)** — start with
[Getting Started](docs/02-Getting_Started.md), browse the
[hub](docs/README.md), or skim [cgp in one page](docs/cgp-for-llms.md). The
precise language reference is [`docs/language-spec.md`](docs/language-spec.md).

## Build from source

Pure Go, no CGO:

```sh
go build ./...
go test ./...
go build -o bin/cgp ./cmd/cgp
```

Cross-compilation is a plain `GOOS`/`GOARCH` build:

```sh
GOOS=linux GOARCH=arm64 go build -o bin/cgp-linux-arm64 ./cmd/cgp
```

## Layout

| Path | Role |
|------|------|
| `cmd/cgp/`        | the `cgp` binary (run a pipeline; `cgp sub` submits one-off jobs) |
| `internal/token/` | lexical tokens + source positions |
| `internal/lexer/` | source → tokens (incl. the `{{ }}` shell-body capture mode) |
| `internal/ast/`   | AST node types |
| `internal/parser/`| hand-rolled recursive-descent parser |
| `internal/eval/`  | evaluator: scope, control flow, target collection → dependency graph; renders shell bodies (`${…}`, `%`-control lines) |
| `internal/runner/` | drive a graph to a backend; `runner/shell` (default), `runner/sched` (slurm/sge/pbs/batchq), `runner/graphviz`, `runner/report` (html) |
| `internal/ledger/` | optional SQLite job ledger (output → owning job) |
| `internal/container/` | container/GPU command wrapping (docker/singularity) |
| `internal/manifest/` | manifest loaders (tsv/csv/json/cgp) for fan-out |
| `internal/convert/` | best-effort migrator from legacy cgpipe scripts |
| `internal/lsp/`   | zero-dependency language server (`cgp lsp`) for editors |
| `docs/`           | the user guide and the language specification |

## Examples

Runnable, self-contained pipelines live in [`examples/`](examples/) — from a
one-line hello to scatter-gather, manifest fan-out, and stage workflows. They use
only coreutils, so they run anywhere; `examples/check.sh` runs them all.

## License

MIT — see [LICENSE](LICENSE).
