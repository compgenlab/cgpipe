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
	"github.com/compgen-io/cgp/internal/parser"
	"github.com/compgen-io/cgp/internal/runner/shell"
)

const usage = `cgp — run a .cgp pipeline

usage:
    cgp [options] <pipeline.cgp> [goal ...] [--name value ...]
    cgp version

options (single hyphen):
    -h           show this help
    -dr          render the shell scripts instead of executing them

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
		fmt.Fprintln(os.Stderr, "cgp: ledger subcommand not implemented yet")
		return 1
	}

	file := args[0]
	vars := map[string]eval.Value{}
	var goals []string
	dryRun := false

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
				fmt.Print(usage)
				return 0
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

	prog, err := eval.Run(f, eval.Options{File: file, Vars: vars})
	if err != nil {
		var ex *eval.ExitError
		if errors.As(err, &ex) {
			return ex.Code
		}
		fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
		return 1
	}

	if err := shell.Run(prog, shell.Options{Goals: goals, DryRun: dryRun}); err != nil {
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
