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
	"path/filepath"
	"sort"
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
	// Status returns the scheduler's native status word for a job (e.g. SLURM
	// "PENDING", batchq "PROXYQUEUED"), or "" if the job is unknown/aged out. Used
	// by `cgp ledger status` to show scheduler-specific states verbatim.
	Status func(string) string
	// EndTime returns the job's completion time (Unix seconds) when the scheduler
	// exposes one, with ok=false otherwise. Best-effort: used by `cgp ledger status`
	// as the upper bound of the output-mtime window. Only some schedulers implement it.
	EndTime func(string) (int64, bool)
	// ArrayTaskVar is the run-time environment variable carrying a job array's task
	// index (e.g. SLURM_ARRAY_TASK_ID). "" means the scheduler has no array support,
	// so array groups fall back to one job per element.
	ArrayTaskVar string
}

var schedulers = map[string]Scheduler{
	"slurm": {
		Name: "slurm", Template: slurmTmpl,
		SubCmd: []string{"sbatch", "--parsable"}, HoldArgs: []string{"-H"},
		DepSep: ":", MailType: "END,FAIL", PrepareMem: slurmMem,
		ReleaseCmd:   func(id string) []string { return []string{"scontrol", "release", id} },
		IsActive:     slurmActive,
		State:        slurmState,
		Status:       slurmStatus,
		EndTime:      slurmEndTime,
		ArrayTaskVar: "SLURM_ARRAY_TASK_ID",
	},
	"sge": {
		Name: "sge", Template: sgeTmpl,
		SubCmd: []string{"qsub"}, HoldArgs: []string{"-h", "u"},
		DepSep: ",", MailType: "ae",
		ReleaseCmd: func(id string) []string { return []string{"qrls", id} },
		IsActive:   sgeActive,
		State:      sgeState,
		Status:     sgeStatus,
	},
	"pbs": {
		Name: "pbs", Template: pbsTmpl,
		SubCmd: []string{"qsub"}, HoldArgs: []string{"-h"},
		DepSep: ":", MailType: "abe", PrepareMem: pbsMem,
		ReleaseCmd: func(id string) []string { return []string{"qrls", id} },
		IsActive:   pbsActive,
		State:      pbsState,
		Status:     pbsStatus,
		// No ArrayTaskVar: pipeline-array task ids on PBS use a different subjob
		// format (12345[i]) than SLURM/BatchQ's <base>_<i>, so pipeline arrays fall
		// back to per-element submission on PBS. (cgp sub --array has no downstream
		// task-id deps and still renders #PBS -J.)
	},
	"batchq": {
		Name: "batchq", Template: batchqTmpl,
		SubCmd: []string{"batchq", "submit"}, HoldArgs: []string{"--hold"},
		DepSep:       ",",
		ReleaseCmd:   func(id string) []string { return []string{"batchq", "release", id} },
		IsActive:     batchqActive,
		State:        batchqState,
		Status:       batchqStatus,
		EndTime:      batchqEndTime,
		ArrayTaskVar: "BATCHQ_ARRAY_TASK_ID",
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

// slurmStatus returns the native SLURM JobState word (e.g. "PENDING", "RUNNING",
// "COMPLETED") from `scontrol -o show job`, or "" if the job is unknown (aged out).
func slurmStatus(id string) string {
	out, err := exec.Command("scontrol", "-o", "show", "job", id).Output()
	if err != nil {
		return ""
	}
	for _, tok := range strings.Fields(string(out)) {
		if kv := strings.SplitN(tok, "=", 2); len(kv) == 2 && kv[0] == "JobState" {
			return kv[1]
		}
	}
	return ""
}

// slurmState maps a SLURM JobState (from `scontrol show job`) to the report
// vocabulary; "" means unknown (e.g. the job has aged out of scontrol).
func slurmState(id string) string {
	switch slurmStatus(id) {
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

// slurmEndTime returns a job's completion time (Unix seconds) from the
// EndTime field of `scontrol -o show job`, with ok=false when the field is
// absent, not yet known ("Unknown"/"None"), or unparseable. SLURM prints it as
// a local-time "YYYY-MM-DDTHH:MM:SS" timestamp.
func slurmEndTime(id string) (int64, bool) {
	out, err := exec.Command("scontrol", "-o", "show", "job", id).Output()
	if err != nil {
		return 0, false
	}
	for _, tok := range strings.Fields(string(out)) {
		kv := strings.SplitN(tok, "=", 2)
		if len(kv) != 2 || kv[0] != "EndTime" {
			continue
		}
		if kv[1] == "Unknown" || kv[1] == "None" || kv[1] == "" {
			return 0, false
		}
		t, err := time.ParseInLocation("2006-01-02T15:04:05", kv[1], time.Local)
		if err != nil {
			return 0, false
		}
		return t.Unix(), true
	}
	return 0, false
}

// batchqStatus returns the status word BatchQ reports for a job (e.g. "RUNNING",
// "SUCCESS"), or "" if the job is unknown or the query fails. `batchq status <id>`
// prints one "<jobid> <STATUS>" line per job and exits 0 even for finished jobs —
// so the status word, not the exit code, is what tells active from done.
func batchqStatus(id string) string {
	out, err := exec.Command("batchq", "status", id).Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == id {
			return f[1]
		}
	}
	return ""
}

// batchqActive reports whether a BatchQ job is still pending or running. The end
// states (SUCCESS, FAILED, CANCELED) are NOT active, so a canceled/finished job
// is treated as stale and resubmitted rather than reused.
func batchqActive(id string) bool {
	switch batchqStatus(id) {
	case "USERHOLD", "WAITING", "QUEUED", "PROXYQUEUED", "RUNNING":
		return true
	}
	return false
}

// batchqState maps a BatchQ status to the report vocabulary; "" means unknown.
func batchqState(id string) string {
	switch batchqStatus(id) {
	case "USERHOLD", "WAITING", "QUEUED", "PROXYQUEUED":
		return "queued"
	case "RUNNING":
		return "running"
	case "SUCCESS":
		return "done"
	case "FAILED", "CANCELED":
		return "failed"
	}
	return ""
}

// batchqEndTime returns a job's completion time (Unix seconds) via `batchq status
// -e <id>`. The -sbet flags ask batchq to append times to the status line
// (s=submit, b=begin, e=end, t=wall); with -e the line is "<jobid> <STATUS>
// <end>", where <end> is an RFC3339 UTC timestamp (e.g. 2026-06-10T12:34:56Z — no
// spaces, so the space-delimited line stays parseable). ok=false when the job is
// unknown or has no end time yet (e.g. still running, where the field is absent or
// not a valid timestamp).
func batchqEndTime(id string) (int64, bool) {
	out, err := exec.Command("batchq", "status", "-e", id).Output()
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && f[0] == id {
			if t, err := time.Parse(time.RFC3339, f[2]); err == nil {
				return t.Unix(), true
			}
		}
	}
	return 0, false
}

// sgeStatus returns the SGE state code (e.g. "r", "qw", "Eqw") for a job, or ""
// if the job is no longer listed. `qstat` prints one row per job with the state
// in the 5th whitespace-separated column; a finished job drops off the list.
func sgeStatus(id string) string {
	out, err := exec.Command("qstat").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) >= 5 && f[0] == id {
			return f[4]
		}
	}
	return ""
}

