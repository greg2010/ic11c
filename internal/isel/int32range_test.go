package isel

import (
	"fmt"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/llvmir"
	"tinygo.org/x/go-llvm"
)

// guardedProgram is the shape every case in
// [TestTheIntervalReadingHoldsAValueToTheRangeItComputes] is written as: one
// device reading, one body, and a batch store on a narrowed deviceHash.
const guardedProgram = `const dev in = d0;

void main(void) {
    double v = __ic_load(in, Setting);
BODY
}`

// TestTheIntervalReadingHoldsAValueToTheRangeItComputes states each rule the
// interval reading rests on as a pair of programs the rule alone separates: one
// arithmetic form over two ranges, one the narrowing carries through as the
// number it is and one it does not.
func TestTheIntervalReadingHoldsAValueToTheRangeItComputes(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		refused bool
	}{
		{
			name: "a product of a guarded value",
			body: `    long long h = (v > -1000.0 && v < 1000.0) ? (long long)(v * 2.0) : 0;
    __ic_store_batch(h, On, 1.0);`,
		},
		{
			// The optimizer sinks the scale into the arms. Both spellings have to
			// compile, or the advice is one no arrangement of the source satisfies.
			name: "a product the optimizer sank into the arms",
			body: `    long long h = ((v > -1000.0 && v < 1000.0) ? (long long)v : 0) * 2;
    __ic_store_batch(h, On, 1.0);`,
		},
		{
			name:    "a product that leaves the range",
			refused: true,
			body: `    long long h = (v > -1000.0 && v < 1000.0) ? (long long)(v * 3000000.0) : 0;
    __ic_store_batch(h, On, 1.0);`,
		},
		{
			// A product over two ranges reaches furthest at a corner pairing one
			// range's low end with the other's high end, which is not a corner
			// either range's own ends make.
			name:    "a product of two guarded values reaching furthest at a crossed corner",
			refused: true,
			body: `    double w = __ic_load(in, Power);
    long long h = (v > -2000.0 && v < 1000.0 && w > 1000.0 && w < 2000000.0) ? (long long)(v * w) : 0;
    __ic_store_batch(h, On, 1.0);`,
		},
		{
			name: "a product of two guarded values whose every corner is inside",
			body: `    double w = __ic_load(in, Power);
    long long h = (v > -1000.0 && v < 1000.0 && w > 1000.0 && w < 2000000.0) ? (long long)(v * w) : 0;
    __ic_store_batch(h, On, 1.0);`,
		},
		{
			name: "a quotient by a divisor the reading holds clear of zero",
			body: `    long long n = (v > -1000.0 && v < 1000.0) ? (long long)v : 0;
    __ic_store_batch((long long)(2147483000.0 / ((double)n + 3000.0)), On, 1.0);`,
		},
		{
			name:    "a quotient clear of zero that leaves the range",
			refused: true,
			body: `    long long n = (v > -1000.0 && v < 1000.0) ? (long long)v : 0;
    __ic_store_batch((long long)(10000000000000.0 / ((double)n + 3000.0)), On, 1.0);`,
		},
		{
			// Zero is inside the range the guard states, so the quotient is an
			// infinity out of operands the reading established.
			name:    "a quotient by a divisor the guard admits zero into",
			refused: true,
			body: `    long long n = (v > -1000.0 && v < 1000.0) ? (long long)v : 0;
    __ic_store_batch((long long)(2147483000.0 / (double)n), On, 1.0);`,
		},
		{
			// Zero at the end of the divisor's range rather than inside it, which
			// is the boundary the rule is written at.
			name:    "a quotient by a divisor whose range ends at zero",
			refused: true,
			body: `    long long n = (v >= 0.0 && v <= 1000.0) ? (long long)v : 0;
    __ic_store_batch((long long)(2147483000.0 / (double)n), On, 1.0);`,
		},
		{
			name: "a quotient by a divisor whose range starts one over zero",
			body: `    long long n = (v >= 1.0 && v <= 1000.0) ? (long long)v : 1;
    __ic_store_batch((long long)(2147483000.0 / (double)n), On, 1.0);`,
		},
		{
			// The guard states the whole range the position carries, so a value
			// added to itself leaves it for every reading but zero.
			name:    "a sum of a value guarded to the whole range with itself",
			refused: true,
			body: `    long long n = (v > -2147483648.0 && v < 2147483648.0) ? (long long)v : 0;
    __ic_store_batch(n + n, On, 1.0);`,
		},
		{
			name: "a sum with itself inside bounds that hold it",
			body: `    long long n = (v > -1000.0 && v < 1000.0) ? (long long)v : 0;
    __ic_store_batch(n + n, On, 1.0);`,
		},
		{
			// The high end lands on 2147483646, one short of the largest integer
			// the position carries; the row below is one integer past it.
			name: "a sum with itself reaching the high end of the range",
			body: `    long long n = (v >= 0.0 && v <= 1073741823.0) ? (long long)v : 0;
    __ic_store_batch(n + n, On, 1.0);`,
		},
		{
			name:    "a sum with itself one integer past the high end",
			refused: true,
			body: `    long long n = (v >= 0.0 && v <= 1073741824.0) ? (long long)v : 0;
    __ic_store_batch(n + n, On, 1.0);`,
		},
		{
			// One admitted distance reaches 2^31, and nothing in the program is a
			// device reading past the range or a value that is not a number.
			name:    "a shift by a value guarded to the whole range",
			refused: true,
			body: `    long long n = (v > -2147483648.0 && v < 2147483648.0) ? (long long)v : 0;
    __ic_store_batch(((long long)1) << n, On, 1.0);`,
		},
		{
			name: "a shift by a distance the guard holds under the width",
			body: `    long long n = (v > 0.0 && v < 4.0) ? (long long)v : 0;
    __ic_store_batch(((long long)1) << n, On, 1.0);`,
		},
		{
			// A distance below zero is a large left shift, not a small right one:
			// -24 shifts left by 40. See
			// [TestTheChipShiftsByTheLowSixBitsOfTheDistance].
			name:    "a shift by a distance the guard holds below zero",
			refused: true,
			body: `    long long n = (v > -25.0 && v < -23.0) ? (long long)v : -24;
    __ic_store_batch(((long long)1) << n, On, 1.0);`,
		},
		{
			// One integer separates this from the row below, which is the whole
			// difference between a scale the instruction takes and one nothing does.
			name: "a shift by a distance the guard holds at the low end of the window",
			body: `    long long n = (v >= 0.0 && v <= 3.0) ? (long long)v : 0;
    __ic_store_batch(((long long)1) << n, On, 1.0);`,
		},
		{
			name:    "a shift by a distance the guard admits one integer below the window",
			refused: true,
			body: `    long long n = (v >= -1.0 && v <= 3.0) ? (long long)v : 0;
    __ic_store_batch(((long long)1) << n, On, 1.0);`,
		},
		{
			// The answer is smaller than the range states rather than larger,
			// which is why this row costs a refusal rather than saving one.
			name:    "a shift by a distance the guard admits past the width",
			refused: true,
			body: `    long long n = (v >= 63.0 && v <= 64.0) ? (long long)v : 63;
    __ic_store_batch(((long long)1) << n, On, 1.0);`,
		},
		{
			// The shape a program building a bit field writes: the distance is
			// what the window is about, the value what the range is about.
			name:    "a shift of a guarded value by a guarded distance below zero",
			refused: true,
			body: `    double w = __ic_load(in, Power);
    long long m = (v > -1000.0 && v < 1000.0) ? (long long)v : 0;
    long long d = (w > -3.0 && w < -1.0) ? (long long)w : -2;
    __ic_store_batch(m << d, On, 1.0);`,
		},
		{
			name: "a shift of a guarded value by a guarded distance inside the window",
			body: `    double w = __ic_load(in, Power);
    long long m = (v > -1000.0 && v < 1000.0) ? (long long)v : 0;
    long long d = (w > 1.0 && w < 3.0) ? (long long)w : 2;
    __ic_store_batch(m << d, On, 1.0);`,
		},
		{
			// The hole this leaves open is the counter's, and a scale by four is
			// the counter's own hole whether written as a shift or a product.
			name: "a counter shifted by a distance inside the window",
			body: `    for (long long i = 0; i < 3; i++) {
        __ic_store_batch(i << 2, On, 1.0);
    }`,
		},
		{
			// The same counter by a distance a reading holds past the window,
			// where there is no scale for its status to be carried through.
			name:    "a counter shifted by a distance past the window",
			refused: true,
			body: `    long long d = (v > 69.0 && v < 71.0) ? (long long)v : 70;
    for (long long i = 0; i < 3; i++) {
        __ic_store_batch(i << d, On, 1.0);
    }`,
		},
		{
			// The bounds are one-sided so that reading the subtrahend unnegated
			// answers a range this one does not.
			name: "a difference of a one-sided guarded value",
			body: `    long long h = (v > 0.0 && v < 1000.0) ? (long long)(2147483647.0 - v) : 0;
    __ic_store_batch(h, On, 1.0);`,
		},
		{
			name:    "a difference of a one-sided guarded value that leaves the range",
			refused: true,
			body: `    long long h = (v > 0.0 && v < 1000.0) ? (long long)(-2147483647.0 - v) : 0;
    __ic_store_batch(h, On, 1.0);`,
		},
		{
			// A negation swaps the ends, and the bounds reach the low end of the
			// range and not the high one.
			name:    "a negation of a value guarded to the low end of the range",
			refused: true,
			body: `    long long h = (v >= -2147483648.0 && v <= 0.0) ? (long long)(-v) : 0;
    __ic_store_batch(h, On, 1.0);`,
		},
		{
			name: "a negation of a value guarded one over the low end",
			body: `    long long h = (v >= -2147483647.0 && v <= 0.0) ? (long long)(-v) : 0;
    __ic_store_batch(h, On, 1.0);`,
		},
		{
			// The failing arm reaches the operand as much as the admitted one, so
			// the range the select states holds both.
			name:    "a guard whose other arm names a magnitude past the range",
			refused: true,
			body: `    long long h = (v > -1000.0 && v < 1000.0) ? (long long)v : 3000000000;
    __ic_store_batch(h, On, 1.0);`,
		},
		{
			// The magnitude is held above a bound rather than below one, which
			// bounds nothing: every large value and both infinities satisfy it.
			name:    "a magnitude held above a bound",
			refused: true,
			body: `    long long h = (__ic_abs(v) > 100.0) ? (long long)v : 0;
    __ic_store_batch(h, On, 1.0);`,
		},
		{
			name: "a magnitude held below a bound",
			body: `    long long h = (__ic_abs(v) < 100.0) ? (long long)v : 0;
    __ic_store_batch(h, On, 1.0);`,
		},
		{
			// The high end of the guard is well inside the range and the low end
			// is not, so a reading of one end alone answers the wrong way.
			name:    "a guard whose low end is past the range",
			refused: true,
			body: `    long long h = (v > -3000000000.0 && v < 1000.0) ? (long long)v : 0;
    __ic_store_batch(h, On, 1.0);`,
		},
		{
			// The inclusive spelling of the guard the refusal advises, which is
			// what a programmer writing the ends of the range out reaches for.
			name: "an inclusive guard on the ends of the range",
			body: `    long long h = (v >= -2147483648.0 && v <= 2147483647.0) ? (long long)v : 0;
    __ic_store_batch(h, On, 1.0);`,
		},
		{
			// 2^31 is not a signed 32-bit integer, so admitting the limit itself
			// admits a value the position does not carry. The difference between
			// this row and the one above is one ulp of the bound.
			name:    "an inclusive guard one past the high end",
			refused: true,
			body: `    long long h = (v >= -2147483648.0 && v <= 2147483648.0) ? (long long)v : 0;
    __ic_store_batch(h, On, 1.0);`,
		},
		{
			// The low end read the same way round: a value held strictly over
			// -2^31-1 truncates to -2^31, and one held at it does not.
			name: "a strict guard one past the low end",
			body: `    long long h = (v > -2147483649.0 && v < 2147483648.0) ? (long long)v : 0;
    __ic_store_batch(h, On, 1.0);`,
		},
		{
			name:    "an inclusive guard one past the low end",
			refused: true,
			body: `    long long h = (v >= -2147483649.0 && v < 2147483648.0) ? (long long)v : 0;
    __ic_store_batch(h, On, 1.0);`,
		},
		{
			name: "the high end of the range spelled as a literal",
			body: `    __ic_store_batch(2147483647, On, 1.0);`,
		},
		{
			name:    "the first magnitude past the high end",
			refused: true,
			body:    `    __ic_store_batch(2147483648, On, 1.0);`,
		},
		{
			// The low end is a magnitude the high end has no counterpart for, and
			// read as unsigned it reaches the operand 2^64 short.
			name: "the low end of the range spelled as a literal",
			body: `    __ic_store_batch(-2147483648, On, 1.0);`,
		},
		{
			name:    "the first magnitude past the low end",
			refused: true,
			body:    `    __ic_store_batch(-2147483649, On, 1.0);`,
		},
		{
			// The optimizer folds a division by zero into a constant, and an
			// infinity is a constant no range holds.
			name:    "a hash the optimizer folded to an infinity",
			refused: true,
			body: `    double z = 0.0;
    __ic_store_batch((long long)(1.0 / z), On, 1.0);`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := selectSource(t, strings.Replace(guardedProgram, "BODY", tc.body, 1))
			switch {
			case tc.refused && err == nil:
				t.Fatalf("selection emitted a store whose deviceHash it cannot hold inside the signed 32-bit range the chip narrows that position to")
			case !tc.refused && err != nil:
				t.Fatalf("selection refused a deviceHash it can hold inside the range: %v", err)
			case tc.refused:
				assertRefusalNames(t, err, "deviceHash")
			}
		})
	}
}

