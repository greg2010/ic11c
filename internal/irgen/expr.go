package irgen

import (
	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/sema"
	"github.com/greg2010/ic11c/internal/source"
	"tinygo.org/x/go-llvm"
)

// value lowers x and applies whatever conversion analysis recorded for it. A call to a void
// function is the one expression that produces no value; it is checked here rather than at each of
// the many operands this feeds, since a null handed to the C++ builder would end the process instead
// of reporting. Analysis admits such a call only where the value is thrown away, which [generator.discard] reads it with.
func (g *generator) value(x ast.Expr) llvm.Value {
	v := g.expr(x)
	if v.IsNil() {
		g.errorf(x.Pos(), "this produces no value; a call to a function returning void is a statement rather than an operand")
		return g.zero()
	}
	return g.convert(x, v, g.prog.Conversions[x])
}

// discard lowers x for its effects and throws the value away. It is the one
// entry point a void call may reach: a whole-statement expression, a for
// statement's post clause, and the initializer of an object nothing reads are
// the positions that have no use for a value.
func (g *generator) discard(x ast.Expr) {
	if v := g.expr(x); !v.IsNil() {
		g.convert(x, v, g.prog.Conversions[x])
	}
}

// convert applies one recorded conversion to an already lowered value.
func (g *generator) convert(x ast.Expr, v llvm.Value, to *sema.Type) llvm.Value {
	switch to.Kind() {
	case sema.Bool:
		g.setLoc(x.Pos())
		return g.widenTruth(g.isNonZero(v), "bool")
	case sema.Double:
		if v.Type() == g.f64 {
			return v
		}
		g.setLoc(x.Pos())
		return g.builder.CreateSIToFP(v, g.f64, "todouble")
	case sema.Invalid, sema.Int, sema.Dev, sema.Void, sema.Pointer, sema.Array:
		// Invalid is no conversion recorded at all. A conversion to long long emits
		// nothing here: a bool already holds 0 or 1, and a double is narrowed
		// by [generator.castExpr], which is the only place one may happen.
	}
	return v
}

func (g *generator) expr(x ast.Expr) llvm.Value {
	if x == nil {
		return g.zero()
	}
	if !g.descend(x.Pos()) {
		return g.zero()
	}
	defer g.ascend()
	g.setLoc(x.Pos())
	switch x := x.(type) {
	case *ast.IntLit:
		return g.intVal(x.Value)
	case *ast.CharLit:
		return g.intVal(x.Value)
	case *ast.FloatLit:
		return g.constFloat(x.Value)
	case *ast.BoolLit:
		if x.Value {
			return g.intVal(1)
		}
		return g.zero()
	case *ast.Ident:
		return g.ident(x)
	case *ast.UnaryExpr:
		return g.unary(x)
	case *ast.IncDecExpr:
		return g.incDec(x)
	case *ast.BinaryExpr:
		return g.binary(x)
	case *ast.AssignExpr:
		return g.assign(x)
	case *ast.CondExpr:
		return g.condExpr(x)
	case *ast.CallExpr:
		return g.call(x)
	case *ast.CastExpr:
		return g.castExpr(x)
	case *ast.IndexExpr:
		return g.index(x)
	case *ast.StringLit:
		g.errorf(x.Pos(), "a string literal is valid only as the argument of __ic_hash")
		return g.zero()
	case *ast.InitListExpr:
		g.errorf(x.Pos(), "a brace initializer is valid only as a variable's initializer")
		return g.zero()
	case *ast.BadExpr:
		g.errorf(x.Pos(), "the parser could not read this expression")
		return g.zero()
	default:
		g.errorf(x.Pos(), "expression %T is not lowered", x)
		return g.zero()
	}
}

// castExpr lowers an explicit cast. A cast to bool or double is a conversion analysis already
// recorded on the operand, which [generator.value] applies. A cast to long long truncates toward
// zero — the language's only narrowing — and is the identity when the operand is already one, read
// off its MicroC type since both sides are the same LLVM double; [generator.signlessZero] corrects the sign a truncated reading in (-1, 0) carries.
func (g *generator) castExpr(x *ast.CastExpr) llvm.Value {
	value := g.value(x.X)
	if g.prog.Types[x].Kind() != sema.Int || value.Type() != g.f64 {
		return value
	}
	g.setLoc(x.Lparen)
	if g.prog.Types[x.X].Kind() != sema.Double {
		return value
	}
	return g.signlessZero(g.machineTrunc(value))
}

