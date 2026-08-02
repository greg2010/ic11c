package vm

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"strings"

	"github.com/greg2010/ic11c/internal/ic10"
)

// InstructionsPerTick is the budget CircuitHousing.Execute spends per tick.
// Exhausting it is not an error: the chip stops where it is and resumes at the
// same instruction on the next tick.
const InstructionsPerTick = 128

// Machine is one programmable chip: its register file, its 512 double array,
// its program counter and its error state.
//
// State is readable and writable from outside so a differential test can set a
// starting point and compare an ending one. Nothing about the chip is hidden
// behind execution; sp and ra are ordinary registers and the array push and
// poke share is the same array get and put reach through db.
//
// A Machine is not safe for concurrent use.
type Machine struct {
	registers [ic10.NumRegisters]float64
	memory    [ic10.NumMemorySlots]float64
	nextAddr  int

	errType ExceptionType
	errLine int
	// compileError blocks execution entirely until the source is replaced,
	// unlike a run time fault, which is retried every tick.
	compileError *Fault

	lines    []operation
	aliases  map[string]aliasValue
	jumpTags map[string]int
	defines  map[string]float64

	housing *Housing
	random  func() float64
	clock   func() float32

	// destroyed records that hcf ran. The game sets the chip on fire; there is
	// nothing to burn here, so the flag is the whole of it.
	destroyed bool
}

// NewMachine returns a chip in an empty housing, with no program loaded.
//
// Registers and memory start at zero, which real hardware does not: chip state
// survives power loss, chip removal and reflashing. Tests that care should set
// a starting state explicitly rather than relying on this.
func NewMachine() *Machine {
	m := &Machine{
		aliases:  make(map[string]aliasValue),
		jumpTags: make(map[string]int),
		defines:  make(map[string]float64),
	}
	m.SetHousing(NewHousing())
	source := rand.NewPCG(0x1c10, 0x1c10)
	generator := rand.New(source)
	m.random = generator.Float64
	m.clock = func() float32 { return 0 }
	return m
}

// SetHousing replaces the circuit housing, which is where device pins, the data
// network and the batch list live. The housing is given a back reference to the
// chip so that db resolves to this machine's own memory.
func (m *Machine) SetHousing(h *Housing) {
	if h == nil {
		h = NewHousing()
	}
	h.chip = m
	m.housing = h
}

// Housing returns the circuit housing, for attaching devices after construction.
func (m *Machine) Housing() *Housing { return m.housing }

// SetRandom replaces the source `rand` draws from. The default is a fixed seed
// generator so that a program using rand is still reproducible; the game uses
// an unseeded System.Random, which no oracle can match, so rand is the one
// instruction differential testing must either pin or avoid.
func (m *Machine) SetRandom(next func() float64) {
	if next != nil {
		m.random = next
	}
}

// SetClock replaces the game clock `sleep` measures against, in seconds. The
// default clock never advances, which makes sleep re-enter itself forever;
// that is deterministic but means a test exercising sleep must supply a clock.
func (m *Machine) SetClock(now func() float32) {
	if now != nil {
		m.clock = now
	}
}

// Register reads one register. r is not range checked because ic10.Register
// only spans the file.
func (m *Machine) Register(r ic10.Register) float64 { return m.registers[r] }

// SetRegister writes one register, including sp and ra, which have no hardware
// protection.
func (m *Machine) SetRegister(r ic10.Register, value float64) { m.registers[r] = value }

// Registers returns a snapshot of the whole file.
func (m *Machine) Registers() [ic10.NumRegisters]float64 { return m.registers }

// SetRegisters replaces the whole file.
func (m *Machine) SetRegisters(values [ic10.NumRegisters]float64) { m.registers = values }

// Memory returns a snapshot of all 512 slots.
func (m *Machine) Memory() [ic10.NumMemorySlots]float64 { return m.memory }

// SetMemory replaces all 512 slots.
func (m *Machine) SetMemory(values [ic10.NumMemorySlots]float64) { m.memory = values }

// MemoryAt reads one slot, reporting false for an address outside the array.
func (m *Machine) MemoryAt(address int) (float64, bool) {
	if address < 0 || address >= ic10.NumMemorySlots {
		return 0, false
	}
	return m.memory[address], true
}

