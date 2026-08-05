package isel

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/chip"
	"github.com/greg2010/ic11c/internal/devtrace"
	"github.com/greg2010/ic11c/internal/ic10"
)

// litHash is the prefab a batch program selects on and a world seeds a device
// with, so that the two agree.
const litHash = "StructureWallLight"

func TestANonFiniteDeviceHashSelectsABatchTheProgramNeverNamed(t *testing.T) {
	hash := ic10.HashName(litHash)

	cases := []struct {
		name    string
		source  string
		written bool
	}{
		{name: "a NaN, from a batch read matching no device", source: "div r0 0 0"},
		{name: "a positive infinity", source: "div r0 1 0"},
		{name: "a negative infinity", source: "div r0 -1 0"},
		{name: "the hash the program named", source: fmt.Sprintf("move r0 %d", hash), written: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assembly := tc.source + "\ntrunc r0 r0\nmove r1 1\nsb r0 On r1"
			events := runWorld(t, assembly, func(t *testing.T, w *world) {
				w.setHashes(t, 0, ic10.HashName(litHash), 0)
			}, 1)
			if tc.written {
				assertWrote(t, events, 0, logicType(t, "On"), 1, assembly)
				return
			}
			assertNoWrite(t, events, 0, logicType(t, "On"), assembly)
		})
	}
}

// TestAnIndexOutsideTheRangeSelectsASlotTheProgramNeverNamed deliberately does
// not pin which index each reading narrows to: ECMA-335 leaves conv.i4
// unspecified for a NaN and past the range, so pinning it would make a supported
// runtime's answer a failure.
func TestAnIndexOutsideTheRangeSelectsASlotTheProgramNeverNamed(t *testing.T) {
	occupied := logicSlotType(t, "Occupied")

	// No reading below can narrow to named, so the marker coming back is the
	// program having chosen the slot its source names.
	const (
		named  = 5
		marker = 77
	)

	cases := []struct {
		name   string
		source string
		// chose reports that the program named the slot, which is the control.
		chose bool
	}{
		{name: "a NaN", source: "div r0 0 0"},
		{name: "a positive infinity", source: "div r0 1 0"},
		{name: "a negative infinity", source: "div r0 -1 0"},
		{name: "a magnitude past the range", source: "move r0 3000000000"},
		{name: "the slot the source named", source: fmt.Sprintf("move r0 %d", named), chose: true},
	}

	setting := logicType(t, "Setting")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assembly := tc.source + "\nls r1 d0 r0 Occupied\ns d1 Setting r1"
			trace := traceWorld(t, assembly, func(t *testing.T, w *world) {
				w.setSlot(t, 0, named, occupied, marker)
			}, 1)
			if trace.Stop.Reason == devtrace.StopFaulted {
				t.Fatalf("the chip stopped, where this position narrows rather than faulting: %s\n%s", trace.Stop, assembly)
			}
			wrote, written := writtenValue(trace.Events, 1, setting)
			if !written {
				t.Fatalf("the program made no write to d1 Setting, so the line the index feeds did not run\n%s", assembly)
			}
			if got := wrote == marker; got != tc.chose {
				t.Errorf("the line read the slot the source named = %v, want %v; it wrote %v\n%s", got, tc.chose, wrote, assembly)
			}
		})
	}
}

