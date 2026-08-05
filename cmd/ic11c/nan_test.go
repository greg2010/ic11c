package main

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
)

// A batch read matching no device answers Average with NaN; ordered
// comparisons are false for it, so the tests below drive the value through
// each operator on the interpreter, since the mnemonic alone does not say
// which arm control reaches.

// nanPrefab is a prefab hash no device in these tests carries, so every batch
// read of it matches nothing.
const nanPrefab = "StructureWallCooler"

// scalarPaths are the two types a NaN reaches a comparison in.
var scalarPaths = []string{"double", "long long"}

// TestOrderedComparisonWithNaNIsFalse holds every ordered operator to the
// machine's own answer: a NaN operand makes the comparison false, whichever way
// round it is written, so its negation is true.
func TestOrderedComparisonWithNaNIsFalse(t *testing.T) {
	tests := []struct {
		name string
		// body is the statements between the two reads and the store, and cond
		// the condition guarding the store.
		body string
		cond string
		want float64
	}{
		{name: "greater or equal", cond: "a >= b"},
		{name: "less or equal", cond: "a <= b"},
		{name: "greater", cond: "a > b"},
		{name: "less", cond: "a < b"},
		{name: "the negation of a false comparison holds", cond: "!(a >= b)", want: 1},
		{name: "a conjunction", cond: "a >= b && b >= 0"},
		{name: "a disjunction of two false comparisons", cond: "a >= b || a < b"},
		{name: "the comparison read as a value", body: "long long c = a >= b;", cond: "c == 1"},
		{name: "equality is false against a NaN", cond: "a == b"},
		{name: "inequality is true against a NaN", cond: "a != b", want: 1},
		{name: "a NaN is unequal to itself", cond: "a != a", want: 1},
		{name: "the NaN test names itself", cond: "__ic_isnan((double)a)", want: 1},
		{name: "the NaN test is false for an ordinary reading", cond: "__ic_isnan((double)b)"},
	}
	for _, tt := range tests {
		for _, scalar := range scalarPaths {
			t.Run(tt.name+" held in a "+scalar, func(t *testing.T) {
				assembly := compileNaNProgram(t, scalar, tt.body, tt.cond)

				housing, sensor, actuator := devicePair(t)
				setLogic(t, sensor, "Setting", 5)
				housedChip(t, assembly, housing)
				runProgram(t, housing, assembly)

				if got := logicValue(t, actuator, "On"); got != tt.want {
					t.Errorf("with a NaN operand %q left d1 On at %v, want %v\n%s",
						tt.cond, got, tt.want, assembly)
				}
			})
		}
	}
}

// TestIsNaNDetectsAnEmptyBatchRead covers the instruction the language exposes
// for the case a comparison cannot answer. Every natural spelling of a NaN test
// folds to false under an ordered comparison, so the batch read documented as
// answering NaN when it matches nothing is undetectable without this one.
func TestIsNaNDetectsAnEmptyBatchRead(t *testing.T) {
	tests := []struct {
		name string
		read string
		want float64
	}{
		{
			name: "a batch read matching no device",
			read: `__ic_load_batch(__ic_hash("` + nanPrefab + `"), Temperature, Average)`,
			want: 1,
		},
		{
			name: "an ordinary device read",
			read: "__ic_load(d0, Setting)",
			want: 0,
		},
		{
			name: "a partial transcendental outside its domain",
			read: "__ic_sqrt(0.0 - __ic_load(d0, Setting))",
			want: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := "void main(void) {\n" +
				"  double v = " + tt.read + ";\n" +
				"  __ic_store(d1, On, __ic_isnan(v));\n" +
				"}\n"
			assembly := compileNaNPath(t, "isnan.c", src)

			housing, sensor, actuator := devicePair(t)
			setLogic(t, sensor, "Setting", 5)
			housedChip(t, assembly, housing)
			runProgram(t, housing, assembly)

			if got := logicValue(t, actuator, "On"); got != tt.want {
				t.Errorf("__ic_isnan over %s left d1 On at %v, want %v\n%s", tt.read, got, tt.want, assembly)
			}
		})
	}
}

