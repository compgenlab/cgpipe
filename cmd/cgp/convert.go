package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/compgen-io/cgp/internal/convert"
)

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
