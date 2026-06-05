package main

import (
	"fmt"
	"os"

	"github.com/compgen-io/cgp/internal/lsp"
)

const lspUsage = `cgp lsp — run the cgp language server

The server speaks the Language Server Protocol over stdin/stdout and is meant to
be launched by an editor, not run interactively. It provides syntax diagnostics,
semantic tokens, hover, and completion for .cgp files.

usage:
    cgp lsp
`

func runLSP(args []string) int {
	for _, a := range args {
		switch a {
		case "-h", "--help", "help":
			fmt.Fprint(os.Stdout, lspUsage)
			return 0
		default:
			fmt.Fprintf(os.Stderr, "cgp lsp: unknown argument %q\n", a)
			return 2
		}
	}
	// stdout is the protocol channel; keep diagnostics on stderr.
	if err := lsp.Run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "cgp lsp: %v\n", err)
		return 1
	}
	return 0
}
