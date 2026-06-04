package main

import "testing"

func TestRunVersion(t *testing.T) {
	if code := run([]string{"--version"}); code != 0 {
		t.Fatalf("run(--version) = %d, want 0", code)
	}
}

func TestRunHelp(t *testing.T) {
	if code := run([]string{"-h"}); code != 0 {
		t.Fatalf("run(-h) = %d, want 0", code)
	}
}

func TestRunNoArgs(t *testing.T) {
	if code := run(nil); code != 2 {
		t.Fatalf("run(nil) = %d, want 2", code)
	}
}
