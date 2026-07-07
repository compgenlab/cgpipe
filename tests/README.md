# cgpipe language-spec fixture suite

Executable examples of every language feature. Each fixture is a real `.cgp`
script run through the `cgpipe` binary; its output is diffed against a checked-in
golden file. The point is that the language's surface syntax stays **visible and
verified** — read `lang/strings.cgp` next to `lang/strings.cgp.out` and you see
exactly what the feature does.

This complements `internal/spectest/` (Go unit tests over the parser/evaluator);
here the contract is end-to-end CLI behaviour.

## Running

```sh
make spec                 # build the host binary, run every fixture
make spec FLAGS=-v        # show a diff for each failure
make spec FLAGS=-u        # accept current output as the new golden
./tests/run.sh            # same, building cgpipe on the fly
./tests/run.sh tests/lang/strings.cgp   # one fixture
go test ./tests/          # the same suite as a Go test (CI path)
```

`go test ./...` runs the suite too, via `TestFixtures` in `tests/fixtures_test.go`.

## Layout

```
tests/
  run.sh              the harness (see its header for full option docs)
  lang/               stdout fixtures — print/expressions/methods/control flow
  build/              dry-run (-dr) fixtures — emitted shell for the build graph
  runners/<sched>/    scheduler fixtures — what cgpipe submits to slurm/sge/pbs/batchq
```

## Fixture formats

### stdout fixtures (`lang/`, `build/`)

Run `cgpipe [args] <file>`; compare against sibling golden files:

| file | required | meaning |
|------|----------|---------|
| `<name>.cgp`        | yes | the script |
| `<name>.cgp.out`    | yes | expected stdout |
| `<name>.cgp.args`   | no  | extra CLI args, one line, word-split |
| `<name>.cgp.rc`     | no  | expected exit code (default 0) |
| `<name>.cgp.err`    | no  | expected stderr (exact) |
| `<name>.cgp.env`    | no  | shell sourced before the run (e.g. `export CGP_ENV=...`) |
| `<name>.files/`     | no  | helper files copied into the run dir (sources, includes) |
| `<name>.setup.sh`   | no  | prep step run in the workdir before cgpipe (set input mtimes, seed a ledger); `$CGP` is available |

`lang/` scripts end with `exit` so execution stops before the runner and the
golden is just the `print` output. `build/` scripts pass `-dr` (via `.args`) so
the golden is the emitted bash script for the resolved build graph.

### runner fixtures (`runners/<scheduler>/`)

The harness puts mock scheduler binaries
(`internal/spectest/testdata/mocks/<scheduler>/`) on `PATH`, submits with
`-r <scheduler>`, and compares what cgpipe sent against `<name>.expected/`:

| file | meaning |
|------|---------|
| `stdout`        | cgpipe's stdout (the assigned job ids) |
| `rc`            | exit code — only present when non-zero |
| `submit-N.argv` | argv of the Nth submit call (one arg per line) |
| `submit-N.stdin`| the rendered job script piped to the Nth submit |
| `status-N.argv`, `release-N.argv`, … | other scheduler calls, in order |

Job ids are deterministic (`CGP_TEST_JOBID_BASE=1001`, incrementing). Optional
siblings: `<name>.cgp.args`, `<name>.cgp.env`, `<name>.setup.sh`, and
`<name>.responses/` (canned status responses keyed by job id, exposed as
`CGP_TEST_RESPONSES`). A `setup.sh` here runs with its own scratch capture dir,
so a first seeding `cgpipe` run (e.g. for the ledger-reuse fixture) doesn't pollute
the captures under test.

## Determinism

Each fixture runs in a fresh temp dir, so cgpipe output that embeds the absolute
working directory (container `-v`/`-w` bind mounts, some error messages) is
normalized to the token `__WORKDIR__` before comparison. Scheduler job ids are
pinned via the mocks. Where mtime ordering matters (staleness fixtures), the
`setup.sh` creates files in sequence rather than relying on a fixed clock.

## Adding a fixture

1. Write the `.cgp` (and any `.args`/`.files/`).
2. `./tests/run.sh tests/<dir>/<name>.cgp -u` to generate the golden.
3. **Read the generated golden and confirm it is actually correct** per
   `docs/language-spec.md` — `-u` blesses whatever the binary produced, so the
   review is what makes it a test. The spec is the design authority; on a genuine
   spec-vs-behaviour discrepancy, prefer fixing the spec/code, not rubber-stamping
   wrong output.
4. Commit the `.cgp` and its golden together.
