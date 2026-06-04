// Command cgsub submits a single one-off job, recording it in the ledger the
// same way cgp does. It is the standalone counterpart to cgp for "submit this
// one command as a job" — kept as its own binary rather than a cgp subcommand.
//
// Phase 0 skeleton: argument handling and version/help only.
package main

import (
	"fmt"
	"os"

	"github.com/compgen-io/cgp/internal/buildinfo"
)

const usage = `cgsub — submit a single job, recorded in the cgp ledger

usage:
    cgsub [flags] <command> [args ...]
    cgsub <command> {} -- <file> [file ...]    # xargs-style fan-out: one job per file
    cgsub version

flags:
    -h, --help       show this help
    -v, --version    print version

Phase 0 skeleton; submission is not implemented yet (Phase 1).
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
	case "-h", "--help", "help":
		fmt.Print(usage)
		return 0
	case "-v", "--version", "version":
		fmt.Printf("cgsub %s\n", buildinfo.Version)
		return 0
	}

	fmt.Fprintln(os.Stderr, "cgsub: job submission is not implemented yet (Phase 1)")
	return 1
}