// TestMaskingANonFiniteHashIsTheHazardRatherThanARemedy covers the mask a
// programmer reaches for to bound a hash. The machine's bitwise conversion is
// total on a NaN and answers zero — the batch of every unset prefab, which d1
// and d2 have — and partial on an infinity, where it stops the chip.
func TestMaskingANonFiniteHashIsTheHazardRatherThanARemedy(t *testing.T) {
	const masked = "l r0 d0 Setting\ntrunc r0 r0\nand r0 r0 2147483647\nmove r1 1\nsb r0 On r1"
	const bare = "l r0 d0 Setting\ntrunc r0 r0\nmove r1 1\nsb r0 On r1"

	cases := []struct {
		name     string
		assembly string
		reading  float64
		// unnamed are the pins the program never selected and wrote anyway.
		unnamed []int
		faulted bool
	}{
		{name: "a mask over a NaN selects the batch of every unset prefab", assembly: masked, reading: math.NaN(), unnamed: []int{1, 2}},
		{name: "no mask over a NaN reaches no device at all", assembly: bare, reading: math.NaN()},
		{name: "a mask over a positive infinity stops the chip", assembly: masked, reading: math.Inf(1), faulted: true},
		{name: "a mask over a negative infinity stops the chip", assembly: masked, reading: math.Inf(-1), faulted: true},
		{name: "no mask over a positive infinity runs and writes nowhere", assembly: bare, reading: math.Inf(1)},
		{name: "no mask over a negative infinity runs and writes nowhere", assembly: bare, reading: math.Inf(-1)},
	}

	on := logicType(t, "On")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			trace := traceWorld(t, tc.assembly, func(t *testing.T, w *world) {
				w.setHashes(t, 0, ic10.HashName(litHash), 0)
				w.set(t, 0, logicType(t, "Setting"), tc.reading)
			}, 1)
			if faulted := trace.Stop.Reason == devtrace.StopFaulted; faulted != tc.faulted {
				t.Fatalf("the chip stopping is %v, want %v: %s\n%s", faulted, tc.faulted, trace.Stop, tc.assembly)
			}
			for pin := range 3 {
				want := slices.Contains(tc.unnamed, pin)
				if got := wroteProperty(trace.Events, pin, on); got != want {
					t.Errorf("d%d written = %v, want %v\n%s", pin, got, want, tc.assembly)
				}
			}
		})
	}
}

// TestANegatedDisjunctionAdmitsTheValueItLooksLikeItBounds covers the spelling
// that reads as a two-sided test and is not one: a NaN satisfies neither ordered
// comparison, so the disjunction is false and the negation admits it. The
// optimizer leaves one ordered comparison with the arms the other way round.
func TestANegatedDisjunctionAdmitsTheValueItLooksLikeItBounds(t *testing.T) {
	hash := ic10.HashName(litHash)
	guarded := fmt.Sprintf(`l r0 d0 Setting
abs r1 r0
sge r1 r1 2147483648
trunc r2 r0
select r0 r1 %d r2
move r1 1
sb r0 On r1`, hash)

	cases := []struct {
		name    string
		reading float64
		// written are the pins the run wrote: the program's own batch for a
		// reading the test excluded, and nothing for the one it let through.
		written []int
	}{
		{name: "a NaN reaches the arm the test looks like it excludes", reading: math.NaN()},
		{name: "a magnitude past the range takes the arm the test does exclude", reading: 3e9, written: []int{0}},
		{name: "the hash the program named", reading: float64(hash), written: []int{0}},
	}

	on := logicType(t, "On")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events := runWorld(t, guarded, func(t *testing.T, w *world) {
				w.setHashes(t, 0, ic10.HashName(litHash), 0)
				w.set(t, 0, logicType(t, "Setting"), tc.reading)
			}, 1)
			for pin := range 3 {
				want := slices.Contains(tc.written, pin)
				if got := wroteProperty(events, pin, on); got != want {
					t.Errorf("d%d written = %v, want %v\n%s", pin, got, want, guarded)
				}
			}
		})
	}
}

// TestTheEmptyBatchHazardCompilesTodayAndIsRefused states the defect as the
// program that produced it: a batch read answers a NaN under Average whenever
// the batch is empty.
func TestTheEmptyBatchHazardCompilesTodayAndIsRefused(t *testing.T) {
	src := fmt.Sprintf(`void main(void) {
    while (true) {
        long long hash = (long long)__ic_load_batch(__ic_hash("%s"), Setting, Average);
        __ic_store_batch(hash, On, 1.0);
        __ic_yield();
    }
}`, litHash)

	_, err := selectSource(t, src)
	if err == nil {
		t.Fatalf("selection emitted a store whose deviceHash it cannot place, which the chip resolves by narrowing a NaN to an integer no program named")
	}
	assertRefusalNames(t, err, "deviceHash")
}

