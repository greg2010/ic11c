package devtrace

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/chip"
	"github.com/greg2010/ic11c/internal/chiptest"
	"github.com/greg2010/ic11c/internal/ic10"
)

// nativeName is what these cases call a native trace, which is what a
// difference [Diff] reports names it by.
const nativeName = "clang"

// intrinsicSignature matches one intrinsic at the start of a line, which is
// where the prelude declares one and where the host defines one.
var intrinsicSignature = regexp.MustCompile(`(?m)^[a-z ]+\b(__ic_[a-z0-9_]+)\(`)

// TestTheHostImplementsEveryIntrinsic holds the host to the generated
// prelude, since a missing implementation is not a link failure until some
// program calls it.
func TestTheHostImplementsEveryIntrinsic(t *testing.T) {
	declared := intrinsicNames(intrinsicSignature, ic10.Prelude)
	implemented := intrinsicNames(intrinsicSignature, hostSource)
	t.Logf("%s declares %d intrinsics and the host implements %d", ic10.PreludeFileName, len(declared), len(implemented))

	for name := range declared {
		if !implemented[name] {
			t.Errorf("%s declares %s and the host implements no such function", ic10.PreludeFileName, name)
		}
	}
	for name := range implemented {
		if !declared[name] {
			t.Errorf("the host implements %s, which %s declares no prototype for", name, ic10.PreludeFileName)
		}
	}
	if len(declared) == 0 {
		t.Fatalf("no intrinsic prototype was found in %s, so this compared nothing", ic10.PreludeFileName)
	}
}

// hostRequest matches a request the host writes directly: the kind, then one
// field per operand, each either an integer or a bit pattern behind its marker.
var hostRequest = regexp.MustCompile(`printf\("([a-z]+)((?: #?%[-0-9a-z]+)*)\\n"`)

// hostMachineCall matches a request the host writes through one of the machine
// function helpers, whose arity is the helper's own.
var hostMachineCall = regexp.MustCompile(`machine([0-9])\("([a-z0-9]+)"`)

// hostControl are the requests the run loop answers itself rather than
// dispatching, so no line runs them and nothing reads their operands.
var hostControl = map[string]bool{"y": true, "end": true, "sleep": true}

// TestTheHostSendsWhatTheDispatchLineReads holds the two hand-written tables
// to each other: the request names and the operand counts. A request short of
// its line would silently answer against a previous call's operands, which
// would arrive as a divergence against the compiler instead of a defect here.
func TestTheHostSendsWhatTheDispatchLineReads(t *testing.T) {
	sent := make(map[string]int)
	for _, match := range hostRequest.FindAllStringSubmatch(hostSource, -1) {
		sent[match[1]] = len(strings.Fields(match[2]))
	}
	for _, match := range hostMachineCall.FindAllStringSubmatch(hostSource, -1) {
		arity, err := strconv.Atoi(match[1])
		if err != nil {
			t.Fatalf("the host calls machine%q, whose arity is not a number: %v", match[1], err)
		}
		sent[match[2]] = arity
	}
	if len(sent) == 0 {
		t.Fatalf("no request was found in the host, so this compared nothing")
	}

	for _, entry := range dispatchLines {
		kind := strings.TrimSuffix(entry.kind, chipSuffix)
		carried, written := sent[kind]
		if !written {
			t.Errorf("a dispatch line runs %q and the host writes no such request, so nothing reaches it", entry.kind)
			continue
		}
		if read := entry.operands(t); carried != read {
			t.Errorf("the host writes %q with %d operands and the line running it reads %d; the arguments arrive in r1 upward and nothing clears the registers above them, so the shorter of the two answers out of a previous request's operands",
				entry.kind, carried, read)
		}
	}
	for kind := range sent {
		if hostControl[kind] {
			continue
		}
		if _, dispatched := dispatchKinds()[kind]; !dispatched {
			t.Errorf("the host writes %q and no dispatch line runs it, so every program reaching it fails on a request nothing answers", kind)
		}
	}
}

// dispatchKinds is every request kind a line runs, with the housing forms under
// the kind the host writes.
func dispatchKinds() map[string]bool {
	kinds := make(map[string]bool, len(dispatchLines))
	for _, entry := range dispatchLines {
		kinds[strings.TrimSuffix(entry.kind, chipSuffix)] = true
	}
	return kinds
}

