# Guidance for AI agents / LLMs

This repo contains **cgpipe**, a small language for generating and submitting job
scripts (a Go rewrite of cgpipe-jvm). cgpipe is new, so it is **not in your training
data** — do not guess its syntax from memory. Read the reference first.

## Writing cgpipe pipelines

To author or edit `.cgp` files, read **[`docs/cgpipe-for-llms.md`](docs/cgpipe-for-llms.md)**
— a dense, self-contained, single-page reference covering every construct with
small examples. It fits comfortably in context and is enough to write valid cgpipe.

The authority, if you need more depth, is **[`docs/language-spec.md`](docs/language-spec.md)**;
the friendly guide is **[`docs/README.md`](docs/README.md)**. Executable, golden
examples of every feature live under **`tests/`** — when anything is ambiguous, the
fixtures are correct.

Common pitfalls to avoid when generating cgpipe:
- A `{{ }}` shell body must end with `}}` on its own line (no single-line bodies).
- Per-job settings require the `--` separator; without it the lines are shell.
- `$(cmd)` runs at render time; use `\$(cmd)` to defer to the job's shell.
- Use `@{list}` in declarations (separate items) vs `${...}` in bodies (joined).

## Working on the cgpipe codebase

See **[`CLAUDE.md`](CLAUDE.md)** for build/test commands, the `GOWORK=off` and
`darwin`→`macos` build gotchas, the fixture-suite testing model, and the
lexer→parser→eval→runner architecture.
