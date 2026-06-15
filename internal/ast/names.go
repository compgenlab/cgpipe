package ast

// Canonical name lists for the language's statement-leading built-ins and its
// reserved (@-prefixed) targets. These are the single source of truth shared by
// the parser (validation) and the language server (highlighting, completion,
// hover docs), so the tooling cannot drift from the language.
//
// The parser's per-word dispatch (parseStmt) and the evaluator's Special-target
// switch route each name to its own handler/field and necessarily enumerate the
// names structurally; the drift-guard test in the lsp package keeps the doc maps
// aligned with these slices.

// BuiltinStmts are the statement-leading built-in words, in a stable order
// (used for completion ordering).
var BuiltinStmts = []string{
	"print", "exit", "unset", "var", "include", "snippet",
	"eval", "sleep", "dumpvars", "showhelp", "export", "stage",
}

// ReservedTargets are the recognized @-prefixed target names, in a stable order.
var ReservedTargets = []string{
	"pre", "post", "setup", "teardown", "postsubmit", "default",
}
