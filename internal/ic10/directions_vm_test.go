package ic10_test

import (
	"context"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/vm"
)

// The operand directions in the generated tables say what an instruction does to
// the register an operand names. internal/vm is a hand transliteration of the
// same game code, written from the C# rather than from these tables, and it is
// the only other statement of the same fact in the tree. What follows asks it,
// by running each instruction over a register whose starting value is the only
// thing that changes between two runs, and comparing what came back against what
// the table declares.
//
// Nothing here reasons from the shape of an operand list. The shape is what the
// register allocator used to guess a destination from, and a check built on it
// would only restate the one cmd/isagen already runs over the same JSON.

// The values the probed register starts each pair of runs with. They differ, so
// a register the instruction replaced ends both runs the same and a register it
// left alone ends them different; they are small, so a probe standing in an
// address, a shift distance or a bit offset is still in range; and each is the
// reference id of a device on the housing, so a probe standing in a device
// position resolves rather than faulting. The help text does not always say
// which positions those are -- clrd spells its device operand num -- so both
// values have to serve every position.
const (
	probeLow  = 3
	probeHigh = 5
)

const (
	probePrefabHash = 4242
	probeNameHash   = 2424
)

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

// probeDevice answers every question a device is asked, so that an instruction
// reaching a device runs to completion instead of faulting. What it answers with
// does not matter: the probe reads the register file, not the device.
type probeDevice struct{ id int }

func (d probeDevice) ReferenceID() int                            { return d.id }
func (probeDevice) PrefabHash() int                               { return probePrefabHash }
func (probeDevice) NameHash() int                                 { return probeNameHash }
func (probeDevice) CanLogicRead(ic10.LogicType) bool              { return true }
func (probeDevice) CanLogicWrite(ic10.LogicType) bool             { return true }
func (probeDevice) LogicValue(ic10.LogicType) float64             { return 0 }
func (probeDevice) SetLogicValue(ic10.LogicType, float64)         {}
func (probeDevice) CanSlotRead(ic10.LogicSlotType, int) bool      { return true }
func (probeDevice) SlotValue(ic10.LogicSlotType, int) float64     { return 0 }
func (probeDevice) CanSlotWrite(ic10.LogicSlotType, int) bool     { return true }
func (probeDevice) SetSlotValue(ic10.LogicSlotType, int, float64) {}
func (probeDevice) ReagentValue(int, int) float64                 { return 0 }
func (probeDevice) ReagentRecipe(float64) float64                 { return 0 }
func (probeDevice) ReadMemory(int) (float64, error)               { return 0, nil }
func (probeDevice) WriteMemory(int, float64) error                { return nil }
func (probeDevice) ClearMemory()                                  {}

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

// TestOperandDirectionsMatchTheInterpreter holds every direction the tables
// declare to what running the instruction does.
//
// An instruction the interpreter cannot be asked about is one no operand count
// assembles, which internal/vm reports for itself; there is nothing to compare
// for those and they are named rather than skipped quietly.
func TestOperandDirectionsMatchTheInterpreter(t *testing.T) {
	for _, instruction := range ic10.Instructions {
		t.Run(instruction.Mnemonic, func(t *testing.T) {
			support, known := vm.SupportFor(instruction.Opcode)
			if !known {
				t.Fatalf("the interpreter has no record of %s", instruction.Mnemonic)
			}
			for position, operand := range instruction.Operands {
				if !operand.Accepts(ic10.OperandRegister) {
					// No spelling of the position names a register, so no
					// direction other than reading is available to it.
					if operand.Direction != ic10.DirectionRead {
						t.Errorf("operand %d accepts no register and is declared %s", position, operand.Direction)
					}
					continue
				}
				if support.ParseImpossible {
					t.Logf("operand %d unchecked: no operand count assembles %s, so the interpreter cannot be asked",
						position, instruction.Mnemonic)
					continue
				}
				got := probeOperand(t, instruction, position)
				if want := operand.Direction; got.direction() != want {
					t.Errorf("running %s leaves operand %d %s, and the table declares it %s",
						renderProbe(t, instruction, position), position, useDescription(got), want)
				}
			}
		})
	}
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

// probeOperand runs one instruction twice over the same operands, differing only
// in what the probed register holds, and reports what happened to that register.
//
// It answers from what the register held afterwards, so it sees writing and not
// reading. Two shapes therefore come back as untouched: a position the
// instruction only reads, which is the same answer and not a mistake, and a fold
// that leaves both probe values where it found them, which is one. The three
// cases below are how the answer is reached rather than a partition of what an
// instruction can do, which is why this is a check on the table and not a
// source for it.
func probeOperand(t *testing.T, instruction ic10.Instruction, position int) registerUse {
	t.Helper()
	source := renderProbe(t, instruction, position)
	low := runProbe(t, source, probeLow)
	high := runProbe(t, source, probeHigh)

	switch {
	case math.Float64bits(low.after) == math.Float64bits(high.after):
		return replaced
	case math.Float64bits(low.after) == math.Float64bits(low.before) &&
		math.Float64bits(high.after) == math.Float64bits(high.before):
		return leftAlone
	}
	return folded
}

// probeRun is one execution: what the probed register held before and after.
type probeRun struct{ before, after float64 }

// runProbe loads one line, runs exactly that line, and reports the probed
// register either side of it.
//
// A line that will not assemble or that faults leaves nothing to compare, and a
// register nothing wrote reads the same as one the instruction left alone, so
// either is a failure of this probe rather than a direction.
func runProbe(t *testing.T, source string, probe float64) probeRun {
	t.Helper()
	machine := vm.NewMachine()
	machine.SetHousing(vm.NewHousing(probeDevice{id: probeLow}, probeDevice{id: probeHigh}))
	if err := machine.Load(context.Background(), source); err != nil {
		t.Fatalf("%q does not assemble: %v", source, err)
	}

	// After loading, which resets the stack pointer.
	index := probeIndex(t)
	registers := machine.Registers()
	for r := range registers {
		registers[r] = probeLow
	}
	registers[ic10.RegSP] = probeStackPointer
	registers[index] = probe
	machine.SetRegisters(registers)

	if _, err := machine.Tick(context.Background(), 1); err != nil {
		t.Fatalf("%q faulted: %v", source, err)
	}
	return probeRun{before: probe, after: machine.Register(index)}
}

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
			return "d0"
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
