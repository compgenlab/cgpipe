package eval

import (
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/compgenlab/cgpipe/internal/ast"
	"github.com/compgenlab/cgpipe/internal/debug"
	"github.com/compgenlab/cgpipe/internal/parser"
	"github.com/compgenlab/cgpipe/internal/token"
)

// Target is a resolved build rule: concrete output/input filenames, the raw
// body, and a snapshot of the scope at definition time (for rendering).
type Target struct {
	Pos     token.Pos
	Special string // "" normal, else pre/post/setup/teardown/postsubmit
	Outputs []string
	Temp    map[string]bool // output path -> is it a ^temporary output
	Inputs  []string
	Body    string
	HasBody bool
	Stem    string // wildcard stem, when instantiated from a % rule
	Scope   *Scope
}

// Program is the result of evaluating a file's global statements.
type Program struct {
	Targets     []*Target
	Default     []string // goals from @default
	FirstOutput string   // fallback default goal
	Pre         *Target
	Post        *Target
	Setup       *Target
	Teardown    *Target
	Postsubmit  *Target
	Snippets    map[string]string // name -> raw body (for @name invocation)
	Help        string            // leading comment block
	Stages      []StageDecl       // workflow stage declarations (raw templates)
	Exports     map[string]Value  // values exposed via `export` (for a calling workflow)
	Scope       *Scope
	DryRun      bool // propagated to render-time interps so body-directive writes no-op under -dr
}

// StageDecl is a workflow stage: run File with Args, exposing its exports as
// ${Name.export}. Fields are raw templates, resolved at orchestration time.
type StageDecl struct {
	Name string
	File string
	Args []string
}

// Stringify renders a value as it appears in substitution (exported for runners).
func Stringify(v Value) string { return stringify(v) }

// Truthy reports a value's truthiness (exported for runners).
func Truthy(v Value) bool { return truthy(v) }

// Get reads a variable from the program's final scope (e.g. a cgpipe.* setting).
func (p *Program) Get(name string) (Value, bool) {
	if p.Scope == nil {
		return nil, false
	}
	return p.Scope.get(name)
}

// JobSpec describes a one-off command to submit (used by `cgpipe sub`).
type JobSpec struct {
	Command  string           // the shell command line (treated as a cgpipe body)
	Name     string           // job name
	Outputs  []string         // declared outputs (ledger ownership)
	Inputs   []string         // declared inputs
	Settings map[string]Value // job.* and cgpipe.* values (e.g. job.mem, cgpipe.ledger)
}

// NewJob builds a single-target Program from a one-off command. The command is
// the target body, so ${input}/${output} substitution works; job.* settings are
// available to scheduler templates.
func NewJob(spec JobSpec) *Program {
	sc := newScope()
	seedJobDefaults(sc)
	for k, v := range spec.Settings {
		sc.set(k, v)
	}
	if spec.Name != "" {
		sc.set("job.name", StrVal(spec.Name))
	}
	t := &Target{
		Outputs: spec.Outputs,
		Inputs:  spec.Inputs,
		Temp:    map[string]bool{},
		Body:    spec.Command,
		HasBody: true,
		Scope:   sc.clone(),
	}
	p := &Program{Targets: []*Target{t}, Snippets: map[string]string{}, Scope: sc}
	if len(t.Outputs) > 0 {
		p.FirstOutput = t.Outputs[0]
	}
	return p
}

// ExitError is returned by Run when the script calls exit.
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return fmt.Sprintf("exit %d", e.Code) }

// ConfigFile is a parsed config script (itself cgpipe), evaluated before the main
// file. Dir is its directory, for resolving its `include`s.
type ConfigFile struct {
	Dir  string
	File *ast.File
}

// Options configures a run. Evaluation order is: Configs (in slice order), then
// Vars (command-line), then the main file — matching the documented resolution
// order (config < CLI < script).
type Options struct {
	File    string
	Configs []ConfigFile
	Vars    map[string]Value
	Out     io.Writer // destination for print (defaults to os.Stdout)
	ErrOut  io.Writer // destination for warnings, e.g. dry-run write skips (defaults to os.Stderr)
	DryRun  bool      // when true, open-for-write/write/close are no-ops (with a warning)
}

