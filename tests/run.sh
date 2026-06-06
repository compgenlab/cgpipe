#!/usr/bin/env bash
#
# cgp language-spec fixture suite.
#
# Runs real .cgp scripts through the cgp binary and diffs the result against
# checked-in golden files, so the language's surface syntax stays visible and
# every feature has an executable example. There are two kinds of fixture:
#
#   stdout fixtures  (tests/lang/**.cgp, tests/build/**.cgp)
#       Run `cgp [args] <file>`; compare combined behaviour against:
#           <file>.out   expected stdout            (required)
#           <file>.args  extra CLI args, one line   (optional; word-split)
#           <file>.rc    expected exit code         (optional; default 0)
#           <file>.err   expected stderr            (optional; exact)
#           <file>.env   shell sourced before run   (optional; e.g. CGP_ENV=...)
#       lang/  fixtures end with `exit` and assert on print output.
#       build/ fixtures pass `-dr` (in .args) and assert on the emitted script.
#
#   runner fixtures  (tests/runners/<scheduler>/**.cgp)
#       Submit to a mock scheduler (sbatch/qsub/...) and compare what cgp sent.
#       <file>.expected/ holds the golden capture:
#           stdout                  cgp's stdout (the job ids)            (required)
#           rc                      exit code, only when non-zero        (optional)
#           submit-N.argv           argv of the Nth submit call
#           submit-N.stdin          rendered script piped to the Nth submit
#           status-N.argv, release-N.argv, ...
#       Optional siblings: <file>.args, <file>.env, <file>.responses/ (canned
#       status responses, exposed as CGP_TEST_RESPONSES).
#
# Usage:
#   tests/run.sh                 # build cgp, run every fixture
#   tests/run.sh <file.cgp> ...  # run only the named fixture(s)
#   tests/run.sh -v              # show a diff for every failure
#   tests/run.sh -u              # update golden files from actual output
#   tests/run.sh -k              # keep per-fixture temp dirs (debug)
#
# Env:
#   CGP_BIN   use this prebuilt binary instead of `go build`ing one.
#
set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
MOCKS="$ROOT/internal/spectest/testdata/mocks"

VERBOSE=0
UPDATE=0
KEEP=0
declare -a SELECT=()

for arg in "$@"; do
    case "$arg" in
        -v|--verbose) VERBOSE=1 ;;
        -u|--update)  UPDATE=1 ;;
        -k|--keep)    KEEP=1 ;;
        -h|--help)    sed -n '2,40p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
        -*)           echo "run.sh: unknown option $arg" >&2; exit 2 ;;
        *)            SELECT+=("$arg") ;;
    esac
done

# ---- locate / build the binary -------------------------------------------
if [ -n "${CGP_BIN:-}" ]; then
    CGP="$CGP_BIN"
    [ -x "$CGP" ] || { echo "run.sh: CGP_BIN=$CGP is not executable" >&2; exit 2; }
else
    CGP="$(mktemp -d)/cgp"
    echo "building cgp ..."
    if ! ( cd "$ROOT" && GOWORK=off GOTOOLCHAIN=auto CGO_ENABLED=0 \
            go build -o "$CGP" ./cmd/cgp ); then
        echo "run.sh: build failed" >&2
        exit 2
    fi
fi

PASS=0
FAIL=0
declare -a FAILED=()

# diff_or_update <golden> <actual> <label> -> 0 ok, 1 mismatch
diff_or_update() {
    local golden="$1" actual="$2" label="$3"
    if [ "$UPDATE" = 1 ]; then
        if [ ! -f "$golden" ] || ! cmp -s "$golden" "$actual"; then
            cp "$actual" "$golden"
            echo "  updated $label"
        fi
        return 0
    fi
    if [ ! -f "$golden" ]; then
        echo "  MISSING golden: $label" >&2
        [ "$VERBOSE" = 1 ] && { echo "  --- actual ---"; sed 's/^/  | /' "$actual"; }
        return 1
    fi
    if cmp -s "$golden" "$actual"; then
        return 0
    fi
    if [ "$VERBOSE" = 1 ]; then
        echo "  diff $label (expected < / > actual):"
        diff "$golden" "$actual" | sed 's/^/  /'
    fi
    return 1
}

# read a one-line .args sidecar into the WORDS array (word-split, no quoting)
read_args() {
    WORDS=()
    [ -f "$1" ] || return 0
    local line
    IFS= read -r line < "$1"
    # shellcheck disable=SC2206
    WORDS=($line)
}

# norm_paths <workdir> <file...> — replace the ephemeral temp workdir path with a
# stable token. cgp embeds the absolute cwd in some output (container bind/-w
# mounts, error messages), which would otherwise differ on every run.
norm_paths() {
    local wd="$1"; shift
    local wdreal; wdreal="$(cd "$wd" 2>/dev/null && pwd -P || echo "$wd")"
    local f
    for f in "$@"; do
        [ -f "$f" ] || continue
        sed -i.bak -e "s#${wdreal}#__WORKDIR__#g" -e "s#${wd}#__WORKDIR__#g" "$f"
        rm -f "$f.bak"
    done
}

