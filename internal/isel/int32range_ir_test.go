package isel

import (
	"strings"
	"testing"
)

// rangeWalkModule is the module every case in
// [TestTheRangeWalkReadsEachRuleItStates] is stated as: fixed declarations, a
// body leaving the deviceHash in %hash, and a batch store to read it. A
// deviceHash is the plainest narrowed position the backend fills.
const rangeWalkModule = `
declare double @__ic_load(i64, i64)
declare void @__ic_store_batch(double, i64, double)
declare i64 @__ic_and(i64, i64)
declare double @__ic_round(double)
declare double @llvm.fabs.f64(double)
declare double @llvm.trunc.f64(double)
declare i64 @llvm.abs.i64(i64, i1)
declare double @__ic_shl(double, double)

GLOBALS

define void @main() {
entry:
BODY
  call void @__ic_store_batch(double %hash, i64 28, double 1.000000e+00)
  ret void
}
`

// TestTheRangeWalkReadsEachRuleItStates states each rule the narrowed refusal
// rests on as a pair of modules the rule alone separates. They are IR because
// every one is a shape MicroC has no spelling for or the optimizer canonicalises
// away from, so stating the module puts the rule in front of the walk directly.
func TestTheRangeWalkReadsEachRuleItStates(t *testing.T) {
	cases := []struct {
		name     string
		globals  string
		body     string
		accepted bool
	}{
		{
			// A bitwise result is placed, which says nothing about this position,
			// whose range is 2^32 times smaller.
			name: "an optimizer-formed conjunction over a device reading",
			body: `  %v = call double @__ic_load(i64 0, i64 12)
  %n = fptosi double %v to i64
  %placed = call i64 @__ic_and(i64 %n, i64 -1)
  %m = and i64 %placed, 255
  %hash = sitofp i64 %m to double`,
		},
		{
			// The same conjunction over operands the walk placed inside the
			// range, which has to keep compiling: an and that wraps an index is
			// the shape a ring buffer is written as.
			name: "an optimizer-formed conjunction over values inside the range",
			body: `  %placed = call i64 @__ic_and(i64 100, i64 255)
  %m = and i64 %placed, 255
  %hash = sitofp i64 %m to double`,
			accepted: true,
		},
		{
			name: "a guard spelled with an ordered comparison",
			body: `  %v = call double @__ic_load(i64 0, i64 12)
  %a = call double @llvm.fabs.f64(double %v)
  %c = fcmp olt double %a, 1.000000e+02
  %t = call double @llvm.trunc.f64(double %v)
  %hash = select i1 %c, double %t, double 0xC1DBB79564000000`,
			accepted: true,
		},
		{
			// An unordered comparison holds for a NaN, so the bound it appears to
			// state is not one. Only the predicate separates this from the row above.
			name: "a guard spelled with an unordered comparison",
			body: `  %v = call double @__ic_load(i64 0, i64 12)
  %a = call double @llvm.fabs.f64(double %v)
  %c = fcmp ult double %a, 1.000000e+02
  %t = call double @llvm.trunc.f64(double %v)
  %hash = select i1 %c, double %t, double 0xC1DBB79564000000`,
		},
		{
			// Zero is inside the guard's range, so the quotient is an infinity
			// out of operands the walk established.
			name: "a quotient of a value the guard bounded",
			body: `  %v = call double @__ic_load(i64 0, i64 12)
  %a = call double @llvm.fabs.f64(double %v)
  %c = fcmp olt double %a, 0x41E0000000000000
  %t = call double @llvm.trunc.f64(double %v)
  %d = select i1 %c, double %t, double 1.000000e+00
  %hash = fdiv double 1.000000e+00, %d`,
		},
		{
			// The same shape multiplying instead, which the walk does carry.
			name: "a product of a value the guard bounded",
			body: `  %v = call double @__ic_load(i64 0, i64 12)
  %a = call double @llvm.fabs.f64(double %v)
  %c = fcmp olt double %a, 0x41E0000000000000
  %t = call double @llvm.trunc.f64(double %v)
  %d = select i1 %c, double %t, double 1.000000e+00
  %hash = fmul double 1.000000e+00, %d`,
			accepted: true,
		},
		{
			// IR generation zero-initialises every object it puts in the data
			// region, so an initialiser it did not write leaves unknown contents.
			name:    "a load out of a global this stage did not initialise",
			globals: `@opaque = global double 4.000000e+00`,
			body:    `  %hash = load double, ptr @opaque`,
		},
		{
			name:     "a load out of a global this stage did initialise",
			globals:  `@zeroed = global double 0.000000e+00`,
			body:     `  %hash = load double, ptr @zeroed`,
			accepted: true,
		},
		{
			// Either comparison holding is enough, so neither end is stated.
			name: "a guard spelled as a disjunction",
			body: `  %v = call double @__ic_load(i64 0, i64 12)
  %lo = fcmp ogt double %v, -1.000000e+03
  %hi = fcmp olt double %v, 1.000000e+03
  %c = or i1 %lo, %hi
  %t = call double @llvm.trunc.f64(double %v)
  %hash = select i1 %c, double %t, double 0.000000e+00`,
		},
		{
			name: "the same guard spelled as a conjunction",
			body: `  %v = call double @__ic_load(i64 0, i64 12)
  %lo = fcmp ogt double %v, -1.000000e+03
  %hi = fcmp olt double %v, 1.000000e+03
  %c = and i1 %lo, %hi
  %t = call double @llvm.trunc.f64(double %v)
  %hash = select i1 %c, double %t, double 0.000000e+00`,
			accepted: true,
		},
		{
			// Both hold, so the value is held to the tighter of the two; keeping
			// the looser would refuse a program the stricter test made right.
			name: "a guard stating two lower bounds",
			body: `  %v = call double @__ic_load(i64 0, i64 12)
  %near = fcmp ogt double %v, -1.000000e+03
  %far = fcmp ogt double %v, -3.000000e+09
  %hi = fcmp olt double %v, 1.000000e+03
  %both = and i1 %near, %far
  %c = and i1 %both, %hi
  %t = call double @llvm.trunc.f64(double %v)
  %hash = select i1 %c, double %t, double 0.000000e+00`,
			accepted: true,
		},
		{
			name: "a guard stating two upper bounds",
			body: `  %v = call double @__ic_load(i64 0, i64 12)
  %lo = fcmp ogt double %v, -1.000000e+03
  %near = fcmp olt double %v, 1.000000e+03
  %far = fcmp olt double %v, 3.000000e+09
  %both = and i1 %near, %far
  %c = and i1 %both, %lo
  %t = call double @llvm.trunc.f64(double %v)
  %hash = select i1 %c, double %t, double 0.000000e+00`,
			accepted: true,
		},
		{
			// A magnitude over a range straddling zero reaches as far as the
			// further end, which is not either end's own magnitude.
			name: "a magnitude of a value the guard straddles zero with",
			body: `  %v = call double @__ic_load(i64 0, i64 12)
  %lo = fcmp ogt double %v, -2.147483647e+09
  %hi = fcmp olt double %v, 1.000000e+03
  %c = and i1 %lo, %hi
  %a = call double @llvm.fabs.f64(double %v)
  %s = fadd double %a, 1.000000e+03
  %hash = select i1 %c, double %s, double 0.000000e+00`,
		},
		{
			name: "a magnitude of a straddle the range holds",
			body: `  %v = call double @__ic_load(i64 0, i64 12)
  %lo = fcmp ogt double %v, -1.000000e+03
  %hi = fcmp olt double %v, 2.147483000e+09
  %c = and i1 %lo, %hi
  %a = call double @llvm.fabs.f64(double %v)
  %hash = select i1 %c, double %a, double 0.000000e+00`,
			accepted: true,
		},
		{
			// A magnitude over a range below zero is the range with its ends
			// swapped and negated, and not the range itself.
			name: "a magnitude of a value the guard holds below zero",
			body: `  %v = call double @__ic_load(i64 0, i64 12)
  %lo = fcmp ogt double %v, -2.147483647e+09
  %hi = fcmp olt double %v, -1.000000e+00
  %c = and i1 %lo, %hi
  %a = call double @llvm.fabs.f64(double %v)
  %s = fadd double %a, 1.000000e+03
  %hash = select i1 %c, double %s, double 0.000000e+00`,
		},
		{
			// Which way the machine breaks a tie is not settled, so round is
			// bounded by both neighbouring integers; the guard's high end would
			// otherwise round to 2^31.
			name: "a rounding of a value guarded to the whole range",
			body: `  %v = call double @__ic_load(i64 0, i64 12)
  %a = call double @llvm.fabs.f64(double %v)
  %c = fcmp olt double %a, 0x41E0000000000000
  %r = call double @__ic_round(double %v)
  %hash = select i1 %c, double %r, double 0.000000e+00`,
		},
		{
			name: "a rounding of a value guarded well inside the range",
			body: `  %v = call double @__ic_load(i64 0, i64 12)
  %a = call double @llvm.fabs.f64(double %v)
  %c = fcmp olt double %a, 1.000000e+03
  %r = call double @__ic_round(double %v)
  %hash = select i1 %c, double %r, double 0.000000e+00`,
			accepted: true,
		},
		{
			// The guard's high end is the largest integer the position carries,
			// so rounding up reaches it and no further; the row below is the same
			// guard one integer out, which is where the widening is decided.
			name: "a rounding of a value guarded to the ends of the range",
			body: `  %v = call double @__ic_load(i64 0, i64 12)
  %lo = fcmp oge double %v, -2.147483648e+09
  %hi = fcmp ole double %v, 2.147483647e+09
  %c = and i1 %lo, %hi
  %r = call double @__ic_round(double %v)
  %hash = select i1 %c, double %r, double 0.000000e+00`,
			accepted: true,
		},
		{
			name: "a rounding of a value guarded one integer past the high end",
			body: `  %v = call double @__ic_load(i64 0, i64 12)
  %lo = fcmp oge double %v, -2.147483648e+09
  %hi = fcmp ole double %v, 2.147483648e+09
  %c = and i1 %lo, %hi
  %r = call double @__ic_round(double %v)
  %hash = select i1 %c, double %r, double 0.000000e+00`,
		},
		{
			// llvm.abs carries an argument the machine instruction has no operand
			// for, with a type crossing either side of it.
			name: "a magnitude taken through the integer form and the crossings",
			body: `  %v = call double @__ic_load(i64 0, i64 12)
  %a = call double @llvm.fabs.f64(double %v)
  %c = fcmp olt double %a, 1.000000e+03
  %n = fptosi double %v to i64
  %m = call i64 @llvm.abs.i64(i64 %n, i1 false)
  %d = sitofp i64 %m to double
  %hash = select i1 %c, double %d, double 0.000000e+00`,
			accepted: true,
		},
		{
			name: "the same shape over a guard the range does not hold",
			body: `  %v = call double @__ic_load(i64 0, i64 12)
  %a = call double @llvm.fabs.f64(double %v)
  %c = fcmp olt double %a, 0x41F0000000000000
  %n = fptosi double %v to i64
  %m = call i64 @llvm.abs.i64(i64 %n, i1 false)
  %d = sitofp i64 %m to double
  %hash = select i1 %c, double %d, double 0.000000e+00`,
		},
		{
			// Both tests hold on the inner arm, so the value is held to the
			// narrower of the two ranges whichever of them states it.
			name: "a guard inside a looser one",
			body: `  %v = call double @__ic_load(i64 0, i64 12)
  %a = call double @llvm.fabs.f64(double %v)
  %outer = fcmp olt double %a, 3.000000e+09
  %inner = fcmp olt double %a, 1.000000e+03
  %t = call double @llvm.trunc.f64(double %v)
  %held = select i1 %inner, double %t, double 0.000000e+00
  %hash = select i1 %outer, double %held, double 0.000000e+00`,
			accepted: true,
		},
		{
			// The chip converts the distance to an integer before shifting, so a
			// distance held between two integers scales by as much as the one
			// above it. See [TestTheChipShiftsByTheLowSixBitsOfTheDistance].
			name: "a shift by a distance the guard holds between two integers",
			body: `  %v = call double @__ic_load(i64 0, i64 12)
  %lo = fcmp ogt double %v, 5.000000e-01
  %hi = fcmp olt double %v, 1.500000e+00
  %c = and i1 %lo, %hi
  %d = select i1 %c, double %v, double 0.000000e+00
  %hash = call double @__ic_shl(double 6.000000e+08, double %d)`,
		},
		{
			name: "the same shift over a scale the range holds either way",
			body: `  %v = call double @__ic_load(i64 0, i64 12)
  %lo = fcmp ogt double %v, 5.000000e-01
  %hi = fcmp olt double %v, 1.500000e+00
  %c = and i1 %lo, %hi
  %d = select i1 %c, double %v, double 0.000000e+00
  %hash = call double @__ic_shl(double 5.000000e+08, double %d)`,
			accepted: true,
		},
		{
			// The value is small enough that every scale in the window stays
			// inside the range, so the window and not the magnitude separates
			// this from the row below — which no long long is small enough to state.
			name: "a shift by a distance the guard holds at the high end of the window",
			body: `  %v = call double @__ic_load(i64 0, i64 12)
  %lo = fcmp ogt double %v, 6.200000e+01
  %hi = fcmp olt double %v, 6.300000e+01
  %c = and i1 %lo, %hi
  %d = select i1 %c, double %v, double 6.300000e+01
  %hash = call double @__ic_shl(double 1.000000e-15, double %d)`,
			accepted: true,
		},
		{
			// One integer further out and 64 shifts by none.
			name: "a shift by a distance the guard admits one integer past the window",
			body: `  %v = call double @__ic_load(i64 0, i64 12)
  %lo = fcmp ogt double %v, 6.200000e+01
  %hi = fcmp olt double %v, 6.400000e+01
  %c = and i1 %lo, %hi
  %d = select i1 %c, double %v, double 6.300000e+01
  %hash = call double @__ic_shl(double 1.000000e-15, double %d)`,
		},
		{
			// The other end, where the reading was wrong rather than coarse: -2
			// shifts left by 62, which is 2^64 times what an exact power states.
			name: "a shift by a distance the guard holds below zero",
			body: `  %v = call double @__ic_load(i64 0, i64 12)
  %lo = fcmp ogt double %v, -3.000000e+00
  %hi = fcmp olt double %v, -1.000000e+00
  %c = and i1 %lo, %hi
  %d = select i1 %c, double %v, double -2.000000e+00
  %hash = call double @__ic_shl(double 1.000000e+00, double %d)`,
		},
		{
			// The distance is a select over two literals because an llvm.Shl is
			// also asked whether selection can place its distance, and a device
			// reading is not placed: reaching this rule means passing that one.
			name: "an optimizer-formed shift by a distance below zero",
			body: `  %c = fcmp olt double 1.000000e+00, 2.000000e+00
  %d = select i1 %c, i64 -2, i64 -3
  %s = shl i64 1, %d
  %hash = sitofp i64 %s to double`,
		},
		{
			name: "an optimizer-formed shift by a distance inside the window",
			body: `  %c = fcmp olt double 1.000000e+00, 2.000000e+00
  %d = select i1 %c, i64 2, i64 3
  %s = shl i64 1, %d
  %hash = sitofp i64 %s to double`,
			accepted: true,
		},
		{
			// Read unsigned this is 2^64 short of what the module holds.
			name:     "a negative integer constant",
			body:     `  %hash = sitofp i64 -1000 to double`,
			accepted: true,
		},
		{
			name: "an integer constant past the range",
			body: `  %hash = sitofp i64 -3000000000 to double`,
		},
		{
			// More than two comparisons reach selection as a select over truth
			// values rather than an and, and both hold on the arm it admits.
			name: "a guard spelled as a select over truth values",
			body: `  %v = call double @__ic_load(i64 0, i64 12)
  %lo = fcmp ogt double %v, -1.000000e+03
  %hi = fcmp olt double %v, 1.000000e+03
  %c = select i1 %lo, i1 %hi, i1 false
  %t = call double @llvm.trunc.f64(double %v)
  %hash = select i1 %c, double %t, double 0.000000e+00`,
			accepted: true,
		},
		{
			// true where the conjunction has false, so it holds wherever the
			// first comparison fails and neither end is stated.
			name: "an implication spelled as a select over truth values",
			body: `  %v = call double @__ic_load(i64 0, i64 12)
  %lo = fcmp ogt double %v, -1.000000e+03
  %hi = fcmp olt double %v, 1.000000e+03
  %c = select i1 %lo, i1 %hi, i1 true
  %t = call double @llvm.trunc.f64(double %v)
  %hash = select i1 %c, double %t, double 0.000000e+00`,
		},
		{
			name: "a guard inside a tighter one",
			body: `  %v = call double @__ic_load(i64 0, i64 12)
  %a = call double @llvm.fabs.f64(double %v)
  %outer = fcmp olt double %a, 1.000000e+03
  %inner = fcmp olt double %a, 3.000000e+09
  %t = call double @llvm.trunc.f64(double %v)
  %held = select i1 %inner, double %t, double 0.000000e+00
  %hash = select i1 %outer, double %held, double 0.000000e+00`,
			accepted: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text := strings.Replace(rangeWalkModule, "GLOBALS", tc.globals, 1)
			text = strings.Replace(text, "BODY", tc.body, 1)
			m := parseIR(t, text)
			_, err := Select(t.Context(), m, Options{File: "case.c"})
			switch {
			case tc.accepted && err != nil:
				t.Fatalf("selection refused a deviceHash it can hold inside the range: %v", err)
			case !tc.accepted && err == nil:
				t.Fatalf("selection emitted a store whose deviceHash it cannot hold inside the signed 32-bit range the chip narrows that position to")
			case !tc.accepted:
				assertRefusalNames(t, err, "deviceHash")
			}
		})
	}
}
