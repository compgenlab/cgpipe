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
	Goals    []string      // explicit targets to build; empty => program default
	DryRun   bool          // render scripts instead of executing
	AutoExec bool          // execute the assembled script instead of emitting it
	Force    bool          // rebuild regardless of staleness
	Dir      string        // working directory for jobs (default: current)
	Cache    *runner.Cache // shared stat cache (for manifest fan-out)
	Out      io.Writer     // emitted script / dry-run output (default os.Stdout)
	Stdout   io.Writer     // job stdout when executing (default os.Stdout)
	Stderr   io.Writer     // job stderr when executing (default os.Stderr)
}

// Run builds the program's goals with the local shell. By default it assembles
// the stale targets into one runnable bash script (in dependency order) and
// writes it to Out — it does NOT execute. Set AutoExec (cgp.runner.shell.autoexec)
// to run the bodies instead. -dr (DryRun) always emits, never executes.
func Run(p *eval.Program, opts Options) error {
	defaults(&opts)
	b := &backend{prog: p, opts: opts, emit: opts.DryRun || !opts.AutoExec}
	if b.emit {
		fmt.Fprint(opts.Out, "#!/usr/bin/env bash\nset -euo pipefail\n\n")
	}
	return runner.Build(p, b, runner.Options{Goals: opts.Goals, Dir: opts.Dir, Cache: opts.Cache, Force: opts.Force})
}

// SubmitOne renders a single target (used by `cgp sub`). Unlike the pipeline
// runner it executes by default — `cgp sub` is an explicit one-off — emitting
// only under -dr.
func SubmitOne(p *eval.Program, t *eval.Target, opts Options) error {
	defaults(&opts)
	b := &backend{prog: p, opts: opts, emit: opts.DryRun}
	_, err := b.Submit(t, nil)
	return err
}

func defaults(opts *Options) {
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
}

type backend struct {
	prog *eval.Program
	opts Options
	emit bool // emit the script (default / dry-run) rather than execute
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
	if b.emit {
		fmt.Fprintf(b.opts.Out, "# ---- @postsubmit (%s) ----\n%s\n\n", runner.Label(job), body)
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
	if b.emit {
		fmt.Fprintf(b.opts.Out, "# ---- %s ----\n%s\n\n", label, script)
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
