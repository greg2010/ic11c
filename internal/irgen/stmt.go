package irgen

import (
	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/source"
	"tinygo.org/x/go-llvm"
)

// stmt lowers one statement into the block the builder is positioned in. A statement below a break,
// continue, or return is unreachable and dropped (C admits the source, and writing past a
// terminator would leave a block LLVM refuses to verify); the check is here rather than per
// construct because every block a statement can be written into passes through this function.
func (g *generator) stmt(s ast.Stmt) {
	if s == nil || g.terminated {
		return
	}
	if err := g.ctx.Err(); err != nil {
		g.errorf(s.Pos(), "lowering was cancelled: %v", err)
		return
	}
	if !g.descend(s.Pos()) {
		return
	}
	defer g.ascend()
	g.setLoc(s.Pos())
	switch s := s.(type) {
	case *ast.BlockStmt:
		for _, inner := range s.Stmts {
			g.stmt(inner)
		}
	case *ast.VarDecl:
		g.varDecl(s)
	case *ast.ExprStmt:
		g.discard(s.X)
	case *ast.EmptyStmt:
	case *ast.IfStmt:
		g.ifStmt(s)
	case *ast.WhileStmt:
		g.whileStmt(s)
	case *ast.DoStmt:
		g.doStmt(s)
	case *ast.ForStmt:
		g.forStmt(s)
	case *ast.SwitchStmt:
		g.switchStmt(s)
	case *ast.BreakStmt:
		g.jumpTo(g.breaks, s.Pos(), "break")
	case *ast.ContinueStmt:
		g.jumpTo(g.continues, s.Pos(), "continue")
	case *ast.ReturnStmt:
		g.returnStmt(s)
	case *ast.BadStmt:
		g.errorf(s.Pos(), "the parser could not read this statement")
	default:
		g.errorf(s.Pos(), "statement %T is not lowered", s)
	}
}

func (g *generator) varDecl(d *ast.VarDecl) {
	sym := g.symbols[d]
	if sym == nil {
		// Nothing reads the object, so it needs no storage. The initializer
		// still runs: it may call something. A brace initializer cannot —
		// every element of one is a constant expression.
		if d.Init != nil {
			if _, isList := d.Init.(*ast.InitListExpr); !isList {
				g.discard(d.Init)
			}
		}
		return
	}
	if sym.Value != nil {
		// A const object whose initializer folded needs no storage: every
		// reference to it becomes the constant.
		return
	}
	slot, ok := g.storageOf(sym)
	if !ok {
		g.errorf(d.Pos(), "'%s' has type %s, which is not lowered", d.Name, sym.Type)
		return
	}
	g.setLoc(d.Pos())
	g.zeroUnwritten(sym, slot, d.Init)
	if d.Init == nil {
		return
	}
	g.initialize(sym, slot, d.Init)
}

func (g *generator) ifStmt(s *ast.IfStmt) {
	then := g.newBlock("if.then")
	var els llvm.BasicBlock
	if s.Else != nil {
		els = g.newBlock("if.else")
	}
	merge := g.newBlock("if.end")
	if s.Else == nil {
		els = merge
	}
	g.condBranch(s.Cond, then, els)

	g.setBlock(then)
	g.stmt(s.Then)
	g.br(merge)

	if s.Else != nil {
		g.tail(els)
		g.stmt(s.Else)
		g.br(merge)
	}
	g.tail(merge)
}

func (g *generator) whileStmt(s *ast.WhileStmt) {
	head := g.newBlock("while.cond")
	body := g.newBlock("while.body")
	end := g.newBlock("while.end")

	g.br(head)
	g.setBlock(head)
	g.condBranch(s.Cond, body, end)

	g.setBlock(body)
	g.pushLoop(end, head)
	g.stmt(s.Body)
	g.popLoop()
	g.br(head)

	g.tail(end)
}

func (g *generator) doStmt(s *ast.DoStmt) {
	body := g.newBlock("do.body")
	test := g.newBlock("do.cond")
	end := g.newBlock("do.end")

	g.br(body)
	g.setBlock(body)
	g.pushLoop(end, test)
	g.stmt(s.Body)
	g.popLoop()
	g.br(test)

	g.tail(test)
	g.condBranch(s.Cond, body, end)

	g.tail(end)
}

func (g *generator) forStmt(s *ast.ForStmt) {
	g.stmt(s.Init)

	head := g.newBlock("for.cond")
	body := g.newBlock("for.body")
	post := g.newBlock("for.post")
	end := g.newBlock("for.end")

	g.br(head)
	g.setBlock(head)
	if s.Cond != nil {
		g.condBranch(s.Cond, body, end)
	} else {
		g.br(body)
	}

	g.setBlock(body)
	g.pushLoop(end, post)
	g.stmt(s.Body)
	g.popLoop()
	g.br(post)

	g.tail(post)
	if s.Post != nil {
		g.setLoc(s.Post.Pos())
		g.discard(s.Post)
	}
	g.br(head)

	g.tail(end)
}

