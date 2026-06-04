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

// §7.2 Multiple definitions for one output: the first whose inputs are all
// satisfiable is used.
//
// GAP: the driver's producer map keeps only the first target defined for an
// output and never falls back to a later, satisfiable rule — it errors with
// "no rule to make <first rule's missing input>" instead. The assertion below
// is the spec-correct behavior; un-skip it when the fallback is implemented.
func TestMultipleDefinitionsFirstSatisfiable(t *testing.T) {
	t.Skip("GAP §7.2: no fallback to the first *satisfiable* multiple-definition rule")
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
//
// GAP: the driver builds only producers (targets with outputs) and the
// setup/goal/teardown phases; a no-output target is dropped when the producer
// map is built, so opportunistic jobs never run. The companion
// TestOpportunisticSkippedWhenInputMissing currently passes only because the
// job never runs at all — when opportunistic jobs are implemented, both should
// pass for the right reasons.
func TestOpportunisticRunsWhenInputsAvailable(t *testing.T) {
	t.Skip("GAP §7.6: opportunistic (no-output) targets are not yet run by the driver")
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
//
// NOTE: this passes today partly vacuously — opportunistic jobs are not yet run
// at all (see TestOpportunisticRunsWhenInputsAvailable) — but it also pins the
// "missing input ⇒ no marker, no error" half of the contract for when they are.
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

// §7.5 A ^ temp file is never auto-deleted by cgp.
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
		t.Error("temp B.txt should remain after the run (cgp never auto-deletes temps)")
	}
}
