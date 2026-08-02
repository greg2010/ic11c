package sema

import (
	"math"

	"github.com/greg2010/ic11c/internal/ast"
)

// cType is the C type of an integer operand in a constant expression, after the
// integer promotions.
//
// MicroC has one integer type and the prelude declares it long long, but a
// literal in the source does not have that type. C gives an integer constant
// the first type from a list that represents it, and the narrower types on that
// list are what make 2147483647 + 1 overflow an int and 0x80000000 unsigned.
// Folding in the machine's own 64-bit signed arithmetic without modelling them
// computes numbers the C the same file is read as does not.
//
// The model is target independent. C's lists name long between int and long
// long, and long is 64 bits under LP64 and 32 under LLP64 — but where it is 32
// bits it has the range of int and never takes a place on either list that int
// or unsigned int has not already taken. Every target whose int is 32 bits and
// whose long long is 64 therefore gives a constant the same width and
// signedness, and only the spelling of the type differs. A target with a
// narrower int would not, and there is none the prelude supports.
type cType struct {
	bits     uint8
	unsigned bool
}

// machineBits is the width every register holds and every fold runs in. A C
// type narrower than it is where C and the machine can answer differently.
const machineBits = 64

// The C types a MicroC constant expression can reach. There is no unsigned
// 64-bit entry: the lexer refuses every literal suffix and every literal past
// what a signed 64-bit integer holds, so no operand starts out with that type,
// and the conversions below never produce one from the three that remain.
var (
	cInt      = cType{bits: 32}
	cUint     = cType{bits: 32, unsigned: true}
	cLongLong = cType{bits: machineBits}
)

// String names the type the way a diagnostic quotes it. The 64-bit signed type
// is long long, which is what the prelude declares and what the programmer
// wrote, even where a given target would have reached it as long.
func (t cType) String() string {
	name := "int"
	if t.bits == machineBits {
		name = "long long"
	}
	if t.unsigned {
		return "unsigned " + name
	}
	return name
}

// holds reports whether v converts to t unchanged. An operand or a result it
// answers false for is where C and a 64-bit fold part company.
func (t cType) holds(v int64) bool {
	switch {
	case t.unsigned && v < 0:
		return false
	case t.bits == machineBits:
		return true
	case t.unsigned:
		return v <= math.MaxUint32
	default:
		return v >= math.MinInt32 && v <= math.MaxInt32
	}
}

// usualArith gives the type C's usual arithmetic conversions convert both
// operands of a binary operator to.
//
// The wider type wins outright rather than lending its rank to an unsigned one,
// because the only widths here are 32 and 64: a 64-bit signed type holds every
// value of a 32-bit unsigned one, which is the pairing C resolves in favour of
// the signed type.
func usualArith(a, b cType) cType {
	if a.bits == b.bits {
		return cType{bits: a.bits, unsigned: a.unsigned || b.unsigned}
	}
	if a.bits > b.bits {
		return a
	}
	return b
}

// cTypeOf gives the C type of x, and false where x is not an integer operand
// there.
//
// The type of a C expression follows from its operands' types alone, so nothing
// here evaluates anything. That is what lets a conditional expression ask for
// the type of the arm it did not take, which decides what the arm it did take
// converts to.
func (c *checker) cTypeOf(x ast.Expr) (cType, bool) {
	switch x := x.(type) {
	case *ast.IntLit:
		return literalCType(x), true
	case *ast.CharLit:
		// A character constant has type int in C; there is no char type for it
		// to have narrower than that.
		return cInt, true
	case *ast.UnaryExpr:
		return c.unaryCType(x)
	case *ast.BinaryExpr:
		return c.binaryCType(x)
	case *ast.CondExpr:
		then, ok := c.cTypeOf(x.Then)
		if !ok {
			return cType{}, false
		}
		els, ok := c.cTypeOf(x.Else)
		if !ok {
			return cType{}, false
		}
		return usualArith(then, els), true
	default:
		return promotedCType(c.prog.Types[x])
	}
}

// literalCType gives an integer literal the first type from the list C searches
// for its spelling: the signed types alone for a decimal constant, and the
// unsigned ones interleaved for a hexadecimal one.
//
// The value is never negative — a literal carries no sign, and the lexer
// refuses one past what a signed 64-bit integer holds — so the search runs
// upwards from int and stops at long long.
func literalCType(x *ast.IntLit) cType {
	switch {
	case x.Value <= math.MaxInt32:
		return cInt
	case x.Hex && x.Value <= math.MaxUint32:
		return cUint
	default:
		return cLongLong
	}
}

// promotedCType gives a MicroC type the C type an operand of it has after the
// integer promotions. A bool promotes to int, which is why it does not force an
// operation into the machine's own width.
func promotedCType(t *Type) (cType, bool) {
	switch t.Kind() {
	case Int:
		return cLongLong, true
	case Bool:
		return cInt, true
	case Invalid, Double, Dev, Void, Pointer, Array:
	}
	return cType{}, false
}

func (c *checker) unaryCType(x *ast.UnaryExpr) (cType, bool) {
	switch x.Op {
	case ast.Plus, ast.Neg, ast.BitNot:
		return c.cTypeOf(x.X)
	case ast.LogicalNot:
		return cInt, true
	case ast.AddrOf, ast.Deref:
	}
	return cType{}, false
}

func (c *checker) binaryCType(x *ast.BinaryExpr) (cType, bool) {
	switch x.Op {
	case ast.Add, ast.Sub, ast.Mul, ast.Div, ast.Mod, ast.BitAnd, ast.BitOr, ast.BitXor:
		return c.convertedCType(x)
	case ast.Shl, ast.Shr:
		// A shift converts its operands separately. The result takes the
		// promoted left operand's type, and the count's type says nothing about
		// it, which is why (long long)1 << 40 shifts where 1 << 40 does not.
		return c.cTypeOf(x.X)
	case ast.Eq, ast.Ne, ast.Lt, ast.Le, ast.Gt, ast.Ge, ast.LogicalAnd, ast.LogicalOr:
		return cInt, true
	}
	return cType{}, false
}

// convertedCType gives the type the usual arithmetic conversions convert both
// operands of x to. A comparison asks for this rather than for the type of x,
// whose result is an int however its operands convert.
func (c *checker) convertedCType(x *ast.BinaryExpr) (cType, bool) {
	lhs, ok := c.cTypeOf(x.X)
	if !ok {
		return cType{}, false
	}
	rhs, ok := c.cTypeOf(x.Y)
	if !ok {
		return cType{}, false
	}
	return usualArith(lhs, rhs), true
}
