package spectest

import (
	"testing"

	"github.com/compgen-io/cgp/internal/eval"
)

// §13.2 export exposes a value to a calling workflow but is a no-op when the
// pipeline runs standalone: the value is collected, and the pipeline still
// builds normally.
func TestExportCollectedAndNoOpStandalone(t *testing.T) {
	chdirTmp(t)
	src := `out.txt: {{
    echo x > ${output}
}}
export bam = "out.txt"
@default: out.txt`
	prog, _ := build(t, src, nil)
	if prog.Exports["bam"] != eval.StrVal("out.txt") {
		t.Errorf("export not collected: %v", prog.Exports)
	}
	// running standalone is unaffected by the export.
	runReal(t, src, "out.txt")
	if !exists("out.txt") {
		t.Error("export changed standalone behavior (out.txt not built)")
	}
}

// §13.1 stage declarations are collected with their args kept raw (resolved at
// orchestration time, since they reference not-yet-produced exports).
func TestStageDeclarationsCollected(t *testing.T) {
	prog, _ := build(t, "stage align align.cgp --ref ${ref}\nstage call call.cgp --bam ${align.bam}",
		map[string]eval.Value{"ref": eval.StrVal("r.fa")})
	if len(prog.Stages) != 2 {
		t.Fatalf("stages = %d, want 2", len(prog.Stages))
	}
	if prog.Stages[0].Name != "align" || prog.Stages[0].File != "align.cgp" {
		t.Errorf("stage 0 = %+v", prog.Stages[0])
	}
	// args stay raw templates (not interpolated) until orchestration.
	if prog.Stages[1].Args[1] != "${align.bam}" {
		t.Errorf("stage args should be raw: %v", prog.Stages[1].Args)
	}
}
