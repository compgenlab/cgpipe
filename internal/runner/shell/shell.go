package shell

import (
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/compgen-io/cgp/internal/eval"
	"github.com/compgen-io/cgp/internal/runner"
)

// Options configures a shell run.
type Options struct {
	Goals  []string      // explicit targets to build; empty => program default
	DryRun bool          // render scripts instead of executing
	Force  bool          // rebuild regardless of staleness
	Dir    string        // working directory for jobs (default: current)
	Cache  *runner.Cache // shared stat cache (for manifest fan-out)
	Out    io.Writer     // dry-run output (default os.Stdout)
	Stdout io.Writer     // job stdout (default os.Stdout)
	Stderr io.Writer     // job stderr (default os.Stderr)
}

// Run builds the program's goals with the local shell: stale targets are
// rendered and executed with bash, in dependency order.
func Run(p *eval.Program, opts Options) error {
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	b := &backend{prog: p, opts: opts}
	return runner.Build(p, b, runner.Options{Goals: opts.Goals, Dir: opts.Dir, Cache: opts.Cache, Force: opts.Force})
}

// SubmitOne renders and runs a single target with bash (used by `cgp sub`).
func SubmitOne(p *eval.Program, t *eval.Target, opts Options) error {
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	b := &backend{prog: p, opts: opts}
	_, err := b.Submit(t, nil)
	return err
}

type backend struct {
	prog *eval.Program
	opts Options
}

// ExternalDep: the shell runner is synchronous, so prerequisites already ran and
// their files exist; there are no external (queued) jobs to depend on.
func (b *backend) ExternalDep(string) (string, bool) { return "", false }

// PostSubmit runs the @postsubmit body for the just-run job (locally, as the
// shell runner already is on the submit host).
func (b *backend) PostSubmit(job *eval.Target, jobID string) error {
	body, err := b.prog.RenderPostsubmit(job, jobID)
	if err != nil || body == "" {
		return err
	}
	if b.opts.DryRun {
		fmt.Fprintf(b.opts.Out, "# ---- @postsubmit (%s) ----\n%s\n", runner.Label(job), body)
		return nil
	}
	cmd := exec.Command("bash", "-c", body)
	cmd.Dir = b.opts.Dir
	cmd.Stdout = b.opts.Stdout
	cmd.Stderr = b.opts.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("@postsubmit (%s): %w", runner.Label(job), err)
	}
	return nil
}

// Submit renders the target body and runs it with bash (synchronously, so
// dependencies have already run). It returns no job id.
func (b *backend) Submit(t *eval.Target, _ []string) (string, error) {
	script, err := b.prog.RenderTarget(t)
	if err != nil {
		return "", err
	}
	label := runner.Label(t)
	if b.opts.DryRun {
		fmt.Fprintf(b.opts.Out, "# ---- %s ----\n%s\n", label, script)
		return "", nil
	}
	cmd := exec.Command("bash", "-c", script)
	cmd.Dir = b.opts.Dir
	cmd.Stdout = b.opts.Stdout
	cmd.Stderr = b.opts.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w", label, err)
	}
	return "", nil
}
