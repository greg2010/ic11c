package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/corpus"
	"github.com/greg2010/ic11c/internal/emit"
	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/isa"
)

// overLimitDiagnostic is what the size report writes beside a program past
// one of its budgets, read by several harnesses to tell such a program
// apart from one the compiler would not build, which leaves the same status.
const overLimitDiagnostic = "over limit: "

// fixtures is where the corpus is checked in. Compiling it here is what
// makes the pipeline end to end rather than a chain of unit tests. It is
// resolved once by locateCorpus, which the test binary runs before any case does.
var fixtures string

// locateCorpus resolves [fixtures], and is called from TestMain so that a
// checkout the corpus cannot be found in stops the binary rather than leaving
// every case to glob a directory that is not there.
func locateCorpus() error {
	dir, err := corpus.Dir()
	if err != nil {
		return err
	}
	fixtures = dir
	return nil
}

// corpusFixtures names every program in the corpus. It globs rather than
// listing, so a program added to the directory joins every harness at
// once. The count is held to what the build captured, since a glob that
// stopped matching would leave every harness running over nothing, which passes.
func corpusFixtures(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(fixtures, "*.c"))
	if err != nil {
		t.Fatalf("globbing %s: %v", fixtures, err)
	}
	programs, err := corpus.Programs()
	if err != nil {
		t.Fatalf("loading the corpus: %v", err)
	}
	if len(paths) != len(programs) {
		t.Fatalf("%s holds %d programs and the build captured %d", fixtures, len(paths), len(programs))
	}
	names := make([]string, len(paths))
	for i, path := range paths {
		names[i] = filepath.Base(path)
	}
	return names
}

// run drives the command the way a shell would, with both streams captured
// so nothing touches the process globals. It calls the command directly
// rather than through execute, which turns a panic into a status that
// would say nothing about which stage failed.
func run(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	cmd := rootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	// A variadic call with nothing after t passes nil, and cobra reads the
	// process arguments for a nil slice: run(t) would compile whatever the test
	// binary was invoked with rather than nothing at all.
	if args == nil {
		args = []string{}
	}
	cmd.SetArgs(args)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ic11c %s panicked: %v\n%s\n%s",
				strings.Join(args, " "), r, debug.Stack(), errOut.String())
		}
	}()
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

// write puts src in a file named after the test, so a diagnostic quotes a path
// that exists.
func write(t *testing.T, name, src string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return path
}

// assemblyLines splits emitted assembly, rejecting the blank lines and comments
// the target forbids: each would cost bytes and an execution slot.
func assemblyLines(t *testing.T, text string) []string {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			t.Errorf("line %d is blank, which counts against both the line limit and the tick budget", i)
		}
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			t.Errorf("line %d is a comment, which costs bytes and an execution slot", i)
		}
	}
	return lines
}

// checkAssembly splits emitted assembly and holds every line to what the
// chip's own assembler accepts, returning the lines for a caller with more
// to ask of them. --readable is included, since that form only adds a
// trailing comment the chip cuts before reading the line.
func checkAssembly(t *testing.T, text string) []string {
	t.Helper()
	lines := assemblyLines(t, text)
	for i, line := range lines {
		checkLine(t, i, line, len(lines))
	}
	return lines
}

// TestCompileFixture is the end-to-end case: MicroC source in, IC10 assembly
// out, checked against what the chip's own assembler would accept.
func TestCompileFixture(t *testing.T) {
	stdout, stderr, err := run(t, filepath.Join(fixtures, "thermostat.c"))
	if err != nil {
		t.Fatalf("compiling thermostat.c: %v\n%s", err, stderr)
	}

	if len(checkAssembly(t, stdout)) == 0 {
		t.Fatalf("no assembly was emitted")
	}
	if !strings.Contains(stderr, "program: ") {
		t.Errorf("no byte budget report was printed:\n%s", stderr)
	}
	t.Logf("--- thermostat.c ---\n%s\n%s", stdout, stderr)
}

// numericOperand reports whether the chip's operand parser reads text as a
// number rather than as a name. A number is what the exponent check is about.
func numericOperand(text string) bool {
	if text == "" {
		return false
	}
	switch c := text[0]; {
	case c >= '0' && c <= '9', c == '-', c == '+', c == '.':
		return true
	default:
		return text == "epsilon"
	}
}

// checkLine holds one emitted line to what the target documents: a known
// mnemonic that is not on the never-emit list, a branch target inside the
// program, no exponential notation, and nothing wider than the editor
// accepts. The line is cut at its first '#', as ProgrammableChip does.
func checkLine(t *testing.T, index int, line string, total int) {
	t.Helper()
	if code, _, commented := strings.Cut(line, "#"); commented {
		line = strings.TrimRight(code, " ")
	}
	if len(line) > emit.MaxLineLength {
		t.Errorf("line %d holds %d characters of instruction, over the %d character limit", index, len(line), emit.MaxLineLength)
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return
	}
	instruction, ok := ic10.LookupInstruction(fields[0])
	if !ok {
		t.Errorf("line %d uses %q, which is not a mnemonic the chip accepts: %s", index, fields[0], line)
		return
	}
	if reason, bad := ic10.Unemittable(instruction.Opcode); bad {
		t.Errorf("line %d emits %s, which must never be emitted: %s", index, fields[0], reason)
	}
	if got := len(fields) - 1; got != len(instruction.Operands) {
		t.Errorf("line %d gives %s %d operands, and the chip's assembler enforces %d: %s",
			index, fields[0], got, len(instruction.Operands), line)
	}
	for _, operand := range fields[1:] {
		// A machine name is not a number, and several of them hold an e. The
		// check is for a formatter reaching for an exponent, so it applies to
		// what the chip's number parser will read as a number.
		if !numericOperand(operand) {
			continue
		}
		if strings.ContainsAny(operand, "eE") && operand != "epsilon" {
			t.Errorf("line %d writes %q, and the chip's number parser admits no exponent: %s", index, operand, line)
		}
	}
	// A jump target is an absolute line number this program has, or the one
	// past the end that ends it. j ra is the exception: it reaches a line
	// the caller put in a register, which no static check can name.
	//exhaustive:ignore
	switch instruction.Opcode {
	case isa.OpJ, isa.OpJal, isa.OpBeq, isa.OpBne, isa.OpBlt, isa.OpBgt, isa.OpBle, isa.OpBge,
		isa.OpBeqz, isa.OpBnez, isa.OpBltz, isa.OpBgtz, isa.OpBlez, isa.OpBgez:
		target := fields[len(fields)-1]
		if _, isRegister := ic10.ParseRegister(target); isRegister {
			return
		}
		number, convErr := strconv.Atoi(target)
		if convErr != nil {
			t.Errorf("line %d branches to %q, which is not a line number: %s", index, target, line)
			return
		}
		if number < 0 || number > total {
			t.Errorf("line %d branches to line %d, and the program has %d lines: %s", index, number, total, line)
		}
	default:
	}
}

// overBudgetSource is a program past the 128 line limit. Each store is a side
// effect on a device, so nothing the optimizer does can collapse the sequence.
func overBudgetSource(stores int) string {
	var b strings.Builder
	b.WriteString("void main(void) {\n")
	for i := range stores {
		fmt.Fprintf(&b, "\t__ic_store(d0, Setting, %d);\n", i+1)
	}
	b.WriteString("}\n")
	return b.String()
}

// TestOverBudgetProgramFails covers the shape a build script takes: the
// assembly goes to a file by redirection and only the exit status says
// whether what landed there is pasteable. Every mode is held to its own
// report, since --readable and --no-optimize each emit a differently sized text.
func TestOverBudgetProgramFails(t *testing.T) {
	path := write(t, "over_budget.c", overBudgetSource(140))
	tests := []struct {
		name string
		args []string
	}{
		{name: "default", args: []string{path}},
		{name: "readable", args: []string{"--readable", path}},
		{name: "numeric", args: []string{"--numeric", path}},
		{name: "no-optimize", args: []string{"--no-optimize", path}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, err := run(t, tt.args...)
			if err == nil {
				t.Errorf("compiling a program over the line limit succeeded, so a redirection would ship it")
			}
			if stdout != "" {
				t.Errorf("the output stream holds %q under a failing status, so a redirection leaves a file the in-game editor will not take and nothing says so", stdout)
			}
			if !strings.Contains(stderr, overLimitDiagnostic) {
				t.Errorf("the report did not name the limit that was exceeded:\n%s", stderr)
			}
			if spend := sizeReport(t, stderr); spend.Lines <= emit.MaxLines {
				t.Errorf("the report counts %d lines, inside the %d line limit, so this program does not reach the case it is here for", spend.Lines, emit.MaxLines)
			}
		})
	}
}

