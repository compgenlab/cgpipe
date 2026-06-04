package spectest

import (
	"io"
	"testing"

	"github.com/compgen-io/cgp/internal/eval"
	"github.com/compgen-io/cgp/internal/parser"
)

// cfg parses src as a config layer.
func cfg(t *testing.T, src string) eval.ConfigFile {
	t.Helper()
	f, err := parser.Parse(src, "config")
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	return eval.ConfigFile{Dir: ".", File: f}
}

// runWith evaluates a script with config layers and CLI vars, returning the
// program.
func runWith(t *testing.T, script string, vars map[string]eval.Value, cfgs ...eval.ConfigFile) *eval.Program {
	t.Helper()
	f, err := parser.Parse(script, "main")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	prog, err := eval.Run(f, eval.Options{Out: io.Discard, Configs: cfgs, Vars: vars})
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	return prog
}

// §11.2 Later config layers win over earlier ones (system < user).
func TestConfigLayerOrder(t *testing.T) {
	prog := runWith(t, ``, nil, cfg(t, `x = 1`), cfg(t, `x = 2`))
	if v, _ := prog.Get("x"); v != eval.IntVal(2) {
		t.Errorf("x = %v, want 2 (later/user config wins)", v)
	}
}

// §11.2 A CLI variable beats config.
func TestCLIBeatsConfig(t *testing.T) {
	prog := runWith(t, ``, map[string]eval.Value{"x": eval.IntVal(5)}, cfg(t, `x = 1`))
	if v, _ := prog.Get("x"); v != eval.IntVal(5) {
		t.Errorf("x = %v, want 5 (CLI beats config)", v)
	}
}

// §11.2 The script's `=` always wins; `?=` respects upstream config.
func TestScriptAssignVsConfig(t *testing.T) {
	prog := runWith(t, `x = 3`, nil, cfg(t, `x = 1`))
	if v, _ := prog.Get("x"); v != eval.IntVal(3) {
		t.Errorf("x = %v, want 3 (script = wins)", v)
	}
	prog = runWith(t, `x ?= 9`, nil, cfg(t, `x = 1`))
	if v, _ := prog.Get("x"); v != eval.IntVal(1) {
		t.Errorf("x = %v, want 1 (config beats script ?=)", v)
	}
}

// §11.1/§11.3 cgp.* settings are read back from the program scope.
func TestCgpSettingsNamespace(t *testing.T) {
	prog := runWith(t, "cgp.runner = \"slurm\"\ncgp.ledger = \"/tmp/x.db\"", nil)
	if v, _ := prog.Get("cgp.runner"); eval.Stringify(v) != "slurm" {
		t.Errorf("cgp.runner = %v", v)
	}
	if v, _ := prog.Get("cgp.ledger"); eval.Stringify(v) != "/tmp/x.db" {
		t.Errorf("cgp.ledger = %v", v)
	}
}