// TestDispatchLineOperands covers the shapes the arity is read off: a form
// taking its device in a register, the same form spelling the housing out, and
// one taking no device at all.
func TestDispatchLineOperands(t *testing.T) {
	for _, tt := range []struct {
		name string
		line dispatchLine
		want int
	}{
		{name: "a device operand in a register", line: dispatchLine{kind: "l", code: "l r0 dr1 r2"}, want: 2},
		{name: "the housing spelled out", line: dispatchLine{kind: "l@db", code: "l r0 db r2"}, want: 2},
		{name: "the housing as the only operand", line: dispatchLine{kind: "dse@db", code: "sdse r0 db"}, want: 1},
		{name: "a device test in a register", line: dispatchLine{kind: "dse", code: "sdse r0 dr1"}, want: 1},
		{name: "no device at all", line: dispatchLine{kind: "lbns", code: "lbns r0 r1 r2 r3 r4 r5"}, want: 5},
		{name: "a result and nothing read", line: dispatchLine{kind: "rand", code: "rand r0"}, want: 0},
		{name: "no result and three read", line: dispatchLine{kind: "sbs", code: "sbs r1 r2 r3 r4"}, want: 4},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.line.operands(t); got != tt.want {
				t.Errorf("%q reads %d operands, want %d", tt.line.code, got, tt.want)
			}
		})
	}
}

// TestTheHostChipRefusesAWrongArity covers the guard itself: a request that
// does not carry what its line reads is refused rather than answered out of a
// register the previous one wrote.
func TestTheHostChipRefusesAWrongArity(t *testing.T) {
	ctx, h := chiptest.Fixtures(t)
	host := newHostChip(ctx, t, h, RunOptions{Name: "arity", World: pins(ic10.NumDevicePins)})
	for _, tt := range []struct {
		name string
		kind string
		args []float64
	}{
		{name: "one operand short", kind: "l", args: []float64{0}},
		{name: "one operand over", kind: "sqrt", args: []float64{1, 2}},
		{name: "a request nothing runs", kind: "nosuch", args: nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := host.call(ctx, tt.kind, tt.args); err == nil {
				t.Errorf("%s with %v was answered, and the answer would have come out of whatever the last request left behind", tt.kind, tt.args)
			}
		})
	}
}

// TestTheHostChipRefusesTheHousingsOwnChipState covers the housing properties
// the two builds do not share, named here rather than read out of
// [housingSelfProperties] so shortening that list fails this test instead of
// shrinking silently with it.
func TestTheHostChipRefusesTheHousingsOwnChipState(t *testing.T) {
	tests := []struct {
		name string
		// property is a logic type name from the generated tables.
		property string
		// onPin reads the property off d0 rather than off the housing, which is
		// the world on both sides and is never refused.
		onPin   bool
		refused bool
	}{
		{
			name: "the program counter is the asking chip's own", property: "LineNumber",
			refused: true,
		},
		{
			name: "the error light follows the asking chip's last instruction", property: "Error",
			refused: true,
		},
		{name: "the program counter of a device on a pin", property: "LineNumber", onPin: true},
		{name: "the error light of a device on a pin", property: "Error", onPin: true},
		{name: "a value the housing holds for whichever chip is in it", property: "Setting"},
		{name: "the display mode is stored the same way", property: "Mode"},
		{name: "a constant is the same on both sides", property: "Power"},
		{name: "so is the length of the array", property: "StackSize"},
	}

	ctx, h := chiptest.Fixtures(t)
	host := newHostChip(ctx, t, h, RunOptions{Name: "housing state", World: pins(ic10.NumDevicePins)})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			property, ok := ic10.LookupLogicType(tt.property)
			if !ok {
				t.Fatalf("the machine tables declare no %s", tt.property)
			}
			device := float64(chipPin)
			if tt.onPin {
				device = 0
			}
			_, _, err := host.call(ctx, "l", []float64{device, float64(property.Value)})
			switch {
			case tt.refused && err == nil:
				t.Errorf("reading %s off the housing was answered, and the answer is the dispatch program's own state", tt.property)
			case !tt.refused && err != nil:
				t.Errorf("reading %s was refused: %v", tt.property, err)
			}
		})
	}
}

