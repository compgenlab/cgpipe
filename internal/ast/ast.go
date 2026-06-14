package ast

import "github.com/compgen-io/cgp/internal/token"

// Node is any AST node.
type Node interface{ Pos() token.Pos }

// File is a parsed cgp source file: a sequence of top-level statements plus the
// leading comment block (help text).
type File struct {
	Stmts []Stmt
	Help  string
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
	// Call is recv.method(args...). Recv is nil for a builtin free call like
	// open("f") — Method then names the builtin. Kwargs holds keyword arguments
	// (name=value), which always follow the positional Args.
	Call struct {
		PosV   token.Pos
		Recv   Expr
		Method string
		Args   []Expr
		Kwargs []KwArg
	}
	// MapLit is {"k": v, …} — an ordered, string-keyed map literal. Each Key must
	// evaluate to a string at eval time. Empty Entries is the empty map {}.
	MapLit struct {
		PosV    token.Pos
		Entries []MapEntry
	}
	// KwArg is a single keyword argument `Name=Value` in a call.
	KwArg struct {
		Name  string
		Value Expr
	}
	// MapEntry is one `Key: Value` pair in a MapLit.
	MapEntry struct {
		Key   Expr
		Value Expr
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
func (e *MapLit) Pos() token.Pos    { return e.PosV }
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
func (*MapLit) exprNode()    {}
func (*Unary) exprNode()     {}
func (*Binary) exprNode()    {}

// ---- statements ----

type Stmt interface {
	Node
	stmtNode()
}

type (
	// Assign is `name OP value` where OP is = / ?= / +=. For a plain or dotted
	// variable target, Name holds the name and Target is nil. For an index target
	// (`m["k"] OP value`), Target holds the Index expression and Name is "".
	Assign struct {
		PosV   token.Pos
		Name   string
		Target Expr
		Op     token.Kind
		Value  Expr
	}
	// ExprStmt is a bare call expression used as a statement, evaluated for its
	// side effect (e.g. `f.write("x")`, `f.close()`). Restricted to calls by the
	// parser so other statement forms (notably targets) are unaffected.
	ExprStmt struct {
		PosV token.Pos
		X    Expr
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
	// An optional `with IndexVar` on the `in` form binds a 1-based loop counter.
	For struct {
		PosV     token.Pos
		Var      string
		IndexVar string // optional `with i` counter; "" when absent
		Iter     Expr
		Cond     Expr
		Body     []Stmt
	}
	// Include inlines another .cgp file at this point (global context).
	Include struct {
		PosV token.Pos
		Path Expr
	}
	// Snippet defines a reusable body fragment, invoked with @Name inside a body.
	Snippet struct {
		PosV token.Pos
		Name string
		Body string
	}
	// EvalStmt evaluates a string-valued expression as cgp source at run time.
	EvalStmt struct {
		PosV token.Pos
		Code Expr
	}
	// Export exposes a named value from a (stage) pipeline to a calling workflow.
	// A no-op when the pipeline runs standalone.
	Export struct {
		PosV  token.Pos
		Name  string
		Value Expr
	}
	// Stage declares a stage of a workflow: run File (a .cgp pipeline) with Args,
	// exposing its exports as ${Name.export}. Name/File/Args are raw templates,
	// resolved at orchestration time (so ${prior_stage.x} references work).
	Stage struct {
		PosV token.Pos
		Name string
		File string
		Args []string
	}
	// Dumpvars prints all in-scope variables (debugging).
	Dumpvars struct{ PosV token.Pos }
	// Showhelp prints the script's help-text block.
	Showhelp struct{ PosV token.Pos }
	// Sleep pauses for the given number of seconds.
	Sleep struct {
		PosV token.Pos
		Secs Expr
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

func (s *Assign) Pos() token.Pos   { return s.PosV }
func (s *ExprStmt) Pos() token.Pos { return s.PosV }
func (s *Print) Pos() token.Pos    { return s.PosV }
func (s *Exit) Pos() token.Pos     { return s.PosV }
func (s *Unset) Pos() token.Pos    { return s.PosV }
func (s *If) Pos() token.Pos       { return s.PosV }
func (s *For) Pos() token.Pos      { return s.PosV }
func (s *Target) Pos() token.Pos   { return s.PosV }
func (s *Include) Pos() token.Pos  { return s.PosV }
func (s *Snippet) Pos() token.Pos  { return s.PosV }
func (s *EvalStmt) Pos() token.Pos { return s.PosV }
func (s *Dumpvars) Pos() token.Pos { return s.PosV }
func (s *Showhelp) Pos() token.Pos { return s.PosV }
func (s *Sleep) Pos() token.Pos    { return s.PosV }
func (s *Export) Pos() token.Pos   { return s.PosV }
func (s *Stage) Pos() token.Pos    { return s.PosV }

func (*Assign) stmtNode()   {}
func (*ExprStmt) stmtNode() {}
func (*Print) stmtNode()    {}
func (*Exit) stmtNode()     {}
func (*Unset) stmtNode()    {}
func (*If) stmtNode()       {}
func (*For) stmtNode()      {}
func (*Target) stmtNode()   {}
func (*Include) stmtNode()  {}
func (*Snippet) stmtNode()  {}
func (*EvalStmt) stmtNode() {}
func (*Dumpvars) stmtNode() {}
func (*Showhelp) stmtNode() {}
func (*Sleep) stmtNode()    {}
func (*Export) stmtNode()   {}
func (*Stage) stmtNode()    {}
