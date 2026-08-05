package main

import (
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
)

// Sources the calling convention cases share. Each recursive function is
// written so that the optimizer's tail recursion elimination either can or
// cannot flatten it, which is what decides whether the call is a leaf.
const (
	// gcdSrc recurses in tail position, so the optimizer turns the body into a
	// loop and the call reaches a leaf.
	gcdSrc = `long long gcd(long long a, long long b) {
    if (b == 0) { return a; }
    return gcd(b, a % b);
}
void main(void) {
    long long n = (long long)__ic_load(d0, Setting);
    __ic_store(d1, Setting, gcd(n, 12));
}`

	// fibSrc recurses twice, so one call stays in the body and the function has
	// to keep its own return address across it.
	fibSrc = `long long fib(long long n) {
    if (n < 2) { return n; }
    return fib(n - 1) + fib(n - 2);
}
void main(void) {
    long long n = (long long)__ic_load(d0, Setting);
    __ic_store(d1, Setting, fib(n));
}`

	// paritySrc is mutual recursion: neither function reaches itself directly.
	// blendSrc holds more live values across its call than the register file
	// has room for; they are floating point, so nothing may reassociate and
	// fold the sum away before the call, and each value is still wanted after it.
	blendSrc = `double blend(double x, long long n) {
    double a = x + 1.0;
    double b = x * 2.0;
    double c = x - 3.0;
    double d = x / 4.0;
    double e = x + 5.0;
    double f = x * 6.0;
    double g = x - 7.0;
    double h = x / 8.0;
    double i = x + 9.0;
    double j = x * 10.0;
    double k = x - 11.0;
    double l = x / 12.0;
    double m = x + 13.0;
    double o = x * 14.0;
    double p = x - 15.0;
    double tail = 0.0;
    if (n > 0) {
        tail = ` + blendTail + `;
    }
    return a + b + c + d + e + f + g + h + i + j + k + l + m + o + p + tail;
}
void main(void) {
    __ic_store(d1, Setting, blend(__ic_load(d0, Setting), 3));
}`

	// blendTail is the one expression that decides whether blend reaches
	// itself, and is what the accepted case replaces.
	blendTail = `blend(x + 1.0, n - 1)`

	paritySrc = `bool isOdd(long long n);
bool isEven(long long n) {
    if (n == 0) { return true; }
    return isOdd(n - 1);
}
bool isOdd(long long n) {
    if (n == 0) { return false; }
    return isEven(n - 1);
}
void main(void) {
    long long n = (long long)__ic_load(d0, Setting);
    __ic_store(d1, Setting, isEven(n));
}`
)

// TestCompileCallShape pins what a real call costs, since the byte budget is
// what the inlining default is chosen against and a save that is not needed is
// two lines on every activation.
func TestCompileCallShape(t *testing.T) {
	cases := []struct {
		name string
		src  string
		// want and absent are mnemonics with a trailing space, so that "j "
		// does not match "jal".
		want   []string
		absent []string
	}{
		{
			name:   "a leaf call is the jal and the return",
			src:    gcdSrc,
			want:   []string{"jal ", "j ra"},
			absent: []string{"push ra", "pop ra"},
		},
		{
			name: "a non-leaf call saves the one return address around itself",
			src:  fibSrc,
			want: []string{"push ra", "pop ra", "jal ", "j ra"},
		},
		{
			name: "mutual recursion makes both functions non-leaf",
			src:  paritySrc,
			want: []string{"push ra", "pop ra"},
		},
		{
			name: "a call passes its arguments in order from r0",
			src: `long long walk(long long a, long long b, long long c) {
    if (c <= 0) { return a + b; }
    return walk(b, a + c, c - 1) + a;
}
void main(void) {
    __ic_store(d1, Setting, walk(1, 2, (long long)__ic_load(d0, Setting)));
}`,
			want: []string{"move r0 1", "move r1 2", "jal "},
		},
		{
			name: "a non-recursive function is inlined, so no call survives",
			src: `long long square(long long x) { return x * x; }
long long cube(long long x) { return square(x) * x; }
void main(void) {
    long long n = (long long)__ic_load(d0, Setting);
    __ic_store(d1, Setting, square(n) + cube(n) + square(n + 1));
}`,
			absent: []string{"jal ", "j ra", "move sp "},
		},
		{
			name:   "the entry point sets sp before anything pushes",
			src:    fibSrc,
			want:   []string{"move sp "},
			absent: []string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assembly := compiled(t, "call.c", tc.src)
			for _, want := range tc.want {
				if !strings.Contains(assembly, want) {
					t.Errorf("the assembly has no %q:\n%s", want, assembly)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(assembly, absent) {
					t.Errorf("the assembly still has %q:\n%s", absent, assembly)
				}
			}
		})
	}
}

