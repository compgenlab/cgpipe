package lsp

import (
	"testing"

	"github.com/compgen-io/cgp/internal/ast"
)

// TestDocKeysMatchCanonicalLists guards against drift: every canonical built-in
// statement and reserved target must have a hover/completion doc string, and the
// doc maps must not describe anything that isn't in the canonical lists.
func TestDocKeysMatchCanonicalLists(t *testing.T) {
	assertSameSet(t, "builtin", ast.BuiltinStmts, builtinDocs)
	assertSameSet(t, "reserved target", ast.ReservedTargets, reservedTargetDocs)
}

func assertSameSet(t *testing.T, kind string, names []string, docs map[string]string) {
	t.Helper()
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
		if _, ok := docs[n]; !ok {
			t.Errorf("%s %q has no doc string", kind, n)
		}
	}
	for k := range docs {
		if !want[k] {
			t.Errorf("doc string for unknown %s %q (not in canonical list)", kind, k)
		}
	}
}