// TestIsNaNReadAsAValueIsOneInstruction covers the peephole's two re-test
// folds through the whole compiler: __ic_isnan asks a value already 0 or 1
// for its truth a second time, which the optimizer leaves in since the
// declaration says nothing about its range. Either polarity folds to one instruction.
func TestIsNaNReadAsAValueIsOneInstruction(t *testing.T) {
	negated := compileNaNPath(t, "isnan_value.c", "void main(void) {\n"+
		`  double v = __ic_load_batch(__ic_hash("`+nanPrefab+`"), Temperature, Average);`+"\n"+
		"  __ic_store(d1, On, !__ic_isnan(v));\n"+
		"}\n")
	if !strings.Contains(negated, "snanz ") {
		t.Errorf("a negated NaN test did not fold into the complementary instruction:\n%s", negated)
	}
	if strings.Contains(negated, "seqz ") {
		t.Errorf("a negated NaN test still pays for a separate negation:\n%s", negated)
	}

	src := "void main(void) {\n" +
		`  double v = __ic_load_batch(__ic_hash("` + nanPrefab + `"), Temperature, Average);` + "\n" +
		"  if (__ic_isnan(v)) {\n" +
		"    v = 0.0;\n" +
		"  }\n" +
		"  __ic_store(d1, Setting, v);\n" +
		"}\n"
	assembly := compileNaNPath(t, "isnan_guard.c", src)
	if strings.Contains(assembly, "snez ") {
		t.Errorf("a NaN test read straight still pays to be asked a second time:\n%s", assembly)
	}

	// The fold is only worth taking if the program still means the same thing,
	// which is why the run matters more than the mnemonic: the condition has to
	// keep the reading it was given and replace only the NaN.
	tests := []struct {
		name string
		// reading replaces the batch read's NaN once the program has produced
		// it. NaN leaves the interpreter's own answer in place.
		reading float64
		want    float64
	}{
		{name: "a batch read matching no device", reading: math.NaN(), want: 0},
		{name: "an ordinary reading is kept", reading: 7.5, want: 7.5},
		{name: "a zero reading is kept", reading: 0, want: 0},
		{name: "a negative reading is kept", reading: -3.25, want: -3.25},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			housing, _, actuator := devicePair(t)
			housedChip(t, assembly, housing)
			runBatchProgram(t, housing, assembly, tt.reading)

			if got := logicValue(t, actuator, "Setting"); got != tt.want {
				t.Errorf("the guard over %v left d1 Setting at %v, want %v\n%s", tt.reading, got, tt.want, assembly)
			}
		})
	}
}

// compileNaNProgram builds a program whose first value comes from a batch
// read matching no device, which the interpreter answers with NaN. scalar is
// the type both values are held in; int reaches the same NaN through the
// explicit cast, since the machine's toward-zero rounding answers NaN with itself.
func compileNaNProgram(t *testing.T, scalar, body, cond string) string {
	t.Helper()
	narrow := ""
	if scalar == "long long" {
		narrow = "(long long)"
	}
	return compileNaNPath(t, "nan.c", "void main(void) {\n"+
		"  "+scalar+" a = "+narrow+"__ic_load_batch(__ic_hash(\""+nanPrefab+"\"), Temperature, Average);\n"+
		"  "+scalar+" b = "+narrow+"__ic_load(d0, Setting);\n"+
		"  "+body+"\n"+
		"  if ("+cond+") { __ic_store(d1, On, 1); }\n"+
		"}\n")
}

