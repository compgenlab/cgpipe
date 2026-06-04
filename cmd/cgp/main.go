// Command cgp runs a pipeline described in a .cgp file by rendering and
// executing its targets with the local shell (no scheduler).
//
// See docs/language-spec.md for the language.
package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/compgen-io/cgp/internal/buildinfo"
	"github.com/compgen-io/cgp/internal/eval"
	"github.com/compgen-io/cgp/internal/ledger"
	"github.com/compgen-io/cgp/internal/parser"
	"github.com/compgen-io/cgp/internal/runner/sched"
	"github.com/compgen-io/cgp/internal/runner/shell"
)

const usage = `cgp — run a .cgp pipeline

usage:
    cgp [options] <pipeline.cgp> [goal ...] [--name value ...]
    cgp sub [options] -- <command ...>     (submit a one-off job; see cgp sub -h)
    cgp ledger {vacuum|unlock} <db>
    cgp version

options (single hyphen):
    -h           show this help
    -dr          dry run: render scripts instead of executing/submitting
    -r NAME      runner: shell (default), slurm, sge, pbs, batchq
                 (also set via cgp.runner in the script/config)

Script variables use a double hyphen: --name value (or --name=value). A bare
argument is a goal (target) to build. With no goal, cgp builds @default (or
the first defined target).
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}

	switch args[0] {
	case "-h", "help":
		fmt.Print(usage)
		return 0
	case "version":
		fmt.Printf("cgp %s\n", buildinfo.Version)
		return 0
	case "ledger":
		return runLedger(args[1:])
	case "sub":
		return runSub(args[1:])
	}

	file := args[0]
	vars := map[string]eval.Value{}
	var goals []string
	dryRun := false
	showHelp := false
	runnerName := ""

	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch {
		case strings.HasPrefix(a, "--"):
			// double hyphen: a script variable (--name value or --name=value)
			nv := a[2:]
			if eq := strings.IndexByte(nv, '='); eq >= 0 {
				vars[nv[:eq]] = parseCLIValue(nv[eq+1:])
				continue
			}
			if i+1 >= len(rest) {
				fmt.Fprintf(os.Stderr, "cgp: variable %s needs a value\n", a)
				return 2
			}
			i++
			vars[nv] = parseCLIValue(rest[i])
		case len(a) > 1 && a[0] == '-':
			// single hyphen: a cgp option
			switch a {
			case "-dr":
				dryRun = true
			case "-h":
				showHelp = true
			case "-r":
				if i+1 >= len(rest) {
					fmt.Fprintln(os.Stderr, "cgp: option -r needs a value")
					return 2
				}
				i++
				runnerName = rest[i]
			default:
				fmt.Fprintf(os.Stderr, "cgp: unknown option %s\n", a)
				return 2
			}
		default:
			goals = append(goals, a)
		}
	}

	src, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
		return 1
	}

	f, err := parser.Parse(string(src), file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
		return 1
	}

	if showHelp {
		if f.Help != "" {
			fmt.Println(f.Help)
		} else {
			fmt.Fprintf(os.Stderr, "cgp: %s has no help text\n", file)
		}
		return 0
	}

	prog, err := eval.Run(f, eval.Options{File: file, Vars: vars})
	if err != nil {
		var ex *eval.ExitError
		if errors.As(err, &ex) {
			return ex.Code
		}
		fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
		return 1
	}

	name := runnerName
	if name == "" {
		if v, ok := prog.Get("cgp.runner"); ok {
			name = eval.Stringify(v)
		}
	}
	if name == "" {
		name = "shell"
	}

	if name == "shell" {
		if err := shell.Run(prog, shell.Options{Goals: goals, DryRun: dryRun}); err != nil {
			fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
			return 1
		}
		return 0
	}

	sch, ok := sched.For(name)
	if !ok {
		fmt.Fprintf(os.Stderr, "cgp: unknown runner %q (have: shell, %s)\n", name, strings.Join(sched.Names(), ", "))
		return 2
	}
	if err := sched.Run(prog, sch, sched.Options{Goals: goals, DryRun: dryRun, Pipeline: file}); err != nil {
		fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
		return 1
	}
	return 0
}

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

	need := func(i int, flag string) (string, bool) {
		if i+1 >= len(args) {
			fmt.Fprintf(os.Stderr, "cgp sub: %s needs a value\n", flag)
			return "", false
		}
		return args[i+1], true
	}

	i := 0
	for i < len(args) {
		a := args[i]
		if a == "--" {
			cmdParts = args[i+1:]
			break
		}
		v, ok := "", false
		switch a {
		case "-dr":
			dryRun = true
		case "-h":
			fmt.Print(subUsage)
			return 0
		case "-name":
			if name, ok = need(i, a); !ok {
				return 2
			}
			i++
		case "-mem":
			if v, ok = need(i, a); !ok {
				return 2
			}
			settings["job.mem"] = eval.StrVal(v)
			i++
		case "-procs":
			if v, ok = need(i, a); !ok {
				return 2
			}
			settings["job.procs"] = parseCLIValue(v)
			i++
		case "-walltime":
			if v, ok = need(i, a); !ok {
				return 2
			}
			settings["job.walltime"] = eval.StrVal(v)
			i++
		case "-o":
			if v, ok = need(i, a); !ok {
				return 2
			}
			outs = append(outs, v)
			i++
		case "-i":
			if v, ok = need(i, a); !ok {
				return 2
			}
			ins = append(ins, v)
			i++
		case "-d":
			if v, ok = need(i, a); !ok {
				return 2
			}
			deps = append(deps, v)
			i++
		case "-after":
			if v, ok = need(i, a); !ok {
				return 2
			}
			after = append(after, v)
			i++
		case "-r":
			if runnerName, ok = need(i, a); !ok {
				return 2
			}
			i++
		case "-ledger":
			if ledgerPath, ok = need(i, a); !ok {
				return 2
			}
			i++
		default:
			fmt.Fprintf(os.Stderr, "cgp sub: unknown option %s (put the command after --)\n", a)
			return 2
		}
		i++
	}

	if len(cmdParts) == 0 {
		fmt.Fprint(os.Stderr, subUsage)
		return 2
	}
	if runnerName != "" {
		settings["cgp.runner"] = eval.StrVal(runnerName)
	}
	if ledgerPath != "" {
		settings["cgp.ledger"] = eval.StrVal(ledgerPath)
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

// runLedger handles `cgp ledger <subcommand> ...`.
func runLedger(args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: cgp ledger {vacuum|unlock} <ledger.db>")
		return 2
	}
	switch args[0] {
	case "vacuum":
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
	case "unlock":
		if err := ledger.Unlock(args[1]); err != nil {
			fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
			return 1
		}
		return 0
	default:
		fmt.Fprintln(os.Stderr, "usage: cgp ledger {vacuum|unlock} <ledger.db>")
		return 2
	}
}

// parseCLIValue parses a command-line value into a typed cgp value, falling back
// to a string (matching cgp's "parse numbers/bools when possible" rule).
func parseCLIValue(s string) eval.Value {
	if s == "true" {
		return eval.BoolVal(true)
	}
	if s == "false" {
		return eval.BoolVal(false)
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return eval.IntVal(i)
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return eval.FloatVal(f)
	}
	return eval.StrVal(s)
}
