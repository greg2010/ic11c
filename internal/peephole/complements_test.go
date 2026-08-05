package peephole

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/chip"
	"github.com/greg2010/ic11c/internal/chiptest"
	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/isa"
)

func TestMain(m *testing.M) { chiptest.Main(m) }

// resultRegister is where every executed set instruction stores its answer, and
// valueRegisters are the operand positions the value grid drives. Three is the
// widest a pair in the table takes, which is `sap` with its two operands and a
// tolerance.
const resultRegister = ic10.Register(0)

var valueRegisters = [...]ic10.Register{1, 2, 3}

// wantComplementPairs is how many unordered pairs the table holds. Pinning it
// is what stops the table shrinking without anything noticing: a pair added to
// the map is executed here with no new case written, but a pair removed would
// otherwise reduce coverage silently.
const wantComplementPairs = 4

// maxReportedMismatches caps how many failing inputs one pair reports, so that
// a genuinely wrong entry names the values that separate it rather than
// printing the whole grid.
const maxReportedMismatches = 8

// twoPow53 is the largest double whose successor is not representable, which is
// where the machine stops counting integers one at a time.
const twoPow53 = 1 << 53

// gridValue is one input the complement table is executed against. The name
// travels with the value so a failure reports what separated the pair rather
// than a bare float.
type gridValue struct {
	name  string
	value float64
}

// valueGrid is the input set every value-taking pair is executed over, as
// the full cross product of the pair's arity.
var valueGrid = []gridValue{
	{"+0", 0},                    // bit pattern vs comparison
	{"-0", math.Copysign(0, -1)}, // bit pattern vs comparison
	{"+1", 1},
	{"-1", -1},
	{"+Inf", math.Inf(1)},    // bit pattern vs comparison
	{"-Inf", math.Inf(-1)},   // bit pattern vs comparison
	{"2^53", twoPow53},       // distinct values collide
	{"2^53-1", twoPow53 - 1}, // distinct values collide
	{"2^53+1", twoPow53 + 1}, // distinct values collide
	{"+subnormal", math.SmallestNonzeroFloat64},  // below tolerance floor
	{"-subnormal", -math.SmallestNonzeroFloat64}, // below tolerance floor
	{"NaN", math.NaN()},                          // false for every ordered comparison
	{"0.25", 0.25},                               // outside tolerance 0.5 of 1
	{"0.5", 0.5},                                 // exactly tolerance 0.5 of 1
	{"100", 100},
	{"101", 101},
}

// TestValueGridHoldsTheSeparatingValues keeps the grid from losing the
// inputs the rest of this file's conclusions rest on. The comparison is
// on bits so +0 and -0 are distinguishable, which an equality check
// would not be.
func TestValueGridHoldsTheSeparatingValues(t *testing.T) {
	required := []gridValue{
		{"+0", 0},
		{"-0", math.Copysign(0, -1)},
		{"+1", 1},
		{"-1", -1},
		{"+Inf", math.Inf(1)},
		{"-Inf", math.Inf(-1)},
		{"2^53", twoPow53},
		{"2^53-1", twoPow53 - 1},
		{"2^53+1", twoPow53 + 1},
		{"+subnormal", math.SmallestNonzeroFloat64},
		{"NaN", math.NaN()},
	}
	for _, want := range required {
		index := slices.IndexFunc(valueGrid, func(g gridValue) bool { return g.name == want.name })
		if index < 0 {
			t.Errorf("the grid holds no entry named %q", want.name)
			continue
		}
		got := valueGrid[index].value
		if math.IsNaN(want.value) {
			if !math.IsNaN(got) {
				t.Errorf("the grid entry %q is %v, want a NaN", want.name, got)
			}
			continue
		}
		if math.Float64bits(got) != math.Float64bits(want.value) {
			t.Errorf("the grid entry %q is %v, want %v", want.name, got, want.value)
		}
	}
	if gridEntry(t, "2^53+1") != gridEntry(t, "2^53") {
		t.Error("2^53+1 and 2^53 are different doubles here, so the grid no longer covers a source value rounding onto its neighbour")
	}
}

