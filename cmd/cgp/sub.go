package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/compgen-io/cgp/internal/ast"
	"github.com/compgen-io/cgp/internal/eval"
	"github.com/compgen-io/cgp/internal/runner/sched"
	"github.com/compgen-io/cgp/internal/runner/shell"
)

const subUsage = `cgp sub — submit a one-off command as a job

usage:
    cgp sub [options] COMMAND... [-- FILE...]

The first token that is not a recognized option begins COMMAND; everything from
there until a bare -- is the command, verbatim (so flags meant for your command,
like ` + "`gzip -9`" + `, pass straight through). The command is treated as a cgp body:
${input}/${output} substitute; use $VAR for shell variables.

Fan-out: list FILEs after -- (or via --files-from) and cgp submits one independent
job per file, expanding {} placeholders in the command, the job name, and the
-o/-i/-a values:

    {}  {^}     the full input path
    {@}         the basename (directory stripped)
    {^SUF}      the full path with a trailing SUF removed (if it ends with SUF)
    {@SUF}      the basename with a trailing SUF removed (if it ends with SUF)
    {#}         the 1-based fan-out index
    {{}}        a literal {}

options:
    -n, --name S         job name
    -p, --procs N        cpus
    -m, --mem S          memory (e.g. 8G)
    -t, --walltime S     wall-time limit
    -o PATH              declared output (repeatable; recorded in the ledger)
    -i PATH              declared input (repeatable)
    -d, --deps IDS       depend on existing job ids (comma-separated; repeatable)
    -a, --after PATH     depend on the active job that owns PATH in the ledger (repeatable)
    -f, --files-from F   read fan-out files from F, one per line (- = stdin; repeatable)
    -r, --runner NAME    runner: shell (default), slurm, sge, pbs, batchq
    -l, --ledger PATH    ledger database
    -dr                  dry run: render the job(s) instead of submitting
    -h, --help           show this help

Tip: quote redirects/pipes in COMMAND (e.g. 'sort {} > {@.txt}.sorted') so your
shell applies them to the job, not to cgp.
`