// TestOrderedComparisonWithoutNaNKeepsBothAnswers checks the guard changed
// nothing else: with two ordinary numbers each operator still answers what it
// says.
func TestOrderedComparisonWithoutNaNKeepsBothAnswers(t *testing.T) {
	tests := []struct {
		name string
		cond string
		// a is the value the batch read is made to return, against a fixed b
		// of 5.
		a    float64
		want float64
	}{
		{name: "greater or equal holds", cond: "a >= b", a: 9, want: 1},
		{name: "greater or equal at the boundary", cond: "a >= b", a: 5, want: 1},
		{name: "greater or equal fails", cond: "a >= b", a: 1, want: 0},
		{name: "less holds", cond: "a < b", a: 1, want: 1},
		{name: "less fails", cond: "a < b", a: 9, want: 0},
		{name: "less or equal at the boundary", cond: "a <= b", a: 5, want: 1},
		{name: "greater fails at the boundary", cond: "a > b", a: 5, want: 0},
	}
	for _, tt := range tests {
		for _, scalar := range scalarPaths {
			t.Run(tt.name+" held in a "+scalar, func(t *testing.T) {
				assembly := compileNaNProgram(t, scalar, "", tt.cond)

				// The batch read matches nothing whatever is on the pins, so the
				// value it would have returned is written into the register it
				// lands in after the read has run.
				housing, sensor, actuator := devicePair(t)
				setLogic(t, sensor, "Setting", 5)
				housedChip(t, assembly, housing)
				runBatchProgram(t, housing, assembly, tt.a)

				if got := logicValue(t, actuator, "On"); got != tt.want {
					t.Errorf("with a = %v, %q left d1 On at %v, want %v\n%s", tt.a, tt.cond, got, tt.want, assembly)
				}
			})
		}
	}
}

// TestFractionalComparisonIsNotAnIntegerOne is the gap a fractional type
// closes. Every value a chip reads is fractional, and lowering one as an integer
// let the optimizer apply integer identities to it: a temperature of 299.5 K
// compared against 300 became a comparison against 299, which it is above.
func TestFractionalComparisonIsNotAnIntegerOne(t *testing.T) {
	tests := []struct {
		name        string
		temperature float64
		want        float64
	}{
		{name: "below the setpoint by half a kelvin", temperature: 299.5, want: 0},
		{name: "at the setpoint", temperature: 300, want: 1},
		{name: "above the setpoint by half a kelvin", temperature: 300.5, want: 1},
		{name: "one ulp below the setpoint", temperature: math.Nextafter(300, 0), want: 0},
	}
	const src = `void main(void) {
  double t = __ic_load(d0, Temperature);
  __ic_store(d1, On, t >= 300);
}
`
	assembly := compileNaNPath(t, "fractional.c", src)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			housing, sensor, actuator := devicePair(t)
			setLogic(t, sensor, "Temperature", tt.temperature)
			housedChip(t, assembly, housing)
			runProgram(t, housing, assembly)

			if got := logicValue(t, actuator, "On"); got != tt.want {
				t.Errorf("at %v kelvin the comparison answered %v, want %v\n%s",
					tt.temperature, got, tt.want, assembly)
			}
		})
	}
}

// TestFractionalArithmeticKeepsItsFraction covers the other half of the same
// gap: an integer lowering turned a multiplication into a shift and a division
// into a truncating one, both of which lose a fraction the machine keeps.
func TestFractionalArithmeticKeepsItsFraction(t *testing.T) {
	tests := []struct {
		name  string
		expr  string
		input float64
		want  float64
	}{
		{name: "doubling a fractional reading", expr: "t * 2", input: 293.15, want: 586.3},
		{name: "halving a fractional reading", expr: "t / 2", input: 7, want: 3.5},
		{name: "double division does not truncate", expr: "t / 4", input: 7, want: 1.75},
		{name: "integer division does truncate", expr: "(double)((long long)t / 4)", input: 7, want: 1},
		{name: "a fraction survives an addition", expr: "t + 0.25", input: 1.5, want: 1.75},
		{name: "the machine's own degree conversion", expr: "t * deg2rad", input: 180, want: 180 * 0.01745329238474369},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := "void main(void) {\n" +
				"  double t = __ic_load(d0, Temperature);\n" +
				"  __ic_store(d1, Setting, " + tt.expr + ");\n" +
				"}\n"
			assembly := compileNaNPath(t, "arith.c", src)

			housing, sensor, actuator := devicePair(t)
			setLogic(t, sensor, "Temperature", tt.input)
			housedChip(t, assembly, housing)
			runProgram(t, housing, assembly)

			if got := logicValue(t, actuator, "Setting"); got != tt.want {
				t.Errorf("%s with t = %v produced %v, want %v\n%s", tt.expr, tt.input, got, tt.want, assembly)
			}
		})
	}
}