// TestACounterNoReadingBoundsStillCompiles holds the concession the interval
// reading leaves standing. A loop counter is a phi, which a reading over
// operands cannot bound — what holds it is the loop's exit test, a fact about
// control — so the arithmetic over one is carried rather than read.
func TestACounterNoReadingBoundsStillCompiles(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{
			name: "a counter reaching the operand as it is",
			body: `__ic_store_batch_named(__ic_hash("%s"), i, On, 1.0);`,
		},
		{
			// No reading covers this either, since its argument is the phi. What
			// carries it is the instruction's own inability to invent a number.
			name: "a counter through a shaping call",
			body: `__ic_store_batch_named(__ic_hash("%s"), (long long)__ic_abs((double)i), On, 1.0);`,
		},
		{
			name: "a counter through a shift",
			body: `__ic_store_batch_named(__ic_hash("%s"), ((long long)1) << i, On, 1.0);`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := fmt.Sprintf(`void main(void) {
    for (long long i = 0; i < 3; i++) {
        %s
    }
}`, fmt.Sprintf(tc.body, litHash))
			if _, err := selectSource(t, src); err != nil {
				t.Fatalf("selection refused a counted loop, which no reading bounds and every program writing a batch per turn is written as: %v", err)
			}
		})
	}
}

// TestTheChipShiftsByTheLowSixBitsOfTheDistance is the machine fact every shift
// window in this package rests on, taken off a chip rather than the game source.
// The chip converts the distance to a signed 32-bit integer and then shifts by
// the low six bits of it, so -24 shifts left by 40 and 64 shifts by none.
func TestTheChipShiftsByTheLowSixBitsOfTheDistance(t *testing.T) {
	const shifted = `l r0 d0 Setting
sll r0 1 r0
s d1 Setting r0`

	cases := []struct {
		name     string
		distance float64
		want     float64
	}{
		{name: "a distance inside the window shifts by itself", distance: 3, want: 8},
		{name: "the high end of the window", distance: 63, want: 0},
		{name: "one past the high end shifts by none", distance: 64, want: 1},
		{name: "one below zero shifts by 63", distance: -1, want: 0},
		{name: "the distance the reading called a scale of 2^-24", distance: -24, want: 1099511627776},
	}

	setting := logicType(t, "Setting")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events := runWorld(t, shifted, func(t *testing.T, w *world) {
				w.set(t, 0, setting, tc.distance)
			}, 1)
			got, written := writtenValue(events, 1, setting)
			if !written {
				t.Fatalf("the program wrote no Setting on d1\n%s", shifted)
			}
			if got != tc.want {
				t.Errorf("shifting 1 left by %v = %v, want %v\n%s", tc.distance, got, tc.want, shifted)
			}
		})
	}
}