func intrinsicNames(pattern *regexp.Regexp, text string) map[string]bool {
	out := make(map[string]bool)
	for _, match := range pattern.FindAllStringSubmatch(text, -1) {
		out[match[1]] = true
	}
	return out
}

// microcFile writes one MicroC program where a C driver can reach it.
func microcFile(t *testing.T, name, src string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return path
}

// TestRunNativeBoundsARunInSegments covers the two ways a native run ends and
// the alignment that makes it comparable: a segment is a turn of the source's
// own loop on this side as well, and the world is stepped between them.
func TestRunNativeBoundsARunInSegments(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		segments int
		want     []float64
		stop     StopReason
		ran      int
	}{
		{
			name: "a control loop the harness stops",
			src: `const dev out = d0;

void main(void) {
    long long n = 0;
    while (true) {
        n = n + 1;
        __ic_store(out, Setting, n);
        __ic_yield();
    }
}`,
			segments: 4,
			want:     []float64{1, 2, 3, 4},
			stop:     StopSegments,
			ran:      4,
		},
		{
			name: "a main that returns",
			src: `const dev out = d0;

void main(void) {
    __ic_store(out, Setting, 7);
}`,
			segments: 4,
			want:     []float64{7},
			stop:     StopEnded,
			ran:      1,
		},
		{
			name: "a reading the world moves between segments",
			src: `const dev in = d0;
const dev out = d1;

void main(void) {
    while (true) {
        __ic_store(out, Setting, __ic_load(in, Temperature) * 2.0);
        __ic_yield();
    }
}`,
			segments: 3,
			want:     []float64{20, 40, 60},
			stop:     StopSegments,
			ran:      3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, h := chiptest.Fixtures(t)
			trace := RunNative(ctx, t, h, microcFile(t, "program.c", tt.src), RunOptions{
				Name:     nativeName,
				Segments: tt.segments,
				World:    pins(ic10.NumDevicePins),
				Stimulus: func(t *testing.T, h *chip.FixtureHarness, segment int) {
					t.Helper()
					setTemperature(ctx, t, h, float64(10*(segment+1)))
				},
			})

			if trace.Stop.Reason != tt.stop {
				t.Errorf("the run ended because %s, want %s", trace.Stop, tt.stop)
			}
			if trace.Segments != tt.ran {
				t.Errorf("the run turned %d segments, want %d", trace.Segments, tt.ran)
			}
			if len(trace.Events) != len(tt.want) {
				t.Fatalf("the run made %d writes, want %d: %v", len(trace.Events), len(tt.want), trace.Events)
			}
			for i, want := range tt.want {
				if trace.Events[i].Value != want {
					t.Errorf("write %d is %s, want %v", i, formatWrite(trace.Events[i]), want)
				}
			}
		})
	}
}

// setTemperature seeds the one property the segment cases read.
func setTemperature(ctx context.Context, t *testing.T, h *chip.FixtureHarness, value float64) {
	t.Helper()
	if err := h.SetProperty(ctx, 0, logicType(t, "Temperature"), value); err != nil {
		t.Fatalf("seed Temperature: %v", err)
	}
}

// TestRunNativeCarriesAFaultBackAsTheStop covers the one way a native run can
// end that the program did not choose: an intrinsic the chip faults on ends
// the trace with that fault as its stop, not as a harness failure.
func TestRunNativeCarriesAFaultBackAsTheStop(t *testing.T) {
	const src = `const dev self = db;
const dev out = d0;

void main(void) {
    while (true) {
        __ic_store(out, Setting, 1);
        __ic_store(out, Setting, __ic_load(self, Temperature));
        __ic_yield();
    }
}`
	ctx, h := chiptest.Fixtures(t)
	trace := RunNative(ctx, t, h, microcFile(t, "fault.c", src),
		RunOptions{Name: nativeName, Segments: 4, World: pins(ic10.NumDevicePins)})

	if trace.Stop.Reason != StopFaulted {
		t.Fatalf("the run ended because %s, want the chip to have faulted", trace.Stop)
	}
	if trace.Stop.Fault != chip.ExcIncorrectLogicType {
		t.Errorf("the run faulted with %s, want %s", trace.Stop.Fault, chip.ExcIncorrectLogicType)
	}
	// The store before the fault is on the trace; the run stops where the chip
	// would have.
	if len(trace.Events) != 1 {
		t.Errorf("the run made %d writes, want the one before the fault: %v", len(trace.Events), trace.Events)
	}
	// The segment the fault happened in counts, which is what [Run] counts for
	// the same program. A count the two producers arrived at by different rules
	// makes [Diff] report a divergence between two runs that stopped at the
	// same point in the source.
	if trace.Segments != 1 {
		t.Errorf("the run turned %d segments, want the faulting one counted as 1", trace.Segments)
	}
}

