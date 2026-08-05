package sema

import (
	"github.com/greg2010/ic11c/internal/ast"
)

// checkDefiniteAssignment reports every read of a tracked local that is not
// assigned on all paths reaching it. Nothing on the chip zeroes a register,
// so an unassigned read folds through the optimizer as undef, which can
// silently delete the code it feeds rather than fault.
func (c *checker) checkDefiniteAssignment() {
	if c.diags.HasErrors() {
		return
	}
	// A declaration records the name it wrote as text, so the symbol it
	// introduced is reached through the uses of that name. A local nothing
	// reads is absent, which is exactly the local no read can be reported
	// against.
	decls := make(map[ast.Node]*Symbol, len(c.prog.Uses))
	for _, sym := range c.prog.Uses {
		if sym.Decl != nil {
			decls[sym.Decl] = sym
		}
	}
	for _, fn := range c.prog.Funcs {
		if fn.Decl == nil || fn.Decl.Body == nil {
			continue
		}
		a := &assigner{c: c, decls: decls}
		a.tracked = a.trackedLocals(fn.Decl)
		if len(a.tracked) == 0 {
			continue
		}
		a.block(newFlow(), fn.Decl.Body.Stmts)
	}
}

// trackedLocals collects the locals of one definition that live in a
// register, which are the ones nothing zeroes. Whether a local's address was
// taken is read off the symbol rather than walked for here, since the address
// can be taken above the line that declares the local.
func (a *assigner) trackedLocals(d *ast.FuncDecl) map[*Symbol]bool {
	declared := make(map[*Symbol]bool)
	a.walkStmts(d.Body.Stmts, func(n ast.Node) {
		if decl, isVar := n.(*ast.VarDecl); isVar {
			if sym := a.declaredLocal(decl); sym != nil && registerClass(sym) {
				declared[sym] = true
			}
		}
	})
	return declared
}

// walkStmts calls visit on every node of a statement list. It exists for the
// one prepass above; every other walk in this file threads a flow state and so
// has to know the shape of what it is visiting.
func (a *assigner) walkStmts(stmts []ast.Stmt, visit func(ast.Node)) {
	for _, s := range stmts {
		a.walkStmt(s, visit)
	}
}

func (a *assigner) walkStmt(s ast.Stmt, visit func(ast.Node)) {
	if s == nil {
		return
	}
	visit(s)
	switch s := s.(type) {
	case *ast.BlockStmt:
		a.walkStmts(s.Stmts, visit)
	case *ast.VarDecl:
		a.walkExpr(s.Init, visit)
	case *ast.ExprStmt:
		a.walkExpr(s.X, visit)
	case *ast.IfStmt:
		a.walkExpr(s.Cond, visit)
		a.walkStmt(s.Then, visit)
		a.walkStmt(s.Else, visit)
	case *ast.WhileStmt:
		a.walkExpr(s.Cond, visit)
		a.walkStmt(s.Body, visit)
	case *ast.DoStmt:
		a.walkStmt(s.Body, visit)
		a.walkExpr(s.Cond, visit)
	case *ast.ForStmt:
		a.walkStmt(s.Init, visit)
		a.walkExpr(s.Cond, visit)
		a.walkExpr(s.Post, visit)
		a.walkStmt(s.Body, visit)
	case *ast.SwitchStmt:
		a.walkExpr(s.Tag, visit)
		for _, clause := range s.Cases {
			a.walkExpr(clause.Value, visit)
			a.walkStmts(clause.Body, visit)
		}
	case *ast.ReturnStmt:
		a.walkExpr(s.Result, visit)
	default:
	}
}

func (a *assigner) walkExpr(x ast.Expr, visit func(ast.Node)) {
	if x == nil {
		return
	}
	visit(x)
	switch x := x.(type) {
	case *ast.UnaryExpr:
		a.walkExpr(x.X, visit)
	case *ast.IncDecExpr:
		a.walkExpr(x.X, visit)
	case *ast.BinaryExpr:
		a.walkExpr(x.X, visit)
		a.walkExpr(x.Y, visit)
	case *ast.AssignExpr:
		a.walkExpr(x.Target, visit)
		a.walkExpr(x.Value, visit)
	case *ast.CondExpr:
		a.walkExpr(x.Cond, visit)
		a.walkExpr(x.Then, visit)
		a.walkExpr(x.Else, visit)
	case *ast.IndexExpr:
		a.walkExpr(x.X, visit)
		a.walkExpr(x.Index, visit)
	case *ast.CallExpr:
		a.walkExpr(x.Fun, visit)
		for _, arg := range x.Args {
			a.walkExpr(arg, visit)
		}
	case *ast.CastExpr:
		a.walkExpr(x.X, visit)
	case *ast.InitListExpr:
		for _, elem := range x.Elems {
			a.walkExpr(elem, visit)
		}
	default:
	}
}

