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

	"github.com/compgen-io/cgp/internal/eval"
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
}

var schedulers = map[string]Scheduler{
	"slurm": {
		Name: "slurm", Template: slurmTmpl,
		SubCmd: []string{"sbatch", "--parsable"}, HoldArgs: []string{"-H"},
		DepSep: ":", MailType: "END,FAIL", PrepareMem: slurmMem,
		ReleaseCmd: func(id string) []string { return []string{"scontrol", "release", id} },
	},
	"sge": {
		Name: "sge", Template: sgeTmpl,
		SubCmd: []string{"qsub"}, HoldArgs: []string{"-h", "u"},
		DepSep: ",", MailType: "ae",
		ReleaseCmd: func(id string) []string { return []string{"qrls", id} },
	},
	"pbs": {
		Name: "pbs", Template: pbsTmpl,
		SubCmd: []string{"qsub"}, HoldArgs: []string{"-h"},
		DepSep: ":", MailType: "abe", PrepareMem: pbsMem,
		ReleaseCmd: func(id string) []string { return []string{"qrls", id} },
	},
	"batchq": {
		Name: "batchq", Template: batchqTmpl,
		SubCmd: []string{"batchq", "submit"}, HoldArgs: []string{"--hold"},
		DepSep:     ",",
		ReleaseCmd: func(id string) []string { return []string{"batchq", "release", id} },
	},
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
	Goals  []string
	DryRun bool
	Dir    string
	Out    io.Writer // submission scripts (dry-run) and job-id output
}

// Run builds the program's goals by submitting stale targets to the scheduler.
func Run(p *eval.Program, sch Scheduler, opts Options) error {
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
	b := &backend{prog: p, sch: sch, opts: opts, shell: shell, globalHold: gh}
	if err := runner.Build(p, b, runner.Options{Goals: opts.Goals, Dir: opts.Dir}); err != nil {
		return err
	}
	return b.finish()
}

type backend struct {
	prog       *eval.Program
	sch        Scheduler
	opts       Options
	shell      string
	globalHold bool
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

func (b *backend) Submit(t *eval.Target, deps []string) (string, error) {
	vars, body, err := b.prog.JobContext(t)
	if err != nil {
		return "", err
	}
	if m, ok := vars["mem"]; ok && b.sch.PrepareMem != nil {
		vars["mem"] = eval.StrVal(b.sch.PrepareMem(eval.Stringify(m)))
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
	vars["_inputs"] = strList(t.Inputs)
	vars["_outputs"] = strList(t.Outputs)
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
	return b.submitScript(runner.Label(t), script)
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

func strList(ss []string) eval.ListVal {
	out := make(eval.ListVal, len(ss))
	for i, s := range ss {
		out[i] = eval.StrVal(s)
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