// TestWithinBudgetProgramSucceeds is the other half: the status must not turn
// on the size of a program that fits.
func TestWithinBudgetProgramSucceeds(t *testing.T) {
	path := write(t, "within_budget.c", overBudgetSource(4))
	stdout, stderr, err := run(t, path)
	if err != nil {
		t.Fatalf("compiling a program inside every limit: %v\n%s", err, stderr)
	}
	if strings.Contains(stderr, overLimitDiagnostic) {
		t.Fatalf("the report names a violation for a program that fits:\n%s", stderr)
	}
	if len(checkAssembly(t, stdout)) == 0 {
		t.Errorf("no assembly was emitted")
	}
}

// TestEmptyProgramWritesNoAssembly covers the one program with no line to
// emit: the output stream must be left untouched rather than write the
// blank line a bare newline is, since a player pastes it straight into an
// IC Editor.
func TestEmptyProgramWritesNoAssembly(t *testing.T) {
	path := write(t, "empty.c", "void main(void) {}")
	stdout, stderr, err := run(t, path)
	if err != nil {
		t.Fatalf("compiling an empty program: %v\n%s", err, stderr)
	}
	if stdout != "" {
		t.Errorf("the output stream holds %q for a program that emitted no instruction, want nothing at all", stdout)
	}
	if !strings.Contains(stderr, "0 of 128 lines") {
		t.Errorf("the report does not say the program is empty:\n%s", stderr)
	}
}

// TestReportNamesTheStackHeadroom covers the budget nothing else surfaces. A
// call frame and the data region share one array with no hardware protection,
// so a frame reaching a global overwrites it and nothing traps; the size report
// is where a program close to that has to be able to see it.
func TestReportNamesTheStackHeadroom(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		wantText []string
	}{
		{
			name:     "no data region and no call",
			src:      "void main(void) { __ic_store(d0, Setting, 1); }",
			wantText: []string{"data region: 0 of 512 slots", "makes no call"},
		},
		{
			name: "an array leaving a recursive program four slots to recurse in",
			src: "double a[508];\n" +
				"long long gcd(long long x, long long y) { if (y == 0) return x; return gcd(y, x % y); }\n" +
				"void main(void) {\n" +
				"  for (long long i = 0; i < 508; i++) a[i] = i;\n" +
				"  __ic_store(d0, Setting, a[507] + gcd(48, 18));\n" +
				"}\n",
			wantText: []string{"data region: 508 of 512 slots", "4 slots for call frames"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, stderr, err := run(t, write(t, "slots.c", tt.src))
			if err != nil {
				t.Fatalf("compiling: %v\n%s", err, stderr)
			}
			for _, want := range tt.wantText {
				if !strings.Contains(stderr, want) {
					t.Errorf("the report does not mention %q:\n%s", want, stderr)
				}
			}
		})
	}
}

// TestCompileAllFixtures runs the whole corpus through the shipped pipeline and
// holds every line to what the chip's own assembler accepts. The size report is
// logged because the byte budget is the limit that binds.
func TestCompileAllFixtures(t *testing.T) {
	for _, name := range corpusFixtures(t) {
		t.Run(name, func(t *testing.T) {
			stdout, stderr, err := run(t, filepath.Join(fixtures, name))
			if err != nil {
				t.Fatalf("compiling: %v\n%s", err, stderr)
			}
			if strings.TrimSpace(stdout) == "" {
				t.Fatalf("no assembly was emitted")
			}
			checkAssembly(t, stdout)
			spend := sizeReport(t, stderr)
			t.Logf("%s: %d bytes over %d lines, widest %d", name, spend.Bytes, spend.Lines, spend.LongestLine)
		})
	}
}

// fixtureTicks bounds a fixture run. It is far more ticks than any fixture's
// loop needs to come back round, so a program still inside its own
// instructions at the end of one is looping rather than merely slow.
const fixtureTicks = 200

// compileSource compiles one file the command has to accept and returns
// assembly the chip's own rules accept. A case reading the size report
// beside the assembly, or expecting a refusal, runs the command itself;
// everything else comes through here.
func compileSource(t *testing.T, source string, args ...string) string {
	t.Helper()
	stdout, stderr, err := run(t, append([]string{source}, args...)...)
	if err != nil {
		t.Fatalf("compiling %s: %v\n%s", filepath.Base(source), err, stderr)
	}
	assembly := strings.TrimSuffix(stdout, "\n")
	checkAssembly(t, assembly)
	return assembly
}

// compileDirect compiles one file through the pipeline rather than through
// the command, under one configuration. Several configurations reach no
// command-line flag, and the command withholds the assembly of any
// program past an in-game editor limit, which most of the generated corpus is.
func compileDirect(t *testing.T, path string, opts options) (emit.Output, error) {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	output, diags, err := compile(t.Context(), path, string(src), opts)
	if err != nil {
		return emit.Output{}, fmt.Errorf("compiling %s: %w", path, err)
	}
	if diags.HasErrors() {
		// Every diagnostic rather than the summary a list renders as an error.
		// A refusal is recognised by the marker its message carries, the command
		// line prints all of them for that reason, and the one that rejects the
		// program is not always the first.
		return emit.Output{}, fmt.Errorf("compiling %s: %s", path, diags.String())
	}
	checkAssembly(t, output.Text)
	return output, nil
}

// pipelineCases are the two ways the command compiles one source. The
// unoptimized form is what instruction selection produced on its own, so it is
// the reference a rewrite the optimizer made has to agree with.
var pipelineCases = []struct {
	name string
	args []string
}{
	{name: "optimized"},
	{name: "unoptimized", args: []string{unoptimizedFlag}},
}

// compileFixture compiles one file from the corpus.
func compileFixture(t *testing.T, name string, args ...string) string {
	t.Helper()
	return compileSource(t, filepath.Join(fixtures, name), args...)
}

// TestFixturesRunEveryTick holds the corpus to the control loop each
// program's comment describes: the program stops when main returns, so a
// fixture without its own loop reads one sample, acts on it once, and is
// dead for the rest of the chip's life.
func TestFixturesRunEveryTick(t *testing.T) {
	for _, name := range corpusFixtures(t) {
		t.Run(name, func(t *testing.T) {
			assembly := compileFixture(t, name)

			// Every pin is populated, because a fixture reading a pin the
			// housing left empty faults rather than reading zero.
			housing := newChipRun(t)
			housing.populate(t, ic10.NumDevicePins)
			if stimulus := fixtureWorld[name]; stimulus != nil {
				stimulus(t, housing.harness, 0)
			}

			runTicks(t, housedChip(t, assembly, housing), fixtureTicks, assembly)
		})
	}
}

// behaviourallyAsserted names every fixture a test states an expected
// answer for, against the test that states it. Nothing derives it, so what
// holds it to reality is that the test takes its fixture from here, and
// both directions of drift fail.
var behaviourallyAsserted = map[string]string{
	"thermostat.c": "TestThermostatHoldsTheSetpoint",
}

// behaviouralFixture is the fixture the calling test asserts about.
func behaviouralFixture(t *testing.T) string {
	t.Helper()
	for fixture, test := range behaviourallyAsserted {
		if test == t.Name() {
			return fixture
		}
	}
	t.Fatalf("%s asserts what a fixture computes and behaviourallyAsserted does not name it, so the documents state one more fixture as unasserted than there is", t.Name())
	return ""
}

// TestThermostatHoldsTheSetpoint drives thermostat.c across the setpoint
// and back, which a single pass cannot exercise: with one reading there is
// nothing for the hysteresis band to hold. The band steps assert the
// heater keeps its last state, not any particular one.
func TestThermostatHoldsTheSetpoint(t *testing.T) {
	// Enough ticks for the loop to come round several times, so a step that
	// changes the answer has changed it for good rather than mid-iteration.
	const ticksPerStep = 8

	assembly := compileFixture(t, behaviouralFixture(t))
	housing, sensor, heater := devicePair(t)
	housedChip(t, assembly, housing)

	steps := []struct {
		name        string
		temperature float64
		want        float64
	}{
		{name: "below the band", temperature: 250, want: 1},
		{name: "inside the band, holding on", temperature: 293, want: 1},
		{name: "above the band", temperature: 350, want: 0},
		{name: "inside the band, holding off", temperature: 293, want: 0},
		{name: "below the band again", temperature: 250, want: 1},
	}

	for _, step := range steps {
		setLogic(t, sensor, "Temperature", step.temperature)
		runTicks(t, housing, ticksPerStep, assembly)
		if got := logicValue(t, heater, "On"); got != step.want {
			t.Errorf("%s: at %v kelvin the heater is %v, want %v:\n%s",
				step.name, step.temperature, got, step.want, assembly)
		}
	}
}

