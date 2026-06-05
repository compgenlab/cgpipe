package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/compgen-io/cgp/internal/ast"
	"github.com/compgen-io/cgp/internal/eval"
	"github.com/compgen-io/cgp/internal/runner/sched"
	"github.com/compgen-io/cgp/internal/runner/shell"
)

const subUsage = `cgp sub — submit a one-off command as a job

usage:
    cgp sub [options] -- <command ...>

options:
    -name S        job name
    -mem S         memory (e.g. 8G)
    -procs N       cpus
    -walltime S    wall-time limit
    -o PATH        declared output (repeatable; recorded in the ledger)
    -i PATH        declared input (repeatable)
    -d JOBID       depend on an existing job id (repeatable)
    -after PATH    depend on the active job that owns PATH in the ledger (repeatable)
    -r NAME        runner: shell (default), slurm, sge, pbs, batchq
    -ledger PATH   ledger database
    -dr            dry run

The command (everything after --) is treated as a cgp body: ${input}/${output}
substitute; use $VAR for shell variables.
`

// runSub handles `cgp sub [options] -- command...`.
func runSub(args []string) int {
	var name string
	var outs, ins, deps, after []string
	var runnerName, ledgerPath string
	dryRun := false
	settings := map[string]eval.Value{}
	var cmdParts []string

	c := newArgCursor(args)
	for c.more() {
		a := c.cur()
		if a == "--" {
			cmdParts = args[c.i+1:]
			break
		}
		// val consumes the value after a value-taking flag; on a missing value it
		// prints the sub usage hint and the caller returns 2.
		val := func() (string, bool) {
			v, ok := c.value()
			if !ok {
				fmt.Fprintf(os.Stderr, "cgp sub: %s needs a value\n", a)
			}
			return v, ok
		}
		switch a {
		case "-dr":
			dryRun = true
			c.advance()
		case "-h":
			fmt.Print(subUsage)
			return 0
		case "-name":
			v, ok := val()
			if !ok {
				return 2
			}
			name = v
		case "-mem":
			v, ok := val()
			if !ok {
				return 2
			}
			settings["job.mem"] = eval.StrVal(v)
		case "-procs":
			v, ok := val()
			if !ok {
				return 2
			}
			settings["job.procs"] = eval.ParseScalar(v)
		case "-walltime":
			v, ok := val()
			if !ok {
				return 2
			}
			settings["job.walltime"] = eval.StrVal(v)
		case "-o":
			v, ok := val()
			if !ok {
				return 2
			}
			outs = append(outs, v)
		case "-i":
			v, ok := val()
			if !ok {
				return 2
			}
			ins = append(ins, v)
		case "-d":
			v, ok := val()
			if !ok {
				return 2
			}
			deps = append(deps, v)
		case "-after":
			v, ok := val()
			if !ok {
				return 2
			}
			after = append(after, v)
		case "-r":
			v, ok := val()
			if !ok {
				return 2
			}
			runnerName = v
		case "-ledger":
			v, ok := val()
			if !ok {
				return 2
			}
			ledgerPath = v
		default:
			fmt.Fprintf(os.Stderr, "cgp sub: unknown option %s (put the command after --)\n", a)
			return 2
		}
	}

	if len(cmdParts) == 0 {
		fmt.Fprint(os.Stderr, subUsage)
		return 2
	}

	// Merge config (cgp.* / job.*) as defaults, then let explicit flags win.
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
	for k, v := range base.Vars() {
		if strings.HasPrefix(k, "cgp.") || strings.HasPrefix(k, "job.") {
			if _, set := settings[k]; !set {
				settings[k] = v
			}
		}
	}
	if runnerName != "" {
		settings["cgp.runner"] = eval.StrVal(runnerName)
	}
	if ledgerPath != "" {
		settings["cgp.ledger"] = eval.StrVal(ledgerPath)
	}
	if runnerName == "" {
		if v, ok := settings["cgp.runner"]; ok {
			runnerName = eval.Stringify(v)
		}
	}

	prog := eval.NewJob(eval.JobSpec{
		Command: strings.Join(cmdParts, " "),
		Name:    name, Outputs: outs, Inputs: ins, Settings: settings,
	})
	t := prog.Targets[0]

	if runnerName == "" || runnerName == "shell" {
		if err := shell.SubmitOne(prog, t, shell.Options{DryRun: dryRun}); err != nil {
			fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
			return 1
		}
		return 0
	}
	sch, ok := sched.For(runnerName)
	if !ok {
		fmt.Fprintf(os.Stderr, "cgp sub: unknown runner %q\n", runnerName)
		return 2
	}
	if _, err := sched.SubmitOne(prog, sch, t, deps, after, sched.Options{DryRun: dryRun, Pipeline: "cgp sub"}); err != nil {
		fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
		return 1
	}
	return 0
}
