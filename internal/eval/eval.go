package eval

import (
	"fmt"
	"io"
	"math"
	"os"
	"strings"

	"github.com/compgen-io/cgp/internal/ast"
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
	Scope       *Scope
}

// ExitError is returned by Run when the script calls exit.
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return fmt.Sprintf("exit %d", e.Code) }

// Options configures a run.
type Options struct {
	File string
	Vars map[string]Value // command-line / manifest variables, applied first
	Out  io.Writer        // destination for print (defaults to os.Stdout)
}

type interp struct {
	file string
	sc   *Scope
	out  io.Writer
	prog *Program
}

// Run evaluates the file's global statements and returns the resulting Program.
// A call to exit surfaces as *ExitError.
func Run(file *ast.File, opts Options) (*Program, error) {
	ip := &interp{
		file: opts.File,
		sc:   newScope(),
		out:  opts.Out,
		prog: &Program{},
	}
	if ip.out == nil {
		ip.out = os.Stdout
	}
	for k, v := range opts.Vars {
		ip.sc.set(k, v)
	}
	if err := ip.execStmts(file.Stmts); err != nil {
		return ip.prog, err
	}
	ip.prog.Scope = ip.sc
	return ip.prog, nil
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
	}
	return fmt.Errorf("%s: unsupported statement %T", s.Pos(), s)
}

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