// constructShape is what one variant of a construct's assembly must and
// must not contain. A fragment names whole fields, matching a line when
// its fields appear as a contiguous run: a substring match instead cannot
// tell beq from beqz or or from xor.
type constructShape struct {
	// present are fragments the variant emits.
	present []string
	// absent are fragments it does not, which is how a case states a claim
	// about a shape that is not there. Execution reaches what a program does,
	// so an absence is what only the text can hold.
	absent []string
}

// lineMatches reports whether fragment's fields are a contiguous run of line's.
func lineMatches(fragment, line string) bool {
	want := strings.Fields(fragment)
	if len(want) == 0 {
		return false
	}
	fields := strings.Fields(line)
	for i := 0; i+len(want) <= len(fields); i++ {
		if slices.Equal(fields[i:i+len(want)], want) {
			return true
		}
	}
	return false
}

// emittedMnemonics names what the lines emitted, in the order they first
// emitted each, so a fragment that matched nothing is reported against what was
// there. A fragment naming a mnemonic one character off from an emitted one is
// the mistake this turns into a diagnosis.
func emittedMnemonics(lines []string) []string {
	var seen []string
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) > 0 && !slices.Contains(seen, fields[0]) {
			seen = append(seen, fields[0])
		}
	}
	return seen
}

// checkShape holds one variant's assembly to what its case claims of it.
func checkShape(t *testing.T, shape constructShape, lines []string, assembly string) {
	t.Helper()
	for _, want := range shape.present {
		if !slices.ContainsFunc(lines, func(line string) bool { return lineMatches(want, line) }) {
			t.Errorf("no line of the assembly carries %q; its mnemonics are %v:\n%s",
				want, emittedMnemonics(lines), assembly)
		}
	}
	for _, unwanted := range shape.absent {
		for i, line := range lines {
			if lineMatches(unwanted, line) {
				t.Errorf("line %d is %q, and the case holds the program to emitting no %q:\n%s",
					i, line, unwanted, assembly)
			}
		}
	}
}

// TestCompileConstructs walks the control flow the backend does lower,
// checking each one reaches assembly rather than only IR. Both paths are
// compiled: unoptimized is the coverage the cases were written for, and
// optimized is what ships and differs wherever the optimizer has a cheaper form.
func TestCompileConstructs(t *testing.T) {
	cases := []struct {
		name string
		src  string
		// unoptimized is the shape selection produces for the construct.
		unoptimized constructShape
		// optimized is the shape the shipped pipeline produces.
		optimized constructShape
	}{
		{
			name:        "if and else",
			src:         "void main(void) { long long t = (long long)__ic_load(d0, Temperature); long long r; if (t > 0) { r = 1; } else { r = 2; } __ic_store(d1, Setting, r); }",
			unoptimized: constructShape{present: []string{"bgtz"}},
			// A branch over two constants is three lines and a layout
			// constraint; the machine's select is one line. The branch going is
			// the whole of the saving, so the absence is what the case holds.
			optimized: constructShape{
				present: []string{"select"},
				absent:  []string{"bgtz", "j"},
			},
		},
		{
			name:        "while",
			src:         "void main(void) { long long t = (long long)__ic_load(d0, Temperature); while (t > 0) { t = (long long)__ic_load(d0, Temperature); __ic_yield(); } __ic_store(d1, Setting, t); }",
			unoptimized: constructShape{present: []string{"bgtz", "j"}},
			optimized:   constructShape{present: []string{"bgtz", "yield"}},
		},
		{
			name:        "do while",
			src:         "void main(void) { long long n = 0; do { n = n + (long long)__ic_load(d0, Temperature); __ic_yield(); } while (n < 100); __ic_store(d1, Setting, n); }",
			unoptimized: constructShape{present: []string{"add", "blt"}},
			optimized:   constructShape{present: []string{"add", "blt"}},
		},
		{
			name: "for with continue and break",
			src: `long long g;
void main(void) {
    for (long long i = 0; i < 8; i++) {
        if (i == 3) { continue; }
        if (i == 6) { break; }
        g = g + i;
    }
    __ic_store(d1, Setting, g);
}`,
			unoptimized: constructShape{present: []string{"bne", "add", "j"}},
			optimized:   constructShape{present: []string{"beq", "add", "j"}},
		},
		{
			name: "switch",
			src: `void main(void) {
    long long m = (long long)__ic_load(d0, Setting);
    long long r;
    switch (m) {
    case 1: r = 10; break;
    case 2: r = 20; break;
    default: r = 30; break;
    }
    __ic_store(d1, Setting, r);
}`,
			unoptimized: constructShape{present: []string{"beq", "j"}},
			optimized:   constructShape{present: []string{"seq", "select"}},
		},
		{
			// Three consecutive cases are what a stock -Oz pipeline turns
			// into a lookup table poked into the data region; the shipped
			// pipeline stops before that pass, so the optimized shape is
			// held to touching the data region not at all.
			name: "switch over consecutive cases",
			src: `void main(void) {
    long long m = (long long)__ic_load(d0, Setting);
    long long r;
    switch (m) {
    case 0: r = 1; break;
    case 1: r = 4; break;
    case 2: r = 9; break;
    default: r = 0; break;
    }
    __ic_store(d1, Setting, r);
}`,
			// Selection spills to the data region at a literal slot, so only
			// the optimized shape can name reading it as the table it would be.
			unoptimized: constructShape{present: []string{"beqz", "beq", "j"}},
			optimized: constructShape{
				present: []string{"bnez", "move", "seq", "select"},
				absent:  []string{"poke", "get"},
			},
		},
		{
			name:        "truncating division and C remainder",
			src:         "void main(void) { long long t = (long long)__ic_load(d0, Temperature); __ic_store(d1, Setting, t / 3); __ic_store(d2, Setting, t % 3); }",
			unoptimized: constructShape{present: []string{"div", "trunc", "mul", "sub"}},
			optimized:   constructShape{present: []string{"div", "trunc", "mul", "sub"}},
		},
		{
			name:        "bitwise operators",
			src:         "void main(void) { long long t = (long long)__ic_load(d0, Temperature); long long a = (t & 3) | (t ^ 5); __ic_store(d1, Setting, ~a); __ic_store(d2, Setting, (a << 2) + (a >> 1)); }",
			unoptimized: constructShape{present: []string{"and", "or", "xor", "not", "sll", "sra"}},
			optimized:   constructShape{present: []string{"and", "or", "xor", "not", "sll", "sra"}},
		},
		{
			name: "an inlined call",
			src: `long long plus7(long long x) { return x + 7; }
void main(void) { __ic_store(d1, Setting, plus7((long long)__ic_load(d0, Temperature))); }`,
			unoptimized: constructShape{present: []string{"add"}},
			optimized:   constructShape{present: []string{"add"}},
		},
		{
			name:        "device intrinsics",
			src:         "void main(void) { long long t = (long long)__ic_load(d0, Temperature); __ic_store(d1, On, t); __ic_yield(); }",
			unoptimized: constructShape{present: []string{"l", "trunc", "s d1", "yield"}},
			optimized:   constructShape{present: []string{"l", "trunc", "s d1", "yield"}},
		},
		{
			name:        "the conditional operator",
			src:         "void main(void) { long long t = (long long)__ic_load(d0, Temperature); __ic_store(d1, Setting, t > 0 ? 1 : 2); }",
			unoptimized: constructShape{present: []string{"move"}},
			optimized: constructShape{
				present: []string{"select"},
				absent:  []string{"move"},
			},
		},
		{
			name:        "short circuit operators become a branch chain",
			src:         "void main(void) { long long t = (long long)__ic_load(d0, Temperature); long long p = (long long)__ic_load(d0, Pressure); if (t > 0 && p < 8) { __ic_store(d1, On, 1); } }",
			unoptimized: constructShape{present: []string{"bgtz", "blt"}},
			optimized:   constructShape{present: []string{"sgtz", "slt"}},
		},
		{
			// Every fraction reaches the chip as a decimal expansion: its own
			// number parser reads no exponent, so a shortest-round-trip
			// formatter reaching for one would emit a line that fails to
			// assemble.
			name:        "a fractional literal emits as a decimal expansion",
			src:         "void main(void) { double t = __ic_load(d0, Temperature); __ic_store(d1, Setting, t * 0.001 + 293.15); }",
			unoptimized: constructShape{present: []string{"0.001", "293.15"}},
			optimized:   constructShape{present: []string{"0.001", "293.15"}},
		},
		{
			name:        "a fractional comparison keeps the value the source wrote",
			src:         "void main(void) { double t = __ic_load(d0, Temperature); __ic_store(d1, On, t >= 300.5); }",
			unoptimized: constructShape{present: []string{"300.5"}},
			optimized:   constructShape{present: []string{"300.5"}},
		},
		{
			// The machine's own value for the constant, which is a float
			// precision literal widened to a double rather than pi/180.
			name:        "a machine constant emits as its name",
			src:         "void main(void) { double t = __ic_load(d0, Temperature); __ic_store(d1, Setting, t * deg2rad); }",
			unoptimized: constructShape{present: []string{"deg2rad"}},
			optimized:   constructShape{present: []string{"deg2rad"}},
		},
		{
			// The second instruction integer division takes is the truncation
			// of the quotient, which is what its absence here names.
			name: "double division is one instruction where integer division is two",
			src:  "void main(void) { double t = __ic_load(d0, Temperature); __ic_store(d1, Setting, t / 3.0); }",
			unoptimized: constructShape{
				present: []string{"div"},
				absent:  []string{"trunc"},
			},
			optimized: constructShape{
				present: []string{"div"},
				absent:  []string{"trunc"},
			},
		},
		{
			name:        "the NaN test and the random draw reach their instructions",
			src:         "void main(void) { double t = __ic_load(d0, Temperature); __ic_store(d1, On, __ic_isnan(t)); __ic_store(d1, Setting, __ic_rand()); }",
			unoptimized: constructShape{present: []string{"snan", "rand"}},
			optimized:   constructShape{present: []string{"snan", "rand"}},
		},
		{
			name:        "a dev names the pin at every use",
			src:         "const dev sensor = d0;\nconstexpr dev remote = d4;\nvoid main(void) { __ic_store(remote, Setting, __ic_load(sensor, Temperature)); }",
			unoptimized: constructShape{present: []string{"l r0 d0", "s d4"}},
			optimized:   constructShape{present: []string{"l r0 d0", "s d4"}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := write(t, "case.c", tc.src)
			variants := []struct {
				name string
				args []string
				want constructShape
			}{
				{name: "optimized", want: tc.optimized},
				{name: "unoptimized", args: []string{unoptimizedFlag}, want: tc.unoptimized},
			}

			for _, variant := range variants {
				t.Run(variant.name, func(t *testing.T) {
					assembly := compileSource(t, path, variant.args...)
					checkShape(t, variant.want, assemblyLines(t, assembly), assembly)
				})
			}
		})
	}
}