// sgeActive reports whether an SGE job is still queued, running, or suspended.
// Unlike a bare `qstat -j` exit-code check, an error (Eqw) or deletion (dr/dt)
// state counts as stale, so the target is resubmitted rather than reused.
func sgeActive(id string) bool {
	switch sgeStatus(id) {
	case "qw", "hqw", "hRwq", "r", "t", "Rr", "Rt", "s", "S", "T":
		return true
	}
	return false
}

// sgeState maps an SGE state code to the report vocabulary; "" means unknown.
func sgeState(id string) string {
	switch st := sgeStatus(id); st {
	case "qw", "hqw", "hRwq":
		return "queued"
	case "r", "t", "Rr", "Rt", "s", "S", "T":
		return "running"
	case "Eqw", "dr", "dt":
		return "failed"
	}
	return ""
}

// pbsStatus returns the PBS/Torque job_state code (e.g. "R", "Q", "C") for a job,
// or "" if the job is unknown. `qstat -f <id>` prints "job_state = <code>" among
// its `key = value` lines; a finished job eventually drops off (qstat exits != 0).
func pbsStatus(id string) string {
	out, err := exec.Command("qstat", "-f", id).Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		kv := strings.SplitN(strings.TrimSpace(line), " = ", 2)
		if len(kv) == 2 && kv[0] == "job_state" {
			return strings.TrimSpace(kv[1])
		}
	}
	return ""
}

