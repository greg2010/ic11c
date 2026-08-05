package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/chip"
	"github.com/greg2010/ic11c/internal/chiptest"
	"github.com/greg2010/ic11c/internal/devtrace"
	"github.com/greg2010/ic11c/internal/ic10"
)

// clangBaselineFile holds what each program's native build was measured to
// write, and the floors under the comparison are derived from it.
const clangBaselineFile = "testdata/clang.json"

// updateClangBaseline rewrites clangBaselineFile from what this run measured.
var updateClangBaseline = flag.Bool("update-clang-baseline", false,
	"rewrite "+clangBaselineFile+" from what this run measures")

// clangBaseline is one number per program: what its native build wrote. The
// chip builds are held to the same floor, since the two are compared write for
// write.
var clangBaseline = baseline[int]{
	file:     clangBaselineFile,
	task:     clangBaselineTask,
	update:   updateClangBaseline,
	excluded: func(name string) bool { _, out := clangExclusions[name]; return out },
	missing: func(name string, writes int) []string {
		if writes >= measurableWrites {
			return nil
		}
		return []string{name}
	},
}

// reasonSleepHasNoHostModel is why a program that suspends cannot be
// compared: sleep is a segment boundary whose count depends on a clock, and
// a host counterpart would have to be a transliteration of the interpreter's
// own sleep operation, which a second reading of the source must not be.
const reasonSleepHasNoHostModel = "__ic_sleep ends a tick without advancing the program counter, " +
	"so it is a segment boundary the host can only reproduce by restating the interpreter's own model of it"

// clangExclusions is why a corpus fixture is reported rather than compared,
// keyed by file name. A fixture absent from it is compared by default. Each
// reason names a call in excludedIntrinsic, and requireClangExclusion checks
// the program still makes it, so an exclusion cannot outlive its condition.
var clangExclusions = map[string]string{
	"airlock.c":           reasonSleepHasNoHostModel,
	"satellitetracking.c": reasonSleepHasNoHostModel,
}

// excludedIntrinsic is the call each exclusion's reason turns on, which is what
// holds the reason to the program.
var excludedIntrinsic = map[string]string{
	reasonSleepHasNoHostModel: "__ic_sleep",
}

// clangProgram is one entry in the comparison. Exactly one of fixture and
// src is set. stimulus prepares the world before each segment, and is what
// makes a program whose branches open on a reading take more than one of them.
type clangProgram struct {
	name     string
	fixture  string
	src      string
	stimulus devtrace.Stimulus
}

// clangCorpus is every showcase fixture, followed by the programs written for
// this comparison to reach what they do not.
func clangCorpus(t *testing.T) []clangProgram {
	t.Helper()
	names := corpusFixtures(t)
	out := make([]clangProgram, 0, len(names)+len(clangSources))
	for _, name := range names {
		out = append(out, clangProgram{name: name, fixture: name, stimulus: fixtureWorld[name]})
	}
	return append(out, clangSources...)
}

// TestCompiledProgramsAgreeWithClang is the differential path whose two
// sides were not both produced by this compiler: every MicroC program is
// also a valid C23 translation unit, so clang compiles and runs the same
// source natively over the same world. A divergence does not stop the rest.
func TestCompiledProgramsAgreeWithClang(t *testing.T) {
	programs := clangCorpus(t)
	names := make([]string, 0, len(programs))
	for _, program := range programs {
		names = append(names, program.name)
	}
	requireNamedInCorpus(t, "clangExclusions", clangExclusions, names)
	requireReasonsCited(t, "excludedIntrinsic", excludedIntrinsic, clangExclusions)

	recorded := clangBaseline.load(t)
	clangBaseline.covers(t, names, recorded)

	measured := make(map[string]int, len(programs))
	for _, program := range programs {
		t.Run(program.name, func(t *testing.T) {
			compareAgainstClang(t, program, recorded[program.name], measured)
		})
	}

	if *updateClangBaseline {
		clangBaseline.record(t, names, measured)
	}
}