// TestIntegerConversionsKeepTheirAnswer covers two places the lowering
// stands between a source construct and the published value: a cast rounds
// only by the operand's MicroC type (both sides are the same double in IR),
// and C's integer-zero negation stays positive where the machine flips sign.
func TestIntegerConversionsKeepTheirAnswer(t *testing.T) {
	tests := []struct {
		name  string
		expr  string
		input float64
		want  float64
		// negativeZero is what the answer's sign bit must be, for the one case
		// where the value alone does not distinguish two answers.
		negativeZero bool
	}{
		{name: "a cast truncates a positive fraction", expr: "(double)((long long)t)", input: 7.5, want: 7},
		{name: "a cast truncates toward zero, not down", expr: "(double)((long long)t)", input: -7.5, want: -7},
		{name: "a cast of a whole reading leaves it alone", expr: "(double)((long long)t)", input: -8, want: -8},
		{name: "negating an integer zero gives a positive zero", expr: "(double)(-(long long)t)", input: 0, want: 0},
		{name: "negating a double zero gives the sign flip C asks for", expr: "-t", input: 0, want: 0, negativeZero: true},
		{name: "negating an integer keeps its value", expr: "(double)(-(long long)t)", input: 6, want: -6},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := "void main(void) {\n" +
				"  double t = __ic_load(d0, Temperature);\n" +
				"  __ic_store(d1, Setting, " + tt.expr + ");\n" +
				"}\n"
			assembly := compileNaNPath(t, "convert.c", src)

			housing, sensor, actuator := devicePair(t)
			setLogic(t, sensor, "Temperature", tt.input)
			// A logic write the machine performs only when the value
			// differs, and -0 does not differ from 0. Starting the field
			// somewhere else is what makes the store happen and the sign
			// bit observable.
			setLogic(t, actuator, "Setting", 99)
			housedChip(t, assembly, housing)
			runProgram(t, housing, assembly)

			got := logicValue(t, actuator, "Setting")
			if got != tt.want {
				t.Errorf("%s with t = %v produced %v, want %v\n%s", tt.expr, tt.input, got, tt.want, assembly)
			}
			// A non-zero answer carries its sign in its value; only a zero
			// needs the sign bit read separately.
			if got == 0 && math.Signbit(got) != tt.negativeZero {
				t.Errorf("%s with t = %v produced a %s zero\n%s", tt.expr, tt.input, signOf(got), assembly)
			}
		})
	}
}

// signOf names the sign of a zero the way a Setting reads.
func signOf(v float64) string {
	if math.Signbit(v) {
		return "negative"
	}
	return "positive"
}