type interp struct {
	file         string
	dir          string // directory of the current file (for include resolution)
	sc           *Scope
	out          io.Writer
	errOut       io.Writer
	prog         *Program
	including    map[string]bool // include cycle guard (absolute paths)
	dryRun       bool            // open-for-write/write/close are no-ops when true
	openWrites   []*writeHandle  // write handles to flush/close at end of eval
	warnedWrites map[string]bool // dry-run write warnings already emitted (per path)
}

// seedJobDefaults pre-populates the global scope with the language-level job
// defaults, before any config layer or the pipeline runs. They are ordinary
// globals: every config layer, target snapshot, and runner inherits them, and any
// later assignment (global or directive) overrides them. Only static, universal
// defaults belong here — `job.shell` is derived from the cgpipe.shell config and so is
// defaulted in the runner, and `job.name` defaults to a target's output and so is
// defaulted per-target in renderTargetScope.
func seedJobDefaults(sc *Scope) {
	sc.set("job.procs", IntVal(1))
	sc.set("job.custom", ListVal{})
	sc.set("job.setup", ListVal{})
}

// Run evaluates the file's global statements and returns the resulting Program.
// A call to exit surfaces as *ExitError.
func Run(file *ast.File, opts Options) (*Program, error) {
	ip := &interp{
		file:         opts.File,
		dir:          filepath.Dir(opts.File),
		sc:           newScope(),
		out:          opts.Out,
		errOut:       opts.ErrOut,
		prog:         &Program{Snippets: map[string]string{}, Exports: map[string]Value{}, Help: file.Help, DryRun: opts.DryRun},
		including:    map[string]bool{},
		dryRun:       opts.DryRun,
		warnedWrites: map[string]bool{},
	}
	if ip.out == nil {
		ip.out = os.Stdout
	}
	if ip.errOut == nil {
		ip.errOut = os.Stderr
	}
	// Flush and close any file handles left open by the script (a forgotten
	// close()), on every return path.
	defer ip.closeWrites()
	seedJobDefaults(ip.sc)
	// 1. config files, in order (system, user, env)
	for _, cfg := range opts.Configs {
		ip.dir = cfg.Dir
		if err := ip.execStmts(cfg.File.Stmts); err != nil {
			return ip.prog, err
		}
	}
	ip.dir = filepath.Dir(opts.File)
	// 2. command-line variables
	for k, v := range opts.Vars {
		ip.sc.set(k, v)
	}
	// 3. the pipeline script
	debug.Logf(1, "eval: %s (%d config layer(s), %d cli var(s))", opts.File, len(opts.Configs), len(opts.Vars))
	if err := ip.execStmts(file.Stmts); err != nil {
		return ip.prog, err
	}
	ip.prog.Scope = ip.sc
	debug.Logf(1, "eval done: %d target(s), %d stage(s)", len(ip.prog.Targets), len(ip.prog.Stages))
	return ip.prog, nil
}

// ExportNames returns the set of names that a file could export — every `export`
// declaration, including those inside if/for branches. Used for best-effort
// static validation of ${stage.x} references in a workflow.
func ExportNames(file *ast.File) []string {
	var names []string
	seen := map[string]bool{}
	var walk func([]ast.Stmt)
	walk = func(stmts []ast.Stmt) {
		for _, s := range stmts {
			switch n := s.(type) {
			case *ast.Export:
				if !seen[n.Name] {
					seen[n.Name] = true
					names = append(names, n.Name)
				}
			case *ast.If:
				for _, b := range n.Blocks {
					walk(b)
				}
				walk(n.Else)
			case *ast.For:
				walk(n.Body)
			}
		}
	}
	walk(file.Stmts)
	return names
}

// Vars returns a copy of the program's final variable scope.
func (p *Program) Vars() map[string]Value {
	m := map[string]Value{}
	if p.Scope != nil {
		for k, v := range p.Scope.vars {
			m[k] = v
		}
	}
	return m
}

func (ip *interp) execStmts(stmts []ast.Stmt) error {
	for _, s := range stmts {
		if err := ip.execStmt(s); err != nil {
			return err
		}
	}
	return nil
}

