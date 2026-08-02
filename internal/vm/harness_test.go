package vm

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
)

// sameFloat compares two doubles by bit pattern, so that positive and negative
// zero are different results and no tolerance is ever applied. Two NaNs count
// as equal regardless of payload: neither implementation promises to carry a
// particular payload through arithmetic, so comparing payloads would report
// differences that are not behaviour.
func sameFloat(a, b float64) bool {
	if math.IsNaN(a) && math.IsNaN(b) {
		return true
	}
	return math.Float64bits(a) == math.Float64bits(b)
}

// instructionCase drives one program through the machine and compares the whole
// final state.
//
// wantRegisters and wantMemory list only what changed; every register and slot
// not listed must still hold what it started with, so the comparison covers all
// 18 registers and all 512 slots without spelling them out.
type instructionCase struct {
	name string
	op   ic10.Opcode

	source    string
	registers map[ic10.Register]float64
	memory    map[int]float64
	devices   []Device
	// detachNetwork removes the data network entirely, which is what makes the
	// batch instructions see a null list.
	detachNetwork bool

	// random replaces the source rand draws from, and clock replaces the game
	// clock sleep measures against. Both default to the machine's own.
	random func() float64
	clock  func() float32

	budget int
	ticks  int

	wantRegisters map[ic10.Register]float64
	wantMemory    map[int]float64
	// wantFault is the run time error the last tick must produce, or nil for a
	// clean run.
	wantFault *Fault
	// wantUnimplemented requires the tick to report a gap in this package
	// rather than a chip fault.
	wantUnimplemented bool
	// wantCompileError is the error Load must produce. When set, no tick runs.
	wantCompileError *Fault
	// wantPC, when set, is the program counter after the last tick.
	wantPC *int
	// wantExecuted, when set, is the instruction count of the last tick.
	wantExecuted *int
}

func runInstructionCase(t *testing.T, c instructionCase) {
	t.Helper()

	m := NewMachine()
	if len(c.devices) > 0 || c.detachNetwork {
		housing := NewHousing(c.devices...)
		if c.detachNetwork {
			housing.SetBatchList(nil)
		}
		m.SetHousing(housing)
	}

	m.SetRandom(c.random)
	m.SetClock(c.clock)

	ctx := context.Background()
	loadErr := m.Load(ctx, c.source)
	if c.wantCompileError != nil {
		checkFault(t, "compile error", loadErr, c.wantCompileError)
		return
	}
	if loadErr != nil {
		t.Fatalf("case %s: Load: %v", c.name, loadErr)
	}

	// State is set after loading because Load resets sp, so a case that starts
	// from a chosen stack pointer has to write it afterwards.
	initialRegisters := m.Registers()
	for r, v := range c.registers {
		initialRegisters[r] = v
	}
	m.SetRegisters(initialRegisters)

	initialMemory := m.Memory()
	for address, v := range c.memory {
		if address < 0 || address >= ic10.NumMemorySlots {
			t.Fatalf("case %s: initial memory address %d is outside the array", c.name, address)
		}
		initialMemory[address] = v
	}
	m.SetMemory(initialMemory)

	budget := c.budget
	if budget == 0 {
		budget = InstructionsPerTick
	}
	ticks := c.ticks
	if ticks == 0 {
		ticks = 1
	}
	var executed int
	var tickErr error
	for range ticks {
		executed, tickErr = m.Tick(ctx, budget)
	}
	if c.wantUnimplemented {
		if !errors.Is(tickErr, ErrUnimplemented) {
			t.Errorf("case %s: tick = %v, want an error wrapping ErrUnimplemented", c.name, tickErr)
		}
		var fault *Fault
		if errors.As(tickErr, &fault) {
			t.Errorf("case %s: an unimplemented path surfaced as the chip fault %v", c.name, fault)
		}
		return
	}
	checkFault(t, "fault", tickErr, c.wantFault)

	wantRegisters := initialRegisters
	for r, v := range c.wantRegisters {
		wantRegisters[r] = v
	}
	got := m.Registers()
	for r := range ic10.Register(ic10.NumRegisters) {
		if !sameFloat(got[r], wantRegisters[r]) {
			t.Errorf("case %s: %s = %v, want %v", c.name, r, got[r], wantRegisters[r])
		}
	}

	wantMemory := initialMemory
	for address, v := range c.wantMemory {
		if address < 0 || address >= ic10.NumMemorySlots {
			t.Fatalf("case %s: expected memory address %d is outside the array", c.name, address)
		}
		wantMemory[address] = v
	}
	gotMemory := m.Memory()
	for address := range ic10.NumMemorySlots {
		if !sameFloat(gotMemory[address], wantMemory[address]) {
			t.Errorf("case %s: memory[%d] = %v, want %v", c.name, address, gotMemory[address], wantMemory[address])
		}
	}

	if c.wantPC != nil && m.PC() != *c.wantPC {
		t.Errorf("case %s: PC = %d, want %d", c.name, m.PC(), *c.wantPC)
	}
	if c.wantExecuted != nil && executed != *c.wantExecuted {
		t.Errorf("case %s: executed = %d, want %d", c.name, executed, *c.wantExecuted)
	}
}