// TestCompileEmitsNoIdentityMoves holds the shipped output to spending
// nothing on a copy that puts a register back where it already was. A phi
// becomes copies over virtual registers, and allocation frequently gives a
// copy's two ends the same physical register — common rather than exotic.
func TestCompileEmitsNoIdentityMoves(t *testing.T) {
	recursive := write(t, "ack.c", `long long ack(long long m, long long n) {
    if (m == 0) { return n + 1; }
    if (n == 0) { return ack(m - 1, 1); }
    return ack(m - 1, ack(m, n - 1));
}
void main(void) {
    __ic_store(d1, Setting, ack((long long)__ic_load(d0, Setting), 2));
}`)

	cases := []struct{ name, path string }{{name: "a recursive function", path: recursive}}
	for _, name := range corpusFixtures(t) {
		cases = append(cases, struct{ name, path string }{name: name, path: filepath.Join(fixtures, name)})
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assembly := compileSource(t, tc.path)
			for i, line := range assemblyLines(t, assembly) {
				fields := strings.Fields(line)
				if len(fields) == 3 && fields[0] == "move" && fields[1] == fields[2] {
					t.Errorf("line %d is %q, which computes nothing:\n%s", i, line, assembly)
				}
			}
		})
	}
}

// TestCompileSwitchArmsSharingOneBody covers the one fallthrough MicroC
// permits: an empty case arm stacks its label onto the arm below, so the
// switch names one destination twice. It runs at every tag the switch
// distinguishes rather than only being inspected.
func TestCompileSwitchArmsSharingOneBody(t *testing.T) {
	path := write(t, "switch.c", `void main(void) {
    long long t = (long long)__ic_load(d0, Setting);
    long long y;
    switch (t) {
    case 1:
    case 2: y = 5; break;
    case 7: y = 9; break;
    default: y = 1;
    }
    __ic_store(d1, Setting, y);
}`)

	inputs := []struct {
		setting int64
		want    float64
	}{
		{setting: 0, want: 1},
		{setting: 1, want: 5},
		{setting: 2, want: 5},
		{setting: 3, want: 1},
		{setting: 7, want: 9},
		{setting: 8, want: 1},
	}

	for _, tc := range pipelineCases {
		t.Run(tc.name, func(t *testing.T) {
			assembly := compileSource(t, path, tc.args...)
			for _, input := range inputs {
				out := runWithSetting(t, assembly, input.setting)
				if got := logicValue(t, out, "Setting"); got != input.want {
					t.Errorf("with Setting %d the program answered %v, want %v:\n%s", input.setting, got, input.want, assembly)
				}
			}
		})
	}
}

// TestCompileARangeCheck is the idiom that made the optimizer a
// regression: InstCombine folds a two-sided signed range test into one
// unsigned comparison, and the machine compares doubles and has no
// unsigned form.
func TestCompileARangeCheck(t *testing.T) {
	const bound = 8
	path := write(t, "range.c", fmt.Sprintf(`void main(void) {
    long long t = (long long)__ic_load(d0, Setting);
    if (t >= 0 && t < %d) { __ic_store(d1, On, 1); } else { __ic_store(d1, On, 0); }
}`, bound))

	// The ends of the value model, then either side of the bound.
	inputs := []int64{-(1 << 53), -1, 0, bound - 1, bound, bound + 1, 1 << 53}

	for _, tc := range pipelineCases {
		t.Run(tc.name, func(t *testing.T) {
			assembly := compileSource(t, path, tc.args...)

			for _, input := range inputs {
				housing, in, out := devicePair(t)
				setLogic(t, in, "Setting", float64(input))
				runOnChip(t, assembly, housing)

				want := 0.0
				if input >= 0 && input < bound {
					want = 1
				}
				if got := logicValue(t, out, "On"); got != want {
					t.Errorf("with Setting %d the program set On to %v, want %v:\n%s", input, got, want, assembly)
				}
			}
		})
	}
}

// TestCompileADivisionUnderASignCheck is the division the optimizer
// rewrites: InstCombine turns a signed division into an unsigned one once
// a dominating sign check proves both operands non-negative. The
// unoptimized pipeline is the reference, since it never performs the rewrite.
func TestCompileADivisionUnderASignCheck(t *testing.T) {
	const divisor = 7
	src := fmt.Sprintf(`void main(void) {
    long long x = (long long)__ic_load(d0, Setting);
    if (x < 0) {
        return;
    }
    __ic_store(d1, Setting, x / %d);
    __ic_store(d1, Mode, x %% %d);
}`, divisor, divisor)

	inputs := []int64{0, 1, divisor - 1, divisor, divisor + 1, 23, 1 << 53}

	forEachPipeline(t, src, func(t *testing.T, assembly string) {
		for _, input := range inputs {
			out := runWithSetting(t, assembly, input)
			if got, want := logicValue(t, out, "Setting"), float64(input/divisor); got != want {
				t.Errorf("%d / %d gave %v, want %v:\n%s", input, divisor, got, want, assembly)
			}
			if got, want := logicValue(t, out, "Mode"), float64(input%divisor); got != want {
				t.Errorf("%d %% %d gave %v, want %v:\n%s", input, divisor, got, want, assembly)
			}
		}
	})
}