func (ip *interp) execStmt(s ast.Stmt) error {
	switch n := s.(type) {
	case *ast.Assign:
		if n.Target != nil {
			return ip.execAssignIndex(n)
		}
		return ip.execAssign(n)
	case *ast.ExprStmt:
		_, err := ip.eval(n.X)
		return err
	case *ast.Print:
		return ip.execPrint(n)
	case *ast.Exit:
		code := 0
		if n.Code != nil {
			v, err := ip.eval(n.Code)
			if err != nil {
				return err
			}
			code = int(toInt(v))
		}
		return &ExitError{Code: code}
	case *ast.Unset:
		ip.sc.del(n.Name)
		return nil
	case *ast.If:
		return ip.execIf(n)
	case *ast.For:
		return ip.execFor(n)
	case *ast.Target:
		return ip.execTarget(n)
	case *ast.Include:
		return ip.execInclude(n)
	case *ast.Snippet:
		ip.prog.Snippets[n.Name] = n.Body
		return nil
	case *ast.EvalStmt:
		v, err := ip.eval(n.Code)
		if err != nil {
			return err
		}
		f, err := parser.Parse(stringify(v), "<eval>")
		if err != nil {
			return err
		}
		return ip.execStmts(f.Stmts)
	case *ast.Dumpvars:
		keys := make([]string, 0, len(ip.sc.vars))
		for k := range ip.sc.vars {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(ip.out, "%s = %s\n", k, stringify(ip.sc.vars[k]))
		}
		return nil
	case *ast.Showhelp:
		fmt.Fprintln(ip.out, ip.prog.Help)
		return nil
	case *ast.Sleep:
		v, err := ip.eval(n.Secs)
		if err != nil {
			return err
		}
		time.Sleep(time.Duration(toFloat(v) * float64(time.Second)))
		return nil
	case *ast.Export:
		v, err := ip.eval(n.Value)
		if err != nil {
			return err
		}
		ip.prog.Exports[n.Name] = v
		return nil
	case *ast.Var:
		return ip.execVar(n)
	case *ast.Stage:
		ip.prog.Stages = append(ip.prog.Stages, StageDecl{Name: n.Name, File: n.File, Args: n.Args})
		return nil
	}
	return fmt.Errorf("%s: unsupported statement %T", s.Pos(), s)
}

