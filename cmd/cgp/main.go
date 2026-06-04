// Command cgp runs a pipeline (or workflow) described in a .cgp file.
//
// This is the Phase 0 skeleton: argument handling and version/help are wired
// up, but pipeline evaluation is not yet implemented. See docs/language-spec.md
// for the language this tool will implement.
package main

import (
	"fmt"
	"os"

	"github.com/compgen-io/cgp/internal/buildinfo"
)

const usage = `cgp — run a .cgp pipeline or workflow

usage:
    cgp [flags] <pipeline.cgp> [-name value ...] [-- args]
    cgp version
    cgp ledger vacuum <ledger.db>

flags:
    -h, --help       show this help
    -v, --version    print version

cgp is the Go rewrite of cgpipe. This build is a Phase 0 skeleton; pipeline
evaluation is not implemented yet.
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
		fmt.Printf("cgp %s\n", buildinfo.Version)
		return 0
	case "ledger":
		fmt.Fprintln(os.Stderr, "cgp: ledger subcommand not implemented yet (Phase 1)")
		return 1
	}

	fmt.Fprintf(os.Stderr, "cgp: running pipelines is not implemented yet (Phase 1): %s\n", args[0])
	return 1
}
