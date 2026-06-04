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
> implemented, plus `cgp sub` for one-off job submission. Workflow composition
> (`stage`/`--manifest`) is not done yet. The JVM version remains available and
> supported at `compgen-io/cgpipe-jvm`.

```sh
cgp pipeline.cgp                 # build @default (or the first target)
cgp pipeline.cgp out.bam         # build a specific target
cgp pipeline.cgp -sample p42     # set a pipeline variable
cgp pipeline.cgp -dr             # dry run: print the rendered shell scripts
```

## Build

```sh
go build ./...
go test ./...
go build -o bin/cgp ./cmd/cgp
```

Cross-compilation is a plain `GOOS`/`GOARCH` build (no CGO):

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
| `internal/eval/`  | evaluator: scope, control flow, target collection → dependency graph |
| `internal/template/` | renders captured shell bodies (`${…}`, `%`-control lines) |
| `internal/runner/` | submit a graph to a backend; `runner/shell` is the default |
| `internal/ledger/` | optional SQLite job ledger (output → owning job) |
| `docs/`           | language specification and (later) the docs site |

## License

MIT — see [LICENSE](LICENSE).
