package frames

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/chip"
	"github.com/greg2010/ic11c/internal/chiptest"
	"github.com/greg2010/ic11c/internal/emit"
	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/irgen"
	"github.com/greg2010/ic11c/internal/isa"
	"github.com/greg2010/ic11c/internal/isel"
	"github.com/greg2010/ic11c/internal/llvmopt"
	"github.com/greg2010/ic11c/internal/mir"
	"github.com/greg2010/ic11c/internal/peephole"
	"github.com/greg2010/ic11c/internal/regalloc"
	"github.com/greg2010/ic11c/internal/sema"
	"github.com/greg2010/ic11c/internal/source"
	"github.com/greg2010/ic11c/internal/tsparse"
)

func TestMain(m *testing.M) { chiptest.Main(m) }

// framesFile is the name the compiled programs carry, so a diagnostic's
// position can be checked against a file as well as a line.
const framesFile = "frames.c"

// Programs the cases share. Each ends in a device store on an empty pin, which
// is unreachable in the programs that fault first and is what keeps the
// computation from being optimized away in the ones that do not.
const (
	// srcDirect recurses with nothing live across the call, so an activation
	// holds its saved return address and nothing else.
	srcDirect = `
const dev display = d0;

long long down(long long n) {
    if (n <= 0) {
        return 0;
    }
    return 1 + down(n - 1);
}

void main(void) {
    __ic_store(display, Setting, down(100000));
}
`
	// srcLiveAcrossCall keeps n live across the recursive call, which costs the
	// activation a second slot for the caller-saved register.
	srcLiveAcrossCall = `
const dev display = d0;

long long sum(long long n) {
    if (n <= 0) {
        return 0;
    }
    long long rest = sum(n - 1);
    return rest + n * n;
}

void main(void) {
    __ic_store(display, Setting, sum(100000));
}
`
	// srcMutualPair is recursion through a cycle of two.
	srcMutualPair = `
const dev display = d0;

long long ping(long long n);

long long pong(long long n) {
    if (n <= 0) {
        return 0;
    }
    return 1 + ping(n - 1);
}

long long ping(long long n) {
    if (n <= 0) {
        return 0;
    }
    return 1 + pong(n - 1);
}

void main(void) {
    __ic_store(display, Setting, ping(100000));
}
`
	// srcCallerHolds keeps a value live in main across the deep call, so
	// the chain into the recursion holds a slot of its own before the
	// first activation exists. It reaches the recursion twice holding
	// different amounts, so the count it states is a floor.
	srcCallerHolds = `
const dev display = d0;

long long down(long long n) {
    if (n <= 0) {
        return 0;
    }
    return 1 + down(n - 1);
}

void main(void) {
    long long shallow = down(3);
    long long deep = down(100000);
    __ic_store(display, Setting, shallow + deep);
}
`
	// srcMutualUneven is a cycle whose two members hold different amounts, so
	// the count the report states is a floor rather than the answer.
	srcMutualUneven = `
const dev display = d0;

long long ping(long long n);

long long pong(long long n) {
    if (n <= 0) {
        return 0;
    }
    return 1 + ping(n - 1);
}

long long ping(long long n) {
    if (n <= 0) {
        return 0;
    }
    long long rest = pong(n - 1);
    return rest + n;
}

void main(void) {
    __ic_store(display, Setting, ping(100000));
}
`
	// srcMutualTriple is recursion through a cycle of three, which no pair of
	// functions closes on its own.
	srcMutualTriple = `
const dev display = d0;

long long a(long long n);
long long b(long long n);

long long c(long long n) {
    if (n <= 0) {
        return 0;
    }
    return 1 + a(n - 1);
}

long long b(long long n) {
    if (n <= 0) {
        return 0;
    }
    return 1 + c(n - 1);
}

long long a(long long n) {
    if (n <= 0) {
        return 0;
    }
    return 1 + b(n - 1);
}

void main(void) {
    __ic_store(display, Setting, a(100000));
}
`
	// srcTailRecursive is recursive in the source and a loop by the time the
	// backend sees it. Analysis over the source calls it recursive; nothing is
	// re-entered on the machine, so no frame grows.
	srcTailRecursive = `
const dev display = d0;

long long count(long long n, long long acc) {
    if (n <= 0) {
        return acc;
    }
    return count(n - 1, acc + n);
}

void main(void) {
    __ic_store(display, Setting, count(1000, 0));
}
`
	// srcDeepChain is a call chain deep enough to have a stack on a
	// conventional machine and no call at all here: every one is inlined.
	srcDeepChain = `
const dev display = d0;

long long l1(long long n) { return n + 1; }
long long l2(long long n) { return l1(n) + 2; }
long long l3(long long n) { return l2(n) + 3; }
long long l4(long long n) { return l3(n) + 4; }
long long l5(long long n) { return l4(n) + 5; }
long long l6(long long n) { return l5(n) + 6; }
long long l7(long long n) { return l6(n) + 7; }
long long l8(long long n) { return l7(n) + 8; }

void main(void) {
    __ic_store(display, Setting, l8(1));
}
`
	// srcManyCallSites calls one function from eight places, which is the shape
	// a call-count heuristic would mistake for a cycle.
	srcManyCallSites = `
const dev display = d0;

long long scale(long long n) { return n * 3 + 1; }

void main(void) {
    long long t = 0;
    t += scale(1);
    t += scale(2);
    t += scale(3);
    t += scale(4);
    t += scale(5);
    t += scale(6);
    t += scale(7);
    t += scale(8);
    __ic_store(display, Setting, t);
}
`
	// srcNested puts one recursion inside another, so neither has a depth the
	// slots decide on their own.
	srcNested = `
const dev display = d0;

long long inner(long long n) {
    if (n <= 0) {
        return 0;
    }
    return 1 + inner(n - 1);
}

long long outer(long long n) {
    if (n <= 0) {
        return 0;
    }
    return inner(n) + outer(n - 1);
}

void main(void) {
    __ic_store(display, Setting, outer(100000));
}
`
	// srcNestedAndCalledDirectly gives the inner recursion a caller
	// outside the outer one as well as the one inside it: the static
	// chain into inner holds nothing, so reading spent off that chain
	// alone would state a count as if nothing else were above inner.
	srcNestedAndCalledDirectly = `
const dev display = d0;

long long inner(long long n) {
    if (n <= 0) {
        return 0;
    }
    return 1 + inner(n - 1);
}

long long outer(long long n) {
    if (n <= 0) {
        return 0;
    }
    return inner(n) + outer(n - 1);
}

void main(void) {
    __ic_store(display, Setting, outer(100000) + inner(5));
}
`
)

