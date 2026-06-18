package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/compgenlab/cgpipe/internal/convert"
)

const convertUsage = `cgpipe convert — migrate a legacy cgpipe-jvm script to cgpipe

usage:
    cgpipe convert <old.cgp> [-o out.cgp]

Reads a legacy (JVM-cgpipe-era) script and writes the cgpipe-equivalent to stdout
(or to -o FILE). Best-effort: the mechanical differences are rewritten and
anything that can't be converted safely is annotated with a "# cgpipe-convert:"
comment. Review the result before running it.
`

// runConvert handles `cgpipe convert <old.cgp> [-o out.cgp]`.
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
				fmt.Fprintln(os.Stderr, "cgpipe convert: -o needs a value")
				return 2
			}
			i++
			out = args[i]
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(os.Stderr, "cgpipe convert: unknown option %s\n", a)
			return 2
		default:
			if in != "" {
				fmt.Fprintln(os.Stderr, "cgpipe convert: only one input file")
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
		fmt.Fprintf(os.Stderr, "cgpipe: %v\n", err)
		return 1
	}
	converted, warnings := convert.Convert(string(src))

	if out == "" {
		fmt.Print(converted)
	} else if err := os.WriteFile(out, []byte(converted), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "cgpipe: %v\n", err)
		return 1
	}
	for _, w := range warnings {
		fmt.Fprintf(os.Stderr, "cgpipe convert: %s\n", w)
	}
	return 0
}
