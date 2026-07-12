package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/compgenlab/cgpipe/internal/buildinfo"
	"github.com/compgenlab/cgpipe/internal/runner/sched"
)

const doctorUsage = `usage:
    cgp doctor [--write]      check the environment for job submission

Reports the loaded config, the resolved cgp.runner/cgp.ledger, and which batch
schedulers are usable on this machine, then recommends a cgp.runner if one is
not set. Setup stays explicit — cgp never picks a scheduler at run time.

    --write   apply the recommendation: write "cgp.runner = <name>" to
              ~/.cgp/config, but ONLY when exactly one scheduler is
              unambiguously detected (never guesses SGE vs PBS).
`

// runDoctor handles `cgp doctor [--write]`: a diagnostic that helps a user get
// job submission configured. It detects schedulers on PATH, reports the resolved
// runner/ledger from config, and recommends a cgp.runner when none is set. With
// --write it applies an unambiguous recommendation to ~/.cgp/config. Exit code is
// 1 only when the *configured* runner is unusable (its submit command is missing).
func runDoctor(args []string) int {
	doWrite := false
	for _, a := range args {
		switch a {
		case "-h", "--help":
			fmt.Fprint(os.Stdout, doctorUsage)
			return 0
		case "--write":
			doWrite = true
		default:
			fmt.Fprint(os.Stderr, doctorUsage)
			return 2
		}
	}
	out := os.Stdout

	// cgp itself.
	fmt.Fprintln(out, "cgp:")
	fmt.Fprintf(out, "  version   %s\n", buildinfo.Version)
	if exe, err := os.Executable(); err == nil {
		fmt.Fprintf(out, "  binary    %s\n", exe)
	}

	// Config files, in the priority order loadConfigs applies them.
	fmt.Fprintln(out, "\nconfig files (low → high priority):")
	for _, p := range configSearchPaths() {
		fmt.Fprintf(out, "  %-24s %s\n", p.label, statLabel(p.path))
	}
	if strings.TrimSpace(os.Getenv("CGP_ENV")) != "" {
		fmt.Fprintf(out, "  %-24s set\n", "CGP_ENV")
	} else {
		fmt.Fprintf(out, "  %-24s unset\n", "CGP_ENV")
	}

	// Resolved settings from the config layers.
	runnerName, ledgerDir, err := resolveRunnerAndLedger("", "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
		return 1
	}
	fmt.Fprintln(out, "\nresolved settings:")
	if runnerName == "" {
		fmt.Fprintf(out, "  %-14s %s\n", "cgp.runner", "shell (default — not set)")
	} else {
		fmt.Fprintf(out, "  %-14s %s\n", "cgp.runner", runnerName)
	}
	if ledgerDir == "" {
		fmt.Fprintf(out, "  %-14s %s\n", "cgp.ledger", "(unset — no cross-run job reuse)")
	} else {
		fmt.Fprintf(out, "  %-14s %s  %s\n", "cgp.ledger", ledgerDir, statLabel(ledgerDir))
	}

	// Schedulers on PATH.
	fmt.Fprintln(out, "\nschedulers on PATH:")
	det := detectSchedulers()
	for _, name := range sched.Names() {
		fmt.Fprintf(out, "  %-8s %s\n", name, det[name].line)
	}

	// Recommendation (and optional apply).
	rec := recommend(runnerName, det)
	fmt.Fprintln(out, "\nrecommendation:")
	for _, line := range rec.lines {
		fmt.Fprintf(out, "  %s\n", line)
	}
	code := 0
	if rec.kind == recBroken {
		code = 1 // configured runner cannot submit here
	}

	if doWrite {
		if rec.kind != recSuggestOne {
			fmt.Fprintf(out, "\n  --write made no change: %s\n", writeSkipReason(rec.kind))
			return code
		}
		path, err := writeRunnerConfig(rec.runner)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cgp: could not write config: %v\n", err)
			return 1
		}
		fmt.Fprintf(out, "\n  wrote: cgp.runner = %s  →  %s\n", rec.runner, path)
	} else if rec.kind == recSuggestOne {
		fmt.Fprintf(out, "\n  Run `cgp doctor --write` to add that line to ~/.cgp/config for you.\n")
	}
	return code
}

type configPath struct{ label, path string }

