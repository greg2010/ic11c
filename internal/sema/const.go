package sema

import (
	"fmt"
	"math"
	"math/big"
	"strconv"

	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/source"
)

// exactLimit is the largest magnitude a long long holds on this machine: every
// value lives in an IEEE double, which represents integers exactly up to and
// including 2^53. It bounds a fold's result, not the arithmetic reaching it,
// so a fold answering inside it always matches what the machine computes.
const exactLimit = int64(1) << 53

// bitwiseLimit is the largest magnitude an operand of a bitwise or shift
// operator, or a shift result, may reach. The machine reduces such an operand
// modulo 2^53 before the instruction reads it, and reads a result back out of
// 53 bits and a sign taken from the next, so a shift reaching 2^53 answers -2^53.
const bitwiseLimit = exactLimit - 1

// widenAdvice and castLeftAdvice both tell the user to cast to long long so C
// computes in the machine's own width; castLeftAdvice names the left operand
// specifically because a shift takes its type from that operand alone. Neither
// follows the file's spelling: the subject is the width C converts in, and long
// is 32 bits under LLP64 and under ILP32, where casting to it widens nothing.
const (
	widenAdvice    = "cast an operand to long long so that C widens the operation too"
	castLeftAdvice = "cast the left operand to long long so that C widens the shift too"
)

// Value is a compile-time constant. Int carries a long long or a bool (0 or
// 1); Float carries a double. Type says which, and every reader must consult
// it: a long long constant leaves Float zero, and a double constant leaves
// Int zero.
type Value struct {
	Type  *Type
	Int   int64
	Float float64
}

// Num gives the value as the double the machine holds, whichever half carries
// it.
func (v Value) Num() float64 {
	if v.Type.Kind() == Double {
		return v.Float
	}
	return float64(v.Int)
}

// String renders the value the way the source would write it.
func (v Value) String() string {
	// Default is the rule: Int carries every constant a double does not, so a
	// kind that is not a bool or a double renders from there.
	//exhaustive:ignore
	switch v.Type.Kind() {
	case Bool:
		if v.Int != 0 {
			return "true"
		}
		return "false"
	case Double:
		return strconv.FormatFloat(v.Float, 'g', -1, 64)
	default:
		return strconv.FormatInt(v.Int, 10)
	}
}

func doubleValue(f float64) Value { return Value{Type: DoubleType, Float: f} }

// constFail says why an expression is not a constant expression. An empty msg
// means the cause was already reported, so the caller stays quiet.
type constFail struct {
	pos source.Position
	msg string
}

func failf(x ast.Expr, msg string) *constFail { return &constFail{pos: x.Pos(), msg: msg} }

func reported(x ast.Expr) *constFail { return &constFail{pos: x.Pos()} }

// constMode says which of C's two constant expressions a position requires.
// An integer constant expression computes in ints alone — a double reaches it
// only as a floating literal a cast converts — while an arithmetic constant
// expression takes a double as an ordinary operand.
type constMode uint8

const (
	arithmeticConst constMode = iota
	integerConst
)

// requireConst evaluates x where the language demands a constant expression of
// the given form, naming the context when x is not one. An x whose mistake
// [checker.diagnoseFold] already reported is skipped, since the stricter mode
// would otherwise re-fold it and report the same mistake twice.
func (c *checker) requireConst(x ast.Expr, mode constMode, what string) (Value, bool) {
	if _, prior := c.constEval(x, arithmeticConst); prior != nil && prior.msg == "" {
		return Value{}, false
	}
	v, fail := c.constEval(x, mode)
	if fail == nil {
		c.prog.Consts[x] = v
		return v, true
	}
	if fail.msg != "" {
		c.errorf(fail.pos, "%s must be a constant expression: %s", what, fail.msg)
	}
	return Value{}, false
}

// initConstMode says which constant expression an initializer of type want,
// declared by sym, must be: C reads a constexpr integer object's initializer
// as an integer constant expression, and every other constant initializer —
// a global's or a constexpr double's — as an arithmetic one.
func initConstMode(sym *Symbol, want *Type) constMode {
	if sym.Constexpr && isIntegral(want) {
		return integerConst
	}
	return arithmeticConst
}

