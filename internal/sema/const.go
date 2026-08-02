package sema

import (
	"math"
	"strconv"

	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/source"
)

// exactLimit is the largest magnitude the machine represents exactly. A
// bitwise or shift result beyond it is corrupted by the round trip through a
// double, which is worth saying at compile time rather than discovering at run
// time.
const exactLimit = int64(1) << 53

// The fix for every divergence between C's type for an operation and the
// machine's own: the cast is what makes C compute in the width the fold does.
// Which operand takes it is the whole difference between the two, since a shift
// takes its type from the left operand alone.
const (
	widenAdvice    = "cast an operand to long long so that C widens the operation too"
	castLeftAdvice = "cast the left operand to long long so that C widens the shift too"
)

// Value is a compile-time constant.
//
// Int carries a long long or a bool, a bool being 0 or 1; Float carries a double.
// Type says which, and is what every reader has to consult: a long long constant
// leaves Float zero and a double constant leaves Int zero.
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
//
// An integer constant expression computes in ints alone: a double reaches it
// only as a floating literal that a cast converts, which is the one shape C
// spells out. An arithmetic constant expression has no such restriction, and a
// double is an ordinary operand there.
//
// The distinction is what keeps MicroC a subset of C23. Both forms fold to the
// same number here, so a position that took the wrong one accepted a program no
// C compiler would translate.
type constMode uint8

const (
	arithmeticConst constMode = iota
	integerConst
)