func TestMeasure(t *testing.T) {
	cases := []struct {
		name string
		src  string
		// base overrides the stack base the pipeline chose, which is how a case
		// states a data region without declaring the globals to fill one.
		base int
		want []string
	}{
		{
			name: "direct recursion holds one slot per activation",
			src:  srcDirect,
			want: []string{"warning: 'down' can reach itself through a call, and how deep it goes is not decided at compile time; of the 512 slots left above the data region the calls into it hold 0 slots and its deepest activation holds 1 slot, and every activation above that one holds 1 slot, so there is room for 512 activations and the next one faults on a push — bound the depth in the source, or rewrite the recursion as a loop"},
		},
		{
			name: "a value live across the call costs the activation a second slot",
			src:  srcLiveAcrossCall,
			want: []string{"warning: 'sum' can reach itself through a call, and how deep it goes is not decided at compile time; of the 512 slots left above the data region the calls into it hold 0 slots and its deepest activation holds 1 slot, and every activation above that one holds 2 slots, so there is room for 256 activations and the next one faults on a push — bound the depth in the source, or rewrite the recursion as a loop"},
		},
		{
			name: "mutual recursion through two functions is one cycle",
			src:  srcMutualPair,
			want: []string{"warning: 'ping' and 'pong' can reach each other through a call, and how deep it goes is not decided at compile time; of the 512 slots left above the data region the calls into it hold 0 slots and its deepest activation holds 1 slot, and every activation above that one holds 1 slot, so there is room for 512 activations and the next one faults on a push — bound the depth in the source, or rewrite the recursion as a loop"},
		},
		{
			name: "mutual recursion through three functions is one cycle",
			src:  srcMutualTriple,
			want: []string{"warning: 'a', 'b' and 'c' can reach one another through a call, and how deep it goes is not decided at compile time; of the 512 slots left above the data region the calls into it hold 0 slots and its deepest activation holds 1 slot, and every activation above that one holds 1 slot, so there is room for 512 activations and the next one faults on a push — bound the depth in the source, or rewrite the recursion as a loop"},
		},
		{
			name: "the chain into the recursion holds slots of its own",
			src:  srcCallerHolds,
			want: []string{"warning: 'down' can reach itself through a call, and how deep it goes is not decided at compile time; the calls that reach it do not all hold the same, so the count is measured over the largest: of the 512 slots left above the data region the calls into it hold 1 slot and its deepest activation holds 1 slot, and every activation above that one holds 1 slot, so there is room for at least 511 activations — bound the depth in the source, or rewrite the recursion as a loop"},
		},
		{
			name: "a cycle whose members hold different amounts states a floor",
			src:  srcMutualUneven,
			want: []string{"warning: 'ping' and 'pong' can reach each other through a call, and how deep it goes is not decided at compile time; the members of the cycle do not all hold the same, so the count is measured over the largest: of the 512 slots left above the data region the calls into it hold 0 slots and its deepest activation holds 1 slot, and every activation above that one holds 2 slots, so there is room for at least 256 activations — bound the depth in the source, or rewrite the recursion as a loop"},
		},
		{
			name: "tail recursion the optimizer turned into a loop is not recursion here",
			src:  srcTailRecursive,
		},
		{
			name: "a deep chain of ordinary calls needs no frame at all",
			src:  srcDeepChain,
		},
		{
			name: "one function called from eight places is not a cycle",
			src:  srcManyCallSites,
		},
		{
			name: "a recursion inside another recursion states no depth",
			src:  srcNested,
			want: []string{
				"warning: 'inner' can reach itself through a call, and how deep it goes is not decided at compile time; each activation holds 1 slot of the 512 slots left above the data region, and it is entered from inside another recursion whose own depth is not decided either, so no activation count holds for it — bound the depth in the source, or rewrite the recursion as a loop",
				"warning: 'outer' can reach itself through a call, and how deep it goes is not decided at compile time; each activation holds 2 slots of the 512 slots left above the data region, and it calls into another recursion whose own depth is not decided either, so no activation count holds for it — bound the depth in the source, or rewrite the recursion as a loop",
			},
		},
		{
			name: "a static caller does not decide a depth another recursion also enters",
			src:  srcNestedAndCalledDirectly,
			want: []string{
				"warning: 'inner' can reach itself through a call, and how deep it goes is not decided at compile time; each activation holds 1 slot of the 512 slots left above the data region, and it is entered from inside another recursion whose own depth is not decided either, so no activation count holds for it — bound the depth in the source, or rewrite the recursion as a loop",
				"warning: 'outer' can reach itself through a call, and how deep it goes is not decided at compile time; each activation holds 2 slots of the 512 slots left above the data region, and it calls into another recursion whose own depth is not decided either, so no activation count holds for it — bound the depth in the source, or rewrite the recursion as a loop",
			},
		},
		{
			name: "a data region leaving two slots holds two activations",
			src:  srcDirect,
			base: ic10.NumMemorySlots - 2,
			want: []string{"warning: 'down' can reach itself through a call, and how deep it goes is not decided at compile time; of the 2 slots left above the data region the calls into it hold 0 slots and its deepest activation holds 1 slot, and every activation above that one holds 1 slot, so there is room for 2 activations and the next one faults on a push — bound the depth in the source, or rewrite the recursion as a loop"},
		},
		{
			name: "a data region leaving one slot holds one activation",
			src:  srcDirect,
			base: ic10.NumMemorySlots - 1,
			want: []string{"warning: 'down' can reach itself through a call, and how deep it goes is not decided at compile time; of the 1 slot left above the data region the calls into it hold 0 slots and its deepest activation holds 1 slot, and every activation above that one holds 1 slot, so there is room for 1 activation and the next one faults on a push — bound the depth in the source, or rewrite the recursion as a loop"},
		},
		{
			name: "a full data region leaves no room for the first activation",
			src:  srcDirect,
			base: ic10.NumMemorySlots,
			want: []string{"'down' can reach itself through a call, and the data region leaves no room for even one activation: 0 slots remain above it, the call into it already holds 0 slots, and an activation holds 1 slot more — shorten an array, drop a global, or rewrite the recursion as a loop"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			built := build(t, tc.src)
			base := built.base
			if tc.base != 0 {
				base = tc.base
			}
			diags, err := Measure(t.Context(), built.prog, Options{StackBase: base})
			if err != nil {
				t.Fatalf("Frames: %v", err)
			}
			got := make([]string, len(diags))
			for i, diag := range diags {
				if diag.Pos.File != framesFile {
					t.Errorf("diagnostic %d is at %s, want a position in %s", i, diag.Pos, framesFile)
				}
				got[i] = messageOf(diag)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d diagnostics, want %d:\n%s", len(got), len(tc.want), strings.Join(got, "\n"))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("diagnostic %d:\n got %s\nwant %s", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestMeasureSeverity holds the one condition that rejects a program rather than
// describing it. A recursion the slots cannot hold one activation of faults on
// its first call whatever the data says, which is the only verdict the
// arithmetic reaches on its own.
func TestMeasureSeverity(t *testing.T) {
	built := build(t, srcDirect)
	cases := []struct {
		name string
		base int
		want source.Severity
	}{
		{name: "the whole array left for frames", base: 0, want: source.Warning},
		{name: "one slot left for frames", base: ic10.NumMemorySlots - 1, want: source.Warning},
		{name: "no slot left for frames", base: ic10.NumMemorySlots, want: source.Error},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diags, err := Measure(t.Context(), built.prog, Options{StackBase: tc.base})
			if err != nil {
				t.Fatalf("Frames: %v", err)
			}
			if len(diags) != 1 {
				t.Fatalf("got %d diagnostics, want 1", len(diags))
			}
			if diags[0].Severity != tc.want {
				t.Errorf("severity is %s, want %s: %s", diags[0].Severity, tc.want, diags[0].Msg)
			}
		})
	}
}

func TestMeasureRejects(t *testing.T) {
	built := build(t, srcDirect)
	cases := []struct {
		name string
		prog *mir.Program
		base int
	}{
		{name: "no program", prog: nil, base: 0},
		{name: "no functions", prog: &mir.Program{}, base: 0},
		{name: "a stack base below the array", prog: built.prog, base: -1},
		{name: "a stack base past the array", prog: built.prog, base: ic10.NumMemorySlots + 1},
		{
			name: "a call naming no function in the program",
			prog: &mir.Program{Funcs: []*mir.Func{stackFunc(t, "main", false, frameCall{target: "ghost"})}},
		},
		{
			name: "a conditional call naming no function in the program",
			prog: &mir.Program{Funcs: []*mir.Func{stackFunc(t, "main", false, frameCall{target: "ghost", op: isa.OpBltal})}},
		},
		{
			name: "a first function that saves a return address, so it is not the entry",
			prog: &mir.Program{Funcs: []*mir.Func{stackFunc(t, "main", true, frameCall{target: "main"})}},
		},
		{
			// A frame measured before allocation is missing the caller-saved
			// pushes allocation has not placed, so every activation comes out
			// smaller than it is and the depth the slots hold comes out larger.
			// Nothing downstream would read that as a failure.
			name: "a function still naming virtual registers",
			prog: &mir.Program{Funcs: []*mir.Func{
				stackFunc(t, "main", false, frameCall{target: "down"}),
				withVirtualCopy(t, stackFunc(t, "down", true, frameCall{target: "down"})),
			}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Measure(t.Context(), tc.prog, Options{StackBase: tc.base}); err == nil {
				t.Fatal("Frames accepted an input it cannot measure")
			}
		})
	}
}

// TestMeasureSaysNothingAboutARecursionNothingReaches covers the call graph the
// front end cannot produce: a recursive definition the entry point never calls.
// Its frames are never built, so a diagnostic about them would describe code
// that does not run.
func TestMeasureSaysNothingAboutARecursionNothingReaches(t *testing.T) {
	prog := &mir.Program{Funcs: []*mir.Func{
		stackFunc(t, "main", false),
		stackFunc(t, "orphan", true, frameCall{target: "orphan"}),
	}}
	diags, err := Measure(t.Context(), prog, Options{})
	if err != nil {
		t.Fatalf("Frames: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("got %d diagnostics about a recursion nothing reaches:\n%s", len(diags), diags)
	}
}

// TestMeasureReportsTheChainAboveTheRecursion holds the slots the
// calls into a recursion hold to being subtracted from what the
// recursion has. The second row adds a call in the middle: forwarding
// only the immediate call would undercount every chain more than one call deep.
func TestMeasureReportsTheChainAboveTheRecursion(t *testing.T) {
	tests := []struct {
		name  string
		funcs []*mir.Func
		want  string
	}{
		{
			// main holds two caller-saved registers across its call and middle
			// its return address, so three of the twelve slots are gone before
			// the first activation of loop exists and the remaining nine are one
			// apiece.
			name: "one call reaches the recursion",
			funcs: []*mir.Func{
				stackFunc(t, "main", false, frameCall{target: "middle", saves: 2}),
				stackFunc(t, "middle", true, frameCall{target: "loop"}),
				stackFunc(t, "loop", true, frameCall{target: "loop"}),
			},
			want: "room for 9 activations",
		},
		{
			// main's three saves, then a return address at each of the two
			// functions below it, leaves six of the twelve.
			name: "two calls reach the recursion",
			funcs: []*mir.Func{
				stackFunc(t, "main", false, frameCall{target: "outer", saves: 3}),
				stackFunc(t, "outer", true, frameCall{target: "middle"}),
				stackFunc(t, "middle", true, frameCall{target: "loop"}),
				stackFunc(t, "loop", true, frameCall{target: "loop"}),
			},
			want: "room for 7 activations",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prog := &mir.Program{Funcs: tt.funcs}
			diags, err := Measure(t.Context(), prog, Options{StackBase: ic10.NumMemorySlots - 12})
			if err != nil {
				t.Fatalf("Frames: %v", err)
			}
			if len(diags) != 1 {
				t.Fatalf("got %d diagnostics, want 1:\n%s", len(diags), diags)
			}
			if !strings.Contains(diags[0].Msg, tt.want) {
				t.Errorf("the diagnostic does not state %q:\n%s", tt.want, diags[0].Msg)
			}
		})
	}
}

// TestMeasureReportsARecursionHoldingNothing covers a cycle whose
// activations each hold no slots. Nothing the backend emits reaches
// it: a function that calls out always saves its return address at
// entry, so every activation holds at least that slot.
func TestMeasureReportsARecursionHoldingNothing(t *testing.T) {
	prog := &mir.Program{Funcs: []*mir.Func{
		stackFunc(t, "main", false, frameCall{target: "a"}),
		stackFunc(t, "a", false, frameCall{target: "b"}),
		stackFunc(t, "b", false, frameCall{target: "a"}),
	}}
	diags, err := Measure(t.Context(), prog, Options{StackBase: ic10.NumMemorySlots - 12})
	if err != nil {
		t.Fatalf("Frames: %v", err)
	}
	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want 1:\n%s", len(diags), diags)
	}
	if diags[0].Severity != source.Warning {
		t.Errorf("the recursion is reported at severity %v, want a warning", diags[0].Severity)
	}
	for _, want := range []string{"'a' and 'b'", "holds none of the 12 slots"} {
		if !strings.Contains(diags[0].Msg, want) {
			t.Errorf("the diagnostic does not state %q:\n%s", want, diags[0].Msg)
		}
	}
}

// TestMeasureLeavesAChainEndingInARecursionOutOfTheDepth covers a
// function every chain out of which enters a recursion: such a
// function reaches a depth the data decides, which the recursion
// itself reports rather than a number folded into the chain above it.
func TestMeasureLeavesAChainEndingInARecursionOutOfTheDepth(t *testing.T) {
	// 'b' is called from inside 'r' as well as from main, so what it is entered
	// holding is a depth 'r' decides and neither recursion below it can be
	// counted against the slots. What is left is the chain main to b, which
	// holds b's return address alone.
	prog := &mir.Program{Funcs: []*mir.Func{
		stackFunc(t, "main", false, frameCall{target: "r"}, frameCall{target: "b"}),
		stackFunc(t, "r", true, frameCall{target: "r"}, frameCall{target: "b"}),
		stackFunc(t, "b", true, frameCall{target: "c", saves: 4}),
		stackFunc(t, "c", true, frameCall{target: "loop"}),
		stackFunc(t, "loop", true, frameCall{target: "loop"}),
	}}

	diags, err := Measure(t.Context(), prog, Options{StackBase: ic10.NumMemorySlots - 4})
	if err != nil {
		t.Fatalf("Frames: %v", err)
	}
	if len(diags) != 2 {
		t.Fatalf("got %d diagnostics, want the two recursions alone:\n%s", len(diags), diags)
	}
	for _, diag := range diags {
		if diag.Severity != source.Warning {
			t.Errorf("a recursion whose depth the data decides is reported at severity %v, want a warning:\n%s", diag.Severity, diag.Msg)
		}
		if strings.Contains(diag.Msg, "nest deep enough") {
			t.Errorf("the chain into a recursion was counted as a depth of its own:\n%s", diag.Msg)
		}
	}
}

// TestMeasureStatesAFloorWhereAnAmountVaries covers the three maxima
// the count is built from. Reading only one of the three would state
// a definite count for the others, understating the recursion's true
// capacity while sounding certain.
func TestMeasureStatesAFloorWhereAnAmountVaries(t *testing.T) {
	cases := []struct {
		name  string
		funcs []*mir.Func
		want  string
	}{
		{
			name: "two chains reach the recursion holding different amounts",
			funcs: []*mir.Func{
				stackFunc(t, "main", false, frameCall{target: "a", saves: 5}, frameCall{target: "b", saves: 1}),
				stackFunc(t, "a", true, frameCall{target: "r"}),
				stackFunc(t, "b", true, frameCall{target: "r"}),
				stackFunc(t, "r", true, frameCall{target: "r"}),
			},
			want: "warning: 'r' can reach itself through a call, and how deep it goes is not decided at compile time; the calls that reach it do not all hold the same, so the count is measured over the largest: of the 512 slots left above the data region the calls into it hold 6 slots and its deepest activation holds 1 slot, and every activation above that one holds 1 slot, so there is room for at least 506 activations — bound the depth in the source, or rewrite the recursion as a loop",
		},
		{
			name: "one caller is reached two ways holding different amounts",
			funcs: []*mir.Func{
				stackFunc(t, "main", false, frameCall{target: "m", saves: 5}, frameCall{target: "m", saves: 1}),
				stackFunc(t, "m", true, frameCall{target: "r"}),
				stackFunc(t, "r", true, frameCall{target: "r"}),
			},
			want: "warning: 'r' can reach itself through a call, and how deep it goes is not decided at compile time; the calls that reach it do not all hold the same, so the count is measured over the largest: of the 512 slots left above the data region the calls into it hold 6 slots and its deepest activation holds 1 slot, and every activation above that one holds 1 slot, so there is room for at least 506 activations — bound the depth in the source, or rewrite the recursion as a loop",
		},
		{
			// Two chains reach the recursion holding the same amount, one with
			// a choice behind it. Folding the second in as exact would lose
			// the first's choice, understating by the cheaper run's five slots.
			name: "two chains hold the same amount and one of them varies",
			funcs: []*mir.Func{
				stackFunc(t, "main", false, frameCall{target: "a", saves: 5}, frameCall{target: "a", saves: 1}, frameCall{target: "b", saves: 5}),
				stackFunc(t, "a", true, frameCall{target: "r"}),
				stackFunc(t, "b", true, frameCall{target: "r"}),
				stackFunc(t, "r", true, frameCall{target: "r"}),
			},
			want: "warning: 'r' can reach itself through a call, and how deep it goes is not decided at compile time; the calls that reach it do not all hold the same, so the count is measured over the largest: of the 512 slots left above the data region the calls into it hold 6 slots and its deepest activation holds 1 slot, and every activation above that one holds 1 slot, so there is room for at least 506 activations — bound the depth in the source, or rewrite the recursion as a loop",
		},
		{
			// A cycle of one, so the deepest activation is measured from a single
			// member and that member's own choice is the whole of what makes the
			// tail a bound. A cycle of two hides that: its members also disagree
			// on the amount, forcing the same verdict for a different reason.
			name: "the only member of the cycle bottoms out two ways",
			funcs: []*mir.Func{
				stackFunc(t, "main", false, frameCall{target: "r"}),
				stackFunc(t, "r", true, frameCall{target: "leaf", saves: 1}, frameCall{target: "r"}),
				stackFunc(t, "leaf", false),
			},
			want: "warning: 'r' can reach itself through a call, and how deep it goes is not decided at compile time; the ways it bottoms out do not all hold the same, so the count is measured over the largest: of the 512 slots left above the data region the calls into it hold 0 slots and its deepest activation holds 2 slots, and every activation above that one holds 1 slot, so there is room for at least 511 activations — bound the depth in the source, or rewrite the recursion as a loop",
		},
		{
			name: "only one member of the cycle calls into a deeper chain",
			funcs: []*mir.Func{
				stackFunc(t, "main", false, frameCall{target: "p"}),
				stackFunc(t, "p", true, frameCall{target: "q"}, frameCall{target: "leaf", saves: 1}),
				stackFunc(t, "q", true, frameCall{target: "p"}),
				stackFunc(t, "leaf", false),
			},
			want: "warning: 'p' and 'q' can reach each other through a call, and how deep it goes is not decided at compile time; the ways it bottoms out do not all hold the same, so the count is measured over the largest: of the 512 slots left above the data region the calls into it hold 0 slots and its deepest activation holds 2 slots, and every activation above that one holds 1 slot, so there is room for at least 511 activations — bound the depth in the source, or rewrite the recursion as a loop",
		},
		{
			// The choice is one call further up than the calls the recursion is
			// reached by, so only the conjunction each step passes on carries it
			// down. A cheaper run up there reaches four activations further
			// than the count says.
			name: "the chain reaching the recursion had a choice above it",
			funcs: []*mir.Func{
				stackFunc(t, "main", false, frameCall{target: "a", saves: 5}, frameCall{target: "a", saves: 1}),
				stackFunc(t, "a", true, frameCall{target: "b"}),
				stackFunc(t, "b", true, frameCall{target: "r"}),
				stackFunc(t, "r", true, frameCall{target: "r"}),
			},
			want: "warning: 'r' can reach itself through a call, and how deep it goes is not decided at compile time; the calls that reach it do not all hold the same, so the count is measured over the largest: of the 512 slots left above the data region the calls into it hold 7 slots and its deepest activation holds 1 slot, and every activation above that one holds 1 slot, so there is room for at least 505 activations — bound the depth in the source, or rewrite the recursion as a loop",
		},
		{
			name: "all three vary at once",
			funcs: []*mir.Func{
				stackFunc(t, "main", false, frameCall{target: "p", saves: 5}, frameCall{target: "q", saves: 1}),
				stackFunc(t, "p", true, frameCall{target: "q"}, frameCall{target: "leaf", saves: 1}),
				stackFunc(t, "q", true, frameCall{target: "p", saves: 1}),
				stackFunc(t, "leaf", false),
			},
			want: "warning: 'p' and 'q' can reach each other through a call, and how deep it goes is not decided at compile time; the members of the cycle, the calls that reach it and the ways it bottoms out do not all hold the same, so the count is measured over the largest: of the 512 slots left above the data region the calls into it hold 5 slots and its deepest activation holds 2 slots, and every activation above that one holds 2 slots, so there is room for at least 253 activations — bound the depth in the source, or rewrite the recursion as a loop",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diags, err := Measure(t.Context(), &mir.Program{Funcs: tc.funcs}, Options{})
			if err != nil {
				t.Fatalf("Frames: %v", err)
			}
			if len(diags) != 1 {
				t.Fatalf("got %d diagnostics, want 1:\n%s", len(diags), diags)
			}
			if got := messageOf(diags[0]); got != tc.want {
				t.Errorf("\n got %s\nwant %s", got, tc.want)
			}
		})
	}
}

// TestMeasureStatesNoDepthWhereARecursionReachesTheCallersOfOne
// covers a function reached both along a recursion-free chain and
// from inside a recursive component. The static chain alone would
// count a stack the recursion above it actually extends without bound.
func TestMeasureStatesNoDepthWhereARecursionReachesTheCallersOfOne(t *testing.T) {
	// The two reports the graphs below produce. 'r' calls into a recursion, so
	// its own tail is undecided; 'c' is entered from inside one, so its head is.
	const (
		callsIn     = "warning: 'r' can reach itself through a call, and how deep it goes is not decided at compile time; each activation holds 4 slots of the 512 slots left above the data region, and it calls into another recursion whose own depth is not decided either, so no activation count holds for it — bound the depth in the source, or rewrite the recursion as a loop"
		enteredFrom = "warning: 'c' can reach itself through a call, and how deep it goes is not decided at compile time; each activation holds 2 slots of the 512 slots left above the data region, and it is entered from inside another recursion whose own depth is not decided either, so no activation count holds for it — bound the depth in the source, or rewrite the recursion as a loop"
	)

	cases := []struct {
		name  string
		funcs []*mir.Func
	}{
		{
			// 'a' is called by main and by 'r', so the chain into 'c' holds one
			// slot along the static route and 4k+2 along the recursive one.
			name: "a recursion calls the caller of another recursion",
			funcs: []*mir.Func{
				declaredAt(stackFunc(t, "main", false, frameCall{target: "a"}, frameCall{target: "r"}), 1),
				declaredAt(stackFunc(t, "r", true, frameCall{target: "r", saves: 3}, frameCall{target: "a"}), 10),
				declaredAt(stackFunc(t, "a", true, frameCall{target: "c"}), 20),
				declaredAt(stackFunc(t, "c", true, frameCall{target: "c", saves: 1}), 30),
			},
		},
		{
			// The same, with a link between the recursion's callee and the
			// caller of the second recursion, so the answer has to carry
			// forward rather than stopping at the function the call names.
			name: "the recursion reaches that caller down a chain of its own",
			funcs: []*mir.Func{
				declaredAt(stackFunc(t, "main", false, frameCall{target: "a"}, frameCall{target: "r"}), 1),
				declaredAt(stackFunc(t, "r", true, frameCall{target: "r", saves: 3}, frameCall{target: "a"}), 10),
				declaredAt(stackFunc(t, "a", true, frameCall{target: "b"}), 20),
				declaredAt(stackFunc(t, "b", true, frameCall{target: "c"}), 25),
				declaredAt(stackFunc(t, "c", true, frameCall{target: "c", saves: 1}), 30),
			},
		},
		{
			// The same chain, with the entry point also calling 'b'. 'b' is
			// therefore reached along a chain that settles what it is entered
			// holding, and the answer that it is entered on a stack the recursion
			// decides has to beat that rather than merely arrive where nothing else did.
			name: "the entry point also reaches that caller along a chain of its own",
			funcs: []*mir.Func{
				declaredAt(stackFunc(t, "main", false, frameCall{target: "a"}, frameCall{target: "b"}, frameCall{target: "r"}), 1),
				declaredAt(stackFunc(t, "r", true, frameCall{target: "r", saves: 3}, frameCall{target: "a"}), 10),
				declaredAt(stackFunc(t, "a", true, frameCall{target: "b"}), 20),
				declaredAt(stackFunc(t, "b", true, frameCall{target: "c"}), 25),
				declaredAt(stackFunc(t, "c", true, frameCall{target: "c", saves: 1}), 30),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diags, err := Measure(t.Context(), &mir.Program{Funcs: tc.funcs}, Options{})
			if err != nil {
				t.Fatalf("Frames: %v", err)
			}
			want := []string{callsIn, enteredFrom}
			if len(diags) != len(want) {
				t.Fatalf("got %d diagnostics, want %d:\n%s", len(diags), len(want), diags)
			}
			for i, msg := range want {
				if got := messageOf(diags[i]); got != msg {
					t.Errorf("diagnostic %d:\n got %s\nwant %s", i, got, msg)
				}
			}
		})
	}
}

// TestMeasureCountsARecursionAnUnreachableCallerAlsoReaches is the
// other side of the mark above: a caller the entry point never
// reaches builds no frame, so it must leave nothing undecided
// either. Each case covers one of the three places the walk asks whether a function is reached.
func TestMeasureCountsARecursionAnUnreachableCallerAlsoReaches(t *testing.T) {
	// main enters 'a', which holds its return address across the call into the
	// recursion, so one of the twelve slots is gone and the remaining eleven are
	// one apiece.
	const want = "room for 11 activations"

	live := []*mir.Func{
		stackFunc(t, "main", false, frameCall{target: "a"}),
		stackFunc(t, "a", true, frameCall{target: "loop"}),
		stackFunc(t, "loop", true, frameCall{target: "loop"}),
	}
	cases := []struct {
		name string
		dead *mir.Func
	}{
		{
			name: "a dead caller of the function that enters the recursion",
			dead: stackFunc(t, "orphan", true, frameCall{target: "a", saves: 4}),
		},
		{
			// The dead function calls into the component itself, so its own
			// unsettled amount reaches the walk that measures the stack above
			// the recursion rather than stopping one call short of it.
			name: "a dead caller of the recursion itself",
			dead: stackFunc(t, "orphan", true, frameCall{target: "loop", saves: 4}),
		},
		{
			// A dead recursion, which is the one shape that marks what it calls
			// as holding a depth nothing decides. Its own frames are never
			// built, so the live chain into 'a' is still the whole of what
			// enters it.
			name: "a dead recursion that also reaches the live caller",
			dead: stackFunc(t, "deadrec", true, frameCall{target: "deadrec"}, frameCall{target: "a"}),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog := &mir.Program{Funcs: append(slices.Clone(live), tc.dead)}
			diags, err := Measure(t.Context(), prog, Options{StackBase: ic10.NumMemorySlots - 12})
			if err != nil {
				t.Fatalf("Frames: %v", err)
			}
			if len(diags) != 1 {
				t.Fatalf("got %d diagnostics, want 1:\n%s", len(diags), diags)
			}
			if !strings.Contains(diags[0].Msg, want) {
				t.Errorf("the diagnostic does not state %q:\n%s", want, diags[0].Msg)
			}
		})
	}
}

