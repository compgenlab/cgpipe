// Package parser is a hand-rolled recursive-descent parser that turns a token
// stream into an AST. Hand-rolled (rather than generated) for the best error
// messages — compiler-style diagnostics with a source line and caret.
//
// See docs/language-spec.md §5–§8.
package parser