func (ip *interp) execInclude(n *ast.Include) error {
	v, err := ip.eval(n.Path)
	if err != nil {
		return err
	}
	path := stringify(v)
	resolved := path
	if !filepath.IsAbs(path) {
		if cand := filepath.Join(ip.dir, path); fileExists(cand) {
			resolved = cand
		}
	}
	abs, _ := filepath.Abs(resolved)
	if ip.including[abs] {
		return fmt.Errorf("%s: include cycle: %s", n.Pos(), path)
	}
	src, err := os.ReadFile(resolved)
	if err != nil {
		return fmt.Errorf("%s: include %q: %w", n.Pos(), path, err)
	}
	f, err := parser.Parse(string(src), resolved)
	if err != nil {
		return err
	}
	ip.including[abs] = true
	defer delete(ip.including, abs)
	oldDir := ip.dir
	ip.dir = filepath.Dir(resolved)
	defer func() { ip.dir = oldDir }()
	return ip.execStmts(f.Stmts)
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// execVar evaluates a `var` declaration. It always binds in the CURRENT frame
// (never writing through to an enclosing one), so it can shadow an outer name and
// owns any write handle bound to it for scope-exit close.
func (ip *interp) execVar(n *ast.Var) error {
	var v Value = UnsetVal{}
	if n.HasInit {
		var err error
		if v, err = ip.eval(n.Value); err != nil {
			return err
		}
	}
	ip.sc.set(n.Name, v)
	ip.adoptHandle(ip.sc, v)
	return nil
}

func (ip *interp) execAssign(n *ast.Assign) error {
	v, err := ip.eval(n.Value)
	if err != nil {
		return err
	}
	switch n.Op {
	case token.ASSIGN:
		ip.adoptHandle(ip.sc.assign(n.Name, v), v)
	case token.QASSIGN:
		if !ip.sc.has(n.Name) {
			ip.adoptHandle(ip.sc.assign(n.Name, v), v)
		}
	case token.PLUSASSIGN:
		cur, ok := ip.sc.get(n.Name)
		switch {
		case !ok:
			ip.sc.assign(n.Name, ListVal{v})
		default:
			if lst, isList := cur.(ListVal); isList {
				ip.sc.assign(n.Name, append(append(ListVal{}, lst...), v))
			} else {
				ip.sc.assign(n.Name, ListVal{cur, v})
			}
		}
	}
	return nil
}

// execAssignIndex handles an index-target assignment `m[key] OP value`. The map
// variable is auto-vivified (created empty) if unset, so the grouping idiom
// `groups[cat] += out` needs no prior `groups = {}`. Keys are strings only.
func (ip *interp) execAssignIndex(n *ast.Assign) error {
	idxExpr := n.Target.(*ast.Index)
	recvIdent, ok := idxExpr.Recv.(*ast.Ident)
	if !ok {
		return fmt.Errorf("%s: index assignment target must be a named variable", n.Pos())
	}
	keyVal, err := ip.eval(idxExpr.Idx)
	if err != nil {
		return err
	}
	ks, ok := keyVal.(StrVal)
	if !ok {
		return fmt.Errorf("%s: map assignment key must be a string, got %s", n.Pos(), keyVal.typeName())
	}
	key := string(ks)

	base, exists := ip.sc.get(recvIdent.Name)
	var mv MapVal
	switch {
	case !exists:
		mv = newMap()
	default:
		if m, isMap := base.(MapVal); isMap {
			mv = m
		} else if _, unset := base.(UnsetVal); unset {
			mv = newMap()
		} else {
			return fmt.Errorf("%s: cannot index-assign into %s", n.Pos(), base.typeName())
		}
	}

	v, err := ip.eval(n.Value)
	if err != nil {
		return err
	}
	switch n.Op {
	case token.ASSIGN:
		mv.set(key, v)
	case token.QASSIGN:
		if _, ok := mv.m[key]; !ok {
			mv.set(key, v)
		}
	case token.PLUSASSIGN:
		cur, ok := mv.m[key]
		switch {
		case !ok:
			mv.set(key, ListVal{v})
		default:
			if lst, isList := cur.(ListVal); isList {
				mv.set(key, append(append(ListVal{}, lst...), v))
			} else {
				mv.set(key, ListVal{cur, v})
			}
		}
	}
	// Bind the (possibly freshly-vivified) map following the bare-assignment rule:
	// an existing map is rebound where it already lives (a no-op for the shared
	// reference); a new one lands in the current frame (or root for job.*/cgpipe.*).
	ip.sc.assign(recvIdent.Name, mv)
	return nil
}

func (ip *interp) execPrint(n *ast.Print) error {
	parts := make([]string, len(n.Args))
	for i, a := range n.Args {
		v, err := ip.eval(a)
		if err != nil {
			return err
		}
		parts[i] = stringify(v)
	}
	fmt.Fprintln(ip.out, strings.Join(parts, " "))
	return nil
}

func (ip *interp) execIf(n *ast.If) error {
	for i, cond := range n.Conds {
		v, err := ip.eval(cond)
		if err != nil {
			return err
		}
		if truthy(v) {
			pop := ip.pushScope()
			defer pop()
			return ip.execStmts(n.Blocks[i])
		}
	}
	if n.Else != nil {
		pop := ip.pushScope()
		defer pop()
		return ip.execStmts(n.Else)
	}
	return nil
}

func (ip *interp) execFor(n *ast.For) error {
	if n.Iter != nil {
		v, err := ip.eval(n.Iter)
		if err != nil {
			return err
		}
		items, ok := asList(v)
		if !ok {
			return fmt.Errorf("%s: for…in requires a list or range, got %s", n.Pos(), v.typeName())
		}
		for i, e := range items {
			// Each iteration is its own scope: the loop variable/counter and any
			// new names assigned in the body are block-local (they do not persist
			// after the loop), and a write handle opened this pass closes here.
			pop := ip.pushScope()
			ip.sc.set(n.Var, e)
			if n.IndexVar != "" { // `with i`: 1-based loop counter
				ip.sc.set(n.IndexVar, IntVal(i+1))
			}
			err := ip.execStmts(n.Body)
			pop()
			if err != nil {
				return err
			}
		}
		return nil
	}
	// while form
	for i := 0; ; i++ {
		if i >= maxLoopIterations {
			return fmt.Errorf("%s: for-loop exceeded %d iterations", n.Pos(), maxLoopIterations)
		}
		v, err := ip.eval(n.Cond) // condition reads the enclosing scope
		if err != nil {
			return err
		}
		if !truthy(v) {
			return nil
		}
		pop := ip.pushScope()
		err = ip.execStmts(n.Body)
		pop()
		if err != nil {
			return err
		}
	}
}

func (ip *interp) execTarget(n *ast.Target) error {
	outs, err := ip.expandWords(n.Outputs)
	if err != nil {
		return err
	}
	ins, err := ip.expandWords(n.Inputs)
	if err != nil {
		return err
	}
	t := &Target{
		Pos:     n.Pos(),
		Special: n.Special,
		Inputs:  ins,
		Scope:   ip.sc.clone(),
		Temp:    map[string]bool{},
	}
	for _, o := range outs {
		if strings.HasPrefix(o, "^") {
			o = o[1:]
			t.Temp[o] = true
		}
		t.Outputs = append(t.Outputs, o)
	}
	if n.Body != nil {
		t.Body = n.Body.Raw
		t.HasBody = true
	}

	switch n.Special {
	case "default":
		ip.prog.Default = append(ip.prog.Default, ins...)
		return nil
	case "pre":
		ip.prog.Pre = t
		return nil
	case "post":
		ip.prog.Post = t
		return nil
	case "setup":
		ip.prog.Setup = t
		return nil
	case "teardown":
		ip.prog.Teardown = t
		return nil
	case "postsubmit":
		ip.prog.Postsubmit = t
		return nil
	}

	ip.prog.Targets = append(ip.prog.Targets, t)
	debug.Logf(4, "target: %v ← %v", t.Outputs, t.Inputs)
	if ip.prog.FirstOutput == "" && len(t.Outputs) > 0 {
		ip.prog.FirstOutput = t.Outputs[0]
	}
	return nil
}

// expandWords expands each raw word template (may produce multiple via @{…}).
func (ip *interp) expandWords(words []string) ([]string, error) {
	var out []string
	for _, w := range words {
		exp, err := ip.expandTemplate(w, modeString)
		if err != nil {
			return nil, err
		}
		out = append(out, exp...)
	}
	return out, nil
}

// ---- expression evaluation ----

func (ip *interp) eval(e ast.Expr) (Value, error) {
	switch n := e.(type) {
	case *ast.IntLit:
		return IntVal(n.Val), nil
	case *ast.FloatLit:
		return FloatVal(n.Val), nil
	case *ast.BoolLit:
		return BoolVal(n.Val), nil
	case *ast.StringLit:
		s, err := ip.interpolate(n.Raw, modeString)
		if err != nil {
			return nil, err
		}
		return StrVal(s), nil
	case *ast.Ident:
		if v, ok := ip.sc.get(n.Name); ok {
			return v, nil
		}
		return UnsetVal{}, nil
	case *ast.ListLit:
		out := make(ListVal, len(n.Elems))
		for i, el := range n.Elems {
			v, err := ip.eval(el)
			if err != nil {
				return nil, err
			}
			out[i] = v
		}
		return out, nil
	case *ast.RangeLit:
		lo, err := ip.eval(n.Lo)
		if err != nil {
			return nil, err
		}
		hi, err := ip.eval(n.Hi)
		if err != nil {
			return nil, err
		}
		return RangeVal{Lo: toInt(lo), Hi: toInt(hi)}, nil
	case *ast.Unary:
		return ip.evalUnary(n)
	case *ast.Binary:
		return ip.evalBinary(n)
	case *ast.Index:
		return ip.evalIndex(n)
	case *ast.Slice:
		return ip.evalSlice(n)
	case *ast.MapLit:
		mv := newMap()
		for _, ent := range n.Entries {
			kv, err := ip.eval(ent.Key)
			if err != nil {
				return nil, err
			}
			ks, ok := kv.(StrVal)
			if !ok {
				return nil, fmt.Errorf("%s: map key must be a string, got %s", n.Pos(), kv.typeName())
			}
			vv, err := ip.eval(ent.Value)
			if err != nil {
				return nil, err
			}
			mv.set(string(ks), vv)
		}
		return mv, nil
	case *ast.Call:
		args := make([]Value, len(n.Args))
		for i, a := range n.Args {
			v, err := ip.eval(a)
			if err != nil {
				return nil, err
			}
			args[i] = v
		}
		var kwargs map[string]Value
		if len(n.Kwargs) > 0 {
			kwargs = make(map[string]Value, len(n.Kwargs))
			for _, kw := range n.Kwargs {
				v, err := ip.eval(kw.Value)
				if err != nil {
					return nil, err
				}
				kwargs[kw.Name] = v
			}
		}
		if n.Recv == nil { // builtin free call, e.g. open("f")
			r, err := ip.callBuiltin(n.Method, args, kwargs)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", n.Pos(), err)
			}
			return r, nil
		}
		recv, err := ip.eval(n.Recv)
		if err != nil {
			return nil, err
		}
		r, err := callMethod(recv, n.Method, args, kwargs)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", n.Pos(), err)
		}
		return r, nil
	}
	return nil, fmt.Errorf("%s: cannot evaluate %T", e.Pos(), e)
}