// TestMeasureCountsARecursionTheEntryPointIsPartOf covers the one
// component nothing calls into: the entry point, entered at line 0
// along no call, so the stack above it holds nothing on every run —
// a count, not the refusal a callers-inside-a-recursion component gets.
func TestMeasureCountsARecursionTheEntryPointIsPartOf(t *testing.T) {
	// The entry saves no return address and pushes three registers around its
	// own call, so 170 activations hold 510 of the 512 slots and the deepest
	// holds none.
	const want = "warning: 'main' can reach itself through a call, and how deep it goes is not decided at compile time; of the 512 slots left above the data region the calls into it hold 0 slots and its deepest activation holds 0 slots, and every activation above that one holds 3 slots, so there is room for 171 activations and the next one faults on a push — bound the depth in the source, or rewrite the recursion as a loop"

	prog := &mir.Program{Funcs: []*mir.Func{
		stackFunc(t, "main", false, frameCall{target: "main", saves: 3}),
	}}
	diags, err := Measure(t.Context(), prog, Options{})
	if err != nil {
		t.Fatalf("Frames: %v", err)
	}
	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want 1:\n%s", len(diags), diags)
	}
	if got := messageOf(diags[0]); got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
}

// TestMeasureStatesNoDepthWhereTheEntryPointIsItselfRecursive covers
// a second recursion reached from an entry point that is part of
// one: an activation calling out is entered on however many
// activations of its own recursion preceded it, which the slots do not decide.
func TestMeasureStatesNoDepthWhereTheEntryPointIsItselfRecursive(t *testing.T) {
	prog := &mir.Program{Funcs: []*mir.Func{
		declaredAt(stackFunc(t, "main", false, frameCall{target: "main", saves: 2}, frameCall{target: "rec"}), 1),
		declaredAt(stackFunc(t, "rec", true, frameCall{target: "rec"}), 10),
	}}
	want := []string{
		"warning: 'main' can reach itself through a call, and how deep it goes is not decided at compile time; each activation holds 2 slots of the 512 slots left above the data region, and it calls into another recursion whose own depth is not decided either, so no activation count holds for it — bound the depth in the source, or rewrite the recursion as a loop",
		"warning: 'rec' can reach itself through a call, and how deep it goes is not decided at compile time; each activation holds 1 slot of the 512 slots left above the data region, and it is entered from inside another recursion whose own depth is not decided either, so no activation count holds for it — bound the depth in the source, or rewrite the recursion as a loop",
	}
	diags, err := Measure(t.Context(), prog, Options{})
	if err != nil {
		t.Fatalf("Frames: %v", err)
	}
	if len(diags) != len(want) {
		t.Fatalf("got %d diagnostics, want %d:\n%s", len(diags), len(want), diags)
	}
	for i, msg := range want {
		if got := messageOf(diags[i]); got != msg {
			t.Errorf("diagnostic %d:\n got %s\nwant %s", i, got, msg)
		}
	}
}