// TestTheIntervalReadingVisitsEachValueOnce holds both readings under a guarded
// select to a cost the module's size states rather than its shape. Operands are
// a DAG, so a reading that followed each path would cover a chain of n
// two-operand instructions 2^n times, once per sweep of [valueflow.Run].
func TestTheIntervalReadingVisitsEachValueOnce(t *testing.T) {
	shapes := []struct {
		name  string
		build func(depth int) string
	}{
		{name: "a tree of sums the arm computes", build: sumTreeModule},
		{name: "a tree of conjunctions the condition states", build: conjunctionTreeModule},
	}

	for _, shape := range shapes {
		for _, depth := range []int{4, 12, 28} {
			t.Run(fmt.Sprintf("%s %d deep", shape.name, depth), func(t *testing.T) {
				m := parseIR(t, shape.build(depth))
				values := countValues(m)
				walk := newInt32Range()
				walk.run(m)
				visited := maxRangeNodes - walk.budget
				if visited > 4*values {
					t.Fatalf("the reading visited %d values in a module holding %d, which is a cost the shape of the program sets rather than its size", visited, values)
				}
			})
		}
	}
}

// countValues is how many instructions the module holds, which is what the
// reading's cost is stated against.
func countValues(m llvm.Module) int {
	values := 0
	for range llvmir.ModuleInstrs(m) {
		values++
	}
	return values
}