// compareAgainstClang runs one program's native build through its world and
// compares what a device saw against the chip running the same source.
func compareAgainstClang(t *testing.T, program clangProgram, want int, measured map[string]int) {
	t.Helper()
	if reason, excluded := clangExclusions[program.name]; excluded {
		requireClangExclusion(t, program, reason)
		t.Skipf("not compared: %s", reason)
	}

	source := program.source(t)
	ctx, harness := chiptest.Fixtures(t)
	native := devtrace.RunNative(ctx, t, harness, source, devtrace.RunOptions{
		Name:     "clang",
		Segments: comparisonSegments,
		World:    populatedWorld,
		Stimulus: program.stimulus,
	})
	t.Logf("%s: clang wrote %d times over %d segments, %s",
		program.name, len(native.Events), native.Segments, native.Stop)

	compiled := traceChip(t, compileSource(t, source), "ic11c", program.stimulus)

	if *updateClangBaseline {
		// Held to the same evidence as a comparison, against measurableWrites
		// rather than a floor derived from the number about to be written, which
		// every run clears by construction. Both are asked before either is read,
		// so a regeneration reports every program it will not record.
		nativeEvident := requireEvidence(t, native, measurableWrites)
		compiledEvident := requireEvidence(t, compiled, measurableWrites)
		if nativeEvident && compiledEvident {
			measured[program.name] = len(native.Events)
		}
	} else {
		floor := writeFloor(want)
		requireEvidence(t, native, floor)
		requireEvidence(t, compiled, floor)
	}

	if err := devtrace.Diff(native, compiled); err != nil {
		t.Errorf("%s: the emitted program does not do what clang reads the same source as doing: %v",
			program.name, err)
	}
}

// source is the file both compilers read. A program written here is put in one,
// since a C driver takes no source on its command line.
func (p clangProgram) source(t *testing.T) string {
	t.Helper()
	if p.fixture != "" {
		return filepath.Join(fixtures, p.fixture)
	}
	return write(t, strings.ReplaceAll(p.name, " ", "-")+".c", p.src)
}

// text is what the program says, which a check about the source reads rather
// than [clangProgram.source]: that one writes a file per call, so asking it
// twice puts a Go-authored program in a second directory only to read it back.
func (p clangProgram) text(t *testing.T) string {
	t.Helper()
	if p.fixture == "" {
		return p.src
	}
	path := filepath.Join(fixtures, p.fixture)
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(source)
}

// traceChip drives one build through the same world the native run met.
func traceChip(t *testing.T, assembly, name string, stimulus devtrace.Stimulus) devtrace.Trace {
	t.Helper()
	ctx, harness := chiptest.Fixtures(t)
	return devtrace.Run(ctx, t, harness, assembly, devtrace.RunOptions{
		Name:     name,
		Segments: comparisonSegments,
		World:    populatedWorld,
		Stimulus: stimulus,
	})
}

// populatedWorld fills every pin, because a program reading a pin the housing
// left empty faults rather than reading zero.
func populatedWorld(t *testing.T, h *chip.FixtureHarness) {
	t.Helper()
	if err := h.AddDevices(t.Context(), ic10.NumDevicePins); err != nil {
		t.Fatalf("fill the housing: %v", err)
	}
}

// requireClangExclusion holds an exclusion to the reason it states: the
// program kept out has to still call the intrinsic that reason turns on. It
// is read through its own text rather than out of the fixture directory, so
// a program written for this comparison (with no file there) can be excluded too.
func requireClangExclusion(t *testing.T, program clangProgram, reason string) {
	t.Helper()
	call, stated := excludedIntrinsic[reason]
	if !stated {
		t.Fatalf("%s is excluded because %s, and nothing says what to look for in the program to know that still holds",
			program.name, reason)
	}
	if !strings.Contains(program.text(t), call) {
		t.Errorf("%s is excluded because %s, and it no longer calls %s", program.name, reason, call)
	}
}

// sweptReading drives one property across the range the machine's own functions
// part company over: negative and positive, inside and outside the domain of
// the inverse trigonometry, and on a half unit often enough that a rounding is
// asked for a midpoint. The period is longer than the run, so no sample repeats.
func sweptReading(segment int) float64 { return (float64((segment*29)%211) - 105) / 8 }

