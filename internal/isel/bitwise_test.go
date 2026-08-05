package isel

import (
	"fmt"
	"math"
	"testing"
)

// guardedMask is a program whose bitwise operand a range test alone bounds,
// which is the shape a device reading takes wherever it is used as an integer.
// The optimizer flattens the guard into a select and then folds the operator
// above that select, where the operand is the raw reading again.
const guardedMask = `const dev in = d0;
const dev out = d1;

void main(void) {
    while (true) {
        double v = __ic_load(in, Setting);
        long long n = 0;
        if (v > -1000000.0 && v < 1000000.0) {
            n = (long long)v;
        }
        __ic_store(out, Setting, (double)(%s));
        __ic_yield();
    }
}`

// TestGuardedBitwiseOperandSurvivesANonFiniteReading holds every bitwise
// operator to C's value when the guard above it rejects the reading. An operator
// computed above its guard meets an infinite reading, faults the conversion, and
// loses every write left in the tick — which only running the program shows.
func TestGuardedBitwiseOperandSurvivesANonFiniteReading(t *testing.T) {
	cases := []struct {
		name    string
		expr    string
		reading float64
		want    float64
	}{
		{name: "a mask over a positive infinity", expr: "n & 15", reading: math.Inf(1), want: 0},
		{name: "a mask over a negative infinity", expr: "n & 15", reading: math.Inf(-1), want: 0},
		{name: "a mask over a NaN", expr: "n & 15", reading: math.NaN(), want: 0},
		{name: "a mask over a reading past the guard", expr: "n & 15", reading: 1e300, want: 0},
		{name: "a mask over a reading the guard admits", expr: "n & 15", reading: 37.5, want: 5},
		{name: "an or over an infinity", expr: "n | 3", reading: math.Inf(1), want: 3},
		{name: "an or over a reading the guard admits", expr: "n | 3", reading: 12, want: 15},
		{name: "an exclusive or over an infinity", expr: "n ^ 9", reading: math.Inf(-1), want: 9},
		{name: "a left shift over an infinity", expr: "n << 2", reading: math.Inf(1), want: 0},
		{name: "a left shift over a reading the guard admits", expr: "n << 2", reading: 5, want: 20},
		{name: "a right shift over an infinity", expr: "n >> 1", reading: math.Inf(-1), want: 0},
		{name: "a complement over an infinity", expr: "~n", reading: math.Inf(1), want: -1},
		{name: "a complement over a reading the guard admits", expr: "~n", reading: 6, want: -7},
		{name: "a mask over a complement above the guard", expr: "(~n) & 7", reading: math.Inf(1), want: 7},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := fmt.Sprintf(guardedMask, tc.expr)
			assembly := compileSource(t, src)
			events := runWorld(t, assembly, func(t *testing.T, w *world) {
				w.set(t, 0, logicType(t, "Setting"), tc.reading)
			}, 1)
			assertWrote(t, events, 1, logicType(t, "Setting"), tc.want, assembly)
		})
	}
}

// TestGuardedBitwiseOperandHoldsAcrossACall covers the same shape with the
// guard and the operator in different functions, which is where the optimizer
// has the most freedom to move one past the other.
func TestGuardedBitwiseOperandHoldsAcrossACall(t *testing.T) {
	const src = `const dev in = d0;
const dev out = d1;

long long bounded(double v) {
    if (v > -1000000.0 && v < 1000000.0) {
        return (long long)v;
    }
    return 0;
}

void main(void) {
    while (true) {
        __ic_store(out, Setting, (double)(bounded(__ic_load(in, Setting)) & 255));
        __ic_yield();
    }
}`

	assembly := compileSource(t, src)
	events := runWorld(t, assembly, func(t *testing.T, w *world) {
		w.set(t, 0, logicType(t, "Setting"), math.Inf(1))
	}, 1)
	assertWrote(t, events, 1, logicType(t, "Setting"), 0, assembly)
}