// runSub handles `cgp sub [options] command... [-- file...]`.
//
// Argument grammar: leading recognized options (and their values) are consumed;
// the first token that is not a recognized option begins the command, which runs
// verbatim until a bare `--`. Tokens after `--` (plus any --files-from lists) are
// the fan-out file list — with files, cgp submits one job per file, expanding `{}`
// placeholders; with none, it submits a single job.
func runSub(args []string) int {
	var name string
	var outs, ins, deps, after, filesFrom []string
	var runnerName, ledgerPath string
	dryRun := false
	settings := map[string]eval.Value{}
	var cmdParts, files []string

	i := 0
	// --- flag phase: recognized options until the first command token ---
flags:
	for i < len(args) {
		tok := args[i]
		if tok == "--" { // empty command (degenerate); rest is files
			files = append(files, args[i+1:]...)
			i = len(args)
			break
		}
		// Long forms accept --name=value as well as --name value.
		optName, inlineVal, hasInline := tok, "", false
		if strings.HasPrefix(tok, "--") {
			if eq := strings.IndexByte(tok, '='); eq >= 0 {
				optName, inlineVal, hasInline = tok[:eq], tok[eq+1:], true
			}
		}
		// val consumes the value for a value-taking option (inline =value, or the
		// next token); on a missing value it prints a hint and the caller returns 2.
		val := func() (string, bool) {
			if hasInline {
				i++
				return inlineVal, true
			}
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "cgp sub: %s needs a value\n", tok)
				return "", false
			}
			v := args[i+1]
			i += 2
			return v, true
		}
		switch optName {
		case "-h", "--help":
			fmt.Print(subUsage)
			return 0
		case "-dr":
			dryRun = true
			i++
		case "-n", "--name":
			v, ok := val()
			if !ok {
				return 2
			}
			name = v
		case "-p", "--procs":
			v, ok := val()
			if !ok {
				return 2
			}
			settings["job.procs"] = eval.ParseScalar(v)
		case "-m", "--mem":
			v, ok := val()
			if !ok {
				return 2
			}
			settings["job.mem"] = eval.StrVal(v)
		case "-t", "--walltime":
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
		case "-r", "--runner":
			v, ok := val()
			if !ok {
				return 2
			}
			runnerName = v
		case "-l", "--ledger":
			v, ok := val()
			if !ok {
				return 2
			}
			ledgerPath = v
		case "-a", "--after":
			v, ok := val()
			if !ok {
				return 2
			}
			after = append(after, v)
		case "-f", "--files-from":
			v, ok := val()
			if !ok {
				return 2
			}
			filesFrom = append(filesFrom, v)
		case "-d", "--deps":
			v, ok := val()
			if !ok {
				return 2
			}
			for _, p := range strings.Split(v, ",") {
				if p = strings.TrimSpace(p); p != "" {
					deps = append(deps, p)
				}
			}
		default:
			// First non-option token: the command starts here (and may itself
			// begin with `-`, e.g. `gzip -9 {}`). Leave i pointing at it.
			break flags
		}
	}

	// --- command phase: verbatim until a bare `--` ---
	for ; i < len(args); i++ {
		if args[i] == "--" {
			files = append(files, args[i+1:]...)
			break
		}
		cmdParts = append(cmdParts, args[i])
	}

	if len(cmdParts) == 0 {
		fmt.Fprint(os.Stderr, subUsage)
		return 2
	}

	// Assemble the fan-out file list: --files-from lists (in order) then the
	// positional files after `--`.
	var fanFiles []string
	for _, fl := range filesFrom {
		list, err := readFilesFrom(fl)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cgp sub: %v\n", err)
			return 1
		}
		fanFiles = append(fanFiles, list...)
	}
	fanFiles = append(fanFiles, files...)

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

	// submitOne builds and dispatches one job. deps (the -d list) apply to every
	// job; jobAfter is the per-job, already-{}-expanded -a list.
	submitOne := func(command, jobName string, jobOuts, jobIns, jobAfter []string) int {
		prog := eval.NewJob(eval.JobSpec{
			Command: command,
			Name:    jobName, Outputs: jobOuts, Inputs: jobIns, Settings: settings,
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
		if _, err := sched.SubmitOne(prog, sch, t, deps, jobAfter, sched.Options{DryRun: dryRun, Pipeline: "cgp sub"}); err != nil {
			fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
			return 1
		}
		return 0
	}

	// No files: a single job, command used as-is.
	if len(fanFiles) == 0 {
		return submitOne(strings.Join(cmdParts, " "), name, outs, ins, after)
	}

	// Fan-out: one independent job per file, with `{}` expansion against it.
	for idx, f := range fanFiles {
		n := idx + 1
		if dryRun {
			fmt.Printf("# ── job %d/%d: %s ──\n", n, len(fanFiles), f)
		}
		jobName := name
		if jobName != "" {
			jobName = substInput(jobName, f, n)
		}
		// The fan-out file itself is the job's primary declared input.
		jobIns := append([]string{f}, substAll(ins, f, n)...)
		code := submitOne(
			strings.Join(substAll(cmdParts, f, n), " "),
			jobName,
			substAll(outs, f, n),
			jobIns,
			substAll(after, f, n),
		)
		if code != 0 {
			return code
		}
	}
	return 0
}

// readFilesFrom reads a fan-out file list (one path per non-empty line, trimmed)
// from path, or from stdin when path is "-".
func readFilesFrom(path string) ([]string, error) {
	var r io.Reader
	if path == "-" {
		r = os.Stdin
	} else {
		fh, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer fh.Close()
		r = fh
	}
	var files []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024) // tolerate long path lines
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			files = append(files, line)
		}
	}
	return files, sc.Err()
}
