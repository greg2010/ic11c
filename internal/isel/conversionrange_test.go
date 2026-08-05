package isel

import (
	"math"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/isa"
)

// sharedArms is a switch whose two labels differ in one bit and reach one body.
// Neither this nor zeroArm reaches [selector.checkConversionRange] — the
// optimizer leaves a seq per label and an or, which [placedResult] places by
// type — so what they hold is the dispatch itself.
const sharedArms = `const dev in = d0;
const dev out = d1;

void main(void) {
    while (true) {
        long long n = (long long)__ic_load(in, Setting);
        long long r;
        switch (n) {
        case 3:
        case 7:
            r = 11;
            break;
        default:
            r = 33;
            break;
        }
        __ic_store(out, Setting, (double)r);
        __ic_yield();
    }
}`

// zeroArm is sharedArms with a label at zero, so that the reading the narrowing
// answers with zero has an arm to reach wrongly.
const zeroArm = `const dev in = d0;
const dev out = d1;

void main(void) {
    while (true) {
        long long n = (long long)__ic_load(in, Setting);
        long long r;
        switch (n) {
        case 0:
        case 4:
            r = 11;
            break;
        default:
            r = 33;
            break;
        }
        __ic_store(out, Setting, (double)r);
        __ic_yield();
    }
}`

// TestSwitchOverANonFiniteTagReachesTheDefaultArm holds the dispatch to the
// answer C computes for a reading no case label equals. A NaN converts to zero,
// which is a label's value as readily as any other, so the zero-label rows are
// what make that hazard observable at all.
func TestSwitchOverANonFiniteTagReachesTheDefaultArm(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		reading float64
		want    float64
	}{
		{name: "a positive infinity", src: sharedArms, reading: math.Inf(1), want: 33},
		{name: "a negative infinity", src: sharedArms, reading: math.Inf(-1), want: 33},
		{name: "a NaN", src: sharedArms, reading: math.NaN(), want: 33},
		{name: "a reading past the conversion range", src: sharedArms, reading: 1e300, want: 33},
		{name: "a reading below the conversion range", src: sharedArms, reading: -1e300, want: 33},
		{name: "a reading just past 2^63", src: sharedArms, reading: 9.3e18, want: 33},
		{name: "the first label", src: sharedArms, reading: 3, want: 11},
		{name: "the second label", src: sharedArms, reading: 7, want: 11},
		{name: "a reading between the labels", src: sharedArms, reading: 5, want: 33},
		{name: "a fractional reading the first label truncates to", src: sharedArms, reading: 3.75, want: 11},

		{name: "a NaN against an arm at zero", src: zeroArm, reading: math.NaN(), want: 33},
		{name: "a positive infinity against an arm at zero", src: zeroArm, reading: math.Inf(1), want: 33},
		{name: "a negative infinity against an arm at zero", src: zeroArm, reading: math.Inf(-1), want: 33},
		{name: "the arm at zero", src: zeroArm, reading: 0, want: 11},
		{name: "the other arm", src: zeroArm, reading: 4, want: 11},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assembly := compileSource(t, tc.src)
			events := runWorld(t, assembly, func(t *testing.T, w *world) {
				w.set(t, 0, logicType(t, "Setting"), tc.reading)
			}, 1)
			assertWrote(t, events, 1, logicType(t, "Setting"), tc.want, assembly)
		})
	}
}

// TestPlacedBitwiseOperandStillSelectsOneInstruction covers the two shapes the
// optimizer forms that selection can place, which must keep costing what they
// cost. Truth values are placed by type and the ring index by its bound, so
// neither reaches the guard; the and is asserted because the answer would not.
func TestPlacedBitwiseOperandStillSelectsOneInstruction(t *testing.T) {
	const truthValues = `const dev in = d0;
const dev out = d1;

void main(void) {
    while (true) {
        double v = __ic_load(in, Setting);
        __ic_store(out, On, v > 1.0 && v < 100.0);
        __ic_yield();
    }
}`

	const ringIndex = `const dev in = d0;
const dev out = d1;

long long cursor;
double samples[8];

void main(void) {
    while (true) {
        samples[cursor] = __ic_load(in, Setting);
        cursor = (cursor + 1) & 7;
        __ic_store(out, Setting, (double)cursor);
        __ic_yield();
    }
}`

	cases := []struct {
		name     string
		src      string
		reading  float64
		property string
		segments int
		want     float64
	}{
		{
			name:     "a conjunction of truth values over an infinity",
			src:      truthValues,
			reading:  math.Inf(1),
			property: "On",
			segments: 1,
		},
		{
			name:     "a conjunction of truth values over a reading inside the range",
			src:      truthValues,
			reading:  50,
			property: "On",
			segments: 1,
			want:     1,
		},
		{
			name:     "a mask that wraps an index, read after eight readings",
			src:      ringIndex,
			reading:  7.5,
			property: "Setting",
			segments: 8,
		},
		{
			name:     "a mask that wraps an index, read before it wraps",
			src:      ringIndex,
			reading:  7.5,
			property: "Setting",
			segments: 3,
			want:     3,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assembly := compileSource(t, tc.src)
			assertSelectsOpcode(t, assembly, isa.OpAnd)
			events := runWorld(t, assembly, func(t *testing.T, w *world) {
				w.set(t, 0, logicType(t, "Setting"), tc.reading)
			}, tc.segments)
			assertWrote(t, events, 1, logicType(t, tc.property), tc.want, assembly)
		})
	}
}

