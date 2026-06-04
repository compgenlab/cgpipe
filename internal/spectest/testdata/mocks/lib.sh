# Shared helpers for the mock scheduler binaries used by the cgp spec suite.
#
# Each mock sources this file and uses capture_call to record what cgp sent it
# (argv, and — for submit — the rendered script on stdin). Tests then read the
# captured files to assert on directives and dependency wiring.
#
# Env (set by the Go harness, installMocks):
#   CGP_TEST_CAPTURE     directory where capture files are written (absolute)
#   CGP_TEST_JOBID_BASE  starting job id (default 1001)
#   CGP_TEST_RESPONSES   optional dir of canned status responses, keyed by jobid
set -u

if [ -z "${CGP_TEST_CAPTURE:-}" ]; then
    echo "lib.sh: CGP_TEST_CAPTURE is unset" >&2
    exit 2
fi
mkdir -p "$CGP_TEST_CAPTURE"

# capture_call <kind> "$@" — record one call. kind ∈ submit|status|release.
# Writes <kind>-<N>.argv (one arg per line) and, for submit, <kind>-<N>.stdin.
capture_call() {
    kind="$1"; shift
    seq_file="${CGP_TEST_CAPTURE}/.seq.${kind}"
    n=$(( $(cat "$seq_file" 2>/dev/null || echo 0) + 1 ))
    echo "$n" > "$seq_file"
    LAST_SEQ="$n"
    stem="${CGP_TEST_CAPTURE}/${kind}-${n}"
    {
        printf '%s\n' "${MOCK_NAME:-$(basename "$0")}"
        for a in "$@"; do printf '%s\n' "$a"; done
    } > "${stem}.argv"
    if [ "$kind" = "submit" ]; then
        cat > "${stem}.stdin"
    fi
}

# next_jobid — emit a deterministic, monotonically increasing job id. The
# MOCK_JOBID_FORMAT printf string lets PBS emit a dotted "<id>.cluster1".
next_jobid() {
    f="${CGP_TEST_CAPTURE}/.jobid.seq"
    base="${CGP_TEST_JOBID_BASE:-1001}"
    if [ -s "$f" ]; then n=$(( $(cat "$f") + 1 )); else n=$base; fi
    echo "$n" > "$f"
    # shellcheck disable=SC2059
    printf "${MOCK_JOBID_FORMAT:-%d}" "$n"
}

# serve_response <tool> <jobid> — print a canned status response if one exists;
# return non-zero (job unknown) otherwise. Absent CGP_TEST_RESPONSES ⇒ every job
# is reported active (exit 0) so cross-run reuse paths can be exercised simply.
serve_response() {
    tool="$1"; jobid="$2"
    if [ -z "${CGP_TEST_RESPONSES:-}" ]; then
        return 0
    fi
    path="${CGP_TEST_RESPONSES}/${tool}/${jobid}"
    if [ -f "$path" ]; then cat "$path"; return 0; fi
    return 1
}