// TestCompileABoolFromABitwiseTest is the narrowing the optimizer forms:
// InstCombine folds a bool's narrowing back into the bitwise test it came
// from only where the bool is the whole expression, so a bool also read by
// another bool operation keeps the narrowing and the backend must select it.
func TestCompileABoolFromABitwiseTest(t *testing.T) {
	src := `void main(void) {
    long long x = (long long)__ic_load(d0, Setting);
    __ic_store(d1, Setting, ((x & 1) != 0) == (x > 3));
}`

	inputs := []int64{0, 1, 2, 3, 4, 5, 1024, 1025, -1, -2, -3}

	forEachPipeline(t, src, func(t *testing.T, assembly string) {
		for _, input := range inputs {
			want := 0.0
			if (input&1 != 0) == (input > 3) {
				want = 1
			}
			out := runWithSetting(t, assembly, input)
			if got := logicValue(t, out, "Setting"); got != want {
				t.Errorf("with Setting %d the program stored %v, want %v:\n%s", input, got, want, assembly)
			}
		}
	})
}

// TestCompileArrayAddressing executes what the address computation
// produced: a wrong base or stride assembles and passes every mnemonic
// check, then reads the wrong slot, so the boundaries are run.
func TestCompileArrayAddressing(t *testing.T) {
	const src = `long long a[4] = {10, 20, 30, 40};
void main(void) {
    long long i = (long long)__ic_load(d0, Setting);
    __ic_store(d1, Setting, a[i]);
    __ic_store(d1, Temperature, a[0]);
    __ic_store(d1, Pressure, a[3]);
}`
	elements := []float64{10, 20, 30, 40}

	forEachPipeline(t, src, func(t *testing.T, assembly string) {
		for index, want := range elements {
			out := runWithSetting(t, assembly, int64(index))
			if got := logicValue(t, out, "Setting"); got != want {
				t.Errorf("a[%d] read %v, want %v:\n%s", index, got, want, assembly)
			}
			if got := logicValue(t, out, "Temperature"); got != elements[0] {
				t.Errorf("a[0] read %v, want %v:\n%s", got, elements[0], assembly)
			}
			if got := logicValue(t, out, "Pressure"); got != elements[3] {
				t.Errorf("a[3] read %v, want %v:\n%s", got, elements[3], assembly)
			}
		}
	})
}

// TestCompileArrayIsWrittenAtEveryIndex is the store side of the same
// computation: reading the right slot proves nothing if the write landed
// somewhere else.
func TestCompileArrayIsWrittenAtEveryIndex(t *testing.T) {
	const src = `long long a[4];
long long seen;
void main(void) {
    long long i = (long long)__ic_load(d0, Setting);
    a[0] = 1;
    a[3] = 8;
    a[i] = 100;
    for (long long k = 0; k < 4; k++) { seen = seen + a[k]; }
    __ic_store(d1, Setting, seen);
    __ic_store(d1, Temperature, a[i]);
}`

	forEachPipeline(t, src, func(t *testing.T, assembly string) {
		// Writing 100 over one index leaves the other three holding what the
		// two literal stores and the zeroing prologue put there.
		totals := []float64{100 + 0 + 0 + 8, 1 + 100 + 0 + 8, 1 + 0 + 100 + 8, 1 + 0 + 0 + 100}
		for index, want := range totals {
			out := runWithSetting(t, assembly, int64(index))
			if got := logicValue(t, out, "Setting"); got != want {
				t.Errorf("with a[%d] written the array sums to %v, want %v:\n%s", index, got, want, assembly)
			}
			if got := logicValue(t, out, "Temperature"); got != 100 {
				t.Errorf("a[%d] read back %v, want 100:\n%s", index, got, assembly)
			}
		}
	})
}

// TestCompilePointerAddressing runs the address of a local and of a global
// through a pointer parameter. Both resolve to one object at compile time,
// which is what makes them a slot index at run time.
func TestCompilePointerAddressing(t *testing.T) {
	const src = `long long g;
void bump(long long *p, long long by) { *p = *p + by; }
void main(void) {
    long long x = (long long)__ic_load(d0, Setting);
    bump(&x, 3);
    g = 5;
    bump(&g, 4);
    __ic_store(d1, Setting, x);
    __ic_store(d1, Temperature, g);
}`

	for _, input := range []int64{-7, 0, 1, 1 << 20} {
		assembly := compiled(t, "pointer.c", src)
		out := runWithSetting(t, assembly, input)
		if got, want := logicValue(t, out, "Setting"), float64(input+3); got != want {
			t.Errorf("the local read %v, want %v:\n%s", got, want, assembly)
		}
		if got := logicValue(t, out, "Temperature"); got != 9 {
			t.Errorf("the global read %v, want 9:\n%s", got, assembly)
		}
	}
}

// TestCompileArrayDecaysToAPointer passes an array name where a pointer is
// taken, which is the one conversion an array has.
func TestCompileArrayDecaysToAPointer(t *testing.T) {
	const src = `long long a[4] = {1, 2, 4, 8};
long long sumTo(long long *p, long long n) {
    long long total = 0;
    for (long long i = 0; i < n; i++) { total = total + p[i]; }
    return total;
}
void main(void) {
    long long n = (long long)__ic_load(d0, Setting);
    __ic_store(d1, Setting, sumTo(a, n));
    __ic_store(d1, Temperature, sumTo(a + 1, 2));
}`

	assembly := compiled(t, "decay.c", src)
	for n, want := range []float64{0, 1, 3, 7, 15} {
		out := runWithSetting(t, assembly, int64(n))
		if got := logicValue(t, out, "Setting"); got != want {
			t.Errorf("the first %d elements sum to %v, want %v:\n%s", n, got, want, assembly)
		}
		if got := logicValue(t, out, "Temperature"); got != 6 {
			t.Errorf("a+1 summed to %v, want 6:\n%s", got, assembly)
		}
	}
}

// TestCompilePointerArithmetic covers the operators that step a pointer rather
// than read through one: '++' on a pointer object, '+=' on a pointer and on
// what one points at, and the comparison of two pointers into one array.
func TestCompilePointerArithmetic(t *testing.T) {
	const src = `long long a[4] = {1, 2, 4, 8};
long long scan(long long *p, long long n) {
    long long total = 0;
    long long *q = p;
    for (long long i = 0; i < n; i++) {
        total += *q;
        q++;
    }
    return total;
}
void main(void) {
    long long n = (long long)__ic_load(d0, Setting);
    __ic_store(d1, Setting, scan(a, n));

    long long x = 5;
    long long *p = &x;
    *p += 3;
    (*p)++;
    __ic_store(d1, Temperature, x);

    long long *q = a;
    q += 2;
    __ic_store(d1, Pressure, *q + (q == a + 2 ? 100 : 0));
}`

	assembly := compiled(t, "arith.c", src)
	for n, want := range []float64{0, 1, 3, 7, 15} {
		out := runWithSetting(t, assembly, int64(n))
		if got := logicValue(t, out, "Setting"); got != want {
			t.Errorf("walking %d elements gave %v, want %v:\n%s", n, got, want, assembly)
		}
		if got := logicValue(t, out, "Temperature"); got != 9 {
			t.Errorf("stepping the pointee gave %v, want 9:\n%s", got, assembly)
		}
		if got := logicValue(t, out, "Pressure"); got != 104 {
			t.Errorf("a+2 read %v, want 104: the element is 4 and the two pointers are equal:\n%s", got, assembly)
		}
	}
}

// TestCompileBooleanComplement runs every combination of two bools through
// a device write. A bool is one bit of value in a whole register, and the
// machine's not complements the whole of it, so folding a complement into
// one leaves -1 where the program says 1 — invisible to a branch.
func TestCompileBooleanComplement(t *testing.T) {
	const src = `void main(void) {
    long long t = (long long)__ic_load(d0, Setting);
    long long u = (long long)__ic_load(d0, Temperature);
    bool a = t > 5;
    bool b = u > 7;
    __ic_store(d1, Setting, a == b);
    __ic_store(d1, Temperature, !a);
    __ic_store(d1, Pressure, a);
    __ic_store(d1, On, b);
}`

	cases := []struct {
		name       string
		first      float64
		second     float64
		same       float64
		complement float64
		a          float64
		b          float64
	}{
		{name: "neither holds", first: 0, second: 0, same: 1, complement: 1, a: 0, b: 0},
		{name: "the first holds", first: 10, second: 0, same: 0, complement: 0, a: 1, b: 0},
		{name: "the second holds", first: 0, second: 10, same: 0, complement: 1, a: 0, b: 1},
		{name: "both hold", first: 10, second: 10, same: 1, complement: 0, a: 1, b: 1},
	}

	forEachPipeline(t, src, func(t *testing.T, assembly string) {
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				housing, in, out := devicePair(t)
				setLogic(t, in, "Setting", tc.first)
				setLogic(t, in, "Temperature", tc.second)
				runOnChip(t, assembly, housing)

				for _, want := range []struct {
					property string
					value    float64
				}{
					{"Setting", tc.same},
					{"Temperature", tc.complement},
					{"Pressure", tc.a},
					{"On", tc.b},
				} {
					if got := logicValue(t, out, want.property); got != want.value {
						t.Errorf("%s read %v, want %v; a bool the program writes is 0 or 1:\n%s",
							want.property, got, want.value, assembly)
					}
				}
			})
		}
	})
}

