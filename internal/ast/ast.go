package ast

import "github.com/compgen-io/cgp/internal/token"

// Node is any AST node.
type Node interface{ Pos() token.Pos }

// File is a parsed cgp source file: a sequence of top-level statements.
type File struct {
	Stmts []Stmt
}

// ---- expressions ----

type Expr interface {
	Node
	exprNode()
}

type (
	// IntLit is an integer literal.
	IntLit struct {
		PosV token.Pos
		Val  int64
	}
	// FloatLit is a floating-point literal.
	FloatLit struct {
		PosV token.Pos
		Val  float64
	}
	// StringLit holds the raw inner text of a "..." literal; ${…}/@{…} escapes
	// are resolved at eval time, not here.
	StringLit struct {
		PosV token.Pos
		Raw  string
	}
	// BoolLit is true/false.
	BoolLit struct {
		PosV token.Pos
		Val  bool
	}
	// Ident references a variable.
	Ident struct {
		PosV token.Pos
		Name string
	}
	// ListLit is [a, b, c].
	ListLit struct {
		PosV  token.Pos
		Elems []Expr
	}
	// RangeLit is lo..hi.
	RangeLit struct {
		PosV   token.Pos
		Lo, Hi Expr
	}
	// Index is recv[idx].
	Index struct {
		PosV token.Pos
		Recv Expr
		Idx  Expr
	}
	// Slice is recv[lo:hi]; Lo and/or Hi may be nil.
	Slice struct {
		PosV   token.Pos
		Recv   Expr
		Lo, Hi Expr
	}
	// Call is recv.method(args...).
	Call struct {
		PosV   token.Pos
		Recv   Expr
		Method string
		Args   []Expr
	}
	// Unary is op X (op is NOT or MINUS).
	Unary struct {
		PosV token.Pos
		Op   token.Kind
		X    Expr
	}
	// Binary is L op R.
	Binary struct {
		PosV token.Pos
		Op   token.Kind
		L, R Expr
	}
)

func (e *IntLit) Pos() token.Pos    { return e.PosV }
func (e *FloatLit) Pos() token.Pos  { return e.PosV }
func (e *StringLit) Pos() token.Pos { return e.PosV }
func (e *BoolLit) Pos() token.Pos   { return e.PosV }
func (e *Ident) Pos() token.Pos     { return e.PosV }
func (e *ListLit) Pos() token.Pos   { return e.PosV }
func (e *RangeLit) Pos() token.Pos  { return e.PosV }
func (e *Index) Pos() token.Pos     { return e.PosV }
func (e *Slice) Pos() token.Pos     { return e.PosV }
func (e *Call) Pos() token.Pos      { return e.PosV }
func (e *Unary) Pos() token.Pos     { return e.PosV }
func (e *Binary) Pos() token.Pos    { return e.PosV }

func (*IntLit) exprNode()    {}
func (*FloatLit) exprNode()  {}
func (*StringLit) exprNode() {}
func (*BoolLit) exprNode()   {}
func (*Ident) exprNode()     {}
func (*ListLit) exprNode()   {}
func (*RangeLit) exprNode()  {}
func (*Index) exprNode()     {}
func (*Slice) exprNode()     {}
func (*Call) exprNode()      {}
func (*Unary) exprNode()     {}
func (*Binary) exprNode()    {}

// ---- statements ----

type Stmt interface {
	Node
	stmtNode()
}

type (
	// Assign is `name OP value` where OP is = / ?= / +=.
	Assign struct {
		PosV  token.Pos
		Name  string
		Op    token.Kind
		Value Expr
	}
	// Print writes its args to stdout (or, inside a body, appends to the script).
	Print struct {
		PosV token.Pos
		Args []Expr
	}
	// Exit stops the pipeline; Code may be nil (=> 0).
	Exit struct {
		PosV token.Pos
		Code Expr
	}
	// Unset removes a variable.
	Unset struct {
		PosV token.Pos
		Name string
	}
	// If is an if/elif/else chain: Conds[i] guards Blocks[i]; Else runs if none match.
	If struct {
		PosV   token.Pos
		Conds  []Expr
		Blocks [][]Stmt
		Else   []Stmt
	}
	// For is `for Var in Iter { Body }` (Cond nil) or `for Cond { Body }` (Var "", Iter nil).
	For struct {
		PosV token.Pos
		Var  string
		Iter Expr
		Cond Expr
		Body []Stmt
	}
	// Target is an output-first build rule. Outputs/Inputs are raw word templates
	// (may carry a leading ^, ${…}, @{…}, % — resolved at eval). Body is nil for a
	// bodyless (aggregator) target. Special names a reserved @-target ("pre",
	// "post", "setup", "teardown", "postsubmit", "default") or "" for a normal one.
	Target struct {
		PosV    token.Pos
		Special string
		Outputs []string
		Inputs  []string
		Body    *Body
	}
)

// Body is the raw text of a {{ }} shell body, captured verbatim. The directive
// block (before --), %-control lines, and shell are resolved at render time.
type Body struct {
	PosV token.Pos
	Raw  string
}

func (s *Assign) Pos() token.Pos { return s.PosV }
func (s *Print) Pos() token.Pos  { return s.PosV }
func (s *Exit) Pos() token.Pos   { return s.PosV }
func (s *Unset) Pos() token.Pos  { return s.PosV }
func (s *If) Pos() token.Pos     { return s.PosV }
func (s *For) Pos() token.Pos    { return s.PosV }
func (s *Target) Pos() token.Pos { return s.PosV }

func (*Assign) stmtNode() {}
func (*Print) stmtNode()  {}
func (*Exit) stmtNode()   {}
func (*Unset) stmtNode()  {}
func (*If) stmtNode()     {}
func (*For) stmtNode()    {}
func (*Target) stmtNode() {}