// callBuiltin dispatches a free-function call (one whose AST Call has no receiver).
// open(path[, mode]) is the only builtin today; "r" returns a lazy read handle,
// "w"/"a" open the file for writing (a no-op under dry-run).
func (ip *interp) callBuiltin(name string, args []Value, kwargs map[string]Value) (Value, error) {
	switch name {
	case "open":
		if err := validateKwargs("open", kwargs); err != nil {
			return nil, err
		}
		if len(args) < 1 || len(args) > 2 {
			return nil, fmt.Errorf("open() takes a path and an optional mode")
		}
		path := stringify(args[0])
		mode := "r"
		if len(args) == 2 {
			mode = stringify(args[1])
		}
		switch mode {
		case "r":
			return FileVal{path: path, mode: "r"}, nil
		case "w", "a":
			return ip.openWrite(path, mode)
		default:
			return nil, fmt.Errorf("open(): unknown mode %q (use \"r\", \"w\", or \"a\")", mode)
		}
	}
	return nil, fmt.Errorf("unknown function %s()", name)
}

// openWrite opens path for writing ("w" truncates, "a" appends). Under dry-run it
// opens nothing and returns a no-op handle, warning once per path.
func (ip *interp) openWrite(path, mode string) (Value, error) {
	if ip.dryRun {
		if !ip.warnedWrites[path] {
			ip.warnedWrites[path] = true
			fmt.Fprintf(ip.errOut, "cgpipe: dry-run: not writing to file %q\n", path)
		}
		h := &writeHandle{path: path, dryRun: true}
		ip.openWrites = append(ip.openWrites, h)
		return FileVal{path: path, mode: mode, w: h}, nil
	}
	flag := os.O_CREATE | os.O_WRONLY
	if mode == "a" {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_TRUNC
	}
	fh, err := os.OpenFile(path, flag, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open(%q, %q): %w", path, mode, err)
	}
	h := &writeHandle{path: path, f: fh}
	ip.openWrites = append(ip.openWrites, h)
	return FileVal{path: path, mode: mode, w: h}, nil
}

