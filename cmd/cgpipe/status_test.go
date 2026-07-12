package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/compgenlab/cgpipe/internal/ledger"
)

// installScontrolShow puts a mock `scontrol` on a temp PATH whose
// `scontrol -o show job <id>` echoes the key=value line mapped for that id, and
// exits non-zero for an unknown id (as the real tool does once a job ages out).
func installScontrolShow(t *testing.T, lines map[string]string) {
	t.Helper()
	dir := t.TempDir()
	var cases strings.Builder
	for id, line := range lines {
		fmt.Fprintf(&cases, "    %s) echo %q ;;\n", id, line)
	}
	script := "#!/bin/bash\n[ \"$1\" = -o ] || exit 1\ncase \"$4\" in\n" +
		cases.String() + "    *) exit 1 ;;\nesac\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "scontrol"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// cgp status (table mode) lists each recorded job with its normalized scheduler
// state (queued/running/done/failed), and "unknown" once a job has aged out.
func TestStatusNormalizedStates(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	db := filepath.Join(dir, "l.db")
	lg, err := ledger.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	base := int64(1700000000)
	lg.Record(ledger.Job{JobID: "j-q", Name: "align", SubmitTime: base, Outputs: []string{"q.out"}})
	lg.Record(ledger.Job{JobID: "j-run", Name: "sort", SubmitTime: base + 1, Outputs: []string{"run.out"}})
	lg.Record(ledger.Job{JobID: "j-done", Name: "index", SubmitTime: base + 2, Outputs: []string{"done.out"}})
	lg.Record(ledger.Job{JobID: "j-fail", Name: "call", SubmitTime: base + 3, Outputs: []string{"fail.out"}})
	lg.Record(ledger.Job{JobID: "j-aged", Name: "old", SubmitTime: base + 4, Outputs: []string{"aged.out"}})
	lg.Close()

	installBatchqStatusLines(t, map[string]string{
		"j-q":    "j-q QUEUED",
		"j-run":  "j-run RUNNING",
		"j-done": "j-done SUCCESS",
		"j-fail": "j-fail FAILED",
		// j-aged: not reported => unknown (its output does not exist on disk)
	})

	out := captureStdout(t, func() int { return run([]string{"status", "-r", "batchq", db}) })
	for _, want := range []string{
		"j-q\tqueued\talign",
		"j-run\trunning\tsort",
		"j-done\tdone\tindex",
		"j-fail\tfailed\tcall",
		"j-aged\tunknown\told",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("status output missing %q:\n%s", want, out)
		}
	}
}