// TestMeasureReadsThePrologueFromTheEntryBlockAlone pins what the
// prologue is: the one save every activation holds for its whole
// body, made at entry and nothing else. A push of ra further into
// the function is held only on the path that reaches it.
func TestMeasureReadsThePrologueFromTheEntryBlockAlone(t *testing.T) {
	// An activation holds nothing, so the array bounds no depth for it: the
	// count the report would otherwise state is what the save is counted into.
	const want = "warning: 'loop' can reach itself through a call, and how deep it goes is not decided at compile time; an activation of it holds none of the 512 slots left above the data region, so no activation count bounds it — bound the depth in the source, or rewrite the recursion as a loop"

	loop := stackFunc(t, "loop", false, frameCall{target: "loop"})
	loop.NewBlock("loop.saved", loop.Pos).Append(pushOf(t, ic10.RegRA))
	prog := &mir.Program{Funcs: []*mir.Func{
		stackFunc(t, "main", false, frameCall{target: "loop"}),
		loop,
	}}

	diags, err := Measure(t.Context(), prog, Options{})
	if err != nil {
		t.Fatalf("Frames: %v", err)
	}
	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want 1:\n%s", len(diags), diags)
	}
	if got := messageOf(diags[0]); got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
}

// TestMeasureCountsEveryLinkOpcode holds the frame walk to the whole
// of the link family rather than to jal: nineteen opcodes write ra
// and come back, and a walk naming only the one instruction
// selection emits would miss a program spelled with any of the other eighteen.
func TestMeasureCountsEveryLinkOpcode(t *testing.T) {
	// Each activation saves its return address and pushes three registers
	// around the call that continues the cycle, so the 511 slots below the
	// deepest activation hold 127 more of them.
	const want = "warning: 'down' can reach itself through a call, and how deep it goes is not decided at compile time; of the 512 slots left above the data region the calls into it hold 0 slots and its deepest activation holds 1 slot, and every activation above that one holds 4 slots, so there is room for 128 activations and the next one faults on a push — bound the depth in the source, or rewrite the recursion as a loop"

	cases := []struct {
		name string
		op   ic10.Opcode
	}{
		{name: "an unconditional call", op: isa.OpJal},
		{name: "a two operand conditional call", op: isa.OpBgezal},
		{name: "a three operand conditional call", op: isa.OpBltal},
		{name: "a four operand conditional call", op: isa.OpBapal},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog := &mir.Program{Funcs: []*mir.Func{
				stackFunc(t, "main", false, frameCall{target: "down", op: tc.op}),
				stackFunc(t, "down", true, frameCall{target: "down", saves: 3, op: tc.op}),
			}}
			diags, err := Measure(t.Context(), prog, Options{})
			if err != nil {
				t.Fatalf("Frames: %v", err)
			}
			if len(diags) != 1 {
				t.Fatalf("got %d diagnostics, want 1:\n%s", len(diags), diags)
			}
			if got := messageOf(diags[0]); got != want {
				t.Errorf("\n got %s\nwant %s", got, want)
			}
		})
	}
}

