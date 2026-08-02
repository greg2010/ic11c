package vm

import (
	"math"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
)

// twoPow53 is the last integer a double represents exactly and the width every
// bitwise round trip is squeezed through.
const twoPow53 = 9007199254740992.0

var arithmeticCases = []instructionCase{
	{
		name: "move copies a literal", op: ic10.OpMove,
		source:        "move r0 42",
		wantRegisters: map[ic10.Register]float64{0: 42},
	},
	{
		name: "move rejects the nan constant", op: ic10.OpMove,
		source:    "move r0 nan",
		wantFault: &Fault{Type: ExcIncorrectVariable, Line: 0},
	},
	{
		name: "move accepts the infinity constants", op: ic10.OpMove,
		source:        "move r0 pinf\nmove r1 ninf",
		wantRegisters: map[ic10.Register]float64{0: math.Inf(1), 1: math.Inf(-1)},
	},
	{
		name: "add", op: ic10.OpAdd,
		source:        "add r0 2 3",
		wantRegisters: map[ic10.Register]float64{0: 5},
	},
	{
		name: "add does not wrap past the exact integer range", op: ic10.OpAdd,
		source:        "add r0 9007199254740992 1",
		wantRegisters: map[ic10.Register]float64{0: twoPow53},
	},
	{
		name: "sub", op: ic10.OpSub,
		source:        "sub r0 2 5",
		wantRegisters: map[ic10.Register]float64{0: -3},
	},
	{
		name: "mul", op: ic10.OpMul,
		source:        "mul r0 6 7",
		wantRegisters: map[ic10.Register]float64{0: 42},
	},
	{
		name: "div by zero grows to infinity rather than faulting", op: ic10.OpDiv,
		source:        "div r0 1 0",
		wantRegisters: map[ic10.Register]float64{0: math.Inf(1)},
	},
	{
		name: "mod of a negative dividend adds the divisor back", op: ic10.OpMod,
		source:        "mod r0 -1 3",
		wantRegisters: map[ic10.Register]float64{0: 2},
	},
	{
		name: "mod of a positive dividend by a negative divisor keeps the remainder", op: ic10.OpMod,
		source:        "mod r0 1 -3",
		wantRegisters: map[ic10.Register]float64{0: 1},
	},
	{
		name: "mod of a negative dividend by a negative divisor goes further negative", op: ic10.OpMod,
		source:        "mod r0 -1 -3",
		wantRegisters: map[ic10.Register]float64{0: -4},
	},
	{
		name: "sqrt of a negative is NaN", op: ic10.OpSqrt,
		source:        "sqrt r0 -1",
		wantRegisters: map[ic10.Register]float64{0: math.NaN()},
	},
	{
		name: "round is banker's rounding", op: ic10.OpRound,
		source:        "round r0 0.5\nround r1 1.5\nround r2 2.5",
		wantRegisters: map[ic10.Register]float64{0: 0, 1: 2, 2: 2},
	},
	{
		name: "trunc towards zero", op: ic10.OpTrunc,
		source:        "trunc r0 -1.7\ntrunc r1 1.7",
		wantRegisters: map[ic10.Register]float64{0: -1, 1: 1},
	},
	{
		name: "ceil", op: ic10.OpCeil,
		source:        "ceil r0 -1.2\nceil r1 1.2",
		wantRegisters: map[ic10.Register]float64{0: -1, 1: 2},
	},
	{
		name: "floor", op: ic10.OpFloor,
		source:        "floor r0 -1.2\nfloor r1 1.2",
		wantRegisters: map[ic10.Register]float64{0: -2, 1: 1},
	},
	{
		name: "abs", op: ic10.OpAbs,
		source:        "abs r0 -3",
		wantRegisters: map[ic10.Register]float64{0: 3},
	},
	{
		name: "max propagates NaN", op: ic10.OpMax,
		registers:     map[ic10.Register]float64{1: math.NaN()},
		source:        "max r0 r1 5",
		wantRegisters: map[ic10.Register]float64{0: math.NaN()},
	},
	{
		name: "min propagates NaN", op: ic10.OpMin,
		registers:     map[ic10.Register]float64{1: math.NaN()},
		source:        "min r0 r1 5",
		wantRegisters: map[ic10.Register]float64{0: math.NaN()},
	},
	{
		name: "clamp propagates NaN", op: ic10.OpClamp,
		registers:     map[ic10.Register]float64{1: math.NaN()},
		source:        "clamp r0 r1 0 10",
		wantRegisters: map[ic10.Register]float64{0: math.NaN()},
	},
	{
		name: "clamp bounds", op: ic10.OpClamp,
		source:        "clamp r0 -5 0 10\nclamp r1 50 0 10\nclamp r2 5 0 10",
		wantRegisters: map[ic10.Register]float64{0: 0, 1: 10, 2: 5},
	},
	{
		name: "lerp clamps its interpolant rather than extrapolating", op: ic10.OpLerp,
		source:        "lerp r0 0 10 0.5\nlerp r1 0 10 2\nlerp r2 0 10 -1",
		wantRegisters: map[ic10.Register]float64{0: 5, 1: 10, 2: 0},
	},
	{
		name: "pow", op: ic10.OpPow,
		source:        "pow r0 2 10",
		wantRegisters: map[ic10.Register]float64{0: 1024},
	},
	{
		name: "log of zero is negative infinity", op: ic10.OpLog,
		source:        "log r0 0",
		wantRegisters: map[ic10.Register]float64{0: math.Inf(-1)},
	},
	{
		name: "exp of zero is one", op: ic10.OpExp,
		source:        "exp r0 0",
		wantRegisters: map[ic10.Register]float64{0: 1},
	},
	{
		name: "sgn treats NaN as zero", op: ic10.OpSgn,
		registers:     map[ic10.Register]float64{1: math.NaN()},
		source:        "sgn r0 -4\nsgn r2 7\nsgn r3 r1",
		wantRegisters: map[ic10.Register]float64{0: -1, 2: 1, 3: 0},
	},
	{
		name: "sin of zero", op: ic10.OpSin,
		source:        "sin r0 0",
		wantRegisters: map[ic10.Register]float64{0: 0},
	},
	{
		name: "cos of zero", op: ic10.OpCos,
		source:        "cos r0 0",
		wantRegisters: map[ic10.Register]float64{0: 1},
	},
	{
		name: "tan of zero", op: ic10.OpTan,
		source:        "tan r0 0",
		wantRegisters: map[ic10.Register]float64{0: 0},
	},
	{
		name: "asin of zero", op: ic10.OpAsin,
		source:        "asin r0 0",
		wantRegisters: map[ic10.Register]float64{0: 0},
	},
	{
		name: "acos of one", op: ic10.OpAcos,
		source:        "acos r0 1",
		wantRegisters: map[ic10.Register]float64{0: 0},
	},
	{
		name: "atan of zero", op: ic10.OpAtan,
		source:        "atan r0 0",
		wantRegisters: map[ic10.Register]float64{0: 0},
	},
	{
		name: "atan2 of zero over one", op: ic10.OpAtan2,
		source:        "atan2 r0 0 1",
		wantRegisters: map[ic10.Register]float64{0: 0},
	},
	{
		// rand is the one instruction differential testing has to pin: the game
		// draws from an unseeded System.Random no oracle can follow.
		name: "rand draws from the injected source", op: ic10.OpRand,
		source:        "rand r0",
		random:        func() float64 { return 0.25 },
		wantRegisters: map[ic10.Register]float64{0: 0.25},
	},
}

