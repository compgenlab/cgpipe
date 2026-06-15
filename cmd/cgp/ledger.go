package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/compgen-io/cgp/internal/ast"
	"github.com/compgen-io/cgp/internal/eval"
	"github.com/compgen-io/cgp/internal/ledger"
	"github.com/compgen-io/cgp/internal/runner/sched"
)

const ledgerUsage = `usage:
    cgp ledger dump <dir>                      dump all jobs as key/value TSV
    cgp ledger search [filters] <dir>          dump jobs matching the filters
    cgp ledger status [-r RUNNER] [-output] <dir>   show each job's (or output's) live scheduler status
    cgp ledger vacuum <dir>                     compact the ledger, dropping jobs that own no current output

search filters (substring match; combined with AND):
    -i PATH      an input path contains PATH
    -o PATH      an output path contains PATH
    -g PATTERN   a job-script line contains PATTERN (grep)
    -name NAME   the job name contains NAME
    -id JOBID    a job id, or an array id (matches all its tasks)

status options:
    -r RUNNER    scheduler to query (slurm/sge/pbs/batchq); defaults to cgp.runner from config
    -output      report per output file (most recent owning job's status) instead of per job
`

// runLedger handles `cgp ledger <subcommand> ...`.
func runLedger(args []string) int {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, ledgerUsage)
		return 2
	}
	switch args[0] {
	case "dump":
		return runLedgerDump(args[1:])
	case "search":
		return runLedgerSearch(args[1:])
	case "status":
		return runLedgerStatus(args[1:])
	case "vacuum":
		if len(args) < 2 {
			fmt.Fprint(os.Stderr, ledgerUsage)
			return 2
		}
		lg, err := ledger.Open(args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
			return 1
		}
		defer lg.Close()
		if err := lg.Vacuum(); err != nil {
			fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
			return 1
		}
		return 0
	default:
		fmt.Fprint(os.Stderr, ledgerUsage)
		return 2
	}
}

// runLedgerDump handles `cgp ledger dump <db>`.
func runLedgerDump(args []string) int {
	if len(args) != 1 {
		fmt.Fprint(os.Stderr, ledgerUsage)
		return 2
	}
	lg, err := ledger.OpenRead(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
		return 1
	}
	defer lg.Close()
	if err := lg.Dump(os.Stdout, nil); err != nil {
		fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
		return 1
	}
	return 0
}

// runLedgerSearch handles `cgp ledger search [filters] <db>`.
func runLedgerSearch(args []string) int {
	var f ledger.Filter
	var db string
	c := newArgCursor(args)
	for c.more() {
		a := c.cur()
		// val consumes a filter's value; on a missing value it prints usage and
		// the caller returns 2.
		val := func() (string, bool) {
			v, ok := c.value()
			if !ok {
				fmt.Fprint(os.Stderr, ledgerUsage)
			}
			return v, ok
		}
		switch a {
		case "-i":
			v, ok := val()
			if !ok {
				return 2
			}
			f.Input = v
		case "-o":
			v, ok := val()
			if !ok {
				return 2
			}
			f.Output = v
		case "-g":
			v, ok := val()
			if !ok {
				return 2
			}
			f.Grep = v
		case "-name":
			v, ok := val()
			if !ok {
				return 2
			}
			f.Name = v
		case "-id":
			v, ok := val()
			if !ok {
				return 2
			}
			f.ID = v
		default:
			if strings.HasPrefix(a, "-") || db != "" {
				fmt.Fprint(os.Stderr, ledgerUsage)
				return 2
			}
			db = a
			c.advance()
		}
	}
	if db == "" || (f == ledger.Filter{}) {
		fmt.Fprint(os.Stderr, ledgerUsage)
		return 2
	}
	lg, err := ledger.OpenRead(db)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
		return 1
	}
	defer lg.Close()
	ids, err := lg.Search(f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
		return 1
	}
	if len(ids) == 0 {
		return 0 // no matches: dump nothing (an empty set is not "everything")
	}
	if err := lg.Dump(os.Stdout, ids); err != nil {
		fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
		return 1
	}
	return 0
}