// SetMemoryAt writes one slot, reporting false for an address outside the array.
func (m *Machine) SetMemoryAt(address int, value float64) bool {
	if address < 0 || address >= ic10.NumMemorySlots {
		return false
	}
	m.memory[address] = value
	return true
}

// PC is the line the next tick will run.
func (m *Machine) PC() int { return m.nextAddr }

// SetPC moves the program counter. A value outside the program makes the next
// tick a no-op, which is what the chip does when it runs off the end.
func (m *Machine) SetPC(line int) { m.nextAddr = line }

// LineCount is how many lines compiled. A compile error truncates the program
// at the offending line, so this can be shorter than the source.
func (m *Machine) LineCount() int { return len(m.lines) }

// Fault returns the run time error currently recorded, or nil. A fault is
// retried on the following tick and clears itself if the cause goes away, so
// this reflects the last tick rather than a latched condition.
func (m *Machine) Fault() *Fault {
	if m.errType == ExcNone {
		return nil
	}
	return &Fault{Type: m.errType, Line: m.errLine}
}

// CompileError returns the error that stopped compilation, or nil. Unlike a
// fault it blocks execution entirely until Load is called again.
func (m *Machine) CompileError() *Fault { return m.compileError }

// Destroyed reports whether hcf has run. The game destroys the chip and starts
// a fire; here it only latches, so that a test can tell the difference between
// hcf and any other way of reaching ExcChipCatchingFire.
func (m *Machine) Destroyed() bool { return m.destroyed }

// Load compiles source, replacing any program already present. It is
// ProgrammableChip.SetSourceCode.
//
// Compilation checks arity, unknown mnemonics, duplicate labels, duplicate
// defines and the preprocessor forms, and nothing else. Bad registers, out of
// range device pins and invalid logic types all compile cleanly and fault at
// run time, which is why operand validation belongs to the compiler rather
// than to this function.
//
// A compile error stops at the offending line, leaving the lines before it
// loaded and blocking execution. The returned error is the same *Fault
// CompileError reports.
//
// Load resets sp, the aliases, the defines and the jump tags. It does not reset
// the other registers or memory, matching a chip that has just been reflashed.
func (m *Machine) Load(ctx context.Context, source string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("load: %w", err)
	}
	m.lines = nil
	m.aliases = make(map[string]aliasValue)
	m.defines = make(map[string]float64)
	m.jumpTags = make(map[string]int)
	m.errType, m.errLine = ExcNone, 0
	m.compileError = nil
	m.registers[ic10.RegSP] = 0

	if err := m.installBuiltinAliases(); err != nil {
		m.compileError = asFault(err, 0)
		return m.compileError
	}

	for i, text := range strings.Split(source, "\n") {
		op, err := compileLine(m, text, i)
		if err != nil {
			m.compileError = asFault(err, i)
			return m.compileError
		}
		m.lines = append(m.lines, op)
	}
	m.nextAddr = 0
	return nil
}

// installBuiltinAliases runs the three alias operations SetSourceCode executes
// before compiling anything, which is what makes db, sp and ra resolve at all.
func (m *Machine) installBuiltinAliases() error {
	builtins := []struct{ name, target string }{
		{"db", fmt.Sprintf("d%d", BaseUnitIndex)},
		{"sp", fmt.Sprintf("r%d", ic10.RegSP)},
		{"ra", fmt.Sprintf("r%d", ic10.RegRA)},
	}
	for _, builtin := range builtins {
		op, err := newAliasOperation(m, 0, builtin.name, builtin.target)
		if err != nil {
			return err
		}
		if _, err := op.execute(0); err != nil {
			return err
		}
	}
	return nil
}

