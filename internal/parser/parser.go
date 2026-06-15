package parser

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/compgen-io/cgp/internal/ast"
	"github.com/compgen-io/cgp/internal/lexer"
	"github.com/compgen-io/cgp/internal/token"
)

// Parse lexes and parses cgp source into an *ast.File. file is used for
// positions in error messages.
func Parse(src, file string) (*ast.File, error) {
	p := &parser{src: src, toks: lexer.Tokenize(src, file)}
	f, err := p.parseFile()
	if f != nil {
		f.Help = extractHelp(src)
	}
	return f, err
}

// extractHelp returns the leading block of comment lines (after an optional
// shebang), with the `#` markers stripped — the script's help text.
func extractHelp(src string) string {
	lines := strings.Split(src, "\n")
	i := 0
	if i < len(lines) && strings.HasPrefix(lines[i], "#!") {
		i++
	}
	var help []string
	for ; i < len(lines); i++ {
		t := strings.TrimLeft(lines[i], " \t")
		if !strings.HasPrefix(t, "#") {
			break
		}
		help = append(help, strings.TrimPrefix(t[1:], " "))
	}
	return strings.Join(help, "\n")
}

// ParseExpr parses a single cgp expression (used for ${…}/@{…} interpolation and
// %-control headers in bodies).
func ParseExpr(src string) (e ast.Expr, err error) {
	p := &parser{src: src, toks: lexer.Tokenize(src, "<expr>")}
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(bailout); ok {
				e, err = nil, p.err
				return
			}
			panic(r)
		}
	}()
	p.skipSeparators()
	e = p.parseExpr(0)
	p.skipSeparators()
	if p.cur().Kind != token.EOF {
		p.fail(p.cur().Pos, "unexpected %s after expression", p.cur())
	}
	return e, nil
}

type parser struct {
	src          string
	toks         []token.Token
	i            int
	err          error
	declEndIndex int // token index of the last declaration terminator (set by scanDeclEnd)
}

// Error is a parse error carrying the source position where parsing failed.
// Its Error() string is "file:line:col: msg" — unchanged from before this type
// was exported, so error output and tests are unaffected. Tools (e.g. the LSP
// server) can type-assert to *Error to recover the structured token.Pos rather
// than parsing the message string.
type Error struct {
	Pos token.Pos
	Msg string
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Pos, e.Msg) }

type bailout struct{}

func (p *parser) fail(pos token.Pos, format string, args ...any) {
	if p.err == nil {
		p.err = &Error{Pos: pos, Msg: fmt.Sprintf(format, args...)}
	}
	panic(bailout{})
}

func (p *parser) cur() token.Token  { return p.toks[p.i] }
func (p *parser) peek() token.Token { return p.toks[min(p.i+1, len(p.toks)-1)] }
func (p *parser) advance() token.Token {
	t := p.toks[p.i]
	if p.i < len(p.toks)-1 {
		p.i++
	}
	return t
}

func (p *parser) accept(k token.Kind) bool {
	if p.cur().Kind == k {
		p.advance()
		return true
	}
	return false
}

func (p *parser) expect(k token.Kind) token.Token {
	if p.cur().Kind != k {
		p.fail(p.cur().Pos, "expected %s, got %s", k, p.cur())
	}
	return p.advance()
}

func (p *parser) skipSeparators() {
	for p.cur().Kind == token.NEWLINE || p.cur().Kind == token.SEMI {
		p.advance()
	}
}

func (p *parser) parseFile() (f *ast.File, err error) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(bailout); ok {
				f, err = nil, p.err
				return
			}
			panic(r)
		}
	}()
	file := &ast.File{}
	p.skipSeparators()
	for p.cur().Kind != token.EOF {
		file.Stmts = append(file.Stmts, p.parseStmt())
		p.skipSeparators()
	}
	return file, nil
}

// ---- statements ----

