// Package runner submits the jobs in a dependency graph to an execution
// backend. The shell runner (subpackage shell) renders to bash and is the
// default; scheduler runners (slurm, sge, pbs, batchq) and the graphviz runner
// follow the same template-based architecture.
//
// See docs/language-spec.md §11 (cgp.runner.*).
package runner