// TestTruncationToATruthValueNeedsAPlacedOperand covers the one bitwise
// instruction selection emits for something that is not a bitwise operation.
// Narrowing to one bit is the machine's and against 1, which reads its operand
// through the same conversion. Only the optimizer forms this shape.
func TestTruncationToATruthValueNeedsAPlacedOperand(t *testing.T) {
	const module = `
declare double @__ic_load(i64, i64)
declare void @__ic_store(i64, i64, double)
declare i64 @__ic_and(i64, i64)

define void @main() {
entry:
  %v = call double @__ic_load(i64 0, i64 12)
  %n = fptosi double %v to i64
  %source = SOURCE
  %bit = trunc i64 %source to i1
  %w = uitofp i1 %bit to double
  call void @__ic_store(i64 1, i64 12, double %w)
  ret void
}
`

	cases := []struct {
		name     string
		source   string
		accepted bool
	}{
		{
			name:   "read straight off a device",
			source: "add i64 %n, 0",
		},
		{
			name:     "masked before the narrowing",
			source:   "call i64 @__ic_and(i64 %n, i64 255)",
			accepted: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := parseIR(t, strings.Replace(module, "SOURCE", tc.source, 1))
			_, err := Select(t.Context(), m, Options{File: "case.c"})
			switch {
			case tc.accepted && err != nil:
				t.Fatalf("selection refused a truncation whose operand a mask placed: %v", err)
			case !tc.accepted && err == nil:
				t.Fatalf("selection accepted a truncation of a value it cannot place")
			}
		})
	}
}