// TestAnOperandOutsideTheNarrowingRangeIsRefused covers every operand position
// the backend fills with a register the machine rounds to an integer. The
// address operand of poke and get is the one position no row refuses; what the
// machine does with what gets through is [TestAnUnboundedStackIndexReachesTheChip].
func TestAnOperandOutsideTheNarrowingRangeIsRefused(t *testing.T) {
	cases := []struct {
		name    string
		operand string
		src     string
		refused bool
	}{
		{
			name:    "a deviceHash read off a device",
			operand: "deviceHash",
			refused: true,
			src: `const dev in = d0;

void main(void) {
    while (true) {
        long long hash = (long long)__ic_load(in, Setting);
        __ic_store_batch(hash, On, 1.0);
        __ic_yield();
    }
}`,
		},
		{
			name:    "a deviceHash the compiler folded",
			operand: "deviceHash",
			src: fmt.Sprintf(`void main(void) {
    while (true) {
        __ic_store_batch(__ic_hash("%s"), On, 1.0);
        __ic_yield();
    }
}`, litHash),
		},
		{
			name:    "a nameHash read off a device",
			operand: "nameHash",
			refused: true,
			src: fmt.Sprintf(`const dev in = d0;

void main(void) {
    while (true) {
        long long name = (long long)__ic_load(in, Setting);
        __ic_store_batch_named(__ic_hash("%s"), name, On, 1.0);
        __ic_yield();
    }
}`, litHash),
		},
		{
			name:    "a nameHash the compiler folded",
			operand: "nameHash",
			src: fmt.Sprintf(`void main(void) {
    while (true) {
        __ic_store_batch_named(__ic_hash("%s"), __ic_hash("lamp"), On, 1.0);
        __ic_yield();
    }
}`, litHash),
		},
		{
			name:    "a reagent hash read off a device",
			operand: "int",
			refused: true,
			src: `const dev in = d0;
const dev out = d1;

void main(void) {
    while (true) {
        long long reagent = (long long)__ic_load(in, Setting);
        __ic_store(out, Setting, __ic_load_reagent(in, Contents, reagent));
        __ic_yield();
    }
}`,
		},
		{
			name:    "a reagent hash the compiler folded",
			operand: "int",
			src: `const dev in = d0;
const dev out = d1;

void main(void) {
    while (true) {
        __ic_store(out, Setting, __ic_load_reagent(in, Contents, __ic_hash("Iron")));
        __ic_yield();
    }
}`,
		},
		{
			// A mask does not remove a NaN, it converts one; see
			// [TestMaskingANonFiniteHashIsTheHazardRatherThanARemedy].
			name:    "a deviceHash masked after a device read",
			operand: "deviceHash",
			refused: true,
			src: `const dev in = d0;

void main(void) {
    while (true) {
        long long hash = (long long)__ic_load(in, Setting) & 0x7fffffff;
        __ic_store_batch(hash, On, 1.0);
        __ic_yield();
    }
}`,
		},
		{
			name:    "a nameHash a counted loop produces",
			operand: "nameHash",
			src: fmt.Sprintf(`void main(void) {
    for (long long i = 0; i < 3; i++) {
        __ic_store_batch_named(__ic_hash("%s"), i, On, 1.0);
    }
}`, litHash),
		},
		{
			// Nothing in the loop is a device reading and nothing in it is
			// non-finite; the counter simply starts past the range.
			name:    "a nameHash a counted loop runs past the range",
			operand: "nameHash",
			refused: true,
			src: fmt.Sprintf(`void main(void) {
    for (long long i = 3000000000; i < 3000000003; i++) {
        __ic_store_batch_named(__ic_hash("%s"), i, On, 1.0);
    }
}`, litHash),
		},
		{
			// The literal reaches the operand directly, with no value flowing
			// anywhere for a walk to read.
			name:    "a deviceHash the source spells past the range",
			operand: "deviceHash",
			refused: true,
			src: `void main(void) {
    __ic_store_batch(3000000000, On, 1.0);
}`,
		},
		{
			name:    "a deviceHash a range test bounds",
			operand: "deviceHash",
			src: fmt.Sprintf(`const dev in = d0;

void main(void) {
    while (true) {
        double v = __ic_load(in, Setting);
        long long hash = %s__ic_hash("%s");
        __ic_store_batch(hash, On, 1.0);
        __ic_yield();
    }
}`, boundedCastAdvice, litHash),
		},
		{
			// Bounding and then leaving the bounds is the shape the advice
			// cannot certify.
			name:    "a deviceHash the arm adds to after the test passed",
			operand: "deviceHash",
			refused: true,
			src: fmt.Sprintf(`const dev in = d0;

void main(void) {
    double v = __ic_load(in, Setting);
    long long hash = (v > -2147483648.0 && v < 2147483648.0) ? (long long)(v + 3000000000.0) : __ic_hash("%s");
    __ic_store_batch(hash, On, 1.0);
}`, litHash),
		},
		{
			// What separates this from the row above is arithmetic the walk can
			// read, not the spelling.
			name:    "a deviceHash the arm adds to inside bounds that hold it",
			operand: "deviceHash",
			src: `const dev in = d0;

void main(void) {
    double v = __ic_load(in, Setting);
    long long hash = (v > -1000.0 && v < 1000.0) ? (long long)(v + 3000.0) : 0;
    __ic_store_batch(hash, On, 1.0);
}`,
		},
		{
			// The bounds are finite and the test is ordered, which is the whole
			// of what the shape used to be held to, and says nothing about what
			// the operand narrows to.
			name:    "a deviceHash a test bounds at a magnitude the narrowing does not carry",
			operand: "deviceHash",
			refused: true,
			src: fmt.Sprintf(`const dev in = d0;

void main(void) {
    double v = __ic_load(in, Setting);
    long long hash = (v > -1e300 && v < 1e300) ? (long long)v : __ic_hash("%s");
    __ic_store_batch(hash, On, 1.0);
}`, litHash),
		},
		{
			// One side is not a bound: every negative value and a negative
			// infinity satisfy it.
			name:    "a deviceHash a one-sided test bounds",
			operand: "deviceHash",
			refused: true,
			src: fmt.Sprintf(`const dev in = d0;

void main(void) {
    double v = __ic_load(in, Setting);
    long long hash = (v < 2147483648.0) ? (long long)v : __ic_hash("%s");
    __ic_store_batch(hash, On, 1.0);
}`, litHash),
		},
		{
			// The failing arm runs where the value is a NaN, an infinity or past
			// the range, so what the test established says nothing there.
			name:    "a deviceHash the other arm reads off a device",
			operand: "deviceHash",
			refused: true,
			src: `const dev in = d0;

void main(void) {
    double v = __ic_load(in, Setting);
    long long hash = (v > -2147483648.0 && v < 2147483648.0) ? (long long)v : (long long)__ic_load(in, Power);
    __ic_store_batch(hash, On, 1.0);
}`,
		},
		{
			name:    "a deviceHash the bounded value fills the other arm of",
			operand: "deviceHash",
			refused: true,
			src: fmt.Sprintf(`const dev in = d0;

void main(void) {
    double v = __ic_load(in, Setting);
    long long hash = (v > -2147483648.0 && v < 2147483648.0) ? __ic_hash("%s") : (long long)v;
    __ic_store_batch(hash, On, 1.0);
}`, litHash),
		},
		{
			// See [TestANegatedDisjunctionAdmitsTheValueItLooksLikeItBounds].
			name:    "a deviceHash a negated disjunction admits a NaN through",
			operand: "deviceHash",
			refused: true,
			src: `const dev in = d0;

void main(void) {
    double v = __ic_load(in, Setting);
    long long hash = !(v <= -2147483648.0 || v >= 2147483648.0) ? (long long)v : 0;
    __ic_store_batch(hash, On, 1.0);
}`,
		},
		{
			// The bounds are symmetric, so negating a value inside them leaves it
			// inside them.
			name:    "a deviceHash a range test bounds and the arm negates",
			operand: "deviceHash",
			src: fmt.Sprintf(`const dev in = d0;

void main(void) {
    double v = __ic_load(in, Setting);
    long long hash = (v > -2147483648.0 && v < 2147483648.0) ? (long long)(0.0 - v) : __ic_hash("%s");
    __ic_store_batch(hash, On, 1.0);
}`, litHash),
		},
		{
			// The address operand narrows like the three above and is not
			// refused; see [selector.lowerLoad] for why.
			name:    "a stack index read off a device",
			operand: "address",
			src: `const dev in = d0;
const dev out = d1;

double samples[8];

void main(void) {
    while (true) {
        long long i = (long long)__ic_load(in, Setting);
        samples[i] = __ic_load(in, Power);
        __ic_store(out, Setting, samples[0]);
        __ic_yield();
    }
}`,
		},
		{
			name:    "a stack index the program's own arithmetic bounds",
			operand: "address",
			src: `const dev in = d0;
const dev out = d1;

long long cursor;
double samples[8];

void main(void) {
    while (true) {
        samples[cursor] = __ic_load(in, Setting);
        cursor = (cursor + 1) & 7;
        __ic_store(out, Setting, samples[0]);
        __ic_yield();
    }
}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := selectSource(t, tc.src)
			switch {
			case tc.refused && err == nil:
				t.Fatalf("selection emitted an instruction whose %s operand it cannot place, which the chip rounds to an integer unrelated to the value", tc.operand)
			case !tc.refused && err != nil:
				t.Fatalf("selection refused an operand it can place: %v", err)
			case tc.refused:
				assertRefusalNames(t, err, tc.operand)
			}
		})
	}
}

// TestTheRefusalNamesARemedyThatCompiles keeps __ic_isnan out of the advice.
// The call is a declaration, which says only that it computes a number, so
// neither select nor control dependence carries what a test of it established.
func TestTheRefusalNamesARemedyThatCompiles(t *testing.T) {
	const src = `const dev in = d0;

void main(void) {
    __ic_store_batch((long long)__ic_load(in, Setting), On, 1.0);
}`

	_, err := selectSource(t, src)
	if err == nil {
		t.Fatalf("selection emitted a store whose deviceHash it cannot place")
	}
	text := err.Error()
	if !strings.Contains(text, boundedCastAdvice) {
		t.Errorf("the refusal does not name the spelling this stage accepts, %q:\n%s", boundedCastAdvice, text)
	}
	if strings.Contains(text, "__ic_isnan") {
		t.Errorf("the refusal names __ic_isnan, and no spelling of it reaches anything but this refusal:\n%s", text)
	}
}

// TestAGuardWrittenAsAnIfStatementCompiles holds the reach of the one shape
// [int32Range.selectSpan] reads. SimplifyCFG speculates an arm holding one
// assignment, so an if and a conditional expression reach selection as the same
// module; an arm holding a call is one it will not speculate.
func TestAGuardWrittenAsAnIfStatementCompiles(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		refused bool
	}{
		{
			name: "an if statement over one assignment",
			body: `    long long h = 0;
    if (v > -2147483648.0 && v < 2147483648.0) { h = (long long)v; }
    __ic_store_batch(h, On, 1.0);`,
		},
		{
			name: "the same guard as a conditional expression",
			body: `    long long h = (v > -2147483648.0 && v < 2147483648.0) ? (long long)v : 0;
    __ic_store_batch(h, On, 1.0);`,
		},
		{
			name:    "an if statement over an arm the optimizer will not speculate",
			refused: true,
			body: `    long long h = 0;
    if (v > -1000.0 && v < 1000.0) { h = (long long)__ic_round(v); }
    __ic_store_batch(h, On, 1.0);`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := selectSource(t, strings.Replace(guardedProgram, "BODY", tc.body, 1))
			switch {
			case tc.refused && err == nil:
				t.Fatalf("selection emitted a store whose deviceHash the test reaches only through control")
			case !tc.refused && err != nil:
				t.Fatalf("selection refused a guard the optimizer left as the select it reads: %v", err)
			case tc.refused:
				assertRefusalNames(t, err, "deviceHash")
			}
		})
	}
}

// TestTheRefusalPromisesNoArrivalValue keeps the arrival integer out of the
// message. It is measured on one build of a runtime the game ships a fork of,
// and naming it would mislead: no device is in that batch, so a reader could
// take the line for harmless where a mask makes it batch zero.
func TestTheRefusalPromisesNoArrivalValue(t *testing.T) {
	const src = `const dev in = d0;

void main(void) {
    __ic_store_batch((long long)__ic_load(in, Setting), On, 1.0);
}`

	_, err := selectSource(t, src)
	if err == nil {
		t.Fatalf("selection emitted a store whose deviceHash it cannot place")
	}
	if text := err.Error(); strings.Contains(text, "arrives as") {
		t.Errorf("the refusal names the integer the operand arrives as, which nothing settles:\n%s", text)
	}
}

// TestABoundedHashSelectsTheBatchTheProgramChose holds the advice to the
// outcome, not only to compiling. The row at 3e9 separates it from any wider
// bounds: a reading no integer denotes fails every finite test there is, so a
// table of only those would pass with bounds that certify nothing.
func TestABoundedHashSelectsTheBatchTheProgramChose(t *testing.T) {
	src := fmt.Sprintf(`const dev in = d0;

void main(void) {
    double v = __ic_load(in, Setting);
    long long hash = %s__ic_hash("%s");
    __ic_store_batch(hash, On, 1.0);
}`, boundedCastAdvice, litHash)

	assembly := compileSource(t, src)
	on := logicType(t, "On")

	cases := []struct {
		name    string
		reading float64
	}{
		{name: "a NaN", reading: math.NaN()},
		{name: "a positive infinity", reading: math.Inf(1)},
		{name: "a negative infinity", reading: math.Inf(-1)},
		{name: "a reading past the range the test admits", reading: 1e300},
		{name: "a reading just past the range the narrowing carries", reading: 3e9},
		{name: "a reading just below it", reading: -3e9},
		{name: "a reading inside the range, being the hash itself", reading: float64(ic10.HashName(litHash))},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events := runWorld(t, assembly, func(t *testing.T, w *world) {
				w.setHashes(t, 0, ic10.HashName(litHash), 0)
				w.set(t, 0, logicType(t, "Setting"), tc.reading)
			}, 1)
			assertWrote(t, events, 0, on, 1, assembly)
			for _, pin := range []int{1, 2} {
				if wroteProperty(events, pin, on) {
					t.Errorf("the program wrote d%d, which has no prefab and is in no batch it named\n%s", pin, assembly)
				}
			}
		})
	}
}

// unboundedStackIndexProgram indexes the data region by a device reading, which
// nothing in the program bounds. Slot 0 is read back so the store survives the
// optimizer, and so that a fault is distinguishable from a write to slot 0.
const unboundedStackIndexProgram = `const dev in = d0;
const dev out = d1;

double samples[8];

void main(void) {
    while (true) {
        long long i = (long long)__ic_load(in, Setting);
        samples[i] = __ic_load(in, Power);
        __ic_store(out, Setting, samples[0]);
        __ic_yield();
    }
}`

// TestNothingBoundsTheStackIndexOnItsWayToTheChip exists so that
// [TestAnUnboundedStackIndexReachesTheChip] goes on discriminating: a lowering
// that clamped the index would leave every row there passing over a value the
// chip never saw.
func TestNothingBoundsTheStackIndexOnItsWayToTheChip(t *testing.T) {
	assembly := compileSource(t, unboundedStackIndexProgram)
	got := mnemonics(strings.Split(assembly, "\n"))
	if !contains(got, "poke") {
		t.Fatalf("the program reaches memory through no poke:\n%s", assembly)
	}
	for _, mnemonic := range got {
		if slices.Contains([]string{"select", "max", "min"}, mnemonic) || strings.HasPrefix(mnemonic, "b") {
			t.Errorf("%s stands ahead of the stack index, so the value the chip narrows is not the one the world supplied:\n%s", mnemonic, assembly)
		}
	}
}

// TestAnUnboundedStackIndexReachesTheChip is the machine half of the row
// [TestAnOperandOutsideTheNarrowingRangeIsRefused] states as "not refused".
// Slots 8 through 511 are the quiet outcome — memory the array does not hold, so
// the line writes over a slot the program never named and nothing reports it.
func TestAnUnboundedStackIndexReachesTheChip(t *testing.T) {
	assembly := compileSource(t, unboundedStackIndexProgram)

	cases := []struct {
		name    string
		reading float64
		faulted bool
		// wrote is what d1 Setting holds after the turn, read back out of the
		// slot the index selected.
		wrote float64
	}{
		{name: "a NaN stops the chip below the memory", reading: math.NaN(), faulted: true},
		{name: "a positive infinity stops the chip at the same end", reading: math.Inf(1), faulted: true},
		{name: "a negative infinity stops the chip", reading: math.Inf(-1), faulted: true},
		{name: "a magnitude past the range the cast represents", reading: 3e9, faulted: true},
		{name: "an index past the array and inside the memory", reading: 8, wrote: 0},
		{name: "the last slot the memory holds", reading: 511, wrote: 0},
		{name: "the first index past the memory", reading: 512, faulted: true},
		{name: "an index the array holds", reading: 0, wrote: 42},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			trace := traceWorld(t, assembly, func(t *testing.T, w *world) {
				w.set(t, 0, logicType(t, "Setting"), tc.reading)
				w.set(t, 0, logicType(t, "Power"), 42)
			}, 1)
			if faulted := trace.Stop.Reason == devtrace.StopFaulted; faulted != tc.faulted {
				t.Fatalf("the chip stopping is %v, want %v: %s\n%s", faulted, tc.faulted, trace.Stop, assembly)
			}
			if tc.faulted {
				return
			}
			assertWrote(t, trace.Events, 1, logicType(t, "Setting"), tc.wrote, assembly)
		})
	}
}

// TestNarrowedOperandRefusalNamesAPlace is stated over a program whose store the
// optimizer hoists out of the loop: the optimizer leaves line 0 on a hoisted
// instruction rather than dropping the metadata, and a diagnostic with no
// position renders as a bare dash.
func TestNarrowedOperandRefusalNamesAPlace(t *testing.T) {
	const src = `const dev in = d0;

void main(void) {
    long long hash = (long long)__ic_load(in, Setting);
    while (true) {
        __ic_store_batch(hash, On, 1.0);
        __ic_yield();
    }
}`

	_, err := selectSource(t, src)
	if err == nil {
		t.Fatalf("selection emitted a store whose deviceHash it cannot place")
	}
	text := err.Error()
	if strings.Contains(text, sourceFile+":-") || !strings.Contains(text, sourceFile+":") {
		t.Fatalf("the refusal names no source position:\n%s", text)
	}
}

// TestNarrowingIsReadOffTheOperandTable holds the two facts [narrowsOperand]
// reads out of ic10 to what the machine's table says today. Both are extracted
// from the game, so a table that moves either has to fail here rather than
// silently turn the refusal off.
func TestNarrowingIsReadOffTheOperandTable(t *testing.T) {
	t.Run("every position named address is a stack index the machine narrows", func(t *testing.T) {
		want := map[string]int{"poke": 0, "get": 2, "put": 1, "getd": 2, "putd": 1}
		got := make(map[string]int)
		for _, instruction := range ic10.Instructions {
			for position, operand := range instruction.Operands {
				if operand.Name != stackIndexName {
					continue
				}
				got[instruction.Mnemonic] = position
				if operand.Direction != ic10.DirectionRead {
					t.Errorf("%s operand %d is named %q and has direction %s, not read",
						instruction.Mnemonic, position, stackIndexName, operand.Direction)
				}
				if operand.Conversion != ic10.ConversionNone {
					t.Errorf("%s operand %d is named %q and is read through %s, so the conversion rule owns it",
						instruction.Mnemonic, position, stackIndexName, operand.Conversion)
				}
			}
		}
		if len(got) != len(want) {
			t.Fatalf("the machine's table names %d operands %q, and this stage knows %d: %v",
				len(got), stackIndexName, len(want), got)
		}
		for mnemonic, position := range want {
			if got[mnemonic] != position {
				t.Errorf("%s names operand %d %q, want operand %d", mnemonic, got[mnemonic], stackIndexName, position)
			}
		}
	})

	t.Run("the positions the backend fills answer as the game's own classes do", func(t *testing.T) {
		cases := []struct {
			mnemonic string
			position int
			narrows  bool
		}{
			{mnemonic: "sb", position: 0, narrows: true},
			{mnemonic: "sb", position: 2},
			{mnemonic: "sbn", position: 1, narrows: true},
			{mnemonic: "lb", position: 1, narrows: true},
			{mnemonic: "lbn", position: 2, narrows: true},
			{mnemonic: "lr", position: 3, narrows: true},
			{mnemonic: "ls", position: 2, narrows: true},
			{mnemonic: "poke", position: 0, narrows: true},
			{mnemonic: "poke", position: 1},
			{mnemonic: "get", position: 2, narrows: true},
			{mnemonic: "add", position: 1},
			{mnemonic: "add", position: 2},
			{mnemonic: "select", position: 3},
			{mnemonic: "push", position: 0},
		}

		for _, tc := range cases {
			instruction, known := ic10.LookupInstruction(tc.mnemonic)
			if !known {
				t.Fatalf("the machine has no %s", tc.mnemonic)
			}
			if got := narrowsOperand(instruction.Operands[tc.position]); got != tc.narrows {
				t.Errorf("%s operand %d narrows = %v, want %v", tc.mnemonic, tc.position, got, tc.narrows)
			}
		}
	})
}

// TestTheGuardIsAskedAboutNoBranch keeps [narrowsOperand]'s answer for a jump
// target from costing anything. The operand table does not separate a branch
// target or clrd's reference id from a value operand, so narrowsOperand answers
// no for them; that is harmless only while [intrinsicForms] names no such form.
func TestTheGuardIsAskedAboutNoBranch(t *testing.T) {
	for name, form := range intrinsicForms {
		instruction, known := form.op.Instruction()
		if !known {
			t.Errorf("%s selects %s, which is not in the machine's table", name, form.op)
			continue
		}
		if strings.HasPrefix(instruction.Mnemonic, "b") || strings.HasPrefix(instruction.Mnemonic, "j") || instruction.Mnemonic == "clrd" {
			t.Errorf("%s selects %s, whose target the operand table does not separate from a value operand, so the narrowing at that position goes unrefused",
				name, instruction.Mnemonic)
		}
	}
}

// assertNoWrite holds one property to having been left alone.
func assertNoWrite(t *testing.T, events []chip.Write, pin int, property ic10.LogicType, assembly string) {
	t.Helper()
	for _, event := range events {
		if event.Pin == pin && event.Property == int(property) && event.Slot == chip.NoSlot {
			t.Errorf("the program wrote d%d %v = %v, and the operand it selected on holds no number\n%s",
				pin, property, event.Value, assembly)
			return
		}
	}
}

// writtenValue answers the value one logic write left at a property, and reports
// false where the program wrote none.
func writtenValue(events []chip.Write, pin int, property ic10.LogicType) (float64, bool) {
	for _, event := range events {
		if event.Pin == pin && event.Property == int(property) && event.Slot == chip.NoSlot {
			return event.Value, true
		}
	}
	return 0, false
}

// assertRefusalNames holds a diagnostic to naming the operand position it is
// about, by the name the chip's help text gives that position, so a reader is
// told which operand to bound rather than which instruction to give up on.
func assertRefusalNames(t *testing.T, err error, operand string) {
	t.Helper()
	want := "the " + operand + " operand"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("the refusal does not name %q:\n%v", want, err)
	}
}
