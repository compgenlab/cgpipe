package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/compgenlab/cgpipe/internal/ast"
	"github.com/compgenlab/cgpipe/internal/eval"
	"github.com/compgenlab/cgpipe/internal/ledger"
	"github.com/compgenlab/cgpipe/internal/runner/sched"
)

const statusUsage = `usage:
    cgp status [--json] [-r RUNNER] [--ledger <dir>] [JOBID ...]

Show each job's normalized live status. With no JOBID, every job that currently
owns an output in the ledger is reported (the latest producer per output). Give
one or more JOBIDs to report just those.

options:
    --json         emit a JSON array of job objects (for tools); default is a table
    -r RUNNER      scheduler to query (slurm/sge/pbs/batchq); defaults to cgp.runner from config
    -l, --ledger DIR   ledger directory; defaults to cgp.ledger from config
    <dir>          a positional existing directory is taken as the ledger dir

state is normalized to: queued | running | done | failed | cancelled | unknown
(the raw scheduler word is native_state in --json). Contrast cgp ledger status,
which prints the native word.
`

// jobStatusJSON is the machine-readable shape emitted by `cgp status --json`.
// Tier-1 fields (job_id, name, state, native_state, reason, exit_code) always
// drive the dashboard; the rest are best-effort and omitted when unavailable.
type jobStatusJSON struct {
	JobID       string `json:"job_id"`
	ArrayID     string `json:"array_id,omitempty"`
	TaskIndex   *int   `json:"task_index,omitempty"`
	Name        string `json:"name,omitempty"`
	State       string `json:"state"`
	NativeState string `json:"native_state,omitempty"`
	Reason      string `json:"reason,omitempty"`
	ExitCode    *int   `json:"exit_code,omitempty"`

	SubmitTime string `json:"submit_time,omitempty"`
	StartTime  string `json:"start_time,omitempty"`
	EndTime    string `json:"end_time,omitempty"`
	Elapsed    string `json:"elapsed,omitempty"`
	TimeLimit  string `json:"time_limit,omitempty"`

	Nodes     string `json:"nodes,omitempty"`
	Partition string `json:"partition,omitempty"`
	CPUs      string `json:"cpus,omitempty"`
	MemReq    string `json:"mem_req,omitempty"`
	MemUsed   string `json:"mem_used,omitempty"`

	Account    string   `json:"account,omitempty"`
	User       string   `json:"user,omitempty"`
	WorkDir    string   `json:"work_dir,omitempty"`
	StdoutPath string   `json:"stdout_path,omitempty"`
	StderrPath string   `json:"stderr_path,omitempty"`
	Deps       []string `json:"deps,omitempty"`

	Pipeline string   `json:"pipeline,omitempty"`
	RunID    string   `json:"run_id,omitempty"`
	Outputs  []string `json:"outputs,omitempty"`
}

// statusTarget is one job to report: its id, the ledger record (if known), and
// the outputs it currently owns.
type statusTarget struct {
	id      string
	job     ledger.Job
	outputs []string
}

