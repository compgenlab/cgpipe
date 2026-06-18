package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/compgenlab/cgpipe/internal/runner/sched"
)

const showTemplateUsage = `cgpipe show-template — print a scheduler's built-in submission template

usage:
    cgpipe show-template -r <slurm|sge|pbs|batchq>

Writes the built-in template to stdout so you can use it as a starting point.
Save it and point a runner at it (or drop it at ~/.cgpipe/custom_template.cgp) to
override the built-in submission script while keeping the rest of the runner's
config:

    cgpipe show-template -r slurm > ~/.cgpipe/custom_template.cgp
`

// runShowTemplate handles `cgpipe show-template -r <runner>`.
func runShowTemplate(args []string) int {
	runnerName := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "-h", "help":
			fmt.Print(showTemplateUsage)
			return 0
		case "-r":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "cgpipe show-template: option -r needs a value")
				return 2
			}
			i++
			runnerName = args[i]
		default:
			fmt.Fprintf(os.Stderr, "cgpipe show-template: unknown option %s\n", a)
			return 2
		}
	}
	if runnerName == "" {
		fmt.Fprint(os.Stderr, showTemplateUsage)
		return 2
	}
	tmpl, ok := sched.DefaultTemplate(runnerName)
	if !ok {
		fmt.Fprintf(os.Stderr, "cgpipe show-template: unknown runner %q (have: %s)\n",
			runnerName, strings.Join(sched.Names(), ", "))
		return 2
	}
	fmt.Print(tmpl) // built-in templates already end in a newline
	return 0
}
