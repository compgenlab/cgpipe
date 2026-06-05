// Command cgp runs a pipeline described in a .cgp file by rendering and
// executing its targets with the local shell (no scheduler).
//
// See docs/language-spec.md for the language.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/compgen-io/cgp/internal/ast"
	"github.com/compgen-io/cgp/internal/buildinfo"
	"github.com/compgen-io/cgp/internal/convert"
	"github.com/compgen-io/cgp/internal/eval"
	"github.com/compgen-io/cgp/internal/ledger"
	"github.com/compgen-io/cgp/internal/manifest"
	"github.com/compgen-io/cgp/internal/parser"
	"github.com/compgen-io/cgp/internal/runner"
	"github.com/compgen-io/cgp/internal/runner/graphviz"
	"github.com/compgen-io/cgp/internal/runner/report"
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
    cgp ledger {dump|search|vacuum|unlock} <db>   (see cgp ledger)
    cgp convert <old.cgp> [-o out.cgp]     (migrate a legacy cgpipe script)
    cgp version

options (single hyphen):
    -h           show this help
    -dr          dry run: render scripts instead of executing/submitting
    -force       rebuild every target in the goal graph, ignoring staleness
    -r NAME      runner: shell (default), slurm, sge, pbs, batchq, graphviz, html
                 (graphviz=DOT to stdout; html=status report reading the ledger)
                 (also set via cgp.runner in the script/config)
    -manifest FILE        run once per CGP manifest file (glob ok); also
    -manifest-tsv FILE    -manifest-csv / -manifest-json: run once per row,
                          columns/keys become variables

