package isel

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/isa"
	"tinygo.org/x/go-llvm"
)

// maskedPointer is a pointer into one array built from two masked indices, with
// the expression under test in the one place a program writes. The single write
// is what leaves the optimizer free to state the comparison over the byte offset
// alone, and the array is as long as the two masks reach.
const maskedPointer = `const dev in = d0;
const dev out = d1;

long long ring[31];

void main(void) {
    while (true) {
        long long i = (long long)__ic_load(in, Setting);
        long long j = (long long)__ic_load(in, Pressure);
        long long *p = &ring[i & 15];
        p = p + (j & 15);
        __ic_store(out, Setting, EXPR);
        __ic_yield();
    }
}`

// pointerReading is one pair of device readings a masked index is built from,
// and what C computes for the expression under them.
type pointerReading struct {
	name   string
	first  float64
	second float64
	want   float64
}

// TestPointerComparedAgainstAFixedElementAnswersOnTheChip covers the comparisons
// the optimizer restates as a mask against the full width divided by the stride,
// which is a constant past 2^53 that no emitted literal names. The difference
// and the ordering are here for not being restated that way.
func TestPointerComparedAgainstAFixedElementAnswersOnTheChip(t *testing.T) {
	cases := []struct {
		name     string
		expr     string
		readings []pointerReading
	}{
		{
			name: "equality against an element in the middle",
			expr: "p == (ring + 22)",
			readings: []pointerReading{
				{name: "the element it names", first: 7, second: 15, want: 1},
				{name: "one element short of it", first: 7, second: 14},
				{name: "the first element", first: 0, second: 0},
				{name: "the far end of what the masks reach", first: 15, second: 15},
			},
		},
		{
			name: "inequality against the far end of what the masks reach",
			expr: "p != (ring + 30)",
			readings: []pointerReading{
				{name: "the element it names", first: 15, second: 15},
				{name: "the first element", first: 0, second: 0, want: 1},
				{name: "readings past the masks, which wrap to the first element", first: 16, second: 32, want: 1},
				{name: "negative readings, which the masks bring back to the far end", first: -1, second: -1},
			},
		},
		{
			name: "equality against the first element of the object",
			expr: "p == ring",
			readings: []pointerReading{
				{name: "both indices zero", first: 0, second: 0, want: 1},
				{name: "readings past the masks, which wrap to it", first: 16, second: 32, want: 1},
				{name: "one element past it", first: 0, second: 1},
				{name: "negative readings, which the masks carry away from it", first: -1, second: -1},
			},
		},
		{
			name: "an ordering, which is not restated over a mask",
			expr: "p < (ring + 22)",
			readings: []pointerReading{
				{name: "below the element it names", first: 7, second: 14, want: 1},
				{name: "the element it names", first: 7, second: 15},
				{name: "above it", first: 15, second: 15},
			},
		},
		{
			name: "a difference, which is not restated over a mask",
			expr: "(double)(p - (ring + 16))",
			readings: []pointerReading{
				{name: "below the element it names, which is a negative difference", first: 0, second: 0, want: -16},
				{name: "the element it names", first: 8, second: 8},
				{name: "above it", first: 15, second: 15, want: 14},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assembly := compileSource(t, strings.Replace(maskedPointer, "EXPR", tc.expr, 1))
			for _, reading := range tc.readings {
				t.Run(reading.name, func(t *testing.T) {
					events := runWorld(t, assembly, func(t *testing.T, w *world) {
						w.set(t, 0, logicType(t, "Setting"), reading.first)
						w.set(t, 0, logicType(t, "Pressure"), reading.second)
					}, 1)
					assertWrote(t, events, 1, logicType(t, "Setting"), reading.want, assembly)
				})
			}
		})
	}
}

// TestARedundantMaskSelectsAsTheValueItMasks holds the rule to being about the
// operation rather than the constant: a mask whose value has no bit outside it
// computes its own operand. The optimizer cannot fold these two together — the
// operators are declarations, so it may not speculate them.
func TestARedundantMaskSelectsAsTheValueItMasks(t *testing.T) {
	const nestedMasks = `const dev in = d0;
const dev out = d1;

void main(void) {
    while (true) {
        long long n = (long long)__ic_load(in, Setting);
        __ic_store(out, Setting, (double)((n & 7) & 15));
        __ic_yield();
    }
}`

	assembly := compileSource(t, nestedMasks)
	if masks := strings.Count(assembly, ic10.Opcode(isa.OpAnd).String()+" "); masks != 1 {
		t.Errorf("the program holds %d and instructions, want the inner mask alone:\n%s", masks, assembly)
	}
	events := runWorld(t, assembly, func(t *testing.T, w *world) {
		w.set(t, 0, logicType(t, "Setting"), 13)
	}, 1)
	assertWrote(t, events, 1, logicType(t, "Setting"), 5, assembly)
}

