package spectest

import "testing"

// §4.1 Operators: arithmetic, string/list +/*, comparison, logic.
func TestOperators(t *testing.T) {
	cases := []struct{ expr, want string }{
		{"1 + 2 * 3", "7"},
		{"(1 + 2) * 3", "9"},
		{"2 ** 3 ** 2", "512"}, // right-associative power
		{"17 % 5", "2"},
		{`"ab" + "cd"`, "abcd"},   // + concatenates strings
		{`"x" * 3`, "xxx"},        // * repeats strings
		{`[1, 2] + [3]`, "1 2 3"}, // + concatenates lists (printed space-joined)
		{"1 < 2", "true"},
		{"3 >= 4", "false"},
		{`"a" == "a"`, "true"},
		{"1 == 1 && 2 == 3", "false"},
		{"1 == 2 || 3 == 3", "true"},
		{"1 == 1.0", "true"},
	}
	for _, c := range cases {
		if got := printsExpr(t, c.expr); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// §4.1 `!foo` is the argument-guard idiom: true when foo is unset, false, 0, or "".
func TestNotIsUnsetOrFalse(t *testing.T) {
	for _, expr := range []string{"!false", "!0", `!""`} {
		if got := printsExpr(t, expr); got != "true" {
			t.Errorf("%s = %q, want true", expr, got)
		}
	}
	if got := printed(t, "if !missing { print \"guarded\" }"); got != "guarded\n" {
		t.Errorf("!unset guard: out = %q", got)
	}
}

// §4.2 Indexing and slicing: zero-based, negative-from-end, Python-style slices.
func TestIndexAndSlice(t *testing.T) {
	base := `foo = ["one", "two", "three"]` + "\n"
	cases := []struct{ expr, want string }{
		{"foo[0]", "one"},
		{"foo[-1]", "three"},
		{"foo[1:]", "two three"},
		{"foo[:2]", "one two"},
	}
	for _, c := range cases {
		if got := printed(t, base+"print "+c.expr); got != c.want+"\n" {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// §4.3 String substitution forms inside a literal.
func TestStringSubstitutionForms(t *testing.T) {
	// ${var}: substitute, error if unset (tested separately); list joins with spaces
	if got := printed(t, `sample = "p42"`+"\n"+`print "results/${sample}/out.vcf"`); got != "results/p42/out.vcf\n" {
		t.Errorf("${var}: %q", got)
	}
	// ${var?}: empty when unset
	if got := printed(t, `print "[${missing?}]"`); got != "[]\n" {
		t.Errorf("${var?}: %q", got)
	}
	// ${expr}: arbitrary expression, methods, indexing
	if got := printed(t, `xs = ["a", "b"]`+"\n"+`print "first=${xs[0]} n=${xs.length()}"`); got != "first=a n=2\n" {
		t.Errorf("${expr}: %q", got)
	}
	// @{list}: one copy per element, expanding the whole surrounding string
	if got := printed(t, `xs = ["a", "b", "c"]`+"\n"+`print "p-@{xs}-s"`); got != "p-a-s p-b-s p-c-s\n" {
		t.Errorf("@{list}: %q", got)
	}
	// @{N..M}: range expansion
	if got := printed(t, `print "@{1..3}"`); got != "1 2 3\n" {
		t.Errorf("@{range}: %q", got)
	}
	// ${{var}}: double evaluation — content is itself a template
	if got := printed(t, `name = "bob"`+"\n"+`tmpl = "hi ${name}"`+"\n"+`print "${{tmpl}}"`); got != "hi bob\n" {
		t.Errorf("${{var}}: %q", got)
	}
	// $(cmd): run at parse time, substitute stdout
	if got := printed(t, `print "[$(printf hello)]"`); got != "[hello]\n" {
		t.Errorf("$(cmd): %q", got)
	}
}

// §4.3 ${var} errors when var is unset (unlike ${var?}).
func TestUnsetSubstitutionErrors(t *testing.T) {
	wantErr(t, "unset ${var}", `print "${definitely_missing}"`)
}

// §4.3 Escaping: `\$` / `\@` produce a literal.
func TestSubstitutionEscaping(t *testing.T) {
	if got := printed(t, `sample = "x"`+"\n"+`print "\${sample} and \@{list}"`); got != "${sample} and @{list}\n" {
		t.Errorf("escaping: %q", got)
	}
}
