#!/usr/bin/env bash
#
# Smoke-test every example: run it and assert it produced output. Keeps the
# examples from bit-rotting. Uses only coreutils + gzip, so it runs anywhere.
#
#   examples/check.sh            # build cgp on the fly, run all examples
#   CGP=/path/to/cgp examples/check.sh   # use a prebuilt binary
#
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$HERE/.." && pwd)"
CGP="${CGP:-$ROOT/bin/cgp}"
if [ ! -x "$CGP" ]; then
    echo "building cgp ..."
    ( cd "$ROOT" && GOWORK=off go build -o bin/cgp ./cmd/cgp )
    CGP="$ROOT/bin/cgp"
fi
# Absolutize: each example runs from its own temp dir, so a relative path breaks.
case "$CGP" in /*) ;; *) CGP="$(cd "$(dirname "$CGP")" && pwd)/$(basename "$CGP")" ;; esac

fail=0
# Each entry: "<dir>|<cgp args>|<output file to check>"
run() {
    local dir="$1" args="$2" out="$3"
    local work; work="$(mktemp -d)"
    cp -R "$HERE/$dir/." "$work/"
    ( cd "$work" && CGP_ENV='cgp.runner.shell.autoexec = true' "$CGP" $args ) >/dev/null 2>&1
    if [ -s "$work/$out" ]; then
        echo "ok    $dir -> $out"
    else
        echo "FAIL  $dir -> $out (missing or empty)"
        fail=1
    fi
    rm -rf "$work"
}

run 01-hello            "pipeline.cgp"                       hello.txt
run 02-batch-compress   "pipeline.cgp"                       data/a.txt.gz
run 03-scatter-gather   "pipeline.cgp"                       total.txt
run 04-manifest-cohort  "pipeline.cgp -manifest-tsv samples.tsv" s2.sum
run 05-stage-workflow   "workflow.cgp --raw data/raw.txt"    summary.txt
run 06-cluster-resources "pipeline.cgp"                      result.txt

echo "----------------------------------------"
if [ "$fail" -eq 0 ]; then echo "all examples ran"; else echo "some examples failed"; exit 1; fi