// configSearchPaths returns the file-based config locations loadConfigs checks,
// in application order (the CGP_ENV layer is reported separately).
func configSearchPaths() []configPath {
	var ps []configPath
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		ps = append(ps, configPath{"<bindir>/.cgprc", filepath.Join(filepath.Dir(exe), ".cgprc")})
	}
	ps = append(ps, configPath{"/etc/cgp/config", "/etc/cgp/config"})
	if home, err := os.UserHomeDir(); err == nil {
		ps = append(ps, configPath{"~/.cgp/config", filepath.Join(home, ".cgp", "config")})
	}
	return ps
}

// statLabel describes whether a path exists (and, for the ledger, is a directory).
func statLabel(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return "not found"
	}
	if info.IsDir() {
		return "found (dir)"
	}
	return "found"
}

// schedInfo is the per-scheduler detection result.
type schedInfo struct {
	submitOK bool   // the submit command is on PATH
	line     string // human summary for the report
}

// detectSchedulers checks each scheduler's submit + status binaries on PATH,
// disambiguating the shared `qsub` between SGE and PBS via environment/tool
// signals.
func detectSchedulers() map[string]schedInfo {
	res := map[string]schedInfo{}
	for _, name := range sched.Names() {
		s, _ := sched.For(name)
		submit := ""
		if len(s.SubCmd) > 0 {
			submit = s.SubCmd[0]
		}
		submitOK := onPath(submit)
		var b strings.Builder
		fmt.Fprintf(&b, "%s: %s", submit, okMissing(submitOK))
		for _, sb := range statusBins(name) {
			if sb == submit {
				continue // same binary (e.g. batchq) — don't list it twice
			}
			fmt.Fprintf(&b, "   %s: %s", sb, okMissing(onPath(sb)))
		}
		switch {
		case !submitOK:
			b.WriteString("   → not installed")
		case name == "sge":
			switch {
			case sgeSignal() && !pbsSignal():
				b.WriteString("   → available (SGE_ROOT/qconf)")
			case pbsSignal() && !sgeSignal():
				b.WriteString("   → qsub present but looks like PBS")
			default:
				b.WriteString("   → qsub present, SGE-vs-PBS ambiguous")
			}
		case name == "pbs":
			switch {
			case pbsSignal() && !sgeSignal():
				b.WriteString("   → available (PBS_*/pbsnodes)")
			case sgeSignal() && !pbsSignal():
				b.WriteString("   → qsub present but looks like SGE")
			default:
				b.WriteString("   → qsub present, SGE-vs-PBS ambiguous")
			}
		default:
			b.WriteString("   → available")
		}
		res[name] = schedInfo{submitOK: submitOK, line: b.String()}
	}
	return res
}

// statusBins lists the status/probe commands cgp uses for a scheduler (the ones
// `cgp status` needs). Optional accounting tools (sacct/qacct) are not required.
func statusBins(name string) []string {
	switch name {
	case "slurm":
		return []string{"scontrol"}
	case "sge", "pbs":
		return []string{"qstat"}
	case "batchq":
		return []string{"batchq"}
	}
	return nil
}

// recommendation kinds.
const (
	recOK          = "ok"           // a scheduler runner is set and usable
	recBroken      = "broken"       // set, but its submit command is missing
	recNonSched    = "nonsched"     // set to a non-scheduler runner (e.g. shell)
	recSuggestOne  = "suggest-one"  // unset; exactly one scheduler detected
	recSuggestMany = "suggest-many" // unset; several detected — user must choose
	recAmbiguous   = "ambiguous"    // unset; qsub present but SGE-vs-PBS unclear
	recNone        = "none"         // unset; no scheduler detected
)

type recommendation struct {
	kind   string
	runner string   // the runner to write, for recSuggestOne
	lines  []string // human advice
}