// TestAFaultingRunCountsTheSameSegmentsOnBothProducers holds the two
// producers to one meaning of a segment count: a program faulting on its
// third turn must count three segments however it was run, or [Diff] reports
// a false divergence. The two sides are one program written twice by hand.
func TestAFaultingRunCountsTheSameSegmentsOnBothProducers(t *testing.T) {
	const source = `const dev self = db;
const dev out = d0;

void main(void) {
    long long n = 0;
    while (true) {
        n = n + 1;
        __ic_store(out, Setting, n);
        if (n >= 3) {
            __ic_store(out, Mode, __ic_load(self, Temperature));
        }
        __ic_yield();
    }
}`
	assembly := strings.Join([]string{
		"move r0 0",
		"add r0 r0 1",
		"s d0 Setting r0",
		"blt r0 3 6",
		"l r1 db Temperature",
		"s d0 Mode r1",
		"yield",
		"j 1",
	}, "\n")

	const segments = 8
	ctx, h := chiptest.Fixtures(t)
	native := RunNative(ctx, t, h, microcFile(t, "faulting.c", source),
		RunOptions{Name: nativeName, Segments: segments, World: pins(ic10.NumDevicePins)})
	// The second run resets the process the first used, and takes it over. That
	// is what a trace being a value bought: it is read out of the chip before
	// the run that made it returns, so the two compared below are two readings
	// rather than two live chips, and one process serves both.
	compiled := Run(ctx, t, h, assembly,
		RunOptions{Name: "chip", Segments: segments, World: pins(ic10.NumDevicePins)})

	for _, trace := range []Trace{native, compiled} {
		if trace.Stop.Reason != StopFaulted || trace.Stop.Fault != chip.ExcIncorrectLogicType {
			t.Fatalf("%s ended because %s, want the chip to have faulted with %s",
				trace.Name, trace.Stop, chip.ExcIncorrectLogicType)
		}
		if trace.Segments != 3 {
			t.Errorf("%s turned %d segments, want the third one it faulted in counted", trace.Name, trace.Segments)
		}
	}
	if err := Diff(native, compiled); err != nil {
		t.Errorf("two runs that faulted at the same point in the source are reported as differing: %v", err)
	}
}

// TestRunNativeComputesTheMachineHash covers the one thing the host computes
// rather than asks for. The compiler folds __ic_hash at compile time, so a host
// that asked the harness for it would leave that folding compared against
// nothing.
func TestRunNativeComputesTheMachineHash(t *testing.T) {
	const src = `const dev out = d0;

void main(void) {
    __ic_store(out, Setting, __ic_hash("StructureFurnace"));
    __ic_store(out, Mode, __ic_hash("Power Input"));
}`
	ctx, h := chiptest.Fixtures(t)
	trace := RunNative(ctx, t, h, microcFile(t, "hash.c", src),
		RunOptions{Name: nativeName, Segments: 1, World: pins(ic10.NumDevicePins)})

	want := []float64{float64(ic10.HashName("StructureFurnace")), float64(ic10.HashName("Power Input"))}
	if len(trace.Events) != len(want) {
		t.Fatalf("the run made %d writes, want %d: %v", len(trace.Events), len(want), trace.Events)
	}
	for i, hash := range want {
		if trace.Events[i].Value != hash {
			t.Errorf("write %d is %s, want %v", i, formatWrite(trace.Events[i]), hash)
		}
	}
}

