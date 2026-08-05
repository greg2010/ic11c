package irgen

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/sema"
	"github.com/greg2010/ic11c/internal/source"
	"github.com/greg2010/ic11c/internal/tsparse"
	"tinygo.org/x/go-llvm"
)

// analyze runs the front end, failing the test for source the fixtures were
// supposed to have written correctly.
func analyze(t *testing.T, src string) *sema.Program {
	t.Helper()
	file, diags, err := tsparse.Parse("test.c", src)
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if len(diags) > 0 {
		t.Fatalf("parsing:\n%s", diags.String())
	}
	prog, diags, err := sema.Analyze(t.Context(), file, sema.Shipped{})
	if err != nil {
		t.Fatalf("analyzing: %v", err)
	}
	if len(diags) > 0 {
		t.Fatalf("analyzing:\n%s", diags.String())
	}
	return prog
}

// generate lowers src and returns the module text, disposing the LLVM state
// before it returns so no case has to.
func generate(t *testing.T, src string) string {
	t.Helper()
	result, err := Generate(t.Context(), analyze(t, src), Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	defer result.Dispose()
	return result.Module.String()
}

// lower is [generate] for a case that walks the module rather than reading its
// text. The LLVM state outlives the call, so it is released on cleanup instead.
func lower(t *testing.T, src string) llvm.Module {
	t.Helper()
	result, err := Generate(t.Context(), analyze(t, src), Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	t.Cleanup(result.Dispose)
	return result.Module
}

// TestGenerateRecordsInlineSites covers the half of per-inline-site attribution
// this stage owns. Splicing a body in is what makes a function called from two
// places cost twice, and nothing downstream can tell the two expansions apart
// unless each carries the call it came through.
func TestGenerateRecordsInlineSites(t *testing.T) {
	const src = `long long inner(long long x) { return x + 1; }
long long outer(long long x) { return inner(x) * 2; }
void main(void) {
    long long n = (long long)__ic_load(d0, Setting);
    __ic_store(d1, Setting, outer(n) + outer(n + 1));
}`

	result, err := Generate(t.Context(), analyze(t, src), Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	defer result.Dispose()

	want := map[source.LineCol]string{
		{Line: 5, Column: 29}: "outer",
		{Line: 5, Column: 40}: "outer",
		{Line: 2, Column: 39}: "inner",
	}
	for at, callee := range want {
		if got := result.InlineSites[at]; got != callee {
			t.Errorf("InlineSites[%+v] = %q, want %q; recorded %v", at, got, callee, result.InlineSites)
		}
	}

	text := result.Module.String()
	for _, site := range []string{"line: 5, column: 29", "line: 5, column: 40", "line: 2, column: 39"} {
		if !strings.Contains(text, site) {
			t.Errorf("the module holds no debug location at %q:\n%s", site, text)
		}
	}
	if !strings.Contains(text, "inlinedAt:") {
		t.Errorf("no debug location names a call site:\n%s", text)
	}
}

func TestGenerateShapes(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
		// absent are substrings whose presence would mean the shape is wrong,
		// not merely different.
		absent []string
	}{
		{
			name:   "integer arithmetic is the machine's own double arithmetic",
			src:    "void main(void) { long long a = 3; long long b = a + 1; b = b - a; b = b * a; }",
			want:   []string{"fadd double", "fsub double", "fmul double"},
			absent: []string{"add nsw i64", "sub nsw i64", "mul nsw i64"},
		},
		{
			// The remainder is the identity a - trunc(a/b)*b, which is C's answer
			// for a negative divisor where the machine's mod is not.
			name:   "integer division truncates and the remainder is built out of it",
			src:    "void main(void) { long long a = 7; long long b = a / 2; long long c = a % 2; }",
			want:   []string{"fdiv double", "call double @llvm.trunc.f64", "fmul double", "fsub double"},
			absent: []string{"sdiv", "srem"},
		},
		{
			// A shift is a call the optimizer may not speculate, because the
			// machine's own shift can refuse an operand. The right shift is
			// arithmetic because MicroC's long long is signed.
			name:   "shifts are calls the optimizer may not move, and are arithmetic on the right",
			src:    "long long g; void main(void) { long long b = g << 2; long long c = g >> 2; }",
			want:   []string{"call double @__ic_shl", "call double @__ic_shr"},
			absent: []string{"shl i64", "ashr i64", "lshr i64", "speculatable"},
		},
		{
			name:   "bitwise not is the machine's own one-operand instruction",
			src:    "long long g; void main(void) { long long b = ~g; }",
			want:   []string{"call double @__ic_not"},
			absent: []string{"xor i64", "speculatable"},
		},
		{
			name:   "a bitwise operator over what the program wrote folds to the constant",
			src:    "long long g; void main(void) { g = (7 & 3) | ~4; }",
			want:   []string{"store double -5"},
			absent: []string{"__ic_and", "__ic_or", "__ic_not"},
		},
		{
			name: "locals become allocas",
			src:  "void main(void) { long long a = 1; long long b = a; }",
			want: []string{"alloca double", "store double", "load double"},
		},
		{
			name: "if and else produce a conditional branch and a merge",
			src:  "long long g; void main(void) { if (g > 0) { g = 1; } else { g = 2; } }",
			want: []string{"fcmp ogt double", "br i1", "if.then:", "if.else:", "if.end:"},
		},
		{
			name: "while loops back to its own test",
			src:  "long long g; void main(void) { while (g > 0) { g = g - 1; } }",
			want: []string{"while.cond:", "while.body:", "while.end:"},
		},
		{
			name: "do tests after the body",
			src:  "long long g; void main(void) { do { g = g - 1; } while (g > 0); }",
			want: []string{"do.body:", "do.cond:", "do.end:"},
		},
		{
			name: "for has a separate post block so continue reaches it",
			src:  "long long g; void main(void) { for (long long i = 0; i < 4; i++) { if (i == 2) { continue; } g = g + i; } }",
			want: []string{"for.cond:", "for.body:", "for.post:", "for.end:"},
		},
		{
			// An LLVM switch takes an integer tag, and stating the value as one
			// is what licenses folding two labels a single bit apart into a mask
			// the machine can refuse. The equality tests are written out instead.
			name: "switch dispatches on equality tests where the register holds the tag",
			src: `long long g;
void main(void) {
    switch (g) {
    case 0:
        g = 1;
        break;
    case 1:
    case 2:
        g = 3;
        break;
    default:
        g = 4;
        break;
    }
}`,
			want:   []string{"fcmp oeq double", "switch.test:", "switch.case:", "switch.end:"},
			absent: []string{"fptosi double", "switch i64"},
		},
		{
			name: "break leaves the loop",
			src:  "long long g; void main(void) { while (true) { if (g > 0) { break; } g = g + 1; } }",
			want: []string{"while.end:"},
		},
		{
			name: "the conditional operator merges with a phi",
			src:  "long long g; void main(void) { g = g > 0 ? 1 : 2; }",
			want: []string{"phi double", "cond.then:", "cond.else:", "cond.end:"},
		},
		{
			name: "a short-circuit operator read as a value merges through a phi",
			src:  "long long g; void main(void) { g = g > 0 && g < 4; }",
			want: []string{"phi i1", "logical.rhs:", "logical.end:"},
		},
		{
			name:   "a short-circuit condition becomes a branch chain, not a value to test",
			src:    "long long g; void main(void) { if (g > 0 && g < 4) { g = 1; } }",
			want:   []string{"land.rhs:"},
			absent: []string{"phi i1"},
		},
		{
			name:   "a short-circuit or condition becomes a branch chain too",
			src:    "long long g; void main(void) { if (g > 0 || g < 4) { g = 1; } }",
			want:   []string{"lor.rhs:"},
			absent: []string{"phi i1"},
		},
		{
			name:   "negating a condition swaps the branch targets",
			src:    "long long g; void main(void) { if (!(g > 0)) { g = 1; } }",
			want:   []string{"fcmp ogt double"},
			absent: []string{"uitofp i1"},
		},
		{
			name: "a comparison used as a value widens to 0 or 1",
			src:  "long long g; void main(void) { g = g > 0; }",
			want: []string{"fcmp ogt double", "uitofp i1"},
		},
		{
			name:   "a call is inlined rather than emitted",
			src:    "long long twice(long long x) { return x + x; } long long g; void main(void) { g = twice(3); }",
			want:   []string{"twice.cont:", "fadd double"},
			absent: []string{"call double @twice"},
		},
		{
			name: "a nested call is inlined too",
			src: `long long twice(long long x) { return x + x; }
long long quad(long long x) { return twice(twice(x)); }
long long g;
void main(void) { g = quad(3); }`,
			want:   []string{"quad.cont:", "twice.cont:"},
			absent: []string{"call double @twice", "call double @quad"},
		},
		{
			name:   "an intrinsic becomes an opaque call carrying its resolved operands",
			src:    "double g; void main(void) { g = __ic_load(d0, Temperature); }",
			want:   []string{"call double @__ic_load(i64 0, i64 6)"},
			absent: []string{"call double @__ic_load(i64 0, i64 0)"},
		},
		{
			name: "a void intrinsic becomes a void call",
			src:  "void main(void) { __ic_yield(); }",
			want: []string{"call void @__ic_yield()"},
		},
		{
			name:   "__ic_hash folds to its CRC-32 and reaches no instruction",
			src:    "long long g; void main(void) { g = __ic_hash(\"StructureWallLight\"); }",
			absent: []string{"@__ic_hash"},
		},
		{
			name:   "a cast of a double to a long long is the machine's own truncation",
			src:    "long long g; void main(void) { g = (long long)__ic_load(d0, Temperature); }",
			want:   []string{"call double @llvm.trunc.f64(double"},
			absent: []string{"@__ic_narrow"},
		},
		{
			// The two types either side of this cast are the same double, so
			// only the operand's MicroC type says whether anything rounds. The
			// conversion analysis records on the operand is the cast's own
			// target and answers the question wrongly.
			name:   "a cast of a value that is already a long long rounds nothing",
			src:    "long long g; void main(void) { g = (long long)(g + 1); }",
			absent: []string{"llvm.trunc"},
		},
		{
			// Negating an integer zero gives a positive zero in C, where fneg is
			// a sign flip that would answer -0.
			name:   "negating an integer is a subtraction from zero rather than a sign flip",
			src:    "long long g; void main(void) { g = -g; }",
			want:   []string{"fsub double 0.000000e+00"},
			absent: []string{"fneg"},
		},
		{
			// The sign flip is the whole of the operation, and a fast-math flag
			// on the instruction licenses dropping it for a zero operand, so the
			// text pins an fneg with nothing standing between it and its type.
			name:   "negating a double is the sign flip C asks for",
			src:    "double g; void main(void) { g = -g; }",
			want:   []string{"= fneg double"},
			absent: []string{"fsub"},
		},
		{
			name: "a global keeps its own storage",
			src:  "long long g = 7; void main(void) { g = g + 1; }",
			want: []string{"@g = internal global double 0.000000e+00", "store double 7.000000e+00, ptr @g"},
		},
		{
			name:   "a constexpr global folds into every use",
			src:    "constexpr long long k = 7; long long g; void main(void) { g = k; }",
			want:   []string{"store double 7.000000e+00, ptr @g"},
			absent: []string{"@k"},
		},
		{
			// const alone is not a constant expression, so the object is real and
			// every use is a load of it.
			name: "a const global keeps its own storage",
			src:  "const long long k = 7; long long g; void main(void) { g = k; }",
			want: []string{"@k = internal global double 0.000000e+00", "store double 7.000000e+00, ptr @k", "load double, ptr @k"},
		},
		{
			name: "an array global is one flat LLVM array",
			src:  "long long a[4]; void main(void) { a[0] = 1; }",
			want: []string{"@a = internal global [4 x double] zeroinitializer"},
		},
		{
			// The element type is uniform, so the stride is the one constant
			// instruction selection divides out.
			name: "a subscript is a getelementptr over the element type",
			src:  "long long a[4]; long long g; void main(void) { g = a[g]; }",
			want: []string{"fptosi double", "getelementptr inbounds double, ptr @a"},
		},
		{
			name: "a constant subscript folds into a constant address",
			src:  "long long a[4]; long long g; void main(void) { g = a[2]; }",
			want: []string{"getelementptr inbounds (double, ptr @a, i64 2)"},
		},
		{
			// Chip memory survives power loss, so a declared value is written
			// at run time; the elements past the last one supplied are left to
			// the prologue's clr db.
			name:   "a brace initializer stores the elements it supplies",
			src:    "long long a[4] = {1, 10}; void main(void) { a[0] = a[1]; }",
			want:   []string{"store double 1.000000e+00, ptr @a", "store double 1.000000e+01, ptr getelementptr"},
			absent: []string{"store double 0.000000e+00, ptr getelementptr"},
		},
		{
			name: "an address-taken local keeps its alloca and a pointer parameter holds a pointer",
			src:  "void put(long long *p) { *p = 7; }\nvoid main(void) { long long x = 1; put(&x); }",
			want: []string{"alloca double", "alloca ptr", "store ptr %x"},
		},
		{
			name: "an array name decays to the address of its first element",
			src:  "long long sum(long long *p) { return p[0] + p[1]; }\nlong long a[2]; long long g;\nvoid main(void) { g = sum(a); }",
			want: []string{"store ptr @a"},
		},
		{
			name: "a local array of the entry point is one global of the whole object",
			src:  "long long g; void main(void) { long long a[3]; a[2] = 5; g = a[2]; }",
			want: []string{"internal global [3 x double] zeroinitializer"},
		},
		{
			name: "a local array of a function compiled out of line is one alloca of the whole object",
			src: `long long step(long long k) {
    long long a[3];
    a[2] = k;
    if (k <= 0) { return a[2]; }
    return step(k - 1);
}
long long g;
void main(void) { g = step(3); }`,
			want: []string{"alloca [3 x double]"},
		},
		{
			name: "debug info is declared at the version LLVM keeps",
			src:  "void main(void) { }",
			want: []string{`!"Debug Info Version", i32 3`, "DISubprogram", "DICompileUnit"},
		},
		{
			// A debugger reading a signed integer off a slot the machine fills
			// with a double would render every value as the bit pattern of one.
			name: "the debug type follows the lowering rather than the source spelling",
			src:  "void main(void) { }",
			want: []string{`!DIBasicType(name: "double", size: 64, encoding: DW_ATE_float)`},
		},
		{
			// The integer identities are what int is lowered as an integer for,
			// and they are wrong for a value that is not one. A float type is
			// what withholds them.
			name:   "double arithmetic is float arithmetic",
			src:    "double g; void main(void) { g = __ic_load(d0, Temperature) * 2.0 + 1.5; }",
			want:   []string{"fmul double", "fadd double"},
			absent: []string{"mul nsw i64", "shl i64"},
		},
		{
			name:   "double division is a bare fdiv",
			src:    "double g; void main(void) { g = __ic_load(d0, Temperature) / 3.0; }",
			want:   []string{"fdiv double"},
			absent: []string{"sdiv", "@__ic_trunc"},
		},
		{
			name:   "long long division still truncates where a double division does not",
			src:    "long long g; void main(void) { g = (long long)__ic_load(d0, Temperature) / 3; }",
			want:   []string{"fdiv double", "call double @llvm.trunc.f64"},
			absent: []string{"sdiv"},
		},
		{
			// LLVM's own fcmp draws the ordered and unordered distinction the
			// machine draws, so the polarity needs no hiding: the four
			// orderings are ordered and inequality is not.
			name: "an ordered comparison of doubles is an ordered fcmp",
			src:  "long long g; void main(void) { g = __ic_load(d0, Temperature) >= 300.0; }",
			want: []string{"fcmp oge double"},
		},
		{
			name: "inequality of doubles is unordered, so a NaN is unequal",
			src:  "long long g; void main(void) { g = __ic_load(d0, Temperature) != 300.0; }",
			want: []string{"fcmp une double"},
		},
		{
			name:   "a long long operand meeting a double one needs no widening",
			src:    "double g; void main(void) { long long n = 2; g = __ic_load(d0, Temperature) * n; }",
			want:   []string{"fmul double"},
			absent: []string{"sitofp"},
		},
		{
			name:   "a cast of a long long to a double emits nothing at all",
			src:    "double g; void main(void) { long long n = 2; g = (double)n; }",
			absent: []string{"@__ic_narrow", "llvm.trunc", "sitofp"},
		},
		{
			name: "the NaN test is an instruction of its own",
			src:  "long long g; void main(void) { g = __ic_isnan(__ic_load(d0, Temperature)); }",
			want: []string{"call double @__ic_isnan(double"},
		},
		{
			name: "a machine constant folds to the game's own value",
			src:  "double g; void main(void) { g = deg2rad; }",
			want: []string{"0x3F91DF46A0000000"},
		},
		{
			// The pin is resolved here rather than passed, which is what makes a
			// dev parameter possible at all: the chip needs a literal where the
			// line is assembled.
			name:   "a dev parameter is substituted at the call site",
			src:    "void drive(dev d) { __ic_store(d, On, 1); } void main(void) { drive(d2); drive(d4); }",
			want:   []string{"call void @__ic_store(i64 2,", "call void @__ic_store(i64 4,"},
			absent: []string{"@drive"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text := generate(t, tc.src)
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Errorf("module does not contain %q", want)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(text, absent) {
					t.Errorf("module contains %q, which it should not", absent)
				}
			}
			if t.Failed() {
				t.Logf("--- module ---\n%s", text)
			}
		})
	}
}

// TestGenerateAttachesDebugLocations is the load-bearing case: the pointer
// check, the byte budget report, and every backend rejection name a user
// source line only because a location rides on every instruction from here.
func TestGenerateAttachesDebugLocations(t *testing.T) {
	const src = `long long g;
long long add(long long a, long long b) {
    return a + b;
}
void main(void) {
    g = add(1, 2);
    if (g > 0) {
        g = g - 1;
    }
}`

	result, err := Generate(t.Context(), analyze(t, src), Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	defer result.Dispose()

	fn := result.Module.NamedFunction("main")
	if fn.IsNil() {
		t.Fatalf("the module defines no main")
	}
	if fn.Subprogram().IsNil() {
		t.Fatalf("main carries no subprogram, so no instruction's scope is valid")
	}

	var lines []int
	for bb := fn.FirstBasicBlock(); !bb.IsNil(); bb = llvm.NextBasicBlock(bb) {
		for in := bb.FirstInstruction(); !in.IsNil(); in = llvm.NextInstruction(in) {
			loc := in.InstructionDebugLoc()
			if loc.IsNil() {
				t.Errorf("instruction has no debug location: %s", in.String())
				continue
			}
			if line := int(loc.LocationLine()); !slices.Contains(lines, line) {
				lines = append(lines, line)
			}
		}
	}

	// The body of add is inlined into main, so its own lines have to appear:
	// that is what lets a diagnostic point into a callee.
	for _, want := range []int{3, 6, 7, 8} {
		if !slices.Contains(lines, want) {
			t.Errorf("no instruction is attributed to source line %d; located lines are %v", want, lines)
		}
	}
	if t.Failed() {
		t.Logf("--- module ---\n%s", result.Module.String())
	}
}

// deepCallChain builds a program whose entry point reaches through depth nested
// calls, one function per line so that a diagnostic's line names the call it is
// about. None of them recurses, so every one is inlined.
func deepCallChain(depth int) string {
	var b strings.Builder
	b.WriteString("long long f0(long long x) { return x + 1; }\n")
	for i := 1; i < depth; i++ {
		fmt.Fprintf(&b, "long long f%d(long long x) { return f%d(x) + 1; }\n", i, i-1)
	}
	fmt.Fprintf(&b, "void main(void) { __ic_store(d0, Setting, (double)f%d((long long)__ic_load(d1, Setting))); }\n", depth-1)
	return b.String()
}

func TestGenerateRejects(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
		line int
	}{
		{
			// The chip reaches the entry point at line 0 rather than through
			// jal, so it holds no return address for a return to jump through.
			name: "a call to the entry point",
			src:  "void main(void) { main(); }",
			want: "cannot be called",
			line: 1,
		},
		{
			// Nothing here recurses, so every call is spliced in and a chain
			// past the bound has nowhere left to expand. It is also the widest
			// error path there is: the whole debug metadata graph is live when
			// the diagnostic is raised and the LLVM state is released.
			name: "a call chain deeper than inlining goes",
			src:  deepCallChain(maxInlineDepth + 2),
			want: "would nest more than",
			line: 3,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := Generate(t.Context(), analyze(t, tc.src), Options{})
			if err == nil {
				result.Dispose()
				t.Fatalf("Generate accepted a program it cannot lower")
			}
			var diags source.DiagnosticList
			if !asDiagnostics(err, &diags) {
				t.Fatalf("error is %T, want a source.DiagnosticList: %v", err, err)
			}
			if !strings.Contains(diags.String(), tc.want) {
				t.Errorf("diagnostics do not mention %q:\n%s", tc.want, diags.String())
			}
			for _, diag := range diags {
				if !diag.Pos.IsValid() {
					t.Errorf("diagnostic %q carries no source position", diag.Msg)
				}
			}
			if !slices.ContainsFunc(diags, func(d source.Diagnostic) bool { return d.Pos.Line == tc.line }) {
				t.Errorf("no diagnostic is at line %d:\n%s", tc.line, diags.String())
			}
		})
	}
}