run_stdout_fixture() {
    local cgp_file="$1"
    # absolutize: sidecars are referenced from inside the per-fixture workdir
    cgp_file="$(cd "$(dirname "$cgp_file")" && pwd)/$(basename "$cgp_file")"
    local name; name="$(basename "$cgp_file")"
    local out_golden="${cgp_file}.out"
    local err_golden="${cgp_file}.err"
    local rc_file="${cgp_file}.rc"
    local env_file="${cgp_file}.env"
    local setup_file="${cgp_file%.cgp}.setup.sh"
    local want_rc=0
    [ -f "$rc_file" ] && IFS= read -r want_rc < "$rc_file"

    read_args "${cgp_file}.args"
    local -a args=(${WORDS[@]+"${WORDS[@]}"})

    local work; work="$(mktemp -d)"
    cp "$cgp_file" "$work/$name"
    # copy a <name>.files/ helper dir (includes, fixtures) if present
    [ -d "${cgp_file%.cgp}.files" ] && cp -R "${cgp_file%.cgp}.files/." "$work/"

    local rc
    (
        cd "$work" || exit 99
        export CGP
        # shellcheck disable=SC1090
        [ -f "$env_file" ] && . "$env_file"
        # optional prep (create inputs with set mtimes, seed a ledger, ...)
        [ -f "$setup_file" ] && bash "$setup_file"
        "$CGP" ${args[@]+"${args[@]}"} "$name" >stdout.actual 2>stderr.actual
        echo $? >rc.actual
    )
    IFS= read -r rc < "$work/rc.actual"
    norm_paths "$work" "$work/stdout.actual" "$work/stderr.actual"

    local ok=1
    diff_or_update "$out_golden" "$work/stdout.actual" "$name (stdout)" || ok=0
    if [ -f "$err_golden" ] || { [ "$UPDATE" = 1 ] && [ -s "$work/stderr.actual" ]; }; then
        diff_or_update "$err_golden" "$work/stderr.actual" "$name (stderr)" || ok=0
    fi
    if [ "$UPDATE" = 1 ]; then
        if [ "$rc" != 0 ]; then echo "$rc" >"$rc_file"; elif [ -f "$rc_file" ]; then rm -f "$rc_file"; fi
    elif [ "$rc" != "$want_rc" ]; then
        echo "  exit code: expected $want_rc, got $rc" >&2
        [ "$VERBOSE" = 1 ] && sed 's/^/  stderr| /' "$work/stderr.actual"
        ok=0
    fi

    [ "$KEEP" = 1 ] && echo "  kept $work" || rm -rf "$work"
    return $((1 - ok))
}

