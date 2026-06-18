package spectest

import (
	"testing"

	"github.com/compgenlab/cgpipe/internal/eval"
)

// §3 Variables.

// `=` sets; the last write wins.
func TestAssign(t *testing.T) {
	if got := printed(t, "x = 1\nx = 2\nprint x"); got != "2\n" {
		t.Errorf("= : out = %q", got)
	}
}

// `?=` sets only if not already set.
func TestDefaultAssign(t *testing.T) {
	if got := printed(t, "x = 1\nx ?= 2\nprint x"); got != "1\n" {
		t.Errorf("?= overrode an existing value: %q", got)
	}
	if got := printed(t, "y ?= 2\nprint y"); got != "2\n" {
		t.Errorf("?= did not set an unset var: %q", got)
	}
}

// §3.1 / §11.2 `?=` respects an upstream CLI value (applied first), but `=`
// always wins.
func TestDefaultRespectsCLIVar(t *testing.T) {
	_, out := build(t, "threads ?= 4\nprint threads", map[string]eval.Value{"threads": eval.IntVal(16)})
	if out != "16\n" {
		t.Errorf("?= overrode a CLI var: %q", out)
	}
	_, out = build(t, "threads = 4\nprint threads", map[string]eval.Value{"threads": eval.IntVal(16)})
	if out != "4\n" {
		t.Errorf("= did not beat a CLI var: %q", out)
	}
}

// `+=` appends, promoting a scalar to a list.
func TestAppendAssign(t *testing.T) {
	if got := printed(t, "x = \"a\"\nx += \"b\"\nprint x"); got != "a b\n" {
		t.Errorf("+= promote-and-append: out = %q (lists print space-joined)", got)
	}
	if got := printsExpr(t, "[].type()"); got != "list" {
		t.Errorf("sanity: empty list type = %q", got)
	}
}

// `unset` removes a variable; `!foo` then reads as "unset or false".
func TestUnset(t *testing.T) {
	if got := printed(t, "x = 1\nunset x\nif !x { print \"gone\" }"); got != "gone\n" {
		t.Errorf("unset: out = %q", got)
	}
}
