package sched

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// installBatchqJSON writes a mock `batchq` that answers `status --json <id>` with
// the JSON array mapped for that id (empty array for an unknown id), and answers
// the plain/porcelain `status <id>` with "<id> <word>" from statusWords. If
// rejectJSON is set, `--json` fails with an "unknown flag" stderr so the fallback
// path is exercised.
func installBatchqJSON(t *testing.T, jsonByID, statusWords map[string]string, rejectJSON bool) {
	t.Helper()
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "batchq"))
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(f, "#!/bin/bash")
	fmt.Fprintln(f, `[ "$1" = status ] || exit 0`)
	fmt.Fprintln(f, "shift")
	if rejectJSON {
		fmt.Fprintln(f, `if [ "$1" = --json ]; then echo "unknown flag: --json" >&2; exit 1; fi`)
	} else {
		fmt.Fprintln(f, `if [ "$1" = --json ]; then`)
		fmt.Fprintln(f, `  case "$2" in`)
		for id, js := range jsonByID {
			fmt.Fprintf(f, "    %s) cat <<'EOJ'\n%s\nEOJ\n    ;;\n", id, js)
		}
		fmt.Fprintln(f, `    *) echo "[]" ;;`)
		fmt.Fprintln(f, `  esac; exit 0; fi`)
	}
	// plain / --porcelain status fallback
	fmt.Fprintln(f, `case "$1" in --porcelain) shift ;; esac`)
	fmt.Fprintln(f, `case "$1" in`)
	for id, w := range statusWords {
		fmt.Fprintf(f, "  %s) echo \"%s %s\" ;;\n", id, id, w)
	}
	fmt.Fprintln(f, `esac`)
	fmt.Fprintln(f, "exit 0")
	f.Close()
	os.Chmod(filepath.Join(dir, "batchq"), 0o755)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestBatchqDetailJSON(t *testing.T) {
	batchqJSONUnsupported.Store(false)
	installBatchqJSON(t, map[string]string{
		"100": `[{"job_id":"100","status":"RUNNING","name":"align","submit_time":"2026-06-10T12:30:00Z","start_time":"2026-06-10T12:34:56Z","return_code":0,"running_details":{"host":"gpu-04","pid":"1234"},"details":{"procs":"8","mem":"16G","walltime":"24:00:00","wd":"/scratch/run","stdout":"/scratch/run/o.log","stderr":"/scratch/run/e.log"}}]`,
		"101": `[{"job_id":"101","status":"SUCCESS","return_code":0,"end_time":"2026-06-10T13:00:00Z"}]`,
		"102": `[{"job_id":"102","status":"FAILED","return_code":7}]`,
		"103": `[{"job_id":"103","status":"CANCELED","return_code":0}]`,
	}, nil, false)

	// Running job: native host mapped to nodes; times parsed; no exit code yet.
	d, ok := batchqDetail("100")
	if !ok {
		t.Fatal("100: expected ok")
	}
	if d.State != "running" || d.NativeState != "RUNNING" {
		t.Errorf("100 state=%q native=%q", d.State, d.NativeState)
	}
	if d.Nodes != "gpu-04" {
		t.Errorf("100 nodes=%q, want gpu-04 (from running_details.host)", d.Nodes)
	}
	// batchq's native detail keys (procs/mem/walltime/wd/stdout) map to cgp fields.
	if d.CPUs != "8" || d.MemReq != "16G" || d.TimeLimit != "24:00:00" {
		t.Errorf("100 cpus=%q mem=%q limit=%q, want 8/16G/24:00:00", d.CPUs, d.MemReq, d.TimeLimit)
	}
	if d.WorkDir != "/scratch/run" || d.StdoutPath != "/scratch/run/o.log" {
		t.Errorf("100 wd=%q stdout=%q", d.WorkDir, d.StdoutPath)
	}
	if d.StartTime == 0 || d.SubmitTime == 0 {
		t.Errorf("100 times not parsed: submit=%d start=%d", d.SubmitTime, d.StartTime)
	}
	if d.HasExit {
		t.Errorf("100 should have no exit code while running")
	}

	// Finished ok: exit code 0 present, end time parsed.
	d, _ = batchqDetail("101")
	if d.State != "done" || !d.HasExit || d.ExitCode != 0 || d.EndTime == 0 {
		t.Errorf("101 got state=%q exit=%d has=%v end=%d", d.State, d.ExitCode, d.HasExit, d.EndTime)
	}
	// Failed: exit code surfaced.
	d, _ = batchqDetail("102")
	if d.State != "failed" || !d.HasExit || d.ExitCode != 7 {
		t.Errorf("102 got state=%q exit=%d has=%v", d.State, d.ExitCode, d.HasExit)
	}
	// Canceled normalizes to the distinct "cancelled" state.
	d, _ = batchqDetail("103")
	if d.State != "cancelled" {
		t.Errorf("103 state=%q, want cancelled", d.State)
	}
	// Unknown id: empty array -> not ok.
	if _, ok := batchqDetail("999"); ok {
		t.Errorf("999 should be unknown")
	}
}

// When batchq lacks --json, batchqDetail falls back to the porcelain state word.
func TestBatchqDetailFallback(t *testing.T) {
	batchqJSONUnsupported.Store(false)
	installBatchqJSON(t, nil, map[string]string{"200": "RUNNING"}, true)
	d, ok := batchqDetail("200")
	if !ok || d.State != "running" || d.NativeState != "RUNNING" {
		t.Errorf("fallback got ok=%v state=%q native=%q", ok, d.State, d.NativeState)
	}
	if !batchqJSONUnsupported.Load() {
		t.Errorf("expected --json to be marked unsupported after the unknown-flag error")
	}
	batchqJSONUnsupported.Store(false) // don't leak to other tests
}