// gridEntry looks up one grid value by name, failing when the grid has lost it.
func gridEntry(t *testing.T, name string) float64 {
	t.Helper()
	index := slices.IndexFunc(valueGrid, func(g gridValue) bool { return g.name == name })
	if index < 0 {
		t.Fatalf("the grid holds no entry named %q", name)
	}
	return valueGrid[index].value
}

// operandRole is what the driver supplies for one operand position of a set
// instruction.
type operandRole int

const (
	// roleDestination is the register the answer is read back from.
	roleDestination operandRole = iota
	// roleValue is a register the value grid drives.
	roleValue
	// roleDevice is a housing pin the device fixture drives.
	roleDevice
)

// classifyOperands describes op's operand positions from the instruction table
// rather than from a hand-written list, so a pair added to the complement map
// is driven without anyone writing a case for it. It reports false for an
// operand shape no fixture here covers.
func classifyOperands(op ic10.Opcode) ([]operandRole, bool) {
	instruction, ok := op.Instruction()
	if !ok {
		return nil, false
	}
	roles := make([]operandRole, 0, len(instruction.Operands))
	for i, operand := range instruction.Operands {
		switch {
		case i == 0 && operand.Accepts(ic10.OperandRegister):
			roles = append(roles, roleDestination)
		case operand.Accepts(ic10.OperandNumber):
			roles = append(roles, roleValue)
		case operand.Accepts(ic10.OperandDevice):
			roles = append(roles, roleDevice)
		default:
			return nil, false
		}
	}
	return roles, true
}

// countValues is how many grid-driven operands roles holds.
func countValues(roles []operandRole) int {
	n := 0
	for _, role := range roles {
		if role == roleValue {
			n++
		}
	}
	return n
}

// sourceLine renders the one-instruction program that exercises op, with any
// device position filled by deviceText.
func sourceLine(op ic10.Opcode, roles []operandRole, deviceText string) string {
	tokens := make([]string, 0, len(roles)+1)
	tokens = append(tokens, op.String())
	value := 0
	for _, role := range roles {
		switch role {
		case roleDestination:
			tokens = append(tokens, resultRegister.String())
		case roleValue:
			tokens = append(tokens, valueRegisters[value].String())
			value++
		case roleDevice:
			tokens = append(tokens, deviceText)
		}
	}
	return strings.Join(tokens, " ")
}

// complementPair is one unordered entry of the complement table, with the
// operand shape both members share.
type complementPair struct {
	op, complement ic10.Opcode
	roles          []operandRole
}

// tablePairs derives every pair from the complement map, each exactly
// once and in opcode order. A pair whose operand shape has no fixture
// fails by name here rather than being dropped, since a driver that
// quietly skips what it cannot build reads as covering the whole table.
func tablePairs(t *testing.T) []complementPair {
	t.Helper()
	var pairs []complementPair
	for op, complement := range complements {
		if op >= complement {
			continue
		}
		roles, ok := classifyOperands(op)
		if !ok {
			t.Errorf("no fixture drives the operands of %v and %v, so the pair is not executed", op, complement)
			continue
		}
		pairs = append(pairs, complementPair{op: op, complement: complement, roles: roles})
	}
	slices.SortFunc(pairs, func(a, b complementPair) int { return int(a.op) - int(b.op) })
	if 2*len(pairs) != len(complements) {
		t.Errorf("the table holds %d entries but yields %d pairs; an opcode maps to itself or is unpaired",
			len(complements), len(pairs))
	}
	return pairs
}

// fixturePin is the housing pin the device fixtures put a device on.
const fixturePin = 0

// fixturePinText is how a line under test names that pin. It is derived from the
// pin rather than spelled a second time, so the line cannot address a pin the
// fixture did not populate.
var fixturePinText = "d" + strconv.Itoa(fixturePin)

// runProgram runs source on the chip with the value registers seeded from
// inputs, and hands back the state the run left. The result register is
// seeded with NaN, so an instruction that stored nothing reads back as
// NaN rather than the 0 that is half of what a complement looks like.
func runProgram(ctx context.Context, t *testing.T, h *chip.Harness, source string, inputs []float64) chip.Snapshot {
	t.Helper()
	var initial chip.State
	initial.Registers[resultRegister] = math.NaN()
	for i, in := range inputs {
		initial.Registers[valueRegisters[i]] = in
	}
	got, err := h.Run(ctx, chip.Request{Source: source, Initial: initial})
	if err != nil {
		t.Fatalf("running %q: %v", source, err)
	}
	if got.Stop != chip.StopEnded {
		t.Fatalf("running %q ended %q, want the program to run out: fault %s, compile error %s",
			source, got.Stop, got.Fault, got.CompileError)
	}
	return got.Snapshot
}