// Tick runs up to budget instructions, which is InstructionsPerTick in game.
// It is ProgrammableChip.Execute.
//
// It returns how many instructions were taken from the budget, counting the one
// that faulted, and the fault if there was one. Running out of budget is not an
// error: the chip stops where it is and the next tick resumes there. A fault
// rewinds the program counter to the faulting line, so the next tick retries it
// and the error clears by itself if the cause has gone.
//
// A program counter past the last line or below zero stops the chip for good,
// with no fault and no error state. A jump reaching either is how a program
// ends.
//
// A compile error blocks the tick entirely; the returned error is the compile
// error and no instruction runs.
//
// An instruction this package does not model returns an error wrapping
// ErrUnimplemented rather than a Fault, so that a gap cannot be mistaken for
// the chip failing.
func (m *Machine) Tick(ctx context.Context, budget int) (executed int, err error) {
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("tick: %w", err)
	}
	if m.compileError != nil {
		return 0, m.compileError
	}
	if m.nextAddr < 0 || m.nextAddr >= len(m.lines) {
		return 0, nil
	}
	// The budget is spent in the loop condition, so exhausting it leaves the
	// program counter where it is and returns normally.
	for remaining := budget; remaining > 0 && m.nextAddr >= 0 && m.nextAddr < len(m.lines); remaining-- {
		current := m.nextAddr
		executed++
		after, opErr := m.lines[current].execute(current)
		if opErr != nil {
			if errors.Is(opErr, ErrUnimplemented) {
				// Not a chip behaviour, so it must not be dressed up as one.
				// The program counter rewinds as it would for a fault, but the
				// error state is left alone: there is no answer to record.
				m.nextAddr = current
				return executed, fmt.Errorf("line %d: %w", current, opErr)
			}
			fault := asFault(opErr, current)
			m.errType, m.errLine = fault.Type, fault.Line
			m.nextAddr = current
			return executed, fault
		}
		m.nextAddr = after.next
		m.errType, m.errLine = ExcNone, 0
		if after.endTick {
			return executed, nil
		}
	}
	return executed, nil
}

// Chip as a device. The housing hands the chip back for db, which is how get,
// put and clr through db reach the same array as push and poke.

// ReferenceID is zero because this package models no world object registry. A
// chip cannot be addressed by id.
func (m *Machine) ReferenceID() int { return 0 }

// PrefabHash is zero, so a chip never matches a batch selector.
func (m *Machine) PrefabHash() int { return 0 }

// NameHash is zero, so a chip never matches a name filtered batch.
func (m *Machine) NameHash() int { return 0 }

// CanLogicRead reports true only for LineNumber. The game's chip also inherits
// the whole item property set from its base class, which this package does not
// model; reading anything else faults with ExcIncorrectLogicType rather than
// returning a wrong number.
func (m *Machine) CanLogicRead(t ic10.LogicType) bool { return t == logicTypeLineNumber }

// CanLogicWrite reports false for everything. See CanLogicRead.
func (m *Machine) CanLogicWrite(ic10.LogicType) bool { return false }

// LogicValue answers LineNumber with the program counter and everything else
// with zero, matching the game's read of an unsupported property.
func (m *Machine) LogicValue(t ic10.LogicType) float64 {
	if t == logicTypeLineNumber {
		return float64(m.nextAddr)
	}
	return 0
}

// SetLogicValue does nothing: no chip property is writable. CanLogicWrite gates
// every caller, so reaching this is not a silent drop.
func (m *Machine) SetLogicValue(ic10.LogicType, float64) {}

// CanSlotRead reports false: a chip has no slots.
func (m *Machine) CanSlotRead(ic10.LogicSlotType, int) bool { return false }

// SlotValue returns zero: a chip has no slots.
func (m *Machine) SlotValue(ic10.LogicSlotType, int) float64 { return 0 }

// ReadMemory is ProgrammableChip.ReadMemory. The faults it produces are raw:
// `get` passes them straight through, so an out of range address reaches the
// caller as a stack fault only because nothing rewrites it.
func (m *Machine) ReadMemory(address int) (float64, error) {
	if address < 0 {
		return 0, errStackUnderflow
	}
	if address >= ic10.NumMemorySlots {
		return 0, errStackOverflow
	}
	return m.memory[address], nil
}

// WriteMemory is ProgrammableChip.WriteMemory.
func (m *Machine) WriteMemory(address int, value float64) error {
	if address < 0 {
		return errStackUnderflow
	}
	if address >= ic10.NumMemorySlots {
		return errStackOverflow
	}
	m.memory[address] = value
	return nil
}

// ClearMemory zeroes all 512 slots, the whole of what `clr db` does.
func (m *Machine) ClearMemory() {
	m.memory = [ic10.NumMemorySlots]float64{}
}

// logicTypeLineNumber is the one property the chip itself answers.
var logicTypeLineNumber = func() ic10.LogicType {
	info, ok := ic10.LookupLogicType("LineNumber")
	if !ok {
		// The generated tables always carry it; a build without it would make
		// every db logic read wrong rather than merely unsupported.
		return ic10.LogicType(math.MaxUint16)
	}
	return info.Value
}()
