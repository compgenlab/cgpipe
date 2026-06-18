package sched

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// isActive is memoized per backend: a job that owns many outputs (or every task
// of a large array) is probed once, not once per output — the fix for a stale
// ledger flooding the scheduler with status calls.
func TestIsActiveMemoized(t *testing.T) {
	var calls int
	b := &backend{sch: Scheduler{IsActive: func(string) bool { calls++; return true }}}

	for i := 0; i < 5; i++ {
		if !b.isActive("J1") {
			t.Fatal("J1 should be active")
		}
	}
	b.isActive("J2")
	b.isActive("J2")
	if calls != 2 {
		t.Fatalf("IsActive invoked %d times, want 2 (once per distinct id)", calls)
	}

	// A scheduler without a probe is treated as active and never shells out.
	b2 := &backend{sch: Scheduler{}}
	if !b2.isActive("X") {
		t.Fatal("nil IsActive should be treated as active")
	}
}

// A scheduler probe is bounded by CGPIPE_PROBE_TIMEOUT, so a hung/slow scheduler
// cannot stall the run indefinitely.
func TestProbeTimeout(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sleepytool"), []byte("#!/bin/bash\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CGPIPE_PROBE_TIMEOUT", "1")

	start := time.Now()
	_, _, err := probe("sleepytool")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected a timeout error from a 30s-sleeping probe")
	}
	if elapsed > 10*time.Second {
		t.Fatalf("probe took %s — the 1s timeout did not fire", elapsed)
	}
}

// batchqStatus falls back to the plain query when --porcelain is unsupported,
// and remembers that so it stops paying for the doomed porcelain probe.
func TestBatchqStatusPorcelainFallback(t *testing.T) {
	batchqPorcelainUnsupported.Store(false)
	defer batchqPorcelainUnsupported.Store(false)

	dir := t.TempDir()
	script := "#!/bin/bash\n" +
		"if [ \"$2\" = \"--porcelain\" ]; then echo 'unknown flag: --porcelain' >&2; exit 1; fi\n" +
		"echo \"$2 RUNNING\"\n"
	if err := os.WriteFile(filepath.Join(dir, "batchq"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if got := batchqStatus("J1"); got != "RUNNING" {
		t.Fatalf("batchqStatus = %q, want RUNNING (via plain fallback)", got)
	}
	if !batchqPorcelainUnsupported.Load() {
		t.Fatal("porcelain should be marked unsupported after an 'unknown flag' error")
	}
}
