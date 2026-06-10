package spectest

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/compgen-io/cgp/internal/ledger"
)

// §10.3 Ownership is last-job-wins (UPSERT), and vacuum keeps every job that
// still owns a path while dropping the rest.
func TestLedgerOwnershipAndVacuum(t *testing.T) {
	path := filepath.Join(t.TempDir(), "l.db")
	lg, err := ledger.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()

	mustRecord(t, lg, ledger.Job{JobID: "1", Outputs: []string{"x.bam"}})
	mustRecord(t, lg, ledger.Job{JobID: "2", Outputs: []string{"x.bam"}}) // re-produces x.bam: 2 now owns it
	mustRecord(t, lg, ledger.Job{JobID: "3", Outputs: []string{"y.bam"}})

	if owner := ownerOf(t, lg, "x.bam"); owner != "2" {
		t.Errorf("owner of x.bam = %q, want 2 (last writer wins)", owner)
	}

	// job 1 owns nothing now ⇒ vacuum drops it, keeps 2 and 3.
	if err := lg.Vacuum(); err != nil {
		t.Fatal(err)
	}
	if n, _ := lg.CountJobs(); n != 2 {
		t.Errorf("after vacuum: %d jobs, want 2 (the two current owners)", n)
	}
	if owner := ownerOf(t, lg, "x.bam"); owner != "2" {
		t.Errorf("after vacuum, owner of x.bam = %q, want 2", owner)
	}
	if owner := ownerOf(t, lg, "y.bam"); owner != "3" {
		t.Errorf("after vacuum, owner of y.bam = %q, want 3", owner)
	}
}

// §10 The JSONL ledger takes no cross-process lock, so multiple writers can open
// the same directory at once.
func TestLedgerNoLock(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ledger")
	lg, err := ledger.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	lg2, err := ledger.Open(dir) // no lock to contend with
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	lg2.Close()
	lg.Close()
}

// §10.4 Restart is mtime-based: an output current relative to its inputs is not
// rebuilt on a second run.
func TestRestartSkipsUpToDate(t *testing.T) {
	chdirTmp(t)
	writeFile(t, "in.txt", "data")
	touch(t, "in.txt", -10*time.Second)
	src := `out.txt: in.txt {{
    cat ${input} > ${output}
    echo built >> build.log
}}
@default: out.txt`
	runReal(t, src, "out.txt")
	runReal(t, src, "out.txt") // out.txt is now newer than in.txt ⇒ skip
	if got := readFile(t, "build.log"); got != "built\n" {
		t.Errorf("build.log = %q, want a single build (up-to-date output should be skipped)", got)
	}
}

// §10.4 A changed input re-triggers the build on the next run.
func TestRestartRebuildsOnChangedInput(t *testing.T) {
	chdirTmp(t)
	writeFile(t, "in.txt", "data")
	touch(t, "in.txt", -10*time.Second)
	src := `out.txt: in.txt {{
    cat ${input} > ${output}
    echo built >> build.log
}}
@default: out.txt`
	runReal(t, src, "out.txt")
	// touch the input newer than the output ⇒ stale ⇒ rebuild
	touch(t, "in.txt", 10*time.Second)
	runReal(t, src, "out.txt")
	if got := readFile(t, "build.log"); got != "built\nbuilt\n" {
		t.Errorf("build.log = %q, want two builds (changed input re-triggers)", got)
	}
}

// §10.4 -force rebuilds even an up-to-date output.
func TestForceRebuilds(t *testing.T) {
	chdirTmp(t)
	writeFile(t, "in.txt", "data")
	touch(t, "in.txt", -10*time.Second)
	src := `out.txt: in.txt {{
    cat ${input} > ${output}
    echo built >> build.log
}}
@default: out.txt`
	prog, _ := build(t, src, nil)
	// first run builds
	runForce(t, prog, false)
	// second run, up-to-date: a normal run skips, but -force rebuilds
	prog2, _ := build(t, src, nil)
	runForce(t, prog2, true)
	if got := readFile(t, "build.log"); got != "built\nbuilt\n" {
		t.Errorf("build.log = %q, want two builds (-force rebuilds up-to-date output)", got)
	}
}

func mustRecord(t *testing.T, lg *ledger.Ledger, j ledger.Job) {
	t.Helper()
	if err := lg.Record(j); err != nil {
		t.Fatalf("record %s: %v", j.JobID, err)
	}
}

func ownerOf(t *testing.T, lg *ledger.Ledger, path string) string {
	t.Helper()
	owner, _, err := lg.OwnerOf(path)
	if err != nil {
		t.Fatalf("ownerOf %s: %v", path, err)
	}
	return owner
}
