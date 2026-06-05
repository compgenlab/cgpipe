// Package sched implements the scheduler (template) runners — SLURM, SGE, PBS,
// and BatchQ. Each renders a submission script from a per-scheduler template and
// the job's settings, pipes it to the scheduler's submit command, and captures
// the job id; dependencies are wired by passing upstream job ids to the template.
package sched

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/compgen-io/cgp/internal/eval"
	"github.com/compgen-io/cgp/internal/ledger"
	"github.com/compgen-io/cgp/internal/runner"
)

// Scheduler describes one batch system.
type Scheduler struct {
	Name       string
	Template   string
	SubCmd     []string              // base submit command (script piped to stdin)
	HoldArgs   []string              // appended to SubCmd to submit held (global_hold)
	DepSep     string                // join separator for dependency ids
	MailType   string                // default mail type when `mail` is set
	PrepareMem func(string) string   // optional mem normalization
	ReleaseCmd func(string) []string // command to release a held job
	IsActive   func(string) bool     // is a job id still queued/running? (for ledger reuse)
	State      func(string) string   // normalized live state for reports: queued|running|done|failed|"" (unknown); optional
}

var schedulers = map[string]Scheduler{
	"slurm": {
		Name: "slurm", Template: slurmTmpl,
		SubCmd: []string{"sbatch", "--parsable"}, HoldArgs: []string{"-H"},
		DepSep: ":", MailType: "END,FAIL", PrepareMem: slurmMem,
		ReleaseCmd: func(id string) []string { return []string{"scontrol", "release", id} },
		IsActive:   slurmActive,
		State:      slurmState,
	},
	"sge": {
		Name: "sge", Template: sgeTmpl,
		SubCmd: []string{"qsub"}, HoldArgs: []string{"-h", "u"},
		DepSep: ",", MailType: "ae",
		ReleaseCmd: func(id string) []string { return []string{"qrls", id} },
		IsActive:   func(id string) bool { return exec.Command("qstat", "-j", id).Run() == nil },
	},
	"pbs": {
		Name: "pbs", Template: pbsTmpl,
		SubCmd: []string{"qsub"}, HoldArgs: []string{"-h"},
		DepSep: ":", MailType: "abe", PrepareMem: pbsMem,
		ReleaseCmd: func(id string) []string { return []string{"qrls", id} },
		IsActive:   func(id string) bool { return exec.Command("qstat", id).Run() == nil },
	},
	"batchq": {
		Name: "batchq", Template: batchqTmpl,
		SubCmd: []string{"batchq", "submit"}, HoldArgs: []string{"--hold"},
		DepSep:     ",",
		ReleaseCmd: func(id string) []string { return []string{"batchq", "release", id} },
		IsActive:   func(id string) bool { return exec.Command("batchq", "status", id).Run() == nil },
	},
}

// slurmActive reports whether a SLURM job is still pending/running (and not
// doomed by an unsatisfiable dependency), via `scontrol show job`.
func slurmActive(id string) bool {
	out, err := exec.Command("scontrol", "-o", "show", "job", id).Output()
	if err != nil {
		return false
	}
	state, reason := "", ""
	for _, tok := range strings.Fields(string(out)) {
		if kv := strings.SplitN(tok, "=", 2); len(kv) == 2 {
			switch kv[0] {
			case "JobState":
				state = kv[1]
			case "Reason":
				reason = kv[1]
			}
		}
	}
	if state != "PENDING" && state != "RUNNING" {
		return false
	}
	return reason != "DependencyNeverSatisfied"
}

// slurmState maps a SLURM JobState (from `scontrol show job`) to the report
// vocabulary; "" means unknown (e.g. the job has aged out of scontrol).
func slurmState(id string) string {
	out, err := exec.Command("scontrol", "-o", "show", "job", id).Output()
	if err != nil {
		return ""
	}
	state := ""
	for _, tok := range strings.Fields(string(out)) {
		if kv := strings.SplitN(tok, "=", 2); len(kv) == 2 && kv[0] == "JobState" {
			state = kv[1]
		}
	}
	switch state {
	case "PENDING":
		return "queued"
	case "RUNNING", "CONFIGURING", "COMPLETING", "RESIZING":
		return "running"
	case "COMPLETED":
		return "done"
	case "FAILED", "CANCELLED", "TIMEOUT", "NODE_FAIL", "OUT_OF_MEMORY", "BOOT_FAIL", "DEADLINE", "PREEMPTED":
		return "failed"
	}
	return ""
}