// answer runs a one-instruction program over inputs and returns what it stored.
func answer(ctx context.Context, t *testing.T, h *chip.Harness, line string, inputs []float64) float64 {
	t.Helper()
	return runProgram(ctx, t, h, line, inputs).Registers[resultRegister]
}

// runOnDevicePin runs source on a permissive chip whose device pin
// either holds a device or does not, and hands back the state the run
// left. It drives the chip by hand rather than through
// [chip.Harness.Run], which begins with a reset that would discard a world laid out before it.
func runOnDevicePin(ctx context.Context, t *testing.T, h *chip.FixtureHarness, source string, attach bool) chip.Snapshot {
	t.Helper()
	if err := h.Reset(ctx); err != nil {
		t.Fatalf("resetting the chip: %v", err)
	}
	if attach {
		if err := h.AddDevice(ctx, fixturePin); err != nil {
			t.Fatalf("putting a device on d%d: %v", fixturePin, err)
		}
	}
	if err := h.Load(ctx, source); err != nil {
		t.Fatalf("loading %q: %v", source, err)
	}
	if err := h.SetRegister(ctx, resultRegister, math.NaN()); err != nil {
		t.Fatalf("seeding the result register: %v", err)
	}
	got, err := h.Step(ctx, chip.InstructionsPerTick)
	if err != nil {
		t.Fatalf("running %q: %v", source, err)
	}
	if got.Stop != chip.StopEnded {
		t.Fatalf("running %q ended %q, want the program to run out: fault %s, compile error %s",
			source, got.Stop, got.Fault, got.CompileError)
	}
	return got.Snapshot
}

// deviceAnswer runs a one-instruction program against a pin that either holds a
// device or does not, and returns what the instruction stored.
func deviceAnswer(ctx context.Context, t *testing.T, h *chip.FixtureHarness, line string, attach bool) float64 {
	t.Helper()
	return runOnDevicePin(ctx, t, h, line, attach).Registers[resultRegister]
}

// exactComplements reports whether two answers are the opposites the fold
// assumes: one exactly 1 and the other exactly 0, which is also the only way
// they sum to 1 given a set instruction stores nothing else.
func exactComplements(first, second float64) bool {
	return (first == 0 && second == 1) || (first == 1 && second == 0)
}

// forEachInput calls run over every n-tuple drawn from the value grid. The
// slice it passes is reused, so a caller that keeps it must copy.
func forEachInput(n int, run func(inputs []gridValue)) {
	index := make([]int, n)
	inputs := make([]gridValue, n)
	for {
		for i, at := range index {
			inputs[i] = valueGrid[at]
		}
		run(inputs)
		pos := n - 1
		for ; pos >= 0; pos-- {
			index[pos]++
			if index[pos] < len(valueGrid) {
				break
			}
			index[pos] = 0
		}
		if pos < 0 {
			return
		}
	}
}

// inputNames renders a tuple for a failure message.
func inputNames(inputs []gridValue) string {
	names := make([]string, len(inputs))
	for i, in := range inputs {
		names[i] = in.name
	}
	return strings.Join(names, ", ")
}

// TestComplementsAnswerOppositesOnTheChip executes both members of every
// pair in the complement table and asserts they answer exact opposites —
// the property the fold rests on, since it rewrites a set instruction's
// opcode into its pair member and leaves the operands alone.
func TestComplementsAnswerOppositesOnTheChip(t *testing.T) {
	pairs := tablePairs(t)
	if len(pairs) != wantComplementPairs {
		t.Fatalf("the complement table yields %d pairs, want %d", len(pairs), wantComplementPairs)
	}
	for _, pair := range pairs {
		t.Run(fmt.Sprintf("%v and %v", pair.op, pair.complement), func(t *testing.T) {
			if slices.Contains(pair.roles, roleDevice) {
				runDevicePair(t, pair)
				return
			}
			runValuePair(t, pair)
		})
	}
}