// TestMeasureCountsTheSavesAroundACall holds the frame walk to every
// save the caller made, whatever regalloc.Allocate placed between
// them and the call: reloads for the call's own spilled operands
// land after the saves, so stopping at the first non-push instruction would undercount.
func TestMeasureCountsTheSavesAroundACall(t *testing.T) {
	tests := []struct {
		name string
		// calls is what 'down' does, and the last of them is the one measured.
		calls []frameCall
		// want is the slots the calling activation holds while that call runs,
		// which is its saved return address plus its own saves.
		want int
	}{
		{
			name:  "the saves sit against the call",
			calls: []frameCall{{target: "down", saves: 3}},
			want:  4,
		},
		{
			name:  "reloads separate the saves from the call",
			calls: []frameCall{{target: "down", saves: 3, reloads: 2}},
			want:  4,
		},
		{
			name:  "a call reloading every operand it reads saves nothing",
			calls: []frameCall{{target: "down", reloads: 2}},
			want:  1,
		},
		{
			name:  "an earlier call's frame is not counted a second time",
			calls: []frameCall{{target: "down", saves: 2}, {target: "down", saves: 3, reloads: 1}},
			want:  4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prog := &mir.Program{Funcs: []*mir.Func{
				stackFunc(t, "main", false, frameCall{target: "down"}),
				stackFunc(t, "down", true, tt.calls...),
			}}
			g, err := newFrameGraph(prog)
			if err != nil {
				t.Fatalf("newFrameGraph: %v", err)
			}
			sites := g.funcs[1].sites
			if len(sites) != len(tt.calls) {
				t.Fatalf("the walk found %d calls in 'down', want %d", len(sites), len(tt.calls))
			}
			if got := sites[len(sites)-1].held; got != tt.want {
				t.Errorf("the call holds %d slots, want %d", got, tt.want)
			}
		})
	}
}

// TestMeasureCountsAFrameReloadsSeparateFromTheCall is the same fact stated
// through the reported count, which is what a reader of the compiler sees.
func TestMeasureCountsAFrameReloadsSeparateFromTheCall(t *testing.T) {
	const want = "warning: 'down' can reach itself through a call, and how deep it goes is not decided at compile time; of the 512 slots left above the data region the calls into it hold 0 slots and its deepest activation holds 1 slot, and every activation above that one holds 4 slots, so there is room for 128 activations and the next one faults on a push — bound the depth in the source, or rewrite the recursion as a loop"

	prog := &mir.Program{Funcs: []*mir.Func{
		stackFunc(t, "main", false, frameCall{target: "down"}),
		stackFunc(t, "down", true, frameCall{target: "down", saves: 3, reloads: 2, op: isa.OpBeqal}),
	}}
	diags, err := Measure(t.Context(), prog, Options{})
	if err != nil {
		t.Fatalf("Frames: %v", err)
	}
	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want 1:\n%s", len(diags), diags)
	}
	if got := messageOf(diags[0]); got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
}

// TestMeasureCountsAFrameAllocationEmitted is the same fact asked of
// the code regalloc.Allocate actually produces rather than a shape
// written by hand: instruction selection emits jal alone, so no
// reload lands before a call on the shipped path, and the disagreement is invisible there.
func TestMeasureCountsAFrameAllocationEmitted(t *testing.T) {
	down := recursionWithASpilledCall(t)
	prog := &mir.Program{Funcs: []*mir.Func{
		stackFunc(t, "main", false, frameCall{target: "down"}),
		down,
	}}
	if err := prog.Validate(); err != nil {
		t.Fatalf("Validate after allocation: %v", err)
	}

	saves, staged := 0, false
	for _, instr := range down.Blocks[0].Instrs {
		switch {
		case ic10.LinksReturn(instr.Op):
		case instr.Op == isa.OpPush && !isRegister(instr, ic10.RegRA):
			saves++
			continue
		case instr.Op == isa.OpGet:
			staged = saves > 0
			continue
		default:
			continue
		}
		break
	}
	if saves == 0 || !staged {
		t.Fatalf("allocation emitted %d saves and staged an operand between them and the call: %t; the case wants both:\n%s", saves, staged, renderedFunc(down))
	}

	g, err := newFrameGraph(prog)
	if err != nil {
		t.Fatalf("newFrameGraph: %v", err)
	}
	if want := saves + 1; g.funcs[1].sites[0].held != want {
		t.Errorf("the call holds %d slots, want %d: the saved return address and the %d registers pushed around it\n%s",
			g.funcs[1].sites[0].held, want, saves, renderedFunc(down))
	}
}

