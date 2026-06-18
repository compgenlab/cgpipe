package main

import "testing"

func TestSubstInput(t *testing.T) {
	const file = "data/sample.fastq.gz" // basename: sample.fastq.gz
	cases := []struct {
		in   string
		idx  int
		want string
	}{
		{"{}", 1, "data/sample.fastq.gz"},                         // full path
		{"{^}", 1, "data/sample.fastq.gz"},                        // full path (alias)
		{"{@}", 1, "sample.fastq.gz"},                             // basename
		{"{^.gz}", 1, "data/sample.fastq"},                        // strip suffix off full path
		{"{@.fastq.gz}", 1, "sample"},                             // strip suffix off basename
		{"{^.bam}", 1, "data/sample.fastq.gz"},                    // suffix absent → unchanged full
		{"{@.bam}", 1, "sample.fastq.gz"},                         // suffix absent → unchanged base
		{"{#}", 7, "7"},                                           // 1-based index
		{"{{}}", 1, "{}"},                                         // escaped literal
		{"{unknown}", 1, "{unknown}"},                             // unrecognized → verbatim
		{"plain text", 1, "plain text"},                           // no placeholders
		{"a{}b{@}c", 1, "adata/sample.fastq.gzbsample.fastq.gzc"}, // adjacent
		{"gzip -9 {} > {@.gz}.out", 1, "gzip -9 data/sample.fastq.gz > sample.fastq.out"}, // multiple
		{"job{#}", 3, "job3"}, // index inside a name
		{"{", 1, "{"},         // unterminated brace → verbatim
	}
	for _, c := range cases {
		if got := substInput(c.in, file, c.idx); got != c.want {
			t.Errorf("substInput(%q, %q, %d) = %q, want %q", c.in, file, c.idx, got, c.want)
		}
	}
}

func TestSubstInputBareName(t *testing.T) {
	// A file with no directory: {} and {@} agree.
	if got := substInput("{@.txt}", "notes.txt", 1); got != "notes" {
		t.Errorf("got %q, want %q", got, "notes")
	}
	if got := substInput("{}", "notes.txt", 1); got != "notes.txt" {
		t.Errorf("got %q, want %q", got, "notes.txt")
	}
}