// sumTreeModule is a body whose sums each read both of the pair before them, so
// the value at the top is reachable by 2^depth paths and 2*depth instructions.
// The arm of a guarded select is read for the range it computes, which is what
// puts the tree under the reading.
func sumTreeModule(depth int) string {
	var body strings.Builder
	body.WriteString(`  %v = call double @__ic_load(i64 0, i64 12)
  %x0 = fadd double %v, 0.000000e+00
  %y0 = fadd double %v, 1.000000e+00
`)
	for i := 1; i <= depth; i++ {
		fmt.Fprintf(&body, "  %%x%d = fadd double %%x%d, %%y%d\n", i, i-1, i-1)
		fmt.Fprintf(&body, "  %%y%d = fsub double %%x%d, %%y%d\n", i, i-1, i-1)
	}
	fmt.Fprintf(&body, `  %%a = call double @llvm.fabs.f64(double %%v)
  %%c = fcmp olt double %%a, 0x41E0000000000000
  %%hash = select i1 %%c, double %%x%d, double 0.000000e+00`, depth)
	return intervalModule(body.String())
}

// conjunctionTreeModule is the same shape over the condition: each conjunction
// reads both of the pair before it, so the truth value the select tests is
// reachable by 2^depth paths through 2*depth of them.
func conjunctionTreeModule(depth int) string {
	var body strings.Builder
	body.WriteString(`  %v = call double @__ic_load(i64 0, i64 12)
  %t = call double @llvm.trunc.f64(double %v)
  %p0 = fcmp ogt double %v, -1.000000e+03
  %q0 = fcmp olt double %v, 1.000000e+03
`)
	for i := 1; i <= depth; i++ {
		fmt.Fprintf(&body, "  %%p%d = and i1 %%p%d, %%q%d\n", i, i-1, i-1)
		fmt.Fprintf(&body, "  %%q%d = and i1 %%q%d, %%p%d\n", i, i-1, i-1)
	}
	fmt.Fprintf(&body, "  %%hash = select i1 %%p%d, double %%t, double 0.000000e+00", depth)
	return intervalModule(body.String())
}

