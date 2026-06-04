package ledger

import (
	"path/filepath"
	"testing"
)

func open(t *testing.T) *Ledger {
	t.Helper()
	l, err := Open(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}

func owner(t *testing.T, l *Ledger, path string) string {
	t.Helper()
	id, ok, err := l.OwnerOf(path)
	if err != nil {
		t.Fatalf("OwnerOf(%q): %v", path, err)
	}
	if !ok {
		return ""
	}
	return id
}

func TestRecordAndOwner(t *testing.T) {
	l := open(t)
	if err := l.Record(Job{JobID: "100", Outputs: []string{"out.bam"}, Inputs: []string{"in.fa"}}); err != nil {
		t.Fatal(err)
	}
	if got := owner(t, l, "out.bam"); got != "100" {
		t.Fatalf("owner = %q, want 100", got)
	}
	if got := owner(t, l, "missing"); got != "" {
		t.Fatalf("owner(missing) = %q, want empty", got)
	}
}

func TestLastJobWins(t *testing.T) {
	l := open(t)
	if err := l.Record(Job{JobID: "100", Outputs: []string{"out.bam"}}); err != nil {
		t.Fatal(err)
	}
	if err := l.Record(Job{JobID: "200", Outputs: []string{"out.bam"}}); err != nil {
		t.Fatal(err)
	}
	if got := owner(t, l, "out.bam"); got != "200" {
		t.Fatalf("owner = %q, want 200 (last job wins)", got)
	}
}

func TestVacuumKeepsCurrentOwners(t *testing.T) {
	l := open(t)
	// 100 produces out.bam, then 200 re-produces it (100 is now orphaned),
	// and 300 produces a different file.
	l.Record(Job{JobID: "100", Outputs: []string{"out.bam"}})
	l.Record(Job{JobID: "200", Outputs: []string{"out.bam"}})
	l.Record(Job{JobID: "300", Outputs: []string{"other.bam"}, Deps: []string{"200"}})

	if n, _ := l.CountJobs(); n != 3 {
		t.Fatalf("jobs before vacuum = %d, want 3", n)
	}
	if err := l.Vacuum(); err != nil {
		t.Fatal(err)
	}
	if n, _ := l.CountJobs(); n != 2 {
		t.Fatalf("jobs after vacuum = %d, want 2 (orphan 100 dropped)", n)
	}
	if got := owner(t, l, "out.bam"); got != "200" {
		t.Fatalf("owner after vacuum = %q, want 200", got)
	}
	if got := owner(t, l, "other.bam"); got != "300" {
		t.Fatalf("owner = %q, want 300", got)
	}
}

func TestReopenPersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "l.db")
	l1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	l1.Record(Job{JobID: "abc", Outputs: []string{"x"}})
	l1.Close()

	l2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()
	if got := owner(t, l2, "x"); got != "abc" {
		t.Fatalf("after reopen owner = %q, want abc", got)
	}
}