// TestCompileANegatedBool runs a bool read as a signed number through two
// device writes: the machine holds a bool as 0 or 1, and its signed
// reading is 0 or -1, so the conversion is an instruction rather than
// nothing — the two writes are what makes a wrongly folded one observable.
func TestCompileANegatedBool(t *testing.T) {
	const src = `void main(void) {
    long long t = (long long)__ic_load(d0, Setting);
    bool b = t > 5;
    long long y = 0 - (long long)b;
    __ic_store(d1, Setting, y);
    __ic_store(d1, Temperature, y + 3);
}`

	cases := []struct {
		name    string
		input   int64
		negated float64
		shifted float64
	}{
		{name: "the comparison holds", input: 10, negated: -1, shifted: 2},
		{name: "the comparison fails", input: 0, negated: 0, shifted: 3},
	}

	forEachPipeline(t, src, func(t *testing.T, assembly string) {
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				out := runWithSetting(t, assembly, tc.input)
				if got := logicValue(t, out, "Setting"); got != tc.negated {
					t.Errorf("the negated bool read %v, want %v:\n%s", got, tc.negated, assembly)
				}
				if got := logicValue(t, out, "Temperature"); got != tc.shifted {
					t.Errorf("the negated bool plus three read %v, want %v:\n%s", got, tc.shifted, assembly)
				}
			})
		}
	})
}

// TestCompileDataSlotsDoNotCollide gives every object a distinct value and
// reads them all back. Two objects sharing a slot is a defect the byte count
// and the mnemonics both look clean under.
func TestCompileDataSlotsDoNotCollide(t *testing.T) {
	const src = `long long first[3] = {1, 2, 3};
long long between;
long long second[3] = {40, 50, 60};
long long last;
void main(void) {
    long long i = (long long)__ic_load(d0, Setting);
    between = between + 6;
    last = last + 7;
    __ic_store(d1, Setting, first[i]);
    __ic_store(d1, Temperature, second[i]);
    __ic_store(d1, Pressure, between * 100 + last);
}`

	forEachPipeline(t, src, func(t *testing.T, assembly string) {
		for index, want := range [][2]float64{{1, 40}, {2, 50}, {3, 60}} {
			out := runWithSetting(t, assembly, int64(index))
			if got := logicValue(t, out, "Setting"); got != want[0] {
				t.Errorf("first[%d] read %v, want %v:\n%s", index, got, want[0], assembly)
			}
			if got := logicValue(t, out, "Temperature"); got != want[1] {
				t.Errorf("second[%d] read %v, want %v:\n%s", index, got, want[1], assembly)
			}
			// The two scalars sit either side of an array, and the prologue
			// zeroed both, so each holds exactly what was added to it.
			if got := logicValue(t, out, "Pressure"); got != 607 {
				t.Errorf("the scalars around the arrays read %v, want 607:\n%s", got, assembly)
			}
		}
	})
}

// TestCompileZeroesTheDataRegion checks what a partial brace initializer
// rests on: the elements it does not supply read zero, via clr db rather
// than any write. The slots are seeded with a value no element holds, since
// chip state survives power loss and a freshly zeroed chip would pass regardless.
func TestCompileZeroesTheDataRegion(t *testing.T) {
	assembly := compiled(t, "partial.c", `void main(void) {
    long long a[8] = {11, 22};
    long long i = (long long)__ic_load(d0, Setting);
    __ic_store(d1, Setting, a[i]);
}`)

	for index, want := range []float64{11, 22, 0, 0, 0, 0, 0, 0} {
		housing, out := loadChip(t, assembly, int64(index))
		housing.setMemory(t, 999)

		runToEnd(t, housing, assembly)
		if got := logicValue(t, out, "Setting"); got != want {
			t.Errorf("element %d read %v, want %v:\n%s", index, got, want, assembly)
		}
	}
}

// TestCompilePointerDifference covers the distance between two pointers
// into one object, which the language counts in elements and LLVM counts
// in bytes: a pointer is a slot index at run time, so the stride is a
// compile-time division.
func TestCompilePointerDifference(t *testing.T) {
	const src = `long long a[8] = {0, 10, 20, 30, 40, 50, 60, 70};
void main(void) {
    long long i = (long long)__ic_load(d0, Setting);
    long long *p = a + i;
    long long *q = a + 2;
    __ic_store(d1, Setting, p - a);
    __ic_store(d1, Temperature, p - q);
    __ic_store(d1, Pressure, q - a);
    __ic_store(d1, On, &a[7] - p);
}`

	forEachPipeline(t, src, func(t *testing.T, assembly string) {
		for _, scaling := range []string{"div ", "mul ", "sll ", "srl ", "sra ", "mod "} {
			if strings.Contains(assembly, scaling) {
				t.Errorf("the difference emitted %q, so the element stride survived into the program:\n%s", scaling, assembly)
			}
		}
		for index := range 8 {
			out := runWithSetting(t, assembly, int64(index))
			for _, want := range []struct {
				property string
				value    float64
			}{
				{"Setting", float64(index)},
				{"Temperature", float64(index - 2)},
				{"Pressure", 2},
				{"On", float64(7 - index)},
			} {
				if got := logicValue(t, out, want.property); got != want.value {
					t.Errorf("at index %d, %s read %v, want %v:\n%s", index, want.property, got, want.value, assembly)
				}
			}
		}
	})
}

// TestCompileSubscriptCostsOneAdd is the stride check. A getelementptr states
// its offset in bytes, and one element is one slot, so the division by eight is
// a compile time constant: nothing that divides or shifts may reach the
// program for a subscript that contains no arithmetic of its own.
func TestCompileSubscriptCostsOneAdd(t *testing.T) {
	path := write(t, "stride.c", `long long a[8];
void main(void) {
    long long i = (long long)__ic_load(d0, Setting);
    a[i] = 1;
    __ic_store(d1, Setting, a[i]);
}`)

	assembly := compileSource(t, path)
	for _, scaling := range []string{"div ", "mul ", "sll ", "srl ", "sra ", "mod "} {
		if strings.Contains(assembly, scaling) {
			t.Errorf("the subscript emitted %q, so the element stride survived into the program:\n%s", scaling, assembly)
		}
	}
	if got := strings.Count(assembly, "add "); got > 1 {
		t.Errorf("one array at one index cost %d adds:\n%s", got, assembly)
	}
}

// forEachPipeline compiles src both ways and runs body over each. The
// unoptimized form is what instruction selection produced; the optimized one is
// what ships, and an address computation the optimizer rewrote has to mean the
// same thing.
func forEachPipeline(t *testing.T, src string, body func(*testing.T, string)) {
	t.Helper()
	path := write(t, "case.c", src)
	for _, tc := range pipelineCases {
		t.Run(tc.name, func(t *testing.T) {
			body(t, compileSource(t, path, tc.args...))
		})
	}
}

// compiled compiles src through the shipped pipeline and returns assembly
// the chip's own rules accept. It is the single-path counterpart of
// forEachPipeline, for a program whose pointers resolve only once the
// optimizer has promoted the parameters they pass through.
func compiled(t *testing.T, name, src string) string {
	t.Helper()
	return compileSource(t, write(t, name, src))
}

// runWithSetting runs assembly with one input on d0 and returns the device it
// wrote to on d1.
func runWithSetting(t *testing.T, assembly string, setting int64) *device {
	t.Helper()
	housing, in, out := devicePair(t)
	setLogic(t, in, "Setting", float64(setting))
	runOnChip(t, assembly, housing)
	return out
}

// runOnChip loads emitted assembly onto the chip in run and runs it until the
// program counter leaves the program.
func runOnChip(t *testing.T, assembly string, housing *chipRun) {
	t.Helper()
	runToEnd(t, housedChip(t, assembly, housing), assembly)
}

// logicType resolves a device property name against the generated tables.
func logicType(t *testing.T, name string) ic10.LogicType {
	t.Helper()
	info, ok := ic10.LookupLogicType(name)
	if !ok {
		t.Fatalf("the instruction tables name no logic type %q", name)
	}
	return info.Value
}

