// Package ast defines the MicroC syntax tree produced by the parser.
//
// Every node carries a source position, threaded through to LLVM debug
// locations so a late rejection can still name a source line.
package ast

import "github.com/greg2010/ic11c/internal/source"

// Node is any syntax tree node.
type Node interface {
	// Pos reports where the node's first token was written. It is always valid
	// for a node the parser built.
	Pos() source.Position
}

// Expr is an expression node.
type Expr interface {
	Node
	exprNode()
}

// Stmt is a statement node.
type Stmt interface {
	Node
	stmtNode()
}

// Decl is a declaration node. [VarDecl] is also a [Stmt], because MicroC admits
// a declaration wherever it admits a statement.
type Decl interface {
	Node
	declNode()
}

// Type is a syntactic type node.
type Type interface {
	Node
	typeNode()
}

// File is one parsed translation unit. Decls holds every declaration that
// parsed, in source order; a declaration the parser could not make sense of
// appears as a [BadDecl] so later positions stay meaningful.
type File struct {
	Name  string
	Start source.Position
	Decls []Decl
}

// ScalarType is a built-in type keyword.
type ScalarType struct {
	TypePos source.Position
	Kind    ScalarKind
}

// PointerType is a pointer to Elem. Pos reports the '*', not the start of the
// whole type; walk Elem to reach the base.
type PointerType struct {
	Star source.Position
	Elem Type
}

// ArrayType is an array of Elem. Size is nil for an unsized array, which the
// grammar admits only as a parameter that decays to a pointer. Pos reports
// the '[', not the start of the whole type. Arrays are one-dimensional, so
// Elem is never itself an ArrayType.
type ArrayType struct {
	Lbrack source.Position
	Elem   Type
	Size   Expr
}

func (t *ScalarType) Pos() source.Position  { return t.TypePos }
func (t *PointerType) Pos() source.Position { return t.Star }
func (t *ArrayType) Pos() source.Position   { return t.Lbrack }

func (*ScalarType) typeNode()  {}
func (*PointerType) typeNode() {}
func (*ArrayType) typeNode()   {}

// Param is one function parameter. Name is empty for an unnamed parameter,
// which a prototype may write; NamePos is then invalid.
type Param struct {
	ParamPos source.Position
	Const    bool
	Type     Type
	Name     string
	NamePos  source.Position
}

func (p *Param) Pos() source.Position { return p.ParamPos }

// FuncDecl is a function definition, or a prototype when Body is nil. Params is
// empty both for "f()" and for "f(void)"; the grammar treats them alike.
type FuncDecl struct {
	DeclPos source.Position
	Result  Type
	Name    string
	NamePos source.Position
	Params  []*Param
	Body    *BlockStmt
}

// PrefabAttr is the attribute a dev declaration uses to state which prefab
// the pin it names is wired to. What a pin reaches is decided when the
// chip is placed in the world, so this is a promise about the world, not
// a program fact: nothing downstream of analysis reads it.
type PrefabAttr struct {
	// At is where the attribute begins: the '[[' of the specifier it leads, or
	// its own first token where a comma separated list wrote it after another.
	At source.Position
	// Name is the prefab name the attribute's string literal spelled, and
	// NamePos where that literal was written.
	Name    string
	NamePos source.Position
}

func (a *PrefabAttr) Pos() source.Position { return a.At }

// VarDecl declares one variable. MicroC admits a single declarator per
// declaration, so the type and initializer sit directly on the node.
type VarDecl struct {
	DeclPos source.Position
	Const   bool
	// Constexpr implies Const and adds that the object names a constant
	// expression.
	Constexpr bool
	// Prefab is the attribute the declaration led with, or nil for none.
	Prefab  *PrefabAttr
	Type    Type
	Name    string
	NamePos source.Position
	Init    Expr
}

// BadDecl marks source the parser could not read as a declaration. From is the
// first token of the failed declaration; what the parser skipped to resynchronize
// is not recorded.
type BadDecl struct {
	From source.Position
}

func (d *FuncDecl) Pos() source.Position { return d.DeclPos }
func (d *VarDecl) Pos() source.Position  { return d.DeclPos }
func (d *BadDecl) Pos() source.Position  { return d.From }

func (*FuncDecl) declNode() {}
func (*VarDecl) declNode()  {}
func (*BadDecl) declNode()  {}

func (*VarDecl) stmtNode() {}

// BlockStmt is a brace-enclosed statement list and a scope.
type BlockStmt struct {
	Lbrace source.Position
	Stmts  []Stmt
	Rbrace source.Position
}

// ExprStmt is an expression evaluated for its effect.
type ExprStmt struct {
	X Expr
}

