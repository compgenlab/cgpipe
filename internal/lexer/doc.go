// Package lexer turns cgp source into a stream of tokens.
//
// Its defining responsibility is the mode flip between cgp code and raw shell
// bodies: on `{{` it switches to capture mode, reading the body verbatim until
// a lone `}}` line, rather than tokenizing it as cgp. `{ }` blocks stay in
// token mode. Inside a captured body, `%`-prefixed lines are cgp code.
//
// See docs/language-spec.md §1.5 (the two block delimiters) and §6 (target bodies).
package lexer