// registerClass reports whether a symbol is a local the optimizer can hold in
// a register — the only locals this file tracks, since a global, an array, or
// an addressed local is zeroed by the entry prologue instead. A const object
// is excluded too: its initializer is required, so no read can precede a write.
func registerClass(sym *Symbol) bool {
	if sym.Kind != LocalVar || sym.Type.IsConst() || sym.InDataRegion() {
		return false
	}
	switch sym.Type.Kind() {
	case Int, Bool, Double, Pointer:
		return true
	case Invalid, Dev, Void, Array:
	}
	return false
}

func (a *assigner) declaredLocal(d *ast.VarDecl) *Symbol { return a.decls[d] }

func (a *assigner) symbolOf(x ast.Expr) *Symbol {
	if id, ok := x.(*ast.Ident); ok {
		return a.c.prog.Uses[id]
	}
	return nil
}

// flow is the state at one point in a function: whether control reaches the
// point, and which tracked locals are assigned there.
type flow struct {
	live bool
	// assigned is nil for a state control cannot reach, where every question
	// about it is vacuous.
	assigned map[*Symbol]bool
}

func newFlow() flow { return flow{live: true, assigned: make(map[*Symbol]bool)} }

func deadFlow() flow { return flow{} }

func (f flow) clone() flow {
	if !f.live {
		return f
	}
	assigned := make(map[*Symbol]bool, len(f.assigned))
	for sym := range f.assigned {
		assigned[sym] = true
	}
	return flow{live: true, assigned: assigned}
}

// merge joins the states of two paths that meet. A local is assigned after the
// join only when both paths assigned it, and a path control cannot reach
// contributes nothing.
func merge(a, b flow) flow {
	switch {
	case !a.live:
		return b
	case !b.live:
		return a
	}
	joined := flow{live: true, assigned: make(map[*Symbol]bool)}
	for sym := range a.assigned {
		if b.assigned[sym] {
			joined.assigned[sym] = true
		}
	}
	return joined
}

// assigner walks one function body carrying the flow state.
type assigner struct {
	c *checker
	// decls indexes every symbol by the declaration that introduced it, since a
	// declaration node records only the name it wrote.
	decls   map[ast.Node]*Symbol
	tracked map[*Symbol]bool
	// jumps holds one frame per enclosing loop or switch: a break leaves the
	// innermost of either, and a continue the innermost loop.
	jumps []*jumpFrame
}

// jumpFrame collects the states at the jumps bound to one loop or switch.
type jumpFrame struct {
	loop      bool
	breaks    []flow
	continues []flow
}

func (a *assigner) push(loop bool) *jumpFrame {
	frame := &jumpFrame{loop: loop}
	a.jumps = append(a.jumps, frame)
	return frame
}

func (a *assigner) pop() { a.jumps = a.jumps[:len(a.jumps)-1] }

// breakFrame is the loop or switch a break leaves, and continueFrame the loop a
// continue restarts. Both are nil only in a program analysis already rejected.
func (a *assigner) breakFrame() *jumpFrame {
	if len(a.jumps) == 0 {
		return nil
	}
	return a.jumps[len(a.jumps)-1]
}

func (a *assigner) continueFrame() *jumpFrame {
	for i := len(a.jumps) - 1; i >= 0; i-- {
		if a.jumps[i].loop {
			return a.jumps[i]
		}
	}
	return nil
}

func (a *assigner) block(f flow, stmts []ast.Stmt) flow {
	for _, s := range stmts {
		f = a.stmt(f, s)
	}
	return f
}