func (p *parser) parseStmt() ast.Stmt {
	switch p.cur().Kind {
	case token.IF:
		return p.parseIf()
	case token.FOR:
		return p.parseFor()
	case token.AT:
		// `@name` is a reserved target; `@{list}` is a normal target whose
		// outputs lead with an @{…} list expansion ([spec §7.4]).
		if p.peek().Kind == token.LBRACE {
			return p.parseTarget()
		}
		return p.parseReservedTarget()
	case token.CARET, token.COLON:
		return p.parseTarget()
	case token.IDENT:
		// export/var/stage are checked before the assignment test (each can contain '=').
		switch p.cur().Lit {
		case "export":
			return p.parseExport()
		case "var":
			return p.parseVar()
		case "stage":
			return p.parseStage()
		}
		if p.lineIsAssignment() {
			return p.parseAssign()
		}
		switch p.cur().Lit {
		case "print":
			return p.parsePrint()
		case "exit":
			return p.parseExit()
		case "unset":
			return p.parseUnset()
		case "include":
			return p.parseInclude()
		case "snippet":
			return p.parseSnippet()
		case "eval":
			return p.parseEvalStmt()
		case "sleep":
			return p.parseSleep()
		case "dumpvars":
			t := p.advance()
			return &ast.Dumpvars{PosV: t.Pos}
		case "showhelp":
			t := p.advance()
			return &ast.Showhelp{PosV: t.Pos}
		}
		// A call line with no top-level ':' (e.g. `f.write("x")`, `f.close()`) is a
		// call-expression statement, evaluated for its side effect. Anything else
		// (including a bare name or a malformed target) falls through to parseTarget,
		// preserving its error. Only *ast.Call qualifies, so targets are untouched.
		if p.findColon() < 0 {
			save := p.i
			expr := p.parseExpr(0)
			if call, ok := expr.(*ast.Call); ok && p.atStmtEnd() {
				return &ast.ExprStmt{PosV: call.Pos(), X: call}
			}
			p.i = save
		}
		return p.parseTarget()
	default:
		// A line that isn't a recognized statement but has a top-level ':' is a
		// target declaration whose first output begins with ${…}/@{…} (which the
		// lexer surfaces as stray tokens; the words are sliced raw from source).
		if p.findColon() >= 0 {
			return p.parseTarget()
		}
		p.fail(p.cur().Pos, "unexpected %s at start of statement", p.cur())
		return nil
	}
}

func (p *parser) parseBlock() []ast.Stmt {
	p.expect(token.LBRACE)
	var stmts []ast.Stmt
	for {
		p.skipSeparators()
		switch p.cur().Kind {
		case token.RBRACE:
			p.advance()
			return stmts
		case token.EOF:
			p.fail(p.cur().Pos, "unclosed '{'")
		}
		stmts = append(stmts, p.parseStmt())
	}
}

func (p *parser) parseIf() ast.Stmt {
	node := &ast.If{PosV: p.cur().Pos}
	p.expect(token.IF)
	node.Conds = append(node.Conds, p.parseExpr(0))
	node.Blocks = append(node.Blocks, p.parseBlock())
	for {
		save := p.i
		p.skipSeparators()
		switch p.cur().Kind {
		case token.ELIF:
			p.advance()
			node.Conds = append(node.Conds, p.parseExpr(0))
			node.Blocks = append(node.Blocks, p.parseBlock())
		case token.ELSE:
			p.advance()
			node.Else = p.parseBlock()
			return node
		default:
			p.i = save // separators belong to the caller
			return node
		}
	}
}

func (p *parser) parseFor() ast.Stmt {
	node := &ast.For{PosV: p.cur().Pos}
	p.expect(token.FOR)
	if p.cur().Kind == token.IDENT && p.peek().Kind == token.IN {
		node.Var = p.advance().Lit
		p.expect(token.IN)
		node.Iter = p.parseExpr(0)
		if p.accept(token.WITH) { // optional `with i` enumerate counter
			node.IndexVar = p.expect(token.IDENT).Lit
		}
	} else {
		node.Cond = p.parseExpr(0)
	}
	node.Body = p.parseBlock()
	return node
}

func (p *parser) parsePrint() ast.Stmt {
	node := &ast.Print{PosV: p.cur().Pos}
	p.advance() // print
	if !p.atStmtEnd() {
		node.Args = append(node.Args, p.parseExpr(0))
		for p.accept(token.COMMA) {
			node.Args = append(node.Args, p.parseExpr(0))
		}
	}
	return node
}

func (p *parser) parseExit() ast.Stmt {
	node := &ast.Exit{PosV: p.cur().Pos}
	p.advance() // exit
	if !p.atStmtEnd() {
		node.Code = p.parseExpr(0)
	}
	return node
}