// TestTheRefusalNamesTheBoundOfTheOperandItIsAbout holds the diagnostic to the
// conversion its own operand is read through. A shift carries both at once: its
// value through GetVariableLong, bounded at ±2^63, and its distance through
// GetVariableInt, bounded at -2^31 through 2^31-1.
func TestTheRefusalNamesTheBoundOfTheOperandItIsAbout(t *testing.T) {
	const module = `
declare double @__ic_load(i64, i64)
declare void @__ic_store(i64, i64, double)
declare i64 @__ic_and(i64, i64)

define void @main() {
entry:
  %v = call double @__ic_load(i64 0, i64 12)
  %n = fptosi double %v to i64
  OPERATION
  %w = sitofp i64 %r to double
  call void @__ic_store(i64 1, i64 12, double %w)
  ret void
}
`

	// The two ranges the machine's conversions carry a value through, spelled as
	// the diagnostics spell them.
	const (
		longRange = "±2^63"
		intRange  = "-2^31 through 2^31-1"
	)

	cases := []struct {
		name      string
		operation string
		// want is the range the refused operand's own conversion carries, and
		// unwanted the one another operand of the same instruction is read
		// through, which a per-instruction refusal would reach for.
		want, unwanted string
		accepted       bool
	}{
		{
			name:      "the value of a left shift",
			operation: "%r = shl i64 %n, 3",
			want:      longRange,
			unwanted:  intRange,
		},
		{
			name:      "the distance of a left shift",
			operation: "%r = shl i64 3, %n",
			want:      intRange,
			unwanted:  longRange,
		},
		{
			name:      "the value of an arithmetic right shift",
			operation: "%r = ashr i64 %n, 3",
			want:      longRange,
			unwanted:  intRange,
		},
		{
			name:      "the distance of an arithmetic right shift",
			operation: "%r = ashr i64 3, %n",
			want:      intRange,
			unwanted:  longRange,
		},
		{
			// srl reads its value through the unsigned reduction, which stops the
			// chip at the same magnitude as the signed one.
			name:      "the value of a logical right shift",
			operation: "%r = lshr i64 %n, 3",
			want:      longRange,
			unwanted:  intRange,
		},
		{
			name:      "the distance of a logical right shift",
			operation: "%r = lshr i64 3, %n",
			want:      intRange,
			unwanted:  longRange,
		},
		{
			// and has no int position at all, so naming the distance range would
			// name a bound the instruction carries nowhere.
			name:      "an operand of a conjunction",
			operation: "%r = and i64 %n, 255",
			want:      longRange,
			unwanted:  intRange,
		},
		{
			name:      "a distance the operand walk placed",
			operation: "%m = call i64 @__ic_and(i64 %n, i64 255)\n  %r = shl i64 3, %m",
			accepted:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := parseIR(t, strings.Replace(module, "OPERATION", tc.operation, 1))
			_, err := Select(t.Context(), m, Options{File: "case.c"})
			if tc.accepted {
				if err != nil {
					t.Fatalf("selection refused an operand the walk placed: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("selection accepted an operand it cannot place")
			}
			text := err.Error()
			if !strings.Contains(text, tc.want) {
				t.Errorf("the refusal does not name %s, the range this operand's own conversion carries: %s", tc.want, text)
			}
			if strings.Contains(text, tc.unwanted) {
				t.Errorf("the refusal names %s, which bounds another operand of the instruction and not this one: %s", tc.unwanted, text)
			}
		})
	}
}

// TestTheFloatArithmeticIsNoConduitForPlacement holds the half of
// [carriesPlacement]'s asymmetry nothing else states. Float forms reach an
// infinity by overflowing what a double holds, nine orders of magnitude past
// placement's ±2^63, and an infinity at a converted position stops the chip.
func TestTheFloatArithmeticIsNoConduitForPlacement(t *testing.T) {
	const module = `
declare double @__ic_load(i64, i64)
declare void @__ic_store(i64, i64, double)
declare i64 @__ic_and(i64, i64)

define void @main() {
entry:
  %v = call double @__ic_load(i64 0, i64 12)
  %n = fptosi double %v to i64
  %masked = call i64 @__ic_and(i64 %n, i64 255)
  %f = sitofp i64 %masked to double
  OPERATION
  %m = fptosi double %s to i64
  %r = and i64 %m, 255
  %w = sitofp i64 %r to double
  call void @__ic_store(i64 1, i64 12, double %w)
  ret void
}
`

	cases := []struct {
		name      string
		operation string
		accepted  bool
	}{
		{name: "a sum of placed values", operation: "%s = fadd double %f, %f"},
		{name: "a difference of placed values", operation: "%s = fsub double %f, 1.000000e+00"},
		{
			name:      "a product of placed values that overflows a double",
			operation: "%big = fmul double %f, 0x7E37E43C8800759C\n  %s = fmul double %big, 0x7E37E43C8800759C",
		},
		{
			// A negation moves no value out of the range it was in, which is
			// what separates it from the three above.
			name:      "a negation of a placed value",
			operation: "%s = fneg double %f",
			accepted:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := parseIR(t, strings.Replace(module, "OPERATION", tc.operation, 1))
			_, err := Select(t.Context(), m, Options{File: "case.c"})
			switch {
			case tc.accepted && err != nil:
				t.Fatalf("selection refused a bitwise operand it can place: %v", err)
			case !tc.accepted && err == nil:
				t.Fatalf("selection emitted a bitwise instruction over an operand that can hold an infinity, which stops the chip")
			case !tc.accepted:
				if text := err.Error(); !strings.Contains(text, "±2^63") {
					t.Errorf("the refusal does not name the range the conversion carries: %s", text)
				}
			}
		})
	}
}

// TestTheConversionRuleIsAskedAboutNoNarrowedPosition lets
// [selector.checkConversionRange] have no arm for ic10.ConversionNarrowedInt:
// that conversion is a cast an operation's body applies with no range check, so
// it carries no bound to name, and rmap alone has it and has no selection site.
func TestTheConversionRuleIsAskedAboutNoNarrowedPosition(t *testing.T) {
	asked := map[ic10.Opcode]bool{isa.OpAnd: true, isa.OpXor: true}
	for _, op := range binaryOps {
		asked[op] = true
	}
	for _, op := range predicateArithmetic {
		asked[op] = true
	}
	for op := range asked {
		instruction, known := op.Instruction()
		if !known {
			t.Errorf("the rule is asked about %s, which is not in the machine's table", op)
			continue
		}
		for position, operand := range instruction.Operands {
			if operand.Conversion == ic10.ConversionNarrowedInt {
				t.Errorf("%s of the %s is narrowed by the operation's own body, and the conversion rule has no bound to refuse it against",
					operandLabel(instruction, position), instruction.Mnemonic)
			}
		}
	}
}

// assertSelectsOpcode holds a program to still containing one instruction, which
// is what a guard spending instructions rather than refusing would have replaced.
func assertSelectsOpcode(t *testing.T, assembly string, op ic10.Opcode) {
	t.Helper()
	for line := range strings.SplitSeq(assembly, "\n") {
		if mnemonic, _, _ := strings.Cut(strings.TrimSpace(line), " "); mnemonic == op.String() {
			return
		}
	}
	t.Errorf("the program holds no %s instruction:\n%s", op, assembly)
}