// diagnoseFold folds x for problems constant evaluation reports on its own —
// an out-of-range shift count, division by zero, an unrepresentable result —
// wherever the operands are constant, not only where a constant expression
// is required, so a plain initializer's mistake is still caught.
func (c *checker) diagnoseFold(x ast.Expr) {
	if _, fail := c.constEval(x, arithmeticConst); fail != nil {
		c.diagnoseRuntimeShift(x)
		c.diagnoseUnfoldedOperands(x)
	}
}

// diagnoseUnfoldedOperands applies the per-operand rules of a bitwise or shift
// operator whose own fold did not reach every operand, because a fold only
// runs once every operand is constant. A compound assignment always lands
// here: its left operand is an object, never one the fold reaches.
func (c *checker) diagnoseUnfoldedOperands(x ast.Expr) {
	switch x := x.(type) {
	case *ast.BinaryExpr:
		c.diagnoseBinaryOperands(x)
	case *ast.AssignExpr:
		c.diagnoseCompoundOperand(x)
	}
}

// diagnoseBinaryOperands applies the per-operand rules to a binary operator,
// either of whose operands may be the constant one.
func (c *checker) diagnoseBinaryOperands(x *ast.BinaryExpr) {
	// Default is the rule: no other operator hands an operand to the machine's
	// bitwise conversion, so none carries a bound one folded operand decides.
	//exhaustive:ignore
	switch x.Op {
	case ast.BitAnd, ast.BitOr, ast.BitXor:
		if c.diagnoseBitwiseOperand(x, x.X) {
			return
		}
		c.diagnoseBitwiseOperand(x, x.Y)
	case ast.Shl, ast.Shr:
		if c.diagnoseBitwiseOperand(x, x.X) {
			return
		}
		if count, constant := c.constOperand(x.Y); constant {
			c.checkShiftCount(x.OpPos, cTypeOrMachine(c.cTypeOf(x.X)), count)
		}
	default:
	}
}

// diagnoseCompoundOperand applies the same rules to the right operand of a
// compound bitwise or shift assignment. The left operand is never asked: it
// is the object being assigned, and a constexpr object is refused as an
// assignment target, so no target ever carries a constant value.
func (c *checker) diagnoseCompoundOperand(x *ast.AssignExpr) {
	v, constant := c.constOperand(x.Value)
	if !constant {
		return
	}
	// Default is the rule: every other compound assignment applies an operator
	// the machine's bitwise conversion never sees.
	//exhaustive:ignore
	switch x.Op {
	case ast.ShlAssign, ast.ShrAssign:
		c.checkShiftCount(x.OpPos, cTypeOrMachine(c.cTypeOf(x.Target)), v)
	case ast.AndAssign, ast.OrAssign, ast.XorAssign:
		c.checkBitwiseOperand(x.Value, x.Op.String(), v)
	default:
	}
}

// diagnoseBitwiseOperand holds one operand of x to [bitwiseLimit] where it is
// constant, reporting whether it was refused.
func (c *checker) diagnoseBitwiseOperand(x *ast.BinaryExpr, operand ast.Expr) bool {
	v, constant := c.constOperand(operand)
	if !constant {
		return false
	}
	return c.checkBitwiseOperand(operand, x.Op.String(), v) != nil
}

// constOperand folds one operand of an operator that did not fold, answering
// false for an operand that is not an integer constant. A double operand is
// not asked about: type checking already refused it, so asking here would
// name the same expression a second time for a window it never reaches.
func (c *checker) constOperand(x ast.Expr) (int64, bool) {
	v, fail := c.constEval(x, arithmeticConst)
	if fail != nil || v.Type.Kind() != Int {
		return 0, false
	}
	return v.Int, true
}

// diagnoseRuntimeShift refuses a shift whose count is not constant and whose
// left operand C gives a type narrower than the machine's own: C computes
// '1 << n' in that promoted type — 32 bits for a bare int — so a count of 32
// or more is undefined in C, while this machine always shifts 64 bits.
func (c *checker) diagnoseRuntimeShift(x ast.Expr) {
	shift, binary := x.(*ast.BinaryExpr)
	if !binary || (shift.Op != ast.Shl && shift.Op != ast.Shr) {
		return
	}
	if _, fail := c.constEval(shift.Y, arithmeticConst); fail == nil || fail.msg == "" {
		return
	}
	t, known := c.cTypeOf(shift.X)
	if !known || t.bits >= machineBits {
		return
	}
	c.errorf(shift.OpPos, "C computes '%s' in '%s', the type it gives the left operand, and the count is not constant, "+
		"so a count of %d or more is undefined there and shifts %d bits here; %s",
		shift.Op, t, t.bits, machineBits, castLeftAdvice)
}