// deviceOf reads the device a dev-valued expression names.
//
// Analysis resolved everything but a reference to a dev parameter, which is a
// property of the call site rather than of the body; that one comes from the
// bindings the enclosing expansion installed.
func (g *generator) deviceOf(x ast.Expr) (sema.Device, bool) {
	if device, resolved := g.prog.Devices[x]; resolved {
		return device, true
	}
	id, named := x.(*ast.Ident)
	if !named {
		return sema.Device{}, false
	}
	sym := g.prog.Uses[id]
	if sym == nil {
		return sema.Device{}, false
	}
	device, bound := g.devices[sym]
	return device, bound
}

// ident reads a named object. An array name is not read: it decays to a pointer
// to its first element, which is the address of the array itself.
func (g *generator) ident(x *ast.Ident) llvm.Value {
	sym := g.prog.Uses[x]
	if sym == nil {
		g.errorf(x.NamePos, "'%s' did not resolve to a declared object", x.Name)
		return g.zero()
	}
	if sym.Value != nil {
		return g.constant(*sym.Value)
	}
	slot, ok := g.storageOf(sym)
	if !ok {
		g.errorf(x.NamePos, "'%s' has type %s, which is not lowered", x.Name, sym.Type)
		return g.zero()
	}
	if sym.Type.Kind() == sema.Array {
		return slot
	}
	g.setLoc(x.NamePos)
	return g.builder.CreateLoad(g.typeOf(x), slot, x.Name)
}

// typeOf gives the LLVM type an expression's value has. An expression analysis
// could not type would have been reported already, so the fallback is the
// scalar type every arithmetic path expects.
func (g *generator) typeOf(x ast.Expr) llvm.Type {
	if typ, ok := g.llvmType(decayed(g.prog.Types[x])); ok {
		return typ
	}
	return g.i64
}

// decayed gives the type an expression's value has, which for an array name is
// a pointer to its first element. An array is never a value: there is no
// operation that copies one.
func decayed(t *sema.Type) *sema.Type {
	if t.Kind() != sema.Array {
		return t
	}
	return sema.PointerTo(t.Elem())
}

// addressOf returns the address an assignable expression names: the storage of
// a variable, the element a subscript selects, or the object a dereference
// reaches.
func (g *generator) addressOf(x ast.Expr) (llvm.Value, bool) {
	switch x := x.(type) {
	case *ast.Ident:
		sym := g.prog.Uses[x]
		if sym == nil {
			g.errorf(x.NamePos, "'%s' did not resolve to a declared object", x.Name)
			return llvm.Value{}, false
		}
		slot, ok := g.storageOf(sym)
		if !ok {
			g.errorf(x.NamePos, "'%s' has type %s, which is not lowered", x.Name, sym.Type)
			return llvm.Value{}, false
		}
		return slot, true
	case *ast.IndexExpr:
		return g.elementAddr(x)
	case *ast.UnaryExpr:
		if x.Op != ast.Deref {
			g.errorf(x.OpPos, "the '%s' operator does not name an object", x.Op)
			return llvm.Value{}, false
		}
		return g.value(x.X), true
	default:
		g.errorf(x.Pos(), "this expression does not name an object")
		return llvm.Value{}, false
	}
}

// elementAddr computes the address of one array element. The getelementptr stride is one element,
// which is one memory slot, so instruction selection divides the byte offset back out.
func (g *generator) elementAddr(x *ast.IndexExpr) (llvm.Value, bool) {
	base := g.value(x.X)
	index := g.value(x.Index)
	g.setLoc(x.Lbrack)
	// A getelementptr index is an integer in LLVM whatever the value types are, so the conversion is
	// written here; the register already holds the whole number and instruction selection emits nothing for it.
	return g.builder.CreateInBoundsGEP(g.typeOf(x), base, []llvm.Value{g.toMachineInt(index)}, ""), true
}

func (g *generator) index(x *ast.IndexExpr) llvm.Value {
	at, ok := g.elementAddr(x)
	if !ok {
		return g.zero()
	}
	g.setLoc(x.Lbrack)
	return g.builder.CreateLoad(g.typeOf(x), at, "")
}

func (g *generator) unary(x *ast.UnaryExpr) llvm.Value {
	switch x.Op {
	case ast.Plus:
		return g.value(x.X)
	case ast.Neg:
		operand := g.value(x.X)
		if operand.Type() != g.f64 {
			g.errorf(x.OpPos, "the unary operator '%s' does not apply to %s", x.Op, g.prog.Types[x.X])
			return g.zero()
		}
		if g.prog.Types[x].Kind() != sema.Double {
			// C's negation of an integer zero is a positive zero, where fneg is
			// a sign flip that would answer -0. Subtracting from a positive zero
			// is not: LLVM may fold that to fneg only under nsz, which nothing
			// here sets.
			return g.builder.CreateFSub(g.constFloat(0), operand, "neg")
		}
		return g.builder.CreateFNeg(operand, "neg")
	case ast.BitNot:
		operand := g.value(x.X)
		g.setLoc(x.OpPos)
		return g.bitwiseCall(notIntrinsic, operand)
	case ast.LogicalNot:
		g.setLoc(x.OpPos)
		return g.widenTruth(g.cond(x), "lnot")
	case ast.AddrOf:
		// Taking the address is what puts an object in the data region: the
		// optimizer promotes an alloca nothing addresses, and one it cannot
		// promote keeps its slot.
		at, ok := g.addressOf(x.X)
		if !ok {
			return g.zero()
		}
		return at
	case ast.Deref:
		at := g.value(x.X)
		g.setLoc(x.OpPos)
		return g.builder.CreateLoad(g.typeOf(x), at, "")
	default:
		g.errorf(x.OpPos, "the unary operator '%s' is not lowered", x.Op)
		return g.zero()
	}
}

