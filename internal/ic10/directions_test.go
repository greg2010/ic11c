package ic10_test

import (
	"context"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/chip"
	"github.com/greg2010/ic11c/internal/chiptest"
	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/isa"
)

func TestMain(m *testing.M) { chiptest.Main(m) }

// The values the probed register starts each pair of runs with: distinct,
// so a replaced register reads the same both times and an untouched one
// does not; small, so a probe fits an address, shift distance, or bit
// offset; and each a device reference id, since some device positions are
// undocumented (clrd spells its device operand num) and both values must
// resolve.
const (
	probeLow  = 3
	probeHigh = 5
)

const (
	probePrefabHash = 4242
	probeNameHash   = 2424
)

// probePin is the pin every line that names one names; probePins is the
// whole arrangement a probe runs among. A fixture device answers its pin's
// reference id plus one, so the other two pins are derived from the probe
// values (probeLow-1, probeHigh-1): a probe standing in a reference-id
// position must resolve to a device.
const probePin = 0

var probePins = []int{probePin, probeLow - 1, probeHigh - 1}

// probeRegister is the register every probe puts under test, and probeOther the
// one every other register position of the same line gets. Keeping the two
// apart is what lets a destination be spelled without disturbing the probe.
const (
	probeRegister = "r0"
	probeOther    = "r1"
)

// probeIndex is the register file entry probeRegister names. It is resolved from
// the name the line is written with rather than spelled a second time, so the
// entry the probe reads back cannot drift from the one the line addresses.
func probeIndex(t *testing.T) ic10.Register {
	t.Helper()
	index, ok := ic10.ParseRegister(probeRegister)
	if !ok {
		t.Fatalf("%s is not a register the machine names", probeRegister)
	}
	return index
}

// probeStackPointer leaves room below and above for push, pop and peek, which
// fault against the ends of the array rather than writing anything.
const probeStackPointer = 8

// probeSeed arms the generator `rand` draws from. Two runs of one line have to
// draw the same number, or a register the instruction replaced would end them
// holding different values and read back as one it folded into.
const probeSeed = 0x1c11c

// registerUse is what running an instruction did to the probed register.
type registerUse int

const (
	// leftAlone is a register both runs ended holding what it started with.
	leftAlone registerUse = iota
	// replaced is a register both runs ended holding the same value, which two
	// runs starting from different ones can only do if the instruction wrote
	// something it did not read out of that register.
	replaced
	// folded is a register both runs changed and neither ended agreeing on,
	// which is the instruction carrying the old value into the new one.
	folded
)

func (u registerUse) direction() ic10.Direction {
	switch u {
	case replaced:
		return ic10.DirectionWrite
	case folded:
		return ic10.DirectionReadWrite
	case leftAlone:
	}
	return ic10.DirectionRead
}

// refusedToCompile names mnemonics the chip does not compile at the
// table's declared operand count, with why. The first four disagree
// between the game's token-count check and what it reads; hcf compiles in
// the game but not in the standalone chip tools/chipgen cuts, which lacks
// a method its operation calls.
var refusedToCompile = map[ic10.Opcode]string{
	isa.OpBrapz:  "the token count check disagrees with the operand read",
	isa.OpBrnaz:  "the token count check disagrees with the operand read",
	isa.OpBapzal: "the token count check disagrees with the operand read",
	isa.OpBnazal: "the token count check disagrees with the operand read",
	isa.OpHcf:    "tools/chipgen drops the parse arm, which has no standalone definition",
}

// needsMemoryHolder names mnemonics whose device reaches a memory array,
// not a logic property. Only a circuit housing holds one; the fixture
// device on a pin or filed by reference id does not, so every position of
// these lines faults for want of a target and no direction can be read.
var needsMemoryHolder = map[ic10.Opcode]bool{
	isa.OpGet:  true,
	isa.OpPut:  true,
	isa.OpGetd: true,
	isa.OpPutd: true,
	isa.OpClrd: true,
}

// TestTheTablesOperandCountCompiles asks the chip which mnemonics it
// accepts at the operand count the tables give them. Checked in both
// directions, since TestOperandDirectionsMatchTheChip rests on this set: a
// mnemonic that starts compiling has to leave it as loudly as one that
// stops.
func TestTheTablesOperandCountCompiles(t *testing.T) {
	ctx, harness := chiptest.Fixtures(t)
	for _, instruction := range ic10.Instructions {
		t.Run(instruction.Mnemonic, func(t *testing.T) {
			source := renderProbe(t, instruction, noProbe)
			reason, refused := refusedToCompile[instruction.Opcode]
			switch got := compiles(ctx, t, harness, source); {
			case got && refused:
				t.Errorf("the chip compiles %q, and it is recorded as refused because %s", source, reason)
			case !got && !refused:
				t.Errorf("the chip does not compile %q, so every direction it declares is checked against nothing", source)
			}
		})
	}
}

