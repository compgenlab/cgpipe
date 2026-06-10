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
	"strings"

	"github.com/compgen-io/cgp/internal/ast"
	"github.com/compgen-io/cgp/internal/buildinfo"
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
			if os.IsNotExist(err) {
				return nil // a missing config file is fine
			}
			return err // surface real errors (e.g. permission denied) instead of silently skipping
		}
		return addSrc(path, filepath.Dir(path), string(b))
	}
	// Server-wide global config next to the installed cgp binary (lowest priority).
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		if err := addFile(filepath.Join(filepath.Dir(exe), ".cgprc")); err != nil {
			return nil, err
		}
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
    cgp sub [options] <command ...> [-- file ...]   (one-off job / fan-out; see cgp sub -h)
    cgp ledger {dump|search|vacuum|unlock} <db>   (see cgp ledger)
    cgp convert <old.cgp> [-o out.cgp]     (migrate a legacy cgpipe script)
    cgp show-template -r <runner>          (print a scheduler's built-in submission template)
    cgp lsp                                (run the language server over stdio; for editors)
    cgp version

options (single hyphen):
    -h, --help   show this help (after a pipeline file, shows that script's help)
    -dr          dry run: render scripts instead of executing/submitting
                 (note: cgp's own $(…) command substitution is evaluated while
                 rendering, so it still runs under -dr; use \$(…) to defer to
                 the job's shell)
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
	case "help":
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
	case "show-template":
		return runShowTemplate(args[1:])
	case "lsp":
		return runLSP(args[1:])
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
			// A bare `--help` is cgp's help request (the universal spelling),
			// equivalent to `-h` — not a script variable. `--help=value` is still a
			// variable, so a script that really wants a `help` var can set it.
			if nv == "help" {
				showHelp = true
				continue
			}
			if eq := strings.IndexByte(nv, '='); eq >= 0 {
				addCLIVar(vars, cliVarName(nv[:eq]), eval.ParseScalar(nv[eq+1:]))
				continue
			}
			if i+1 < len(rest) && !isOptionToken(rest[i+1]) {
				i++
				addCLIVar(vars, cliVarName(nv), eval.ParseScalar(rest[i]))
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

	// -h/--help with no pipeline file: cgp's own help. (With a file, help resolves
	// to that script's help text after it is parsed, below.)
	if showHelp && file == "" {
		printUsage(os.Stdout)
		return 0
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
		autoexec := false
		if v, ok := prog.Get("cgp.runner.shell.autoexec"); ok {
			autoexec = eval.Truthy(v)
		}
		if err := shell.Run(prog, shell.Options{Goals: goals, DryRun: dryRun, AutoExec: autoexec, Force: force, Cache: cache}); err != nil {
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
	// Parse each stage file at most once, shared between validation and the run
	// loop below (a stage file is otherwise read+parsed by both). Keyed by the
	// resolved path, so a stage whose file depends on a prior stage's export still
	// parses correctly when orchestration reaches it.
	pc := parseCache{}
	if code := validateStageRefs(wf, wfVars, pc); code != 0 {
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
				addCLIVar(subVars, cliVarName(nv[:eq]), eval.ParseScalar(nv[eq+1:]))
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
				addCLIVar(subVars, cliVarName(nv), eval.ParseScalar(val))
			} else {
				addCLIVar(subVars, cliVarName(nv), eval.BoolVal(true))
			}
		}

		sf, err := pc.load(subfile)
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
func validateStageRefs(wf *eval.Program, wfVars map[string]eval.Value, pc parseCache) int {
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
		sf, err := pc.load(subfile)
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

// parseCache memoizes parsed stage files by resolved path, so a file referenced
// by both stage-ref validation and the orchestration run is read and parsed once.
type parseCache map[string]*ast.File

// load returns the parsed file at path, parsing (and caching) it on first use. A
// read or parse error is returned and not cached, so a later caller still sees it.
func (pc parseCache) load(path string) (*ast.File, error) {
	if f, ok := pc[path]; ok {
		return f, nil
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f, err := parser.Parse(string(src), path)
	if err != nil {
		return nil, err
	}
	pc[path] = f
	return f, nil
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
			// Copy rather than append in place: the same backing array can be
			// shared across manifest rows / repeated runs, so a mutating append
			// could alias another row's list.
			vars[name] = append(append(eval.ListVal{}, lst...), v)
		} else {
			vars[name] = eval.ListVal{prev, v}
		}
		return
	}
	vars[name] = v
}