func intervalModule(body string) string {
	return strings.Replace(strings.Replace(rangeWalkModule, "GLOBALS", "", 1), "BODY", body, 1)
}

// TestArithmeticAfterTheTestLosesTheWriteItWasFor is why the sum of a guarded
// value with itself is refused, stated as what the machine does with the program
// that used to compile: the test holds the reading inside the range and the
// addition after it moves the operand back out for every reading but zero.
func TestArithmeticAfterTheTestLosesTheWriteItWasFor(t *testing.T) {
	hash := ic10.HashName(litHash)
	doubled := `l r0 d0 Setting
abs r1 r0
slt r1 r1 2147483648
trunc r0 r0
select r0 r1 r0 0
add r0 r0 r0
move r1 1
sb r0 On r1`

	cases := []struct {
		name    string
		reading float64
		written bool
	}{
		{name: "a reading whose doubling is the hash the program named", reading: float64(hash) / 2, written: true},
		{name: "a reading whose doubling is past the range", reading: 1.5e9},
	}

	on := logicType(t, "On")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events := runWorld(t, doubled, func(t *testing.T, w *world) {
				w.setHashes(t, 0, ic10.HashName(litHash), 0)
				w.set(t, 0, logicType(t, "Setting"), tc.reading)
			}, 1)
			for pin := range 3 {
				if got := wroteProperty(events, pin, on); got != (tc.written && pin == 0) {
					t.Errorf("d%d written = %v, want %v\n%s", pin, got, tc.written && pin == 0, doubled)
				}
			}
		})
	}
}