// constEval folds x at compile time under the constant expression the mode
// asks for. It reports a diagnostic itself for a constant whose value the machine
// cannot represent and for a double an integer constant expression may not
// compute with; everything else it returns as a failure for the caller to name.
func (c *checker) constEval(x ast.Expr, mode constMode) (Value, *constFail) {
	v, fail := c.constFold(x, mode)
	if fail != nil || mode == arithmeticConst || v.Type.Kind() != Double {
		return v, fail
	}
	c.errorf(x.Pos(), "a constant expression of integer type cannot compute with a double value; "+
		"only a floating literal that a cast converts may appear here")
	return Value{}, reported(x)
}

// foldKey names one folded node. The mode is part of it because the two
// constant expressions admit different operands, so the same node can fold
// under one and fail under the other.
type foldKey struct {
	x    ast.Expr
	mode constMode
}

// foldResult is what one fold answered, kept so that a second ask does not
// repeat the work beneath it.
type foldResult struct {
	value Value
	fail  *constFail
}

// constFold folds one node, leaving the mode to constEval to enforce on the
// result. It expects x to already be type checked.
func (c *checker) constFold(x ast.Expr, mode constMode) (Value, *constFail) {
	key := foldKey{x: x, mode: mode}
	if got, folded := c.folded[key]; folded {
		return got.value, got.fail
	}
	v, fail := c.foldNode(x, mode)
	c.folded[key] = foldResult{value: v, fail: fail}
	return v, fail
}

func (c *checker) foldNode(x ast.Expr, mode constMode) (Value, *constFail) {
	if t, ok := c.prog.Types[x]; ok && t.Kind() == Invalid {
		return Value{}, reported(x)
	}
	switch x := x.(type) {
	case *ast.IntLit:
		return c.checkWidth(x.Pos(), "the integer literal "+strconv.FormatInt(x.Value, 10), x.Value, c.intType)
	case *ast.CharLit:
		return Value{Type: c.intType, Int: x.Value}, nil
	case *ast.FloatLit:
		return doubleValue(x.Value), nil
	case *ast.BoolLit:
		return Value{Type: BoolType, Int: boolInt(x.Value)}, nil
	case *ast.Ident:
		return c.constIdent(x)
	case *ast.UnaryExpr:
		return c.constUnary(x, mode)
	case *ast.BinaryExpr:
		return c.constBinary(x, mode)
	case *ast.CondExpr:
		return c.constCond(x, mode)
	case *ast.CastExpr:
		return c.constCast(x, mode)
	case *ast.CallExpr:
		if id, isName := x.Fun.(*ast.Ident); isName && intrinsics[id.Name] != nil {
			return Value{}, failf(x, "an intrinsic is not constant")
		}
		return Value{}, failf(x, "a function call is not constant")
	case *ast.IndexExpr:
		return Value{}, failf(x, "an array element is not constant")
	case *ast.AssignExpr:
		return Value{}, failf(x, "an assignment is not constant")
	case *ast.IncDecExpr:
		return Value{}, failf(x, "'"+x.Op.String()+"' is not constant")
	case *ast.StringLit:
		return Value{}, failf(x, "a string literal is valid only as the argument of __ic_hash")
	case *ast.InitListExpr:
		return Value{}, failf(x, "a brace initializer is not an expression")
	default:
		return Value{}, reported(x)
	}
}

func (c *checker) constIdent(x *ast.Ident) (Value, *constFail) {
	sym := c.prog.Uses[x]
	switch {
	case sym == nil:
		return Value{}, reported(x)
	case sym.Value != nil:
		return *sym.Value, nil
	case sym.Kind == FuncName:
		return Value{}, failf(x, "a function name is not constant")
	case sym.Type.Kind() == Array || sym.Type.Kind() == Pointer:
		return Value{}, failf(x, "a pointer or array operand is not constant")
	case sym.Type.Kind() == Dev:
		return Value{}, failf(x, "a device is not a number")
	case sym.Constexpr:
		return Value{}, failf(x, "'"+x.Name+"' is constexpr but its initializer is not constant")
	default:
		return Value{}, failf(x, "'"+x.Name+"' is not a constexpr object")
	}
}

