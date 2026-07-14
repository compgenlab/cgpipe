package main

import (
	"strings"
	"testing"
)

// renderSub runs `cgp sub` in dry-run mode against a scheduler and returns the
// rendered submission script (the same path a real submit would pipe to stdin).
func renderSub(t *testing.T, args ...string) string {
	t.Helper()
	full := append([]string{"sub", "-dr"}, args...)
	return captureStdout(t, func() int { return run(full) })
}

// --custom appends verbatim scheduler directive lines, in order and repeatable.
func TestSubCustomDirectives(t *testing.T) {
	t.Chdir(t.TempDir())
	out := renderSub(t, "-r", "slurm",
		"--custom", "-A foo", "--custom", "--exclusive",
		"-o", "out.bam", "echo", "hi")
	for _, want := range []string{"#SBATCH -A foo", "#SBATCH --exclusive"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\n--- got ---\n%s", want, out)
		}
	}
	// Repeatable and ordered: the first --custom precedes the second.
	if i, j := strings.Index(out, "#SBATCH -A foo"), strings.Index(out, "#SBATCH --exclusive"); i < 0 || j < 0 || i > j {
		t.Errorf("custom directives out of order (i=%d j=%d)\n%s", i, j, out)
	}
}

// The portable named flags map to the right per-scheduler directive on slurm.
func TestSubNamedSettingsSlurm(t *testing.T) {
	t.Chdir(t.TempDir())
	out := renderSub(t, "-r", "slurm",
		"--account", "acct1", "--queue", "bigmem", "--gpu", "2", "--mail", "me@x.com",
		"-o", "out.bam", "echo", "hi")
	for _, want := range []string{
		"#SBATCH -A acct1",
		"#SBATCH -p bigmem",
		"#SBATCH --gres=gpu:2",
		"#SBATCH --mail-user=me@x.com",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\n--- got ---\n%s", want, out)
		}
	}
}

// The same named flags are portable: on PBS they render with the PBS prefix and
// PBS's own flag spellings (account -A, queue -q), proving the settings are not
// slurm-specific. --custom is likewise emitted with the scheduler's prefix.
func TestSubNamedSettingsPBS(t *testing.T) {
	t.Chdir(t.TempDir())
	out := renderSub(t, "-r", "pbs",
		"--account", "acct1", "--queue", "bigmem", "--custom", "-l feature=hpc",
		"-o", "out.bam", "echo", "hi")
	for _, want := range []string{
		"#PBS -A acct1",
		"#PBS -q bigmem",
		"#PBS -l feature=hpc",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\n--- got ---\n%s", want, out)
		}
	}
}
