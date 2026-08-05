package isel

import (
	"fmt"
	"math"
	"testing"

	"github.com/greg2010/ic11c/internal/chip"
	"github.com/greg2010/ic11c/internal/ic10"
)

// dividedReadings is a program whose dividend and divisor are both device
// readings, so nothing in the module bounds either away from zero. Two readings
// rather than one is what separates a division by a zero read from elsewhere
// from a division of a value by itself, which LLVM has an identity for.
const dividedReadings = `const dev in = d0;
const dev out = d1;

void main(void) {
    while (true) {
        long long a = (long long)__ic_load(in, Setting);
        long long b = (long long)__ic_load(in, Power);
        __ic_store(out, Setting, (double)(%s));
        __ic_yield();
    }
}`

// TestIntegerDivisionOfAReadingByItself covers the operands being the same zero
// value. The machine divides doubles, so `a / a` is 0.0/0.0 and a NaN, where
// over an LLVM i64 the same expression is 1 by identity — and InstCombine
// applies that identity, since nothing about the operands says it may not.
func TestIntegerDivisionOfAReadingByItself(t *testing.T) {
	cases := []struct {
		name        string
		expr        string
		setting     float64
		power       float64
		want        float64
		wantNonFini bool
	}{
		{name: "a zero reading divided by itself is a NaN", expr: "a / a", wantNonFini: true, want: math.NaN()},
		{name: "a zero reading remaindered by itself is a NaN", expr: "a % a", wantNonFini: true, want: math.NaN()},
		{name: "a reading divided by itself is one", expr: "a / a", setting: 7, want: 1},
		{name: "a reading remaindered by itself is zero", expr: "a % a", setting: 7, want: 0},
		{name: "a negative reading divided by itself is one", expr: "a / a", setting: -7, want: 1},
		{name: "a reading divided by a zero read separately is an infinity", expr: "a / b", setting: 5, want: math.Inf(1)},
		{name: "a reading remaindered by a zero read separately is a NaN", expr: "a % b", setting: 5, wantNonFini: true, want: math.NaN()},
		{name: "a division of two readings truncates toward zero", expr: "a / b", setting: -7, power: 2, want: -3},
		{name: "a remainder of two readings takes the dividend's sign", expr: "a % b", setting: -7, power: 2, want: -1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := fmt.Sprintf(dividedReadings, tc.expr)
			assembly := compileSource(t, src)
			events := runWorld(t, assembly, func(t *testing.T, w *world) {
				w.set(t, 0, logicType(t, "Setting"), tc.setting)
				w.set(t, 0, logicType(t, "Power"), tc.power)
			}, 1)
			if tc.wantNonFini {
				assertWroteNaN(t, events, 1, logicType(t, "Setting"), assembly)
				return
			}
			assertWrote(t, events, 1, logicType(t, "Setting"), tc.want, assembly)
		})
	}
}

// assertWroteNaN holds the last write to one property to being a NaN of any
// payload. [assertWrote] compares bit patterns, which is the wrong question
// here: which quiet pattern a NaN carries is a property of the operand order the
// backend chose rather than of the value C computes.
func assertWroteNaN(t *testing.T, events []chip.Write, pin int, property ic10.LogicType, assembly string) {
	t.Helper()
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		if event.Pin != pin || event.Property != int(property) || event.Slot != chip.NoSlot {
			continue
		}
		if !math.IsNaN(event.Value) {
			t.Errorf("the program wrote d%d %v = %v, want a NaN\n%s", pin, property, event.Value, assembly)
		}
		return
	}
	t.Errorf("the program made no write to d%d %v; it wrote %s\n%s", pin, property, describeWrites(events), assembly)
}
