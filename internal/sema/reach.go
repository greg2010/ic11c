package sema

import (
	"slices"

	"github.com/greg2010/ic11c/internal/ast"
)

// terminates reports whether control cannot reach the end of s: whether a
// switch arm falls into the next one, or a function reaches its end without
// returning a value it owes.
func (c *checker) terminates(s ast.Stmt) bool {
	if got, asked := c.terminated[s]; asked {
		return got
	}
	trapped := c.stmtTerminates(s)
	c.terminated[s] = trapped
	return trapped
}

func (c *checker) stmtTerminates(s ast.Stmt) bool {
	switch s := s.(type) {
	case *ast.ReturnStmt, *ast.BreakStmt, *ast.ContinueStmt:
		return true
	case *ast.BlockStmt:
		return c.terminatesList(s.Stmts)
	case *ast.IfStmt:
		return s.Else != nil && c.terminates(s.Then) && c.terminates(s.Else)
	case *ast.WhileStmt:
		return c.isAlwaysTrue(s.Cond) && !jumpsOut(s.Body, breakJump)
	case *ast.ForStmt:
		return (s.Cond == nil || c.isAlwaysTrue(s.Cond)) && !jumpsOut(s.Body, breakJump)
	case *ast.DoStmt:
		if jumpsOut(s.Body, breakJump) {
			return false
		}
		// A continue reaches the condition, which may then be false, so a body
		// that only terminates by continuing does not trap control.
		return c.isAlwaysTrue(s.Cond) || (c.terminates(s.Body) && !jumpsOut(s.Body, continueJump))
	case *ast.SwitchStmt:
		return c.switchTerminates(s)
	default:
		return false
	}
}

// terminatesList reports whether control cannot reach the end of a statement
// list. A statement after a terminating one is unreachable, so any terminating
// statement in the list settles it.
func (c *checker) terminatesList(list []ast.Stmt) bool {
	return slices.ContainsFunc(list, c.terminates)
}

// switchTerminates reports whether control cannot reach the end of a switch. It
// needs a default arm, no break bound to the switch itself, and a last arm with
// a body, since an empty arm stacks onto the one below and the last one has
// nothing below it.
func (c *checker) switchTerminates(s *ast.SwitchStmt) bool {
	if len(s.Cases) == 0 {
		return false
	}
	hasDefault := false
	for _, clause := range s.Cases {
		if clause.Value == nil {
			hasDefault = true
		}
		if jumpsOutList(clause.Body, breakJump) {
			return false
		}
		if len(clause.Body) > 0 && !c.terminatesList(clause.Body) {
			return false
		}
	}
	return hasDefault && len(s.Cases[len(s.Cases)-1].Body) > 0
}

// isAlwaysTrue reports whether a controlling expression is a constant the loop
// can never leave on. The truth of the constant is read through Num rather than
// off Int, which carries nothing for a double.
func (c *checker) isAlwaysTrue(x ast.Expr) bool {
	if x == nil {
		return true
	}
	v, fail := c.constEval(x, arithmeticConst)
	return fail == nil && v.Num() != 0
}

// jump names the two statements that leave a construct from inside it. Which
// construct each binds to is what a search for one has to know: a loop binds
// both, and a switch binds a break alone.
type jump uint8

const (
	breakJump jump = iota
	continueJump
)

// jumpsOut reports whether s contains a jump of the given kind bound to the
// construct s is the body of. The search stops where the jump would bind to
// something nested instead: at any loop, and at a switch when the jump is a
// break.
func jumpsOut(s ast.Stmt, want jump) bool {
	switch s := s.(type) {
	case *ast.BreakStmt:
		return want == breakJump
	case *ast.ContinueStmt:
		return want == continueJump
	case *ast.BlockStmt:
		return jumpsOutList(s.Stmts, want)
	case *ast.IfStmt:
		return jumpsOut(s.Then, want) || (s.Else != nil && jumpsOut(s.Else, want))
	case *ast.SwitchStmt:
		if want == breakJump {
			return false
		}
		for _, clause := range s.Cases {
			if jumpsOutList(clause.Body, want) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func jumpsOutList(list []ast.Stmt, want jump) bool {
	for _, s := range list {
		if jumpsOut(s, want) {
			return true
		}
	}
	return false
}
