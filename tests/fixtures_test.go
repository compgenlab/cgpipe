// Package tests runs the standalone .cgp fixture suite (tests/run.sh) as part
// of `go test ./...`, so the language-spec golden files are exercised in CI the
// same way a developer runs them by hand. The shell harness is the source of
// truth for the fixture format; this wrapper just builds the binary under test
// and points the harness at it.
package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestFixtures(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available; skipping fixture suite")
	}

	_, file, _, _ := runtime.Caller(0)
	dir := filepath.Dir(file) // tests/
	root := filepath.Dir(dir) // repo root

	// Build the binary under test once and hand it to the harness via CGP_BIN
	// so the fixtures exercise the current code rather than a stale bin/.
	bin := filepath.Join(t.TempDir(), "cgp")
	build := exec.Command("go", "build", "-o", bin, "./cmd/cgp")
	build.Dir = root
	build.Env = append(os.Environ(), "GOWORK=off", "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build cgp: %v\n%s", err, out)
	}

	cmd := exec.Command("bash", filepath.Join(dir, "run.sh"))
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGP_BIN="+bin)
	out, err := cmd.CombinedOutput()
	t.Logf("\n%s", out)
	if err != nil {
		t.Fatalf("fixture suite failed: %v", err)
	}
}