// recursionWithASpilledCall builds a self-recursive function whose
// call reads two values allocation sent to memory. The file is
// narrowed to two allocatable and two scratch registers, so those
// two are the cheapest to spill and the call reads them from memory.
func recursionWithASpilledCall(t *testing.T) *mir.Func {
	t.Helper()
	pos := source.Position{File: framesFile}
	fn := mir.NewFunc("down", pos)
	block := fn.NewBlock("down.entry", pos)
	block.AddSucc(block)
	block.Append(pushOf(t, ic10.RegRA))

	emit := func(op ic10.Opcode, args ...mir.Operand) {
		t.Helper()
		instr, err := mir.NewInstr(op, pos, args...)
		if err != nil {
			t.Fatalf("building a %v: %v", op, err)
		}
		block.Append(instr)
	}
	cold := [2]mir.VirtReg{fn.NewVirtReg(), fn.NewVirtReg()}
	for i, v := range cold {
		emit(isa.OpMove, v, mir.Imm{Value: float64(i)})
	}
	across := [2]mir.VirtReg{fn.NewVirtReg(), fn.NewVirtReg()}
	for i, v := range across {
		emit(isa.OpMove, v, mir.Imm{Value: float64(i)})
	}
	for range 4 {
		hot := fn.NewVirtReg()
		emit(isa.OpMove, hot, mir.Imm{Value: 1})
		emit(isa.OpAdd, hot, hot, hot)
		emit(isa.OpAdd, hot, hot, hot)
		emit(isa.OpS, mir.NewDeviceBase(), mir.LogicType{Value: 0}, hot)
	}
	emit(isa.OpBeqal, cold[0], cold[1], mir.Label{Name: "down.entry"})
	for _, v := range across {
		emit(isa.OpS, mir.NewDeviceBase(), mir.LogicType{Value: 0}, v)
	}

	cfg := regalloc.Config{Reserved: []ic10.Register{ic10.RegSP, ic10.RegRA}, Scratch: []ic10.Register{2, 3}}
	for r := ic10.Register(4); r < ic10.NumGeneralRegisters; r++ {
		cfg.Reserved = append(cfg.Reserved, r)
	}
	if _, err := regalloc.Allocate(fn, cfg); err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	return fn
}

func renderedFunc(fn *mir.Func) string {
	var lines []string
	for _, instr := range fn.AllInstrs() {
		lines = append(lines, instr.String())
	}
	return strings.Join(lines, "\n")
}

// frameCall is one call a synthetic function makes: the function it names, the
// caller-saved registers allocation would have pushed around it, and the link
// opcode it is spelled with.
type frameCall struct {
	target string
	saves  int
	// reloads is how many spilled operands the call reads, each of which
	// regalloc.Allocate stages through a scratch register with a get placed
	// between the saves and the call itself.
	reloads int
	// op is the opcode the call is made with, and jal where it is unset.
	// Opcode zero is l, which is not a call, so the zero value names no link
	// form of its own.
	op ic10.Opcode
}

// stackFunc builds one function of a synthetic program in the shape
// the backend produces: the return address save at the top for a
// function that calls, and each call bracketed by its own saves and
// the pops that mirror them.
func stackFunc(t *testing.T, name string, prologue bool, calls ...frameCall) *mir.Func {
	t.Helper()
	fn := mir.NewFunc(name, source.Position{File: framesFile})
	block := fn.NewBlock(name+".entry", fn.Pos)
	if prologue {
		block.Append(pushOf(t, ic10.RegRA))
	}
	for _, call := range calls {
		for i := range call.saves {
			block.Append(pushOf(t, ic10.Register(i)))
		}
		for i := range call.reloads {
			block.Append(reloadOf(t, ic10.Register(i), i))
		}
		op := call.op
		if op == 0 {
			op = isa.OpJal
		}
		info, known := op.Instruction()
		if !known {
			t.Fatalf("opcode %v is not in the instruction table", op)
		}
		args := make([]mir.Operand, len(info.Operands))
		for i := range args[:len(args)-1] {
			args[i] = mir.Imm{Value: 0}
		}
		args[len(args)-1] = mir.Label{Name: call.target + ".entry"}
		instr, err := mir.NewInstr(op, fn.Pos, args...)
		if err != nil {
			t.Fatalf("building the %s to %s: %v", info.Mnemonic, call.target, err)
		}
		block.Append(instr)
		for i := call.saves - 1; i >= 0; i-- {
			block.Append(popOf(t, ic10.Register(i)))
		}
	}
	return fn
}

// withVirtualCopy puts fn back into the form it had before register
// allocation, which the presence of one virtual register is enough to name.
func withVirtualCopy(t *testing.T, fn *mir.Func) *mir.Func {
	t.Helper()
	move, err := mir.NewInstr(isa.OpMove, fn.Pos, mir.VirtReg{ID: 0}, mir.VirtReg{ID: 1})
	if err != nil {
		t.Fatalf("building a copy over virtual registers: %v", err)
	}
	fn.Blocks[0].Append(move)
	return fn
}

func pushOf(t *testing.T, reg ic10.Register) *mir.Instr {
	t.Helper()
	instr, err := mir.NewInstr(isa.OpPush, source.Position{File: framesFile}, mir.PhysReg{Reg: reg})
	if err != nil {
		t.Fatalf("building a push of %s: %v", reg, err)
	}
	return instr
}

func popOf(t *testing.T, reg ic10.Register) *mir.Instr {
	t.Helper()
	instr, err := mir.NewInstr(isa.OpPop, source.Position{File: framesFile}, mir.PhysReg{Reg: reg})
	if err != nil {
		t.Fatalf("building a pop of %s: %v", reg, err)
	}
	return instr
}

func reloadOf(t *testing.T, reg ic10.Register, slot int) *mir.Instr {
	t.Helper()
	instr, err := mir.NewInstr(isa.OpGet, source.Position{File: framesFile}, mir.PhysReg{Reg: reg}, mir.NewDeviceBase(), mir.Imm{Value: float64(slot)})
	if err != nil {
		t.Fatalf("building a reload of %s from slot %d: %v", reg, slot, err)
	}
	return instr
}

// TestMeasureDepthMatchesTheMachine runs each program until it
// faults and holds the reported count to what the chip did: the
// arithmetic rests on facts no test of the analysis alone reaches,
// like sp starting where allocation put it and a push past the array faulting rather than wrapping.
func TestMeasureDepthMatchesTheMachine(t *testing.T) {
	cases := []struct {
		name string
		src  string
		// entries names the functions whose activations are counted, which is
		// every member of the cycle.
		entries []string
		// depth is the count the diagnostic states.
		depth int
		// floor says the count is a lower bound rather than the answer, which
		// is what a recursion reports when any of the three amounts it is
		// built from was the largest of differing choices; the machine is
		// then held to reaching at least that far.
		floor bool
	}{
		{name: "direct recursion", src: srcDirect, entries: []string{"down"}, depth: 512},
		{name: "a value live across the call", src: srcLiveAcrossCall, entries: []string{"sum"}, depth: 256},
		{name: "a caller holding a value across the call", src: srcCallerHolds, entries: []string{"down"}, depth: 511, floor: true},
		{name: "mutual recursion through two functions", src: srcMutualPair, entries: []string{"ping", "pong"}, depth: 512},
		{name: "mutual recursion through three functions", src: srcMutualTriple, entries: []string{"a", "b", "c"}, depth: 512},
		{name: "a cycle whose members hold different amounts", src: srcMutualUneven, entries: []string{"ping", "pong"}, depth: 256, floor: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			built := build(t, tc.src)
			diags, err := Measure(t.Context(), built.prog, Options{StackBase: built.base})
			if err != nil {
				t.Fatalf("Frames: %v", err)
			}
			if len(diags) != 1 {
				t.Fatalf("got %d diagnostics, want 1", len(diags))
			}
			stated := "room for " + source.Plural(tc.depth, "activation") + " and the next one faults"
			if tc.floor {
				stated = "room for at least " + source.Plural(tc.depth, "activation")
			}
			if !strings.Contains(diags[0].Msg, stated) {
				t.Fatalf("the diagnostic does not state %q:\n%s", stated, diags[0].Msg)
			}
			// The activation that faults is one past the ones that fit, and it
			// faults on its first push, so the machine reaches it and gets no
			// further.
			reached := deepestBeforeStackOverflow(t, built, tc.entries) - 1
			switch {
			case tc.floor && tc.depth > reached:
				t.Errorf("the diagnostic states at least %d activations and the machine held %d, so the bound is not one",
					tc.depth, reached)
			case !tc.floor && tc.depth != reached:
				t.Errorf("the diagnostic states %d activations and the machine held %d", tc.depth, reached)
			}
		})
	}
}