// pointerNotConstant is what '&' and '*' answer with. Both are refused before
// their operand is folded, so that the diagnostic names the pointer rather than
// whatever stands under it.
func pointerNotConstant(x ast.Expr) *constFail {
	return failf(x, "a pointer operand is not constant")
}

// constUnary folds a prefix operator, in the type its operand folded to.
func (c *checker) constUnary(x *ast.UnaryExpr, mode constMode) (Value, *constFail) {
	if x.Op == ast.AddrOf || x.Op == ast.Deref {
		return Value{}, pointerNotConstant(x)
	}
	v, fail := c.constEval(x.X, mode)
	if fail != nil {
		return Value{}, fail
	}
	double := v.Type.Kind() == Double
	switch x.Op {
	case ast.Plus:
		if double {
			return v, nil
		}
		return Value{Type: c.intType, Int: v.Int}, nil
	case ast.Neg:
		if double {
			return doubleValue(-v.Float), nil
		}
		return c.constUnaryInt(x, -v.Int)
	case ast.LogicalNot:
		return Value{Type: BoolType, Int: boolInt(v.Num() == 0)}, nil
	case ast.BitNot:
		if double {
			// Type checking has already refused '~' over a double.
			return Value{}, reported(x)
		}
		if fail := c.checkBitwiseOperand(x.X, x.Op.String(), v.Int); fail != nil {
			return Value{}, fail
		}
		return c.constUnaryInt(x, ^v.Int)
	case ast.AddrOf, ast.Deref:
		// Handled above, before the operand folded.
	}
	return Value{}, reported(x)
}

// constUnaryInt checks the result of '-' or '~', which C computes in the
// promoted type of the operand rather than in the machine's own.
func (c *checker) constUnaryInt(x *ast.UnaryExpr, v int64) (Value, *constFail) {
	return c.constResult(x.OpPos, operatorResult(x.Op.String()), cTypeOrMachine(c.cTypeOf(x.X)), v)
}

// cTypeOrMachine reads one of the cType lookups, answering with the machine's
// own width where the lookup found no type: an unmodeled type restricts
// nothing, since the operand already folded to an integer to be asked about at
// all.
func cTypeOrMachine(t cType, known bool) cType {
	if !known {
		return cLongLong
	}
	return t
}

func (c *checker) constBinary(x *ast.BinaryExpr, mode constMode) (Value, *constFail) {
	lhs, fail := c.constEval(x.X, mode)
	if fail != nil {
		return Value{}, fail
	}
	// && and || decide on the left operand alone when they can, and the right
	// one is then never evaluated. The truth of the operand is read through Num
	// rather than off Int, which carries nothing for a double. Default is the
	// rule: every other operator evaluates both.
	//exhaustive:ignore
	switch x.Op {
	case ast.LogicalAnd:
		if lhs.Num() == 0 {
			return Value{Type: BoolType}, nil
		}
	case ast.LogicalOr:
		if lhs.Num() != 0 {
			return Value{Type: BoolType, Int: 1}, nil
		}
	default:
	}
	rhs, fail := c.constEval(x.Y, mode)
	if fail != nil {
		return Value{}, fail
	}
	if lhs.Type.Kind() == Double || rhs.Type.Kind() == Double {
		return c.constDouble(x, lhs.Num(), rhs.Num())
	}

	a, b := lhs.Int, rhs.Int
	if x.Op == ast.Shl || x.Op == ast.Shr {
		return c.constShift(x, a, b)
	}
	if x.Op == ast.LogicalAnd || x.Op == ast.LogicalOr {
		// C converts nothing between these two operands: each is read for its
		// truth alone, and the left one has already decided wherever it could.
		return Value{Type: BoolType, Int: boolInt(b != 0)}, nil
	}
	t, fail := c.convertOperands(x, a, b)
	if fail != nil {
		return Value{}, fail
	}
	switch x.Op {
	case ast.Add:
		return c.constArith(x, t, a, b, addInt)
	case ast.Sub:
		return c.constArith(x, t, a, b, subInt)
	case ast.Mul:
		return c.constArith(x, t, a, b, mulInt)
	case ast.Div, ast.Mod:
		return c.constDivide(x, t, a, b)
	case ast.BitAnd:
		return c.constBitwise(x, t, a, b, a&b)
	case ast.BitOr:
		return c.constBitwise(x, t, a, b, a|b)
	case ast.BitXor:
		return c.constBitwise(x, t, a, b, a^b)
	case ast.Eq:
		return Value{Type: BoolType, Int: boolInt(a == b)}, nil
	case ast.Ne:
		return Value{Type: BoolType, Int: boolInt(a != b)}, nil
	case ast.Lt:
		return Value{Type: BoolType, Int: boolInt(a < b)}, nil
	case ast.Le:
		return Value{Type: BoolType, Int: boolInt(a <= b)}, nil
	case ast.Gt:
		return Value{Type: BoolType, Int: boolInt(a > b)}, nil
	case ast.Ge:
		return Value{Type: BoolType, Int: boolInt(a >= b)}, nil
	case ast.Shl, ast.Shr, ast.LogicalAnd, ast.LogicalOr:
		// Answered above: none of these converts its operands to a common
		// type, so none has one to be checked against.
	}
	return Value{}, reported(x)
}