func (p *parser) parseUnset() ast.Stmt {
	node := &ast.Unset{PosV: p.cur().Pos}
	p.advance() // unset
	node.Name = p.dottedName()
	return node
}

func (p *parser) parseInclude() ast.Stmt {
	pos := p.advance().Pos // include
	return &ast.Include{PosV: pos, Path: p.parseExpr(0)}
}

func (p *parser) parseSnippet() ast.Stmt {
	pos := p.advance().Pos // snippet
	name := p.expect(token.IDENT).Lit
	body := p.parseBody()
	return &ast.Snippet{PosV: pos, Name: name, Body: body.Raw}
}

func (p *parser) parseEvalStmt() ast.Stmt {
	pos := p.advance().Pos // eval
	return &ast.EvalStmt{PosV: pos, Code: p.parseExpr(0)}
}

func (p *parser) parseSleep() ast.Stmt {
	pos := p.advance().Pos // sleep
	return &ast.Sleep{PosV: pos, Secs: p.parseExpr(0)}
}

func (p *parser) parseExport() ast.Stmt {
	pos := p.advance().Pos // export
	name := p.expect(token.IDENT).Lit
	p.expect(token.ASSIGN)
	return &ast.Export{PosV: pos, Name: name, Value: p.parseExpr(0)}
}

// parseVar parses `var name` or `var name = expr` — a lexical-scope declaration.
// Only `=` initializes (a fresh declaration has nothing for ?=/+= to build on).
func (p *parser) parseVar() ast.Stmt {
	pos := p.advance().Pos // var
	name := p.expect(token.IDENT).Lit
	node := &ast.Var{PosV: pos, Name: name}
	if p.accept(token.ASSIGN) {
		node.Value = p.parseExpr(0)
		node.HasInit = true
	}
	return node
}

// parseStage parses `stage NAME FILE ARGS...`. The rest of the line is captured
// raw (so ${stage.x} references survive) and split into words.
func (p *parser) parseStage() ast.Stmt {
	pos := p.advance().Pos // stage
	startOff := p.cur().Pos.Off
	endOff, _, _ := p.scanDeclEnd()
	p.i = p.declEndIndex
	words := declWords(p.src[startOff:endOff])
	if len(words) < 2 {
		p.fail(pos, "stage requires a name and a pipeline file")
	}
	return &ast.Stage{PosV: pos, Name: words[0], File: words[1], Args: words[2:]}
}

func (p *parser) atStmtEnd() bool {
	switch p.cur().Kind {
	case token.NEWLINE, token.SEMI, token.EOF, token.RBRACE:
		return true
	}
	return false
}

// lineIsAssignment reports whether the current logical line is `name OP expr`
// (OP in = ?= +=) rather than a target declaration, by checking which comes
// first at bracket-depth 0: an assignment operator or a ':'.
func (p *parser) lineIsAssignment() bool {
	depth := 0
	for j := p.i; j < len(p.toks); j++ {
		switch p.toks[j].Kind {
		case token.LBRACK, token.LPAREN:
			depth++
		case token.RBRACK, token.RPAREN:
			depth--
		case token.ASSIGN, token.QASSIGN, token.PLUSASSIGN:
			if depth == 0 {
				return true
			}
		case token.COLON, token.LBODY, token.NEWLINE, token.SEMI, token.EOF:
			if depth == 0 {
				return false
			}
		}
	}
	return false
}

func (p *parser) parseAssign() ast.Stmt {
	start := p.cur().Pos
	// Index target: `recv[idx] OP value`. A top-level '[' before the assignment
	// operator marks an index lvalue; parse it as an expression rather than a name.
	if p.lhsHasIndex() {
		target := p.parsePostfix()
		idx, ok := target.(*ast.Index)
		if !ok {
			p.fail(start, "invalid assignment target")
		}
		op := p.cur().Kind
		switch op {
		case token.ASSIGN, token.QASSIGN, token.PLUSASSIGN:
		default:
			p.fail(p.cur().Pos, "expected assignment operator")
		}
		p.advance()
		return &ast.Assign{PosV: start, Target: idx, Op: op, Value: p.parseExpr(0)}
	}
	startOff := start.Off
	for {
		switch p.cur().Kind {
		case token.ASSIGN, token.QASSIGN, token.PLUSASSIGN:
			name := strings.TrimSpace(p.src[startOff:p.cur().Pos.Off])
			op := p.cur().Kind
			p.advance()
			return &ast.Assign{PosV: start, Name: name, Op: op, Value: p.parseExpr(0)}
		case token.NEWLINE, token.EOF:
			p.fail(p.cur().Pos, "expected assignment operator")
		}
		p.advance()
	}
}