// runStatus handles `cgp status [--json] [-r RUNNER] [--ledger <dir>] [JOBID ...]`.
// It reports each job's normalized live state (queued/running/done/failed/
// cancelled/unknown) — the scheduler-agnostic, enriched counterpart of `cgp
// ledger status` (which prints native scheduler words as TSV).
func runStatus(args []string) int {
	var runnerName, dir string
	var jobIDs []string
	jsonOut := false
	c := newArgCursor(args)
	for c.more() {
		a := c.cur()
		switch a {
		case "-h", "--help":
			fmt.Fprint(os.Stdout, statusUsage)
			return 0
		case "--json":
			jsonOut = true
			c.advance()
		case "-r":
			v, ok := c.value()
			if !ok {
				fmt.Fprint(os.Stderr, statusUsage)
				return 2
			}
			runnerName = v
		case "-l", "--ledger":
			v, ok := c.value()
			if !ok {
				fmt.Fprint(os.Stderr, statusUsage)
				return 2
			}
			dir = v
		default:
			if strings.HasPrefix(a, "-") {
				fmt.Fprint(os.Stderr, statusUsage)
				return 2
			}
			// A positional that is an existing directory is the ledger dir (as in
			// `cgp ledger status`); anything else is a job id.
			if info, err := os.Stat(a); dir == "" && err == nil && info.IsDir() {
				dir = a
			} else {
				jobIDs = append(jobIDs, a)
			}
			c.advance()
		}
	}

	var err error
	if runnerName, dir, err = resolveRunnerAndLedger(runnerName, dir); err != nil {
		fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
		return 1
	}
	sch, ok := sched.For(runnerName)
	if !ok {
		fmt.Fprintf(os.Stderr, "cgp: status needs a scheduler runner (%s), got %q\n",
			strings.Join(sched.Names(), "/"), runnerName)
		return 2
	}

	var lg *ledger.Ledger
	if dir != "" {
		l, err := ledger.OpenRead(dir)
		if err != nil {
			// With explicit job ids the ledger is only for enrichment, so a missing
			// one is non-fatal; without ids we have nothing to enumerate.
			if len(jobIDs) == 0 {
				fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
				return 1
			}
		} else {
			lg = l
			defer lg.Close()
		}
	}
	if lg == nil && len(jobIDs) == 0 {
		fmt.Fprint(os.Stderr, statusUsage)
		return 2
	}

	targets := statusTargets(lg, jobIDs)
	rows := make([]jobStatusJSON, 0, len(targets))
	cache := map[string]sched.JobDetail{}
	okCache := map[string]bool{}
	for _, t := range targets {
		d, ok := cache[t.id]
		if _, done := okCache[t.id]; !done {
			if sch.Detail != nil {
				d, ok = sch.Detail(t.id)
			}
			cache[t.id], okCache[t.id] = d, ok
		} else {
			ok = okCache[t.id]
		}
		rows = append(rows, buildJobStatus(t, d, ok))
	}

	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rows); err != nil {
			fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
			return 1
		}
		return 0
	}
	writeStatusTable(os.Stdout, rows)
	return 0
}

// statusTargets resolves the jobs to report. With explicit ids, each is looked up
// in the ledger (for enrichment). Otherwise every current owner (latest producer
// per output, last-write-wins) is reported, ordered by submit time then id, with
// its owned outputs attached.
func statusTargets(lg *ledger.Ledger, jobIDs []string) []statusTarget {
	byID := map[string]ledger.Job{}
	var ordered []ledger.Job
	if lg != nil {
		ordered = lg.Jobs()
		for _, j := range ordered {
			byID[j.JobID] = j
		}
	}
	if len(jobIDs) > 0 {
		targets := make([]statusTarget, 0, len(jobIDs))
		for _, id := range jobIDs {
			j := byID[id]
			targets = append(targets, statusTarget{id: id, job: j, outputs: j.Outputs})
		}
		return targets
	}

	outsByID := map[string][]string{}
	for out, id := range lg.Owners() {
		outsByID[id] = append(outsByID[id], out)
	}
	for _, outs := range outsByID {
		sort.Strings(outs)
	}
	targets := make([]statusTarget, 0, len(outsByID))
	for _, j := range ordered { // Jobs() order: submit time, then id
		if outs, ok := outsByID[j.JobID]; ok {
			targets = append(targets, statusTarget{id: j.JobID, job: j, outputs: outs})
			delete(outsByID, j.JobID)
		}
	}
	// Any owner id without a folded job record (shouldn't happen) — report bare.
	leftover := make([]string, 0, len(outsByID))
	for id := range outsByID {
		leftover = append(leftover, id)
	}
	sort.Strings(leftover)
	for _, id := range leftover {
		targets = append(targets, statusTarget{id: id, outputs: outsByID[id]})
	}
	return targets
}