// runValuePair executes one pair over the whole cross product of the grid.
func runValuePair(t *testing.T, pair complementPair) {
	t.Helper()
	ctx, harness := chiptest.Harness(t)
	firstLine := sourceLine(pair.op, pair.roles, "")
	secondLine := sourceLine(pair.complement, pair.roles, "")

	arity := countValues(pair.roles)
	values := make([]float64, arity)
	executed, mismatches := 0, 0
	forEachInput(arity, func(inputs []gridValue) {
		for i, in := range inputs {
			values[i] = in.value
		}
		executed++
		a := answer(ctx, t, harness, firstLine, values)
		b := answer(ctx, t, harness, secondLine, values)
		if exactComplements(a, b) {
			return
		}
		mismatches++
		if mismatches <= maxReportedMismatches {
			t.Errorf("for (%s) %q answered %v and %q answered %v, want one 1 and one 0",
				inputNames(inputs), firstLine, a, secondLine, b)
		}
	})
	if mismatches > maxReportedMismatches {
		t.Errorf("%v and %v disagree on %d of %d inputs", pair.op, pair.complement, mismatches, executed)
	}
	if want := gridSize(arity); executed != want {
		t.Errorf("executed %d inputs over %d operands, want %d", executed, arity, want)
	}
}

// gridSize is how many tuples the cross product of the grid holds at one arity.
func gridSize(arity int) int {
	size := 1
	for range arity {
		size *= len(valueGrid)
	}
	return size
}

// devicePresence is the whole input domain of the device pair: `sdse` and
// `sdns` ask only whether a pin resolves to something.
var devicePresence = []struct {
	name   string
	attach bool
}{
	{name: "a pin holding a device", attach: true},
	{name: "an empty pin", attach: false},
}

// runDevicePair executes the device pair, driven by a presence fixture
// instead of the grid. The two worlds must give different answers, or a
// pin that never took the device would pass while executed against only
// one input.
func runDevicePair(t *testing.T, pair complementPair) {
	t.Helper()
	ctx, harness := chiptest.Fixtures(t)
	firstLine := sourceLine(pair.op, pair.roles, fixturePinText)
	secondLine := sourceLine(pair.complement, pair.roles, fixturePinText)
	answers := make([]float64, len(devicePresence))
	for i, world := range devicePresence {
		a := deviceAnswer(ctx, t, harness, firstLine, world.attach)
		b := deviceAnswer(ctx, t, harness, secondLine, world.attach)
		if !exactComplements(a, b) {
			t.Errorf("for %s %q answered %v and %q answered %v, want one 1 and one 0",
				world.name, firstLine, a, secondLine, b)
		}
		answers[i] = a
	}
	if len(devicePresence) != 2 || answers[0] == answers[1] {
		t.Errorf("%q answered %v across the presence fixtures, which do not distinguish a populated pin from an empty one",
			firstLine, answers)
	}
}

// nonComplementPairs are the comparisons that look like complements and
// are not, absent from the complement table on purpose (see
// [complements] for why). onlyNaN marks a pair whose members both answer
// 0 only for NaN inputs; the approximate comparisons do it for more.
var nonComplementPairs = []struct {
	first, second ic10.Opcode
	onlyNaN       bool
}{
	{first: isa.OpSlt, second: isa.OpSge, onlyNaN: true},
	{first: isa.OpSgt, second: isa.OpSle, onlyNaN: true},
	{first: isa.OpSltz, second: isa.OpSgez, onlyNaN: true},
	{first: isa.OpSgtz, second: isa.OpSlez, onlyNaN: true},
	{first: isa.OpSap, second: isa.OpSna},
	{first: isa.OpSapz, second: isa.OpSnaz},
}

