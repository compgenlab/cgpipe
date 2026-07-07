// Package debug is cgpipe's tiny leveled trace facility. It is off by default
// (level 0); the CLI raises the level from -debug N / CGP_DEBUG. Higher levels
// print progressively more detail about what the interpreter and runner are
// doing — useful when a pipeline won't resolve, stalls, or behaves unexpectedly.
//
// Conventions for the levels (higher includes everything below it):
//
//	1  high-level phases (config, parse, eval done, runner, ledger, build)
//	2  DAG resolution decisions (resolve/staleness, producer chosen, arrays)
//	3  submits, reuse decisions, and each scheduler "is this job active?" probe
//	4  interpreter detail (assignments, for-iterations, target collection)
//	5  finest detail (dependency-edge lists, per-record folds, collapse)
//
// All output goes to stderr (a settable writer in tests), so it never collides
// with a pipeline's rendered scripts or report output on stdout.
package debug

import (
	"fmt"
	"io"
	"os"
	"sync"
)

var (
	mu    sync.Mutex
	level int
	out   io.Writer = os.Stderr
)

// SetLevel sets the global verbosity (0 = off). Higher = more detail.
func SetLevel(n int) {
	mu.Lock()
	level = n
	mu.Unlock()
}

// Level returns the current verbosity.
func Level() int {
	mu.Lock()
	defer mu.Unlock()
	return level
}

// On reports whether output at verbosity n would be emitted. Use it to guard
// work that is only needed to build a debug message.
func On(n int) bool {
	mu.Lock()
	defer mu.Unlock()
	return level >= n
}

// SetWriter redirects debug output (default os.Stderr). Returns the previous
// writer so a test can restore it.
func SetWriter(w io.Writer) io.Writer {
	mu.Lock()
	prev := out
	out = w
	mu.Unlock()
	return prev
}

// Logf emits one trace line at verbosity n, prefixed "cgpipe[dN] ", when the
// current level is >= n. A trailing newline is added.
func Logf(n int, format string, args ...any) {
	mu.Lock()
	defer mu.Unlock()
	if level < n || out == nil {
		return
	}
	fmt.Fprintf(out, "cgpipe[d%d] ", n)
	fmt.Fprintf(out, format, args...)
	fmt.Fprintln(out)
}