// switchStmt lowers the dispatch and the arms of one switch. The dispatch is equality tests over the
// double the register holds ([generator.dispatchOnDoubles] explains why not an LLVM switch). An arm
// with an empty body stacks its label onto the arm below — the only fallthrough MicroC permits — so
// several case values can reach one block.
func (g *generator) switchStmt(s *ast.SwitchStmt) {
	tag := g.value(s.Tag)
	end := g.newBlock("switch.end")

	blocks := make([]llvm.BasicBlock, len(s.Cases))
	for i, arm := range s.Cases {
		if len(arm.Body) == 0 {
			continue
		}
		blocks[i] = g.newBlock("switch.case")
	}
	// An empty arm resolves to the first arm below it with a body, and to the
	// exit when none does.
	target := func(i int) llvm.BasicBlock {
		for ; i < len(s.Cases); i++ {
			if len(s.Cases[i].Body) != 0 {
				return blocks[i]
			}
		}
		return end
	}

	// Analysis rejects a second default arm, so the first one found is the one
	// the switch has; a switch with none falls straight to the exit.
	deflt := end
	for i, arm := range s.Cases {
		if arm.Value == nil {
			deflt = target(i)
			break
		}
	}

	g.setLoc(s.Pos())
	g.dispatchOnDoubles(s, tag, target, deflt)

	g.pushBreak(end)
	for i, arm := range s.Cases {
		if len(arm.Body) == 0 {
			continue
		}
		g.tail(blocks[i])
		for _, inner := range arm.Body {
			g.stmt(inner)
		}
		g.br(end)
	}
	g.popBreak()

	g.tail(end)
}

// dispatchOnDoubles branches to the arm one case label selects, testing the tag as the double the
// register holds rather than converting to an i64 for an LLVM switch: that conversion would state
// the tag as one of finitely many integers, licensing a rewrite that folds nearby labels into a
// bitwise mask — and the machine's bitwise conversion faults on an infinity where seq does not, so a
// tag the source only ever compared could stop the chip. The comparison is ordered, so a NaN matches
// no label and reaches the default arm, which is the machine's own answer: beq is false for a NaN operand.
func (g *generator) dispatchOnDoubles(s *ast.SwitchStmt, tag llvm.Value, target func(int) llvm.BasicBlock, deflt llvm.BasicBlock) {
	for i, arm := range s.Cases {
		label, ok := g.caseLabel(arm)
		if !ok {
			continue
		}
		hit := g.builder.CreateFCmp(llvm.FloatOEQ, tag, g.constFloat(float64(label)), "")
		next := g.newBlock("switch.test")
		g.condBr(hit, target(i), next)
		g.setBlock(next)
	}
	g.br(deflt)
}

// caseLabel reads the constant one arm is labelled with, and reports false for
// the default arm and for a label analysis did not fold.
func (g *generator) caseLabel(arm *ast.CaseClause) (int64, bool) {
	if arm.Value == nil {
		return 0, false
	}
	value, ok := g.prog.Consts[arm.Value]
	if !ok {
		g.errorf(arm.Value.Pos(), "the case label did not fold to a constant")
		return 0, false
	}
	return value.Int, true
}

func (g *generator) returnStmt(s *ast.ReturnStmt) {
	fr := g.frames[len(g.frames)-1]
	if s.Result != nil {
		value := g.value(s.Result)
		if !fr.retSlot.IsNil() {
			g.setLoc(s.Pos())
			g.builder.CreateStore(value, fr.retSlot)
		}
	}
	g.br(fr.retBlock)
}

func (g *generator) jumpTo(stack []llvm.BasicBlock, pos source.Position, what string) {
	if len(stack) == 0 {
		g.errorf(pos, "'%s' is not inside a construct it can leave", what)
		return
	}
	g.br(stack[len(stack)-1])
}

func (g *generator) pushLoop(brk, cont llvm.BasicBlock) {
	g.breaks = append(g.breaks, brk)
	g.continues = append(g.continues, cont)
}

func (g *generator) popLoop() {
	g.breaks = g.breaks[:len(g.breaks)-1]
	g.continues = g.continues[:len(g.continues)-1]
}

// pushBreak enters a switch, which break leaves and continue passes through: a
// continue inside a switch continues the enclosing loop.
func (g *generator) pushBreak(brk llvm.BasicBlock) { g.breaks = append(g.breaks, brk) }

func (g *generator) popBreak() { g.breaks = g.breaks[:len(g.breaks)-1] }