// constantIdentity writes one bitwise identity over one constant. The value
// reaches the operator through a local rather than as a literal because analysis
// refuses a bitwise operator whose constant operand the source names, and a
// local is not a constant expression — which puts the boundary at selection.
const constantIdentity = `const dev out = d1;

void main(void) {
    while (true) {
        DECLS
        long long value = VALUE;
        __ic_store(out, Setting, (double)(value OP));
        __ic_yield();
    }
}`

// identitySpelling is one of the three operators that leave a value alone, with
// the constant that makes it do so.
type identitySpelling struct {
	name string
	expr string
	// mask says the spelling is the one [selector.identityMask] can remove, so
	// a case can hold the removal as well as the answer.
	mask bool
}

// TestABitwiseIdentityOverAConstantAnswersWhatTheMachineAnswers holds the mask
// fold to the value the chip produces rather than to whether it fired: a fold
// that removed a mask the machine would have kept is a program whose and
// disagrees with its own or and xor. The cases sit on the 2^53 boundary.
func TestABitwiseIdentityOverAConstantAnswersWhatTheMachineAnswers(t *testing.T) {
	spellings := []identitySpelling{
		{name: "an and against the full-width mask", expr: "& -1", mask: true},
		{name: "an or against zero", expr: "| 0"},
		{name: "an xor against zero", expr: "^ 0"},
	}

	cases := []struct {
		name string
		// decls declares whatever locals value reads, empty where value is a
		// constant expression. Analysis holds a folded constant to the same
		// ±2^53 window it holds a literal to, so a magnitude past that reaches
		// selection only by arithmetic over a mutable local.
		decls string
		// value is the initializer that puts the constant in the local. The two
		// at the modulus are sums because a literal that magnitude is a constant
		// the language holds and the sum is one it folds.
		value string
		want  float64
		// elided says the walk bounds the constant and the mask covers it, so
		// the and spelling reaches no emitted line.
		elided bool
		// refused says the constant is one the conversion sends to zero, which
		// no spelling answers for and selection reports instead.
		refused bool
	}{
		{
			name:   "the largest magnitude the conversion carries",
			value:  "9007199254740991",
			want:   9007199254740991,
			elided: true,
		},
		{
			name:    "one past it, which the conversion sends to zero",
			value:   "9007199254740991 + 1",
			refused: true,
		},
		{
			name:  "its negation, which no bound covers",
			value: "-9007199254740991",
			want:  -9007199254740991,
		},
		{
			name:    "one past it below zero, which the conversion sends to zero",
			value:   "-9007199254740991 - 1",
			refused: true,
		},
		{
			// The reduction is not a test for a multiple: three halves of the
			// modulus reaches the instruction as the remaining half.
			name:    "three halves of the modulus, which the conversion leaves a residue of",
			decls:   "long long h = 4503599627370496;",
			value:   "h + h + h",
			refused: true,
		},
		{
			name:    "2^60, which the conversion sends to zero seven doublings out",
			decls:   "long long h = 4503599627370496;",
			value:   "h * 256",
			refused: true,
		},
		{
			name:    "the negation of 2^60",
			decls:   "long long h = -4503599627370496;",
			value:   "h * 256",
			refused: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, spelling := range spellings {
				t.Run(spelling.name, func(t *testing.T) {
					src := strings.Replace(constantIdentity, "DECLS", tc.decls, 1)
					src = strings.Replace(src, "VALUE", tc.value, 1)
					src = strings.Replace(src, "OP", spelling.expr, 1)
					if tc.refused {
						_, err := selectSource(t, src)
						if err == nil {
							t.Fatalf("selection accepted %q over a constant the conversion sends to zero", spelling.expr)
						}
						return
					}
					assembly := compileSource(t, src)
					if spelling.mask {
						assertMaskCount(t, assembly, tc.elided)
					}
					events := runWorld(t, assembly, func(*testing.T, *world) {}, 1)
					assertWrote(t, events, 1, logicType(t, "Setting"), tc.want, assembly)
				})
			}
		})
	}
}

// assertMaskCount holds the and spelling to reaching the chip exactly when the
// walk did not bound the value it masks.
func assertMaskCount(t *testing.T, assembly string, elided bool) {
	t.Helper()
	want := 1
	if elided {
		want = 0
	}
	if masks := strings.Count(assembly, ic10.Opcode(isa.OpAnd).String()+" "); masks != want {
		t.Errorf("the program holds %d and instructions, want %d:\n%s", masks, want, assembly)
	}
}

