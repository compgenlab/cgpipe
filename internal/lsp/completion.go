package lsp

import (
	"github.com/compgen-io/cgp/internal/lexer"
	"github.com/compgen-io/cgp/internal/token"
)

// completions returns context-free completion items: the language keywords,
// built-in statements, reserved targets, and variable names harvested from the
// current document.
func completions(src, file string) []completionItem {
	items := make([]completionItem, 0, 32)

	for _, k := range []string{"if", "elif", "else", "for", "in", "true", "false"} {
		items = append(items, completionItem{Label: k, Kind: ciKeyword, Documentation: keywordDocs[k]})
	}
	for _, b := range builtinList {
		items = append(items, completionItem{
			Label:         b,
			Kind:          ciFunction,
			Detail:        "built-in statement",
			Documentation: builtinDocs[b],
		})
	}
	for _, r := range []string{"pre", "post", "setup", "teardown", "postsubmit", "default"} {
		items = append(items, completionItem{
			Label:         "@" + r,
			Kind:          ciKeyword,
			Detail:        "reserved target",
			Documentation: reservedTargetDocs[r],
		})
	}

	// Variables defined or referenced in the document.
	seen := map[string]bool{}
	for _, t := range lexer.Tokenize(src, file) {
		if t.Kind == token.IDENT && !builtinStmts[t.Lit] && !seen[t.Lit] {
			seen[t.Lit] = true
			items = append(items, completionItem{Label: t.Lit, Kind: ciVariable})
		}
	}
	return items
}
