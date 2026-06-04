// Package shell is the default runner: it renders the dependency graph to a
// bash script (one function per target, dependency-ordered) and optionally
// executes it directly. It is the target for the Phase 0 end-to-end MVP.
package shell
