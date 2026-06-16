package main

import (
	"bytes"
	"os"
	"testing"

	"github.com/compgen-io/cgp/internal/debug"
)

// -debug N and CGP_DEBUG set the global level, with the explicit flag winning;
// a non-numeric -debug value is a usage error.
func TestDebugFlagAndEnv(t *testing.T) {
	t.Chdir(t.TempDir())
	os.WriteFile("p.cgp", []byte("out.txt: {{\n--\necho hi > ${output}\n}}\n@default: out.txt\n"), 0o644)

	// Keep debug output out of the test log, and restore global state after.
	prev := debug.SetWriter(&bytes.Buffer{})
	defer debug.SetWriter(prev)
	defer debug.SetLevel(0)

	// runDr runs a dry-run pipeline with extra leading args, swallowing stdout.
	runDr := func(extra ...string) {
		captureStdout(t, func() int { return run(append(extra, "-dr", "p.cgp")) })
	}

	debug.SetLevel(0)
	runDr("-debug", "4")
	if debug.Level() != 4 {
		t.Fatalf("-debug 4 → level %d, want 4", debug.Level())
	}

	debug.SetLevel(0)
	t.Setenv("CGP_DEBUG", "2")
	runDr()
	if debug.Level() != 2 {
		t.Fatalf("CGP_DEBUG=2 → level %d, want 2", debug.Level())
	}

	// Explicit flag beats the env var.
	debug.SetLevel(0)
	runDr("-debug", "5")
	if debug.Level() != 5 {
		t.Fatalf("flag should beat CGP_DEBUG → level %d, want 5", debug.Level())
	}

	// Non-numeric level is a usage error (exit 2), before any pipeline work.
	if code := run([]string{"-debug", "x", "p.cgp"}); code != 2 {
		t.Fatalf("-debug x → code %d, want 2", code)
	}
}
