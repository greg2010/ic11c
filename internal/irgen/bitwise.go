package irgen

import (
	"math"

	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/sema"
	"tinygo.org/x/go-llvm"
)

// The bitwise operators and the shifts are calls the optimizer may not speculate: each converts its
// operands to a signed 64-bit integer, which faults (ShiftUnderflow/ShiftOverflow) outside that
// range, but LLVM's own and/or/xor/shl/ashr cannot fail and so may be moved past a guarding range
// check. Shr is arithmetic (MicroC's long long is signed); operands and result are the register double.
var bitwiseIntrinsics = map[ast.BinaryOp]*sema.Intrinsic{
	ast.Shl:    bitwiseIntrinsic("__ic_shl", 2),
	ast.Shr:    bitwiseIntrinsic("__ic_shr", 2),
	ast.BitAnd: bitwiseIntrinsic("__ic_and", 2),
	ast.BitOr:  bitwiseIntrinsic("__ic_or", 2),
	ast.BitXor: bitwiseIntrinsic("__ic_xor", 2),
}

// notIntrinsic is the same declaration for the one unary bitwise operator.
var notIntrinsic = bitwiseIntrinsic("__ic_not", 1)

func bitwiseIntrinsic(name string, operands int) *sema.Intrinsic {
	params := make([]sema.OperandKind, operands)
	for i := range params {
		params[i] = sema.OperandValue
	}
	return &sema.Intrinsic{Name: name, Result: sema.IntType, Params: params, Pure: true}
}

// bitwiseNames is every declaration standing for a machine bitwise instruction, by name. It is
// derived from the tables above rather than written out again, so a new operator added there cannot
// be forgotten here and silently keep speculatable, which [mayFault] withholds by consulting this.
var bitwiseNames = declaredBitwiseNames()

func declaredBitwiseNames() map[string]bool {
	names := make(map[string]bool, len(bitwiseIntrinsics)+1)
	for _, in := range bitwiseIntrinsics {
		names[in.Name] = true
	}
	names[notIntrinsic.Name] = true
	return names
}

// bitwiseFolds gives the machine's answer for a bitwise instruction whose operands the program wrote
// out, keyed by declaration name. The opacity that keeps the optimizer from moving these calls also
// keeps it from folding one on its own, so a mask built out of constants needs this to avoid an
// instruction the program did not need.
var bitwiseFolds = map[string]func(a, b int64) (int64, bool){
	"__ic_and": func(a, b int64) (int64, bool) { return a & b, true },
	"__ic_or":  func(a, b int64) (int64, bool) { return a | b, true },
	"__ic_xor": func(a, b int64) (int64, bool) { return a ^ b, true },
	"__ic_not": func(a, _ int64) (int64, bool) { return ^a, true },
	"__ic_shl": func(a, b int64) (int64, bool) {
		if b < 0 || b >= shiftDistances {
			return 0, false
		}
		shifted := a << uint(b)
		return shifted, shifted>>uint(b) == a
	},
	"__ic_shr": func(a, b int64) (int64, bool) {
		if b < 0 || b >= shiftDistances {
			return 0, false
		}
		return a >> uint(b), true
	},
}

// exactBits bounds the fold to whole numbers in [-2^exactBits, 2^exactBits]. It is one short of the
// machine's own width because the machine's read conversion reduces an operand modulo 2^53 while its
// write-back sign-extends from bit 53, so the widest window common to both ends is ±(2^53 - 1), and
// a bit count can only name that as ±2^52. A constant outside it still gets the machine's own answer, as a call.
const exactBits = 52

// shiftDistances is the distance the fold declines at: the machine reduces a shift distance modulo
// 64, where Go's shift keeps counting past the width, so this is where a folded distance and the
// machine's answer would first disagree.
const shiftDistances = 64

// mayFault reports whether the instruction behind an intrinsic can refuse its
// operands, which is what keeps its declaration out of speculatable.
func mayFault(name string) bool { return bitwiseNames[name] }

// bitwiseCall writes one bitwise or shift operator as the call standing for the
// machine instruction it becomes, or as the constant that instruction would
// have computed.
func (g *generator) bitwiseCall(in *sema.Intrinsic, operands ...llvm.Value) llvm.Value {
	if folded, ok := g.foldBitwise(in.Name, operands); ok {
		return folded
	}
	fnType, fn := g.intrinsicFunc(in, len(operands))
	return g.builder.CreateCall(fnType, fn, operands, "")
}

func (g *generator) foldBitwise(name string, operands []llvm.Value) (llvm.Value, bool) {
	var args [2]int64
	for i, operand := range operands {
		value, exact := exactInteger(operand)
		if !exact {
			return llvm.Value{}, false
		}
		args[i] = value
	}
	fold, declared := bitwiseFolds[name]
	if !declared {
		// A declaration with no fold leaves the call standing, which is the
		// machine computing its own answer rather than a refusal.
		// TestEveryBitwiseDeclarationIsHeldToBeFaulting is what keeps that from
		// being how a new operator arrives.
		return llvm.Value{}, false
	}
	result, folded := fold(args[0], args[1])
	if !folded || !withinExactBits(result) {
		return llvm.Value{}, false
	}
	return g.intVal(result), true
}

// exactInteger reads a constant operand as the integer the machine would convert it to, and reports
// false for one it would not convert exactly: a value with a fraction, a NaN, an infinity, or
// anything past [exactBits]. The inexact flag from the binding means it rounded the constant to a
// double the module's type cannot represent exactly, which is refused rather than ignored since a fold replacing a machine instruction cannot round.
func exactInteger(v llvm.Value) (int64, bool) {
	if v.IsAConstantFP().IsNil() {
		return 0, false
	}
	value, inexact := v.DoubleValue()
	if inexact || value != math.Trunc(value) || !withinExactBits(value) {
		return 0, false
	}
	return int64(value), true
}

// withinExactBits reports whether a value is inside the window [exactBits]
// names, which both an operand the fold reads and an answer it keeps have to
// be.
func withinExactBits[T int64 | float64](v T) bool {
	return v >= -(1<<exactBits) && v <= 1<<exactBits
}

// bitwiseOp writes op as that call, and reports false for an operator that is
// not a bitwise one. The declaration takes the double a long long is held in, so
// no conversion stands around the call.
func (g *generator) bitwiseOp(op ast.BinaryOp, lhs, rhs llvm.Value) (llvm.Value, bool) {
	in, bitwise := bitwiseIntrinsics[op]
	if !bitwise {
		return llvm.Value{}, false
	}
	return g.bitwiseCall(in, lhs, rhs), true
}