// thresholdSrc names one helper from two sites and a second, taking a dev,
// from two more. Nothing recurses, so the shipped rule inlines every call.
const thresholdSrc = `const dev intake = d0;
const dev exhaust = d1;

long long twice(long long n) { return n + n; }
void publish(dev target, long long v) { __ic_store(target, Setting, v); }

void main(void) {
    long long n = (long long)__ic_load(intake, Setting);
    publish(intake, twice(n));
    publish(exhaust, twice(n + 1));
}`

// TestCompileCallSiteThresholdOutlines pins what
// [irgen.Options.OutOfLineCallSites] does, recomputed against compiler.md's
// inlining figures. The threshold reaches no command-line flag, so this is
// the only thing holding it to the rule the document describes.
func TestCompileCallSiteThresholdOutlines(t *testing.T) {
	cases := []struct {
		name  string
		sites int
		// outlined reports whether a real call should reach the assembly.
		outlined bool
	}{
		{name: "the shipped rule inlines a two-site function", sites: 0},
		{name: "a two-site threshold gives it a shared body", sites: 2, outlined: true},
		{name: "a three-site threshold no longer reaches it", sites: 3},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const path = "threshold.c"
			output, diags, err := compile(t.Context(), path, thresholdSrc, options{outOfLineCallSites: tc.sites})
			if err != nil {
				t.Fatalf("compiling at a %d-site threshold: %v", tc.sites, err)
			}
			if diags.HasErrors() {
				t.Fatalf("compiling at a %d-site threshold: %v", tc.sites, diags)
			}
			if got := strings.Contains(output.Text, "jal "); got != tc.outlined {
				t.Errorf("a real call reaching the assembly is %v, want %v:\n%s", got, tc.outlined, output.Text)
			}
		})
	}
}

// TestCompileStackPointerLeadsTheProgram checks the one ordering the frame
// region depends on: nothing may push before sp is out of the data region.
// Chip state survives power loss, so sp arrives holding whatever the last
// program left in it.
func TestCompileStackPointerLeadsTheProgram(t *testing.T) {
	assembly := compiled(t, "order.c", `long long table[4] = {1, 2, 3, 4};
`+fibSrc)

	lines := assemblyLines(t, assembly)
	setter, pusher := -1, -1
	for i, line := range lines {
		if strings.HasPrefix(line, "move sp ") && setter < 0 {
			setter = i
		}
		if strings.HasPrefix(line, "push ") && pusher < 0 {
			pusher = i
		}
	}
	if setter < 0 {
		t.Fatalf("nothing sets sp in a program that pushes:\n%s", assembly)
	}
	if pusher >= 0 && pusher < setter {
		t.Errorf("line %d pushes before line %d sets sp:\n%s", pusher, setter, assembly)
	}
	if setter != 0 {
		t.Errorf("sp is set on line %d rather than first:\n%s", setter, assembly)
	}
}

// TestCompileRecursionRuns executes the convention rather than reading it: a
// frame that assembles and loses a return address on the second nested call
// leaves no trace in the instruction text, so each case recurses deep enough
// to save and restore repeatedly, checked against an answer computed here.
func TestCompileRecursionRuns(t *testing.T) {
	cases := []struct {
		name  string
		src   string
		input int64
		want  float64
	}{
		{"tail recursion, flattened into a loop", gcdSrc, 30, 6},
		{"nested recursion twelve deep", fibSrc, 12, 144},
		{"nested recursion at the base case", fibSrc, 0, 0},
		{"nested recursion one deep", fibSrc, 2, 1},
		{"mutual recursion twenty deep", paritySrc, 20, 1},
		{"mutual recursion, odd", paritySrc, 21, 0},
		{
			name: "a call inside a larger expression",
			src: `long long fib(long long n) {
    if (n < 2) { return n; }
    return fib(n - 1) + fib(n - 2);
}
void main(void) {
    long long n = (long long)__ic_load(d0, Setting);
    __ic_store(d1, Setting, fib(n) * 2 + fib(n - 1));
}`,
			input: 10,
			want:  144,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assembly := compiled(t, "recurse.c", tc.src)
			out, base, final := runWithStack(t, assembly, tc.input)
			if got := logicValue(t, out, "Setting"); got != tc.want {
				t.Errorf("the program answered %v, want %v:\n%s", got, tc.want, assembly)
			}
			// A frame the callee did not unwind is invisible until the region
			// grows down into the data region, which nothing traps.
			if final != base {
				t.Errorf("sp came back as %v, want the %v the prologue set:\n%s", final, base, assembly)
			}
		})
	}
}

