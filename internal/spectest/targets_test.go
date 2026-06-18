package spectest

import (
	"strings"
	"testing"
	"time"
)

// §7.1 Build-variable substitutions: ${input}/${output}, indexed forms.
func TestBuildVarsIndexed(t *testing.T) {
	got := render(t, "x y: a b c {{\n    use ${input[0]} ${output[1]}\n}}")
	if lines := shellLines(got); len(lines) != 1 || lines[0] != "use a y" {
		t.Errorf("indexed build vars: %v", lines)
	}
}

// §7.1 ${stem} carries the wildcard stem into the body (also §7.3).
func TestWildcardStem(t *testing.T) {
	chdirTmp(t)
	writeFile(t, "foo.in", "data")
	runReal(t, "%.out: %.in {{\n    echo ${stem} > ${output}\n}}\n@default: foo.out", "foo.out")
	if got := strings.TrimSpace(readFile(t, "foo.out")); got != "foo" {
		t.Errorf("wildcard stem: foo.out = %q, want foo", got)
	}
}

// §7.3 `%` matches one or more characters: a single-character and a
// multi-character stem both work, and the stem is reused on the input side.
func TestWildcardSingleAndMultiChar(t *testing.T) {
	t.Run("single_char", func(t *testing.T) {
		chdirTmp(t)
		writeFile(t, "a.in", "x")
		runReal(t, "%.out: %.in {{\n    echo ${stem} > ${output}\n}}\n@default: a.out", "a.out")
		if got := strings.TrimSpace(readFile(t, "a.out")); got != "a" {
			t.Errorf("single-char stem = %q, want a", got)
		}
	})
	t.Run("multi_char", func(t *testing.T) {
		chdirTmp(t)
		writeFile(t, "sample01.in", "x")
		runReal(t, "%.out: %.in {{\n    echo ${stem} > ${output}\n}}\n@default: sample01.out", "sample01.out")
		if got := strings.TrimSpace(readFile(t, "sample01.out")); got != "sample01" {
			t.Errorf("multi-char stem = %q, want sample01", got)
		}
	})
}

// §7.3 `%` requires at least one character: a zero-length stem does not match,
// so there is no rule to build a bare ".out".
func TestWildcardRequiresNonEmptyStem(t *testing.T) {
	chdirTmp(t)
	writeFile(t, ".in", "x")
	if err := runRealErr(t, "%.out: %.in {{\n    cp ${input} ${output}\n}}\n@default: .out", ".out"); err == nil {
		t.Error("a zero-length stem should not match the % wildcard")
	}
}

// §7.2 Multiple definitions for one output: the first whose inputs are all
// satisfiable is used.
func TestMultipleDefinitionsFirstSatisfiable(t *testing.T) {
	chdirTmp(t)
	writeFile(t, "have_b.txt", "B")
	// first rule needs a file nothing can produce; second is satisfiable.
	src := `out.txt: missing_a.txt {{
    echo via-a > ${output}
}}
out.txt: have_b.txt {{
    cat ${input} > ${output}
}}
@default: out.txt`
	runReal(t, src, "out.txt")
	if got := readFile(t, "out.txt"); got != "B" {
		t.Errorf("out.txt = %q, want the second (satisfiable) rule's output", got)
	}
}

// §7.2 A blanket wildcard rule provides a build path for an output that no
// explicit rule names — the canonical "index a .bam if the .bai is missing".
func TestWildcardProvidesFallbackPath(t *testing.T) {
	chdirTmp(t)
	writeFile(t, "x.bam", "reads")
	src := `%.bam.bai: %.bam {{
    echo "index of ${input}" > ${output}
}}
@default: x.bam.bai`
	runReal(t, src, "x.bam.bai")
	if got := strings.TrimSpace(readFile(t, "x.bam.bai")); got != "index of x.bam" {
		t.Errorf("x.bam.bai = %q, want \"index of x.bam\"", got)
	}
}

// §7.2 When an explicit rule and a wildcard both could build an output and both
// are satisfiable, the explicit rule wins (explicit candidates come first).
func TestExplicitPreferredOverWildcard(t *testing.T) {
	chdirTmp(t)
	writeFile(t, "in.txt", "data")
	writeFile(t, "special.src", "data") // makes the wildcard path satisfiable too
	src := `special.out: in.txt {{
    echo explicit > ${output}
}}
%.out: %.src {{
    echo wildcard > ${output}
}}
@default: special.out`
	runReal(t, src, "special.out")
	if got := strings.TrimSpace(readFile(t, "special.out")); got != "explicit" {
		t.Errorf("special.out = %q, want explicit (explicit rule precedes the wildcard)", got)
	}
}

// §7.2 If no definition is satisfiable, it is still an error (no build path).
func TestNoSatisfiablePathErrors(t *testing.T) {
	chdirTmp(t)
	src := `out.txt: missing_a.txt {{
    cp ${input} ${output}
}}
out.txt: missing_b.txt {{
    cp ${input} ${output}
}}
@default: out.txt`
	if err := runRealErr(t, src); err == nil {
		t.Fatal("expected a no-build-path error when no definition is satisfiable")
	}
}

// §7.4 @{list} in a declaration lists each item separately; ${input} then joins
// them with spaces in the body.
func TestListExpansionInDeclaration(t *testing.T) {
	got := render(t, `parts = ["a.txt", "b.txt", "c.txt"]
merged.txt: @{parts} {{
    cat ${input} > ${output}
}}`)
	mustContain(t, got, "cat a.txt b.txt c.txt > merged.txt")
}

