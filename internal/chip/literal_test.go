package chip

import (
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/emit"
	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/isa"
	"github.com/greg2010/ic11c/internal/mir"
	"github.com/greg2010/ic11c/internal/source"
)

// TestEmittedLiteralsLoadAsThemselves runs the emitter's own output through
// the game's parser and holds every value to the double it was rendered from.
// It's the only check that can catch a value rendered to the wrong text:
// Mono's double.TryParse is not correctly rounded and drops the sign of a
// zero, so only the chip can settle whether a rendering was right.
//
// Each value goes through the same two stages a compile ends with, including
// the values with no literal at all, which the pass turns into arithmetic
// this holds to the value it stands for.
//
// The table's rows are chosen for the corners a sample of random doubles
// misses — both zeros, both infinities, a NaN, the exponent range's ends, the
// 2^53 boundary, the named constants — since the miss this catches is a value
// class, not a rounding error.
func TestEmittedLiteralsLoadAsThemselves(t *testing.T) {
	ctx, harness := liveHarness(t)

	tests := []struct {
		name  string
		value float64
	}{
		{name: "a shortest rendering Mono read as c0de6137aa3c8efc", value: math.Float64frombits(0xc0de6137aa3c8efb)},
		{name: "a shortest rendering Mono read as c154347f83334cdf", value: math.Float64frombits(0xc154347f83334cde)},
		{name: "a shortest rendering Mono read as 41eca1819d90d448", value: math.Float64frombits(0x41eca1819d90d447)},
		{name: "a shortest rendering Mono read as bfb360175e56c759", value: math.Float64frombits(0xbfb360175e56c758)},
		{name: "a decimal at the gate's width", value: 1.234567890123},
		{name: "a decimal under the gate", value: 1.2345678901},
		{name: "an inexact integer past 2^53", value: 1.234567890123e18},
		{name: "two to the fortieth", value: 1099511627776},
		{name: "two to the forty-eighth", value: 281474976710656},
		{name: "the widest exact integer", value: 9007199254740992},
		{name: "two under it", value: 9007199254740990},
		{name: "a half at the gate's width", value: 12345678901.5},
		{name: "a quarter past it", value: 123456789012.25},
		{name: "one", value: 1},
		{name: "minus one", value: -1},
		{name: "a temperature", value: 273.15},
		{name: "a tenth, which no binary fraction spells", value: 0.1},
		{name: "positive zero", value: 0},
		{name: "negative zero", value: math.Copysign(0, -1)},
		{name: "not a number", value: math.NaN()},
		{name: "positive infinity", value: math.Inf(1)},
		{name: "negative infinity", value: math.Inf(-1)},
		// The smallest subnormal is the one value below the normal range that
		// reaches the chip: the parser's constant table carries it, and the emitter
		// picks that name over the 326 character expansion. Every other subnormal
		// needs a line no editor accepts — a refusal, checked in internal/emit.
		{name: "the smallest subnormal", value: math.SmallestNonzeroFloat64},
		{name: "one ulp under one", value: math.Nextafter(1, 0)},
		{name: "one ulp over one", value: math.Nextafter(1, 2)},
		{name: "a magnitude at the wide end of what a line holds", value: 1e70},
		{name: "a magnitude at the narrow end of what a line holds", value: 1e-70},
		{name: "a named constant", value: math.Pi},
		{name: "the widest named constant", value: 8.31446261815324},
		{name: "a named constant the game stores at float width", value: 0.01745329238474369},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			program := emitLoad(t, tt.value)
			observed, err := harness.Run(ctx, Request{Source: program})
			if err != nil {
				t.Fatalf("run %q: %v", program, err)
			}
			if observed.CompileError.Type != ExcNone {
				t.Fatalf("the chip refused %q: %v", program, observed.CompileError)
			}
			got := observed.Registers[0]
			// A NaN is held to being one rather than to its bits, the only value that
			// is: the division standing in for the literal computes the machine's own
			// NaN, whose payload comes from the C library Mono's Math forwards to, so
			// no payload this compiler chose could survive. Every other row compares
			// bits, which is what tells the two zeros apart.
			if math.IsNaN(tt.value) {
				if !math.IsNaN(got) {
					t.Errorf("the chip read %q as %v (%016x), want a NaN", program, got, math.Float64bits(got))
				}
				return
			}
			if math.Float64bits(got) != math.Float64bits(tt.value) {
				t.Errorf("the chip read %q as %v (%016x), want %v (%016x)",
					program, got, math.Float64bits(got), tt.value, math.Float64bits(tt.value))
			}
		})
	}
}