// TestOperandDirectionsMatchTheChip holds every direction the tables declare to
// what running the instruction does.
func TestOperandDirectionsMatchTheChip(t *testing.T) {
	ctx, harness := chiptest.Fixtures(t)
	for _, instruction := range ic10.Instructions {
		t.Run(instruction.Mnemonic, func(t *testing.T) {
			for position, operand := range instruction.Operands {
				if !operand.Accepts(ic10.OperandRegister) {
					// No spelling of the position names a register, so there is
					// nothing for a run to leave changed, and nothing to compare.
					continue
				}
				if reason, refused := refusedToCompile[instruction.Opcode]; refused {
					t.Logf("operand %d unchecked: the chip compiles no line for %s, because %s",
						position, instruction.Mnemonic, reason)
					continue
				}
				source := renderProbe(t, instruction, position)
				got, fault := probeOperand(ctx, t, harness, source)
				if needsMemoryHolder[instruction.Opcode] {
					// Executed rather than taken on trust, so that a fixture
					// which grew a memory array is a failure here and not a
					// position that went on being excused.
					if !missingMemory(fault) {
						t.Errorf("%q no longer faults for want of a memory holder, it %s, so its direction is being excused rather than unaskable",
							source, faultDescription(fault))
					}
					continue
				}
				if fault.Type != chip.ExcNone {
					t.Fatalf("%q faulted: %s", source, fault)
				}
				if want := operand.Direction; got.direction() != want {
					t.Errorf("running %s leaves operand %d %s, and the table declares it %s",
						source, position, useDescription(got), want)
				}
			}
		})
	}
}

// missingMemory reports the two faults a device that holds no memory array
// raises, which are the whole of what stops the family above being probed.
func missingMemory(fault chip.Fault) bool {
	return fault.Type == chip.ExcMemoryNotReadable || fault.Type == chip.ExcMemoryNotWriteable
}

func faultDescription(fault chip.Fault) string {
	if fault.Type == chip.ExcNone {
		return "ran to the end of the program"
	}
	return "faulted with " + fault.String()
}

func useDescription(u registerUse) string {
	switch u {
	case replaced:
		return "holding a value it did not start with, whatever it started with"
	case folded:
		return "holding a value that depends on what it started with"
	case leftAlone:
	}
	return "holding what it started with"
}

// probeOperand runs one line twice, differing only in what the probed
// register holds, and reports what happened to it: identical outputs mean
// the register was replaced, unchanged outputs mean it was left alone, and
// anything else means the new value depends on the old one. A run the chip
// stopped on returns the fault instead, for the caller to judge.
func probeOperand(ctx context.Context, t *testing.T, h *chip.FixtureHarness, source string) (registerUse, chip.Fault) {
	t.Helper()
	low, fault := runProbe(ctx, t, h, source, probeLow)
	if fault.Type != chip.ExcNone {
		return leftAlone, fault
	}
	high, fault := runProbe(ctx, t, h, source, probeHigh)
	if fault.Type != chip.ExcNone {
		return leftAlone, fault
	}

	switch {
	case math.Float64bits(low.after) == math.Float64bits(high.after):
		return replaced, fault
	case math.Float64bits(low.after) == math.Float64bits(low.before) &&
		math.Float64bits(high.after) == math.Float64bits(high.before):
		return leftAlone, fault
	}
	return folded, fault
}

// probeRun is one execution: what the probed register held before and after.
type probeRun struct{ before, after float64 }

// runProbe loads one line onto a fresh chip, runs exactly that line, and
// reports the probed register either side of it, with whatever the chip
// stopped on. A line that fails to compile is a t.Fatalf here, not a
// returned fault: TestTheTablesOperandCountCompiles is what names those,
// and reaching one here means the caller ignored it.
func runProbe(ctx context.Context, t *testing.T, h *chip.FixtureHarness, source string, probe float64) (probeRun, chip.Fault) {
	t.Helper()
	layOutWorld(ctx, t, h)
	if err := h.Load(ctx, source); err != nil {
		t.Fatalf("loading %q: %v", source, err)
	}
	// After loading, which resets the stack pointer.
	index := probeIndex(t)
	registers := make([]float64, ic10.NumRegisters)
	for r := range registers {
		registers[r] = probeLow
	}
	registers[ic10.RegSP] = probeStackPointer
	registers[index] = probe
	if err := h.SetRegisters(ctx, registers...); err != nil {
		t.Fatalf("seeding the register file for %q: %v", source, err)
	}
	// The generator is rearmed for each run rather than left where the last one
	// put it, because a reset keeps the sequence going. Two runs of a line that
	// draws from it would otherwise draw different numbers, and a register the
	// instruction replaced would read back as one it folded into.
	if err := h.SetRandomSeed(ctx, probeSeed); err != nil {
		t.Fatalf("arming the generator for %q: %v", source, err)
	}

	got, err := h.Step(ctx, 1)
	if err != nil {
		t.Fatalf("running %q: %v", source, err)
	}
	if got.Stop == chip.StopCompileError {
		t.Fatalf("the chip does not compile %q: %s", source, got.CompileError)
	}
	return probeRun{before: probe, after: got.Registers[index]}, got.Fault
}