// pbsActive reports whether a PBS/Torque job is still queued, running, or held.
// Completed (C) or exiting (E) jobs — which `qstat` keeps listing for a while —
// count as stale, so the target is resubmitted rather than reused.
func pbsActive(id string) bool {
	switch pbsStatus(id) {
	case "Q", "R", "H":
		return true
	}
	return false
}

// pbsState maps a PBS job_state to the report vocabulary; "" means unknown.
func pbsState(id string) string {
	switch pbsStatus(id) {
	case "Q", "H", "W", "T":
		return "queued"
	case "R", "E", "S":
		return "running"
	case "C":
		return "done"
	}
	return ""
}

// For returns the scheduler with the given name.
func For(name string) (Scheduler, bool) {
	s, ok := schedulers[name]
	return s, ok
}

// schedulerNames is the supported scheduler names in display order. It is the
// source of truth for Names() and is kept in sync with the schedulers map by
// TestSchedulerNamesMatchMap.
var schedulerNames = []string{"slurm", "sge", "pbs", "batchq"}

// Names lists the supported scheduler names.
func Names() []string { return schedulerNames }

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
	if err := b.resolveTemplate(); err != nil {
		return nil, err
	}
	return b, nil
}

// resolveTemplate lets a site override a scheduler's built-in submission template
// while keeping the rest of its wiring (submit command, status probes, mem
// normalization). Two sources, in priority order:
//
//  1. cgp.runner.<name>.template = "<path>" — explicit and per-scheduler, via
//     normal config layering. A set-but-unreadable/empty path is a loud error.
//  2. ~/.cgp/custom_template.cgp — a single zero-config convention file applied to
//     whichever scheduler runner is active (most users target one cluster). Absent
//     or empty ⇒ silently keep the built-in.
//
// On success it replaces b.sch.Template (a copy, safe to mutate) and records the
// source path in b.templateSrc for error messages.
func (b *backend) resolveTemplate() error {
	name := b.sch.Name
	if path := b.cfg("cgp.runner." + name + ".template"); path != "" {
		full := expandTilde(path)
		data, err := os.ReadFile(full)
		if err != nil {
			return fmt.Errorf("runner %s: custom template %q: %w", name, path, err)
		}
		if strings.TrimSpace(string(data)) == "" {
			return fmt.Errorf("runner %s: custom template %q is empty", name, path)
		}
		b.sch.Template = string(data)
		b.templateSrc = full
		return nil
	}
	if home, err := os.UserHomeDir(); err == nil {
		conv := filepath.Join(home, ".cgp", "custom_template.cgp")
		if data, err := os.ReadFile(conv); err == nil && strings.TrimSpace(string(data)) != "" {
			b.sch.Template = string(data)
			b.templateSrc = conv
		}
	}
	return nil
}

// expandTilde expands a leading ~ or ~/ to the user's home directory; other paths
// pass through unchanged.
func expandTilde(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p[1:], "/"))
		}
	}
	return p
}

func (b *backend) closeLedger() {
	if b.ledger != nil {
		b.ledger.Close()
	}
}