// TestModPinsItsBitPatterns compares mod bit for bit, which the instruction
// cases cannot: they go through sameFloat, which counts every NaN as equal and
// so would pass whatever payload the implementation happened to produce. The
// expectations are the C# operator's, and every one of them reproduces on
// ic10emu, whose Rust % reaches the same fmod.
func TestModPinsItsBitPatterns(t *testing.T) {
	const (
		dotnetNaNBits = 0xfff8000000000000
		goNaNBits     = 0x7ff8000000000001
		oddNaNBits    = 0x7ff8000000000007
		positiveInf   = 0x7ff0000000000000
		negativeInf   = 0xfff0000000000000
		negativeZero  = 0x8000000000000000
	)
	tests := []struct {
		name              string
		dividend, divisor uint64
		want              uint64
	}{
		{
			name:     "a NaN dividend propagates its own bit pattern",
			dividend: dotnetNaNBits, divisor: 0x4008000000000000, want: dotnetNaNBits,
		},
		{
			name:     "a NaN dividend Go would never manufacture survives unchanged",
			dividend: oddNaNBits, divisor: 0x4008000000000000, want: oddNaNBits,
		},
		{
			name:     "a NaN divisor propagates its own bit pattern",
			dividend: 0x4014000000000000, divisor: oddNaNBits, want: oddNaNBits,
		},
		{
			name:     "the dividend wins when both operands are NaN",
			dividend: goNaNBits, divisor: oddNaNBits, want: goNaNBits,
		},
		{
			name:     "an infinite dividend yields the .NET NaN",
			dividend: positiveInf, divisor: 0x4008000000000000, want: dotnetNaNBits,
		},
		{
			name:     "a zero divisor yields the .NET NaN",
			dividend: 0x4014000000000000, divisor: 0, want: dotnetNaNBits,
		},
		{
			name:     "an infinite divisor leaves a positive dividend alone",
			dividend: 0x4014000000000000, divisor: positiveInf, want: 0x4014000000000000,
		},
		{
			name:     "adding an infinite divisor back sends a negative dividend to infinity",
			dividend: 0xc014000000000000, divisor: positiveInf, want: positiveInf,
		},
		{
			name:     "a negative divisor sends a negative dividend further from zero",
			dividend: 0xc01c000000000000, divisor: 0xc008000000000000, want: 0xc010000000000000,
		},
		{
			name:     "a negative divisor leaves a positive dividend's remainder alone",
			dividend: 0x401c000000000000, divisor: 0xc008000000000000, want: 0x3ff0000000000000,
		},
		{
			name:     "an exact division keeps the dividend's negative zero",
			dividend: 0xbfe0000000000000, divisor: 0xbfd0000000000000, want: negativeZero,
		},
		{
			name:     "a negative infinite divisor keeps a negative dividend negative",
			dividend: 0xc014000000000000, divisor: negativeInf, want: negativeInf,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewMachine()
			if err := m.Load(t.Context(), "mod r1 r0 r2"); err != nil {
				t.Fatalf("Load: %v", err)
			}
			registers := m.Registers()
			registers[0] = math.Float64frombits(tt.dividend)
			registers[2] = math.Float64frombits(tt.divisor)
			m.SetRegisters(registers)

			if _, err := m.Tick(t.Context(), InstructionsPerTick); err != nil {
				t.Fatalf("Tick: %v", err)
			}
			if got := math.Float64bits(m.Register(1)); got != tt.want {
				t.Errorf("mod 0x%016x 0x%016x = 0x%016x, want 0x%016x",
					tt.dividend, tt.divisor, got, tt.want)
			}
		})
	}
}

// TestRandStaysInTheUnitInterval covers the default source, which the table
// case above replaces.
func TestRandStaysInTheUnitInterval(t *testing.T) {
	m := NewMachine()
	if err := m.Load(t.Context(), "rand r0"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := m.Tick(t.Context(), InstructionsPerTick); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got := m.Register(0); got < 0 || got >= 1 {
		t.Errorf("r0 = %v, want a value in [0, 1)", got)
	}
}
