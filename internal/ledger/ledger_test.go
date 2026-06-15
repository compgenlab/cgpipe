package ledger

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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

// Array tasks carry their array id and index; a query by the bare array id
// matches every task, and the dump surfaces ARRAY/TASKINDEX.
func TestArrayIDMatching(t *testing.T) {
	l := open(t)
	for i := 1; i <= 3; i++ {
		if err := l.Record(Job{
			JobID: "arr_" + itoa(i), ArrayID: "arr", TaskIndex: i, SubmitTime: int64(100 + i),
			Outputs: []string{"calls." + itoa(i) + ".vcf"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	// A lone non-array job, to confirm the array id doesn't over-match.
	if err := l.Record(Job{JobID: "solo", SubmitTime: 200, Outputs: []string{"merged.vcf"}}); err != nil {
		t.Fatal(err)
	}

	// The bare array id matches all three tasks; a specific task id matches one.
	ids, err := l.Search(Filter{ID: "arr"})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(ids)
	if got := strings.Join(ids, ","); got != "arr_1,arr_2,arr_3" {
		t.Errorf("Search(ID=arr) = %q, want arr_1,arr_2,arr_3", got)
	}
	if ids, _ := l.Search(Filter{ID: "arr_2"}); strings.Join(ids, ",") != "arr_2" {
		t.Errorf("Search(ID=arr_2) = %v, want [arr_2]", ids)
	}
	if ids, _ := l.Search(Filter{ID: "solo"}); strings.Join(ids, ",") != "solo" {
		t.Errorf("Search(ID=solo) = %v, want [solo]", ids)
	}

	// Dump by the array id returns all tasks with ARRAY/TASKINDEX, and no solo.
	var b strings.Builder
	if err := l.Dump(&b, []string{"arr"}); err != nil {
		t.Fatal(err)
	}
	d := b.String()
	for _, want := range []string{"arr_1\tARRAY\tarr", "arr_1\tTASKINDEX\t1", "arr_3\tTASKINDEX\t3"} {
		if !strings.Contains(d, want) {
			t.Errorf("dump missing %q in:\n%s", want, d)
		}
	}
	if strings.Contains(d, "solo\t") {
		t.Errorf("dump of array id leaked the solo job:\n%s", d)
	}
}

func itoa(i int) string { return strconv.Itoa(i) }

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

// TestFoldMultiWriterLastWins simulates two separate processes each appending to
// their own log file and both claiming the same output: the later record (by the
// (ts,host,pid,seq) order) must win — the scenario a single shared SQLite file
// couldn't survive.
func TestFoldMultiWriterLastWins(t *testing.T) {
	dir := t.TempDir()
	writeLog(t, dir, "hostA-10-100-1.jsonl",
		record{Ts: 100, Seq: 1, Host: "hostA", Pid: 10, Job: Job{JobID: "A", Outputs: []string{"out.bam"}}})
	writeLog(t, dir, "hostB-20-200-1.jsonl",
		record{Ts: 200, Seq: 1, Host: "hostB", Pid: 20, Job: Job{JobID: "B", Outputs: []string{"out.bam"}}})

	l, err := OpenRead(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if got := owner(t, l, "out.bam"); got != "B" {
		t.Fatalf("owner = %q, want B (later record wins across writers)", got)
	}
	if n, _ := l.CountJobs(); n != 2 {
		t.Fatalf("jobs = %d, want 2", n)
	}
}

// TestFoldToleratesTornLine makes sure a partial trailing record (a writer that
// crashed mid-append) never makes the whole ledger unreadable.
func TestFoldToleratesTornLine(t *testing.T) {
	dir := t.TempDir()
	good, _ := json.Marshal(record{Ts: 100, Seq: 1, Host: "h", Pid: 1,
		Job: Job{JobID: "ok", Outputs: []string{"a"}}})
	content := string(good) + "\n" + `{"ts":200,"job_id":"trunc","outp` // truncated
	if err := os.WriteFile(filepath.Join(dir, "h-1-1-1.jsonl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := OpenRead(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if got := owner(t, l, "a"); got != "ok" {
		t.Fatalf("owner = %q, want ok (torn trailing line must not break the read)", got)
	}
	if n, _ := l.CountJobs(); n != 1 {
		t.Fatalf("jobs = %d, want 1 (torn record skipped)", n)
	}
}

// TestVacuumCompactsToSnapshot checks that vacuum collapses the per-process logs
// into a single snapshot.jsonl holding only the current owners.
func TestVacuumCompactsToSnapshot(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	l.Record(Job{JobID: "1", Outputs: []string{"x"}})
	l.Record(Job{JobID: "2", Outputs: []string{"x"}}) // 1 orphaned
	l.Record(Job{JobID: "3", Outputs: []string{"y"}})
	if err := l.Vacuum(); err != nil {
		t.Fatal(err)
	}
	l.Close()

	if names := jsonlNames(t, dir); len(names) != 1 || names[0] != snapshotName {
		t.Fatalf("after vacuum dir = %v, want [%s]", names, snapshotName)
	}
	l2, err := OpenRead(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()
	if n, _ := l2.CountJobs(); n != 2 {
		t.Fatalf("jobs after vacuum = %d, want 2", n)
	}
	if got := owner(t, l2, "x"); got != "2" {
		t.Fatalf("owner x = %q, want 2", got)
	}
	if got := owner(t, l2, "y"); got != "3" {
		t.Fatalf("owner y = %q, want 3", got)
	}
}

func writeLog(t *testing.T, dir, name string, recs ...record) {
	t.Helper()
	var b strings.Builder
	for _, r := range recs {
		j, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(j)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func jsonlNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonl") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
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