// For returns the scheduler with the given name.
func For(name string) (Scheduler, bool) {
	s, ok := schedulers[name]
	return s, ok
}

// Names lists the supported scheduler names.
func Names() []string { return []string{"slurm", "sge", "pbs", "batchq"} }

// Options configure a scheduler run.
type Options struct {
	Goals    []string
	DryRun   bool
	Force    bool // resubmit regardless of staleness
	Dir      string
	Pipeline string        // pipeline filename, recorded in the ledger
	Cache    *runner.Cache // shared stat cache (for manifest fan-out)
	Out      io.Writer     // submission scripts (dry-run) and job-id output
}

// Run builds the program's goals by submitting stale targets to the scheduler.
func Run(p *eval.Program, sch Scheduler, opts Options) error {
	b, err := newBackend(p, sch, opts)
	if err != nil {
		return err
	}
	defer b.closeLedger()
	if err := runner.Build(p, b, runner.Options{Goals: opts.Goals, Dir: opts.Dir, Cache: opts.Cache, Force: opts.Force}); err != nil {
		return err
	}
	return b.finish()
}

// SubmitOne submits a single target with the given explicit dependency job ids,
// plus dependencies derived from afterOutputs (the active ledger owner of each).
// Used by `cgp sub`.
func SubmitOne(p *eval.Program, sch Scheduler, t *eval.Target, explicitDeps, afterOutputs []string, opts Options) (string, error) {
	b, err := newBackend(p, sch, opts)
	if err != nil {
		return "", err
	}
	defer b.closeLedger()

	deps := append([]string{}, explicitDeps...)
	if b.ledger != nil {
		for _, out := range afterOutputs {
			if owner, ok, err := b.ledger.OwnerOf(out); err == nil && ok && owner != "" {
				if sch.IsActive == nil || sch.IsActive(owner) {
					deps = append(deps, owner)
				}
			}
		}
	}
	id, err := b.Submit(t, deps)
	if err != nil {
		return "", err
	}
	return id, b.finish()
}

func newBackend(p *eval.Program, sch Scheduler, opts Options) (*backend, error) {
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	shell := "/bin/bash"
	if v, ok := p.Get("cgp.shell"); ok {
		shell = eval.Stringify(v)
	}
	gh := false
	if v, ok := p.Get("cgp.runner." + sch.Name + ".global_hold"); ok {
		gh = eval.Truthy(v)
	}
	// Effective working directory recorded in the ledger so a later status read /
	// restart can resolve the job's relative paths: the explicit Dir if set,
	// otherwise the process cwd (the directory cgp was launched from).
	wd := opts.Dir
	if wd == "" {
		wd, _ = os.Getwd()
	}
	b := &backend{prog: p, sch: sch, opts: opts, shell: shell, globalHold: gh, user: os.Getenv("USER"), wd: wd}
	if v, ok := p.Get("cgp.run_id"); ok {
		b.runID = eval.Stringify(v)
	}
	// Optional ledger: enables cross-run reuse of still-active jobs. Not used in
	// dry-run (no real job ids).
	if v, ok := p.Get("cgp.ledger"); ok && eval.Stringify(v) != "" && !opts.DryRun {
		lg, err := ledger.Open(eval.Stringify(v))
		if err != nil {
			return nil, fmt.Errorf("ledger: %w", err)
		}
		b.ledger = lg
	}
	return b, nil
}

func (b *backend) closeLedger() {
	if b.ledger != nil {
		b.ledger.Close()
	}
}

type backend struct {
	prog       *eval.Program
	sch        Scheduler
	opts       Options
	shell      string
	globalHold bool
	ledger     *ledger.Ledger
	runID      string
	user       string
	wd         string // effective working directory recorded in the ledger
	dryN       int
	ids        []string
	held       []string
}

func (b *backend) cfg(name string) string {
	if v, ok := b.prog.Get(name); ok {
		return eval.Stringify(v)
	}
	return ""
}

// ExternalDep reports an active ledger job that owns input (from a prior run or
// an earlier workflow stage whose jobs are still queued), so a dependent stage
// wires afterok onto it instead of failing on the not-yet-produced file.
func (b *backend) ExternalDep(input string) (string, bool) {
	if b.ledger == nil {
		return "", false
	}
	owner, ok, err := b.ledger.OwnerOf(input)
	if err != nil || !ok || owner == "" {
		return "", false
	}
	if b.sch.IsActive != nil && !b.sch.IsActive(owner) {
		return "", false
	}
	return owner, true
}

