package manifest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/compgen-io/cgp/internal/eval"
)

func TestLoadTSV(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.tsv")
	os.WriteFile(path, []byte("# a comment\nsample\tbam\tthreads\nP001\t/d/P001.bam\t4\nP002\t/d/P002.bam\t8\n"), 0o644)

	rows, err := LoadDelimited(path, '\t')
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if rows[0]["sample"] != eval.StrVal("P001") || rows[0]["bam"] != eval.StrVal("/d/P001.bam") {
		t.Errorf("row0 = %v", rows[0])
	}
	if rows[0]["threads"] != eval.IntVal(4) {
		t.Errorf("threads should parse as int, got %v", rows[0]["threads"])
	}
	if rows[1]["sample"] != eval.StrVal("P002") {
		t.Errorf("row1 sample = %v", rows[1]["sample"])
	}
}

func TestLoadCSV(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.csv")
	os.WriteFile(path, []byte("a,b\n1,two\n"), 0o644)
	rows, err := LoadDelimited(path, ',')
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["a"] != eval.IntVal(1) || rows[0]["b"] != eval.StrVal("two") {
		t.Fatalf("rows = %v", rows)
	}
}

func TestLoadJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.json")
	os.WriteFile(path, []byte(`[{"sample":"P001","threads":4,"paired":true},{"sample":"P002","threads":8}]`), 0o644)
	rows, err := LoadJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if rows[0]["sample"] != eval.StrVal("P001") || rows[0]["threads"] != eval.IntVal(4) || rows[0]["paired"] != eval.BoolVal(true) {
		t.Errorf("row0 = %v", rows[0])
	}
}
