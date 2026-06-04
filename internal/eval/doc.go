// Package eval walks the AST in global context: variable scope and assignment
// (`=`, `?=`, `+=`), control flow (`if`/`for`), expression evaluation and
// methods, and target collection. The result is the dependency graph of
// concrete targets to build (dynamic generation happens here, as `for` loops
// emit targets at parse/eval time).
//
// See docs/language-spec.md §2–§7.
package eval
