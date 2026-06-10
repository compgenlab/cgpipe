package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/compgen-io/cgp/internal/ledger"
)

const ledgerUsage = `usage:
    cgp ledger dump <dir>                      dump all jobs as key/value TSV
    cgp ledger search [filters] <dir>          dump jobs matching the filters
    cgp ledger vacuum <dir>                     compact the ledger, dropping jobs that own no current output

search filters (substring match; combined with AND):
    -i PATH      an input path contains PATH
    -o PATH      an output path contains PATH
    -g PATTERN   a job-script line contains PATTERN (grep)
    -name NAME   the job name contains NAME
    -id JOBID    the job id (exact)
`

// runLedger handles `cgp ledger <subcommand> ...`.
func runLedger(args []string) int {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, ledgerUsage)
		return 2
	}
	switch args[0] {
	case "dump":
		return runLedgerDump(args[1:])
	case "search":
		return runLedgerSearch(args[1:])
	case "vacuum":
		if len(args) < 2 {
			fmt.Fprint(os.Stderr, ledgerUsage)
			return 2
		}
		lg, err := ledger.Open(args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
			return 1
		}
		defer lg.Close()
		if err := lg.Vacuum(); err != nil {
			fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
			return 1
		}
		return 0
	default:
		fmt.Fprint(os.Stderr, ledgerUsage)
		return 2
	}
}

// runLedgerDump handles `cgp ledger dump <db>`.
func runLedgerDump(args []string) int {
	if len(args) != 1 {
		fmt.Fprint(os.Stderr, ledgerUsage)
		return 2
	}
	lg, err := ledger.OpenRead(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
		return 1
	}
	defer lg.Close()
	if err := lg.Dump(os.Stdout, nil); err != nil {
		fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
		return 1
	}
	return 0
}

// runLedgerSearch handles `cgp ledger search [filters] <db>`.
func runLedgerSearch(args []string) int {
	var f ledger.Filter
	var db string
	c := newArgCursor(args)
	for c.more() {
		a := c.cur()
		// val consumes a filter's value; on a missing value it prints usage and
		// the caller returns 2.
		val := func() (string, bool) {
			v, ok := c.value()
			if !ok {
				fmt.Fprint(os.Stderr, ledgerUsage)
			}
			return v, ok
		}
		switch a {
		case "-i":
			v, ok := val()
			if !ok {
				return 2
			}
			f.Input = v
		case "-o":
			v, ok := val()
			if !ok {
				return 2
			}
			f.Output = v
		case "-g":
			v, ok := val()
			if !ok {
				return 2
			}
			f.Grep = v
		case "-name":
			v, ok := val()
			if !ok {
				return 2
			}
			f.Name = v
		case "-id":
			v, ok := val()
			if !ok {
				return 2
			}
			f.ID = v
		default:
			if strings.HasPrefix(a, "-") || db != "" {
				fmt.Fprint(os.Stderr, ledgerUsage)
				return 2
			}
			db = a
			c.advance()
		}
	}
	if db == "" || (f == ledger.Filter{}) {
		fmt.Fprint(os.Stderr, ledgerUsage)
		return 2
	}
	lg, err := ledger.OpenRead(db)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
		return 1
	}
	defer lg.Close()
	ids, err := lg.Search(f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
		return 1
	}
	if len(ids) == 0 {
		return 0 // no matches: dump nothing (an empty set is not "everything")
	}
	if err := lg.Dump(os.Stdout, ids); err != nil {
		fmt.Fprintf(os.Stderr, "cgp: %v\n", err)
		return 1
	}
	return 0
}