// TestDeviceParameterReachesTheRightPin covers what a dev parameter is for. The
// chip needs a literal in a device position, so the pin cannot travel in a
// register; a function taking one is inlined and each site's device substituted,
// which is only observable by running the program against several devices.
func TestDeviceParameterReachesTheRightPin(t *testing.T) {
	const src = `const dev spare = d3;

void publish(dev target, double value) {
    __ic_store(target, Setting, value);
}

void main(void) {
    double base = __ic_load(d0, Setting);
    publish(d1, base + 1.5);
    publish(d2, base + 2.5);
    publish(spare, base + 3.5);
}
`
	assembly := compileNaNPath(t, "devparam.c", src)

	housing := newChipRun(t)
	housing.populate(t, 4)
	setLogic(t, housing.device(0), "Setting", 10)
	housedChip(t, assembly, housing)
	runProgram(t, housing, assembly)

	for _, want := range []struct {
		pin   int
		value float64
	}{
		{pin: 1, value: 11.5},
		{pin: 2, value: 12.5},
		{pin: 3, value: 13.5},
	} {
		if got := logicValue(t, housing.device(want.pin), "Setting"); got != want.value {
			t.Errorf("d%d holds %v, want %v\n%s", want.pin, got, want.value, assembly)
		}
	}
}

// TestNaNSurvivesAcrossAssignments checks that a NaN reaches a comparison
// several assignments away from the batch read that produced it, rather than
// being folded out of the arithmetic between them.
func TestNaNSurvivesAcrossAssignments(t *testing.T) {
	src := "void main(void) {\n" +
		"  long long raw = (long long)__ic_load_batch(__ic_hash(\"" + nanPrefab + "\"), Temperature, Average);\n" +
		"  long long scaled = raw * 2;\n" +
		"  long long shifted = scaled - 273;\n" +
		"  if (shifted >= (long long)__ic_load(d0, Setting)) { __ic_store(d1, On, 1); }\n" +
		"}\n"
	assembly := compileNaNPath(t, "chain.c", src)

	housing, sensor, actuator := devicePair(t)
	setLogic(t, sensor, "Setting", 5)
	housedChip(t, assembly, housing)
	runProgram(t, housing, assembly)

	if got := logicValue(t, actuator, "On"); got != 0 {
		t.Errorf("a NaN carried through arithmetic ran the conditional statement: d1 On is %v, want 0\n%s", got, assembly)
	}
}