// floatOps map the arithmetic operators a double takes. Division is a bare
// fdiv: the machine's div is a plain double division, which is exactly what a
// fractional quotient wants, where an integer one has to be truncated after it.
var floatOps = map[ast.BinaryOp]func(llvm.Builder, llvm.Value, llvm.Value, string) llvm.Value{
	ast.Add: llvm.Builder.CreateFAdd,
	ast.Sub: llvm.Builder.CreateFSub,
	ast.Mul: llvm.Builder.CreateFMul,
	ast.Div: llvm.Builder.CreateFDiv,
}

// comparePredicates maps the relational operators over pointers, the only values not held as a
// double (which compares through [floatPredicates]). They are signed because a pointer comparison
// is admitted only between offsets within one object, and no such offset is large enough for a
// signed and an unsigned ordering to disagree.
var comparePredicates = map[ast.BinaryOp]llvm.IntPredicate{
	ast.Eq: llvm.IntEQ,
	ast.Ne: llvm.IntNE,
	ast.Lt: llvm.IntSLT,
	ast.Le: llvm.IntSLE,
	ast.Gt: llvm.IntSGT,
	ast.Ge: llvm.IntSGE,
}

// floatPredicates map the relational operators over doubles: the four orderings are ordered
// predicates and equality is unordered, which matches the machine (a NaN operand answers false to
// all four orderings, false to seq, true to sne) and is also what LLVM already believes about a
// float comparison, so nothing here needs withholding from the optimizer.
var floatPredicates = map[ast.BinaryOp]llvm.FloatPredicate{
	ast.Eq: llvm.FloatOEQ,
	ast.Ne: llvm.FloatUNE,
	ast.Lt: llvm.FloatOLT,
	ast.Le: llvm.FloatOLE,
	ast.Gt: llvm.FloatOGT,
	ast.Ge: llvm.FloatOGE,
}

func (g *generator) binary(x *ast.BinaryExpr) llvm.Value {
	// Default is the rule: an operator with no shape of its own lowers through
	// the operator tables below.
	//exhaustive:ignore
	switch x.Op {
	case ast.LogicalAnd, ast.LogicalOr:
		g.setLoc(x.OpPos)
		return g.widenTruth(g.cond(x), "land")
	case ast.Eq, ast.Ne, ast.Lt, ast.Le, ast.Gt, ast.Ge:
		g.setLoc(x.OpPos)
		return g.widenTruth(g.compare(x), "cmp")
	case ast.Add, ast.Sub:
		if g.prog.Types[x].Kind() == sema.Pointer {
			return g.pointerArith(x)
		}
		if x.Op == ast.Sub && isPointerValued(g.prog.Types[x.X]) && isPointerValued(g.prog.Types[x.Y]) {
			return g.pointerDiff(x)
		}
	default:
	}
	if g.prog.Types[x].Kind() == sema.Double {
		build, ok := floatOps[x.Op]
		if !ok {
			g.errorf(x.OpPos, "the binary operator '%s' does not apply to a double", x.Op)
			return g.constFloat(0)
		}
		lhs := g.value(x.X)
		rhs := g.value(x.Y)
		g.setLoc(x.OpPos)
		return build(g.builder, lhs, rhs, "")
	}
	lhs := g.value(x.X)
	rhs := g.value(x.Y)
	g.setLoc(x.OpPos)
	if value, bitwise := g.bitwiseOp(x.Op, lhs, rhs); bitwise {
		return value
	}
	if lhs.Type() != g.f64 {
		g.errorf(x.OpPos, "the binary operator '%s' does not apply to %s", x.Op, g.prog.Types[x.X])
		return g.zero()
	}
	return g.machineIntOp(x.Op, x.OpPos, lhs, rhs)
}