// layOutWorld resets the chip and rebuilds the housing every probe runs
// among, so a store one line made cannot be what the next line reads.
func layOutWorld(ctx context.Context, t *testing.T, h *chip.FixtureHarness) {
	t.Helper()
	if err := h.Reset(ctx); err != nil {
		t.Fatalf("resetting the chip: %v", err)
	}
	for _, pin := range probePins {
		if err := h.AddDevice(ctx, pin); err != nil {
			t.Fatalf("putting a device on d%d: %v", pin, err)
		}
		if err := h.SetHashes(ctx, pin, probePrefabHash, probeNameHash); err != nil {
			t.Fatalf("giving d%d its hashes: %v", pin, err)
		}
	}
}

// compiles reports whether the chip accepts source, laying the world out first
// so that a refusal is the line and not the housing.
func compiles(ctx context.Context, t *testing.T, h *chip.FixtureHarness, source string) bool {
	t.Helper()
	layOutWorld(ctx, t, h)
	if err := h.Load(ctx, source); err != nil {
		t.Fatalf("loading %q: %v", source, err)
	}
	got, err := h.State(ctx)
	if err != nil {
		t.Fatalf("reading the chip's verdict on %q: %v", source, err)
	}
	return got.CompileError.Type == chip.ExcNone
}

// noProbe is the position renderProbe takes to write a line with nothing under
// probe, which is what a question about the whole line rather than about one of
// its operands needs.
const noProbe = -1

// renderProbe writes the line that puts the probed register in one position and
// something fixed in every other.
func renderProbe(t *testing.T, instruction ic10.Instruction, position int) string {
	t.Helper()
	fields := make([]string, 0, len(instruction.Operands)+1)
	fields = append(fields, instruction.Mnemonic)
	for i, operand := range instruction.Operands {
		if i == position {
			fields = append(fields, probeRegister)
			continue
		}
		fields = append(fields, fixedOperand(t, operand))
	}
	return strings.Join(fields, " ")
}

// fixedOperand is what a position that is not under probe is written as: a
// literal wherever the position takes one, so the probed register is the only
// one in the line, and a register of its own where nothing else will do.
func fixedOperand(t *testing.T, operand ic10.Operand) string {
	t.Helper()
	for _, kind := range operand.Kinds {
		switch kind {
		case ic10.OperandRegister:
			continue
		case ic10.OperandNumber:
			return strconv.Itoa(probeLow)
		case ic10.OperandInteger, ic10.OperandSlotIndex:
			return "0"
		case ic10.OperandDevice:
			return "d" + strconv.Itoa(probePin)
		case ic10.OperandRefID:
			return strconv.Itoa(probeLow)
		case ic10.OperandLogicType:
			return firstUsable(t, logicTypeNames())
		case ic10.OperandLogicSlotType:
			return firstUsable(t, slotTypeNames())
		case ic10.OperandBatchMode:
			return firstUsable(t, batchModeNames())
		case ic10.OperandReagentMode:
			return firstUsable(t, reagentModeNames())
		case ic10.OperandDeviceHash:
			return strconv.Itoa(probePrefabHash)
		case ic10.OperandNameHash:
			return strconv.Itoa(probeNameHash)
		case ic10.OperandString:
			return "probe"
		}
		t.Fatalf("operand kind %s has no probe spelling", kind)
	}
	return probeOther
}

// member is one enum entry reduced to what a probe needs of it.
type member struct {
	name       string
	deprecated bool
}

func logicTypeNames() []member {
	names := make([]member, len(ic10.LogicTypes))
	for i, entry := range ic10.LogicTypes {
		names[i] = member{name: entry.Name, deprecated: entry.Deprecated}
	}
	return names
}

func slotTypeNames() []member {
	names := make([]member, len(ic10.LogicSlotTypes))
	for i, entry := range ic10.LogicSlotTypes {
		names[i] = member{name: entry.Name, deprecated: entry.Deprecated}
	}
	return names
}

func batchModeNames() []member {
	names := make([]member, len(ic10.BatchModes))
	for i, entry := range ic10.BatchModes {
		names[i] = member{name: entry.Name, deprecated: entry.Deprecated}
	}
	return names
}

func reagentModeNames() []member {
	names := make([]member, len(ic10.ReagentModes))
	for i, entry := range ic10.ReagentModes {
		names[i] = member{name: entry.Name, deprecated: entry.Deprecated}
	}
	return names
}

// firstUsable names the enum member a probe writes, taken from the tables rather
// than spelled here so that a renamed member fails loudly.
func firstUsable(t *testing.T, members []member) string {
	t.Helper()
	for _, m := range members {
		if !m.deprecated {
			return m.name
		}
	}
	t.Fatal("every member of an operand enum is deprecated, so no probe can name one")
	return ""
}