func asDiagnostics(err error, out *source.DiagnosticList) bool {
	diags, ok := err.(source.DiagnosticList)
	if ok {
		*out = diags
	}
	return ok
}

func TestGenerateRejectsProgramWithoutMainBody(t *testing.T) {
	// Analysis assigns Main before it checks the signature and the body, so a
	// program it rejected can hand back a Main with no declaration. Reaching
	// through it would be a nil dereference rather than a diagnostic.
	file, _, err := tsparse.Parse("test.c", "void main(void);")
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	prog, diags, err := sema.Analyze(context.Background(), file, sema.Shipped{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(diags) == 0 {
		t.Fatalf("analysis accepted a main with no body")
	}
	result, err := Generate(context.Background(), prog, Options{})
	if err == nil {
		result.Dispose()
		t.Fatalf("Generate accepted a program with no main body")
	}
	if !strings.Contains(err.Error(), "main") {
		t.Errorf("error does not name main: %v", err)
	}
}

func TestGenerateRejectsNilProgram(t *testing.T) {
	result, err := Generate(context.Background(), nil, Options{})
	if err == nil {
		result.Dispose()
		t.Fatalf("Generate accepted a nil program")
	}
}

func TestGenerateHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := Generate(ctx, analyze(t, "void main(void) { }"), Options{})
	if err == nil {
		result.Dispose()
		t.Fatalf("Generate ignored a cancelled context")
	}
}

// TestVerifyModuleReportsRatherThanAborts pins the behaviour Generate depends
// on for its own defects: a malformed module has to come back as an error, not
// as a process abort, because the module text is what diagnoses it.
func TestVerifyModuleReportsRatherThanAborts(t *testing.T) {
	ctx := llvm.NewContext()
	defer ctx.Dispose()
	m := ctx.NewModule("malformed")
	defer m.Dispose()

	// A block with no terminator is the smallest thing the verifier rejects.
	fn := llvm.AddFunction(m, "broken", llvm.FunctionType(ctx.VoidType(), nil, false))
	llvm.AddBasicBlock(fn, "entry")

	if err := llvm.VerifyModule(m, llvm.ReturnStatusAction); err == nil {
		t.Fatalf("VerifyModule accepted a block with no terminator")
	}
}

func TestOptionsNameTheModuleAndItsFile(t *testing.T) {
	result, err := Generate(t.Context(), analyze(t, "void main(void) { }"), Options{ModuleName: "airlock.c", Dir: "/src"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	defer result.Dispose()
	text := result.Module.String()
	for _, want := range []string{`ModuleID = 'airlock.c'`, `filename: "airlock.c"`, `directory: "/src"`} {
		if !strings.Contains(text, want) {
			t.Errorf("module does not contain %q", want)
		}
	}
}

// functionAttributeIndex is LLVMAttributeFunctionIndex, which the attribute
// accessors take where an attribute belongs to the function rather than to one
// of its parameters. The bindings expose no name for it.
const functionAttributeIndex = -1

// TestDefinitionsAreFreestanding covers the attribute that keeps the optimizer
// from replacing a run of instructions with a call to a C library routine. There
// is no library on the chip and no lowering for a call to one, and a run of
// zeroing stores merged into a memset is the shape that reaches it.
func TestDefinitionsAreFreestanding(t *testing.T) {
	const src = `long long step(long long k) {
    if (k <= 0) { return 0; }
    return step(k - 1) + 1;
}
void main(void) {
    long long a[4];
    for (long long i = 0; i < 4; i++) { a[i] = step(i); }
    __ic_store(d0, Setting, a[2]);
}`
	m := lower(t, src)

	for _, name := range []string{"main", "step"} {
		fn := m.NamedFunction(name)
		if fn.IsNil() {
			t.Fatalf("the module defines no %s:\n%s", name, m.String())
		}
		if attr := fn.GetStringAttributeAtIndex(functionAttributeIndex, "no-builtins"); attr == (llvm.Attribute{}) {
			t.Errorf("%s carries no no-builtins attribute, so a library call may be synthesized into it:\n%s", name, m.String())
		}
	}
}

func TestDisposeIsIdempotent(t *testing.T) {
	result, err := Generate(t.Context(), analyze(t, "void main(void) { }"), Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	result.Dispose()
	result.Dispose()
}

// TestAnalysisRefusesAValueFromAVoidCall pins the precondition [generator.value]
// rests on. A call returning void is the one expression producing no value, and
// what keeps a null out of the C++ builder is that analysis admits one nowhere a
// value is wanted. The rows are the positions this stage would otherwise reach.
func TestAnalysisRefusesAValueFromAVoidCall(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "an operand of an arithmetic operator", body: "long long a = v() + 1;"},
		{name: "an operand of a comparison", body: "long long a = v() == 1;"},
		{name: "the operand of a bitwise operator", body: "long long a = v() & 1;"},
		{name: "the operand of a cast", body: "long long a = (long long)v();"},
		{name: "an initializer", body: "long long a = v();"},
		{name: "the value of an assignment", body: "long long a; a = v();"},
		{name: "a condition", body: "if (v()) { }"},
		{name: "an arm of a conditional", body: "long long a = 1 ? v() : 2;"},
		{name: "a switch tag", body: "switch (v()) { default: break; }"},
		{name: "an argument", body: "long long a = f(v());"},
		{name: "an intrinsic operand", body: "__ic_store(d0, Setting, v());"},
		{name: "a subscript", body: "long long a[2]; long long b = a[v()];"},
		{name: "a returned value", body: "long long a = g(v());"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := "void v(void) { }\nlong long f(long long x) { return x; }\nlong long g(long long x) { return x; }\nvoid main(void) { " + tt.body + " }"
			file, diags, err := tsparse.Parse("test.c", src)
			if err != nil {
				t.Fatalf("parsing: %v", err)
			}
			if len(diags) > 0 {
				t.Fatalf("parsing:\n%s", diags.String())
			}
			_, diags, err = sema.Analyze(t.Context(), file, sema.Shipped{})
			if err != nil {
				t.Fatalf("Analyze: %v", err)
			}
			if !diags.HasErrors() {
				t.Errorf("analysis accepted a void call where a value is wanted, "+
					"which is a null handed to the builder here:\n%s", src)
			}
		})
	}
}

// TestAVoidCallWhereAValueIsWantedIsReported covers what happens when that
// precondition is bypassed. Analysis returns its program alongside its
// diagnostics, so a caller lowering one it rejected reaches this stage with the
// shape the rows above normally keep out.
func TestAVoidCallWhereAValueIsWantedIsReported(t *testing.T) {
	file, diags, err := tsparse.Parse("test.c", "void v(void) { }\nvoid main(void) { long long a = v() + 1; }")
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if len(diags) > 0 {
		t.Fatalf("parsing:\n%s", diags.String())
	}
	prog, diags, err := sema.Analyze(t.Context(), file, sema.Shipped{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if !diags.HasErrors() {
		t.Fatal("analysis accepted a void call as an operand")
	}
	result, err := Generate(t.Context(), prog, Options{})
	if err == nil {
		result.Dispose()
		t.Fatal("lowering accepted a call that produces no value where one is wanted")
	}
	if !strings.Contains(err.Error(), "produces no value") {
		t.Errorf("the refusal does not name the missing value: %v", err)
	}
}