// convertOperands applies C's usual arithmetic conversions to a folded
// operand pair and reports the common type. An operand outside that type is
// refused only where [convertedAnswerDiffers] finds the converted answer
// actually diverges from the one the fold computed.
func (c *checker) convertOperands(x *ast.BinaryExpr, a, b int64) (cType, *constFail) {
	t := cTypeOrMachine(c.convertedCType(x))
	if !convertedAnswerDiffers(x.Op, t, a, b) {
		return t, nil
	}
	// The answers can only differ where the conversion changed something, so one
	// of the two is an operand t does not hold, and that is the one to name.
	operand, v := x.X, a
	if t.holds(a) {
		operand, v = x.Y, b
	}
	c.errorf(operand.Pos(), "C converts this operand of '%s' to '%s', where %d is a different number; %s",
		x.Op, t, v, widenAdvice)
	return t, &constFail{pos: operand.Pos()}
}

// convertedAnswerDiffers reports whether op answers differently over the pair
// C converts to t than over the pair the source wrote. A type that holds both
// operands converts neither, so the two never differ there.
func convertedAnswerDiffers(op ast.BinaryOp, t cType, a, b int64) bool {
	ca, cb := t.convert(a), t.convert(b)
	switch op {
	case ast.BitAnd:
		return ca&cb != a&b
	case ast.BitOr:
		return ca|cb != a|b
	case ast.BitXor:
		return ca^cb != a^b
	case ast.Div, ast.Mod:
		// Guards against a zero divisor (reported elsewhere) and the one pair
		// Go's division panics on at this width.
		if b == 0 || divideUndefined(cLongLong, a, b) {
			return false
		}
		if op == ast.Div {
			return ca/cb != a/b
		}
		return ca%cb != a%b
	case ast.Eq, ast.Ne, ast.Lt, ast.Le, ast.Gt, ast.Ge:
		return compareInts(op, ca, cb) != compareInts(op, a, b)
	case ast.Add, ast.Sub, ast.Mul, ast.Shl, ast.Shr, ast.LogicalAnd, ast.LogicalOr:
	}
	return false
}

// compareInts applies one of C's six relational operators, which answer over
// two values of one type and so need no type of their own.
func compareInts(op ast.BinaryOp, a, b int64) bool {
	switch op {
	case ast.Eq:
		return a == b
	case ast.Ne:
		return a != b
	case ast.Lt:
		return a < b
	case ast.Le:
		return a <= b
	case ast.Gt:
		return a > b
	case ast.Ge:
		return a >= b
	case ast.Add, ast.Sub, ast.Mul, ast.Div, ast.Mod, ast.Shl, ast.Shr,
		ast.BitAnd, ast.BitOr, ast.BitXor, ast.LogicalAnd, ast.LogicalOr:
	}
	return false
}

