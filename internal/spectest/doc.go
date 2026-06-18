// Package spectest is the spec-driven conformance suite for the cgpipe language.
//
// Where the per-package tests (lexer, parser, eval, …) verify that each
// component works in isolation, the tests here drive the whole stack —
// parse → eval → render → run — once per feature documented in
// docs/language-spec.md, organized section by section so coverage maps 1:1 to
// the spec. A spec feature with no test here is a visible gap.
//
// The files mirror the spec:
//
//	lexical_test.go      §1  Lexical structure
//	types_test.go        §2  Data types
//	variables_test.go    §3  Variables
//	expressions_test.go  §4  Expressions
//	statements_test.go   §5  Statements and control flow
//	bodies_test.go       §6  Target bodies
//	targets_test.go      §7  Target declaration features
//	reserved_test.go     §8  Reserved targets
//	methods_test.go      §9  Methods on built-in types
//	config_test.go       §11 Configuration
//	containers_test.go   §12 Containers and GPUs
//	scheduler_test.go    §10/§13 ledger + scheduler submission (mock sbatch/qsub/…)
//
// The package has no non-test source beyond this doc.
package spectest
