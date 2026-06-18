package lsp

// Short Markdown descriptions surfaced by hover and completion. They summarize
// the language constructs documented in docs/language-spec.md. The keys are kept
// in sync with the canonical ast.BuiltinStmts / ast.ReservedTargets lists by the
// drift-guard test (docs_test.go).

var builtinDocs = map[string]string{
	"print":    "`print <expr>...` — evaluate the expressions and write them to stdout, space-separated.",
	"exit":     "`exit [code]` — stop the pipeline immediately; the optional integer becomes cgpipe's exit status.",
	"unset":    "`unset <name>` — remove a variable binding.",
	"var":      "`var <name> [= <value>]` — declare a variable in the current lexical scope (a `{ }` block). A bare `name = …` writes through to an existing binding; `var` forces a new local one — which a deeper block can then assign through, and which owns any write handle bound to it (closed when the scope exits).",
	"include":  "`include <path>` — splice another cgpipe file into this one.",
	"snippet":  "`snippet <name> {{ ... }}` — define a reusable shell-body fragment, invoked as `@name` inside a `{{ }}` body.",
	"eval":     "`eval <string>` — parse and run cgpipe source produced at runtime.",
	"sleep":    "`sleep <seconds>` — pause execution.",
	"dumpvars": "`dumpvars` — print all currently-defined variables (debugging aid).",
	"showhelp": "`showhelp` — print the script's leading comment block as help text.",
	"export":   "`export <name> = <value>` — expose a value from this (stage) pipeline to a calling workflow, referenced there as `${stage.name}`.",
	"stage":    "`stage <name> <pipeline-file> [--arg value ...]` — declare a workflow stage that runs another pipeline; its exports are available as `${name.export}`.",
}

var keywordDocs = map[string]string{
	"if":    "`if <cond> { ... } [elif <cond> { ... }] [else { ... }]` — conditional execution.",
	"elif":  "`elif <cond> { ... }` — an additional condition on an `if` chain.",
	"else":  "`else { ... }` — the fallback branch of an `if` chain.",
	"for":   "`for <var> in <iterable> [with <i>] { ... }` — iterate over a list or a range. Ranges are inclusive and may descend (`5..1`).",
	"in":    "Separates the loop variable from its iterable in a `for` loop.",
	"with":  "`for <var> in <iterable> with <i> { ... }` — bind `<i>` to the 1-based loop index alongside `<var>`.",
	"true":  "Boolean literal `true`.",
	"false": "Boolean literal `false`.",
}

var reservedTargetDocs = map[string]string{
	"pre":        "`@pre` — runs before the build graph executes.",
	"post":       "`@post` — runs after the build graph completes.",
	"setup":      "`@setup` — one-time setup before any jobs are submitted.",
	"teardown":   "`@teardown` — cleanup after all jobs finish.",
	"postsubmit": "`@postsubmit` — runs after each job is submitted; `${jobid}` is available.",
	"default":    "`@default` — the target built when cgpipe is run with no explicit target.",
}