// constBitwise folds '&', '|' or '^' over the operand pair C converted,
// holding both operands to the window the machine's conversion carries them
// through — an operand at 2^53 is where fold and machine part: the fold reads
// the number the source wrote, and the instruction reads zero.
func (c *checker) constBitwise(x *ast.BinaryExpr, t cType, a, b, folded int64) (Value, *constFail) {
	for _, operand := range [...]struct {
		x ast.Expr
		v int64
	}{{x.X, a}, {x.Y, b}} {
		if fail := c.checkBitwiseOperand(operand.x, x.Op.String(), operand.v); fail != nil {
			return Value{}, fail
		}
	}
	return c.constResult(x.OpPos, operatorResult(x.Op.String()), t, folded)
}

// checkBitwiseOperand refuses an operand the machine's conversion does not hand
// to a bitwise or shift instruction unchanged.
func (c *checker) checkBitwiseOperand(x ast.Expr, op string, v int64) *constFail {
	if v >= -bitwiseLimit && v <= bitwiseLimit {
		return nil
	}
	c.errorf(x.Pos(), "the machine reduces an operand of '%s' modulo 2^53 before the instruction reads it, so %d does not reach it; "+
		"an operand of a bitwise or shift operator must be between %d and %d", op, v, -bitwiseLimit, bitwiseLimit)
	return &constFail{pos: x.Pos()}
}

// constResult checks a folded integer against both limits it has to meet: the
// C type the operation computes in, and the range [exactLimit] names.
func (c *checker) constResult(pos source.Position, what string, t cType, v int64) (Value, *constFail) {
	if !t.holds(v) {
		c.errorf(pos, "%s is %d, which does not fit '%s', the type C computes it in; %s", what, v, t, widenAdvice)
		return Value{}, &constFail{pos: pos}
	}
	return c.checkWidth(pos, what, v, c.intType)
}

// constDouble folds an operator with a double operand. Go's float64 arithmetic
// is IEEE, matching every register on the chip, so each fold matches what the
// chip computes at run time — including division by zero, which the machine
// answers with an infinity or a NaN rather than refusing.
func (c *checker) constDouble(x *ast.BinaryExpr, a, b float64) (Value, *constFail) {
	switch x.Op {
	case ast.Add:
		return doubleValue(a + b), nil
	case ast.Sub:
		return doubleValue(a - b), nil
	case ast.Mul:
		return doubleValue(a * b), nil
	case ast.Div:
		return doubleValue(a / b), nil
	case ast.Eq:
		return Value{Type: BoolType, Int: boolInt(a == b)}, nil
	case ast.Ne:
		return Value{Type: BoolType, Int: boolInt(a != b)}, nil
	case ast.Lt:
		return Value{Type: BoolType, Int: boolInt(a < b)}, nil
	case ast.Le:
		return Value{Type: BoolType, Int: boolInt(a <= b)}, nil
	case ast.Gt:
		return Value{Type: BoolType, Int: boolInt(a > b)}, nil
	case ast.Ge:
		return Value{Type: BoolType, Int: boolInt(a >= b)}, nil
	case ast.LogicalAnd, ast.LogicalOr:
		return Value{Type: BoolType, Int: boolInt(b != 0)}, nil
	case ast.Mod, ast.Shl, ast.Shr, ast.BitAnd, ast.BitOr, ast.BitXor:
	}
	// The remaining operators take long long operands, which type checking has
	// already held them to.
	return Value{}, reported(x)
}

// constArith reports the value of a long long fold of '+', '-', or '*', or
// that the operation left the range the machine holds. Go's int64 wraps on
// overflow where the machine's double grows toward infinity, so folding a
// wrapped result would stamp in a number the same expression never computes.
func (c *checker) constArith(x *ast.BinaryExpr, t cType, a, b int64, fold func(a, b int64) (int64, bool)) (Value, *constFail) {
	what := operatorResult(x.Op.String())
	v, exact := fold(a, b)
	if !exact {
		return c.unrepresentable(x.OpPos, what, c.intType)
	}
	return c.constResult(x.OpPos, what, t, v)
}

