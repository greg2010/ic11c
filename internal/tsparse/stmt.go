package tsparse

import (
	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/tsnode"
	ts "github.com/tree-sitter/go-tree-sitter"
)

// stmtConverters dispatches on the kind of a statement node. It is assigned in
// init because every converter in it reaches back through [converter.stmt],
// which reads the table.
var stmtConverters map[tsnode.Kind]func(*converter, *ts.Node) ast.Stmt

func init() {
	stmtConverters = map[tsnode.Kind]func(*converter, *ts.Node) ast.Stmt{
		tsnode.KindBreakStatement:      (*converter).breakStmt,
		tsnode.KindCaseStatement:       (*converter).strayCase,
		tsnode.KindCompoundStatement:   func(c *converter, n *ts.Node) ast.Stmt { return c.block(n) },
		tsnode.KindContinueStatement:   (*converter).continueStmt,
		tsnode.KindDeclaration:         (*converter).declStmt,
		tsnode.KindDoStatement:         (*converter).doStmt,
		tsnode.KindExpressionStatement: (*converter).exprStmt,
		tsnode.KindForStatement:        (*converter).forStmt,
		tsnode.KindIfStatement:         (*converter).ifStmt,
		tsnode.KindReturnStatement:     (*converter).returnStmt,
		tsnode.KindSwitchStatement:     (*converter).switchStmt,
		tsnode.KindWhileStatement:      (*converter).whileStmt,
	}
}

// stmt converts one statement. A node the grammar could not read, or one
// spelling a construct MicroC excludes, becomes a [ast.BadStmt] so the
// statements after it keep their positions.
func (c *converter) stmt(n *ts.Node) ast.Stmt {
	if bad(n) {
		return &ast.BadStmt{From: c.start(n)}
	}
	if inner, wrote := c.unwrapped(n); wrote {
		return c.stmt(inner)
	}
	// The grammar reads "a(b[])" as a name standing for a type, which is how a
	// library spells a macro. MicroC has neither a typedef nor a preprocessor,
	// so where a statement belongs it is the call it was written as, exactly as
	// "a * b" is a product. See misread.go.
	if tsnode.Kind(n.Kind()) == tsnode.KindMacroTypeSpecifier {
		return c.misreadCallStmt(n)
	}
	convert, known := stmtConverters[tsnode.Kind(n.Kind())]
	if !known {
		c.refuseAsStatement(n)
		return &ast.BadStmt{From: c.start(n)}
	}
	return convert(c, n)
}

// block converts a brace-enclosed statement list.
func (c *converter) block(n *ts.Node) *ast.BlockStmt {
	block := &ast.BlockStmt{Lbrace: c.start(n)}
	if rbrace, closed := c.anonymous(n, tsnode.KindRbrace); closed {
		block.Rbrace = rbrace
	}
	for _, ch := range c.children(n) {
		// A ';' the grammar could not attach to the construct in front of it
		// stands loose here; it terminates that construct and has already been
		// reported on.
		if ch.kind == tsnode.KindLbrace || ch.kind == tsnode.KindRbrace || ch.kind == tsnode.KindSemi {
			continue
		}
		block.Stmts = append(block.Stmts, c.stmt(ch.node))
	}
	return block
}

// declStmt converts a declaration written where a statement is expected, which
// MicroC admits for variables alone. A declaration that did not convert is
// left to whatever refused it, which has already cost a diagnostic.
func (c *converter) declStmt(n *ts.Node) ast.Stmt {
	if c.misreadDeclaration(n) {
		return c.misreadStmt(n)
	}
	switch decl := c.declaration(n).(type) {
	case ast.Stmt:
		return decl
	case *ast.FuncDecl:
		// A definition inside a block is spelled as its own construct and never
		// reaches here, so what did is a declaration with no body.
		c.errorf(c.start(n), "%s", blockPrototypeMsg)
	}
	return &ast.BadStmt{From: c.start(n)}
}

// exprStmt converts an expression evaluated for its effect. The grammar spells
// a lone semicolon the same way, with no expression in it.
func (c *converter) exprStmt(n *ts.Node) ast.Stmt {
	for _, ch := range c.children(n) {
		if ch.kind == tsnode.KindSemi {
			continue
		}
		return &ast.ExprStmt{X: c.expr(ch.node)}
	}
	return &ast.EmptyStmt{Semi: c.start(n)}
}

func (c *converter) breakStmt(n *ts.Node) ast.Stmt {
	return &ast.BreakStmt{BreakPos: c.start(n)}
}

func (c *converter) continueStmt(n *ts.Node) ast.Stmt {
	return &ast.ContinueStmt{ContinuePos: c.start(n)}
}

func (c *converter) returnStmt(n *ts.Node) ast.Stmt {
	stmt := &ast.ReturnStmt{ReturnPos: c.start(n)}
	for _, ch := range c.children(n) {
		if ch.kind == tsnode.KindReturn || ch.kind == tsnode.KindSemi {
			continue
		}
		stmt.Result = c.expr(ch.node)
	}
	return stmt
}

