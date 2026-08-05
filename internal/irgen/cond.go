package irgen

import (
	"github.com/greg2010/ic11c/internal/ast"
	"tinygo.org/x/go-llvm"
)

// isNonZero tests a value for its truth, in whichever of the two forms it has.
func (g *generator) isNonZero(v llvm.Value) llvm.Value {
	if v.Type() == g.f64 {
		// Unordered, so a NaN is true: it is not zero, and the machine's own
		// test against zero answers the same way.
		return g.builder.CreateFCmp(llvm.FloatUNE, v, g.constFloat(0), "tobool")
	}
	return g.builder.CreateICmp(llvm.IntNE, v, g.zero(), "tobool")
}

// isZero is the negation of isNonZero, taken directly rather than by complementing it: the machine
// has a comparison for each and no one-bit not. The float form is ordered where isNonZero's is
// unordered, which reads the same pair of answers the other way round — a NaN is not zero, matching
// the machine's own seqz and snez.
func (g *generator) isZero(v llvm.Value) llvm.Value {
	if v.Type() == g.f64 {
		return g.builder.CreateFCmp(llvm.FloatOEQ, v, g.constFloat(0), "lnot")
	}
	return g.builder.CreateICmp(llvm.IntEQ, v, g.zero(), "lnot")
}

// widenTruth reads an i1 as the 0 or 1 a MicroC long long or bool holds. It costs no instruction:
// the machine already holds a truth value as 0 or 1, so instruction selection treats the unsigned
// conversion as the register the comparison landed in.
func (g *generator) widenTruth(cond llvm.Value, name string) llvm.Value {
	return g.builder.CreateUIToFP(cond, g.f64, name)
}

// condExpr lowers '?:' to a branch rather than a select, because MicroC
// evaluates only the arm it takes and an arm may call something. Instruction
// selection turns a select the optimizer later introduces into the machine's
// one-instruction form.
func (g *generator) condExpr(x *ast.CondExpr) llvm.Value {
	then := g.newBlock("cond.then")
	els := g.newBlock("cond.else")
	merge := g.newBlock("cond.end")
	g.condBranch(x.Cond, then, els)

	g.setBlock(then)
	thenValue := g.value(x.Then)
	thenBlock := g.block()
	g.br(merge)

	g.tail(els)
	elseValue := g.value(x.Else)
	elseBlock := g.block()
	g.br(merge)

	g.tail(merge)
	g.setLoc(x.Question)
	phi := g.builder.CreatePHI(g.typeOf(x), "cond")
	phi.AddIncoming([]llvm.Value{thenValue, elseValue}, []llvm.BasicBlock{thenBlock, elseBlock})
	return phi
}

// condBranch lowers x for its truth and branches on it, rather than computing a value and testing
// that. '&&' and '||' become the branch chain they mean, and '!' swaps the two targets: this lets
// instruction selection fuse each comparison with its own branch instead of costing a set
// instruction and a second test. [generator.logical] handles a short-circuit operator read as a value, which still needs a phi.
func (g *generator) condBranch(x ast.Expr, then, els llvm.BasicBlock) {
	if !g.descend(x.Pos()) {
		g.br(els)
		return
	}
	defer g.ascend()
	g.setLoc(x.Pos())
	switch x := x.(type) {
	case *ast.BoolLit:
		if x.Value {
			g.br(then)
		} else {
			g.br(els)
		}
		return
	case *ast.BinaryExpr:
		// Default is the rule: an operator with no branch chain of its own is
		// computed as a value and tested, which is the tail of this function.
		//exhaustive:ignore
		switch x.Op {
		case ast.LogicalAnd:
			rhs := g.chainBlock("land.rhs")
			g.condBranch(x.X, rhs, els)
			g.setBlock(rhs)
			g.condBranch(x.Y, then, els)
			return
		case ast.LogicalOr:
			rhs := g.chainBlock("lor.rhs")
			g.condBranch(x.X, then, rhs)
			g.setBlock(rhs)
			g.condBranch(x.Y, then, els)
			return
		default:
		}
	case *ast.UnaryExpr:
		if x.Op == ast.LogicalNot {
			g.condBranch(x.X, els, then)
			return
		}
	}
	g.condBr(g.cond(x), then, els)
}

// chainBlock creates the block holding the right operand of a short-circuit
// operator and places it immediately after the block that branches into it, so
// that layout order still follows control flow.
func (g *generator) chainBlock(name string) llvm.BasicBlock {
	bb := g.newBlock(name)
	bb.MoveAfter(g.block())
	return bb
}

// cond lowers x for its truth and produces an i1. A comparison becomes the icmp directly rather than
// widened to 0 or 1 and compared against zero again, since instruction selection fuses an icmp whose
// only use is the branch into the machine's one compare-and-branch instruction; the widened form
// would deny it that, costing a second instruction against the byte budget.
func (g *generator) cond(x ast.Expr) llvm.Value {
	if !g.descend(x.Pos()) {
		return llvm.ConstInt(g.i1, 0, false)
	}
	defer g.ascend()
	g.setLoc(x.Pos())
	switch x := x.(type) {
	case *ast.BoolLit:
		if x.Value {
			return llvm.ConstInt(g.i1, 1, false)
		}
		return llvm.ConstInt(g.i1, 0, false)
	case *ast.BinaryExpr:
		// Default is the rule: an operator that produces no i1 of its own is
		// computed as a value and tested against zero, which is the tail of
		// this function.
		//exhaustive:ignore
		switch x.Op {
		case ast.Eq, ast.Ne, ast.Lt, ast.Le, ast.Gt, ast.Ge:
			return g.compare(x)
		case ast.LogicalAnd, ast.LogicalOr:
			return g.logical(x)
		default:
		}
	case *ast.UnaryExpr:
		if x.Op == ast.LogicalNot {
			operand := g.value(x.X)
			g.setLoc(x.OpPos)
			return g.isZero(operand)
		}
	}
	v := g.expr(x)
	g.setLoc(x.Pos())
	return g.isNonZero(v)
}

// logical lowers '&&' and '||', which evaluate their right operand only when
// the left does not decide the result.
func (g *generator) logical(x *ast.BinaryExpr) llvm.Value {
	short := x.Op == ast.LogicalOr
	lhs := g.cond(x.X)
	rhs := g.newBlock("logical.rhs")
	merge := g.newBlock("logical.end")
	lhsBlock := g.block()
	if short {
		g.condBr(lhs, merge, rhs)
	} else {
		g.condBr(lhs, rhs, merge)
	}

	g.setBlock(rhs)
	rhsValue := g.cond(x.Y)
	rhsBlock := g.block()
	g.br(merge)

	g.tail(merge)
	g.setLoc(x.OpPos)
	phi := g.builder.CreatePHI(g.i1, "logical")
	decided := llvm.ConstInt(g.i1, 0, false)
	if short {
		decided = llvm.ConstInt(g.i1, 1, false)
	}
	phi.AddIncoming([]llvm.Value{decided, rhsValue}, []llvm.BasicBlock{lhsBlock, rhsBlock})
	return phi
}