// TestEveryPathToAnIntegerComparisonKeepsItsPolarity drives each route a NaN
// takes to an integer comparison through the whole compiler, checking the
// branch the source's polarity selects. plain is the same program with the
// NaN swapped for an ordinary read; identical output means it folded away.
func TestEveryPathToAnIntegerComparisonKeepsItsPolarity(t *testing.T) {
	const nanRead = "__ic_sqrt(0.0 - __ic_load(d0, Setting))"
	const plainRead = "__ic_load(d0, Setting)"

	cases := []struct {
		name string
		// src holds one %s, filled with the read that produces the value.
		src string
	}{
		{
			name: "an argument carries it into an out-of-line callee",
			src: `long long step(long long n, long long d) {
    if (n >= d) { return step(n - d, d) + 1; }
    return 0;
}
void main(void) {
    long long n = (long long)%s;
    __ic_store(d1, Setting, (double)step(n, 3));
}`,
		},
		{
			name: "a result carries it out of an out-of-line callee",
			src: `long long src(long long k) {
    if (k <= 0) { return (long long)%s; }
    return src(k - 1);
}
void main(void) {
    long long n = src(3);
    long long m = (long long)__ic_load(d1, Setting);
    if (n >= m) { __ic_store(d2, Setting, 1); }
}`,
		},
		{
			name: "a division makes one with no intrinsic in sight",
			src: `void main(void) {
    double a = %s;
    double b = __ic_load(d1, Setting);
    long long n = (long long)(a / b);
    long long m = (long long)__ic_load(d2, Setting);
    if (n >= m) { __ic_store(d3, Setting, 1); }
}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			nan := compileNaNPath(t, "tainted.c", fmt.Sprintf(tc.src, nanRead))
			plain := compileNaNPath(t, "plain.c", fmt.Sprintf(tc.src, plainRead))

			if counts := mnemonics(t, nan); counts["bge"] != 1 || counts["blt"] != 0 {
				t.Errorf("the program does not branch on the '>=' the source wrote:\n%s", nan)
			}
			if nan == plain {
				t.Errorf("the program with a NaN source emits what the one without it does, so the source folded away:\n%s", nan)
			}
		})
	}
}

// TestDivisionByZeroDoesNotRunTheGuardedStatement is the same coverage taken to
// the interpreter for the one source that needs no intrinsic at all. Zero over
// zero is a NaN, every ordered comparison against it is false, and the
// statement the condition guards must not run.
func TestDivisionByZeroDoesNotRunTheGuardedStatement(t *testing.T) {
	const src = `void main(void) {
  double a = __ic_load(d0, Setting);
  long long n = (long long)(a / a);
  long long m = (long long)__ic_load(d0, Temperature);
  if (n >= m) { __ic_store(d1, On, 1); }
}
`
	assembly := compileNaNPath(t, "divzero.c", src)

	housing, sensor, actuator := devicePair(t)
	setLogic(t, sensor, "Setting", 0)
	setLogic(t, sensor, "Temperature", 0)
	housedChip(t, assembly, housing)
	runProgram(t, housing, assembly)

	if got := logicValue(t, actuator, "On"); got != 0 {
		t.Errorf("zero over zero ran the guarded statement: d1 On is %v, want 0\n%s", got, assembly)
	}
}

// compileNaNPath compiles one whole source and gives back the assembly.
func compileNaNPath(t *testing.T, name, src string) string {
	t.Helper()
	assembly, stderr, err := run(t, write(t, name, src))
	if err != nil {
		t.Fatalf("compiling:\n%s\n%v\n%s", src, err, stderr)
	}
	checkAssembly(t, assembly)
	return assembly
}

// mnemonics counts the lines of assembly starting with each instruction name.
func mnemonics(t *testing.T, assembly string) map[string]int {
	t.Helper()
	counts := make(map[string]int)
	for _, line := range assemblyLines(t, assembly) {
		name, _, _ := strings.Cut(strings.TrimSpace(line), " ")
		counts[name]++
	}
	return counts
}

// runProgram runs a chip until it leaves its own instructions, which is how a
// MicroC program without a loop of its own ends.
func runProgram(t *testing.T, housing *chipRun, assembly string) {
	t.Helper()
	runBatchProgram(t, housing, assembly, math.NaN())
}

// runBatchProgram runs a chip, overwriting the result of the batch read with
// value once it has run. The batch read matches no device, so the
// interpreter answers it with NaN and no world configures it to answer
// otherwise; passing NaN itself leaves the machine's own answer in place.
func runBatchProgram(t *testing.T, housing *chipRun, assembly string, value float64) {
	t.Helper()
	const maxSteps = 1000
	substituted := math.IsNaN(value)
	for range maxSteps {
		if !housing.running() {
			return
		}
		housing.step(t, 1)
		housing.faulted(t)
		if substituted {
			continue
		}
		for r := range ic10.Register(ic10.NumRegisters) {
			if math.IsNaN(housing.register(r)) {
				housing.setRegister(t, r, value)
				substituted = true
			}
		}
	}
	t.Fatalf("the program did not end within %d steps:\n%s", maxSteps, assembly)
}

// An empty Maximum batch read answers negative infinity — a source that
// reaches a register without a NaN anywhere in the program. Every integer
// identity LLVM applies is false of it (a-a isn't zero, a*0 isn't zero...),
// so the tests below drive each shape on the interpreter and read the value.

// infinitePrefab is a prefab hash no device in these tests carries, so a
// Maximum batch read of it matches nothing.
const infinitePrefab = "StructureWallCooler"

// TestIntegerArithmeticOverAnInfinityIsNotFolded holds each integer operator
// to the value the machine computes, against a long long holding negative
// infinity. A compiler lowering these as plain LLVM integers would fail
// every case: the identity folds to a constant and the chip is never asked.
func TestIntegerArithmeticOverAnInfinityIsNotFolded(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want float64
	}{
		{name: "a difference of a value with itself", expr: "m - m", want: math.NaN()},
		{name: "a product with zero", expr: "m * 0", want: math.NaN()},
		{name: "a quotient of a value by itself", expr: "m / m", want: math.NaN()},
		{name: "a remainder of a value by itself", expr: "m % m", want: math.NaN()},
		{name: "a sum keeps the infinity", expr: "m + 1", want: math.Inf(-1)},
		{name: "a product keeps the infinity", expr: "m * 2", want: math.Inf(-1)},
		{name: "a difference from a number keeps the infinity", expr: "m - 1", want: math.Inf(-1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := "void main(void) {\n" +
				`  long long m = (long long)__ic_load_batch(__ic_hash("` + infinitePrefab + `"), Temperature, BatchMode_Maximum);` + "\n" +
				"  __ic_store(d1, Setting, " + tt.expr + ");\n" +
				"}\n"
			assembly := compileNaNPath(t, "infinity.c", src)

			housing, _, actuator := devicePair(t)
			housedChip(t, assembly, housing)
			runProgram(t, housing, assembly)

			got := logicValue(t, actuator, "Setting")
			if math.IsNaN(tt.want) != math.IsNaN(got) || (!math.IsNaN(tt.want) && got != tt.want) {
				t.Errorf("%q over an infinity left d1 Setting at %v, want %v\n%s", tt.expr, got, tt.want, assembly)
			}
		})
	}
}

// TestOrderedComparisonOverAnInfinityFollowsTheMachine is the same source under
// a comparison. An infinity is ordered against every number, so the arithmetic
// below it is what decides: m - m is a NaN, and every ordered comparison of a
// NaN is false however it is written.
func TestOrderedComparisonOverAnInfinityFollowsTheMachine(t *testing.T) {
	tests := []struct {
		name string
		cond string
		want float64
	}{
		{name: "a difference of a value with itself is not at least zero", cond: "m - m >= 0"},
		{name: "a difference of a value with itself is not below zero", cond: "m - m < 0"},
		{name: "the negation of the false comparison holds", cond: "!(m - m >= 0)", want: 1},
		{name: "an infinity is below zero", cond: "m < 0", want: 1},
		{name: "an infinity is not at least zero", cond: "m >= 0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := "void main(void) {\n" +
				`  long long m = (long long)__ic_load_batch(__ic_hash("` + infinitePrefab + `"), Temperature, BatchMode_Maximum);` + "\n" +
				"  if (" + tt.cond + ") { __ic_store(d1, On, 1); }\n" +
				"}\n"
			assembly := compileNaNPath(t, "infinity_cmp.c", src)

			housing, _, actuator := devicePair(t)
			housedChip(t, assembly, housing)
			runProgram(t, housing, assembly)

			if got := logicValue(t, actuator, "On"); got != tt.want {
				t.Errorf("%q over an infinity left d1 On at %v, want %v\n%s", tt.cond, got, tt.want, assembly)
			}
		})
	}
}

// TestIntegerRemainderByZeroIsANaN covers the other source the machine has for a
// value no integer holds. The machine divides doubles, so a % 0 is an infinity
// times zero, which is a NaN, and every ordered comparison it reaches has to
// answer false rather than fall into the arm the inverted form would.
func TestIntegerRemainderByZeroIsANaN(t *testing.T) {
	const src = `void main(void) {
  long long a = (long long)__ic_load(d0, Setting);
  long long b = (long long)__ic_load(d0, Power);
  long long r = a % b;
  if (r >= 0) { __ic_store(d1, On, 1); } else { __ic_store(d1, On, 2); }
}
`
	tests := []struct {
		name    string
		divisor float64
		want    float64
	}{
		{name: "a remainder by zero answers neither arm's comparison", divisor: 0, want: 2},
		{name: "an ordinary divisor keeps the comparison", divisor: 4, want: 1},
	}
	assembly := compileNaNPath(t, "rem_zero.c", src)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			housing, sensor, actuator := devicePair(t)
			setLogic(t, sensor, "Setting", 7)
			setLogic(t, sensor, "Power", tt.divisor)
			housedChip(t, assembly, housing)
			runProgram(t, housing, assembly)

			if got := logicValue(t, actuator, "On"); got != tt.want {
				t.Errorf("a remainder by %v left d1 On at %v, want %v\n%s", tt.divisor, got, tt.want, assembly)
			}
		})
	}
}

// TestFoldedNaNReachesTheChip covers the value the machine holds and has no
// literal for: constant folding produces one from finite source text, so
// the backend has to compute it rather than spell it. The assertion is on
// the bit pattern, since a NaN is unequal to itself and would pass against any value.
func TestFoldedNaNReachesTheChip(t *testing.T) {
	tests := []struct {
		name string
		// decls precede main and expr is what the program stores, so a case can
		// fold at analysis or leave the folding to LLVM.
		decls string
		expr  string
	}{
		{
			name:  "a constexpr zero divided by itself",
			decls: "constexpr double kZero = 0.0;",
			expr:  "kZero / kZero",
		},
		{
			name: "a local zero divided by itself, which only the optimizer folds",
			expr: "__ic_min(0.0, 0.0) / __ic_min(0.0, 0.0)",
		},
		{
			name:  "a constexpr infinity minus itself",
			decls: "constexpr double kInf = 1.0 / 0.0;",
			expr:  "kInf - kInf",
		},
		{
			name:  "a NaN in an operand position no move holds",
			decls: "constexpr double kZero = 0.0;",
			expr:  "__ic_max(__ic_load(d0, Setting), kZero / kZero)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := tt.decls + "\nvoid main(void) {\n" +
				"  __ic_store(d1, Setting, " + tt.expr + ");\n}\n"
			assembly := compileNaNPath(t, "foldednan.c", src)
			assertNoNaNLiteral(t, assembly)

			housing, sensor, actuator := devicePair(t)
			setLogic(t, sensor, "Setting", 5)
			housedChip(t, assembly, housing)
			runProgram(t, housing, assembly)

			got := logicValue(t, actuator, "Setting")
			if !math.IsNaN(got) {
				t.Errorf("%q left d1 Setting at %v, bits %016x, want a NaN\n%s",
					tt.expr, got, math.Float64bits(got), assembly)
			}
		})
	}
}

// assertNoNaNLiteral holds the emitted assembly to what the chip's operand
// parser reads: it takes a NaN literal for unset and raises
// IncorrectVariable on the line, a fault per tick a running-only test would
// misread as a wrong answer rather than a compile-time defect.
func assertNoNaNLiteral(t *testing.T, assembly string) {
	t.Helper()
	for i, line := range assemblyLines(t, assembly) {
		for _, field := range strings.Fields(line)[1:] {
			if strings.EqualFold(field, "nan") {
				t.Errorf("line %d spells a NaN literal, which the chip reads as unset: %q\n%s", i, line, assembly)
			}
		}
	}
}

// TestMaterialisedNaNCarriesTheMachinePayload pins the NaN the chip ends up
// holding to the one the same division computes on the host. The payload is
// read off the device rather than through the differential harness, whose
// rule between a chip trace and a native one drops a NaN's payload; see devtrace's Diff.
func TestMaterialisedNaNCarriesTheMachinePayload(t *testing.T) {
	zero := 0.0
	want := math.Float64bits(zero / zero)
	src := "constexpr double kZero = 0.0;\n" +
		"void main(void) {\n  __ic_store(d1, Setting, kZero / kZero);\n}\n"
	assembly := compileNaNPath(t, "nanpayload.c", src)

	housing, _, actuator := devicePair(t)
	housedChip(t, assembly, housing)
	runProgram(t, housing, assembly)

	if got := math.Float64bits(logicValue(t, actuator, "Setting")); got != want {
		t.Errorf("d1 Setting holds %016x, want %016x, the pattern a zero divided by zero has\n%s",
			got, want, assembly)
	}
}
