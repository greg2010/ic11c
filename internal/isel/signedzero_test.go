package isel

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"
)

// TestIntegerDivisionWritesCZero holds an integer quotient of zero to the zero C
// computes, which has no sign. The machine divides doubles and rounds toward
// zero, and both steps carry the sign: 0.0 / -3.0 is -0.0 and trunc leaves it
// there, so a quotient C calls 0 reaches a device as -0.0.
func TestIntegerDivisionWritesCZero(t *testing.T) {
	cases := []struct {
		name    string
		expr    string
		setting float64
		power   float64
		want    float64
	}{
		{name: "zero over a negative literal", expr: "a / -3", want: 0},
		{name: "zero over a positive literal", expr: "a / 3", want: 0},
		{name: "a negative dividend truncating to zero over a positive literal", expr: "a / 3", setting: -1, want: 0},
		{name: "a positive dividend truncating to zero over a negative literal", expr: "a / -3", setting: 1, want: 0},
		{name: "zero over a negative reading", expr: "a / b", power: -3, want: 0},
		{name: "zero over a positive reading", expr: "a / b", power: 3, want: 0},
		{name: "a negative dividend truncating to zero over a positive reading", expr: "a / b", setting: -1, power: 3, want: 0},
		{name: "a positive dividend truncating to zero over a negative reading", expr: "a / b", setting: 1, power: -3, want: 0},
		{name: "a quotient that is not zero keeps its sign", expr: "a / b", setting: -7, power: 2, want: -3},
		{name: "a quotient of zero carried into a product", expr: "(a / -3) * 5", want: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runSignedZeroCase(t, tc.expr, tc.setting, tc.power, tc.want)
		})
	}
}

// TestIntegerRemainderWritesCZero holds an integer remainder of zero to C's
// zero. A subtraction answers -0.0 only for a -0.0 minuend against a +0.0
// subtrahend, and a - trunc(a/b)*b never forms that pair — a property of the
// identity rather than of the remainder, which a later change could re-choose.
func TestIntegerRemainderWritesCZero(t *testing.T) {
	cases := []struct {
		name    string
		expr    string
		setting float64
		power   float64
		want    float64
	}{
		{name: "zero over a negative literal", expr: "a % -3", want: 0},
		{name: "zero over a positive literal", expr: "a % 3", want: 0},
		{name: "a negative dividend dividing exactly by a positive literal", expr: "a % 3", setting: -6, want: 0},
		{name: "a positive dividend dividing exactly by a negative literal", expr: "a % -3", setting: 6, want: 0},
		{name: "zero over a negative reading", expr: "a % b", power: -3, want: 0},
		{name: "a negative dividend dividing exactly by a positive reading", expr: "a % b", setting: -6, power: 3, want: 0},
		{name: "a positive dividend dividing exactly by a negative reading", expr: "a % b", setting: 6, power: -3, want: 0},
		{name: "a remainder that is not zero takes the dividend's sign", expr: "a % b", setting: -1, power: 3, want: -1},
		{name: "a remainder of a quotient that is zero", expr: "(a / -3) % 5", want: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runSignedZeroCase(t, tc.expr, tc.setting, tc.power, tc.want)
		})
	}
}