// closeWrites flushes and closes any write handles the script left open. It is the
// end-of-eval backstop; handles owned by a scope frame are already closed when that
// frame exits (writeHandle.close is idempotent).
func (ip *interp) closeWrites() {
	for _, h := range ip.openWrites {
		_ = h.close()
	}
}

// adoptHandle ties a write handle to the frame its variable binds in, so the handle
// is flushed/closed when that frame exits. A handle is adopted by exactly one frame
// (the first binding); aliasing it (`g = f`) does not re-adopt it.
func (ip *interp) adoptHandle(frame *Scope, v Value) {
	if fv, ok := v.(FileVal); ok && fv.w != nil && !fv.w.owned {
		fv.w.owned = true
		frame.handles = append(frame.handles, fv.w)
	}
}

// pushScope enters a child frame and returns a function that, when called, flushes
// and closes the frame's owned write handles and restores the parent frame.
func (ip *interp) pushScope() func() {
	parent := ip.sc
	ip.sc = parent.child()
	child := ip.sc
	return func() {
		for _, h := range child.handles {
			_ = h.close()
		}
		ip.sc = parent
	}
}

func (ip *interp) evalUnary(n *ast.Unary) (Value, error) {
	v, err := ip.eval(n.X)
	if err != nil {
		return nil, err
	}
	switch n.Op {
	case token.NOT:
		return BoolVal(!truthy(v)), nil
	case token.MINUS:
		switch x := v.(type) {
		case IntVal:
			return -x, nil
		case FloatVal:
			return -x, nil
		}
		return nil, fmt.Errorf("%s: cannot negate %s", n.Pos(), v.typeName())
	}
	return nil, fmt.Errorf("%s: bad unary op", n.Pos())
}