// TestReadableModeCarriesBlockIdentityAndNumericModeRemovesNames covers
// what each of the two rendering flags is for: --numeric is the escape
// hatch for a program out of bytes, and --readable must not change the
// program, since the chip cuts a line at its first '#'.
func TestReadableModeCarriesBlockIdentityAndNumericModeRemovesNames(t *testing.T) {
	// Temperature is read rather than written, since the name has to be
	// longer than the integer behind it for the byte difference to be the
	// one measured. The arms write different properties, keeping the
	// optimizer from folding the pair into one block for --readable to name.
	path := write(t, "readable.c", `void main(void) {
    double t = __ic_load(d0, Temperature);
    if (t > 291.15) {
        __ic_store(d1, On, 1);
    } else {
        __ic_store(d1, Setting, 2);
    }
    __ic_store(d1, Temperature, t);
}`)

	plain := compileSource(t, path)
	numeric := compileSource(t, path, "--numeric")
	readable := compileSource(t, path, "--readable")

	if !strings.Contains(plain, " Temperature") {
		t.Errorf("the shipped form does not name the logic type:\n%s", plain)
	}
	if strings.Contains(numeric, " Temperature") {
		t.Errorf("--numeric names a logic type instead of numbering it:\n%s", numeric)
	}
	if len(plain) <= len(numeric) {
		t.Errorf("naming machine values did not cost bytes, so one of the two forms is wrong")
	}
	if got, want := len(assemblyLines(t, plain)), len(assemblyLines(t, numeric)); got != want {
		t.Errorf("naming machine values cost %d lines against %d, and it must cost none", got, want)
	}

	annotated := 0
	for i, line := range assemblyLines(t, readable) {
		instruction, comment, found := strings.Cut(line, "#")
		if found {
			annotated++
		}
		if strings.Contains(comment, "#") {
			t.Errorf("readable line %d carries a second '#', which the chip would not cut at: %q", i, line)
		}
		if got := strings.TrimSpace(instruction); got != assemblyLines(t, plain)[i] {
			t.Errorf("readable line %d is %q where the shipped form emits %q; annotating must not change the program",
				i, got, assemblyLines(t, plain)[i])
		}
	}
	if annotated == 0 {
		t.Errorf("--readable annotated no line, so it says no more about the program than the shipped form:\n%s", readable)
	}
	if !strings.Contains(readable, "main_entry:") {
		t.Errorf("--readable names no block the program starts in, so a line does not say which block it opens:\n%s", readable)
	}
	if !strings.Contains(readable, "->") {
		t.Errorf("--readable names no branch target, so a branch says only the line number the shipped form already carries:\n%s", readable)
	}
}

func TestCompileReportsSourceErrors(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "a parse error",
			src:  "void main(void) { long long x = ; }",
			want: "expr.c:1:",
		},
		{
			name: "a rejected construct",
			src:  "void main(void) { goto done; }",
			want: "goto",
		},
		{
			name: "no entry point",
			src:  "long long f(void) { return 1; }",
			want: "main",
		},
		{
			name: "a type error",
			src:  "void main(void) { long long *p = 1; }",
			want: "cannot use",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := write(t, "expr.c", tc.src)
			stdout, stderr, err := run(t, path)
			if err == nil {
				t.Fatalf("compiling accepted invalid source:\n%s", stdout)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr does not mention %q:\n%s", tc.want, stderr)
			}
			if strings.TrimSpace(stdout) != "" {
				t.Errorf("assembly was written for a program that did not compile:\n%s", stdout)
			}
		})
	}
}

// TestCompileRejectsOperandsTheChipWouldFaultOn covers the operands the chip
// accepts at compile time and then faults on, once per tick, for as long as it
// runs. Every case is source a programmer can write, and every refusal has to
// name the line that wrote it.
func TestCompileRejectsOperandsTheChipWouldFaultOn(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "a device pin the housing does not have",
			src: `void main(void) {
    __ic_store(d6, Setting, 1);
}`,
			want: []string{"operands.c:2:", "'d6' is not a device", "d0 through d5"},
		},
		{
			name: "a negative device slot index",
			src: `void main(void) {
    __ic_store_slot(d0, -1, Quantity, 1);
}`,
			want: []string{"operands.c:2:", "slot index", "numbered from 0"},
		},
		{
			// Written on the array, the subscript is bounded by its type and
			// never reaches a slot at all. A pointer carries no length, so
			// this stays open until the pointer computation folds.
			name: "an index past the end of the array it starts from",
			src: `long long a[4];
void main(void) {
    long long *p = a;
    p[600] = (long long)__ic_load(d0, Setting);
    __ic_store(d1, Setting, a[0]);
}`,
			want: []string{"operands.c:4:", "element 600", "'a'", "4 elements"},
		},
		{
			// The data region lays objects out end to end, so a few elements
			// past a short array is another object rather than an address the
			// chip refuses. Nothing at run time can be asked about it and the
			// program writes the wrong slot every tick.
			name: "an index reaching the array laid out next",
			src: `long long a[4];
long long b[4];
void main(void) {
    long long i = (long long)__ic_load(d0, Setting);
    a[i] = 1;
    b[i] = 2;
    __ic_store(d1, Setting, *(a + 7));
}`,
			want: []string{"operands.c:7:", "an address in 'a'", "between 0 and 4", "found 7"},
		},
		{
			// Every register and memory slot holds one double, exact only
			// inside 2^53, so a constant outside that window is not a
			// number this machine computes with, and is refused where written.
			name: "an integer constant a double does not hold exactly",
			src: `void main(void) {
    long long big = 9007199254740993;
    __ic_store_batch(big, Setting, 1);
}`,
			want: []string{"operands.c:2:", "9007199254740993", "outside -2^53 to 2^53"},
		},
		{
			name: "a logic type the tables do not name",
			src: `void main(void) {
    __ic_store(d0, NotALogicType, 1);
}`,
			want: []string{"operands.c:2:", "is not a logic type"},
		},
		{
			name: "a device named by a variable rather than a pin",
			src: `void main(void) {
    long long pin = 1;
    __ic_store(pin, Setting, 1);
}`,
			want: []string{"operands.c:3:", "'pin' is not a device"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := write(t, "operands.c", tc.src)
			stdout, stderr, err := run(t, path)
			if err == nil {
				t.Fatalf("compiling accepted an operand the chip faults on:\n%s\n%s", stdout, stderr)
			}
			for _, want := range tc.want {
				if !strings.Contains(stderr, want) {
					t.Errorf("the refusal does not mention %q:\n%s", want, stderr)
				}
			}
			if strings.TrimSpace(stdout) != "" {
				t.Errorf("assembly was written for a program that did not compile:\n%s", stdout)
			}
		})
	}
}

// TestBackendDiagnosticsNameSourceRatherThanIR is the bar every rejection after
// IR generation has to meet: a line the programmer wrote, described in the
// language they wrote it in. Quoting the optimizer's own text names SSA
// registers and types that appear nowhere in the source.
func TestBackendDiagnosticsNameSourceRatherThanIR(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "a pointer that could name either of two arrays",
			src: `long long a[2];
long long b[2];
void main(void) {
    long long n = (long long)__ic_load(d0, Setting);
    long long *p = n > 0 ? a : b;
    p[0] = n;
    __ic_store(d1, Setting, a[0]);
}`,
			want: []string{"diag.c:5:", "exactly one object"},
		},
		{
			name: "sleep as the program's first instruction",
			src: `void main(void) {
    __ic_sleep(2);
}`,
			want: []string{"diag.c:2:", "'sleep'", "first instruction", "__ic_yield()"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := write(t, "diag.c", tc.src)
			stdout, stderr, err := run(t, path)
			if err == nil {
				t.Fatalf("compiling accepted a program the backend cannot lower:\n%s", stdout)
			}
			for _, want := range tc.want {
				if !strings.Contains(stderr, want) {
					t.Errorf("the refusal does not mention %q:\n%s", want, stderr)
				}
			}
			// The optimizer's own spelling of a value: an SSA register, a type
			// suffix, or a metadata reference. None of them is in the source.
			for _, leak := range []string{"%", "!dbg", "i64 ", "ptr "} {
				if strings.Contains(stderr, leak) {
					t.Errorf("the refusal quotes LLVM IR (%q):\n%s", leak, stderr)
				}
			}
		})
	}
}

