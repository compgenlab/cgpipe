package ledger

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestDumpAndSearch covers the joblog TSV dump and the search filters.
func TestDumpAndSearch(t *testing.T) {
	l := open(t)
	if err := l.Record(Job{
		JobID: "1001", Name: "align", Pipeline: "p.cgp", WorkingDir: "/w", User: "u", SubmitTime: 100,
		Outputs: []string{"out.bam"}, Inputs: []string{"reads.fq", "ref.fa"},
		Script: "bwa mem ref.fa reads.fq > out.bam", Settings: map[string]string{"mem": "8000", "procs": "4"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := l.Record(Job{
		JobID: "1002", Name: "sort", Pipeline: "p.cgp", SubmitTime: 200,
		Outputs: []string{"sorted.bam"}, Temp: map[string]bool{"sorted.bam": true}, Inputs: []string{"out.bam"},
		Deps: []string{"1001"}, Script: "samtools sort out.bam > sorted.bam", Settings: map[string]string{"mem": "4000"},
	}); err != nil {
		t.Fatal(err)
	}

	var b strings.Builder
	if err := l.Dump(&b, nil); err != nil {
		t.Fatal(err)
	}
	d := b.String()
	for _, want := range []string{
		"1001\tPIPELINE\tp.cgp", "1001\tNAME\talign", "1001\tSUBMIT\t100",
		"1001\tOUTPUT\tout.bam", "1001\tINPUT\treads.fq", "1001\tINPUT\tref.fa",
		"1001\tSRC\tbwa mem ref.fa reads.fq > out.bam",
		"1001\tSETTING\tmem\t8000", "1001\tSETTING\tprocs\t4",
		"1002\tDEP\t1001", "1002\tTEMP\tsorted.bam", "1002\tSRC\tsamtools sort out.bam > sorted.bam",
	} {
		if !strings.Contains(d, want) {
			t.Errorf("dump missing %q in:\n%s", want, d)
		}
	}
	if strings.Contains(d, "1002\tOUTPUT\tsorted.bam") {
		t.Errorf("temp output should be TEMP, not OUTPUT")
	}

	eq := func(got []string, want ...string) {
		t.Helper()
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("got %v, want %v", got, want)
		}
	}
	search := func(f Filter) []string {
		t.Helper()
		ids, err := l.Search(f)
		if err != nil {
			t.Fatal(err)
		}
		return ids
	}
	eq(search(Filter{Grep: "samtools"}), "1002")
	eq(search(Filter{Input: "reads.fq"}), "1001")
	eq(search(Filter{Output: "sorted"}), "1002")
	eq(search(Filter{Name: "ali"}), "1001")
	eq(search(Filter{ID: "1002"}), "1002")
	eq(search(Filter{Input: "out.bam", Grep: "sort"}), "1002") // AND
	eq(search(Filter{Grep: "nope"}))                           // no match → empty

	var b2 strings.Builder
	if err := l.Dump(&b2, []string{"1002"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b2.String(), "1001\t") {
		t.Errorf("filtered dump leaked job 1001:\n%s", b2.String())
	}
}

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