func (ip *interp) evalBinary(n *ast.Binary) (Value, error) {
	// short-circuit logic
	if n.Op == token.AND || n.Op == token.OR {
		l, err := ip.eval(n.L)
		if err != nil {
			return nil, err
		}
		if n.Op == token.AND && !truthy(l) {
			return BoolVal(false), nil
		}
		if n.Op == token.OR && truthy(l) {
			return BoolVal(true), nil
		}
		r, err := ip.eval(n.R)
		if err != nil {
			return nil, err
		}
		return BoolVal(truthy(r)), nil
	}
	l, err := ip.eval(n.L)
	if err != nil {
		return nil, err
	}
	r, err := ip.eval(n.R)
	if err != nil {
		return nil, err
	}
	v, err := applyBinary(n.Op, l, r)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", n.Pos(), err)
	}
	return v, nil
}

func (ip *interp) evalIndex(n *ast.Index) (Value, error) {
	recv, err := ip.eval(n.Recv)
	if err != nil {
		return nil, err
	}
	idx, err := ip.eval(n.Idx)
	if err != nil {
		return nil, err
	}
	// A map is indexed by string key (lookup; miss → unset) or int (positional
	// lookup into the ordered keys). Keys are always strings, so the two never
	// collide.
	if mv, isMap := recv.(MapVal); isMap {
		switch k := idx.(type) {
		case StrVal:
			if v, ok := mv.m[string(k)]; ok {
				return v, nil
			}
			return UnsetVal{}, nil
		case IntVal:
			i := int(k)
			if i < 0 {
				i += len(mv.keys)
			}
			if i < 0 || i >= len(mv.keys) {
				return nil, fmt.Errorf("%s: index %d out of range (len %d)", n.Pos(), int(k), len(mv.keys))
			}
			return mv.m[mv.keys[i]], nil
		default:
			return nil, fmt.Errorf("%s: map index must be a string key or int position, got %s", n.Pos(), idx.typeName())
		}
	}
	// A range yields its i-th value arithmetically — no need to materialize the
	// whole sequence to read one element.
	if r, isRange := recv.(RangeVal); isRange {
		cnt := int(r.count())
		i := int(toInt(idx))
		if i < 0 {
			i += cnt
		}
		if i < 0 || i >= cnt {
			return nil, fmt.Errorf("%s: index %d out of range (len %d)", n.Pos(), int(toInt(idx)), cnt)
		}
		step := int64(1)
		if r.Lo > r.Hi {
			step = -1
		}
		return IntVal(r.Lo + step*int64(i)), nil
	}
	items, ok := asList(recv)
	if !ok {
		if s, isStr := recv.(StrVal); isStr {
			items = make([]Value, 0, len(s))
			for _, c := range string(s) {
				items = append(items, StrVal(string(c)))
			}
		} else {
			return nil, fmt.Errorf("%s: cannot index %s", n.Pos(), recv.typeName())
		}
	}
	i := int(toInt(idx))
	if i < 0 {
		i += len(items)
	}
	if i < 0 || i >= len(items) {
		return nil, fmt.Errorf("%s: index %d out of range (len %d)", n.Pos(), int(toInt(idx)), len(items))
	}
	return items[i], nil
}

func (ip *interp) evalSlice(n *ast.Slice) (Value, error) {
	recv, err := ip.eval(n.Recv)
	if err != nil {
		return nil, err
	}
	items, ok := asList(recv)
	if !ok {
		return nil, fmt.Errorf("%s: cannot slice %s", n.Pos(), recv.typeName())
	}
	lo, hi := 0, len(items)
	if n.Lo != nil {
		v, err := ip.eval(n.Lo)
		if err != nil {
			return nil, err
		}
		lo = clampIndex(int(toInt(v)), len(items))
	}
	if n.Hi != nil {
		v, err := ip.eval(n.Hi)
		if err != nil {
			return nil, err
		}
		hi = clampIndex(int(toInt(v)), len(items))
	}
	if lo > hi {
		lo = hi
	}
	return ListVal(append(ListVal{}, items[lo:hi]...)), nil
}

func clampIndex(i, n int) int {
	if i < 0 {
		i += n
	}
	if i < 0 {
		return 0
	}
	if i > n {
		return n
	}
	return i
}

// ---- operators ----