// machineIntOp writes one MicroC integer arithmetic operator out of the operations the machine
// performs for it ([generator.bitwiseOp] handles the bitwise operators and shifts instead). Division
// truncates toward zero (C's rule, not the machine div's) and multiplication can sign a zero product
// by disagreeing operands, so both route through [generator.signlessZero]; the remainder's closing
// subtraction already leaves a zero positive, and addition and subtraction cannot produce a negative one.
func (g *generator) machineIntOp(op ast.BinaryOp, pos source.Position, lhs, rhs llvm.Value) llvm.Value {
	// Default is the rule: an operator with no machine form here was rejected by
	// analysis before it reached this stage.
	//exhaustive:ignore
	switch op {
	case ast.Add:
		return g.builder.CreateFAdd(lhs, rhs, "")
	case ast.Sub:
		return g.builder.CreateFSub(lhs, rhs, "")
	case ast.Mul:
		return g.signlessZero(g.builder.CreateFMul(lhs, rhs, ""))
	case ast.Div:
		return g.signlessZero(g.machineTrunc(g.builder.CreateFDiv(lhs, rhs, "")))
	case ast.Mod:
		quotient := g.machineTrunc(g.builder.CreateFDiv(lhs, rhs, ""))
		return g.builder.CreateFSub(lhs, g.builder.CreateFMul(quotient, rhs, ""), "")
	default:
		g.errorf(pos, "the binary operator '%s' is not lowered", op)
		return g.zero()
	}
}

// signlessZero reads a machine result as the integer C computes, which has one zero where IEEE has
// two; integer division, an explicit cast, and multiplication can each sign a zero result by
// disagreeing operands, and the two zeroes read as different bits to a device. It must run directly
// on that result, never before the rounding or multiply that made the sign, and is an ordinary fadd
// rather than an opaque call so the optimizer still folds or drops it where the value is never -0.0.
func (g *generator) signlessZero(result llvm.Value) llvm.Value {
	return g.builder.CreateFAdd(result, g.constFloat(0), "")
}

// isPointerValued reports whether an expression of type t denotes a pointer,
// an array name included: an array decays wherever it is used as a value.
func isPointerValued(t *sema.Type) bool {
	return decayed(t).Kind() == sema.Pointer
}

// pointerArith steps a pointer by a count of elements. The getelementptr is
// what carries the element stride; adding the count to the pointer directly
// would be right only because one element is one slot, which is a fact of the
// backend rather than of the IR.
func (g *generator) pointerArith(x *ast.BinaryExpr) llvm.Value {
	base, count := x.X, x.Y
	if !isPointerValued(g.prog.Types[base]) {
		base, count = count, base
	}
	pointer := g.value(base)
	index := g.value(count)
	g.setLoc(x.OpPos)
	index = g.toMachineInt(index)
	if x.Op == ast.Sub {
		index = g.builder.CreateNSWNeg(index, "")
	}
	return g.builder.CreateInBoundsGEP(g.elemTypeOf(base), pointer, []llvm.Value{index}, "")
}

// pointerDiff lowers the distance between two pointers into one object. The language counts that
// distance in elements and LLVM counts it in bytes, so the difference is divided by the element
// size; the division is exact because MicroC admits the operation only between pointers into one
// object, whose elements share a size.
func (g *generator) pointerDiff(x *ast.BinaryExpr) llvm.Value {
	lhs := g.value(x.X)
	rhs := g.value(x.Y)
	g.setLoc(x.OpPos)
	distance := g.builder.CreateNSWSub(
		g.builder.CreatePtrToInt(lhs, g.i64, ""),
		g.builder.CreatePtrToInt(rhs, g.i64, ""),
		"",
	)
	return g.fromMachineInt(g.builder.CreateExactSDiv(distance, g.constInt(ic10.SlotBytes), ""))
}

// elemTypeOf gives the LLVM type of what a pointer-valued expression points at.
func (g *generator) elemTypeOf(x ast.Expr) llvm.Type {
	if typ, ok := g.llvmType(decayed(g.prog.Types[x]).Elem()); ok {
		return typ
	}
	return g.i64
}

func (g *generator) compare(x *ast.BinaryExpr) llvm.Value {
	lhs := g.value(x.X)
	rhs := g.value(x.Y)
	g.setLoc(x.OpPos)
	if lhs.Type() == g.f64 {
		pred, ok := floatPredicates[x.Op]
		if !ok {
			g.errorf(x.OpPos, "the comparison '%s' is not lowered", x.Op)
			return llvm.ConstInt(g.i1, 0, false)
		}
		return g.builder.CreateFCmp(pred, lhs, rhs, "")
	}
	pred, ok := comparePredicates[x.Op]
	if !ok {
		g.errorf(x.OpPos, "the comparison '%s' is not lowered", x.Op)
		return llvm.ConstInt(g.i1, 0, false)
	}
	return g.builder.CreateICmp(pred, lhs, rhs, "")
}