// §7.7 A bodyless target is a pure aggregation rule: requesting it builds its
// inputs but produces no file of its own.
func TestBodylessAggregator(t *testing.T) {
	chdirTmp(t)
	src := `a.txt: {{
    echo a > ${output}
}}
b.txt: {{
    echo b > ${output}
}}
all: a.txt b.txt
@default: all`
	runReal(t, src, "all")
	if !exists("a.txt") || !exists("b.txt") {
		t.Error("aggregator did not build its inputs")
	}
	if exists("all") {
		t.Error(`"all" is virtual and must not be created on disk`)
	}
}

// §7.6 An opportunistic target (leading `:`) runs after the pipeline, only if
// all its inputs are available — and never forces them to build.
func TestOpportunisticRunsWhenInputsAvailable(t *testing.T) {
	chdirTmp(t)
	// prod.txt is built this run, so the opportunistic job's input becomes
	// available and it runs.
	src := `prod.txt: {{
    echo p > ${output}
}}
: prod.txt {{
    echo cleaned > marker.txt
}}
@default: prod.txt`
	runReal(t, src, "prod.txt")
	if !exists("marker.txt") {
		t.Error("opportunistic job did not run though its input was available")
	}
}

// §7.6 An opportunistic target is silently skipped when an input is missing and
// nothing will produce it (it never forces the input to build).
func TestOpportunisticSkippedWhenInputMissing(t *testing.T) {
	chdirTmp(t)
	src := `done.txt: {{
    echo d > ${output}
}}
: never.txt {{
    echo ran > marker.txt
}}
@default: done.txt`
	runReal(t, src, "done.txt")
	if exists("marker.txt") {
		t.Error("opportunistic job ran despite a missing, unbuildable input")
	}
}

// §7.6 The canonical guarded temp-cleanup idiom: an opportunistic job removes
// the per-chunk temps once the final output has been built.
func TestOpportunisticTempCleanup(t *testing.T) {
	chdirTmp(t)
	src := `^part.1.txt: {{
    echo one > ${output}
}}
^part.2.txt: {{
    echo two > ${output}
}}
merged.txt: part.1.txt part.2.txt {{
    cat ${input} > ${output}
}}
: merged.txt part.1.txt part.2.txt {{
    rm -f part.1.txt part.2.txt
}}
@default: merged.txt`
	runReal(t, src, "merged.txt")
	if !exists("merged.txt") {
		t.Fatal("merge did not run")
	}
	if exists("part.1.txt") || exists("part.2.txt") {
		t.Error("opportunistic cleanup did not remove the temp parts")
	}
}

// §7.5 Temp-output (^) staleness matrix for the chain A → ^B → C.
//
// A temp's *absence* is transparent: staleness looks through a missing temp to
// its own inputs. A *present* temp is a normal mtime-checked file.
func TestTempStalenessMatrix(t *testing.T) {
	const chain = `^B.txt: A.txt {{
    cat ${input} > ${output}
}}
C.txt: B.txt {{
    cat ${input} > ${output}
}}
@default: C.txt`

	// Row 1: A,C present (B deleted); A newer than C ⇒ look through missing B to
	// A ⇒ C stale ⇒ rebuild B then C.
	t.Run("missing_temp_source_newer_rebuilds", func(t *testing.T) {
		chdirTmp(t)
		writeFile(t, "A.txt", "A1")
		writeFile(t, "C.txt", "OLD")
		touch(t, "C.txt", -10*time.Second)
		touch(t, "A.txt", -1*time.Second) // A newer than C
		runReal(t, chain)
		if !exists("B.txt") {
			t.Error("missing temp B was not rebuilt when the source was newer")
		}
		if got := readFile(t, "C.txt"); got != "A1" {
			t.Errorf("C.txt = %q, want it rebuilt from A through B", got)
		}
	})

	// Row 3: A,C present (B deleted); A older than C ⇒ look through missing B to
	// A ⇒ C current ⇒ skip everything (B is NOT created).
	t.Run("missing_temp_source_older_skips", func(t *testing.T) {
		chdirTmp(t)
		writeFile(t, "A.txt", "A1")
		touch(t, "A.txt", -10*time.Second)
		writeFile(t, "C.txt", "SENTINEL")
		touch(t, "C.txt", -1*time.Second) // C newer than A
		runReal(t, chain)
		if exists("B.txt") {
			t.Error("missing temp B was rebuilt though nothing downstream was stale")
		}
		if got := readFile(t, "C.txt"); got != "SENTINEL" {
			t.Errorf("C.txt = %q, want it left untouched", got)
		}
	})

	// Row 2: A,B,C present; B newer than C ⇒ present temp is mtime-checked ⇒ C
	// rebuilds from B.
	t.Run("present_temp_newer_rebuilds_downstream", func(t *testing.T) {
		chdirTmp(t)
		writeFile(t, "A.txt", "A1")
		touch(t, "A.txt", -30*time.Second)
		writeFile(t, "B.txt", "B1")
		touch(t, "B.txt", -1*time.Second)
		writeFile(t, "C.txt", "C1")
		touch(t, "C.txt", -10*time.Second) // B newer than C
		runReal(t, chain)
		if got := readFile(t, "C.txt"); got != "B1" {
			t.Errorf("C.txt = %q, want it rebuilt from the present, newer B", got)
		}
	})
}

// §7.5 A ^ temp file is never auto-deleted by cgpipe.
func TestTempNotAutoDeleted(t *testing.T) {
	chdirTmp(t)
	writeFile(t, "A.txt", "A1")
	src := `^B.txt: A.txt {{
    cat ${input} > ${output}
}}
C.txt: B.txt {{
    cat ${input} > ${output}
}}
@default: C.txt`
	runReal(t, src)
	if !exists("B.txt") {
		t.Error("temp B.txt should remain after the run (cgpipe never auto-deletes temps)")
	}
}