// TestIntegerProductWritesCZero holds an integer product of zero to C's zero.
// IEEE signs a zero product by the operands disagreeing, and the product needs
// no rounding to get there, so the correction has nothing to follow but the
// multiply. A literal zero factor is not folded: the other factor can be a NaN.
func TestIntegerProductWritesCZero(t *testing.T) {
	cases := []struct {
		name    string
		expr    string
		setting float64
		power   float64
		want    float64
	}{
		{name: "a negative reading times a zero literal", expr: "a * 0", setting: -5, want: 0},
		{name: "a zero literal times a negative reading", expr: "0 * a", setting: -5, want: 0},
		{name: "a zero reading times a negative literal", expr: "a * -3", want: 0},
		{name: "a negative literal times a zero reading", expr: "-3 * a", want: 0},
		{name: "a negative reading times a zero reading", expr: "a * b", setting: -5, want: 0},
		{name: "a zero reading times a negative reading", expr: "a * b", power: -5, want: 0},
		{name: "two zero readings", expr: "a * b", want: 0},
		{name: "a product that is not zero keeps its sign", expr: "a * b", setting: -5, power: 3, want: -15},
		{name: "two negative readings multiply to a positive", expr: "a * b", setting: -5, power: -3, want: 15},
		{name: "a product of zero carried into a sum", expr: "(a * b) + 0", setting: -5, want: 0},
		{name: "a product of zero carried into a second product", expr: "(a * b) * 7", setting: -5, want: 0},
		{name: "a product of zero carried into a cast", expr: "(long long)(double)(a * b)", setting: -5, want: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runSignedZeroCase(t, tc.expr, tc.setting, tc.power, tc.want)
		})
	}
}

// TestIntegerArithmeticWritesCZeroThroughout sweeps the operators that carry no
// correction. They are safe by an induction, not a correction: no operator here
// hands a negative zero on, so those that answer one only for an operand that
// already is one never see it — which a later change breaks silently.
func TestIntegerArithmeticWritesCZeroThroughout(t *testing.T) {
	cases := []struct {
		name    string
		expr    string
		setting float64
		power   float64
		want    float64
	}{
		{name: "a sum of two zero readings", expr: "a + b", want: 0},
		{name: "a sum of a reading and its negation", expr: "a + (0 - a)", setting: -5, want: 0},
		{name: "a difference of equal readings", expr: "a - b", setting: -5, power: -5, want: 0},
		{name: "a difference of a reading from itself", expr: "a - a", setting: -5, want: 0},
		{name: "a negated zero reading", expr: "-a", want: 0},
		{name: "a negated zero carried into a product", expr: "-a * -3", want: 0},
		{name: "a left shift of a zero reading", expr: "a << b", power: 3, want: 0},
		{name: "a right shift down to zero", expr: "a >> b", setting: 3, power: 5, want: 0},
		{name: "a bitwise and down to zero", expr: "a & b", setting: -5, want: 0},
		{name: "a bitwise xor of equal readings", expr: "a ^ b", setting: -5, power: -5, want: 0},
		{name: "a quotient of zero carried into a difference", expr: "(a / -3) - 0", want: 0},
		{name: "a product of zero carried into a right shift", expr: "(a * b) >> 1", setting: -5, want: 0},
		{name: "a remainder of zero carried into a sum", expr: "(a % -3) + 0", want: 0},
		{name: "a cast of zero carried into a difference", expr: "(long long)(double)a - 0", setting: -0.5, want: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runSignedZeroCase(t, tc.expr, tc.setting, tc.power, tc.want)
		})
	}
}

// castReadings is the program the cast cases are written against: two device
// readings held as doubles, so the cast in the expression is the only rounding.
// [dividedReadings] has already cast both by the time an expression sees them,
// so a case written against that one has no double left to narrow.
const castReadings = `const dev in = d0;
const dev out = d1;

void main(void) {
    while (true) {
        double a = __ic_load(in, Setting);
        double b = __ic_load(in, Power);
        __ic_store(out, Setting, (double)(%s));
        __ic_yield();
    }
}`