// addInt, subInt, and mulInt apply one operator, reporting false where it
// overflows the int64 the fold runs in, rather than answering with the wrapped
// result. Only mulInt can actually overflow here — every operand is inside
// 2^53, so a sum or difference stays well inside int64 — but all three check.
func addInt(a, b int64) (int64, bool) {
	v := a + b
	return v, (v > a) == (b > 0)
}

func subInt(a, b int64) (int64, bool) {
	v := a - b
	return v, (v < a) == (b > 0)
}

func mulInt(a, b int64) (int64, bool) {
	switch {
	case a == 0 || b == 0:
		return 0, true
	case a == math.MinInt64 || b == math.MinInt64:
		// Unreachable with a real operand (all are inside 2^53), but needed:
		// the overflow check below divides, and MinInt64 / -1 panics in Go.
		return 0, false
	}
	v := a * b
	return v, v/b == a
}

// divideUndefined reports whether C leaves 'a / b' and 'a % b' undefined over
// an operand pair of type t: a signed t has exactly one such pair, its most
// negative value over -1, whose quotient overflows t by one; an unsigned t has
// none. The same pair is what Go's own division panics on at int64's width.
func divideUndefined(t cType, a, b int64) bool {
	return b == -1 && a == t.min()
}

// constDivide folds '/' or '%' over the operand pair C converted to t. The
// pair C leaves undefined is refused before folding, so every pair that
// reaches [checker.constResult] already divides into a value t holds.
func (c *checker) constDivide(x *ast.BinaryExpr, t cType, a, b int64) (Value, *constFail) {
	if b == 0 {
		what := "division"
		if x.Op == ast.Mod {
			what = "remainder"
		}
		c.errorf(x.OpPos, "%s by zero in a constant expression", what)
		return Value{}, reported(x)
	}
	ca, cb := t.convert(a), t.convert(b)
	if divideUndefined(t, ca, cb) {
		c.errorf(x.OpPos, "C leaves '%s' undefined where the quotient does not fit '%s', the type C computes it in, "+
			"and %d over -1 is the one such pair; %s", x.Op, t, ca, widenAdvice)
		return Value{}, &constFail{pos: x.OpPos}
	}
	v := a / b
	if x.Op == ast.Mod {
		v = a % b
	}
	return c.constResult(x.OpPos, operatorResult(x.Op.String()), t, v)
}

// constShift folds '<<' or '>>'. The result takes the promoted left operand's
// type, whose width bounds the count. The count needs no [bitwiseLimit] test
// of its own — [checker.checkShiftCount] already keeps it inside 64 — but the
// shifted value and a left shift's result both do.
func (c *checker) constShift(x *ast.BinaryExpr, a, b int64) (Value, *constFail) {
	t := cTypeOrMachine(c.cTypeOf(x.X))
	what := operatorResult(x.Op.String())
	if !c.checkShiftCount(x.OpPos, t, b) {
		return Value{}, reported(x)
	}
	if fail := c.checkBitwiseOperand(x.X, x.Op.String(), a); fail != nil {
		return Value{}, fail
	}
	if x.Op == ast.Shr {
		return c.constResult(x.OpPos, what, t, a>>uint(b))
	}
	if a < 0 {
		c.errorf(x.OpPos, "C leaves the left shift of a negative value undefined, and the left operand is %d", a)
		return Value{}, reported(x)
	}
	if b > 0 && a > bitwiseLimit>>uint(b) {
		// The operand stops at 2^53 and the count at 63, so the result reaches
		// 2^116 and naming it needs a wider arithmetic than the fold runs in.
		shifted := new(big.Int).Lsh(big.NewInt(a), uint(b))
		c.errorf(x.OpPos, "%s is %s, which is past %d, and the machine reads a shift result back out of 53 bits "+
			"and a sign taken from the next, so a left shift that reaches 2^53 answers with -2^53", what, shifted, bitwiseLimit)
		return Value{}, &constFail{pos: x.OpPos}
	}
	return c.constResult(x.OpPos, what, t, a<<uint(b))
}

// checkShiftCount holds a constant count to the width of t, the type C gives
// the left operand, and reports whether it stands — the whole reason
// '(long long)1 << 40' shifts and '1 << 40' does not. A narrow t is one
// MicroC cannot spell directly, which is why the message advises a cast.
func (c *checker) checkShiftCount(pos source.Position, t cType, count int64) bool {
	if count >= 0 && count < int64(t.bits) {
		return true
	}
	msg := fmt.Sprintf("a shift count must be between 0 and %d, the width of '%s' that C gives the left operand, found %d",
		t.bits-1, t, count)
	if t.bits < machineBits {
		msg += "; " + castLeftAdvice
	}
	c.errorf(pos, "%s", msg)
	return false
}