Script variables use a double hyphen: --name value (or --name=value). A bare
--name (no value) sets name=true; hyphens in a name become underscores
(--hp-dist sets hp_dist); a repeated flag builds a list (--x a --x b => [a, b]).
A bare argument is a goal (target) to build. With no goal, cgp builds @default
(or the first defined target).
`

// printUsage writes the help text followed by the version footer (e.g.
// "cgp v0.1.2-dev-abcdef").
func printUsage(w io.Writer) {
	fmt.Fprint(w, usage)
	fmt.Fprintf(w, "\ncgp %s\n", buildinfo.Version)
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		printUsage(os.Stderr)
		return 2
	}

	switch args[0] {
	case "-h", "help":
		printUsage(os.Stdout)
		return 0
	case "version":
		fmt.Printf("cgp %s\n", buildinfo.Version)
		return 0
	case "ledger":
		return runLedger(args[1:])
	case "sub":
		return runSub(args[1:])
	case "convert":
		return runConvert(args[1:])
	}

	// Pipeline run. cgp options (single hyphen) and script variables (--name)
	// may appear before or after the pipeline file; the first bare argument is
	// the pipeline file, any later bare arguments are goals.
	file := ""
	vars := map[string]eval.Value{}
	var goals []string
	dryRun := false
	force := false
	showHelp := false
	runnerName := ""
	manifestPath, manifestFmt := "", ""

	rest := args
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch {
		case strings.HasPrefix(a, "--"):
			// double hyphen: a script variable. --name=value (explicit), --name
			// value (the next token, unless it's another option), or a bare --name
			// boolean flag. Hyphens in the name become underscores; a repeated flag
			// builds a list.
			nv := a[2:]
			if eq := strings.IndexByte(nv, '='); eq >= 0 {
				addCLIVar(vars, cliVarName(nv[:eq]), parseCLIValue(nv[eq+1:]))
				continue
			}
			if i+1 < len(rest) && !isOptionToken(rest[i+1]) {
				i++
				addCLIVar(vars, cliVarName(nv), parseCLIValue(rest[i]))
			} else {
				addCLIVar(vars, cliVarName(nv), eval.BoolVal(true))
			}
		case len(a) > 1 && a[0] == '-':
			// single hyphen: a cgp option
			switch a {
			case "-dr":
				dryRun = true
			case "-force":
				force = true
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
			if file == "" {
				file = a // the first bare argument is the pipeline file
			} else {
				goals = append(goals, a)
			}
		}
	}

	if file == "" {
		fmt.Fprintln(os.Stderr, "cgp: no pipeline file given")
		printUsage(os.Stderr)
		return 2
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
		return runPipeline(f, file, cfgs, vars, goals, runnerName, dryRun, force, runner.NewCache())
	}

	// Manifest fan-out: run the pipeline once per row/file. A shared stat cache
	// means common inputs (e.g. a reference) are stat'd once across all runs.
	rows, err := loadManifest(manifestPath, manifestFmt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
		return 1
	}
	// The graphviz/html runners produce a single document; for a manifest run
	// emit one combined document (a cluster/section per row) rather than one per
	// row concatenated to stdout.
	if runnerName == "graphviz" || runnerName == "html" {
		return runManifestGraph(f, file, cfgs, vars, goals, runnerName, rows)
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
		if code := runPipeline(f, file, cfgs, rv, goals, runnerName, dryRun, force, cache); code != 0 {
			return code
		}
	}
	return 0
}

// runPipeline evaluates the (already-parsed) pipeline with the given variables
// and runs it through the selected runner, sharing the stat cache.
func runPipeline(f *ast.File, file string, cfgs []eval.ConfigFile, vars map[string]eval.Value, goals []string, runnerName string, dryRun, force bool, cache *runner.Cache) int {
	prog, err := eval.Run(f, eval.Options{File: file, Configs: cfgs, Vars: vars})
	if err != nil {
		var ex *eval.ExitError
		if errors.As(err, &ex) {
			return ex.Code
		}
		fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
		return 1
	}
	if len(prog.Stages) > 0 {
		return orchestrate(prog, cfgs, runnerName, dryRun, force)
	}
	return dispatchRun(prog, file, goals, runnerName, dryRun, force, cache)
}

// dispatchRun runs an evaluated program through the selected runner.
func dispatchRun(prog *eval.Program, file string, goals []string, runnerName string, dryRun, force bool, cache *runner.Cache) int {
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
		if err := shell.Run(prog, shell.Options{Goals: goals, DryRun: dryRun, Force: force, Cache: cache}); err != nil {
			fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
			return 1
		}
		return 0
	}
	if name == "graphviz" {
		if err := graphviz.Run(prog, goals, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
			return 1
		}
		return 0
	}
	if name == "html" {
		return runReport(prog, file, goals, os.Stdout)
	}
	sch, ok := sched.For(name)
	if !ok {
		fmt.Fprintf(os.Stderr, "cgp: unknown runner %q (have: shell, graphviz, html, %s)\n", name, strings.Join(sched.Names(), ", "))
		return 2
	}
	if err := sched.Run(prog, sch, sched.Options{Goals: goals, DryRun: dryRun, Force: force, Pipeline: file, Cache: cache}); err != nil {
		fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
		return 1
	}
	return 0
}

// runReport renders an HTML status report of one pipeline's DAG to out.
func runReport(prog *eval.Program, file string, goals []string, out io.Writer) int {
	g := graphviz.Build(prog, goals)
	if err := report.Run(g, precomputeStatus(prog, g), file, out); err != nil {
		fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
		return 1
	}
	return 0
}

// precomputeStatus resolves every node's status now (disk existence → the
// ledger's owning job → the script-configured scheduler's live state) and
// returns a lookup over that snapshot. The ledger is opened read-only and closed
// here, so the snapshot does not hold the ledger open while rendering.
func precomputeStatus(prog *eval.Program, g graphviz.Graph) func(string) report.State {
	var sch *sched.Scheduler
	if v, ok := prog.Get("cgp.runner"); ok {
		if s, found := sched.For(eval.Stringify(v)); found {
			sch = &s
		}
	}
	var lg *ledger.Ledger
	if v, ok := prog.Get("cgp.ledger"); ok && eval.Stringify(v) != "" {
		if l, err := ledger.OpenRead(eval.Stringify(v)); err == nil {
			lg = l
			defer lg.Close()
		}
	}
	one := func(name string) report.State {
		if _, err := os.Stat(name); err == nil {
			return report.Done // present on disk ⇒ produced
		}
		if lg == nil {
			return report.Pending
		}
		owner, ok, err := lg.OwnerOf(name)
		if err != nil || !ok || owner == "" {
			return report.Pending
		}
		if sch != nil && sch.State != nil {
			switch sch.State(owner) {
			case "queued":
				return report.Queued
			case "running":
				return report.Running
			case "done":
				return report.Done
			case "failed":
				return report.Failed
			}
		}
		if sch != nil && sch.IsActive != nil {
			if sch.IsActive(owner) {
				return report.Running
			}
			return report.Failed // owning job gone, output missing
		}
		return report.Running // owner exists but no scheduler to probe
	}
	snap := map[string]report.State{}
	for _, n := range g.Nodes {
		snap[n.Name] = one(n.Name)
	}
	return func(name string) report.State { return snap[name] }
}

// runManifestGraph emits a single combined graphviz/html document for a manifest
// run — one cluster (graphviz) / section (html) per row, labeled by the row.
func runManifestGraph(f *ast.File, file string, cfgs []eval.ConfigFile, baseVars map[string]eval.Value, goals []string, runnerName string, rows []map[string]eval.Value) int {
	var dotSecs []graphviz.Labeled
	var htmlSecs []report.Section
	for i, row := range rows {
		rv := map[string]eval.Value{}
		for k, v := range row {
			rv[k] = v
		}
		for k, v := range baseVars { // explicit CLI vars override columns
			rv[k] = v
		}
		prog, err := eval.Run(f, eval.Options{File: file, Configs: cfgs, Vars: rv})
		if err != nil {
			var ex *eval.ExitError
			if errors.As(err, &ex) {
				return ex.Code
			}
			fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
			return 1
		}
		label := rowLabel(row, i)
		g := graphviz.Build(prog, goals)
		if runnerName == "graphviz" {
			dotSecs = append(dotSecs, graphviz.Labeled{Label: label, Graph: g})
		} else {
			htmlSecs = append(htmlSecs, report.Section{Label: label, Graph: g, StatusOf: precomputeStatus(prog, g)})
		}
	}
	if runnerName == "graphviz" {
		io.WriteString(os.Stdout, graphviz.DOTCombined(dotSecs))
		return 0
	}
	if err := report.RunCombined(file, htmlSecs, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
		return 1
	}
	return 0
}

// rowLabel names a manifest row for a report cluster/section: a sample/id/name
// column if present, else "row N".
func rowLabel(row map[string]eval.Value, i int) string {
	for _, key := range []string{"sample", "id", "name"} {
		if v, ok := row[key]; ok {
			return eval.Stringify(v)
		}
	}
	return fmt.Sprintf("row %d", i+1)
}

var stageRefRe = regexp.MustCompile(`\$\{\s*([A-Za-z_][A-Za-z0-9_]*)\.([A-Za-z_][A-Za-z0-9_]*)`)

// orchestrate runs a workflow's stages in declaration order, threading each
// stage's exports (as ${name.export}) into the variables of later stages. Each
// stage gets a fresh stat cache, since a later stage reads an earlier stage's
// freshly produced outputs (so they must not be cached as missing).
func orchestrate(wf *eval.Program, cfgs []eval.ConfigFile, runnerName string, dryRun, force bool) int {
	wfVars := wf.Vars()
	if code := validateStageRefs(wf, wfVars); code != 0 {
		return code
	}
	for _, st := range wf.Stages {
		name, err := eval.Interpolate(st.Name, wfVars)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cgp: stage name: %v\n", err)
			return 1
		}
		subfile, err := eval.Interpolate(st.File, wfVars)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cgp: stage %s file: %v\n", name, err)
			return 1
		}
		subVars := map[string]eval.Value{}
		for i := 0; i < len(st.Args); i++ {
			a, err := eval.Interpolate(st.Args[i], wfVars)
			if err != nil {
				fmt.Fprintf(os.Stderr, "cgp: stage %s args: %v\n", name, err)
				return 1
			}
			if !strings.HasPrefix(a, "--") {
				fmt.Fprintf(os.Stderr, "cgp: stage %s: expected --name, got %q\n", name, a)
				return 2
			}
			nv := a[2:]
			if eq := strings.IndexByte(nv, '='); eq >= 0 {
				addCLIVar(subVars, cliVarName(nv[:eq]), parseCLIValue(nv[eq+1:]))
				continue
			}
			// next token is the value, unless it's another --flag (then boolean)
			if i+1 < len(st.Args) && !isOptionToken(st.Args[i+1]) {
				i++
				val, err := eval.Interpolate(st.Args[i], wfVars)
				if err != nil {
					fmt.Fprintf(os.Stderr, "cgp: stage %s args: %v\n", name, err)
					return 1
				}
				addCLIVar(subVars, cliVarName(nv), parseCLIValue(val))
			} else {
				addCLIVar(subVars, cliVarName(nv), eval.BoolVal(true))
			}
		}

		src, err := os.ReadFile(subfile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cgp: stage %s: %v\n", name, err)
			return 1
		}
		sf, err := parser.Parse(string(src), subfile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cgp: stage %s: %v\n", name, err)
			return 1
		}
		subProg, err := eval.Run(sf, eval.Options{File: subfile, Configs: cfgs, Vars: subVars})
		if err != nil {
			var ex *eval.ExitError
			if errors.As(err, &ex) {
				return ex.Code
			}
			fmt.Fprintf(os.Stderr, "cgp: stage %s: %v\n", name, err)
			return 1
		}
		if code := dispatchRun(subProg, subfile, nil, runnerName, dryRun, force, runner.NewCache()); code != 0 {
			return code
		}
		for k, v := range subProg.Exports {
			wfVars[name+"."+k] = v
		}
	}
	return 0
}

// validateStageRefs is a best-effort static check: a ${NAME.X} reference to a
// declared stage NAME whose file never exports X is a typo and fails fast.
// (Conditional exports that don't fire are caught at runtime when referenced.)
func validateStageRefs(wf *eval.Program, wfVars map[string]eval.Value) int {
	exports := map[string]map[string]bool{}
	for _, st := range wf.Stages {
		name, err := eval.Interpolate(st.Name, wfVars)
		if err != nil {
			continue
		}
		subfile, err := eval.Interpolate(st.File, wfVars)
		if err != nil {
			continue
		}
		src, err := os.ReadFile(subfile)
		if err != nil {
			continue
		}
		sf, err := parser.Parse(string(src), subfile)
		if err != nil {
			continue
		}
		set := map[string]bool{}
		for _, e := range eval.ExportNames(sf) {
			set[e] = true
		}
		exports[name] = set
	}
	for _, st := range wf.Stages {
		for _, a := range append([]string{st.File}, st.Args...) {
			for _, m := range stageRefRe.FindAllStringSubmatch(a, -1) {
				stage, exp := m[1], m[2]
				set, known := exports[stage]
				if known && !set[exp] {
					fmt.Fprintf(os.Stderr, "cgp: reference ${%s.%s}, but stage %q exports no %q\n", stage, exp, stage, exp)
					return 1
				}
			}
		}
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

const convertUsage = `cgp convert — migrate a legacy cgpipe script to cgp

usage:
    cgp convert <old.cgp> [-o out.cgp]

Reads a legacy (JVM-cgpipe-era) script and writes the cgp-equivalent to stdout
(or to -o FILE). Best-effort: the mechanical differences are rewritten and
anything that can't be converted safely is annotated with a "# cgp-convert:"
comment. Review the result before running it.
`

// runConvert handles `cgp convert <old.cgp> [-o out.cgp]`.
func runConvert(args []string) int {
	var in, out string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-h":
			fmt.Print(convertUsage)
			return 0
		case a == "-o":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "cgp convert: -o needs a value")
				return 2
			}
			i++
			out = args[i]
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(os.Stderr, "cgp convert: unknown option %s\n", a)
			return 2
		default:
			if in != "" {
				fmt.Fprintln(os.Stderr, "cgp convert: only one input file")
				return 2
			}
			in = a
		}
	}
	if in == "" {
		fmt.Fprint(os.Stderr, convertUsage)
		return 2
	}

	src, err := os.ReadFile(in)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
		return 1
	}
	converted, warnings := convert.Convert(string(src))

	if out == "" {
		fmt.Print(converted)
	} else if err := os.WriteFile(out, []byte(converted), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
		return 1
	}
	for _, w := range warnings {
		fmt.Fprintf(os.Stderr, "cgp convert: %s\n", w)
	}
	return 0
}

const ledgerUsage = `usage:
    cgp ledger dump <db>                       dump all jobs as key/value TSV
    cgp ledger search [filters] <db>           dump jobs matching the filters
    cgp ledger vacuum <db>                      drop jobs that own no current output
    cgp ledger unlock <db>                      remove a stale lockfile

search filters (substring match; combined with AND):
    -i PATH      an input path contains PATH
    -o PATH      an output path contains PATH
    -g PATTERN   a job-script line contains PATTERN (grep)
    -name NAME   the job name contains NAME
    -id JOBID    the job id (exact)
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
	case "unlock":
		if len(args) < 2 {
			fmt.Fprint(os.Stderr, ledgerUsage)
			return 2
		}
		if err := ledger.Unlock(args[1]); err != nil {
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
	need := func(i int) (string, bool) {
		if i+1 >= len(args) {
			fmt.Fprint(os.Stderr, ledgerUsage)
			return "", false
		}
		return args[i+1], true
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		var v string
		ok := true
		switch a {
		case "-i":
			if v, ok = need(i); ok {
				f.Input = v
				i++
			}
		case "-o":
			if v, ok = need(i); ok {
				f.Output = v
				i++
			}
		case "-g":
			if v, ok = need(i); ok {
				f.Grep = v
				i++
			}
		case "-name":
			if v, ok = need(i); ok {
				f.Name = v
				i++
			}
		case "-id":
			if v, ok = need(i); ok {
				f.ID = v
				i++
			}
		default:
			if strings.HasPrefix(a, "-") || db != "" {
				fmt.Fprint(os.Stderr, ledgerUsage)
				return 2
			}
			db = a
		}
		if !ok {
			return 2
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

// cliVarName normalizes a command-line variable name: hyphens become underscores
// so `--hp-dist` sets the cgp identifier `hp_dist` (identifiers can't contain
// hyphens). `--hp_dist` is therefore equivalent.
func cliVarName(s string) string { return strings.ReplaceAll(s, "-", "_") }

// isOptionToken reports whether a token looks like an option/flag (starts with
// `-`), so it should not be consumed as the value of a preceding --name.
func isOptionToken(s string) bool { return len(s) > 0 && s[0] == '-' }

// addCLIVar sets a command-line variable, building a list when the same name is
// given more than once (e.g. `--x a --x b` ⇒ x = [a, b]).
func addCLIVar(vars map[string]eval.Value, name string, v eval.Value) {
	if prev, ok := vars[name]; ok {
		if lst, isList := prev.(eval.ListVal); isList {
			vars[name] = append(lst, v)
		} else {
			vars[name] = eval.ListVal{prev, v}
		}
		return
	}
	vars[name] = v
}
