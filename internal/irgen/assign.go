package irgen

import (
	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/sema"
	"tinygo.org/x/go-llvm"
)

func (g *generator) incDec(x *ast.IncDecExpr) llvm.Value {
	slot, ok := g.addressOf(x.X)
	if !ok {
		return g.zero()
	}
	step := int64(1)
	switch x.Op {
	case ast.Inc:
	case ast.Dec:
		step = -1
	default:
		g.errorf(x.OpPos, "the operator '%s' is not lowered", x.Op)
		return g.zero()
	}

	g.setLoc(x.OpPos)
	typ := g.typeOf(x.X)
	old := g.builder.CreateLoad(typ, slot, "")
	var next llvm.Value
	if g.prog.Types[x].Kind() == sema.Pointer {
		next = g.builder.CreateInBoundsGEP(g.elemTypeOf(x.X), old, []llvm.Value{g.constInt(step)}, "")
	} else {
		// Analysis admits a long long, a double, and a pointer here and rejects
		// the rest, and the first two are the same double in a register.
		next = g.builder.CreateFAdd(old, g.constFloat(float64(step)), "")
	}
	g.builder.CreateStore(next, slot)
	if x.Postfix {
		return old
	}
	return next
}

// compoundOps maps each compound assignment to the binary operator it applies.
var compoundOps = map[ast.AssignOp]ast.BinaryOp{
	ast.AddAssign: ast.Add,
	ast.SubAssign: ast.Sub,
	ast.MulAssign: ast.Mul,
	ast.DivAssign: ast.Div,
	ast.ModAssign: ast.Mod,
	ast.ShlAssign: ast.Shl,
	ast.ShrAssign: ast.Shr,
	ast.AndAssign: ast.BitAnd,
	ast.OrAssign:  ast.BitOr,
	ast.XorAssign: ast.BitXor,
}

func (g *generator) assign(x *ast.AssignExpr) llvm.Value {
	slot, ok := g.addressOf(x.Target)
	if !ok {
		g.discard(x.Value)
		return g.zero()
	}
	value := g.value(x.Value)
	if x.Op != ast.Assign {
		value = g.compound(x, slot, value)
		if value.IsNil() {
			return g.zero()
		}
	}
	g.setLoc(x.OpPos)
	g.builder.CreateStore(value, slot)
	return value
}

// compound applies the operator of a compound assignment to the target's
// current value. Only '+=' and '-=' reach a pointer target, where they step it
// by a count of elements the way '+' and '-' do.
func (g *generator) compound(x *ast.AssignExpr, slot, value llvm.Value) llvm.Value {
	op, known := compoundOps[x.Op]
	if !known {
		g.errorf(x.OpPos, "the assignment operator '%s' is not lowered", x.Op)
		return llvm.Value{}
	}
	g.setLoc(x.OpPos)
	old := g.builder.CreateLoad(g.typeOf(x.Target), slot, "")
	if g.prog.Types[x.Target].Kind() == sema.Pointer {
		value = g.toMachineInt(value)
		if op == ast.Sub {
			value = g.builder.CreateNSWNeg(value, "")
		}
		return g.builder.CreateInBoundsGEP(g.elemTypeOf(x.Target), old, []llvm.Value{value}, "")
	}
	if result, bitwise := g.bitwiseOp(op, old, value); bitwise {
		return result
	}
	if old.Type() != g.f64 {
		g.errorf(x.OpPos, "the assignment operator '%s' does not apply to %s", x.Op, g.prog.Types[x.Target])
		return llvm.Value{}
	}
	if g.prog.Types[x.Target].Kind() != sema.Double {
		return g.machineIntOp(op, x.OpPos, old, value)
	}
	build, lowered := floatOps[op]
	if !lowered {
		g.errorf(x.OpPos, "the assignment operator '%s' does not apply to a double", x.Op)
		return llvm.Value{}
	}
	return build(g.builder, old, value, "")
}