// TestReportAttributesBytesToInlineSites is the accounting a programmer over
// budget acts on. One callee spliced in at two calls has to appear twice, since
// deleting one call recovers only its own expansion.
func TestReportAttributesBytesToInlineSites(t *testing.T) {
	path := write(t, "sites.c", `long long scale(long long v, long long by) {
    long long t = v * by;
    if (t < 0) {
        return 0;
    }
    return t + by;
}

void main(void) {
    long long a = scale((long long)__ic_load(d0, Setting), 3);
    long long b = scale((long long)__ic_load(d1, Setting), 5);
    __ic_store(d2, Setting, a + b);
}`)

	stdout, stderr, err := run(t, path)
	if err != nil {
		t.Fatalf("compiling: %v\n%s", err, stderr)
	}
	checkAssembly(t, stdout)
	for _, want := range []string{
		"bytes by construct",
		"scale inlined at " + path + ":10:",
		"scale inlined at " + path + ":11:",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the report does not mention %q:\n%s", want, stderr)
		}
	}
	t.Logf("--- sites.c ---\n%s\n%s", stdout, stderr)
}

func TestCommandArguments(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no source file", nil, "exactly one source file"},
		{"two source files", []string{"a.c", "b.c"}, "exactly one source file"},
		{"a file that is not there", []string{filepath.Join(t.TempDir(), "missing.c")}, "reading"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := run(t, tc.args...)
			if err == nil {
				t.Fatalf("the command accepted %v", tc.args)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error does not mention %q: %v", tc.want, err)
			}
		})
	}
}

func TestVersionPrintsTheBareString(t *testing.T) {
	stdout, _, err := run(t, "--version")
	if err != nil {
		t.Fatalf("--version: %v", err)
	}
	if strings.TrimSpace(stdout) != version {
		t.Errorf("--version printed %q, want %q", strings.TrimSpace(stdout), version)
	}
}

// sizeReport reads what the command spent against each of the three
// limits out of the size report it writes to its error stream — all
// three, since two would leave the width free to move unnoticed.
func sizeReport(t *testing.T, stderr string) (spend fixtureBuild) {
	t.Helper()
	const marker = "program: "
	_, after, ok := strings.Cut(stderr, marker)
	if !ok {
		t.Fatalf("no size report was printed:\n%s", stderr)
	}
	var byteBudget, lineBudget, widthBudget int
	_, err := fmt.Sscanf(after, "%d of %d bytes (%d%%), %d of %d lines (%d%%), longest line %d of %d characters (%d%%)",
		&spend.Bytes, &byteBudget, &spend.ByteShare,
		&spend.Lines, &lineBudget, &spend.LineShare,
		&spend.LongestLine, &widthBudget, &spend.WidthShare)
	if err != nil {
		t.Fatalf("the size report does not parse: %v\n%s", err, stderr)
	}
	return spend
}

// TestOptimizerShrinksOutput is the measurement the optimizer exists for. The
// comparison is against --no-optimize rather than a recorded number, because
// the exact figure moves with the libLLVM version and what has to hold is the
// direction.
func TestOptimizerShrinksOutput(t *testing.T) {
	cases := []struct {
		name string
		// path names a fixture. Empty means src is compiled instead.
		path string
		src  string
	}{
		{name: "thermostat.c", path: filepath.Join(fixtures, "thermostat.c")},
		{
			name: "a local nothing else reads",
			src:  "long long g;\nvoid main(void) { long long t = (long long)__ic_load(d0, Temperature); g = t + t; }",
		},
		{
			name: "an inlined call chain",
			src: `long long sq(long long x) { return x * x; }
long long cube(long long x) { return sq(x) * x; }
void main(void) { __ic_store(d1, Setting, cube((long long)__ic_load(d0, Temperature))); }`,
		},
		{
			name: "a loop whose body is invariant",
			src:  "void main(void) { long long s = 0;\nfor (long long i = 0; i < 8; i++) { for (long long j = 0; j < 8; j++) { s = s + i * j; } }\n__ic_store(d1, Setting, s); }",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := tc.path
			if path == "" {
				path = write(t, "case.c", tc.src)
			}

			plain, plainErr, err := run(t, "--no-optimize", path)
			if err != nil {
				t.Fatalf("compiling unoptimized: %v\n%s", err, plainErr)
			}
			optimized, optimizedErr, err := run(t, path)
			if err != nil {
				t.Fatalf("compiling: %v\n%s", err, optimizedErr)
			}

			unoptimized, optimizedSpend := sizeReport(t, plainErr), sizeReport(t, optimizedErr)
			t.Logf("%s: %d bytes over %d lines unoptimized, %d bytes over %d lines optimized",
				tc.name, unoptimized.Bytes, unoptimized.Lines, optimizedSpend.Bytes, optimizedSpend.Lines)

			if optimizedSpend.Bytes >= unoptimized.Bytes {
				t.Errorf("optimized output is %d bytes against %d unoptimized:\n--- unoptimized ---\n%s\n--- optimized ---\n%s",
					optimizedSpend.Bytes, unoptimized.Bytes, plain, optimized)
			}
			checkAssembly(t, plain)
			checkAssembly(t, optimized)
		})
	}
}

// TestUnoptimizedOutputStillAssembles keeps --no-optimize a usable
// comparison: what it emits has to be something the chip would accept. A
// fixture the command withholds for running past the line limit is
// assembled through the pipeline directly instead.
func TestUnoptimizedOutputStillAssembles(t *testing.T) {
	for _, name := range corpusFixtures(t) {
		t.Run(name, func(t *testing.T) {
			stdout, stderr, err := run(t, unoptimizedFlag, filepath.Join(fixtures, name))
			if reason, unsupported := unsupportedUnoptimized[name]; unsupported {
				if err == nil {
					t.Fatalf("%s is recorded as unsupported because %s, and %s now compiles it:\n%s",
						name, reason, unoptimizedFlag, stdout)
				}
				// The optimized path compiles every fixture, so a refusal naming
				// the pointer restriction is this path being stricter about
				// pointers rather than refusing for the reason recorded beside
				// the fixture.
				if strings.Contains(stderr, "does not name a local or a global") {
					t.Errorf("the unoptimized path is stricter about pointers than the optimized one:\n%s", stderr)
				}
				return
			}
			switch {
			case overEditorLimit[name] && err == nil:
				t.Errorf("the unoptimized form fits inside every limit, and overEditorLimit names it:\n%s", stderr)
			case !overEditorLimit[name] && err != nil:
				t.Fatalf("compiling unoptimized: %v\n%s", err, stderr)
			case overEditorLimit[name]:
				stdout = buildUnoptimized(t, name).assembly
			}
			if len(checkAssembly(t, stdout)) == 0 {
				t.Fatalf("no assembly was emitted")
			}
		})
	}
}

// TestOptimizedOutputPromotesLocals checks the promotion the byte counts depend
// on at the instruction level, since a pipeline that stopped running mem2reg
// would still shrink the output some other way and leave the comparison green.
func TestOptimizedOutputPromotesLocals(t *testing.T) {
	path := write(t, "locals.c", "long long g;\nvoid main(void) { long long a = (long long)__ic_load(d0, Temperature); long long b = a * 3; g = a + b; }")

	assembly := compileSource(t, path)
	for _, memory := range []string{"poke ", "get "} {
		if strings.Contains(assembly, memory) {
			t.Errorf("the assembly still reaches the data region with %q, so a local was not promoted:\n%s", memory, assembly)
		}
	}
}

// TestDeprecatedLogicTypeWarnsAndStillEmits covers the whole-command half of
// the deprecation report: a warning on the error stream, the assembly on the
// output stream, and a zero exit.
func TestDeprecatedLogicTypeWarnsAndStillEmits(t *testing.T) {
	path := write(t, "deprecated.c", "void main(void) { __ic_store(d0, None, 1); }")

	stdout, stderr, err := run(t, path)
	if err != nil {
		t.Fatalf("a deprecated logic type was rejected: %v\n%s", err, stderr)
	}
	checkAssembly(t, stdout)
	if !strings.Contains(stderr, "warning: ") {
		t.Errorf("the diagnostic is not labelled a warning:\n%s", stderr)
	}
	if !strings.Contains(stderr, "None") || !strings.Contains(stderr, "deprecated") {
		t.Errorf("the warning does not say which name is deprecated:\n%s", stderr)
	}
	if !strings.Contains(stdout, "s d0 None") {
		t.Errorf("the program was not emitted, or the operand changed:\n%s", stdout)
	}
	if !strings.Contains(stderr, "program: ") {
		t.Errorf("the size report was withheld from a program that compiled:\n%s", stderr)
	}
}