// lhsHasIndex reports whether the assignment LHS on the current line is an index
// target (a top-level '[' appears before the assignment operator at bracket
// depth 0).
func (p *parser) lhsHasIndex() bool {
	depth := 0
	for j := p.i; j < len(p.toks); j++ {
		switch p.toks[j].Kind {
		case token.LPAREN:
			depth++
		case token.RPAREN:
			depth--
		case token.LBRACK:
			if depth == 0 {
				return true
			}
			depth++
		case token.RBRACK:
			depth--
		case token.ASSIGN, token.QASSIGN, token.PLUSASSIGN:
			if depth == 0 {
				return false
			}
		case token.NEWLINE, token.SEMI, token.EOF:
			return false
		}
	}
	return false
}

// dottedName reads a (possibly dotted) name from the raw source up to the next
// statement separator and returns it trimmed.
func (p *parser) dottedName() string {
	startOff := p.cur().Pos.Off
	endOff := startOff
	for !p.atStmtEnd() {
		endOff = p.cur().Pos.Off + tokenWidth(p.cur())
		p.advance()
	}
	return strings.TrimSpace(p.src[startOff:endOff])
}

// ---- targets ----

func (p *parser) parseReservedTarget() ast.Stmt {
	node := &ast.Target{PosV: p.cur().Pos}
	p.expect(token.AT)
	nameTok := p.expect(token.IDENT)
	if !reservedTargets[nameTok.Lit] {
		p.fail(nameTok.Pos, "unknown reserved target @%s", nameTok.Lit)
	}
	node.Special = nameTok.Lit
	if p.cur().Kind == token.COLON {
		colonOff := p.cur().Pos.Off
		endOff, _, bodyIdx := p.scanDeclEnd()
		node.Inputs = declWords(p.src[colonOff+1 : endOff])
		if bodyIdx >= 0 {
			p.i = bodyIdx
			node.Body = p.parseBody()
		} else {
			p.i = p.declEndIndex
		}
		return node
	}
	if p.cur().Kind == token.LBODY {
		node.Body = p.parseBody()
	}
	return node
}

func (p *parser) parseTarget() ast.Stmt {
	node := &ast.Target{PosV: p.cur().Pos}
	declStartOff := p.cur().Pos.Off
	colonOff := p.findColon()
	if colonOff < 0 {
		p.fail(p.cur().Pos, "target declaration requires ':'")
	}
	endOff, hasBody, bodyIdx := p.scanDeclEnd()
	node.Outputs = declWords(p.src[declStartOff:colonOff])
	node.Inputs = declWords(p.src[colonOff+1 : endOff])
	if hasBody {
		p.i = bodyIdx
		node.Body = p.parseBody()
	} else {
		p.i = p.declEndIndex
	}
	return node
}

// findColon returns the byte offset of the first depth-0 ':' on the current
// logical line, or -1 if none precedes the body / end of line.
func (p *parser) findColon() int {
	depth := 0
	for j := p.i; j < len(p.toks); j++ {
		switch p.toks[j].Kind {
		case token.LBRACK, token.LPAREN:
			depth++
		case token.RBRACK, token.RPAREN:
			depth--
		case token.COLON:
			if depth == 0 {
				return p.toks[j].Pos.Off
			}
		case token.LBODY, token.NEWLINE, token.SEMI, token.EOF:
			if depth == 0 {
				return -1
			}
		}
	}
	return -1
}

// scanDeclEnd finds where the target declaration ends. It returns the byte
// offset of the terminator, whether a {{ body follows, and the token index of
// the LBODY (or -1). It also records declEndIndex (the terminator token index).
func (p *parser) scanDeclEnd() (endOff int, hasBody bool, bodyIdx int) {
	depth := 0
	for j := p.i; j < len(p.toks); j++ {
		t := p.toks[j]
		switch t.Kind {
		case token.LBRACK, token.LPAREN:
			depth++
		case token.RBRACK, token.RPAREN:
			depth--
		case token.LBODY:
			if depth == 0 {
				p.declEndIndex = j
				return t.Pos.Off, true, j
			}
		case token.NEWLINE, token.SEMI, token.EOF:
			if depth == 0 {
				p.declEndIndex = j
				return t.Pos.Off, false, -1
			}
		}
	}
	last := len(p.toks) - 1
	p.declEndIndex = last
	return p.toks[last].Pos.Off, false, -1
}