// TestIntegerCastWritesCZero holds an explicit cast to long long to C's integer,
// whose zero has no sign. The machine's trunc keeps the sign of what it rounded,
// so every reading in (-1, 0) reaches a device as -0.0 where C writes 0. A
// literal operand is folded by the optimizer and a read one is not.
func TestIntegerCastWritesCZero(t *testing.T) {
	cases := []struct {
		name    string
		expr    string
		setting float64
		power   float64
		want    float64
	}{
		{name: "a literal between minus one and zero", expr: "(long long)(-0.5)", want: 0},
		{name: "a literal negative zero", expr: "(long long)(-0.0)", want: 0},
		{name: "a reading between minus one and zero", expr: "(long long)a", setting: -0.5, want: 0},
		{name: "a reading just above minus one", expr: "(long long)a", setting: -0.999, want: 0},
		{name: "a reading of negative zero", expr: "(long long)a", setting: math.Copysign(0, -1), want: 0},
		{name: "a positive reading below one", expr: "(long long)a", setting: 0.5, want: 0},
		{name: "a computed quotient between minus one and zero", expr: "(long long)(a / b)", setting: -1, power: 3, want: 0},
		{name: "a reading whose truncation is not zero keeps its sign", expr: "(long long)a", setting: -7.5, want: -7},
		{name: "a cast of zero carried into a product", expr: "(long long)a * 5", setting: -0.5, want: 0},
		{name: "a cast of zero carried into a second cast", expr: "(long long)(double)(long long)a", setting: -0.5, want: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runReadingCase(t, castReadings, tc.expr, tc.setting, tc.power, tc.want)
		})
	}
}

// TestDoubleNegationFlipsTheSignOfAZero is the one construct whose zero must
// come back signed. Selection lowers fneg as a multiply by -1 because mul
// carries a zero's sign across where subtracting from zero answers +0.0 for
// both — the opposite of what a truth value read as signed needs.
func TestDoubleNegationFlipsTheSignOfAZero(t *testing.T) {
	negativeZero := math.Copysign(0, -1)
	cases := []struct {
		name    string
		expr    string
		setting float64
		want    float64
	}{
		{name: "a positive zero negates to a negative zero", expr: "-a", want: negativeZero},
		{name: "a negative zero negates to a positive zero", expr: "-a", setting: negativeZero, want: 0},
		{name: "a positive reading negates to a negative one", expr: "-a", setting: 2.5, want: -2.5},
		{name: "a negative reading negates to a positive one", expr: "-a", setting: -2.5, want: 2.5},
		{name: "a positive zero negated twice comes back positive", expr: "-(-a)", want: 0},
		{name: "a negative zero negated twice comes back negative", expr: "-(-a)", setting: negativeZero, want: negativeZero},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runReadingCase(t, castReadings, tc.expr, tc.setting, 0, tc.want)
		})
	}
}

// runSignedZeroCase compiles one expression against [dividedReadings].
func runSignedZeroCase(t *testing.T, expr string, setting, power, want float64) {
	t.Helper()
	runReadingCase(t, dividedReadings, expr, setting, power, want)
}

// runReadingCase substitutes one expression into program, runs it on a chip
// against the two readings, and holds the device write to want's bit pattern.
func runReadingCase(t *testing.T, program, expr string, setting, power, want float64) {
	t.Helper()
	assembly := compileSource(t, fmt.Sprintf(program, expr))
	events := runWorld(t, assembly, func(t *testing.T, w *world) {
		w.set(t, 0, logicType(t, "Setting"), setting)
		w.set(t, 0, logicType(t, "Power"), power)
	}, 1)
	assertWrote(t, events, 1, logicType(t, "Setting"), want, assembly)
}

// conversionModule reads one device property, converts the comparison of that
// reading against 5 the way a case names, and writes the answer to a second
// device. It is IR because no MicroC source reaches either conversion: both
// spellings of "read this truth value as signed" arrive only from a rewrite.
const conversionModule = `
declare double @__ic_load(i64, i64)
declare void @__ic_store(i64, i64, double)

define void @main() {
entry:
  %v = call double @__ic_load(i64 0, i64 SETTING)
  %n = fptosi double %v to i64
  %c = icmp sgt i64 %n, 5
  CONVERSION
  call void @__ic_store(i64 1, i64 SETTING, double %w)
  ret void
}
`