// emitLoad builds a one instruction program that loads value into r0, runs it
// through the two stages a compile ends with, and returns the assembly.
// Materialisation runs because it stands between a value with no literal and
// the emitter, which refuses one; skipping it would leave this test checking
// only the values that were never in question.
func emitLoad(t *testing.T, value float64) string {
	t.Helper()
	pos := source.Position{File: "literal.mc", Offset: 1, Line: 1, Column: 1}
	instr, err := mir.NewInstr(isa.OpMove, pos, mir.PhysReg{Reg: 0}, mir.Imm{Value: value})
	if err != nil {
		t.Fatalf("build the load: %v", err)
	}
	fn := mir.NewFunc("main", pos)
	fn.NewBlock("main.entry", pos).Append(instr)
	program := &mir.Program{Funcs: []*mir.Func{fn}}
	if err := mir.MaterialiseUnreadable(program); err != nil {
		t.Fatalf("materialise the load: %v", err)
	}
	out, err := emit.Emit(program, emit.Options{})
	if err != nil {
		t.Fatalf("emit the load: %v", err)
	}
	return out.Text
}

// TestNoLiteralNamesAnUnreadableValue is the evidence behind
// [ic10.Unreadable]: that table says two values have no literal, costing every
// program holding one a line of arithmetic. The claim is about Mono's
// double.TryParse and the chip's own constant table, which nothing in this
// repository can settle except by asking the chip. A row failing here doesn't
// mean the compiler is wrong — it means the game started reading a spelling it
// used to misread, and the entry it justified is no longer needed. The
// comparison is on bits, since a negative zero compares equal to a positive
// one.
func TestNoLiteralNamesAnUnreadableValue(t *testing.T) {
	ctx, harness := liveHarness(t)

	tests := []struct {
		name    string
		literal string
		// loads is the bit pattern the chip ends up with, and it is never the
		// one the spelling names. A row leaves it unset when the operand raises
		// instead, which leaves the register at the zero a reset put there.
		loads uint64
		// raises is the error the instruction reports, and ExcNone for a
		// spelling that loads the wrong value in silence.
		raises ExceptionType
	}{
		{name: "a bare negative zero", literal: "-0"},
		{name: "a negative zero with a fraction", literal: "-0.0"},
		{name: "a negative zero with two fractional digits", literal: "-0.00"},
		{name: "a negative zero with no leading digit", literal: "-.0"},
		{name: "a negative zero with no fractional digit", literal: "-0."},
		{name: "a negative zero written with a trailing sign", literal: "0-"},
		{name: "a magnitude too small to be anything but a zero", literal: "-0." + strings.Repeat("0", 330) + "1"},
		{name: "the constant table's own NaN", literal: "nan", raises: ExcIncorrectVariable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			program := "move r0 " + tt.literal + "\n"
			observed, err := harness.Run(ctx, Request{Source: program})
			if err != nil {
				t.Fatalf("run %q: %v", program, err)
			}
			if observed.CompileError.Type != ExcNone {
				t.Fatalf("the chip refused %q: %v", program, observed.CompileError)
			}
			if observed.Fault.Type != tt.raises {
				t.Errorf("the chip answered %q with %v, want %v", program, observed.Fault.Type, tt.raises)
			}
			got := math.Float64bits(observed.Registers[0])
			t.Logf("%q left r0 = %016x", program, got)
			if got != tt.loads {
				t.Errorf("the chip read %q as %016x, want %016x", program, got, tt.loads)
			}
		})
	}
}

// TestEveryUnreadableValueIsStillUnreadable is the other half of that
// evidence: it holds the values themselves, not a list of spellings, to
// having no literal at all — asking the emitter for each value's text would
// surface a spelling nobody wrote a row for above. No chip is needed, which is
// why this isn't behind [EnvEnabled]: the question is what the compiler
// ships.
func TestEveryUnreadableValueIsStillUnreadable(t *testing.T) {
	for _, value := range []float64{math.NaN(), math.Copysign(0, -1)} {
		t.Run(strconv.FormatUint(math.Float64bits(value), 16), func(t *testing.T) {
			if _, unreadable := ic10.Unreadable(value); !unreadable {
				t.Fatalf("ic10.Unreadable no longer names %016x", math.Float64bits(value))
			}
			program := emitLoad(t, value)
			if strings.HasPrefix(program, "move ") {
				t.Errorf("the emitter spelled %016x as the literal in %q, so the arithmetic it costs a line for is no longer needed",
					math.Float64bits(value), program)
			}
		})
	}
}
