package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/compgen-io/cgp/internal/ledger"
)

// installBatchqStatusLines puts a mock `batchq` on a temp PATH whose
// `status [-flags] <id>` echoes the line mapped for that id (already including the
// id, status word, and any trailing time fields), or nothing for an unknown id —
// like the real tool, it exits 0 either way. It tolerates a leading flag arg
// (e.g. `-e`) so both `batchq status <id>` and `batchq status -e <id>` resolve.
func installBatchqStatusLines(t *testing.T, lines map[string]string) {
	t.Helper()
	dir := t.TempDir()
	var cases strings.Builder
	for id, line := range lines {
		fmt.Fprintf(&cases, "    %s) echo %q ;;\n", id, line)
	}
	script := "#!/bin/bash\n[ \"$1\" = status ] || exit 0\nshift\n" +
		"case \"$1\" in -*) shift ;; esac\ncase \"$1\" in\n" +
		cases.String() + "esac\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "batchq"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// §15.2 cgp ledger status (job mode) shows each job's native scheduler status,
// UNKNOWN once a job has aged out.
func TestLedgerStatusJobMode(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	db := filepath.Join(dir, "l.db")
	lg, err := ledger.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	base := int64(1700000000)
	lg.Record(ledger.Job{JobID: "j-run", Name: "align", SubmitTime: base, Outputs: []string{"run.out"}})
	lg.Record(ledger.Job{JobID: "j-proxy", Name: "sort", SubmitTime: base + 1, Outputs: []string{"proxy.out"}})
	lg.Record(ledger.Job{JobID: "j-done", Name: "index", SubmitTime: base + 2, Outputs: []string{"done.out"}})
	lg.Record(ledger.Job{JobID: "j-aged", Name: "old", SubmitTime: base + 3, Outputs: []string{"aged.out"}})
	lg.Close()

	installBatchqStatusLines(t, map[string]string{
		"j-run":   "j-run RUNNING",
		"j-proxy": "j-proxy PROXYQUEUED",
		"j-done":  "j-done SUCCESS",
		// j-aged: not reported => UNKNOWN
	})

	out := captureStdout(t, func() int { return run([]string{"ledger", "status", "-r", "batchq", db}) })
	for _, want := range []string{
		"j-run\tRUNNING\talign",
		"j-proxy\tPROXYQUEUED\tsort",
		"j-done\tSUCCESS\tindex",
		"j-aged\tUNKNOWN\told",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("job-mode output missing %q:\n%s", want, out)
		}
	}
}

// §15.2 cgp ledger status -output cross-checks each output's mtime against its
// owning job's submit/end window: COMPLETE for an aged-out job with a good file,
// DIRTY for a missing/stale/too-new file, the native word otherwise.
func TestLedgerStatusOutputMode(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	db := filepath.Join(dir, "l.db")
	lg, err := ledger.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	base := int64(1700000000)
	end := base + 200
	jobs := []struct {
		id, out string
		submit  int64
	}{
		{"j-run", "run.out", base},         // still running
		{"j-good", "good.out", base},       // SUCCESS, fresh file -> SUCCESS
		{"j-stale", "stale.out", base},     // SUCCESS, file older than submit -> DIRTY
		{"j-missing", "missing.out", base}, // SUCCESS, no file -> DIRTY
		{"j-aged", "aged.out", base},       // aged out, fresh file -> COMPLETE
		{"j-agedmiss", "gone.out", base},   // aged out, no file -> DIRTY
		{"j-toonew", "toonew.out", base},   // SUCCESS w/ end time, file modified well after end -> DIRTY
		{"j-inwin", "inwin.out", base},     // SUCCESS w/ end time, file within end+5min -> SUCCESS
	}
	for _, j := range jobs {
		lg.Record(ledger.Job{JobID: j.id, Name: j.id, SubmitTime: j.submit, Outputs: []string{j.out}})
	}
	lg.Close()

	// Create output files with controlled mtimes (seconds resolution).
	mk := func(name string, mtime int64) {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		ts := time.Unix(mtime, 0)
		if err := os.Chtimes(p, ts, ts); err != nil {
			t.Fatal(err)
		}
	}
	mk("good.out", base+100)
	mk("stale.out", base-100)
	mk("aged.out", base+100)
	mk("toonew.out", end+600) // > 5 min after end
	mk("inwin.out", end+100)  // within 5 min of end
	// missing.out, gone.out, run.out intentionally absent

	// batchq status -e appends the end time as an RFC3339 UTC timestamp.
	endStr := time.Unix(end, 0).UTC().Format(time.RFC3339)
	installBatchqStatusLines(t, map[string]string{
		"j-run":     "j-run RUNNING",
		"j-good":    "j-good SUCCESS",
		"j-stale":   "j-stale SUCCESS",
		"j-missing": "j-missing SUCCESS",
		// j-aged, j-agedmiss: unknown => aged out
		"j-toonew": "j-toonew SUCCESS " + endStr,
		"j-inwin":  "j-inwin SUCCESS " + endStr,
	})

	out := captureStdout(t, func() int { return run([]string{"ledger", "status", "-r", "batchq", "-output", db}) })
	for _, want := range []string{
		"run.out\tj-run\tRUNNING",
		"good.out\tj-good\tSUCCESS",
		"stale.out\tj-stale\tDIRTY",
		"missing.out\tj-missing\tDIRTY",
		"aged.out\tj-aged\tCOMPLETE",
		"gone.out\tj-agedmiss\tDIRTY",
		"toonew.out\tj-toonew\tDIRTY",
		"inwin.out\tj-inwin\tSUCCESS",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output-mode result missing %q:\n%s", want, out)
		}
	}
}

// A non-scheduler runner has nothing to query, so `ledger status` rejects it.
func TestLedgerStatusRejectsShell(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "l.db")
	lg, err := ledger.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	lg.Record(ledger.Job{JobID: "1", Outputs: []string{"o"}})
	lg.Close()
	if code := run([]string{"ledger", "status", "-r", "shell", db}); code != 2 {
		t.Errorf("ledger status -r shell = %d, want 2", code)
	}
}