// TestNonComplementComparisonsAreNotComplements executes the exclusion
// the table rests on rather than asserting it in prose: each pair
// answers exact opposites over most of the grid and 0 for both members
// over the rest.
func TestNonComplementComparisonsAreNotComplements(t *testing.T) {
	if len(nonComplementPairs) == 0 {
		t.Fatal("no comparison pair is checked, so the exclusion is not established")
	}
	for _, pair := range nonComplementPairs {
		t.Run(fmt.Sprintf("%v and %v", pair.first, pair.second), func(t *testing.T) {
			for _, op := range []ic10.Opcode{pair.first, pair.second} {
				if complement, listed := complements[op]; listed {
					t.Errorf("%v is in the complement table, paired with %v", op, complement)
				}
			}
			roles, ok := classifyOperands(pair.first)
			if !ok {
				t.Fatalf("no fixture drives the operands of %v", pair.first)
			}
			runNonComplementPair(t, pair.first, pair.second, roles, pair.onlyNaN)
		})
	}
}

// runNonComplementPair asserts the two halves of the exclusion over the grid.
func runNonComplementPair(t *testing.T, first, second ic10.Opcode, roles []operandRole, onlyNaN bool) {
	t.Helper()
	ctx, harness := chiptest.Harness(t)
	firstLine := sourceLine(first, roles, "")
	secondLine := sourceLine(second, roles, "")

	arity := countValues(roles)
	values := make([]float64, arity)
	bothZero, opposite, neither, mismatches := 0, 0, 0, 0
	forEachInput(arity, func(inputs []gridValue) {
		holdsNaN := false
		for i, in := range inputs {
			values[i] = in.value
			holdsNaN = holdsNaN || math.IsNaN(in.value)
		}
		a := answer(ctx, t, harness, firstLine, values)
		b := answer(ctx, t, harness, secondLine, values)
		var want string
		switch {
		case a == 0 && b == 0:
			bothZero++
			if onlyNaN && !holdsNaN {
				want = "want one 1 and one 0"
			}
		case exactComplements(a, b):
			opposite++
			if onlyNaN && holdsNaN {
				want = "want both 0"
			}
		default:
			neither++
			want = "want either one 1 and one 0, or both 0"
		}
		if want == "" {
			return
		}
		mismatches++
		if mismatches <= maxReportedMismatches {
			t.Errorf("for (%s) %q answered %v and %q answered %v, %s",
				inputNames(inputs), firstLine, a, secondLine, b, want)
		}
	})
	executed := bothZero + opposite + neither
	if mismatches > maxReportedMismatches {
		t.Errorf("%v and %v depart from the exclusion on %d of %d inputs",
			first, second, mismatches, executed)
	}
	if bothZero == 0 {
		t.Error("no input made both members answer 0, so nothing established why the pair is excluded")
	}
	if opposite == 0 {
		t.Error("no input separated the two, so nothing established that the pair is otherwise opposite")
	}
	if want := gridSize(arity); executed != want {
		t.Errorf("executed %d inputs over %d operands, want %d", executed, arity, want)
	}
}

// wantTruthValueOps is how many opcodes [complements] and
// [uncomplementedSets] name between them, pinned so neither table shrinks
// without anything noticing: an opcode added is executed below with no
// new case written, and one removed would reduce coverage silently.
const wantTruthValueOps = 20

// truthValueOps lists every opcode [complements] or [uncomplementedSets] names, in
// opcode order so a failure reports the same way on every run.
func truthValueOps() []ic10.Opcode {
	ops := make([]ic10.Opcode, 0, len(complements)+len(uncomplementedSets))
	for op := range complements {
		ops = append(ops, op)
	}
	for op := range uncomplementedSets {
		ops = append(ops, op)
	}
	slices.Sort(ops)
	return slices.Compact(ops)
}

// TestEveryTruthValueAnswersZeroOrOne executes every set instruction the
// `snez` retest may stand over and asserts it stores exactly 0 or 1 — the
// whole precondition of that half of the fold. Both answers must appear,
// so an instruction the fixture never drives past 0 reads as unexecuted.
func TestEveryTruthValueAnswersZeroOrOne(t *testing.T) {
	ops := truthValueOps()
	if len(ops) != wantTruthValueOps {
		t.Fatalf("the tables yield %d opcodes answering a truth value, want %d", len(ops), wantTruthValueOps)
	}
	for _, op := range ops {
		t.Run(op.String(), func(t *testing.T) {
			roles, ok := classifyOperands(op)
			if !ok {
				t.Fatalf("no fixture drives the operands of %v", op)
			}
			if slices.Contains(roles, roleDevice) {
				runDeviceTruthValue(t, op, roles)
				return
			}
			runValueTruthValue(t, op, roles)
		})
	}
}

