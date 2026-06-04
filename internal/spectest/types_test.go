package spectest

import "testing"

// §2 Data types: six types, each reporting its name via .type().
func TestTypeNames(t *testing.T) {
	cases := []struct{ src, want string }{
		{"flag = true\nprint flag.type()", "bool"},
		{"count = 10\nprint count.type()", "int"},
		{"rate = 0.5\nprint rate.type()", "float"},
		{`name = "sample-1"` + "\nprint name.type()", "string"},
		{"samples = []\nprint samples.type()", "list"},
		{"chunks = 1..100\nprint chunks.type()", "range"},
	}
	for _, c := range cases {
		if got := printed(t, c.src); got != c.want+"\n" {
			t.Errorf("%q: type = %q, want %q", c.src, got, c.want)
		}
	}
}

// §2 bool literals are case-sensitive true/false.
func TestBoolLiterals(t *testing.T) {
	if got := printed(t, "if true { print \"yes\" }\nif !false { print \"also\" }"); got != "yes\nalso\n" {
		t.Errorf("bool literals: out = %q", got)
	}
}

// §2 Lists may mix types.
func TestMixedTypeList(t *testing.T) {
	if got := printsExpr(t, `[1, 2, "x"].length()`); got != "3" {
		t.Errorf("mixed list length = %q, want 3", got)
	}
}

// §2 A range iterates as 1..N inclusive of N.
func TestRangeIsInclusive(t *testing.T) {
	if got := printed(t, "for i in 1..3 { print i }"); got != "1\n2\n3\n" {
		t.Errorf("range iteration = %q, want 1..3 inclusive", got)
	}
}