// program is one compiled MicroC source: what the analysis reads, the boundary
// allocation chose, and the assembly the chip runs.
type program struct {
	prog   *mir.Program
	base   int
	output emit.Output
}

// build runs the whole pipeline over src, in the order and with the
// configuration cmd/ic11c uses. The stages are duplicated rather than called
// because the command is not a library, and a frame is only final once
// allocation and the peephole have both run.
func build(t *testing.T, src string) program {
	t.Helper()
	lines := source.NewLineMap(src)

	file, diags, err := tsparse.Parse(framesFile, src)
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if diags.HasErrors() {
		t.Fatalf("parsing: %v", diags)
	}
	analyzed, diags, err := sema.Analyze(t.Context(), file, sema.Shipped{})
	if err != nil {
		t.Fatalf("analyzing: %v", err)
	}
	if diags.HasErrors() {
		t.Fatalf("analyzing: %v", diags)
	}
	module, err := irgen.Generate(t.Context(), analyzed, irgen.Options{ModuleName: framesFile})
	if err != nil {
		t.Fatalf("generating IR: %v", err)
	}
	t.Cleanup(module.Dispose)
	if err := llvmopt.Run(t.Context(), module.Module, llvmopt.Options{}); err != nil {
		t.Fatalf("optimizing: %v", err)
	}
	selected, err := isel.Select(t.Context(), module.Module, isel.Options{
		File:        framesFile,
		Lines:       lines,
		InlineSites: module.InlineSites,
	})
	if err != nil {
		t.Fatalf("selecting: %v", err)
	}

	cfg := regalloc.Config{Scratch: regalloc.DefaultScratch(), SpillSlotBase: selected.DataSlots}
	if selected.CallingConvention {
		cfg.Reserved = []ic10.Register{ic10.RegSP, ic10.RegRA}
	}
	for _, fn := range selected.Program.Funcs {
		if form := fn.RegForm(); form == mir.RegFormPhysical || form == mir.RegFormEmpty {
			continue
		}
		result, err := regalloc.Allocate(fn, cfg)
		if err != nil {
			t.Fatalf("allocating %s: %v", fn.Name, err)
		}
		cfg.SpillSlotBase += result.SpillSlots
	}
	if selected.CallingConvention {
		if err := regalloc.SetStackBase(selected.Program.Funcs[0], cfg.SpillSlotBase); err != nil {
			t.Fatalf("setting the stack base: %v", err)
		}
	}
	peephole.Run(selected.Program)
	if err := selected.Program.CheckPlacement(); err != nil {
		t.Fatalf("checking instruction placement: %v", err)
	}

	output, err := emit.Emit(selected.Program, emit.Options{Slots: emit.SlotReport{
		Data:   selected.DataSlots,
		Spill:  cfg.SpillSlotBase - selected.DataSlots,
		Frames: selected.CallingConvention,
	}})
	if err != nil {
		t.Fatalf("emitting: %v", err)
	}
	return program{prog: selected.Program, base: cfg.SpillSlotBase, output: output}
}

// maxSteps bounds a run: the longest of the programs below faults
// inside four thousand instructions, and the bound is four times that —
// far tighter than the array is large, since a step is a round trip to
// the chip and a bound of a million would take the better part of an hour.
const maxSteps = 1 << 14

// deepestBeforeStackOverflow executes the program one instruction at
// a time and returns the most activations of the named functions
// live at once before the array ran out. Live rather than entered:
// shallow calls into the recursion are not part of the chain the deep one built.
func deepestBeforeStackOverflow(t *testing.T, built program, entries []string) int {
	t.Helper()
	ctx, harness := chiptest.Harness(t)
	if err := harness.Load(ctx, built.output.Text); err != nil {
		t.Fatalf("loading the emitted assembly:\n%s\n%v", built.output.Text, err)
	}

	enter := make(map[int]bool, len(entries))
	leave := make(map[int]bool, len(entries))
	for _, name := range entries {
		found := false
		for _, fn := range built.output.Report.Functions {
			if fn.Name != name {
				continue
			}
			enter[fn.FirstLine] = true
			leave[fn.FirstLine+fn.Lines-1] = true
			found = true
		}
		if !found {
			t.Fatalf("the emitted program has no function named %s", name)
		}
	}

	line, live, deepest := 0, 0, 0
	for range maxSteps {
		switch {
		case enter[line]:
			live++
			deepest = max(deepest, live)
		case leave[line]:
			live--
		}
		got, err := harness.Step(ctx, 1)
		if err != nil {
			t.Fatalf("stepping the emitted assembly at line %d: %v\n%s", line, err, built.output.Text)
		}
		switch got.Stop {
		case chip.StopFaulted:
			if got.Fault.Type != chip.ExcStackOverFlow {
				t.Fatalf("the chip faulted with %s, want a stack overflow", got.Fault)
			}
			if sp := got.Registers[ic10.RegSP]; sp != float64(ic10.NumMemorySlots) {
				t.Errorf("sp is %v at the overflow, want %d: the fault is not the push that reached the end of the array",
					sp, ic10.NumMemorySlots)
			}
			return deepest
		case chip.StopEnded:
			t.Fatalf("the program ran to completion without exhausting the array:\n%s", built.output.Text)
		case chip.StopCompileError:
			t.Fatalf("the chip refused the emitted assembly: %s\n%s", got.CompileError, built.output.Text)
		case chip.StopTickBudget:
			t.Fatalf("a segment ended %q, which is an ending only a whole run has", got.Stop)
		case chip.StopSuspended, chip.StopBudget:
			// The program is still inside itself, which is where a segment of
			// one instruction leaves it for every instruction but the last.
		}
		line = got.Address
	}
	t.Fatalf("the program did not fault within %d instructions:\n%s", maxSteps, built.output.Text)
	return 0
}

// messageOf renders a diagnostic without its position, which the cases check
// separately and which carries an absolute path on no machine but this one.
func messageOf(diag source.Diagnostic) string {
	if diag.Severity == source.Error {
		return diag.Msg
	}
	return diag.Severity.String() + ": " + diag.Msg
}

// TestMeasureReportsACallChainThatOverflows covers the depth of a
// non-recursive program, a static number nothing used to be held
// against: the shipped inliner leaves only recursive functions out
// of line, so this needs call site out-lining turned on to be reachable.
func TestMeasureReportsACallChainThatOverflows(t *testing.T) {
	// Each link saves its return address and pushes one register around its
	// call, so a link costs two slots and the last one costs its return address
	// alone. main holds nothing of its own.
	chain := func(links int) []*mir.Func {
		funcs := []*mir.Func{stackFunc(t, "main", false, frameCall{target: "f0"})}
		for i := range links {
			name := "f" + strconv.Itoa(i)
			if i == links-1 {
				funcs = append(funcs, stackFunc(t, name, true))
				break
			}
			funcs = append(funcs, stackFunc(t, name, true, frameCall{target: "f" + strconv.Itoa(i+1), saves: 1}))
		}
		return funcs
	}

	cases := []struct {
		name  string
		links int
		left  int
		want  string
	}{
		{
			name:  "a chain that fits exactly",
			links: 3,
			// Two links at two slots each and a third holding only its return
			// address.
			left: 5,
		},
		{
			name:  "a chain one slot too deep",
			links: 3,
			left:  4,
			want:  "the calls nest deep enough to hold 5 slots at once and only 4 slots are left above the data region, so the chain main to f0 to f1 to f2 faults on a push before it returns — shorten an array, drop a global, or hold fewer values across the calls on that chain",
		},
		{
			name:  "a single call with nowhere to put its return address",
			links: 1,
			left:  0,
			want:  "the calls nest deep enough to hold 1 slot at once and only 0 slots are left above the data region, so the chain main to f0 faults on a push before it returns — shorten an array, drop a global, or hold fewer values across the calls on that chain",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog := &mir.Program{Funcs: chain(tc.links)}
			diags, err := Measure(t.Context(), prog, Options{StackBase: ic10.NumMemorySlots - tc.left})
			if err != nil {
				t.Fatalf("Frames: %v", err)
			}
			if tc.want == "" {
				if len(diags) != 0 {
					t.Fatalf("got %d diagnostics for a chain that fits:\n%s", len(diags), diags)
				}
				return
			}
			if len(diags) != 1 {
				t.Fatalf("got %d diagnostics, want 1:\n%s", len(diags), diags)
			}
			if got := messageOf(diags[0]); got != tc.want {
				t.Errorf("\n got %s\nwant %s", got, tc.want)
			}
		})
	}
}

// declaredAt puts a synthetic function on a line, which is what a case checks
// the order of the diagnostics against. [stackFunc] leaves every function on
// the file alone, where every position compares equal and any order reads as
// the right one.
func declaredAt(fn *mir.Func, line int) *mir.Func {
	fn.Pos = source.Position{File: framesFile, Line: line}
	return fn
}

