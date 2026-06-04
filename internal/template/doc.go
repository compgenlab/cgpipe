// Package template renders a captured target body at job-submission time:
// `${…}` substitution (incl. `${if c; a; b}` and `@{…}`), the directive block
// before `--`, and `%`-prefixed cgp control lines (e.g. `% for x in list {`).
// The body is processed as text — this package never parses it as shell.
//
// See docs/language-spec.md §6 (target bodies).
package template