func (a *assigner) stmt(f flow, s ast.Stmt) flow {
	switch s := s.(type) {
	case *ast.BlockStmt:
		return a.block(f, s.Stmts)
	case *ast.VarDecl:
		return a.varDecl(f, s)
	case *ast.ExprStmt:
		return a.expr(f, s.X)
	case *ast.IfStmt:
		return a.ifStmt(f, s)
	case *ast.WhileStmt:
		return a.whileStmt(f, s)
	case *ast.DoStmt:
		return a.doStmt(f, s)
	case *ast.ForStmt:
		return a.forStmt(f, s)
	case *ast.SwitchStmt:
		return a.switchStmt(f, s)
	case *ast.BreakStmt:
		if frame := a.breakFrame(); frame != nil {
			frame.breaks = append(frame.breaks, f)
		}
		return deadFlow()
	case *ast.ContinueStmt:
		if frame := a.continueFrame(); frame != nil {
			frame.continues = append(frame.continues, f)
		}
		return deadFlow()
	case *ast.ReturnStmt:
		if s.Result != nil {
			// Walked for the reads in it. What it assigns reaches nothing:
			// control leaves the function here.
			a.expr(f, s.Result)
		}
		return deadFlow()
	default:
		// An EmptyStmt does nothing and a BadStmt is source the parser could
		// not read, which is reported there.
		return f
	}
}

func (a *assigner) varDecl(f flow, d *ast.VarDecl) flow {
	sym := a.declaredLocal(d)
	if d.Init != nil {
		f = a.expr(f, d.Init)
	}
	if !f.live || sym == nil || !a.tracked[sym] {
		return f
	}
	if d.Init != nil {
		f.assigned[sym] = true
		return f
	}
	// A declaration without an initializer does not write the object. It also
	// shadows any outer name, so anything the enclosing scope knew about a
	// symbol of the same name is irrelevant: this is a different symbol.
	delete(f.assigned, sym)
	return f
}

func (a *assigner) ifStmt(f flow, s *ast.IfStmt) flow {
	f = a.expr(f, s.Cond)
	then := a.stmt(f.clone(), s.Then)
	if s.Else == nil {
		return merge(then, f)
	}
	return merge(then, a.stmt(f, s.Else))
}

// whileStmt tests before the body, so the state after the loop is the state
// after the condition: an iteration may not happen at all, and one that did
// cannot be relied on. A loop whose condition is always true is left only by a
// break, and then only with what every break carried.
func (a *assigner) whileStmt(f flow, s *ast.WhileStmt) flow {
	f = a.expr(f, s.Cond)
	frame := a.push(true)
	a.stmt(f.clone(), s.Body)
	a.pop()
	if a.c.isAlwaysTrue(s.Cond) {
		return mergeAll(frame.breaks)
	}
	return f
}

// doStmt runs its body before testing, so the body's assignments do reach the
// condition and the statement after the loop. A continue reaches the condition
// too, and a break skips it.
func (a *assigner) doStmt(f flow, s *ast.DoStmt) flow {
	frame := a.push(true)
	end := a.stmt(f, s.Body)
	a.pop()
	tested := mergeAll(append(frame.continues, end))
	if s.Cond != nil {
		tested = a.expr(tested, s.Cond)
	}
	if s.Cond == nil || a.c.isAlwaysTrue(s.Cond) {
		return mergeAll(frame.breaks)
	}
	return mergeAll(append(frame.breaks, tested))
}

// forStmt evaluates its init clause once and its condition before every
// iteration. The post clause runs after the body, so nothing it assigns is
// visible to the statement after the loop.
func (a *assigner) forStmt(f flow, s *ast.ForStmt) flow {
	if s.Init != nil {
		f = a.stmt(f, s.Init)
	}
	if s.Cond != nil {
		f = a.expr(f, s.Cond)
	}
	frame := a.push(true)
	body := a.stmt(f.clone(), s.Body)
	if s.Post != nil {
		a.expr(mergeAll(append(frame.continues, body)), s.Post)
	}
	a.pop()
	if s.Cond == nil || a.c.isAlwaysTrue(s.Cond) {
		return mergeAll(frame.breaks)
	}
	return f
}

// switchStmt runs one arm. Every arm starts from the state after the tag, and
// an arm that runs off its own body leaves the switch, since the language
// admits no fallthrough out of a non-empty arm. Without a default arm the tag
// may match nothing, so the state after the switch is the state before it.
func (a *assigner) switchStmt(f flow, s *ast.SwitchStmt) flow {
	f = a.expr(f, s.Tag)
	frame := a.push(false)
	exits := make([]flow, 0, len(s.Cases))
	hasDefault := false
	for _, clause := range s.Cases {
		if clause.Value == nil {
			hasDefault = true
		} else {
			f = a.expr(f, clause.Value)
		}
		// An empty arm stacks its label onto the arm below, which is the one
		// fallthrough the language permits, so it contributes no exit of its
		// own.
		if len(clause.Body) == 0 {
			continue
		}
		exits = append(exits, a.block(f.clone(), clause.Body))
	}
	a.pop()
	if !hasDefault {
		return f
	}
	return mergeAll(append(frame.breaks, exits...))
}