// maskedIndex is the shape the optimizer leaves a pointer comparison as, with
// the index the mask is taken over in the one place a case varies. It is IR
// because no source asks for it: the mask is what a rewrite of a comparison
// against a fixed element produces.
const maskedIndex = `
declare double @__ic_load(i64, i64)
declare void @__ic_store(i64, i64, double)
declare i64 @__ic_and(i64, i64)
declare i64 @__ic_or(i64, i64)

define void @main() {
entry:
  %v = call double @__ic_load(i64 0, i64 12)
  %n = fptosi double %v to i64
INDEX
  %masked = and i64 %index, MASK
  %eq = icmp eq i64 %masked, 22
  %w = uitofp i1 %eq to double
  call void @__ic_store(i64 1, i64 12, double %w)
  ret void
}
`

// TestAMaskPastTheExactRangeNeedsAnIndexItCovers holds the bound to being what
// removes the mask. A mask is dropped when every value its operand can hold
// passes through unchanged; an operand the walk cannot bound keeps the
// diagnostic, which is all the machine can offer for a number no register holds.
func TestAMaskPastTheExactRangeNeedsAnIndexItCovers(t *testing.T) {
	// twoPow61Minus1 is the full-width mask divided by the eight bytes a slot
	// is, which is the constant a comparison against a fixed element leaves.
	const twoPow61Minus1 = "2305843009213693951"

	cases := []struct {
		name     string
		index    string
		mask     string
		accepted bool
	}{
		{
			name:     "an index one mask bounds",
			index:    "  %index = call i64 @__ic_and(i64 %n, i64 15)",
			mask:     twoPow61Minus1,
			accepted: true,
		},
		{
			name: "an index that is the sum of two bounded ones",
			index: `  %low = call i64 @__ic_and(i64 %n, i64 15)
  %high = call i64 @__ic_and(i64 %n, i64 7)
  %index = add i64 %low, %high`,
			mask:     twoPow61Minus1,
			accepted: true,
		},
		{
			name:  "an index no mask bounds",
			index: "  %index = call i64 @__ic_or(i64 %n, i64 3)",
			mask:  twoPow61Minus1,
		},
		{
			name: "an index a subtraction can carry below zero",
			index: `  %bounded = call i64 @__ic_and(i64 %n, i64 15)
  %index = sub i64 %bounded, 3`,
			mask: twoPow61Minus1,
		},
		{
			name:  "an index bounded by a mask that is itself negative",
			index: "  %index = call i64 @__ic_and(i64 %n, i64 -1)",
			mask:  twoPow61Minus1,
		},
		{
			name:  "a mask that clears a bit the bound reaches",
			index: "  %index = call i64 @__ic_and(i64 %n, i64 15)",
			mask:  "2305843009213693950",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text := strings.Replace(maskedIndex, "INDEX", tc.index, 1)
			m := parseIR(t, strings.Replace(text, "MASK", tc.mask, 1))
			_, err := Select(t.Context(), m, Options{File: "case.c"})
			switch {
			case tc.accepted && err != nil:
				t.Fatalf("selection refused a mask that covers every value its index holds: %v", err)
			case !tc.accepted && err == nil:
				t.Fatalf("selection accepted a mask whose constant no register holds")
			case !tc.accepted && !strings.Contains(err.Error(), "is not one an IEEE double holds exactly"):
				t.Errorf("the refusal does not name the constant the machine cannot hold: %v", err)
			}
		})
	}
}

// summedIndex builds an index that is a chain of additions over one bounded
// value, so the walk from the mask down to the bound is as deep as the chain is
// long. Every element is bounded, so nothing but the depth decides the answer.
func summedIndex(steps int) string {
	var b strings.Builder
	b.WriteString("  %bit = call i64 @__ic_and(i64 %n, i64 1)\n")
	b.WriteString("  %sum0 = add i64 %bit, %bit\n")
	for i := 1; i < steps; i++ {
		fmt.Fprintf(&b, "  %%sum%d = add i64 %%sum%d, %%bit\n", i, i-1)
	}
	fmt.Fprintf(&b, "  %%index = add i64 %%sum%d, %%bit", steps-1)
	return b.String()
}