// recommend produces structured advice given the configured runner and detection.
func recommend(runnerName string, det map[string]schedInfo) recommendation {
	// A runner is already configured.
	if runnerName != "" && runnerName != "shell" {
		d, isSched := det[runnerName]
		if !isSched {
			return recommendation{kind: recNonSched, lines: []string{
				fmt.Sprintf("cgp.runner is set to %q (a non-scheduler runner); nothing to submit.", runnerName)}}
		}
		if d.submitOK {
			return recommendation{kind: recOK, lines: []string{
				fmt.Sprintf("cgp.runner is set to %q and its tools are on PATH — you're set to submit.", runnerName)}}
		}
		return recommendation{kind: recBroken, lines: []string{
			fmt.Sprintf("cgp.runner is set to %q, but its submit command is NOT on PATH.", runnerName),
			"Fix PATH (e.g. load the scheduler module), or change cgp.runner.",
		}}
	}

	// Not configured: figure out what's unambiguously usable.
	var clear []string
	qsubAmbiguous := false
	for _, name := range sched.Names() {
		if !det[name].submitOK {
			continue
		}
		switch name {
		case "sge":
			if sgeSignal() && !pbsSignal() {
				clear = append(clear, "sge")
			} else if !pbsSignal() {
				qsubAmbiguous = true
			}
		case "pbs":
			if pbsSignal() && !sgeSignal() {
				clear = append(clear, "pbs")
			} else if !sgeSignal() {
				qsubAmbiguous = true
			}
		default:
			clear = append(clear, name)
		}
	}
	sort.Strings(clear)

	switch {
	case len(clear) == 1:
		return recommendation{kind: recSuggestOne, runner: clear[0], lines: []string{
			fmt.Sprintf("Detected %s. To submit jobs, set in ~/.cgp/config:", clear[0]),
			"",
			fmt.Sprintf("    cgp.runner = %q", clear[0]),
			"",
			"(cgp.runner is unset, so cgp currently just renders a bash script to stdout.)",
		}}
	case len(clear) > 1:
		return recommendation{kind: recSuggestMany, lines: []string{
			fmt.Sprintf("Multiple schedulers detected (%s). Pick one in ~/.cgp/config:", strings.Join(clear, ", ")),
			fmt.Sprintf("    cgp.runner = %q", clear[0]),
		}}
	case qsubAmbiguous:
		return recommendation{kind: recAmbiguous, lines: []string{
			"`qsub` is on PATH but SGE vs PBS is ambiguous (no SGE_ROOT/PBS_* signal).",
			"Set the one your cluster uses explicitly in ~/.cgp/config:",
			`    cgp.runner = "sge"      # or: "pbs"`,
		}}
	default:
		return recommendation{kind: recNone, lines: []string{
			"No batch scheduler detected — cgp will render a bash script to stdout (the default).",
			"To run those scripts locally, set:  cgp.runner.shell.autoexec = true",
		}}
	}
}

// writeSkipReason explains why --write declined to act for a non-suggest-one kind.
func writeSkipReason(kind string) string {
	switch kind {
	case recOK, recNonSched:
		return "cgp.runner is already set"
	case recBroken:
		return "the configured runner's tools are missing — fix PATH or set it yourself"
	case recSuggestMany:
		return "several schedulers detected — choose one and set it yourself"
	case recAmbiguous:
		return "SGE vs PBS is ambiguous — set it explicitly (cgp never guesses)"
	default:
		return "no scheduler detected"
	}
}

// writeRunnerConfig appends `cgp.runner = <runner>` to ~/.cgp/config (creating the
// directory/file as needed) and returns the path written. It is only called for an
// unambiguous recommendation where cgp.runner is not already set in config.
func writeRunnerConfig(runner string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".cgp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "config")
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	buf := existing
	if len(buf) > 0 && buf[len(buf)-1] != '\n' {
		buf = append(buf, '\n')
	}
	buf = append(buf, []byte(fmt.Sprintf("cgp.runner = %q\n", runner))...)
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// sgeSignal / pbsSignal disambiguate a shared `qsub` using environment variables
// and management tools specific to each system.
func sgeSignal() bool {
	return os.Getenv("SGE_ROOT") != "" || os.Getenv("SGE_CELL") != "" || onPath("qconf") || onPath("qhost")
}

func pbsSignal() bool {
	for _, e := range []string{"PBS_HOME", "PBS_SERVER", "PBS_DEFAULT", "PBS_O_HOST"} {
		if os.Getenv(e) != "" {
			return true
		}
	}
	return onPath("pbsnodes") || onPath("pbs_server")
}

func onPath(name string) bool {
	if name == "" {
		return false
	}
	_, err := exec.LookPath(name)
	return err == nil
}

func okMissing(ok bool) string {
	if ok {
		return "ok"
	}
	return "missing"
}