// cgp status --json emits a JSON array with the normalized enum and best-effort
// Tier-2 fields parsed from the scheduler probe.
func TestStatusJSON(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	db := filepath.Join(dir, "l.db")
	lg, err := ledger.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	base := int64(1700000000)
	lg.Record(ledger.Job{JobID: "1", Name: "align", Pipeline: "p.cgp", SubmitTime: base, Outputs: []string{"run.out"}})
	lg.Record(ledger.Job{JobID: "2", Name: "index", SubmitTime: base + 1, Outputs: []string{"done.out"}})
	lg.Record(ledger.Job{JobID: "3", Name: "call", SubmitTime: base + 2, Outputs: []string{"cancel.out"}})
	lg.Close()

	installScontrolShow(t, map[string]string{
		"1": "JobState=RUNNING Reason=None Partition=general NodeList=node01 NumCPUs=4 UserId=alice(1001) StartTime=2024-01-02T03:00:00",
		"2": "JobState=COMPLETED ExitCode=0:0 EndTime=2024-01-02T03:04:05 Partition=general",
		"3": "JobState=CANCELLED ExitCode=0:15",
	})

	out := captureStdout(t, func() int { return run([]string{"status", "--json", "-r", "slurm", db}) })
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	byID := map[string]map[string]any{}
	for _, r := range rows {
		byID[r["job_id"].(string)] = r
	}
	if len(byID) != 3 {
		t.Fatalf("want 3 jobs, got %d:\n%s", len(byID), out)
	}
	// Tier-1: normalized state + native word + provenance from the ledger.
	if s := byID["1"]["state"]; s != "running" {
		t.Errorf("job 1 state = %v, want running", s)
	}
	if s := byID["1"]["native_state"]; s != "RUNNING" {
		t.Errorf("job 1 native_state = %v, want RUNNING", s)
	}
	if s := byID["2"]["state"]; s != "done" {
		t.Errorf("job 2 state = %v, want done", s)
	}
	if s := byID["3"]["state"]; s != "cancelled" {
		t.Errorf("job 3 state = %v, want cancelled", s)
	}
	if p := byID["1"]["pipeline"]; p != "p.cgp" {
		t.Errorf("job 1 pipeline = %v, want p.cgp (from ledger)", p)
	}
	// Tier-2: parsed from scontrol.
	if p := byID["1"]["partition"]; p != "general" {
		t.Errorf("job 1 partition = %v, want general", p)
	}
	if n := byID["1"]["nodes"]; n != "node01" {
		t.Errorf("job 1 nodes = %v, want node01", n)
	}
	if u := byID["1"]["user"]; u != "alice" { // stripped of "(1001)"
		t.Errorf("job 1 user = %v, want alice", u)
	}
	if ec, ok := byID["2"]["exit_code"]; !ok || ec.(float64) != 0 {
		t.Errorf("job 2 exit_code = %v (present=%v), want 0", ec, ok)
	}
	if et := byID["2"]["end_time"]; et == nil || et == "" {
		t.Errorf("job 2 end_time missing, want an RFC3339 timestamp")
	}
}

// With no JOBID, cgp status reports the CURRENT owner of each output (last-write-
// wins), not the full job history: a failed job whose output was reproduced by a
// later successful job is not reported.
func TestStatusOwnersNotHistory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	db := filepath.Join(dir, "l.db")
	lg, err := ledger.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	base := int64(1700000000)
	// Both jobs produce shared.out; the resubmit (j-ok) is recorded last, so it
	// owns the output.
	lg.Record(ledger.Job{JobID: "j-old", Name: "first-try", SubmitTime: base, Outputs: []string{"shared.out"}})
	lg.Record(ledger.Job{JobID: "j-ok", Name: "resubmit", SubmitTime: base + 1, Outputs: []string{"shared.out"}})
	lg.Close()

	installBatchqStatusLines(t, map[string]string{
		"j-old": "j-old FAILED",
		"j-ok":  "j-ok RUNNING",
	})

	out := captureStdout(t, func() int { return run([]string{"status", "-r", "batchq", db}) })
	if !strings.Contains(out, "j-ok\trunning") {
		t.Errorf("current owner j-ok missing:\n%s", out)
	}
	if strings.Contains(out, "j-old") {
		t.Errorf("superseded job j-old should not be reported:\n%s", out)
	}
}

// The ledger dir may be given via -l (alias of --ledger) as well as positionally.
func TestStatusLedgerFlagAlias(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	db := filepath.Join(dir, "l.db")
	lg, err := ledger.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	lg.Record(ledger.Job{JobID: "j-run", Name: "sort", Outputs: []string{"run.out"}})
	lg.Close()
	installBatchqStatusLines(t, map[string]string{"j-run": "j-run RUNNING"})

	out := captureStdout(t, func() int { return run([]string{"status", "-r", "batchq", "-l", db}) })
	if !strings.Contains(out, "j-run\trunning\tsort") {
		t.Errorf("-l alias did not resolve ledger dir:\n%s", out)
	}
}

// A non-scheduler runner has no state probe, so `cgp status` rejects it.
func TestStatusRejectsShell(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "l.db")
	lg, err := ledger.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	lg.Record(ledger.Job{JobID: "1", Outputs: []string{"o"}})
	lg.Close()
	if code := run([]string{"status", "-r", "shell", db}); code != 2 {
		t.Errorf("cgp status -r shell = %d, want 2", code)
	}
}