// TestSignExtendedTruthValueWritesCZero holds a truth value read as signed to
// the two numbers LLVM states for it. A multiply by -1 answers -0.0 for a false
// condition where LLVM's sext of false is 0, so this subtracts from zero
// instead — the opposite choice from the fneg lowering, in a neighbouring arm.
func TestSignExtendedTruthValueWritesCZero(t *testing.T) {
	cases := []struct {
		name       string
		conversion string
		reading    float64
		want       float64
	}{
		{
			name:       "a false condition read as signed",
			conversion: "%w = sitofp i1 %c to double",
			reading:    0,
			want:       0,
		},
		{
			name:       "a true condition read as signed",
			conversion: "%w = sitofp i1 %c to double",
			reading:    10,
			want:       -1,
		},
		{
			name:       "a false condition sign extended",
			conversion: "%e = sext i1 %c to i64\n  %w = sitofp i64 %e to double",
			reading:    0,
			want:       0,
		},
		{
			name:       "a true condition sign extended",
			conversion: "%e = sext i1 %c to i64\n  %w = sitofp i64 %e to double",
			reading:    10,
			want:       -1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setting := logicType(t, "Setting")
			text := strings.NewReplacer(
				"SETTING", strconv.Itoa(int(setting)),
				"CONVERSION", tc.conversion,
			).Replace(conversionModule)
			assembly := assemble(t, parseIR(t, text))
			events := runWorld(t, assembly, func(t *testing.T, w *world) {
				w.set(t, 0, setting, tc.reading)
			}, 1)
			assertWrote(t, events, 1, setting, tc.want, assembly)
		})
	}
}

// remainderModule takes the remainder of one device reading by another, or by a
// literal, and writes it to a second device. It is IR because no MicroC source
// reaches [selector.lowerRem]; reading the dividend off a device is what lets a
// case hand it the negative zero the no-correction argument is about.
const remainderModule = `
declare double @__ic_load(i64, i64)
declare void @__ic_store(i64, i64, double)

define void @main() {
entry:
  %a = call double @__ic_load(i64 0, i64 SETTING)
  %b = call double @__ic_load(i64 0, i64 POWER)
  %x = fptosi double %a to i64
  %y = fptosi double %b to i64
  %r = srem i64 %x, DIVISOR
  %w = sitofp i64 %r to double
  call void @__ic_store(i64 1, i64 SETTING, double %w)
  ret void
}
`

// TestSelectedRemainderWritesCZero holds the synthesized remainder to C's zero
// over the operand its no-correction argument rests on. The cases put a negative
// zero on the dividend either side of the divisor's sign, which is where a
// lowering that re-chose the identity would answer the zero C cannot name.
func TestSelectedRemainderWritesCZero(t *testing.T) {
	negativeZero := math.Copysign(0, -1)
	cases := []struct {
		name    string
		divisor string
		setting float64
		power   float64
		want    float64
	}{
		{name: "a negative zero dividend over a positive literal", divisor: "3", setting: negativeZero, want: 0},
		{name: "a negative zero dividend over a negative literal", divisor: "-3", setting: negativeZero, want: 0},
		{name: "a negative zero dividend over a positive reading", divisor: "%y", setting: negativeZero, power: 3, want: 0},
		{name: "a negative zero dividend over a negative reading", divisor: "%y", setting: negativeZero, power: -3, want: 0},
		{name: "a positive zero dividend over a negative reading", divisor: "%y", power: -3, want: 0},
		{name: "a negative dividend dividing exactly", divisor: "%y", setting: -6, power: 3, want: 0},
		{name: "a negative dividend dividing exactly by a negative divisor", divisor: "%y", setting: -6, power: -3, want: 0},
		{name: "a remainder that is not zero takes the dividend's sign", divisor: "%y", setting: -7, power: 3, want: -1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setting, power := logicType(t, "Setting"), logicType(t, "Power")
			text := strings.NewReplacer(
				"SETTING", strconv.Itoa(int(setting)),
				"POWER", strconv.Itoa(int(power)),
				"DIVISOR", tc.divisor,
			).Replace(remainderModule)
			assembly := assemble(t, parseIR(t, text))
			events := runWorld(t, assembly, func(t *testing.T, w *world) {
				w.set(t, 0, setting, tc.setting)
				w.set(t, 0, power, tc.power)
			}, 1)
			assertWrote(t, events, 1, setting, tc.want, assembly)
		})
	}
}