// EmptyStmt is a lone semicolon, which a for loop with no body writes.
type EmptyStmt struct {
	Semi source.Position
}

// IfStmt is a conditional. Else is nil when no else clause was written.
type IfStmt struct {
	IfPos source.Position
	Cond  Expr
	Then  Stmt
	Else  Stmt
}

// WhileStmt tests before each iteration.
type WhileStmt struct {
	WhilePos source.Position
	Cond     Expr
	Body     Stmt
}

// DoStmt tests after each iteration.
type DoStmt struct {
	DoPos source.Position
	Body  Stmt
	Cond  Expr
}

// ForStmt is a counted loop. Init is a [VarDecl], an [ExprStmt], or nil; Cond
// and Post are nil when omitted. An omitted Cond loops forever.
type ForStmt struct {
	ForPos source.Position
	Init   Stmt
	Cond   Expr
	Post   Expr
	Body   Stmt
}

// SwitchStmt selects among case clauses on the value of Tag.
type SwitchStmt struct {
	SwitchPos source.Position
	Tag       Expr
	Lbrace    source.Position
	Cases     []*CaseClause
}

// CaseClause is one arm of a [SwitchStmt]. Value is nil for the default
// arm; Body holds the statements up to the next arm. The parser records
// arms as written — constant-ness, distinctness, and fallthrough are all
// semantic questions.
type CaseClause struct {
	CasePos source.Position
	Value   Expr
	Body    []Stmt
}

func (c *CaseClause) Pos() source.Position { return c.CasePos }

// BreakStmt leaves the innermost loop or switch.
type BreakStmt struct {
	BreakPos source.Position
}

// ContinueStmt begins the next iteration of the innermost loop.
type ContinueStmt struct {
	ContinuePos source.Position
}

// ReturnStmt leaves the enclosing function. Result is nil for a bare return.
type ReturnStmt struct {
	ReturnPos source.Position
	Result    Expr
}

// BadStmt marks source the parser could not read as a statement. From is the
// first token of the failed statement; what the parser skipped to resynchronize
// is not recorded.
type BadStmt struct {
	From source.Position
}

func (s *BlockStmt) Pos() source.Position    { return s.Lbrace }
func (s *ExprStmt) Pos() source.Position     { return s.X.Pos() }
func (s *EmptyStmt) Pos() source.Position    { return s.Semi }
func (s *IfStmt) Pos() source.Position       { return s.IfPos }
func (s *WhileStmt) Pos() source.Position    { return s.WhilePos }
func (s *DoStmt) Pos() source.Position       { return s.DoPos }
func (s *ForStmt) Pos() source.Position      { return s.ForPos }
func (s *SwitchStmt) Pos() source.Position   { return s.SwitchPos }
func (s *BreakStmt) Pos() source.Position    { return s.BreakPos }
func (s *ContinueStmt) Pos() source.Position { return s.ContinuePos }
func (s *ReturnStmt) Pos() source.Position   { return s.ReturnPos }
func (s *BadStmt) Pos() source.Position      { return s.From }

func (*BlockStmt) stmtNode()    {}
func (*ExprStmt) stmtNode()     {}
func (*EmptyStmt) stmtNode()    {}
func (*IfStmt) stmtNode()       {}
func (*WhileStmt) stmtNode()    {}
func (*DoStmt) stmtNode()       {}
func (*ForStmt) stmtNode()      {}
func (*SwitchStmt) stmtNode()   {}
func (*BreakStmt) stmtNode()    {}
func (*ContinueStmt) stmtNode() {}
func (*ReturnStmt) stmtNode()   {}
func (*BadStmt) stmtNode()      {}

// Ident names a variable, a parameter, a function, or an intrinsic. The parser
// does not distinguish them; resolution is semantic analysis's job.
type Ident struct {
	NamePos source.Position
	Name    string
}

// IntLit is a decimal or hexadecimal integer literal. Value is the
// decoded value, and Hex says which spelling wrote it: C's type for an
// integer constant depends on it, since a decimal constant searches the
// signed types alone where a hexadecimal one may land on unsigned.
type IntLit struct {
	ValuePos source.Position
	Value    int64
	Hex      bool
}

// CharLit is a character literal, which denotes a long long. Value is the decoded
// code point, so an escape and the character it denotes are indistinguishable
// once parsed.
type CharLit struct {
	ValuePos source.Position
	Value    int64
}

// FloatLit is a floating-point literal, which denotes a double. Value is the
// decoded value; the spelling it was written with does not survive parsing.
type FloatLit struct {
	ValuePos source.Position
	Value    float64
}