// PostSubmit runs the @postsubmit body on the submission host after a job is
// submitted, with the scheduler job id available as ${jobid}.
func (b *backend) PostSubmit(job *eval.Target, jobID string) error {
	body, err := b.prog.RenderPostsubmit(job, jobID)
	if err != nil || body == "" {
		return err
	}
	if b.opts.DryRun {
		fmt.Fprintf(b.opts.Out, "# [postsubmit %s] %s\n%s\n", jobID, runner.Label(job), body)
		return nil
	}
	cmd := exec.Command(b.shell, "-c", body)
	cmd.Dir = b.opts.Dir
	cmd.Stdout = b.opts.Out
	cmd.Stderr = b.opts.Out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("@postsubmit (%s): %w", runner.Label(job), err)
	}
	return nil
}

func (b *backend) Submit(t *eval.Target, deps []string) (string, error) {
	// Multiple inputs can come from one upstream (multi-output) job, so the same
	// dependency id may appear more than once; collapse duplicates before wiring
	// them (some schedulers, e.g. batchq, reject a repeated afterok).
	deps = dedupeIDs(deps)
	// Cross-run reuse: if an existing job still owns this output and is still
	// active in the scheduler, depend on it instead of resubmitting.
	if b.ledger != nil && len(t.Outputs) > 0 {
		if owner, ok, err := b.ledger.OwnerOf(t.Outputs[0]); err == nil && ok && owner != "" {
			if b.sch.IsActive == nil || b.sch.IsActive(owner) {
				fmt.Fprintf(b.opts.Out, "# reuse: %s already owned by active job %s\n", runner.Label(t), owner)
				return owner, nil
			}
		}
	}

	vars, body, err := b.prog.JobContext(t)
	if err != nil {
		return "", err
	}
	// shexec: run the body directly on the submission host instead of submitting
	// it (the usual choice for @setup/@teardown — e.g. mkdir). No scheduler job,
	// so no job id and nothing recorded in the ledger.
	if v, ok := vars["shexec"]; ok && eval.Truthy(v) {
		return b.shExec(runner.Label(t), body)
	}
	if m, ok := vars["mem"]; ok && b.sch.PrepareMem != nil {
		vars["mem"] = eval.StrVal(b.sch.PrepareMem(eval.Stringify(m)))
	}
	// Normalize a boolean gpu (gpu = true) to a count for the scheduler directive.
	if v, ok := vars["gpu"]; ok {
		if b, isBool := v.(eval.BoolVal); isBool {
			if bool(b) {
				vars["gpu"] = eval.IntVal(1)
			} else {
				delete(vars, "gpu")
			}
		}
	}
	setDefault(vars, "procs", eval.IntVal(1))
	setDefault(vars, "name", eval.StrVal(jobName(t)))
	setDefault(vars, "custom", eval.ListVal{})
	setDefault(vars, "setup", eval.ListVal{})
	setDefault(vars, "shell", eval.StrVal(b.shell))
	if _, ok := vars["mail"]; ok {
		setDefault(vars, "mailtype", eval.StrVal(b.sch.MailType))
	}
	vars["_body"] = eval.StrVal(body)
	vars["_inputs"] = eval.StrList(t.Inputs)
	vars["_outputs"] = eval.StrList(t.Outputs)
	if len(deps) > 0 {
		vars["depids"] = eval.StrVal(strings.Join(deps, b.sch.DepSep))
	}
	if pe := b.cfg("cgp.runner.sge.parallelenv"); pe != "" {
		setDefault(vars, "parallelenv", eval.StrVal(pe))
	}
	if rid := b.cfg("cgp.run_id"); rid != "" {
		setDefault(vars, "run_id", eval.StrVal(rid))
	}

	script, err := b.prog.RenderText(b.sch.Template, vars)
	if err != nil {
		return "", err
	}
	id, err := b.submitScript(runner.Label(t), script)
	if err != nil {
		return "", err
	}
	if b.ledger != nil && id != "" && len(t.Outputs) > 0 {
		if err := b.ledger.Record(ledger.Job{
			JobID: id, RunID: b.runID, Name: eval.Stringify(vars["name"]), Pipeline: b.opts.Pipeline,
			WorkingDir: b.wd, User: b.user, SubmitTime: time.Now().Unix(),
			Outputs: t.Outputs, Temp: t.Temp, Inputs: t.Inputs, Deps: deps,
			Script: body, Settings: jobSettings(vars),
		}); err != nil {
			return "", fmt.Errorf("ledger record: %w", err)
		}
	}
	return id, nil
}