// clangSources are the programs written for this comparison: the corpus
// fixtures assert nothing about what any machine function computes, so
// these publish each result to its own slot, letting a comparison over the
// sequence name the function whose answer moved.
var clangSources = []clangProgram{
	{
		name: "every one argument machine function",
		src: `const dev gauge = d0;
const dev out = d1;

void main(void) {
    while (true) {
        double x = __ic_load(gauge, Setting);
        __ic_store_slot(out, 0, Damage, __ic_sqrt(x));
        __ic_store_slot(out, 1, Damage, __ic_abs(x));
        __ic_store_slot(out, 2, Damage, __ic_sgn(x));
        __ic_store_slot(out, 3, Damage, __ic_round(x));
        __ic_store_slot(out, 4, Damage, __ic_trunc(x));
        __ic_store_slot(out, 5, Damage, __ic_ceil(x));
        __ic_store_slot(out, 6, Damage, __ic_floor(x));
        __ic_store_slot(out, 7, Damage, __ic_log(x));
        __ic_store_slot(out, 8, Damage, __ic_exp(x));
        __ic_store_slot(out, 9, Damage, __ic_sin(x));
        __ic_store_slot(out, 10, Damage, __ic_cos(x));
        __ic_store_slot(out, 11, Damage, __ic_tan(x));
        __ic_store_slot(out, 12, Damage, __ic_asin(x));
        __ic_store_slot(out, 13, Damage, __ic_acos(x));
        __ic_store_slot(out, 14, Damage, __ic_atan(x));
        __ic_yield();
    }
}`,
		stimulus: func(t *testing.T, h *chip.FixtureHarness, segment int) {
			t.Helper()
			setLogic(t, pinOn(t, h, 0), "Setting", sweptReading(segment))
		},
	},
	{
		name: "the machine functions of more than one argument",
		src: `const dev gauge = d0;
const dev out = d1;

void main(void) {
    while (true) {
        double a = __ic_load(gauge, Setting);
        double b = __ic_load(gauge, Mode);
        __ic_store_slot(out, 0, Damage, __ic_min(a, b));
        __ic_store_slot(out, 1, Damage, __ic_max(a, b));
        __ic_store_slot(out, 2, Damage, __ic_pow(a, b));
        __ic_store_slot(out, 3, Damage, __ic_atan2(a, b));
        __ic_store_slot(out, 4, Damage, __ic_clamp(a, -1.0, b));
        __ic_store_slot(out, 5, Damage, __ic_lerp(a, b, a - b));
        __ic_store_slot(out, 6, Damage, __ic_isnan(a / b));
        __ic_store_slot(out, 7, Damage, __ic_rand());
        __ic_yield();
    }
}`,
		stimulus: func(t *testing.T, h *chip.FixtureHarness, segment int) {
			t.Helper()
			// The second operand reaches zero, which is where the quotient the
			// NaN test is taken over is a NaN rather than an infinity, and it
			// crosses the first so that neither the minimum nor the maximum is
			// the same operand every turn.
			setLogic(t, pinOn(t, h, 0), "Setting", sweptReading(segment))
			setLogic(t, pinOn(t, h, 0), "Mode", float64((segment*13)%7)-3)
		},
	},
	{
		name: "integer division, remainder and bit operations",
		src: `const dev gauge = d0;
const dev out = d1;

long long history[8];

void main(void) {
    long long turn = 0;
    while (true) {
        long long n = (long long)__ic_load(gauge, Setting);
        long long d = n % 7 - 3;
        if (d == 0) {
            d = -5;
        }
        history[turn % 8] = (n * 37 - 11) / d;

        long long packed = 0;
        for (long long i = 0; i < 8; i++) {
            packed = (packed << 3) ^ (history[i] & 7);
        }

        __ic_store(out, Setting, history[turn % 8]);
        __ic_store(out, Mode, packed);
        __ic_store(out, On, (n / d) % 2 != 0);
        turn++;
        __ic_yield();
    }
}`,
		stimulus: func(t *testing.T, h *chip.FixtureHarness, segment int) {
			t.Helper()
			// Negative on some turns, which is where C's truncating division
			// and remainder differ from the machine's mod, and small enough
			// that nothing here approaches the 2^53 the two languages part
			// company at.
			setLogic(t, pinOn(t, h, 0), "Setting", float64((segment*29)%97)-48)
		},
	},
}