// buildJobStatus merges a scheduler probe (d, ok) with the ledger record into one
// report row. When the scheduler no longer knows the job (ok=false), the state is
// reconciled from disk: "done" if every owned output exists and is at least as new
// as the submit time, else "unknown".
func buildJobStatus(t statusTarget, d sched.JobDetail, ok bool) jobStatusJSON {
	j := t.job
	js := jobStatusJSON{
		JobID:    t.id,
		ArrayID:  j.ArrayID,
		Name:     j.Name,
		WorkDir:  j.WorkingDir,
		User:     j.User,
		Deps:     j.Deps,
		Pipeline: j.Pipeline,
		RunID:    j.RunID,
		Outputs:  t.outputs,
	}
	if j.ArrayID != "" {
		ti := j.TaskIndex
		js.TaskIndex = &ti
	}
	submit := j.SubmitTime
	if ok {
		js.State = d.State
		js.NativeState = d.NativeState
		js.Reason = d.Reason
		if d.HasExit {
			ec := d.ExitCode
			js.ExitCode = &ec
		}
		if d.SubmitTime != 0 {
			submit = d.SubmitTime
		}
		if d.WorkDir != "" {
			js.WorkDir = d.WorkDir
		}
		if d.User != "" {
			js.User = d.User
		}
		js.StartTime = rfc3339(d.StartTime)
		js.EndTime = rfc3339(d.EndTime)
		js.Elapsed = d.Elapsed
		js.TimeLimit = d.TimeLimit
		js.Nodes = d.Nodes
		js.Partition = d.Partition
		js.CPUs = d.CPUs
		js.MemReq = d.MemReq
		js.MemUsed = d.MemUsed
		js.Account = d.Account
		js.StdoutPath = d.StdoutPath
		js.StderrPath = d.StderrPath
	} else {
		js.State = reconcileAgedOut(submit, t.outputs)
	}
	js.SubmitTime = rfc3339(submit)
	return js
}

// reconcileAgedOut infers a state for a job the scheduler has forgotten: "done"
// when it owns at least one output and every owned output exists on disk no older
// than the submit time, otherwise "unknown".
func reconcileAgedOut(submit int64, outputs []string) string {
	if len(outputs) == 0 {
		return "unknown"
	}
	for _, o := range outputs {
		mt, exists := statMtime(o)
		if !exists || mt < submit {
			return "unknown"
		}
	}
	return "done"
}

// rfc3339 formats Unix seconds as an RFC3339 UTC timestamp, or "" for a zero/unset
// time (so it is omitted from JSON).
func rfc3339(sec int64) string {
	if sec <= 0 {
		return ""
	}
	return time.Unix(sec, 0).UTC().Format(time.RFC3339)
}

// writeStatusTable prints the human view: one "<job_id>\t<state>\t<name>" row per
// job, with a trailing reason column when present.
func writeStatusTable(w io.Writer, rows []jobStatusJSON) {
	for _, r := range rows {
		fields := []string{r.JobID, r.State, r.Name}
		if r.Reason != "" {
			fields = append(fields, r.Reason)
		}
		fmt.Fprintln(w, strings.Join(fields, "\t"))
	}
}

// resolveRunnerAndLedger fills a missing runner name and/or ledger dir from the
// config layers (cgp.runner / cgp.ledger), evaluated with an empty pipeline —
// mirroring `cgp sub`. Values already supplied by the caller are left untouched.
func resolveRunnerAndLedger(runnerName, dir string) (string, string, error) {
	if runnerName != "" && dir != "" {
		return runnerName, dir, nil
	}
	cfgs, err := loadConfigs()
	if err != nil {
		return runnerName, dir, err
	}
	base, err := eval.Run(&ast.File{}, eval.Options{Configs: cfgs})
	if err != nil {
		return runnerName, dir, err
	}
	vars := base.Vars()
	if runnerName == "" {
		if v, ok := vars["cgp.runner"]; ok {
			runnerName = eval.Stringify(v)
		}
	}
	if dir == "" {
		if v, ok := vars["cgp.ledger"]; ok {
			dir = eval.Stringify(v)
		}
	}
	return runnerName, dir, nil
}