// jobSettings extracts the per-job settings worth recording (mem, procs,
// walltime, name, container, gpu, …) from the render vars: scalar values, minus
// the build-in/internal names and cgp.* config.
func jobSettings(vars map[string]eval.Value) map[string]string {
	skip := map[string]bool{
		"input": true, "output": true, "stem": true, "name": true, // name is recorded as NAME
		"_body": true, "_inputs": true, "_outputs": true,
		"depids": true, "custom": true, "setup": true, "shell": true,
		"run_id": true, "parallelenv": true, "mailtype": true,
	}
	out := map[string]string{}
	for k, v := range vars {
		if skip[k] || strings.HasPrefix(k, "cgp.") {
			continue
		}
		switch v.(type) {
		case eval.StrVal, eval.IntVal, eval.FloatVal, eval.BoolVal:
			out[k] = eval.Stringify(v)
		}
	}
	return out
}

// shExec runs body on the submission host (for shexec targets). In dry-run it is
// rendered, not executed.
func (b *backend) shExec(label, body string) (string, error) {
	if b.opts.DryRun {
		fmt.Fprintf(b.opts.Out, "# [shexec] %s\n%s\n", label, body)
		return "", nil
	}
	cmd := exec.Command(b.shell, "-c", body)
	cmd.Dir = b.opts.Dir
	cmd.Stdout = b.opts.Out
	cmd.Stderr = b.opts.Out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s (shexec): %w", label, err)
	}
	return "", nil
}

func (b *backend) submitScript(label, script string) (string, error) {
	if b.opts.DryRun {
		b.dryN++
		fmt.Fprintf(b.opts.Out, "# [dryrun.%d] %s\n%s\n", b.dryN, label, script)
		return fmt.Sprintf("dryrun.%d", b.dryN), nil
	}
	args := append([]string{}, b.sch.SubCmd...)
	if b.globalHold {
		args = append(args, b.sch.HoldArgs...)
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = b.opts.Dir
	cmd.Stdin = strings.NewReader(script)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s: submit failed: %v: %s", label, err, strings.TrimSpace(errb.String()))
	}
	id := strings.TrimSpace(string(out))
	b.ids = append(b.ids, id)
	if b.globalHold {
		b.held = append(b.held, id)
	}
	return id, nil
}

func (b *backend) finish() error {
	for _, id := range b.ids {
		fmt.Fprintln(b.opts.Out, id)
	}
	if b.globalHold && !b.opts.DryRun {
		for _, id := range b.held {
			rc := b.sch.ReleaseCmd(id)
			if err := exec.Command(rc[0], rc[1:]...).Run(); err != nil {
				return fmt.Errorf("release %s: %w", id, err)
			}
		}
	}
	return nil
}

func setDefault(m map[string]eval.Value, k string, v eval.Value) {
	if _, ok := m[k]; !ok {
		m[k] = v
	}
}

// dedupeIDs returns ids with empties and later duplicates removed, preserving
// first-seen order.
func dedupeIDs(ids []string) []string {
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func jobName(t *eval.Target) string {
	if len(t.Outputs) > 0 {
		return t.Outputs[0]
	}
	if t.Special != "" {
		return "cgp." + t.Special
	}
	return "cgp.job"
}

// slurmMem normalizes a memory spec to megabytes (SLURM --mem unit). "8G" -> 8000.
func slurmMem(s string) string {
	num, units := splitMem(s)
	if num < 0 {
		return s
	}
	switch strings.ToUpper(units) {
	case "G", "GB":
		return strconv.Itoa(int(num*1000 + 0.5))
	default:
		return strconv.Itoa(int(num + 0.5))
	}
}

// pbsMem lowercases the spec and appends "b" (PBS mem=... unit). "4G" -> "4gb".
func pbsMem(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s) + "b"
}

func splitMem(s string) (float64, string) {
	i := 0
	for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '.') {
		i++
	}
	if i == 0 {
		return -1, ""
	}
	n, err := strconv.ParseFloat(s[:i], 64)
	if err != nil {
		return -1, ""
	}
	return n, s[i:]
}
