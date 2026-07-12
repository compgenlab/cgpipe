package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// det builds a detection map with the named schedulers marked submit-available.
func det(available ...string) map[string]schedInfo {
	m := map[string]schedInfo{"slurm": {}, "sge": {}, "pbs": {}, "batchq": {}}
	for _, n := range available {
		m[n] = schedInfo{submitOK: true}
	}
	return m
}

func TestDoctorRecommend(t *testing.T) {
	// Unset scheduler-signal env so the sge/pbs branches are deterministic.
	for _, e := range []string{"SGE_ROOT", "SGE_CELL", "PBS_HOME", "PBS_SERVER", "PBS_DEFAULT", "PBS_O_HOST"} {
		t.Setenv(e, "")
	}

	// Exactly one non-ambiguous scheduler -> suggest it.
	if r := recommend("", det("slurm")); r.kind != recSuggestOne || r.runner != "slurm" {
		t.Errorf("slurm-only: kind=%q runner=%q, want suggest-one/slurm", r.kind, r.runner)
	}
	if r := recommend("", det("batchq")); r.kind != recSuggestOne || r.runner != "batchq" {
		t.Errorf("batchq-only: kind=%q runner=%q, want suggest-one/batchq", r.kind, r.runner)
	}
	// Configured and usable.
	if r := recommend("slurm", det("slurm")); r.kind != recOK {
		t.Errorf("configured+present: kind=%q, want ok", r.kind)
	}
	// Configured but the submit command is missing.
	if r := recommend("slurm", det()); r.kind != recBroken {
		t.Errorf("configured+missing: kind=%q, want broken", r.kind)
	}
	// A non-scheduler runner.
	if r := recommend("graphviz", det("slurm")); r.kind != recNonSched {
		t.Errorf("graphviz: kind=%q, want nonsched", r.kind)
	}
	// Nothing detected.
	if r := recommend("", det()); r.kind != recNone {
		t.Errorf("none: kind=%q, want none", r.kind)
	}
	// Two clear schedulers -> user must choose.
	if r := recommend("", det("slurm", "batchq")); r.kind != recSuggestMany {
		t.Errorf("slurm+batchq: kind=%q, want suggest-many", r.kind)
	}
}

// qsub present with no SGE/PBS signal is ambiguous and must never be auto-picked.
func TestDoctorRecommendAmbiguousQsub(t *testing.T) {
	for _, e := range []string{"SGE_ROOT", "SGE_CELL", "PBS_HOME", "PBS_SERVER", "PBS_DEFAULT", "PBS_O_HOST"} {
		t.Setenv(e, "")
	}
	// Point PATH somewhere empty so qconf/pbsnodes/etc. are not found.
	t.Setenv("PATH", t.TempDir())
	r := recommend("", det("sge", "pbs"))
	if r.kind != recAmbiguous {
		t.Fatalf("ambiguous qsub: kind=%q, want ambiguous", r.kind)
	}
	if r.runner != "" {
		t.Errorf("ambiguous must not pick a runner, got %q", r.runner)
	}
}

// SGE_ROOT set disambiguates qsub to SGE.
func TestDoctorRecommendSGESignal(t *testing.T) {
	for _, e := range []string{"PBS_HOME", "PBS_SERVER", "PBS_DEFAULT", "PBS_O_HOST"} {
		t.Setenv(e, "")
	}
	t.Setenv("PATH", t.TempDir())
	t.Setenv("SGE_ROOT", "/opt/sge")
	if r := recommend("", det("sge", "pbs")); r.kind != recSuggestOne || r.runner != "sge" {
		t.Errorf("SGE_ROOT set: kind=%q runner=%q, want suggest-one/sge", r.kind, r.runner)
	}
}

func TestWriteRunnerConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path, err := writeRunnerConfig("slurm")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".cgp", "config"); path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Must be a quoted string assignment (a bare word would be an undefined ident).
	if got := strings.TrimSpace(string(b)); got != `cgp.runner = "slurm"` {
		t.Errorf("wrote %q, want cgp.runner = \"slurm\"", got)
	}
}