// condition unwraps the parenthesized head of a control statement: the
// condition of if, while and do, and the tag of switch. A head the source does
// not have stands at the control statement's own position, the only place a
// reader can be sent when what is missing has no position of its own.
func (c *converter) condition(n *ts.Node, slot tsnode.Field) ast.Expr {
	head := field(n, slot)
	if head == nil {
		return &ast.BadExpr{From: c.start(n)}
	}
	if bad(head) {
		return &ast.BadExpr{From: c.start(head)}
	}
	return c.parenExpr(head)
}

func (c *converter) ifStmt(n *ts.Node) ast.Stmt {
	stmt := &ast.IfStmt{
		IfPos: c.start(n),
		Cond:  c.condition(n, tsnode.FieldCondition),
		Then:  c.stmt(field(n, tsnode.FieldConsequence)),
	}
	if alternative := field(n, tsnode.FieldAlternative); alternative != nil {
		stmt.Else = c.elseClause(alternative)
	}
	return stmt
}

func (c *converter) elseClause(n *ts.Node) ast.Stmt {
	for _, ch := range c.children(n) {
		if ch.kind == tsnode.KindElse {
			continue
		}
		return c.stmt(ch.node)
	}
	c.errorf(c.start(n), "expected a statement after 'else'")
	return &ast.BadStmt{From: c.start(n)}
}

func (c *converter) whileStmt(n *ts.Node) ast.Stmt {
	return &ast.WhileStmt{
		WhilePos: c.start(n),
		Cond:     c.condition(n, tsnode.FieldCondition),
		Body:     c.stmt(field(n, tsnode.FieldBody)),
	}
}

func (c *converter) doStmt(n *ts.Node) ast.Stmt {
	return &ast.DoStmt{
		DoPos: c.start(n),
		Body:  c.stmt(field(n, tsnode.FieldBody)),
		Cond:  c.condition(n, tsnode.FieldCondition),
	}
}

func (c *converter) forStmt(n *ts.Node) ast.Stmt {
	stmt := &ast.ForStmt{ForPos: c.start(n), Body: c.stmt(field(n, tsnode.FieldBody))}
	if init := field(n, tsnode.FieldInitializer); init != nil {
		// The grammar gives the head an expression where the language gives it
		// a statement, so the two forms an initializer takes converge here.
		if tsnode.Kind(init.Kind()) == tsnode.KindDeclaration {
			stmt.Init = c.declStmt(init)
		} else {
			stmt.Init = &ast.ExprStmt{X: c.expr(init)}
		}
	}
	if cond := field(n, tsnode.FieldCondition); cond != nil {
		stmt.Cond = c.expr(cond)
	}
	if post := field(n, tsnode.FieldUpdate); post != nil {
		stmt.Post = c.expr(post)
	}
	return stmt
}

// switchStmt converts a switch. The grammar nests each arm's statements inside
// the label that introduces them, which is the shape the tree wants; a
// statement before the first label belongs to no arm and is refused.
func (c *converter) switchStmt(n *ts.Node) ast.Stmt {
	stmt := &ast.SwitchStmt{SwitchPos: c.start(n), Tag: c.condition(n, tsnode.FieldCondition)}
	body := field(n, tsnode.FieldBody)
	if body == nil {
		return stmt
	}
	stmt.Lbrace = c.start(body)
	for _, ch := range c.children(body) {
		// Default is the rule: tsnode.Kind is the grammar's whole alphabet, and
		// what a construct is written with is a handful of it.
		//exhaustive:ignore
		switch ch.kind {
		case tsnode.KindLbrace, tsnode.KindRbrace:
		case tsnode.KindCaseStatement:
			stmt.Cases = append(stmt.Cases, c.caseClause(ch.node))
		default:
			c.errorf(c.start(ch.node), "a statement in a switch body must follow a case or default label")
		}
	}
	return stmt
}

func (c *converter) caseClause(n *ts.Node) *ast.CaseClause {
	clause := &ast.CaseClause{CasePos: c.start(n)}
	value := field(n, tsnode.FieldValue)
	if value != nil {
		clause.Value = c.expr(value)
	}
	for _, ch := range c.children(n) {
		switch {
		case ch.field == tsnode.FieldValue:
		case ch.kind == tsnode.KindCase || ch.kind == tsnode.KindDefault || ch.kind == tsnode.KindColon:
		default:
			clause.Body = append(clause.Body, c.stmt(ch.node))
		}
	}
	return clause
}

// strayCase reports a case label outside any switch, which the grammar admits
// wherever a statement is admitted.
func (c *converter) strayCase(n *ts.Node) ast.Stmt {
	c.errorf(c.start(n), "a case or default label is only valid inside a switch")
	return &ast.BadStmt{From: c.start(n)}
}
