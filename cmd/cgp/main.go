// Command cgp runs a pipeline described in a .cgp file by rendering and
// executing its targets with the local shell (no scheduler).
//
// See docs/language-spec.md for the language.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/compgen-io/cgp/internal/ast"
	"github.com/compgen-io/cgp/internal/buildinfo"
	"github.com/compgen-io/cgp/internal/eval"
	"github.com/compgen-io/cgp/internal/ledger"
	"github.com/compgen-io/cgp/internal/manifest"
	"github.com/compgen-io/cgp/internal/parser"
	"github.com/compgen-io/cgp/internal/runner"
	"github.com/compgen-io/cgp/internal/runner/sched"
	"github.com/compgen-io/cgp/internal/runner/shell"
)

// loadConfigs discovers and parses the config layers (system, then user, then
// CGP_ENV / CGP_RUN_ID), each itself a cgp script, in resolution order.
func loadConfigs() ([]eval.ConfigFile, error) {
	var cfgs []eval.ConfigFile
	addSrc := func(name, dir, src string) error {
		f, err := parser.Parse(src, name)
		if err != nil {
			return fmt.Errorf("config %s: %w", name, err)
		}
		cfgs = append(cfgs, eval.ConfigFile{Dir: dir, File: f})
		return nil
	}
	addFile := func(path string) error {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil // a missing config file is fine
		}
		return addSrc(path, filepath.Dir(path), string(b))
	}
	if err := addFile("/etc/cgp/config"); err != nil {
		return nil, err
	}
	if home, err := os.UserHomeDir(); err == nil {
		if err := addFile(filepath.Join(home, ".cgp", "config")); err != nil {
			return nil, err
		}
	}
	envSrc := os.Getenv("CGP_ENV")
	if rid := os.Getenv("CGP_RUN_ID"); rid != "" {
		envSrc += "\ncgp.run_id = \"" + rid + "\""
	}
	if strings.TrimSpace(envSrc) != "" {
		cwd, _ := os.Getwd()
		if err := addSrc("CGP_ENV", cwd, envSrc); err != nil {
			return nil, err
		}
	}
	return cfgs, nil
}

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
    -manifest FILE        run once per CGP manifest file (glob ok); also
    -manifest-tsv FILE    -manifest-csv / -manifest-json: run once per row,
                          columns/keys become variables

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
	manifestPath, manifestFmt := "", ""

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
			case "-manifest", "-manifest-cgp", "-manifest-tsv", "-manifest-csv", "-manifest-json":
				if i+1 >= len(rest) {
					fmt.Fprintf(os.Stderr, "cgp: option %s needs a value\n", a)
					return 2
				}
				i++
				manifestPath = rest[i]
				switch a {
				case "-manifest-tsv":
					manifestFmt = "tsv"
				case "-manifest-csv":
					manifestFmt = "csv"
				case "-manifest-json":
					manifestFmt = "json"
				default:
					manifestFmt = "cgp"
				}
			default:
				fmt.Fprintf(os.Stderr, "cgp: unknown option %s\n", a)
				return 2
			}
		default:
			goals = append(goals, a)
		}
	}

	if os.Getenv("CGP_DRYRUN") != "" {
		dryRun = true
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

	cfgs, err := loadConfigs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
		return 1
	}

	// No manifest: a single run.
	if manifestPath == "" {
		return runPipeline(f, file, cfgs, vars, goals, runnerName, dryRun, runner.NewCache())
	}

	// Manifest fan-out: run the pipeline once per row/file. A shared stat cache
	// means common inputs (e.g. a reference) are stat'd once across all runs.
	rows, err := loadManifest(manifestPath, manifestFmt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
		return 1
	}
	cache := runner.NewCache()
	for _, row := range rows {
		rv := map[string]eval.Value{}
		for k, v := range row {
			rv[k] = v
		}
		for k, v := range vars { // explicit CLI variables override the manifest
			rv[k] = v
		}
		if code := runPipeline(f, file, cfgs, rv, goals, runnerName, dryRun, cache); code != 0 {
			return code
		}
	}
	return 0
}

// runPipeline evaluates the (already-parsed) pipeline with the given variables
// and runs it through the selected runner, sharing the stat cache.
func runPipeline(f *ast.File, file string, cfgs []eval.ConfigFile, vars map[string]eval.Value, goals []string, runnerName string, dryRun bool, cache *runner.Cache) int {
	prog, err := eval.Run(f, eval.Options{File: file, Configs: cfgs, Vars: vars})
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
		if err := shell.Run(prog, shell.Options{Goals: goals, DryRun: dryRun, Cache: cache}); err != nil {
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
	if err := sched.Run(prog, sch, sched.Options{Goals: goals, DryRun: dryRun, Pipeline: file, Cache: cache}); err != nil {
		fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
		return 1
	}
	return 0
}

// loadManifest loads a manifest into rows of variables, one run per row.
func loadManifest(path, format string) ([]map[string]eval.Value, error) {
	switch format {
	case "tsv":
		return manifest.LoadDelimited(path, '\t')
	case "csv":
		return manifest.LoadDelimited(path, ',')
	case "json":
		return manifest.LoadJSON(path)
	case "cgp":
		return loadCGPManifest(path)
	}
	return nil, fmt.Errorf("unknown manifest format %q", format)
}

// loadCGPManifest globs pattern and evaluates each matched .cgp file (which sets
// variables); each file becomes one run's variable set.
func loadCGPManifest(pattern string) ([]map[string]eval.Value, error) {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("no manifest files match %q", pattern)
	}
	var rows []map[string]eval.Value
	for _, m := range matches {
		b, err := os.ReadFile(m)
		if err != nil {
			return nil, err
		}
		mf, err := parser.Parse(string(b), m)
		if err != nil {
			return nil, err
		}
		mp, err := eval.Run(mf, eval.Options{File: m})
		if err != nil {
			return nil, fmt.Errorf("manifest %s: %w", m, err)
		}
		rows = append(rows, mp.Vars())
	}
	return rows, nil
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