// TestRunNativeReachesTheMachineFunctions holds each machine function
// intrinsic to what the machine computes for it, from a C source calling it
// by name. Every expected value below is the machine's own answer, which is
// not always C's — see the inline notes on round, clamp and lerp.
func TestRunNativeReachesTheMachineFunctions(t *testing.T) {
	const src = `const dev out = d0;

void main(void) {
    __ic_store_slot(out, 0, Damage, __ic_sqrt(6.25));
    __ic_store_slot(out, 1, Damage, __ic_abs(-2.5));
    __ic_store_slot(out, 2, Damage, __ic_sgn(-7.0));
    __ic_store_slot(out, 3, Damage, __ic_round(2.5));
    __ic_store_slot(out, 4, Damage, __ic_round(3.5));
    __ic_store_slot(out, 5, Damage, __ic_round(-2.5));
    __ic_store_slot(out, 6, Damage, __ic_trunc(-2.75));
    __ic_store_slot(out, 7, Damage, __ic_ceil(-2.75));
    __ic_store_slot(out, 8, Damage, __ic_floor(-2.75));
    __ic_store_slot(out, 9, Damage, __ic_log(1.0));
    __ic_store_slot(out, 10, Damage, __ic_exp(0.0));
    __ic_store_slot(out, 11, Damage, __ic_sin(0.0));
    __ic_store_slot(out, 12, Damage, __ic_cos(0.0));
    __ic_store_slot(out, 13, Damage, __ic_tan(0.0));
    __ic_store_slot(out, 14, Damage, __ic_asin(1.0));
    __ic_store_slot(out, 15, Damage, __ic_acos(1.0));
    __ic_store_slot(out, 16, Damage, __ic_atan(0.0));
    __ic_store_slot(out, 17, Damage, __ic_min(3.0, -4.0));
    __ic_store_slot(out, 18, Damage, __ic_max(3.0, -4.0));
    __ic_store_slot(out, 19, Damage, __ic_pow(2.0, 10.0));
    __ic_store_slot(out, 20, Damage, __ic_atan2(0.0, -1.0));
    __ic_store_slot(out, 21, Damage, __ic_clamp(9.0, 0.0, 4.0));
    __ic_store_slot(out, 22, Damage, __ic_clamp(4.0, 9.0, 0.0));
    __ic_store_slot(out, 23, Damage, __ic_lerp(10.0, 20.0, 0.25));
    __ic_store_slot(out, 24, Damage, __ic_lerp(10.0, 20.0, 4.0));
    __ic_store_slot(out, 25, Damage, __ic_isnan(__ic_sqrt(-1.0)));
}`
	// A function the harness dispatches and this source never calls would have
	// its mnemonic asserted nowhere, and adding one to the dispatch table is how
	// that happens quietly.
	for _, name := range machineFunctionNames() {
		if !strings.Contains(src, "__ic_"+name+"(") {
			t.Errorf("the harness dispatches %s and nothing here calls __ic_%s, so what the machine computes for it is asserted against no program", name, name)
		}
	}

	want := []float64{
		2.5, 2.5, -1,
		// A midpoint goes to even, both ways, which is not what C's round does.
		2, 4, -2,
		-2, -2, -3,
		0, 1, 0, 1, 0,
		math.Pi / 2, 0, 0,
		-4, 3, 1024, math.Pi,
		// clamp is min(max(a, b), c) and does not order its bounds, so a low
		// bound above the high one wins.
		4, 0,
		12.5,
		// lerp clamps the interpolant rather than extrapolating past b.
		20,
		1,
	}

	// Every slot starts holding a NaN, because a store of the value already
	// there is skipped and half of these answers are zero.
	ctx, h := chiptest.Fixtures(t)
	trace := RunNative(ctx, t, h, microcFile(t, "functions.c", src), RunOptions{
		Name:     nativeName,
		Segments: 1,
		World: func(t *testing.T, h *chip.FixtureHarness) {
			t.Helper()
			pins(ic10.NumDevicePins)(t, h)
			var world chip.Seeding
			damage := slotType(t, "Damage")
			for slot := range len(want) {
				world.SlotProperty(0, slot, damage, math.NaN())
			}
			if err := h.SeedWorld(ctx, &world); err != nil {
				t.Fatalf("seed the slots the answers land in: %v", err)
			}
		},
	})
	if trace.Stop.Reason != StopEnded {
		t.Fatalf("the run ended because %s, want the program to end", trace.Stop)
	}
	if len(trace.Events) != len(want) {
		t.Fatalf("the run made %d writes, want %d: %v", len(trace.Events), len(want), trace.Events)
	}
	for i, value := range want {
		if trace.Events[i].Value != value {
			t.Errorf("write %d is %s, want %v", i, formatWrite(trace.Events[i]), value)
		}
	}
}