// TestCompileRecursionThroughAPointerParameter covers the shape microc.md
// admits and the verifier has to reach through: a recursive function taking a
// pointer, where the object is named at the call from outside and the recursive
// call only steps the parameter along.
func TestCompileRecursionThroughAPointerParameter(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want float64
	}{
		{
			name: "a global array walked by a stepped pointer",
			src: `long long table[4] = {1, 2, 3, 4};
long long total(long long *p, long long n) {
    if (n <= 0) { return 0; }
    return *p + total(p + 1, n - 1);
}
void main(void) {
    __ic_store(d1, Setting, (double)total(table, 4));
}`,
			want: 10,
		},
		{
			name: "a pointer parameter written through",
			src: `long long cell;
void fill(long long *p, long long n) {
    if (n <= 0) { return; }
    *p = *p + n;
    fill(p, n - 1);
}
void main(void) {
    cell = 0;
    fill(&cell, 3);
    __ic_store(d1, Setting, (double)cell);
}`,
			want: 6,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assembly := compiled(t, "pointerparam.c", tc.src)
			out, _, _ := runWithStack(t, assembly, 0)
			if got := logicValue(t, out, "Setting"); got != tc.want {
				t.Errorf("the program answered %v, want %v:\n%s", got, tc.want, assembly)
			}
		})
	}
}

// TestCompileRecursionOutgrowingTheRegisterFile witnesses the refusal a
// recursion meets when its live values do not fit in the registers: a spill
// slot is one fixed address, so the inner activation would overwrite what the
// outer one left there. Held beside the same program with recursion removed.
func TestCompileRecursionOutgrowingTheRegisterFile(t *testing.T) {
	for _, tt := range []struct {
		name string
		src  string
		// refusal is what the diagnostic has to carry, and empty is a program
		// that compiles.
		refusal string
	}{
		{
			name:    "values enough to spill, held across a call into itself",
			src:     blendSrc,
			refusal: "holds more live values than the register file has room for",
		},
		{
			name: "the same values where the call does not come back round",
			src:  strings.Replace(blendSrc, blendTail, "x + 1.0", 1),
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, err := run(t, write(t, "blend.c", tt.src))
			if tt.refusal == "" {
				if err != nil {
					t.Fatalf("the same values outside a recursion no longer compile:\n%s", stderr)
				}
				checkAssembly(t, strings.TrimSuffix(stdout, "\n"))
				return
			}
			if err == nil {
				t.Fatalf("the program compiled, and every activation would share the one spill slot:\n%s", stdout)
			}
			if !strings.Contains(stderr, tt.refusal) {
				t.Errorf("the compiler refused it for something else:\n%s", stderr)
			}
		})
	}
}

// TestCompileFrameDepthGrowsWithRecursion checks that the frames are real: a
// deeper recursion has to reach further up the array, or the saves are landing
// on top of each other.
func TestCompileFrameDepthGrowsWithRecursion(t *testing.T) {
	assembly := compiled(t, "depth.c", fibSrc)

	depths := make(map[int64]int)
	for _, input := range []int64{2, 6, 12} {
		_, base, _ := runWithStack(t, assembly, input)
		depths[input] = highWater(t, assembly, input, int(base))
	}
	t.Logf("fib frame slots by input: %v", depths)
	if depths[6] <= depths[2] || depths[12] <= depths[6] {
		t.Errorf("frame depth did not grow with the recursion: %v", depths)
	}
}

// highWater runs the program one instruction at a time and reports the furthest
// sp reached past base, which is the frame slots one run consumed.
func highWater(t *testing.T, assembly string, input int64, base int) int {
	t.Helper()
	housing, _ := loadChip(t, assembly, input)
	deepest := 0
	for step := 0; housing.running(); step++ {
		if step > maxChipSteps {
			t.Fatalf("the program never left its own instructions:\n%s", assembly)
		}
		housing.step(t, 1)
		housing.faulted(t)
		if used := int(housing.register(ic10.RegSP)) - base; used > deepest {
			deepest = used
		}
	}
	return deepest
}

// maxChipSteps bounds a run. It is far past what any case here needs; the
// bound is what turns a lowering that loops into a failure rather than a hang.
const maxChipSteps = 200000

// spSeed is the value sp is given before a run, standing in for whatever the
// last program to hold the chip left there. It is nowhere near any boundary the
// prologue would choose, so a program that fails to set sp is visible rather
// than accidentally correct.
const spSeed = 401

// runWithStack runs assembly with one input on d0 and reports the device it
// wrote to on d1, the stack base its prologue chose, and the stack pointer it
// finished with.
func runWithStack(t *testing.T, assembly string, input int64) (out *device, base, final float64) {
	t.Helper()
	housing, out := loadChip(t, assembly, input)

	housing.step(t, 1)
	housing.faulted(t)
	base = housing.register(ic10.RegSP)
	if base == spSeed {
		t.Fatalf("the first line left sp at the value the chip powered up with:\n%s", assembly)
	}

	runToEnd(t, housing, assembly)
	return out, base, housing.register(ic10.RegSP)
}

// loadChip puts assembly on a fresh chip with a device to read from on d0 and
// one to write to on d1, and seeds sp so that a program relying on it already
// holding anything in particular fails. Loading resets sp, so the seed goes in
// after it.
func loadChip(t *testing.T, assembly string, input int64) (*chipRun, *device) {
	t.Helper()
	housing, in, out := devicePair(t)
	setLogic(t, in, "Setting", float64(input))

	housedChip(t, assembly, housing)
	housing.setRegister(t, ic10.RegSP, spSeed)
	return housing, out
}
