package lsp

import "testing"

func findItem(items []completionItem, label string) (completionItem, bool) {
	for _, it := range items {
		if it.Label == label {
			return it, true
		}
	}
	return completionItem{}, false
}

func TestCompletionsIncludeKeywordsBuiltinsAndReserved(t *testing.T) {
	items := completions("", "test.cgp")

	if it, ok := findItem(items, "if"); !ok || it.Kind != ciKeyword {
		t.Errorf("keyword `if` = %+v (ok=%v), want kind ciKeyword", it, ok)
	}
	if it, ok := findItem(items, "with"); !ok || it.Kind != ciKeyword {
		t.Errorf("keyword `with` = %+v (ok=%v), want kind ciKeyword", it, ok)
	}
	if it, ok := findItem(items, "print"); !ok || it.Kind != ciFunction {
		t.Errorf("built-in `print` = %+v (ok=%v), want kind ciFunction", it, ok)
	}
	if it, ok := findItem(items, "var"); !ok || it.Kind != ciFunction {
		t.Errorf("built-in `var` = %+v (ok=%v), want kind ciFunction", it, ok)
	}
	if it, ok := findItem(items, "@default"); !ok || it.Kind != ciKeyword {
		t.Errorf("reserved target `@default` = %+v (ok=%v), want kind ciKeyword", it, ok)
	}
}

func TestCompletionsHarvestDocumentVariables(t *testing.T) {
	items := completions("myvar = 41\nprint myvar\n", "test.cgp")

	it, ok := findItem(items, "myvar")
	if !ok || it.Kind != ciVariable {
		t.Errorf("document variable `myvar` = %+v (ok=%v), want kind ciVariable", it, ok)
	}
	// A built-in statement word must not also be offered as a (variable) completion.
	for _, it := range items {
		if it.Label == "print" && it.Kind == ciVariable {
			t.Errorf("`print` offered as a variable; built-ins should be excluded from harvest")
		}
	}
}