// runValueTruthValue executes one instruction over the whole cross product of
// the grid.
func runValueTruthValue(t *testing.T, op ic10.Opcode, roles []operandRole) {
	t.Helper()
	ctx, harness := chiptest.Harness(t)
	line := sourceLine(op, roles, "")

	arity := countValues(roles)
	values := make([]float64, arity)
	executed, mismatches := 0, 0
	seen := map[float64]bool{}
	forEachInput(arity, func(inputs []gridValue) {
		for i, in := range inputs {
			values[i] = in.value
		}
		executed++
		got := answer(ctx, t, harness, line, values)
		seen[got] = true
		if got == 0 || got == 1 {
			return
		}
		mismatches++
		if mismatches <= maxReportedMismatches {
			t.Errorf("for (%s) %q answered %v, want 0 or 1", inputNames(inputs), line, got)
		}
	})
	if mismatches > maxReportedMismatches {
		t.Errorf("%v answered outside 0 and 1 on %d of %d inputs", op, mismatches, executed)
	}
	if !seen[0] || !seen[1] {
		t.Errorf("%q answered %v over the grid, which never separates the two truth values", line, sortedAnswers(seen))
	}
	if want := gridSize(arity); executed != want {
		t.Errorf("executed %d inputs over %d operands, want %d", executed, arity, want)
	}
}

// runDeviceTruthValue executes an instruction that asks a housing rather than a
// value, which the presence fixture drives.
func runDeviceTruthValue(t *testing.T, op ic10.Opcode, roles []operandRole) {
	t.Helper()
	ctx, harness := chiptest.Fixtures(t)
	line := sourceLine(op, roles, fixturePinText)
	seen := map[float64]bool{}
	for _, world := range devicePresence {
		got := deviceAnswer(ctx, t, harness, line, world.attach)
		seen[got] = true
		if got != 0 && got != 1 {
			t.Errorf("for %s %q answered %v, want 0 or 1", world.name, line, got)
		}
	}
	if !seen[0] || !seen[1] {
		t.Errorf("%q answered %v across the presence fixtures, which do not separate the two truth values", line, sortedAnswers(seen))
	}
}

// sortedAnswers renders an answer set for a failure message.
func sortedAnswers(seen map[float64]bool) []float64 {
	answers := make([]float64, 0, len(seen))
	for got := range seen {
		answers = append(answers, got)
	}
	slices.Sort(answers)
	return answers
}

// TestAnsweringATruthValueIsNotTheSameQuestionAsHavingAComplement keeps
// the two predicates apart: the ordered comparisons answer a truth value
// without having a complement to fold into.
func TestAnsweringATruthValueIsNotTheSameQuestionAsHavingAComplement(t *testing.T) {
	tests := []struct {
		name         string
		op           ic10.Opcode
		truthValue   bool
		complemented bool
	}{
		{name: "the NaN test", op: isa.OpSnan, truthValue: true, complemented: true},
		{name: "equality", op: isa.OpSeq, truthValue: true, complemented: true},
		{name: "the device test", op: isa.OpSdse, truthValue: true, complemented: true},
		{name: "an ordered comparison", op: isa.OpSlt, truthValue: true},
		{name: "an ordered comparison against zero", op: isa.OpSgez, truthValue: true},
		{name: "an approximate comparison", op: isa.OpSap, truthValue: true},
		{name: "a negated approximate comparison", op: isa.OpSna, truthValue: true},
		{name: "the sign, which answers -1 as readily", op: isa.OpSgn},
		{name: "a sum", op: isa.OpAdd},
		{name: "a choice between two values", op: isa.OpSelect},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The two tables together are the instructions whose result is
			// already 0 or 1, and the complement table alone is the ones that
			// also have a negation. Reading the union off both is what
			// [retestsInPlace] does.
			_, complemented := complements[tt.op]
			if got := complemented || uncomplementedSets[tt.op]; got != tt.truthValue {
				t.Errorf("%v answers a truth value: %v, want %v", tt.op, got, tt.truthValue)
			}
			if complemented != tt.complemented {
				t.Errorf("%v is in the complement table: %v, want %v", tt.op, complemented, tt.complemented)
			}
		})
	}
}