// TestTheBoundWalkStopsWhereTheAddressWalksStop holds the walk behind a mask to
// the depth every other walk over the operand graph takes. What the bound
// answers is a use chain referring back to itself; a chain past it is refused
// rather than mis-folded, since the mask is kept and no literal names it.
func TestTheBoundWalkStopsWhereTheAddressWalksStop(t *testing.T) {
	// twoPow61Minus1 is the mask a comparison against a fixed element leaves,
	// which is the full width divided by the eight bytes a slot is.
	const twoPow61Minus1 = "2305843009213693951"

	cases := []struct {
		name     string
		steps    int
		accepted bool
	}{
		{name: "a chain the walk reaches the bound of", steps: maxOffsetDepth / 2, accepted: true},
		{name: "a chain deeper than every walk goes", steps: 2 * maxOffsetDepth},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text := strings.Replace(maskedIndex, "INDEX", summedIndex(tc.steps), 1)
			m := parseIR(t, strings.Replace(text, "MASK", twoPow61Minus1, 1))
			_, err := Select(t.Context(), m, Options{File: "case.c"})
			switch {
			case tc.accepted && err != nil:
				t.Fatalf("selection refused a mask over an index it can bound: %v", err)
			case !tc.accepted && err == nil:
				t.Fatalf("selection bounded an index past the depth every other walk stops at")
			case !tc.accepted && !strings.Contains(err.Error(), "is not one an IEEE double holds exactly"):
				t.Errorf("the refusal does not name the constant the machine cannot hold: %v", err)
			}
		})
	}
}

// boundedProgram holds one instruction the bound walk is asked about, over a
// value a mask already bound, the same value as a double, and a truth value an
// arm can choose on. The result reaches no reader, since what the placement walk
// answers is a property of the instruction alone.
const boundedProgram = `
declare i64 @__ic_and(i64, i64)

define void @main() {
entry:
  %slot = alloca i64
  %n = load i64, ptr %slot
  %b = and i64 %n, 7
  %d = sitofp i64 %b to double
  %c = icmp sgt i64 %n, 5
BODY
  ret void
}
`

// boundedPrograms is one instruction per opcode boundedOps admits, written so
// the walk answers a bound for it: a widening over a truth value, and the rest
// over a value a mask bound.
var boundedPrograms = map[llvm.Opcode]string{
	llvm.And:     "%r = and i64 %b, 15",
	llvm.Call:    "%r = call i64 @__ic_and(i64 %b, i64 15)",
	llvm.Add:     "%r = add i64 %b, 3",
	llvm.Mul:     "%r = mul i64 %b, 3",
	llvm.Select:  "%r = select i1 %c, i64 %b, i64 3",
	llvm.ZExt:    "%r = zext i1 %c to i64",
	llvm.SIToFP:  "%r = sitofp i64 %b to double",
	llvm.UIToFP:  "%r = uitofp i1 %c to double",
	llvm.FPToSI:  "%r = fptosi double %d to i64",
	opcodeFreeze: "%r = freeze i64 %b",
}

// TestEveryBoundedOpcodeIsPlaced holds the two walks to the relation mask.go
// rests on: a value the bound walk answers for is one selection can place. They
// are separate rules over separate opcodes and nothing but this holds them
// together, so an opcode joining boundedOps alone fails here.
func TestEveryBoundedOpcodeIsPlaced(t *testing.T) {
	for _, op := range slices.Sorted(maps.Keys(boundedOps)) {
		body, written := boundedPrograms[op]
		if !written {
			t.Errorf("the bound walk admits opcode %d and this test holds no program for it", op)
			continue
		}
		t.Run(body, func(t *testing.T) {
			m := parseIR(t, strings.Replace(boundedProgram, "BODY", "  "+body, 1))
			result := namedInstruction(t, m, "r")
			s := selector{covers: make(map[llvm.Value]cover)}
			if _, bounded := s.coverOf(result, 0); !bounded {
				t.Fatalf("the walk bounds no value of %q, so the case holds the two walks against nothing", body)
			}
			if planPlacement(m)[result] {
				t.Errorf("the walk bounds %q and selection cannot place its result inside the machine's conversion range", body)
			}
		})
	}
}

// TestABoundIsRefusedForAValueSelectionCannotPlace pins a guard the rules make
// unreachable, which is what [TestEveryBoundedOpcodeIsPlaced] establishes. The
// placement is supplied rather than measured, since a program that reached the
// guard would be that test failing.
func TestABoundIsRefusedForAValueSelectionCannotPlace(t *testing.T) {
	m := parseIR(t, strings.Replace(boundedProgram, "BODY", "  %r = and i64 %b, 15", 1))
	result := namedInstruction(t, m, "r")

	placed := selector{covers: make(map[llvm.Value]cover)}
	if _, bounded := placed.coverOf(result, 0); !bounded {
		t.Fatalf("the walk bounds no value of the mask, so the case holds the guard against nothing")
	}
	unplaced := selector{
		covers:   make(map[llvm.Value]cover),
		unplaced: map[llvm.Value]bool{result: true},
	}
	if bound, bounded := unplaced.coverOf(result, 0); bounded {
		t.Errorf("the walk bounds by %d a value selection cannot place inside the machine's conversion range", bound)
	}
}