// mergeAll joins every state in a set of paths that meet. An empty set is a
// point control cannot reach.
func mergeAll(paths []flow) flow {
	joined := deadFlow()
	for _, path := range paths {
		joined = merge(joined, path)
	}
	return joined
}

func (a *assigner) expr(f flow, x ast.Expr) flow {
	switch x := x.(type) {
	case nil:
		return f
	case *ast.Ident:
		return a.read(f, x)
	case *ast.UnaryExpr:
		// '&' designates its operand rather than reading it, and taking the
		// address of a local is what untracks it, so naming one here reports
		// nothing on its own. What selects the object is read like any other
		// operand: '&p[i]' is the arithmetic 'p + i' and '&*p' the pointer p.
		return a.expr(f, x.X)
	case *ast.IncDecExpr:
		f = a.expr(f, x.X)
		return a.write(f, x.X)
	case *ast.BinaryExpr:
		return a.binary(f, x)
	case *ast.AssignExpr:
		return a.assign(f, x)
	case *ast.CondExpr:
		f = a.expr(f, x.Cond)
		return merge(a.expr(f.clone(), x.Then), a.expr(f, x.Else))
	case *ast.IndexExpr:
		return a.expr(a.expr(f, x.X), x.Index)
	case *ast.CallExpr:
		// Fun names a function or an intrinsic, and a named intrinsic operand
		// is a device pin or a table name. None resolves to a variable, so the
		// general walk reads none of them.
		f = a.expr(f, x.Fun)
		for _, arg := range x.Args {
			f = a.expr(f, arg)
		}
		return f
	case *ast.CastExpr:
		return a.expr(f, x.X)
	case *ast.InitListExpr:
		for _, elem := range x.Elems {
			f = a.expr(f, elem)
		}
		return f
	default:
		// A literal reads nothing, and a BadExpr is source the parser could not
		// read.
		return f
	}
}

// binary walks an infix expression. The right operand of a short-circuit
// operator may not be evaluated, so what it assigns does not survive the
// expression; what it reads is still checked, against the state the operator
// would reach it with.
func (a *assigner) binary(f flow, x *ast.BinaryExpr) flow {
	f = a.expr(f, x.X)
	// Default is the rule: every operator but these two evaluates its right
	// operand unconditionally.
	//exhaustive:ignore
	switch x.Op {
	case ast.LogicalAnd, ast.LogicalOr:
		a.expr(f.clone(), x.Y)
		return f
	default:
		return a.expr(f, x.Y)
	}
}

// assign walks an assignment. A compound assignment reads its target before
// writing it; a plain one does not read it at all, so a first assignment
// through '=' is what makes a local defined.
func (a *assigner) assign(f flow, x *ast.AssignExpr) flow {
	f = a.expr(f, x.Value)
	if x.Op == ast.Assign {
		if _, direct := x.Target.(*ast.Ident); !direct {
			// A subscript or a dereference names storage in the data region;
			// the expressions selecting it are ordinary reads.
			f = a.expr(f, x.Target)
		}
		return a.write(f, x.Target)
	}
	f = a.expr(f, x.Target)
	return a.write(f, x.Target)
}

// read reports a use of a tracked local that is not assigned on every path
// reaching it, and then treats it as assigned so that one uninitialized
// variable produces one diagnostic rather than one per mention.
func (a *assigner) read(f flow, id *ast.Ident) flow {
	sym := a.c.prog.Uses[id]
	if !f.live || sym == nil || !a.tracked[sym] || f.assigned[sym] {
		return f
	}
	a.c.errorf(id.NamePos, "'%s' is read here without having been assigned on every path that reaches this point, and it lives in a register, which nothing zeroes; give the declaration at %s an initializer, or assign it in every branch before this one", sym.Name, sym.Pos)
	f.assigned[sym] = true
	return f
}

func (a *assigner) write(f flow, target ast.Expr) flow {
	sym := a.symbolOf(target)
	if !f.live || sym == nil || !a.tracked[sym] {
		return f
	}
	f.assigned[sym] = true
	return f
}