func (p *parser) parseBody() *ast.Body {
	pos := p.cur().Pos
	p.expect(token.LBODY)
	raw := p.expect(token.BODY).Lit
	p.expect(token.RBODY)
	return &ast.Body{PosV: pos, Raw: raw}
}

// ---- expressions (precedence climbing) ----

func binPower(k token.Kind) (bp int, rightAssoc bool) {
	switch k {
	case token.OR:
		return 1, false
	case token.AND:
		return 2, false
	case token.EQ, token.NEQ, token.LT, token.LE, token.GT, token.GE:
		return 3, false
	case token.DOTDOT:
		return 4, false
	case token.PLUS, token.MINUS:
		return 5, false
	case token.STAR, token.SLASH, token.PERCENT:
		return 6, false
	case token.POW:
		return 7, true
	}
	return 0, false
}

func (p *parser) parseExpr(minBP int) ast.Expr {
	left := p.parseUnary()
	for {
		op := p.cur().Kind
		bp, right := binPower(op)
		if bp == 0 || bp < minBP {
			return left
		}
		pos := p.advance().Pos
		nextMin := bp + 1
		if right {
			nextMin = bp
		}
		rhs := p.parseExpr(nextMin)
		if op == token.DOTDOT {
			left = &ast.RangeLit{PosV: pos, Lo: left, Hi: rhs}
		} else {
			left = &ast.Binary{PosV: pos, Op: op, L: left, R: rhs}
		}
	}
}

func (p *parser) parseUnary() ast.Expr {
	if p.cur().Kind == token.NOT {
		pos := p.cur().Pos
		p.advance()
		return &ast.Unary{PosV: pos, Op: token.NOT, X: p.parseUnary()}
	}
	if p.cur().Kind == token.MINUS {
		pos := p.cur().Pos
		p.advance()
		// Unary minus binds looser than ** so -2**2 parses as -(2**2), matching
		// the Python/Ruby/Fortran convention. Parsing the operand at **'s binding
		// power lets it absorb a trailing ** chain but nothing lower-precedence,
		// so -2*3 still parses as (-2)*3.
		powBP, _ := binPower(token.POW)
		return &ast.Unary{PosV: pos, Op: token.MINUS, X: p.parseExpr(powBP)}
	}
	return p.parsePostfix()
}

func (p *parser) parsePostfix() ast.Expr {
	x := p.parsePrimary()
	for {
		switch p.cur().Kind {
		case token.LPAREN:
			// free builtin call: an identifier directly followed by '(', e.g. open("f").
			id, ok := x.(*ast.Ident)
			if !ok {
				return x
			}
			pos := p.advance().Pos // (
			args, kwargs := p.parseCallArgs()
			x = &ast.Call{PosV: pos, Recv: nil, Method: id.Name, Args: args, Kwargs: kwargs}
		case token.DOT:
			pos := p.advance().Pos
			name := p.expect(token.IDENT).Lit
			if p.cur().Kind != token.LPAREN {
				// dotted variable name (e.g. job.stdout, cgp.runner) — extend the
				// identifier rather than treating '.' as a method call.
				if id, ok := x.(*ast.Ident); ok {
					id.Name += "." + name
					continue
				}
				p.fail(pos, "expected '(' for method call after '.%s'", name)
			}
			p.advance() // (
			args, kwargs := p.parseCallArgs()
			x = &ast.Call{PosV: pos, Recv: x, Method: name, Args: args, Kwargs: kwargs}
		case token.LBRACK:
			pos := p.advance().Pos
			x = p.parseIndexOrSlice(pos, x)
		default:
			return x
		}
	}
}