// TestMeasureCoversChainsBesideARecursion covers the depth of a
// chain that a recursion elsewhere in the program used to hide: a
// chain leaving the entry point in another direction holds what it
// holds regardless of how deep the recursion goes.
func TestMeasureCoversChainsBesideARecursion(t *testing.T) {
	cases := []struct {
		name  string
		funcs []*mir.Func
		left  int
		// want is every diagnostic in the order they come back, which is the
		// order the lines the functions were declared on put them in.
		want []string
	}{
		{
			// Each link of the chain saves its return address and pushes one
			// register around its call, so the three links hold five slots
			// between them while the recursion beside them holds one.
			name: "a chain the entry point reaches beside a recursion",
			funcs: []*mir.Func{
				declaredAt(stackFunc(t, "main", false, frameCall{target: "loop"}, frameCall{target: "f0"}), 1),
				declaredAt(stackFunc(t, "loop", true, frameCall{target: "loop"}), 20),
				declaredAt(stackFunc(t, "f0", true, frameCall{target: "f1", saves: 1}), 30),
				declaredAt(stackFunc(t, "f1", true, frameCall{target: "f2", saves: 1}), 40),
				declaredAt(stackFunc(t, "f2", true), 50),
			},
			left: 4,
			want: []string{
				"the calls nest deep enough to hold 5 slots at once and only 4 slots are left above the data region, so the chain main to f0 to f1 to f2 faults on a push before it returns — shorten an array, drop a global, or hold fewer values across the calls on that chain",
				"warning: 'loop' can reach itself through a call, and how deep it goes is not decided at compile time; of the 4 slots left above the data region the calls into it hold 0 slots and its deepest activation holds 1 slot, and every activation above that one holds 1 slot, so there is room for 4 activations and the next one faults on a push — bound the depth in the source, or rewrite the recursion as a loop",
			},
		},
		{
			// The deepest activation is a leaf, keeping its return address in
			// the register and holding nothing, so the chain through it reaches
			// exactly as far as the caller stopping one short of it. Naming
			// the shorter one would point at the wrong function.
			name: "a chain whose deepest activation holds nothing of its own",
			funcs: []*mir.Func{
				stackFunc(t, "main", false, frameCall{target: "f0"}),
				stackFunc(t, "f0", true, frameCall{target: "f1"}),
				stackFunc(t, "f1", false),
			},
			left: 0,
			want: []string{
				"the calls nest deep enough to hold 1 slot at once and only 0 slots are left above the data region, so the chain main to f0 to f1 faults on a push before it returns — shorten an array, drop a global, or hold fewer values across the calls on that chain",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog := &mir.Program{Funcs: tc.funcs}
			diags, err := Measure(t.Context(), prog, Options{StackBase: ic10.NumMemorySlots - tc.left})
			if err != nil {
				t.Fatalf("Frames: %v", err)
			}
			if len(diags) != len(tc.want) {
				t.Fatalf("got %d diagnostics, want %d:\n%s", len(diags), len(tc.want), diags)
			}
			for i, want := range tc.want {
				if got := messageOf(diags[i]); got != want {
					t.Errorf("diagnostic %d:\n got %s\nwant %s", i, got, want)
				}
			}
		})
	}
}

// TestMeasureNamesTheChainTheDepthWasMeasuredAlong covers the
// callees the walk down to the deepest activation steps over: a
// callee with no measurement of its own reads as holding nothing,
// which could send the walk into a component the chain never took.
func TestMeasureNamesTheChainTheDepthWasMeasuredAlong(t *testing.T) {
	cases := []struct {
		name  string
		funcs []*mir.Func
		left  int
		want  []string
	}{
		{
			// The call into the recursion holds exactly what the chain beside it
			// reaches, so a recursion read as holding nothing attains the same
			// maximum as the chain that produced the number.
			name: "a recursion that reaches nothing outside itself",
			funcs: []*mir.Func{
				declaredAt(stackFunc(t, "main", false, frameCall{target: "r", saves: 1}, frameCall{target: "f0"}), 1),
				declaredAt(stackFunc(t, "r", true, frameCall{target: "r"}), 10),
				declaredAt(stackFunc(t, "f0", true), 20),
			},
			left: 0,
			want: []string{
				"the calls nest deep enough to hold 1 slot at once and only 0 slots are left above the data region, so the chain main to f0 faults on a push before it returns — shorten an array, drop a global, or hold fewer values across the calls on that chain",
				"'r' can reach itself through a call, and the data region leaves no room for even one activation: 0 slots remain above it, the call into it already holds 1 slot, and an activation holds 1 slot more — shorten an array, drop a global, or rewrite the recursion as a loop",
			},
		},
		{
			// The recursion calls out to a chain of its own, so measuring it
			// like an ordinary function gives it an amount rather than none.
			// Both walks have to leave a recursive component out, not just
			// the one that reads the answer.
			name: "a recursion that calls a chain of its own",
			funcs: []*mir.Func{
				declaredAt(stackFunc(t, "main", false, frameCall{target: "r", saves: 3}, frameCall{target: "f0", saves: 4}), 1),
				declaredAt(stackFunc(t, "r", true, frameCall{target: "r"}, frameCall{target: "t"}), 10),
				declaredAt(stackFunc(t, "f0", true), 20),
				declaredAt(stackFunc(t, "t", true), 30),
			},
			left: 4,
			want: []string{
				"the calls nest deep enough to hold 5 slots at once and only 4 slots are left above the data region, so the chain main to f0 faults on a push before it returns — shorten an array, drop a global, or hold fewer values across the calls on that chain",
				"'r' can reach itself through a call, and the data region leaves no room for even one activation: 4 slots remain above it, the call into it already holds 3 slots, and an activation holds 2 slots more — shorten an array, drop a global, or rewrite the recursion as a loop",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog := &mir.Program{Funcs: tc.funcs}
			diags, err := Measure(t.Context(), prog, Options{StackBase: ic10.NumMemorySlots - tc.left})
			if err != nil {
				t.Fatalf("Frames: %v", err)
			}
			if len(diags) != len(tc.want) {
				t.Fatalf("got %d diagnostics, want %d:\n%s", len(diags), len(tc.want), diags)
			}
			for i, want := range tc.want {
				if got := messageOf(diags[i]); got != want {
					t.Errorf("diagnostic %d:\n got %s\nwant %s", i, got, want)
				}
			}
		})
	}
}

// TestMeasureLeavesTheDepthToTheRecursionReport covers the overlap between the
// two: a program whose chain reaches a recursion has no static depth, and
// stating one would be a number that ignores the recursion in the middle of it.
func TestMeasureLeavesTheDepthToTheRecursionReport(t *testing.T) {
	prog := &mir.Program{Funcs: []*mir.Func{
		stackFunc(t, "main", false, frameCall{target: "middle", saves: 2}),
		stackFunc(t, "middle", true, frameCall{target: "loop"}),
		stackFunc(t, "loop", true, frameCall{target: "loop"}),
	}}
	diags, err := Measure(t.Context(), prog, Options{StackBase: ic10.NumMemorySlots - 2})
	if err != nil {
		t.Fatalf("Frames: %v", err)
	}
	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want the recursion's alone:\n%s", len(diags), diags)
	}
	// The whole message rather than the absence of the other one's wording: an
	// assertion that a phrase belonging to a different message is missing goes
	// vacuously true the moment that message is reworded.
	want := "'loop' can reach itself through a call, and the data region leaves no room for even one activation: 2 slots remain above it, the call into it already holds 3 slots, and an activation holds 1 slot more — shorten an array, drop a global, or rewrite the recursion as a loop"
	if got := messageOf(diags[0]); got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
}

// TestStackMoveOfCoversEveryStackPointerMover pins the set
// [stackMoveOf] names against the one the instruction table states:
// a mover stepped over is a slot missing from a frame, over-reporting
// the room since the slots left are divided by an activation measured short.
func TestStackMoveOfCoversEveryStackPointerMover(t *testing.T) {
	want := map[ic10.Opcode]stackMove{
		isa.OpPush: stackGrows,
		isa.OpPop:  stackShrinks,
	}

	movers := make(map[ic10.Opcode]bool)
	for _, info := range ic10.Instructions {
		if info.WritesImplicitly(ic10.RegSP) {
			movers[info.Opcode] = true
		}
	}
	if len(movers) != len(want) {
		t.Errorf("the instruction table names %d instructions that move sp, want %d: %v", len(movers), len(want), movers)
	}
	for op := range want {
		if !movers[op] {
			t.Errorf("%s no longer writes sp implicitly in the instruction table", op)
		}
	}
	for op := range movers {
		if _, named := want[op]; !named {
			t.Errorf("%s moves sp and no case here says which way", op)
		}
	}

	names := [...]string{stackSame: "unmoved", stackGrows: "grown", stackShrinks: "shrunk"}
	for _, info := range ic10.Instructions {
		got, err := stackMoveOf(info.Opcode)
		if err != nil {
			t.Errorf("stackMoveOf(%s): %v", info.Mnemonic, err)
			continue
		}
		expected, moves := want[info.Opcode]
		if !moves {
			expected = stackSame
		}
		if got != expected {
			t.Errorf("stackMoveOf(%s) leaves sp %s, want %s", info.Mnemonic, names[got], names[expected])
		}
	}
}
