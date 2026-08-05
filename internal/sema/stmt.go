package sema

import (
	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/source"
)

func (c *checker) stmt(s ast.Stmt) {
	switch s := s.(type) {
	case *ast.BlockStmt:
		c.scope.push()
		for _, inner := range s.Stmts {
			c.stmt(inner)
		}
		c.scope.pop()
	case *ast.VarDecl:
		c.varDecl(s, LocalVar)
	case *ast.ExprStmt:
		c.expr(s.X)
	case *ast.IfStmt:
		c.condition(s.Cond, c.expr(s.Cond), "the condition of 'if'")
		c.scopedStmt(s.Then)
		if s.Else != nil {
			c.scopedStmt(s.Else)
		}
	case *ast.WhileStmt:
		c.condition(s.Cond, c.expr(s.Cond), "the condition of 'while'")
		c.loops++
		c.scopedStmt(s.Body)
		c.loops--
	case *ast.DoStmt:
		c.loops++
		c.scopedStmt(s.Body)
		c.loops--
		if s.Cond != nil {
			c.condition(s.Cond, c.expr(s.Cond), "the condition of 'do'")
		}
	case *ast.ForStmt:
		c.forStmt(s)
	case *ast.SwitchStmt:
		c.switchStmt(s)
	case *ast.BreakStmt:
		if c.loops == 0 && c.switches == 0 {
			c.errorf(s.BreakPos, "break is only valid inside a loop or a switch")
		}
	case *ast.ContinueStmt:
		if c.loops == 0 {
			c.errorf(s.ContinuePos, "continue is only valid inside a loop")
		}
	case *ast.ReturnStmt:
		c.returnStmt(s)
	default:
		// An EmptyStmt has nothing to check and a BadStmt was already
		// reported by the parser.
	}
}

// scopedStmt checks a statement that is the body of an if, a loop, or an else.
// A declaration written there is scoped to itself, which is the only thing it
// could mean.
func (c *checker) scopedStmt(s ast.Stmt) {
	if _, isBlock := s.(*ast.BlockStmt); isBlock {
		c.stmt(s)
		return
	}
	c.scope.push()
	c.stmt(s)
	c.scope.pop()
}

// forStmt checks a counted loop. The init clause is scoped to the loop, so a
// variable declared there does not outlive it.
func (c *checker) forStmt(s *ast.ForStmt) {
	c.scope.push()
	if s.Init != nil {
		c.stmt(s.Init)
	}
	if s.Cond != nil {
		c.condition(s.Cond, c.expr(s.Cond), "the condition of 'for'")
	}
	if s.Post != nil {
		c.expr(s.Post)
	}
	c.loops++
	c.scopedStmt(s.Body)
	c.loops--
	c.scope.pop()
}

func (c *checker) returnStmt(s *ast.ReturnStmt) {
	if c.fn == nil {
		return
	}
	result := c.fn.Result
	if s.Result == nil {
		if result.Kind() != Void && result.Kind() != Invalid {
			c.errorf(s.ReturnPos, "'%s' must return a value of type %s", c.fn.Name, result)
		}
		return
	}
	t := c.expr(s.Result)
	if result.Kind() == Void {
		c.errorf(s.Result.Pos(), "'%s' returns void and may not return a value", c.fn.Name)
		return
	}
	c.checkAssignable(result, t, s.Result, "a return from '"+c.fn.Name+"'")
}

// switchStmt checks a switch: the tag, the constancy, type, and distinctness of
// the case labels, and that no arm's body falls into the next.
func (c *checker) switchStmt(s *ast.SwitchStmt) {
	tag := unqual(decay(c.expr(s.Tag)))
	switch tag.Kind() {
	case Int, Bool, Invalid:
	case Double, Dev, Void, Pointer, Array:
		c.errorf(s.Tag.Pos(), "a switch tag must be a %s or a bool, found %s", c.intAs(tag), tag)
		tag = invalidType
	}

	c.switches++
	// The switch body is one scope, however many arms it has.
	c.scope.push()

	labels := make([]*Value, len(s.Cases))
	seen := make(map[int64]source.Position, len(s.Cases))
	var defaultPos source.Position
	for i, clause := range s.Cases {
		if clause.Value == nil {
			if defaultPos.IsValid() {
				c.errorf(clause.CasePos, "a switch may have at most one default label; the earlier one is at %s", defaultPos)
			} else {
				defaultPos = clause.CasePos
			}
		} else {
			labels[i] = c.caseLabel(clause, tag, seen)
		}
		for _, inner := range clause.Body {
			c.stmt(inner)
		}
	}
	c.checkFallthrough(s, labels)

	c.scope.pop()
	c.switches--
}

func (c *checker) caseLabel(clause *ast.CaseClause, tag *Type, seen map[int64]source.Position) *Value {
	t := unqual(c.expr(clause.Value))
	// The label's type is settled before it is folded, so a double written
	// where the tag wants a long long is named as the type mistake it is rather than
	// as a double an integer constant expression may not compute with.
	if tag.Kind() != Invalid && t.Kind() != Invalid && !t.Equal(tag) {
		c.errorf(clause.Value.Pos(), "a case label of a %s switch must be %s, found %s", tag, tag, t)
		return nil
	}
	v, ok := c.requireConst(clause.Value, integerConst, "a case label")
	if !ok {
		return nil
	}
	if prev, dup := seen[v.Int]; dup {
		c.errorf(clause.Value.Pos(), "duplicate case label %s; the earlier one is at %s", v, prev)
		return &v
	}
	seen[v.Int] = clause.Value.Pos()
	return &v
}

// checkFallthrough reports an arm whose body can run into the next one. An arm
// with an empty body stacks its label onto the arm below, which is the only
// fallthrough the language permits; the last arm has nothing below it to fall
// into.
func (c *checker) checkFallthrough(s *ast.SwitchStmt, labels []*Value) {
	if len(s.Cases) == 0 {
		return
	}
	for i, clause := range s.Cases[:len(s.Cases)-1] {
		if len(clause.Body) == 0 || c.terminatesList(clause.Body) {
			continue
		}
		c.errorf(clause.CasePos,
			"control falls out of the body of %s into the next case; an arm must end with break, continue, or return",
			describeClause(clause, labels[i]))
	}
}

func describeClause(clause *ast.CaseClause, label *Value) string {
	switch {
	case clause.Value == nil:
		return "default"
	case label != nil:
		return "case " + label.String()
	default:
		return "this case"
	}
}
