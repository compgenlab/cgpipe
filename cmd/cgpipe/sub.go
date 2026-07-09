package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/compgenlab/cgpipe/internal/ast"
	"github.com/compgenlab/cgpipe/internal/eval"
	"github.com/compgenlab/cgpipe/internal/runner/sched"
	"github.com/compgenlab/cgpipe/internal/runner/shell"
)

const subUsage = `cgp sub — submit a one-off command as a job

usage:
    cgp sub [options] COMMAND... [-- FILE...]

The first token that is not a recognized option begins COMMAND; everything from
there until a bare -- is the command, verbatim (so flags meant for your command,
like ` + "`gzip -9`" + `, pass straight through). The command is treated as a cgpipe body:
${input}/${output} substitute; use $VAR for shell variables.

Fan-out: list FILEs after -- (or via --files-from) and cgpipe submits one independent
job per file, expanding {} placeholders in the command, the job name, and the
-o/-i/-a values. A file whose -o outputs already exist and are newer than its
inputs is skipped (make-like; logged to stderr):

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
    -o, --output PATH    declared output (repeatable; recorded in the ledger)
    -i, --input PATH     declared input (repeatable)
    --stdout PATH        redirect job stdout to PATH (scheduler runners; {}-expanded per file)
    --stderr PATH        redirect job stderr to PATH (scheduler runners; {}-expanded per file)
    -d, --deps IDS       depend on existing job ids (comma-separated; repeatable)
    -a, --after PATH     depend on the active job that owns PATH in the ledger (repeatable)
    -f, --files-from F   read fan-out files from F, one per line (- = stdin; only once)
    --array              submit the fan-out as ONE job array (slurm/batchq/pbs); each
                         task runs one file's command, dispatched by the scheduler's
                         array task-id. shell/sge fall back to one job per file. Tasks
                         whose -o outputs are already up to date are skipped, so only
                         the missing indices are submitted (--array=1,3,6). A
                         {}-expanded --after is rejected (per-element dependency).
    -r, --runner NAME    runner: shell (default), slurm, sge, pbs, batchq
    -l, --ledger PATH    ledger directory
    -dr                  dry run: render the job(s) instead of submitting
    -h, --help           show this help

Tip: quote redirects/pipes in COMMAND (e.g. 'sort {} > {@.txt}.sorted') so your
shell applies them to the job, not to cgpipe.
`