run_runner_fixture() {
    local cgp_file="$1"
    # absolutize: sidecars are referenced from inside the per-fixture workdir
    cgp_file="$(cd "$(dirname "$cgp_file")" && pwd)/$(basename "$cgp_file")"
    local name; name="$(basename "$cgp_file")"
    local sched; sched="$(basename "$(dirname "$cgp_file")")"
    local exp="${cgp_file%.cgp}.expected"
    local env_file="${cgp_file}.env"
    local resp_dir="${cgp_file%.cgp}.responses"
    local setup_file="${cgp_file%.cgp}.setup.sh"

    if [ ! -d "$MOCKS/$sched" ]; then
        echo "  no mocks for scheduler '$sched'" >&2
        return 1
    fi

    read_args "${cgp_file}.args"
    local -a extra=(${WORDS[@]+"${WORDS[@]}"})

    local work; work="$(mktemp -d)"
    local capture="$work/capture" bindir="$work/bin"
    mkdir -p "$capture" "$bindir"
    cp "$MOCKS/lib.sh" "$work/lib.sh"
    cp "$MOCKS/$sched"/* "$bindir/" && chmod +x "$bindir"/*
    cp "$cgp_file" "$work/$name"
    # copy a <name>.files/ helper dir (e.g. a custom submission template) if present
    [ -d "${cgp_file%.cgp}.files" ] && cp -R "${cgp_file%.cgp}.files/." "$work/"

    local rc
    (
        cd "$work" || exit 99
        export CGP
        export PATH="$bindir:$PATH"
        export CGP_TEST_CAPTURE="$capture"
        export CGP_TEST_JOBID_BASE=1001
        [ -d "$resp_dir" ] && export CGP_TEST_RESPONSES="$resp_dir"
        # shellcheck disable=SC1090
        [ -f "$env_file" ] && . "$env_file"
        # optional prep — e.g. a first `cgp` run to seed the ledger, or files
        # with set mtimes. Its scheduler calls go to a scratch capture dir so
        # they don't pollute the captures under test.
        [ -f "$setup_file" ] && CGP_TEST_CAPTURE="$work/setup-capture" bash "$setup_file"
        "$CGP" -r "$sched" ${extra[@]+"${extra[@]}"} "$name" >stdout.actual 2>stderr.actual
        echo $? >rc.actual
    )
    IFS= read -r rc < "$work/rc.actual"
    norm_paths "$work" "$work/stdout.actual" "$work/stderr.actual"
    local cf
    for cf in "$capture"/*; do
        [ -f "$cf" ] || continue
        case "$(basename "$cf")" in .seq.*|.jobid.seq) continue ;; esac
        norm_paths "$work" "$cf"
    done

    local ok=1
    if [ "$UPDATE" = 1 ]; then
        mkdir -p "$exp"
        # drop any captures that no longer occur, then refresh
        find "$exp" -mindepth 1 -maxdepth 1 ! -name 'stdout' ! -name 'stderr' ! -name 'rc' -exec rm -f {} +
        cp "$work/stdout.actual" "$exp/stdout"
        if [ -s "$work/stderr.actual" ]; then cp "$work/stderr.actual" "$exp/stderr"; else rm -f "$exp/stderr"; fi
        if [ "$rc" != 0 ]; then echo "$rc" >"$exp/rc"; else rm -f "$exp/rc"; fi
        local f
        for f in "$capture"/*; do
            [ -e "$f" ] || continue
            case "$(basename "$f")" in .seq.*|.jobid.seq) continue ;; esac
            cp "$f" "$exp/$(basename "$f")"
        done
        echo "  updated $sched/$name expected/"
    else
        if [ ! -d "$exp" ]; then
            echo "  MISSING golden dir: $sched/$name" >&2
            ok=0
        else
            local g bn target
            for g in "$exp"/*; do
                [ -e "$g" ] || continue
                bn="$(basename "$g")"
                case "$bn" in
                    stdout) target="$work/stdout.actual" ;;
                    stderr) target="$work/stderr.actual" ;;
                    rc)     target="$work/rc.actual" ;;
                    *)      target="$capture/$bn" ;;
                esac
                if [ ! -f "$target" ]; then
                    echo "  missing capture for $sched/$name: $bn" >&2
                    ok=0
                    continue
                fi
                diff_or_update "$g" "$target" "$sched/$name:$bn" || ok=0
            done
            # default rc assertion when no golden rc is recorded
            if [ ! -f "$exp/rc" ] && [ "$rc" != 0 ]; then
                echo "  $sched/$name exited $rc (expected 0)" >&2
                [ "$VERBOSE" = 1 ] && sed 's/^/  stderr| /' "$work/stderr.actual"
                ok=0
            fi
            # flag captures that were produced but not expected
            local c
            for c in "$capture"/*; do
                [ -e "$c" ] || continue
                bn="$(basename "$c")"
                case "$bn" in .seq.*|.jobid.seq) continue ;; esac
                if [ ! -e "$exp/$bn" ]; then
                    echo "  unexpected capture in $sched/$name: $bn" >&2
                    ok=0
                fi
            done
        fi
    fi

    [ "$KEEP" = 1 ] && echo "  kept $work" || rm -rf "$work"
    return $((1 - ok))
}

run_one() {
    local f="$1"
    local label="${f#"$ROOT"/}"
    case "$f" in
        */runners/*) run_runner_fixture "$f" ;;
        *)           run_stdout_fixture "$f" ;;
    esac
    if [ $? -eq 0 ]; then
        PASS=$((PASS + 1))
        [ "$UPDATE" = 1 ] || echo "ok   $label"
    else
        FAIL=$((FAIL + 1))
        FAILED+=("$label")
        echo "FAIL $label"
    fi
}

# ---- collect fixtures -----------------------------------------------------
declare -a FIXTURES=()
if [ "${#SELECT[@]}" -gt 0 ]; then
    FIXTURES=("${SELECT[@]}")
else
    while IFS= read -r f; do FIXTURES+=("$f"); done < <(
        find "$SCRIPT_DIR/lang" "$SCRIPT_DIR/build" "$SCRIPT_DIR/runners" \
            -type d -name '*.files' -prune -o \
            -name '*.cgp' -type f -print 2>/dev/null | sort
    )
fi

if [ "${#FIXTURES[@]}" -eq 0 ]; then
    echo "no fixtures found" >&2
    exit 1
fi

for f in "${FIXTURES[@]}"; do
    [ -f "$f" ] || { echo "FAIL (not found) $f"; FAIL=$((FAIL + 1)); FAILED+=("$f"); continue; }
    run_one "$f"
done

echo
echo "----------------------------------------"
echo "passed: $PASS   failed: $FAIL"
if [ "$FAIL" -gt 0 ]; then
    printf '  %s\n' "${FAILED[@]}"
    exit 1
fi
exit 0
