package irgen

import (
	"math"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/sema"
	"tinygo.org/x/go-llvm"
)

// TestEveryBitwiseDeclarationIsHeldToBeFaulting covers the derivation behind
// [mayFault] reaching every declaration: one it does not hold of is one LLVM is
// free to move above the guard bounding its operand, and nothing else in the
// compiler would say so.
func TestEveryBitwiseDeclarationIsHeldToBeFaulting(t *testing.T) {
	declared := make([]*sema.Intrinsic, 0, len(bitwiseIntrinsics)+1)
	for _, in := range bitwiseIntrinsics {
		declared = append(declared, in)
	}
	declared = append(declared, notIntrinsic)

	for _, in := range declared {
		t.Run(in.Name, func(t *testing.T) {
			if !mayFault(in.Name) {
				t.Errorf("%s is declared for a machine bitwise instruction but is not held to fault, "+
					"so its declaration carries speculatable", in.Name)
			}
			if _, folded := bitwiseFolds[in.Name]; !folded {
				t.Errorf("%s is declared for a machine bitwise instruction and has no constant fold", in.Name)
			}
		})
	}
	for name := range bitwiseFolds {
		if !bitwiseNames[name] {
			t.Errorf("%s has a constant fold and no declaration to reach it", name)
		}
	}
}