// runLedgerStatus handles `cgp ledger status [-r RUNNER] [-output] <dir>`.
func runLedgerStatus(args []string) int {
	var runnerName, dir string
	outputMode := false
	c := newArgCursor(args)
	for c.more() {
		a := c.cur()
		switch a {
		case "-output":
			outputMode = true
			c.advance()
		case "-r":
			v, ok := c.value()
			if !ok {
				fmt.Fprint(os.Stderr, ledgerUsage)
				return 2
			}
			runnerName = v
		default:
			if strings.HasPrefix(a, "-") || dir != "" {
				fmt.Fprint(os.Stderr, ledgerUsage)
				return 2
			}
			dir = a
			c.advance()
		}
	}

	// Resolve the runner and ledger dir from config when not given explicitly,
	// mirroring `cgp sub`: evaluate the config layers with an empty pipeline.
	if runnerName == "" || dir == "" {
		cfgs, err := loadConfigs()
		if err != nil {
			fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
			return 1
		}
		base, err := eval.Run(&ast.File{}, eval.Options{Configs: cfgs})
		if err != nil {
			fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
			return 1
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
	}

	if dir == "" {
		fmt.Fprint(os.Stderr, ledgerUsage)
		return 2
	}
	sch, ok := sched.For(runnerName)
	if !ok {
		fmt.Fprintf(os.Stderr, "cgp: ledger status needs a scheduler runner (%s), got %q\n",
			strings.Join(sched.Names(), "/"), runnerName)
		return 2
	}
	lg, err := ledger.OpenRead(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
		return 1
	}
	defer lg.Close()
	if err := ledgerStatus(os.Stdout, sch, lg, outputMode); err != nil {
		fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
		return 1
	}
	return 0
}

// ledgerStatus writes a job- or output-oriented status table to w, probing the
// scheduler for each job's live state. Probes are memoized per job id so outputs
// that share an owning job query the scheduler once.
//
// Job mode (one row per recorded job): "<jobid>\t<STATUS>\t<name>", where STATUS
// is the scheduler's native status word or UNKNOWN if the job has aged out.
//
// Output mode (one row per owned output): "<output>\t<jobid>\t<STATUS>". For a
// job still queued/running/failed STATUS is the native word; for a finished or
// aged-out job the file's mtime is cross-checked against the job's submit/end
// window — COMPLETE (aged out, file present and not older than submit) or DIRTY
// (missing, too old, or modified after the job's end).
func ledgerStatus(w io.Writer, sch sched.Scheduler, lg *ledger.Ledger, outputMode bool) error {
	type probe struct {
		state, status string
		end           int64
		endOK         bool
	}
	cache := map[string]probe{}
	probeJob := func(id string) probe {
		if p, ok := cache[id]; ok {
			return p
		}
		var p probe
		if sch.Status != nil {
			p.status = sch.Status(id)
		}
		if sch.State != nil {
			p.state = sch.State(id)
		}
		if sch.EndTime != nil {
			p.end, p.endOK = sch.EndTime(id)
		}
		cache[id] = p
		return p
	}

	if !outputMode {
		for _, j := range lg.Jobs() {
			st := probeJob(j.JobID).status
			if st == "" {
				st = "UNKNOWN"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\n", j.JobID, st, j.Name)
		}
		return nil
	}

	byID := map[string]ledger.Job{}
	for _, j := range lg.Jobs() {
		byID[j.JobID] = j
	}
	owners := lg.Owners()
	paths := make([]string, 0, len(owners))
	for p := range owners {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, path := range paths {
		id := owners[path]
		p := probeJob(id)
		st := outputStatus(path, byID[id].SubmitTime, p.state, p.status, p.end, p.endOK)
		fmt.Fprintf(w, "%s\t%s\t%s\n", path, id, st)
	}
	return nil
}

// outputStatus reports the status of one output file given its owning job's
// submit time and live scheduler state. A still-active or failed job reports its
// native status word; a finished or aged-out job is cross-checked against the
// file's mtime (COMPLETE/DIRTY). The end-time upper bound is best-effort: enforced
// only when the scheduler exposed one (endOK).
func outputStatus(path string, submit int64, state, status string, end int64, endOK bool) string {
	mtime, exists := statMtime(path)
	switch state {
	case "queued", "running", "failed":
		return status
	case "done":
		if exists && mtime >= submit && (!endOK || mtime <= end+300) {
			return status
		}
		return "DIRTY"
	default: // "" — unknown / aged out of the scheduler
		if exists && mtime >= submit {
			return "COMPLETE"
		}
		return "DIRTY"
	}
}

// statMtime returns a file's modification time in Unix seconds, ok=false if the
// file does not exist (or cannot be stat'd).
func statMtime(path string) (int64, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	return fi.ModTime().Unix(), true
}