// checkWidth rejects an integer constant outside the range the machine holds,
// naming intType in the message and giving it to the value. The bound is on
// magnitude, not round-trip: a power of two past 2^53 survives a double
// untouched but is rejected anyway, since arithmetic near it is no longer exact.
func (c *checker) checkWidth(pos source.Position, what string, v int64, intType *Type) (Value, *constFail) {
	if v > exactLimit || v < -exactLimit {
		return c.unrepresentable(pos, what, intType)
	}
	return Value{Type: intType, Int: v}, nil
}

// unrepresentable reports a value outside [exactLimit], spelling the type it
// does not fit the way intType writes it.
//
// It is also what an overflowed int64 fold reports, which is past the window by
// a wide margin, so the range the message names describes both.
func (c *checker) unrepresentable(pos source.Position, what string, intType *Type) (Value, *constFail) {
	c.errorf(pos, "%s is outside -2^53 to 2^53, the range a %s holds on this machine, where every value lives in an IEEE double", what, intType)
	return Value{}, &constFail{pos: pos}
}

// operatorResult names an operator's result the way checkWidth reports it.
func operatorResult(op string) string { return "the result of '" + op + "'" }

// constCond folds a conditional expression, evaluating only the arm the
// condition selects (through Num, since Int carries nothing for a double).
// The untaken arm still has a say: C converts both arms to one type, so its
// unsigned-ness can decide whether a negative value in the taken arm survives.
func (c *checker) constCond(x *ast.CondExpr, mode constMode) (Value, *constFail) {
	cond, fail := c.constEval(x.Cond, mode)
	if fail != nil {
		return Value{}, fail
	}
	arm := x.Else
	if cond.Num() != 0 {
		arm = x.Then
	}
	v, fail := c.constEval(arm, mode)
	if fail != nil || v.Type.Kind() != Int {
		return v, fail
	}
	t, ok := c.cTypeOf(x)
	if !ok {
		return v, nil
	}
	return c.constResult(x.Question, operatorResult("?:"), t, v.Int)
}

// constCast folds a cast. A cast to long long truncates toward zero — the
// machine's trunc, not its round; a cast to bool normalizes; a cast to double
// widens exactly, up to 2^53. A floating literal written directly under the
// cast is the one double an integer constant expression admits.
func (c *checker) constCast(x *ast.CastExpr, mode constMode) (Value, *constFail) {
	operand := mode
	if _, literal := x.X.(*ast.FloatLit); literal {
		operand = arithmeticConst
	}
	v, fail := c.constEval(x.X, operand)
	if fail != nil {
		return Value{}, fail
	}
	switch target := c.prog.Types[x]; target.Kind() {
	case Bool:
		return Value{Type: BoolType, Int: boolInt(v.Num() != 0)}, nil
	case Double:
		return doubleValue(v.Num()), nil
	case Int:
		return c.constTruncate(x, target, v)
	case Invalid, Dev, Void, Pointer, Array:
	}
	return Value{}, reported(x)
}

// constTruncate folds a cast of a double to the integer type target, which the
// cast wrote. A whole part no int64 holds — including NaN and an infinity,
// which the machine's trunc answers with unchanged — is refused outright. One
// that fits is still held to the 53 bits the machine represents exactly, like
// every integer constant.
func (c *checker) constTruncate(x *ast.CastExpr, target *Type, v Value) (Value, *constFail) {
	if v.Type.Kind() != Double {
		return Value{Type: target, Int: v.Int}, nil
	}
	whole := math.Trunc(v.Float)
	if math.IsNaN(whole) || whole < math.MinInt64 || whole >= -float64(math.MinInt64) {
		c.errorf(x.Lparen, "the cast truncates %s, which is not a value a %s holds", v, target)
		return Value{}, &constFail{pos: x.Lparen}
	}
	return c.checkWidth(x.Lparen, "the value this cast truncates to", int64(whole), target)
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