type backend struct {
	prog        *eval.Program
	sch         Scheduler
	opts        Options
	shell       string
	globalHold  bool
	ledger      *ledger.Ledger
	runID       string
	user        string
	wd          string // effective working directory recorded in the ledger
	templateSrc string // path a custom template was loaded from ("" ⇒ built-in)
	dryN        int
	ids         []string
	held        []string
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
	// Cross-run reuse: if an existing job still owns any of this target's outputs
	// and is still active in the scheduler, depend on it instead of resubmitting.
	// Check every output (not just the first): a target's outputs are produced by
	// one job, but the first output may be unowned while a later one is owned.
	if b.ledger != nil {
		for _, out := range t.Outputs {
			owner, ok, err := b.ledger.OwnerOf(out)
			if err != nil || !ok || owner == "" {
				continue
			}
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
	if v, ok := vars["job.shexec"]; ok && eval.Truthy(v) {
		return b.shExec(runner.Label(t), body)
	}
	// An integer job.array is a pipeline array-membership marker (the element's task
	// index), consumed by the driver/SubmitArray — a lone target is not an array, so
	// strip it here. A string job.array (a literal index spec, e.g. from
	// `cgp sub --array`) is kept and rendered into the array directive.
	if v, ok := vars["job.array"]; ok {
		if _, isInt := v.(eval.IntVal); isInt {
			delete(vars, "job.array")
		}
	}
	b.finalizeVars(vars, body, t.Inputs, t.Outputs, deps)

	script, err := b.prog.RenderText(b.sch.Template, vars)
	if err != nil {
		src := b.templateSrc
		if src == "" {
			src = "built-in"
		}
		return "", fmt.Errorf("runner %s: rendering template (%s): %w", b.sch.Name, src, err)
	}
	id, err := b.submitScript(runner.Label(t), script)
	if err != nil {
		return "", err
	}
	if b.ledger != nil && id != "" && len(t.Outputs) > 0 {
		if err := b.ledger.Record(ledger.Job{
			JobID: id, RunID: b.runID, Name: eval.Stringify(vars["job.name"]), Pipeline: b.opts.Pipeline,
			WorkingDir: b.wd, User: b.user, SubmitTime: time.Now().Unix(),
			Outputs: t.Outputs, Temp: t.Temp, Inputs: t.Inputs, Deps: deps,
			Script: body, Settings: jobSettings(vars),
		}); err != nil {
			return "", fmt.Errorf("ledger record: %w", err)
		}
	}
	return id, nil
}

// finalizeVars applies the scheduler/config normalization shared by Submit and
// SubmitArray: mem/gpu normalization, runner-owned defaults, the body and
// input/output lists, and the dependency directive.
func (b *backend) finalizeVars(vars map[string]eval.Value, body string, inputs, outputs, deps []string) {
	if m, ok := vars["job.mem"]; ok && b.sch.PrepareMem != nil {
		vars["job.mem"] = eval.StrVal(b.sch.PrepareMem(eval.Stringify(m)))
	}
	// Normalize a boolean gpu (job.gpu = true) to a count for the scheduler directive.
	if v, ok := vars["job.gpu"]; ok {
		if bb, isBool := v.(eval.BoolVal); isBool {
			if bool(bb) {
				vars["job.gpu"] = eval.IntVal(1)
			} else {
				delete(vars, "job.gpu")
			}
		}
	}
	// job.procs / job.name / job.custom / job.setup come pre-seeded from eval; the
	// runner-owned settings below default here.
	setDefault(vars, "job.shell", eval.StrVal(b.shell))
	if _, ok := vars["job.mail"]; ok {
		setDefault(vars, "job.mailtype", eval.StrVal(b.sch.MailType))
	}
	vars["_body"] = eval.StrVal(body)
	vars["_inputs"] = eval.StrList(inputs)
	vars["_outputs"] = eval.StrList(outputs)
	if len(deps) > 0 {
		vars["job.depids"] = eval.StrVal(strings.Join(deps, b.sch.DepSep))
	}
	if pe := b.cfg("cgp.runner.sge.parallelenv"); pe != "" {
		setDefault(vars, "job.parallelenv", eval.StrVal(pe))
	}
	if rid := b.cfg("cgp.run_id"); rid != "" {
		setDefault(vars, "job.run_id", eval.StrVal(rid))
	}
}

// SubmitArray submits a group of array-member targets (parallel indices[]) as a
// single scheduler job array, returning each member's per-task job id
// (<arrayid>_<index>). The members must be submission-compatible (same job.*
// settings apart from the index); the differing per-element commands become the
// branches of a `case` keyed by the scheduler's task-id variable. Schedulers
// without array support (ArrayTaskVar == "") fall back to one job per element.
func (b *backend) SubmitArray(members []*eval.Target, indices []int, deps []string) (map[*eval.Target]string, error) {
	deps = dedupeIDs(deps)
	if b.sch.ArrayTaskVar == "" {
		out := make(map[*eval.Target]string, len(members))
		for _, m := range members {
			id, err := b.Submit(m, deps)
			if err != nil {
				return nil, err
			}
			out[m] = id
		}
		return out, nil
	}

	type elem struct {
		t    *eval.Target
		idx  int
		body string
	}
	var elems []elem
	var baseVars map[string]eval.Value
	var baseSig string
	for i, m := range members {
		vars, body, err := b.prog.JobContext(m)
		if err != nil {
			return nil, err
		}
		sig := arraySignature(vars)
		if i == 0 {
			baseVars, baseSig = vars, sig
		} else if sig != baseSig {
			return nil, fmt.Errorf("array %q: element %q is not submission-compatible with the first element "+
				"(differing job.* settings) — all array tasks share one set of resources/name; "+
				"split the divergent element out or drop job.array",
				runner.Label(members[0]), runner.Label(m))
		}
		elems = append(elems, elem{m, indices[i], body})
	}
	sort.Slice(elems, func(i, j int) bool { return elems[i].idx < elems[j].idx })

	// Index spec for the --array header (e.g. "1,2,4") and the dispatch table.
	var spec, casebody strings.Builder
	fmt.Fprintf(&casebody, "case \"$%s\" in\n", b.sch.ArrayTaskVar)
	var allIn, allOut []string
	for i, e := range elems {
		if i > 0 {
			spec.WriteByte(',')
		}
		spec.WriteString(strconv.Itoa(e.idx))
		fmt.Fprintf(&casebody, "%d)\n%s\n;;\n", e.idx, e.body)
		allIn = append(allIn, e.t.Inputs...)
		allOut = append(allOut, e.t.Outputs...)
	}
	fmt.Fprintf(&casebody, "*) echo \"cgp: no array task $%s\" >&2; exit 1 ;;\nesac\n", b.sch.ArrayTaskVar)

	vars := baseVars
	vars["job.array"] = eval.StrVal(spec.String())
	b.finalizeVars(vars, casebody.String(), uniqueStrings(allIn), uniqueStrings(allOut), deps)

	script, err := b.prog.RenderText(b.sch.Template, vars)
	if err != nil {
		src := b.templateSrc
		if src == "" {
			src = "built-in"
		}
		return nil, fmt.Errorf("runner %s: rendering array template (%s): %w", b.sch.Name, src, err)
	}
	baseID, err := b.submitScript(runner.Label(members[0])+"[]", script)
	if err != nil {
		return nil, err
	}

	// Each element's outputs are owned by its own task id (<base>_<index>), so a
	// downstream dependency resolves to the exact task that produces what it needs.
	out := make(map[*eval.Target]string, len(elems))
	for _, e := range elems {
		taskID := baseID + "_" + strconv.Itoa(e.idx)
		out[e.t] = taskID
		if b.ledger != nil && baseID != "" && len(e.t.Outputs) > 0 {
			if err := b.ledger.Record(ledger.Job{
				JobID: taskID, RunID: b.runID, Name: eval.Stringify(vars["job.name"]), Pipeline: b.opts.Pipeline,
				WorkingDir: b.wd, User: b.user, SubmitTime: time.Now().Unix(),
				Outputs: e.t.Outputs, Temp: e.t.Temp, Inputs: e.t.Inputs, Deps: deps,
				Script: e.body, Settings: jobSettings(vars),
			}); err != nil {
				return nil, fmt.Errorf("ledger record: %w", err)
			}
		}
	}
	return out, nil
}

// arraySignature is a deterministic fingerprint of a member's job.* settings,
// excluding job.array (the per-element index). Members of one array must share it.
func arraySignature(vars map[string]eval.Value) string {
	var keys []string
	for k := range vars {
		if strings.HasPrefix(k, "job.") && k != "job.array" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%s;", k, eval.Stringify(vars[k]))
	}
	return b.String()
}

// uniqueStrings returns ss with duplicates removed, preserving first-seen order.
func uniqueStrings(ss []string) []string {
	seen := make(map[string]bool, len(ss))
	out := ss[:0:0]
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// jobSettings extracts the per-job settings worth recording (mem, procs,
// walltime, container, gpu, …) from the render vars: scalar job.* values, minus
// the name (recorded separately as NAME) and the internal plumbing. Keys are
// recorded with the job. prefix stripped, keeping the ledger schema stable.
func jobSettings(vars map[string]eval.Value) map[string]string {
	skip := map[string]bool{
		"name":   true, // recorded as NAME
		"depids": true, "custom": true, "setup": true, "shell": true,
		"run_id": true, "parallelenv": true, "mailtype": true, "array": true,
	}
	out := map[string]string{}
	for k, v := range vars {
		bare, ok := strings.CutPrefix(k, "job.")
		if !ok || skip[bare] {
			continue
		}
		switch v.(type) {
		case eval.StrVal, eval.IntVal, eval.FloatVal, eval.BoolVal:
			out[bare] = eval.Stringify(v)
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