func applyBinary(op token.Kind, l, r Value) (Value, error) {
	if _, ok := l.(UnsetVal); ok {
		if op != token.EQ && op != token.NEQ {
			return nil, fmt.Errorf("use of unset value in expression")
		}
	}
	if _, ok := r.(UnsetVal); ok {
		if op != token.EQ && op != token.NEQ {
			return nil, fmt.Errorf("use of unset value in expression")
		}
	}

	switch op {
	case token.EQ:
		return BoolVal(valueEqual(l, r)), nil
	case token.NEQ:
		return BoolVal(!valueEqual(l, r)), nil
	}

	// string concatenation / repetition
	if ls, ok := l.(StrVal); ok {
		switch op {
		case token.PLUS:
			return StrVal(string(ls) + stringify(r)), nil
		case token.STAR:
			return StrVal(strings.Repeat(string(ls), int(toInt(r)))), nil
		}
	}
	// list concatenation / repetition
	if ll, ok := l.(ListVal); ok {
		switch op {
		case token.PLUS:
			if rl, ok := r.(ListVal); ok {
				return ListVal(append(append(ListVal{}, ll...), rl...)), nil
			}
			return ListVal(append(append(ListVal{}, ll...), r)), nil
		case token.STAR:
			n := int(toInt(r))
			out := ListVal{}
			for i := 0; i < n; i++ {
				out = append(out, ll...)
			}
			return out, nil
		}
	}

	// numeric
	if isNumeric(l) && isNumeric(r) {
		if isFloat(l) || isFloat(r) {
			a, b := toFloat(l), toFloat(r)
			switch op {
			case token.PLUS:
				return FloatVal(a + b), nil
			case token.MINUS:
				return FloatVal(a - b), nil
			case token.STAR:
				return FloatVal(a * b), nil
			case token.SLASH:
				return FloatVal(a / b), nil
			case token.POW:
				return FloatVal(math.Pow(a, b)), nil
			case token.LT:
				return BoolVal(a < b), nil
			case token.LE:
				return BoolVal(a <= b), nil
			case token.GT:
				return BoolVal(a > b), nil
			case token.GE:
				return BoolVal(a >= b), nil
			}
		}
		a, b := toInt(l), toInt(r)
		switch op {
		case token.PLUS:
			return IntVal(a + b), nil
		case token.MINUS:
			return IntVal(a - b), nil
		case token.STAR:
			return IntVal(a * b), nil
		case token.SLASH:
			if b == 0 {
				return nil, fmt.Errorf("division by zero")
			}
			return IntVal(a / b), nil
		case token.PERCENT:
			if b == 0 {
				return nil, fmt.Errorf("division by zero")
			}
			return IntVal(a % b), nil
		case token.POW:
			if r, ok := intPow(a, b); ok {
				return IntVal(r), nil
			}
			return IntVal(int64(math.Pow(float64(a), float64(b)))), nil
		case token.LT:
			return BoolVal(a < b), nil
		case token.LE:
			return BoolVal(a <= b), nil
		case token.GT:
			return BoolVal(a > b), nil
		case token.GE:
			return BoolVal(a >= b), nil
		}
	}

	return nil, fmt.Errorf("cannot apply %s to %s and %s", op, l.typeName(), r.typeName())
}

func valueEqual(l, r Value) bool {
	// Two ints compare exactly as int64; mixed int/float (and float/float) compare
	// as float64 so 1 == 1.0. (Comparing large int64s via float64 would lose
	// precision above 2^53, hence the dedicated int path.)
	if li, lok := l.(IntVal); lok {
		if ri, rok := r.(IntVal); rok {
			return li == ri
		}
	}
	if isNumeric(l) && isNumeric(r) {
		return toFloat(l) == toFloat(r)
	}
	return stringify(l) == stringify(r)
}

// intPow computes base**exp exactly for a non-negative exponent (no float
// rounding). ok is false for a negative exponent, where the caller falls back to
// floating-point. Overflow wraps as ordinary int64 arithmetic.
func intPow(base, exp int64) (int64, bool) {
	if exp < 0 {
		return 0, false
	}
	result := int64(1)
	for i := int64(0); i < exp; i++ {
		result *= base
	}
	return result, true
}

func isNumeric(v Value) bool {
	switch v.(type) {
	case IntVal, FloatVal:
		return true
	}
	return false
}

func isFloat(v Value) bool { _, ok := v.(FloatVal); return ok }

func toInt(v Value) int64 {
	switch x := v.(type) {
	case IntVal:
		return int64(x)
	case FloatVal:
		return int64(x)
	case BoolVal:
		if x {
			return 1
		}
		return 0
	}
	return 0
}

func toFloat(v Value) float64 {
	switch x := v.(type) {
	case IntVal:
		return float64(x)
	case FloatVal:
		return float64(x)
	}
	return 0
}