func (p *parser) parseIndexOrSlice(pos token.Pos, recv ast.Expr) ast.Expr {
	// '[' already consumed
	if p.cur().Kind == token.COLON { // [:hi]
		p.advance()
		var hi ast.Expr
		if p.cur().Kind != token.RBRACK {
			hi = p.parseExpr(0)
		}
		p.expect(token.RBRACK)
		return &ast.Slice{PosV: pos, Recv: recv, Lo: nil, Hi: hi}
	}
	lo := p.parseExpr(0)
	if p.cur().Kind == token.COLON { // [lo:hi]
		p.advance()
		var hi ast.Expr
		if p.cur().Kind != token.RBRACK {
			hi = p.parseExpr(0)
		}
		p.expect(token.RBRACK)
		return &ast.Slice{PosV: pos, Recv: recv, Lo: lo, Hi: hi}
	}
	p.expect(token.RBRACK)
	return &ast.Index{PosV: pos, Recv: recv, Idx: lo}
}

func (p *parser) parsePrimary() ast.Expr {
	t := p.cur()
	switch t.Kind {
	case token.INT:
		p.advance()
		v, err := strconv.ParseInt(t.Lit, 10, 64)
		if err != nil {
			p.fail(t.Pos, "invalid integer %q", t.Lit)
		}
		return &ast.IntLit{PosV: t.Pos, Val: v}
	case token.FLOAT:
		p.advance()
		v, err := strconv.ParseFloat(t.Lit, 64)
		if err != nil {
			p.fail(t.Pos, "invalid float %q", t.Lit)
		}
		return &ast.FloatLit{PosV: t.Pos, Val: v}
	case token.STRING:
		p.advance()
		return &ast.StringLit{PosV: t.Pos, Raw: t.Lit}
	case token.TRUE:
		p.advance()
		return &ast.BoolLit{PosV: t.Pos, Val: true}
	case token.FALSE:
		p.advance()
		return &ast.BoolLit{PosV: t.Pos, Val: false}
	case token.IDENT:
		p.advance()
		return &ast.Ident{PosV: t.Pos, Name: t.Lit}
	case token.LPAREN:
		p.advance()
		e := p.parseExpr(0)
		p.expect(token.RPAREN)
		return e
	case token.LBRACK:
		return p.parseListLit()
	case token.LBRACE:
		return p.parseMapLit()
	default:
		p.fail(t.Pos, "unexpected %s in expression", t)
		return nil
	}
}

// parseCallArgs parses a call's argument list, assuming the opening '(' has been
// consumed, up to and including the closing ')'. Positional args come first;
// a keyword argument is `IDENT = expr` (ASSIGN, distinct from the EQ of `==`).
// A positional arg after a keyword arg is an error.
func (p *parser) parseCallArgs() ([]ast.Expr, []ast.KwArg) {
	var args []ast.Expr
	var kwargs []ast.KwArg
	if p.cur().Kind != token.RPAREN {
		for {
			if p.cur().Kind == token.IDENT && p.peek().Kind == token.ASSIGN {
				name := p.cur().Lit
				p.advance() // IDENT
				p.advance() // =
				kwargs = append(kwargs, ast.KwArg{Name: name, Value: p.parseExpr(0)})
			} else {
				if len(kwargs) > 0 {
					p.fail(p.cur().Pos, "positional argument after keyword argument")
				}
				args = append(args, p.parseExpr(0))
			}
			if !p.accept(token.COMMA) {
				break
			}
			if p.cur().Kind == token.RPAREN { // trailing comma
				break
			}
		}
	}
	p.expect(token.RPAREN)
	return args, kwargs
}

// parseMapLit parses an ordered, string-keyed map literal `{ k: v, … }` (and the
// empty map `{}`). A single '{' lexes as LBRACE (a shell body is '{{'), and map
// literals only appear in expression position, so this never collides with an
// if/for block (those consume their '{' directly via parseBlock).
func (p *parser) parseMapLit() ast.Expr {
	pos := p.cur().Pos
	p.expect(token.LBRACE)
	node := &ast.MapLit{PosV: pos}
	if p.cur().Kind != token.RBRACE {
		for {
			key := p.parseExpr(0)
			p.expect(token.COLON)
			val := p.parseExpr(0)
			node.Entries = append(node.Entries, ast.MapEntry{Key: key, Value: val})
			if !p.accept(token.COMMA) {
				break
			}
			if p.cur().Kind == token.RBRACE { // trailing comma
				break
			}
		}
	}
	p.expect(token.RBRACE)
	return node
}