// requireConst evaluates x where the language demands a constant expression of
// the given form, naming the context when x is not one.
func (c *checker) requireConst(x ast.Expr, mode constMode, what string) (Value, bool) {
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
// declared by sym, must be.
//
// C reads the initializer of a constexpr object of integer type as an integer
// constant expression. Every other constant initializer — a global's, and a
// constexpr double's — is an arithmetic one, so a double computes there.
func initConstMode(sym *Symbol, want *Type) constMode {
	if sym.Constexpr && isIntegral(want) {
		return integerConst
	}
	return arithmeticConst
}

// diagnoseFold folds x for the problems constant evaluation reports itself: a
// shift count no shift takes, a division by zero, and a result the machine
// cannot hold.
//
// Those are properties of the operands rather than of the context, so they are
// reported wherever the operands are constant. Reporting them only where the
// language requires a constant expression left the same expression in a local
// initializer folding to a number the program did not write, with nothing said.
//
// The failure constEval returns says only that x is not constant, which is a
// problem in a required-constant context alone; those callers reach constEval
// through requireConst, which names the context. What an expression that did
// not fold is still held to is the shift width, which its operands' types
// decide on their own.
func (c *checker) diagnoseFold(x ast.Expr) {
	if _, fail := c.constEval(x, arithmeticConst); fail != nil {
		c.diagnoseRuntimeShift(x)
	}
}

// diagnoseRuntimeShift refuses a shift whose count the program computes and
// whose left operand C gives a type narrower than the machine's own.
//
// A shift takes the promoted left operand's type, and the width of that type is
// what bounds the count: [checker.constShift] refuses 1 << 31 because C gives
// the bare literal the type int. A count that is not constant cannot be bounded
// that way, and the divergence is the same one — C computes 1 << n in 32 bits,
// where a count of 32 or more is undefined and clang answers 1 << 40 with 256,
// and this machine shifts a 64-bit value and answers 2^40. The left operand's
// type is known whether or not the count is, which is what lets the same rule
// reject 1 << n and admit (long long)1 << n.
//
// Widening the operation instead would compile a program that means one thing
// here and another read as C, which is the subset claim the whole cType model
// exists to keep.
//
// Every operand reaching a MicroC shift is an integer, so a left operand with
// no C type is one analysis does not model rather than one C narrows, and it
// restricts nothing.
func (c *checker) diagnoseRuntimeShift(x ast.Expr) {
	shift, binary := x.(*ast.BinaryExpr)
	if !binary || (shift.Op != ast.Shl && shift.Op != ast.Shr) {
		return
	}
	if _, fail := c.constEval(shift.Y, arithmeticConst); fail == nil {
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

// constFold folds one node, leaving the mode to constEval to enforce on the
// result.
//
// It expects x to have been type checked, so a well-typed operand pair is all
// it has to handle.
func (c *checker) constFold(x ast.Expr, mode constMode) (Value, *constFail) {
	if t, ok := c.prog.Types[x]; ok && t.Kind() == Invalid {
		return Value{}, reported(x)
	}
	switch x := x.(type) {
	case *ast.IntLit:
		return c.checkWidth(x.Pos(), "the integer literal "+strconv.FormatInt(x.Value, 10), x.Value)
	case *ast.CharLit:
		return Value{Type: IntType, Int: x.Value}, nil
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
		return Value{Type: IntType, Int: v.Int}, nil
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
		return c.constUnaryInt(x, ^v.Int)
	case ast.AddrOf, ast.Deref:
		// Answered above, before the operand was folded, so that the
		// diagnostic names the pointer rather than whatever stands under it.
	}
	return Value{}, reported(x)
}

// constUnaryInt checks the result of '-' or '~', which C computes in the
// promoted type of the operand rather than in the machine's own.
func (c *checker) constUnaryInt(x *ast.UnaryExpr, v int64) (Value, *constFail) {
	return c.constResult(x.OpPos, operatorResult(x.Op.String()), cTypeOrMachine(c.cTypeOf(x.X)), v)
}

// cTypeOrMachine reads one of the cType lookups, answering with the machine's
// own width where the lookup found no type.
//
// A type the model does not record restricts nothing: the operand folded to an
// integer to be asked about at all, so this is a shape the model does not cover
// rather than a width C narrows the fold to.
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
		return c.constResult(x.OpPos, operatorResult(x.Op.String()), t, a&b)
	case ast.BitOr:
		return c.constResult(x.OpPos, operatorResult(x.Op.String()), t, a|b)
	case ast.BitXor:
		return c.constResult(x.OpPos, operatorResult(x.Op.String()), t, a^b)
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

// convertOperands applies C's usual arithmetic conversions to a folded operand
// pair and reports the type both convert to.
//
// An operand the common type does not hold is a divergence rather than a
// rounding: C goes on computing with the number the conversion produced and a
// 64-bit fold with the number the source wrote, so the operator answers
// differently in each. Refusing it is what keeps the two agreeing.
//
// A pair with no common type is left unrestricted, which [cTypeOrMachine]
// explains.
func (c *checker) convertOperands(x *ast.BinaryExpr, a, b int64) (cType, *constFail) {
	t := cTypeOrMachine(c.convertedCType(x))
	for _, operand := range [...]struct {
		x ast.Expr
		v int64
	}{{x.X, a}, {x.Y, b}} {
		if t.holds(operand.v) {
			continue
		}
		c.errorf(operand.x.Pos(), "C converts this operand of '%s' to '%s', where %d is a different number; %s",
			x.Op, t, operand.v, widenAdvice)
		return t, &constFail{pos: operand.x.Pos()}
	}
	return t, nil
}

// constResult checks a folded integer against both limits it has to meet: the C
// type the operation computes in, and the 53 bits the machine represents
// exactly.
func (c *checker) constResult(pos source.Position, what string, t cType, v int64) (Value, *constFail) {
	if !t.holds(v) {
		c.errorf(pos, "%s is %d, which does not fit '%s', the type C computes it in; %s", what, v, t, widenAdvice)
		return Value{}, &constFail{pos: pos}
	}
	return c.checkWidth(pos, what, v)
}

// constDouble folds an operator with a double operand.
//
// Go's arithmetic on float64 is IEEE, which is what every register holds, so
// each of these folds to the number the chip would have computed. Division by
// zero is not refused the way the integer form is: it answers with an infinity
// there too, and that is a value the machine holds and the emitter spells.
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

// constArith reports the value of a long long fold of '+', '-', or '*', or that the
// operation left the range the machine holds.
//
// Go's int64 wraps where the machine grows toward infinity, so folding a wrapped
// result would stamp a number into the program that the same expression does not
// compute at run time. Overflow is undefined either way; only one of the two is
// silent.
func (c *checker) constArith(x *ast.BinaryExpr, t cType, a, b int64, fold func(a, b int64) (int64, bool)) (Value, *constFail) {
	what := operatorResult(x.Op.String())
	v, exact := fold(a, b)
	if !exact {
		return c.unrepresentable(x.OpPos, what)
	}
	return c.constResult(x.OpPos, what, t, v)
}

// addInt, subInt, and mulInt apply one operator, reporting false where it
// overflows the int64 the fold runs in rather than answering with the wrapped
// result.
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
		// Past the range the machine holds whatever the other operand is, and
		// the one pair the overflow check below would panic dividing.
		return 0, false
	}
	v := a * b
	return v, v/b == a
}

func (c *checker) constDivide(x *ast.BinaryExpr, t cType, a, b int64) (Value, *constFail) {
	if b == 0 {
		what := "division"
		if x.Op == ast.Mod {
			what = "remainder"
		}
		c.errorf(x.OpPos, "%s by zero in a constant expression", what)
		return Value{}, reported(x)
	}
	// The one operand pair Go's division panics on. The spec makes signed
	// overflow undefined, so there is no answer worth folding.
	if a == math.MinInt64 && b == -1 {
		c.errorf(x.OpPos, "the result of '%s' is not representable on this machine", x.Op)
		return Value{}, reported(x)
	}
	v := a / b
	if x.Op == ast.Mod {
		v = a % b
	}
	return c.constResult(x.OpPos, operatorResult(x.Op.String()), t, v)
}

// constShift folds '<<' or '>>', which convert their operands separately: the
// result takes the promoted left operand's type, and the width of that type is
// what bounds the count. That is why (long long)1 << 40 shifts and 1 << 40 does
// not — C gives the bare literal the type int, whose width is 32.
func (c *checker) constShift(x *ast.BinaryExpr, a, b int64) (Value, *constFail) {
	t := cTypeOrMachine(c.cTypeOf(x.X))
	what := operatorResult(x.Op.String())
	if b < 0 || b >= int64(t.bits) {
		c.errorf(x.OpPos, "a shift count must be between 0 and %d, the width of '%s' that C gives the left operand, found %d",
			t.bits-1, t, b)
		return Value{}, reported(x)
	}
	if x.Op == ast.Shr {
		return c.constResult(x.OpPos, what, t, a>>uint(b))
	}
	if a < 0 {
		c.errorf(x.OpPos, "C leaves the left shift of a negative value undefined, and the left operand is %d", a)
		return Value{}, reported(x)
	}
	if b > 0 && a > math.MaxInt64>>uint(b) {
		return c.unrepresentable(x.OpPos, what)
	}
	return c.constResult(x.OpPos, what, t, a<<uint(b))
}

// checkWidth rejects a long long constant the machine cannot hold, naming it as what
// reads best where it came from. Every register and memory slot holds one
// double, and every operation on ints converts to a signed 64-bit integer,
// operates, and converts back, so anything past 53 significant bits comes back
// changed.
//
// It is what makes the integer type a subset of the C type the prelude declares
// it as rather than an approximation of it: long long is at least 64 bits
// everywhere, so a constant this admits denotes the same number there.
func (c *checker) checkWidth(pos source.Position, what string, v int64) (Value, *constFail) {
	if v > exactLimit || v < -exactLimit {
		return c.unrepresentable(pos, what)
	}
	return Value{Type: IntType, Int: v}, nil
}

func (c *checker) unrepresentable(pos source.Position, what string) (Value, *constFail) {
	c.errorf(pos, "%s needs more than 53 significant bits and is not representable on this machine", what)
	return Value{}, &constFail{pos: pos}
}

// operatorResult names an operator's result the way checkWidth reports it.
func operatorResult(op string) string { return "the result of '" + op + "'" }

// constCond folds a conditional expression, evaluating only the arm the
// condition selects. The truth of the condition is read through Num rather than
// off Int, which carries nothing for a double.
//
// The arm not taken still has a say. C converts both arms to one type, so an
// unsigned arm the fold never evaluates is what decides whether a negative
// value in the other arm survives.
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

// constCast folds a cast. A cast to long long truncates toward zero, which is the
// machine's trunc and not its round; a cast to bool normalizes; a cast to
// double widens, exactly to 2^53.
//
// A floating literal written directly under the cast is the one double an
// integer constant expression admits, so the operand of that shape alone folds
// as an arithmetic constant expression. Anything else keeps the caller's mode
// and is held to it.
func (c *checker) constCast(x *ast.CastExpr, mode constMode) (Value, *constFail) {
	operand := mode
	if _, literal := x.X.(*ast.FloatLit); literal {
		operand = arithmeticConst
	}
	v, fail := c.constEval(x.X, operand)
	if fail != nil {
		return Value{}, fail
	}
	switch c.prog.Types[x].Kind() {
	case Bool:
		return Value{Type: BoolType, Int: boolInt(v.Num() != 0)}, nil
	case Double:
		return doubleValue(v.Num()), nil
	case Int:
		return c.constTruncate(x, v)
	case Invalid, Dev, Void, Pointer, Array:
	}
	return Value{}, reported(x)
}

// constTruncate folds a cast of a double to a long long.
//
// A whole part no int64 holds is refused outright, a NaN and an infinity among
// them: the machine's trunc answers with them unchanged, and there is no integer
// constant to fold them to. One that fits an int64 is still held to the 53 bits
// the machine represents exactly, which is what every other integer constant is
// held to.
func (c *checker) constTruncate(x *ast.CastExpr, v Value) (Value, *constFail) {
	if v.Type.Kind() != Double {
		return Value{Type: IntType, Int: v.Int}, nil
	}
	whole := math.Trunc(v.Float)
	if math.IsNaN(whole) || whole < math.MinInt64 || whole >= -float64(math.MinInt64) {
		c.errorf(x.Lparen, "the cast truncates %s, which is not a value a long long holds", v)
		return Value{}, &constFail{pos: x.Lparen}
	}
	return c.checkWidth(x.Lparen, "the value this cast truncates to", int64(whole))
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
