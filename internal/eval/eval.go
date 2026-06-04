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

	"github.com/compgen-io/cgp/internal/ast"
	"github.com/compgen-io/cgp/internal/parser"
	"github.com/compgen-io/cgp/internal/token"
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

// Get reads a variable from the program's final scope (e.g. a cgp.* setting).
func (p *Program) Get(name string) (Value, bool) {
	if p.Scope == nil {
		return nil, false
	}
	return p.Scope.get(name)
}

// JobSpec describes a one-off command to submit (used by `cgp sub`).
type JobSpec struct {
	Command  string           // the shell command line (treated as a cgp body)
	Name     string           // job name
	Outputs  []string         // declared outputs (ledger ownership)
	Inputs   []string         // declared inputs
	Settings map[string]Value // job.* and cgp.* values (e.g. job.mem, cgp.ledger)
}

// NewJob builds a single-target Program from a one-off command. The command is
// the target body, so ${input}/${output} substitution works; job.* settings are
// available to scheduler templates.
func NewJob(spec JobSpec) *Program {
	sc := newScope()
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

// ConfigFile is a parsed config script (itself cgp), evaluated before the main
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
}

type interp struct {
	file      string
	dir       string // directory of the current file (for include resolution)
	sc        *Scope
	out       io.Writer
	prog      *Program
	including map[string]bool // include cycle guard (absolute paths)
}

// Run evaluates the file's global statements and returns the resulting Program.
// A call to exit surfaces as *ExitError.
func Run(file *ast.File, opts Options) (*Program, error) {
	ip := &interp{
		file:      opts.File,
		dir:       filepath.Dir(opts.File),
		sc:        newScope(),
		out:       opts.Out,
		prog:      &Program{Snippets: map[string]string{}, Exports: map[string]Value{}, Help: file.Help},
		including: map[string]bool{},
	}
	if ip.out == nil {
		ip.out = os.Stdout
	}
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
	if err := ip.execStmts(file.Stmts); err != nil {
		return ip.prog, err
	}
	ip.prog.Scope = ip.sc
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
		return ip.execAssign(n)
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

func (ip *interp) execAssign(n *ast.Assign) error {
	v, err := ip.eval(n.Value)
	if err != nil {
		return err
	}
	switch n.Op {
	case token.ASSIGN:
		ip.sc.set(n.Name, v)
	case token.QASSIGN:
		if !ip.sc.has(n.Name) {
			ip.sc.set(n.Name, v)
		}
	case token.PLUSASSIGN:
		cur, ok := ip.sc.get(n.Name)
		switch {
		case !ok:
			ip.sc.set(n.Name, ListVal{v})
		default:
			if lst, isList := cur.(ListVal); isList {
				ip.sc.set(n.Name, append(append(ListVal{}, lst...), v))
			} else {
				ip.sc.set(n.Name, ListVal{cur, v})
			}
		}
	}
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
			return ip.execStmts(n.Blocks[i])
		}
	}
	if n.Else != nil {
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
		for _, e := range items {
			ip.sc.set(n.Var, e)
			if err := ip.execStmts(n.Body); err != nil {
				return err
			}
		}
		return nil
	}
	// while form
	const cap = 1_000_000
	for i := 0; ; i++ {
		if i >= cap {
			return fmt.Errorf("%s: for-loop exceeded %d iterations", n.Pos(), cap)
		}
		v, err := ip.eval(n.Cond)
		if err != nil {
			return err
		}
		if !truthy(v) {
			return nil
		}
		if err := ip.execStmts(n.Body); err != nil {
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
	if ip.prog.FirstOutput == "" && len(t.Outputs) > 0 {
		ip.prog.FirstOutput = t.Outputs[0]
	}
	return nil
}

// expandWords expands each raw word template (may produce multiple via @{…}).
func (ip *interp) expandWords(words []string) ([]string, error) {
	var out []string
	for _, w := range words {
		exp, err := ip.expandTemplate(w)
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
		s, err := ip.interpolate(n.Raw)
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
	case *ast.Call:
		recv, err := ip.eval(n.Recv)
		if err != nil {
			return nil, err
		}
		args := make([]Value, len(n.Args))
		for i, a := range n.Args {
			v, err := ip.eval(a)
			if err != nil {
				return nil, err
			}
			args[i] = v
		}
		r, err := callMethod(recv, n.Method, args)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", n.Pos(), err)
		}
		return r, nil
	}
	return nil, fmt.Errorf("%s: cannot evaluate %T", e.Pos(), e)
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
	if isNumeric(l) && isNumeric(r) {
		return toFloat(l) == toFloat(r)
	}
	return stringify(l) == stringify(r)
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
