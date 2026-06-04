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

type parseError struct {
	pos token.Pos
	msg string
}

func (e *parseError) Error() string { return fmt.Sprintf("%s: %s", e.pos, e.msg) }

type bailout struct{}

func (p *parser) fail(pos token.Pos, format string, args ...any) {
	if p.err == nil {
		p.err = &parseError{pos: pos, msg: fmt.Sprintf(format, args...)}
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
		return p.parseReservedTarget()
	case token.CARET, token.COLON:
		return p.parseTarget()
	case token.IDENT:
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
		case "log":
			return p.parseLog()
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

func (p *parser) parseLog() ast.Stmt {
	pos := p.advance().Pos // log
	return &ast.Log{PosV: pos, Path: p.parseExpr(0)}
}

func (p *parser) parseEvalStmt() ast.Stmt {
	pos := p.advance().Pos // eval
	return &ast.EvalStmt{PosV: pos, Code: p.parseExpr(0)}
}

func (p *parser) parseSleep() ast.Stmt {
	pos := p.advance().Pos // sleep
	return &ast.Sleep{PosV: pos, Secs: p.parseExpr(0)}
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
	if p.cur().Kind == token.NOT || p.cur().Kind == token.MINUS {
		pos := p.cur().Pos
		op := p.advance().Kind
		return &ast.Unary{PosV: pos, Op: op, X: p.parseUnary()}
	}
	return p.parsePostfix()
}

func (p *parser) parsePostfix() ast.Expr {
	x := p.parsePrimary()
	for {
		switch p.cur().Kind {
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
			var args []ast.Expr
			if p.cur().Kind != token.RPAREN {
				args = append(args, p.parseExpr(0))
				for p.accept(token.COMMA) {
					args = append(args, p.parseExpr(0))
				}
			}
			p.expect(token.RPAREN)
			x = &ast.Call{PosV: pos, Recv: x, Method: name, Args: args}
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
	default:
		p.fail(t.Pos, "unexpected %s in expression", t)
		return nil
	}
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

var reservedTargets = map[string]bool{
	"pre": true, "post": true, "setup": true, "teardown": true,
	"postsubmit": true, "default": true,
}

// declWords splits a raw target-declaration fragment into whitespace-separated
// word templates, dropping a trailing comment. ${…}/@{…}/^ are preserved for eval.
func declWords(raw string) []string {
	if i := strings.IndexByte(raw, '#'); i >= 0 {
		raw = raw[:i]
	}
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return nil
	}
	return fields
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