func (p *parser) parseListLit() ast.Expr {
	pos := p.cur().Pos
	p.expect(token.LBRACK)
	node := &ast.ListLit{PosV: pos}
	if p.cur().Kind != token.RBRACK {
		node.Elems = append(node.Elems, p.parseExpr(0))
		for p.accept(token.COMMA) {
			if p.cur().Kind == token.RBRACK { // trailing comma
				break
			}
			node.Elems = append(node.Elems, p.parseExpr(0))
		}
	}
	p.expect(token.RBRACK)
	return node
}

// ---- helpers ----

// reservedTargets is the lookup set for @-prefixed target validation, built from
// the canonical ast.ReservedTargets list.
var reservedTargets = func() map[string]bool {
	m := make(map[string]bool, len(ast.ReservedTargets))
	for _, n := range ast.ReservedTargets {
		m[n] = true
	}
	return m
}()

// declWords splits a raw target-declaration fragment into word templates,
// dropping a trailing comment. It splits on unquoted whitespace while treating
// "…", ${…}, @{…}, and $(…) as opaque spans, so a word like ${if c; "a b"; c}
// (whose interior contains spaces or a #) stays a single word. ${…}/@{…}/^ are
// preserved verbatim for eval. A '#' outside any span starts a comment.
func declWords(raw string) []string {
	var words []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			words = append(words, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(raw); {
		c := raw[i]
		switch {
		case c == '#':
			flush()
			return words // comment to end of line
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			flush()
			i++
		case c == '"':
			j := i + 1
			for j < len(raw) { // closing-quote scan, honoring \ escapes
				if raw[j] == '\\' && j+1 < len(raw) {
					j += 2
					continue
				}
				if raw[j] == '"' {
					j++
					break
				}
				j++
			}
			cur.WriteString(raw[i:j])
			i = j
		case (c == '$' || c == '@') && i+1 < len(raw) && raw[i+1] == '{':
			i = copySpan(&cur, raw, i, declBraceSpan(raw[i+2:]))
		case c == '$' && i+1 < len(raw) && raw[i+1] == '(':
			i = copySpan(&cur, raw, i, declParenSpan(raw[i+2:]))
		default:
			cur.WriteByte(c)
			i++
		}
	}
	flush()
	return words
}

// copySpan appends the span starting at i (an opener of width 2, e.g. "${"/"$(")
// whose inner length is span (offset of the closer within raw[i+2:], or -1 if
// unterminated) to cur, and returns the new scan index. An unterminated span
// consumes the rest of the fragment.
func copySpan(cur *strings.Builder, raw string, i, span int) int {
	if span < 0 {
		cur.WriteString(raw[i:])
		return len(raw)
	}
	end := i + 2 + span + 1
	cur.WriteString(raw[i:end])
	return end
}

// declBraceSpan returns the offset of the '}' closing a ${…}/@{…} whose opener
// was consumed, or -1 if unterminated. Mirrors eval.braceSpan (a quoted substring
// is opaque; \-escaped bytes are skipped; an escaped \" toggles string mode too)
// — duplicated here to avoid a parser→eval import cycle.
func declBraceSpan(s string) int {
	depth, inStr := 0, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' && i+1 < len(s) {
			if s[i+1] == '"' {
				inStr = !inStr
			}
			i++
			continue
		}
		if c == '"' {
			inStr = !inStr
			continue
		}
		if inStr {
			continue
		}
		switch c {
		case '{':
			depth++
		case '}':
			if depth == 0 {
				return i
			}
			depth--
		}
	}
	return -1
}

// declParenSpan returns the offset of the ')' closing a $( … ) whose opener was
// consumed, or -1 if unterminated. Mirrors eval.parenSpan's shell-quoting rules
// — duplicated here to avoid a parser→eval import cycle.
func declParenSpan(s string) int {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '\'':
			i++
			for i < len(s) && s[i] != '\'' {
				i++
			}
		case '"':
			i++
			for i < len(s) && s[i] != '"' {
				if s[i] == '\\' {
					i++
				}
				i++
			}
		case '(':
			depth++
		case ')':
			if depth == 0 {
				return i
			}
			depth--
		}
	}
	return -1
}

// tokenWidth is the source width of a token, for raw-name slicing.
func tokenWidth(t token.Token) int {
	switch t.Kind {
	case token.IDENT, token.INT, token.FLOAT:
		return len(t.Lit)
	default:
		return len(t.Kind.String())
	}
}