// BoolLit is 'true' or 'false'.
type BoolLit struct {
	ValuePos source.Position
	Value    bool
}

// StringLit is a string literal. Value holds the decoded bytes, which may be
// invalid UTF-8 when the source used hex or octal escapes; __ic_hash takes the
// CRC-32 of exactly those bytes.
type StringLit struct {
	ValuePos source.Position
	Value    string
}

// UnaryExpr applies a prefix operator. Increment and decrement are
// [IncDecExpr] instead, because they also have a postfix form.
type UnaryExpr struct {
	OpPos source.Position
	Op    UnaryOp
	X     Expr
}

// IncDecExpr increments or decrements X. Postfix distinguishes "x++" from
// "++x", which differ in the value they produce.
type IncDecExpr struct {
	OpPos   source.Position
	Op      IncDecOp
	Postfix bool
	X       Expr
}

// BinaryExpr applies an infix operator. Grouping is encoded by the tree, so no
// node records parentheses.
type BinaryExpr struct {
	OpPos source.Position
	Op    BinaryOp
	X     Expr
	Y     Expr
}

// AssignExpr assigns to Target. Whether Target is assignable is a semantic
// question.
type AssignExpr struct {
	OpPos  source.Position
	Op     AssignOp
	Target Expr
	Value  Expr
}

// CondExpr is the ternary conditional.
type CondExpr struct {
	Question source.Position
	Cond     Expr
	Then     Expr
	Else     Expr
}

// IndexExpr subscripts X.
type IndexExpr struct {
	Lbrack source.Position
	X      Expr
	Index  Expr
}

// CallExpr calls Fun. Fun is an [Ident] in every program MicroC accepts, since
// function pointers are excluded, but the parser records whatever postfix
// expression preceded the argument list.
type CallExpr struct {
	Lparen source.Position
	Fun    Expr
	Args   []Expr
}

// CastExpr converts X to Type, which the grammar restricts to long long, bool or
// double.
type CastExpr struct {
	Lparen source.Position
	Type   Type
	X      Expr
}

// InitListExpr is a brace-enclosed initializer. Elems never holds another
// InitListExpr, since arrays are one-dimensional. It is an expression only so
// that it can sit in [VarDecl.Init]; it is not valid in any other position, and
// checking that is semantic analysis's job.
type InitListExpr struct {
	Lbrace source.Position
	Elems  []Expr
}

// BadExpr marks source the parser could not read as an expression. It keeps the
// enclosing tree shaped so that the rest of the statement still parses.
type BadExpr struct {
	From source.Position
}

func (e *Ident) Pos() source.Position        { return e.NamePos }
func (e *IntLit) Pos() source.Position       { return e.ValuePos }
func (e *CharLit) Pos() source.Position      { return e.ValuePos }
func (e *FloatLit) Pos() source.Position     { return e.ValuePos }
func (e *BoolLit) Pos() source.Position      { return e.ValuePos }
func (e *StringLit) Pos() source.Position    { return e.ValuePos }
func (e *UnaryExpr) Pos() source.Position    { return e.OpPos }
func (e *BinaryExpr) Pos() source.Position   { return e.X.Pos() }
func (e *AssignExpr) Pos() source.Position   { return e.Target.Pos() }
func (e *CondExpr) Pos() source.Position     { return e.Cond.Pos() }
func (e *IndexExpr) Pos() source.Position    { return e.X.Pos() }
func (e *CallExpr) Pos() source.Position     { return e.Fun.Pos() }
func (e *CastExpr) Pos() source.Position     { return e.Lparen }
func (e *InitListExpr) Pos() source.Position { return e.Lbrace }
func (e *BadExpr) Pos() source.Position      { return e.From }

// Pos reports the operand for a postfix increment and the operator for a
// prefix one, so the position always sits at the start of the written form.
func (e *IncDecExpr) Pos() source.Position {
	if e.Postfix {
		return e.X.Pos()
	}
	return e.OpPos
}

func (*Ident) exprNode()        {}
func (*IntLit) exprNode()       {}
func (*CharLit) exprNode()      {}
func (*FloatLit) exprNode()     {}
func (*BoolLit) exprNode()      {}
func (*StringLit) exprNode()    {}
func (*UnaryExpr) exprNode()    {}
func (*IncDecExpr) exprNode()   {}
func (*BinaryExpr) exprNode()   {}
func (*AssignExpr) exprNode()   {}
func (*CondExpr) exprNode()     {}
func (*IndexExpr) exprNode()    {}
func (*CallExpr) exprNode()     {}
func (*CastExpr) exprNode()     {}
func (*InitListExpr) exprNode() {}
func (*BadExpr) exprNode()      {}