func checkFault(t *testing.T, label string, got error, want *Fault) {
	t.Helper()
	if want == nil {
		if got != nil {
			t.Errorf("%s = %v, want none", label, got)
		}
		return
	}
	if got == nil {
		t.Errorf("%s = none, want %v", label, want)
		return
	}
	var fault *Fault
	if !errors.As(got, &fault) {
		t.Errorf("%s = %v, want a *Fault", label, got)
		return
	}
	if fault.Type != want.Type || fault.Line != want.Line {
		t.Errorf("%s = %v, want %v", label, fault, want)
	}
}

// fakeDevice is a device with a fixed set of readable and writable properties,
// enough to exercise every device instruction without pretending to be a real
// piece of equipment.
//
// A property absent from readable makes CanLogicRead false, which is how a test
// reaches ExcIncorrectLogicType.
type fakeDevice struct {
	id       int
	prefab   int
	nameHash int

	values   map[ic10.LogicType]float64
	readable map[ic10.LogicType]bool
	writable map[ic10.LogicType]bool

	slots         map[slotKey]float64
	slotReadable  map[slotKey]bool
	slotWritable  map[slotKey]bool
	supportsSlots bool

	memory        []float64
	supportsMem   bool
	memoryCleared bool
}

type slotKey struct {
	slot int
	kind ic10.LogicSlotType
}

func newFakeDevice(id, prefab int) *fakeDevice {
	return &fakeDevice{
		id:           id,
		prefab:       prefab,
		values:       make(map[ic10.LogicType]float64),
		readable:     make(map[ic10.LogicType]bool),
		writable:     make(map[ic10.LogicType]bool),
		slots:        make(map[slotKey]float64),
		slotReadable: make(map[slotKey]bool),
		slotWritable: make(map[slotKey]bool),
	}
}

func (d *fakeDevice) withProperty(name string, value float64, canWrite bool) *fakeDevice {
	info, ok := ic10.LookupLogicType(name)
	if !ok {
		panic("unknown logic type in a test fixture: " + name)
	}
	d.values[info.Value] = value
	d.readable[info.Value] = true
	d.writable[info.Value] = canWrite
	return d
}

func (d *fakeDevice) withSlot(slot int, name string, value float64, canWrite bool) *fakeDevice {
	info, ok := ic10.LookupLogicSlotType(name)
	if !ok {
		panic("unknown logic slot type in a test fixture: " + name)
	}
	key := slotKey{slot: slot, kind: info.Value}
	d.slots[key] = value
	d.slotReadable[key] = true
	d.slotWritable[key] = canWrite
	d.supportsSlots = true
	return d
}

func (d *fakeDevice) withMemory(size int) *fakeDevice {
	d.memory = make([]float64, size)
	d.supportsMem = true
	return d
}

func (d *fakeDevice) ReferenceID() int { return d.id }
func (d *fakeDevice) PrefabHash() int  { return d.prefab }
func (d *fakeDevice) NameHash() int    { return d.nameHash }

func (d *fakeDevice) CanLogicRead(t ic10.LogicType) bool  { return d.readable[t] }
func (d *fakeDevice) CanLogicWrite(t ic10.LogicType) bool { return d.writable[t] }
func (d *fakeDevice) LogicValue(t ic10.LogicType) float64 { return d.values[t] }
func (d *fakeDevice) SetLogicValue(t ic10.LogicType, value float64) {
	d.values[t] = value
}

func (d *fakeDevice) CanSlotRead(t ic10.LogicSlotType, slot int) bool {
	return d.slotReadable[slotKey{slot: slot, kind: t}]
}

func (d *fakeDevice) SlotValue(t ic10.LogicSlotType, slot int) float64 {
	return d.slots[slotKey{slot: slot, kind: t}]
}

// slotWriterDevice adds ISlotWriteable to a fake device. It is a separate type
// so that a test can present a device that has slots but cannot be written to.
type slotWriterDevice struct{ *fakeDevice }

func (d slotWriterDevice) CanSlotWrite(t ic10.LogicSlotType, slot int) bool {
	return d.slotWritable[slotKey{slot: slot, kind: t}]
}

func (d slotWriterDevice) SetSlotValue(t ic10.LogicSlotType, slot int, value float64) {
	d.slots[slotKey{slot: slot, kind: t}] = value
}

// networkedDevice adds the game's network reference to a fake device, which is
// what a `d0:1` operand reaches. A device without it answers any network
// suffix with nothing at all, so the two have to be separate types.
type networkedDevice struct {
	*fakeDevice
	connected map[int]Device
}

func (d *networkedDevice) Network(index int) Device { return d.connected[index] }

// memoryDevice adds IMemoryReadable and IMemoryWritable to a fake device.
type memoryDevice struct{ *fakeDevice }

func (d memoryDevice) ReadMemory(address int) (float64, error) {
	if address < 0 {
		return 0, errStackUnderflow
	}
	if address >= len(d.memory) {
		return 0, errStackOverflow
	}
	return d.memory[address], nil
}

func (d memoryDevice) WriteMemory(address int, value float64) error {
	if address < 0 {
		return errStackUnderflow
	}
	if address >= len(d.memory) {
		return errStackOverflow
	}
	d.memory[address] = value
	return nil
}

func (d memoryDevice) ClearMemory() {
	for i := range d.memory {
		d.memory[i] = 0
	}
	d.memoryCleared = true
}