// runSub handles `cgp sub [options] command... [-- file...]`.
//
// Argument grammar: leading recognized options (and their values) are consumed;
// the first token that is not a recognized option begins the command, which runs
// verbatim until a bare `--`. Tokens after `--` (plus any --files-from lists) are
// the fan-out file list — with files, cgpipe submits one job per file, expanding `{}`
// placeholders; with none, it submits a single job.
func runSub(args []string) int {
	var name string
	var stdout, stderr string
	var outs, ins, deps, after []string
	var runnerName, ledgerPath, filesFrom string
	filesFromSet := false
	dryRun := false
	asArray := false
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
		case "-array", "--array":
			asArray = true
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
		case "-o", "--output":
			v, ok := val()
			if !ok {
				return 2
			}
			outs = append(outs, v)
		case "-i", "--input":
			v, ok := val()
			if !ok {
				return 2
			}
			ins = append(ins, v)
		case "-stdout", "--stdout":
			v, ok := val()
			if !ok {
				return 2
			}
			stdout = v
		case "-stderr", "--stderr":
			v, ok := val()
			if !ok {
				return 2
			}
			stderr = v
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
			if filesFromSet {
				fmt.Fprintln(os.Stderr, "cgp sub: --files-from may be given only once")
				return 2
			}
			filesFrom, filesFromSet = v, true
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

	// CGP_DRYRUN forces dry run, matching the pipeline path in main.go.
	if os.Getenv("CGP_DRYRUN") != "" {
		dryRun = true
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

	// Assemble the fan-out file list: the --files-from list (if any) then the
	// positional files after `--`.
	var fanFiles []string
	if filesFromSet {
		list, err := readFilesFrom(filesFrom)
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
	submitOne := func(command, jobName string, jobOuts, jobIns, jobAfter []string, jobStdout, jobStderr string) int {
		// job.stdout/job.stderr are per-job (they may carry {}-expanded paths), so
		// layer them over a copy of the shared settings rather than mutating it.
		js := settings
		if jobStdout != "" || jobStderr != "" {
			js = make(map[string]eval.Value, len(settings)+2)
			for k, v := range settings {
				js[k] = v
			}
			if jobStdout != "" {
				js["job.stdout"] = eval.StrVal(jobStdout)
			}
			if jobStderr != "" {
				js["job.stderr"] = eval.StrVal(jobStderr)
			}
		}
		prog := eval.NewJob(eval.JobSpec{
			Command: command,
			Name:    jobName, Outputs: jobOuts, Inputs: jobIns, Settings: js,
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
		return submitOne(strings.Join(cmdParts, " "), name, outs, ins, after, stdout, stderr)
	}

	// Array fan-out: a single `--array=1-N` submission over the whole file list,
	// dispatched at run time by the scheduler's task-id var. Supported on the
	// schedulers with native array support (slurm/batchq/pbs); shell/sge fall
	// through to the per-file loop below.
	if asArray {
		if taskVar := arrayTaskVar(runnerName); taskVar != "" {
			// A {}-expanded -a/--after is a per-element dependency, which one array
			// submission (a single dependency directive) cannot express.
			for _, a := range after {
				if strings.Contains(a, "{") {
					fmt.Fprintln(os.Stderr, "cgp sub: --array cannot use a {}-expanded --after "+
						"(per-element dependency); use a fixed --after, or drop --array")
					return 2
				}
			}
			var body strings.Builder
			fmt.Fprintf(&body, "case \"$%s\" in\n", taskVar)
			var jobIns, jobOuts []string
			var survivors []int
			for idx, f := range fanFiles {
				n := idx + 1
				taskOuts := substAll(outs, f, n)
				taskIns := append([]string{f}, substAll(ins, f, n)...)
				// Skip a task whose declared outputs are already up to date, so only
				// the missing indices end up in the --array spec.
				if upToDate(taskOuts, taskIns) {
					fmt.Fprintf(os.Stderr, "# skip: array task %d (%s) — output up to date\n", n, f)
					continue
				}
				fmt.Fprintf(&body, "  %d) %s ;;\n", n, strings.Join(substAll(cmdParts, f, n), " "))
				jobIns = append(jobIns, f)
				jobIns = append(jobIns, substAll(ins, f, n)...)
				jobOuts = append(jobOuts, taskOuts...)
				survivors = append(survivors, n)
			}
			if len(survivors) == 0 {
				fmt.Fprintf(os.Stderr, "cgp sub: all %d array tasks already up to date; nothing to submit\n", len(fanFiles))
				return 0
			}
			fmt.Fprintf(&body, "  *) echo \"cgp: no array task $%s\" >&2; exit 1 ;;\nesac\n", taskVar)
			settings["job.array"] = eval.StrVal(arraySpec(survivors))
			if dryRun {
				fmt.Printf("# ── array job: %d tasks ──\n", len(survivors))
			}
			return submitOne(body.String(), name, dedupeStrings(jobOuts), dedupeStrings(jobIns), after, stdout, stderr)
		}
		fmt.Fprintf(os.Stderr, "cgp sub: --array is not supported for this runner; submitting one job per file\n")
	}

	// Fan-out: one independent job per file, with `{}` expansion against it.
	for idx, f := range fanFiles {
		n := idx + 1
		// The fan-out file itself is the job's primary declared input.
		jobIns := append([]string{f}, substAll(ins, f, n)...)
		// Skip a file whose declared outputs are already up to date (make-like).
		if upToDate(substAll(outs, f, n), jobIns) {
			fmt.Fprintf(os.Stderr, "# skip: %s — output up to date\n", f)
			continue
		}
		if dryRun {
			fmt.Printf("# ── job %d/%d: %s ──\n", n, len(fanFiles), f)
		}
		jobName := name
		if jobName != "" {
			jobName = substInput(jobName, f, n)
		}
		code := submitOne(
			strings.Join(substAll(cmdParts, f, n), " "),
			jobName,
			substAll(outs, f, n),
			jobIns,
			substAll(after, f, n),
			substInput(stdout, f, n),
			substInput(stderr, f, n),
		)
		if code != 0 {
			return code
		}
	}
	return 0
}

// arrayTaskVar returns the shell variable that carries the array task index at run
// time for a runner that supports job arrays, or "" for runners that do not (shell,
// sge), which then fall back to one job per file.
func arrayTaskVar(runner string) string {
	switch runner {
	case "slurm":
		return "SLURM_ARRAY_TASK_ID"
	case "batchq":
		return "BATCHQ_ARRAY_TASK_ID"
	case "pbs":
		return "PBS_ARRAY_INDEX"
	}
	return ""
}

// upToDate reports whether every declared output exists and is at least as new
// as the newest input — i.e. the task's work is already done and can be skipped.
// With no declared outputs it returns false (nothing to check against → always
// run), matching the pipeline's staleness rule (internal/runner/driver.go resolve).
func upToDate(outs, ins []string) bool {
	if len(outs) == 0 {
		return false
	}
	var newestIn time.Time
	for _, in := range ins {
		if fi, err := os.Stat(in); err == nil && fi.ModTime().After(newestIn) {
			newestIn = fi.ModTime()
		}
	}
	for _, o := range outs {
		fi, err := os.Stat(o)
		if err != nil || fi.ModTime().Before(newestIn) {
			return false // missing or stale → must run
		}
	}
	return true
}

// arraySpec renders ascending 1-based task indices as a scheduler array spec,
// collapsing contiguous runs into ranges: [1,2,3,5] -> "1-3,5", [1] -> "1". The
// indices arrive in order (fan-out files are iterated in order), so no sort.
func arraySpec(idx []int) string {
	if len(idx) == 0 {
		return ""
	}
	var b strings.Builder
	start, prev := idx[0], idx[0]
	flush := func() {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		if start == prev {
			b.WriteString(strconv.Itoa(start))
		} else {
			fmt.Fprintf(&b, "%d-%d", start, prev)
		}
	}
	for _, n := range idx[1:] {
		if n == prev+1 {
			prev = n
			continue
		}
		flush()
		start, prev = n, n
	}
	flush()
	return b.String()
}

// dedupeStrings returns ss with duplicates removed, preserving first-seen order.
func dedupeStrings(ss []string) []string {
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