// TestBitwiseFoldsAreTheMachineAnswer covers the one place this stage states an
// answer instead of emitting the instruction that would have computed it. The
// window on the answer is applied by [generator.foldBitwise] rather than here,
// so a row past it states what the machine computes and is refused above.
func TestBitwiseFoldsAreTheMachineAnswer(t *testing.T) {
	const exact = int64(1) << exactBits
	tests := []struct {
		name   string
		fold   string
		a, b   int64
		want   int64
		folded bool
	}{
		{name: "and", fold: "__ic_and", a: 7, b: 3, want: 3, folded: true},
		{name: "or", fold: "__ic_or", a: 4, b: 1, want: 5, folded: true},
		{name: "xor", fold: "__ic_xor", a: 6, b: 3, want: 5, folded: true},
		{name: "not", fold: "__ic_not", a: 4, want: -5, folded: true},

		{name: "a left shift of nothing", fold: "__ic_shl", a: 3, b: 0, want: 3, folded: true},
		{name: "a left shift", fold: "__ic_shl", a: 3, b: 4, want: 48, folded: true},
		{name: "a left shift the width of the type", fold: "__ic_shl", a: 1, b: shiftDistances},
		{name: "a left shift past the width", fold: "__ic_shl", a: 1, b: shiftDistances + 1},
		{name: "a left shift by a negative distance", fold: "__ic_shl", a: 1, b: -1},
		{name: "a left shift one under the width", fold: "__ic_shl", a: -1, b: shiftDistances - 1,
			want: -1 << (shiftDistances - 1), folded: true},
		{
			// The bit shifted into the sign is not the one that was there, so the
			// answer does not shift back to what it was shifted from.
			name: "a left shift into the sign", fold: "__ic_shl", a: 1, b: shiftDistances - 1,
		},
		{
			// Every bit leaves the type, so the answer Go computes is zero and
			// bears no relation to what was shifted. It is inside the window, so
			// this guard is the only thing between it and a folded zero.
			name: "a left shift whose bits all leave the type", fold: "__ic_shl", a: exact, b: 12,
		},
		{
			// The guard admits it — the bits are all still there — and the window
			// above refuses it.
			name: "a left shift past the window", fold: "__ic_shl", a: exact, b: 1, want: exact << 1, folded: true,
		},

		{name: "a right shift", fold: "__ic_shr", a: 48, b: 4, want: 3, folded: true},
		{
			// MicroC's long long is signed and the machine's conversion is signed
			// with it, so the shift is arithmetic.
			name: "a right shift of a negative value", fold: "__ic_shr", a: -8, b: 1, want: -4, folded: true,
		},
		{name: "a right shift the width of the type", fold: "__ic_shr", a: 1, b: shiftDistances},
		{name: "a right shift past the width", fold: "__ic_shr", a: 1, b: shiftDistances + 1},
		{name: "a right shift by a negative distance", fold: "__ic_shr", a: 1, b: -1},
		{name: "a right shift one under the width", fold: "__ic_shr", a: -1, b: shiftDistances - 1,
			want: -1, folded: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fold, declared := bitwiseFolds[tt.fold]
			if !declared {
				t.Fatalf("no fold is declared for %s", tt.fold)
			}
			got, folded := fold(tt.a, tt.b)
			if folded != tt.folded {
				t.Fatalf("%s(%d, %d) folded = %v, want %v", tt.fold, tt.a, tt.b, folded, tt.folded)
			}
			if folded && got != tt.want {
				t.Errorf("%s(%d, %d) = %d, want %d", tt.fold, tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// TestWithinExactBitsIsTheWindowBothEndsShare covers the boundary the fold turns
// on, from both sides and in both the type it reads an operand as and the type
// it reads a constant as.
func TestWithinExactBitsIsTheWindowBothEndsShare(t *testing.T) {
	const exact = int64(1) << exactBits
	tests := []struct {
		name  string
		value int64
		want  bool
	}{
		{name: "zero", value: 0, want: true},
		{name: "the top of the window", value: exact, want: true},
		{name: "one past the top", value: exact + 1},
		{name: "the bottom of the window", value: -exact, want: true},
		{name: "one past the bottom", value: -exact - 1},
		{name: "the widest integer", value: math.MaxInt64},
		{name: "the narrowest integer", value: math.MinInt64},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := withinExactBits(tt.value); got != tt.want {
				t.Errorf("withinExactBits(%d) = %v, want %v", tt.value, got, tt.want)
			}
			if got := withinExactBits(float64(tt.value)); got != tt.want {
				t.Errorf("withinExactBits(%v) = %v, want %v, which is what an operand is read as",
					float64(tt.value), got, tt.want)
			}
		})
	}
}

// TestExactIntegerReadsOnlyWhatTheMachineConverts covers what the fold accepts
// as an operand. A value it reads inexactly is a folded constant that disagrees
// with the instruction it replaced, which is wrong output where everywhere else
// a refusal is what a limit produces.
func TestExactIntegerReadsOnlyWhatTheMachineConverts(t *testing.T) {
	const exact = float64(int64(1) << exactBits)
	ctx := llvm.NewContext()
	defer ctx.Dispose()
	f64 := ctx.DoubleType()

	tests := []struct {
		name  string
		value float64
		want  int64
		exact bool
	}{
		{name: "zero", value: 0, want: 0, exact: true},
		{name: "a whole number", value: 42, want: 42, exact: true},
		{name: "a negative whole number", value: -42, want: -42, exact: true},
		{name: "the top of the window", value: exact, want: int64(exact), exact: true},
		{name: "one past the top", value: exact + 1},
		{name: "the bottom of the window", value: -exact, want: -int64(exact), exact: true},
		{name: "one past the bottom", value: -exact - 1},
		{name: "a fraction", value: 0.5},
		{name: "a whole number with a fraction on it", value: 4.25},
		{name: "a NaN", value: math.NaN()},
		{name: "an infinity", value: math.Inf(1)},
		{name: "a negative infinity", value: math.Inf(-1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := exactInteger(llvm.ConstFloat(f64, tt.value))
			if ok != tt.exact {
				t.Fatalf("exactInteger(%v) exact = %v, want %v", tt.value, ok, tt.exact)
			}
			if ok && got != tt.want {
				t.Errorf("exactInteger(%v) = %d, want %d", tt.value, got, tt.want)
			}
		})
	}
}

// TestExactIntegerReadsNoRuntimeValue covers the operand every real program
// hands the fold. Nothing about a value the machine computes is known here, and
// reading one as the constant zero would fold a mask over it away.
func TestExactIntegerReadsNoRuntimeValue(t *testing.T) {
	ctx := llvm.NewContext()
	defer ctx.Dispose()
	m := ctx.NewModule("runtime")
	defer m.Dispose()
	f64 := ctx.DoubleType()

	fn := llvm.AddFunction(m, "f", llvm.FunctionType(f64, []llvm.Type{f64}, false))
	builder := ctx.NewBuilder()
	defer builder.Dispose()
	builder.SetInsertPointAtEnd(llvm.AddBasicBlock(fn, "entry"))
	sum := builder.CreateFAdd(fn.Param(0), llvm.ConstFloat(f64, 1), "")

	for _, operand := range []struct {
		name  string
		value llvm.Value
	}{
		{name: "a parameter", value: fn.Param(0)},
		{name: "an instruction", value: sum},
	} {
		t.Run(operand.name, func(t *testing.T) {
			if _, ok := exactInteger(operand.value); ok {
				t.Error("read a constant out of a value the machine computes")
			}
		})
	}
}

// TestFoldingStopsAtTheWindow covers the two helpers above reaching a program.
// Each row is a constant expression one side of the boundary, and what it turns
// on is whether the module holds an answer or the call that computes one.
func TestFoldingStopsAtTheWindow(t *testing.T) {
	tests := []struct {
		name   string
		src    string
		folded bool
	}{
		{name: "an operand at the top of the window", src: "g = 4503599627370496 & -1;", folded: true},
		{name: "an operand one past the top", src: "g = 4503599627370497 & -1;"},
		{name: "an operand at the bottom of the window", src: "g = -4503599627370496 | 0;", folded: true},
		{name: "an operand one past the bottom", src: "g = -4503599627370497 | 0;"},
		{name: "a result at the top of the window", src: "g = (long long)1 << 52;", folded: true},
		{name: "a result one past the top", src: "g = ~4503599627370496;"},
		{name: "a value the machine computes", src: "g = g & 3;"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text := generate(t, "long long g;\nvoid main(void) { "+tt.src+" }")
			called := strings.Contains(text, "call double @__ic_")
			if called == tt.folded {
				t.Errorf("the module %s a machine bitwise call, want the other:\n%s",
					map[bool]string{true: "holds", false: "does not hold"}[called], text)
			}
		})
	}
}
