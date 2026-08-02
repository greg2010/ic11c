package vm

import (
	"math"

	"github.com/greg2010/ic11c/internal/ic10"
)

const pumpPrefab = 1234

func pump(id int, setting float64) *fakeDevice {
	d := newFakeDevice(id, pumpPrefab)
	d.withProperty("Setting", setting, true)
	d.withProperty("On", 1, false)
	return d
}

func namedPump(id int, setting float64, nameHash int) *fakeDevice {
	d := pump(id, setting)
	d.nameHash = nameHash
	return d
}

var deviceCases = []instructionCase{
	{
		name: "l reads a device property", op: ic10.OpL,
		devices:       []Device{pump(101, 5)},
		source:        "l r0 d0 Setting",
		wantRegisters: map[ic10.Register]float64{0: 5},
	},
	{
		name: "l resolves a logic type by its numeric value", op: ic10.OpL,
		devices:       []Device{pump(101, 5)},
		source:        "l r0 d0 12",
		wantRegisters: map[ic10.Register]float64{0: 5},
	},
	{
		name: "l rejects an unreadable property", op: ic10.OpL,
		devices:   []Device{pump(101, 5)},
		source:    "l r0 d0 Temperature",
		wantFault: &Fault{Type: ExcIncorrectLogicType, Line: 0},
	},
	{
		name: "l rejects LogicType None", op: ic10.OpL,
		devices:   []Device{pump(101, 5)},
		source:    "l r0 d0 None",
		wantFault: &Fault{Type: ExcLogicTypeIsNone, Line: 0},
	},
	{
		name: "l on an empty pin is a device not found", op: ic10.OpL,
		devices:   []Device{pump(101, 5)},
		source:    "l r0 d1 Setting",
		wantFault: &Fault{Type: ExcDeviceNotFound, Line: 0},
	},
	{
		// The pin operand accepts d0 through d9 but the housing has six sockets,
		// so the array read runs off the end and the chip reports Unknown.
		name: "a pin past the housing is an unknown error", op: ic10.OpL,
		devices:   []Device{pump(101, 5)},
		source:    "l r0 d9 Setting",
		wantFault: &Fault{Type: ExcUnknown, Line: 0},
	},
	{
		name: "s writes a device property", op: ic10.OpS,
		devices:       []Device{pump(101, 5)},
		source:        "s d0 Setting 7\nl r0 d0 Setting",
		wantRegisters: map[ic10.Register]float64{0: 7},
	},
	{
		name: "s rejects an unwritable property", op: ic10.OpS,
		devices:   []Device{pump(101, 5)},
		source:    "s d0 On 1",
		wantFault: &Fault{Type: ExcIncorrectLogicType, Line: 0},
	},
	{
		name: "ld reads by reference id", op: ic10.OpLd,
		devices:       []Device{pump(101, 5)},
		source:        "ld r0 101 Setting",
		wantRegisters: map[ic10.Register]float64{0: 5},
	},
	{
		// ld does not check for a missing device before using it, so the chip
		// reports Unknown rather than DeviceNotFound.
		name: "ld with an unknown id is an unknown error", op: ic10.OpLd,
		devices:   []Device{pump(101, 5)},
		source:    "ld r0 999 Setting",
		wantFault: &Fault{Type: ExcUnknown, Line: 0},
	},
	{
		name: "sd writes by reference id", op: ic10.OpSd,
		devices:       []Device{pump(101, 5)},
		source:        "sd 101 Setting 7\nl r0 d0 Setting",
		wantRegisters: map[ic10.Register]float64{0: 7},
	},
	{
		name: "sd with an unknown id is a device not found", op: ic10.OpSd,
		devices:   []Device{pump(101, 5)},
		source:    "sd 999 Setting 7",
		wantFault: &Fault{Type: ExcDeviceNotFound, Line: 0},
	},
	{
		name: "a register holding a reference id addresses a device", op: ic10.OpL,
		devices:       []Device{pump(101, 5)},
		registers:     map[ic10.Register]float64{3: 101},
		source:        "l r0 r3 Setting",
		wantRegisters: map[ic10.Register]float64{0: 5},
	},
	{
		name: "an indirect pin reference reads the register for its socket", op: ic10.OpL,
		devices:       []Device{pump(101, 5), pump(102, 9)},
		registers:     map[ic10.Register]float64{3: 1},
		source:        "l r0 dr3 Setting",
		wantRegisters: map[ic10.Register]float64{0: 9},
	},
	{
		name: "sdse and sdns report whether a pin is occupied", op: ic10.OpSdse,
		devices:       []Device{pump(101, 5)},
		source:        "sdse r0 d0\nsdns r1 d0\nsdse r2 d1\nsdns r3 d1",
		wantRegisters: map[ic10.Register]float64{0: 1, 1: 0, 2: 0, 3: 1},
	},
	{
		name: "sdns alone", op: ic10.OpSdns,
		devices:       []Device{pump(101, 5)},
		source:        "sdns r0 d1",
		wantRegisters: map[ic10.Register]float64{0: 1},
	},
	{
		name: "bdse branches when the pin is occupied", op: ic10.OpBdse,
		devices:       []Device{pump(101, 5)},
		source:        "bdse d0 2\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 5},
	},
	{
		name: "bdns branches when the pin is empty", op: ic10.OpBdns,
		devices:       []Device{pump(101, 5)},
		source:        "bdns d1 2\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 5},
	},
	{
		name: "brdse is relative", op: ic10.OpBrdse,
		devices:       []Device{pump(101, 5)},
		source:        "add r0 r0 1\nbrdse d0 2\nadd r0 r0 10\nadd r0 r0 100",
		wantRegisters: map[ic10.Register]float64{0: 101},
	},
	{
		name: "brdns is relative", op: ic10.OpBrdns,
		devices:       []Device{pump(101, 5)},
		source:        "add r0 r0 1\nbrdns d1 2\nadd r0 r0 10\nadd r0 r0 100",
		wantRegisters: map[ic10.Register]float64{0: 101},
	},
	{
		name: "bdseal links when it branches", op: ic10.OpBdseal,
		devices:       []Device{pump(101, 5)},
		source:        "bdseal d0 2\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 5, ic10.RegRA: 1},
	},
	{
		name: "bdnsal links when it branches", op: ic10.OpBdnsal,
		devices:       []Device{pump(101, 5)},
		source:        "bdnsal d1 2\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 5, ic10.RegRA: 1},
	},
	{
		name: "bdnvl branches when the property cannot be read", op: ic10.OpBdnvl,
		devices:       []Device{pump(101, 5)},
		source:        "bdnvl d0 Temperature 2\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 5},
	},
	{
		name: "bdnvs branches when the property cannot be written", op: ic10.OpBdnvs,
		devices:       []Device{pump(101, 5)},
		source:        "bdnvs d0 On 2\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 5},
	},
	{
		name: "ls reads a slot property", op: ic10.OpLs,
		devices:       []Device{withSlot(pump(101, 5), 0, "Occupied", 1, false)},
		source:        "ls r0 d0 0 Occupied",
		wantRegisters: map[ic10.Register]float64{0: 1},
	},
	{
		name: "ss writes a slot property", op: ic10.OpSs,
		devices:       []Device{slotWriterDevice{withSlot(pump(101, 5), 0, "Quantity", 1, true)}},
		source:        "ss d0 0 Quantity 4\nls r0 d0 0 Quantity",
		wantRegisters: map[ic10.Register]float64{0: 4},
	},
	{
		name: "ss on a device with no slot writing is rejected", op: ic10.OpSs,
		devices:   []Device{withSlot(pump(101, 5), 0, "Quantity", 1, true)},
		source:    "ss d0 0 Quantity 4",
		wantFault: &Fault{Type: ExcDeviceNotSlotWriteable, Line: 0},
	},
	{
		name: "lb takes the maximum across a batch", op: ic10.OpLb,
		devices:       []Device{pump(101, 3), pump(102, 9)},
		source:        "lb r0 1234 Setting Maximum",
		wantRegisters: map[ic10.Register]float64{0: 9},
	},
	{
		name: "lb sums a batch", op: ic10.OpLb,
		devices:       []Device{pump(101, 3), pump(102, 9)},
		source:        "lb r0 1234 Setting Sum",
		wantRegisters: map[ic10.Register]float64{0: 12},
	},
	{
		name: "lb counts a batch", op: ic10.OpLb,
		devices:       []Device{pump(101, 3), pump(102, 9)},
		source:        "lb r0 1234 Setting Count",
		wantRegisters: map[ic10.Register]float64{0: 2},
	},
	{
		// A batch that matches nothing does not fault, and each mode answers
		// with a different value. Testing the result against zero is wrong.
		name: "an empty batch answers NaN for average", op: ic10.OpLb,
		devices:       []Device{pump(101, 3)},
		source:        "lb r0 999 Setting Average",
		wantRegisters: map[ic10.Register]float64{0: math.NaN()},
	},
	{
		name: "an empty batch answers zero for sum and minimum", op: ic10.OpLb,
		devices:       []Device{pump(101, 3)},
		source:        "lb r0 999 Setting Sum\nlb r1 999 Setting Minimum",
		wantRegisters: map[ic10.Register]float64{0: 0, 1: 0},
	},
	{
		name: "an empty batch answers negative infinity for maximum", op: ic10.OpLb,
		devices:       []Device{pump(101, 3)},
		source:        "lb r0 999 Setting Maximum",
		wantRegisters: map[ic10.Register]float64{0: math.Inf(-1)},
	},
	{
		name: "a detached data network makes a batch read fault", op: ic10.OpLb,
		detachNetwork: true,
		source:        "lb r0 1234 Setting Average",
		wantFault:     &Fault{Type: ExcDeviceListNull, Line: 0},
	},
	{
		name: "lbn narrows a batch by name hash", op: ic10.OpLbn,
		devices:       []Device{namedPump(101, 3, 77), namedPump(102, 9, 88)},
		source:        "lbn r0 1234 88 Setting Maximum",
		wantRegisters: map[ic10.Register]float64{0: 9},
	},
	{
		name: "lbs aggregates a slot property", op: ic10.OpLbs,
		devices:       []Device{withSlot(pump(101, 3), 0, "Quantity", 4, false), withSlot(pump(102, 9), 0, "Quantity", 6, false)},
		source:        "lbs r0 1234 0 Quantity Sum",
		wantRegisters: map[ic10.Register]float64{0: 10},
	},
	{
		name: "lbns narrows a slot batch by name hash", op: ic10.OpLbns,
		devices:       []Device{withName(withSlot(pump(101, 3), 0, "Quantity", 4, false), 77), withName(withSlot(pump(102, 9), 0, "Quantity", 6, false), 88)},
		source:        "lbns r0 1234 88 0 Quantity Sum",
		wantRegisters: map[ic10.Register]float64{0: 6},
	},
	{
		name: "sb writes across a batch", op: ic10.OpSb,
		devices:       []Device{pump(101, 3), pump(102, 9)},
		source:        "sb 1234 Setting 4\nl r0 d0 Setting\nl r1 d1 Setting",
		wantRegisters: map[ic10.Register]float64{0: 4, 1: 4},
	},
	{
		name: "sbn writes across a name filtered batch", op: ic10.OpSbn,
		devices:       []Device{namedPump(101, 3, 77), namedPump(102, 9, 88)},
		source:        "sbn 1234 88 Setting 4\nl r0 d0 Setting\nl r1 d1 Setting",
		wantRegisters: map[ic10.Register]float64{0: 3, 1: 4},
	},
	{
		name: "sbs writes a slot property across a batch", op: ic10.OpSbs,
		devices:       []Device{slotWriterDevice{withSlot(pump(101, 3), 0, "Quantity", 1, true)}},
		source:        "sbs 1234 0 Quantity 5\nls r0 d0 0 Quantity",
		wantRegisters: map[ic10.Register]float64{0: 5},
	},
	{
		name: "get and put reach another device's memory", op: ic10.OpGet,
		devices:       []Device{memoryDevice{withMemory(pump(101, 0), 8)}},
		source:        "put d0 2 42\nget r0 d0 2",
		wantRegisters: map[ic10.Register]float64{0: 42},
	},
	{
		name: "getd and putd address memory by reference id", op: ic10.OpGetd,
		devices:       []Device{memoryDevice{withMemory(pump(101, 0), 8)}},
		source:        "putd 101 2 42\ngetd r0 101 2",
		wantRegisters: map[ic10.Register]float64{0: 42},
	},
	{
		name: "putd alone", op: ic10.OpPutd,
		devices:       []Device{memoryDevice{withMemory(pump(101, 0), 8)}},
		source:        "putd 101 1 7\ngetd r0 101 1",
		wantRegisters: map[ic10.Register]float64{0: 7},
	},
	{
		name: "get from a device with no memory is rejected", op: ic10.OpGet,
		devices:   []Device{pump(101, 0)},
		source:    "get r0 d0 0",
		wantFault: &Fault{Type: ExcMemoryNotReadable, Line: 0},
	},
	{
		name: "put to a device with no memory is rejected", op: ic10.OpPut,
		devices:   []Device{pump(101, 0)},
		source:    "put d0 0 1",
		wantFault: &Fault{Type: ExcMemoryNotWriteable, Line: 0},
	},
	{
		name: "clrd zeroes another device's memory by reference id", op: ic10.OpClrd,
		devices:       []Device{memoryDevice{withMemory(pump(101, 0), 8)}},
		source:        "putd 101 3 5\nclrd 101\ngetd r0 101 3",
		wantRegisters: map[ic10.Register]float64{0: 0},
	},
	{
		// clr and clrd disagree about which fault an unclearable device raises.
		name: "clr on a device with no memory reports memory not readable", op: ic10.OpClr,
		devices:   []Device{pump(101, 0)},
		source:    "clr d0",
		wantFault: &Fault{Type: ExcMemoryNotReadable, Line: 0},
	},
	{
		name: "clrd on a device with no memory reports memory not writeable", op: ic10.OpClrd,
		devices:   []Device{pump(101, 0)},
		source:    "clrd 101",
		wantFault: &Fault{Type: ExcMemoryNotWriteable, Line: 0},
	},
	{
		name: "lr reports that reagent mixtures are not modelled", op: ic10.OpLr,
		devices:           []Device{pump(101, 0)},
		source:            "lr r0 d0 Contents 1",
		wantUnimplemented: true,
	},
	{
		name: "lr rejects an undefined reagent mode", op: ic10.OpLr,
		devices:   []Device{pump(101, 0)},
		source:    "lr r0 d0 99 1",
		wantFault: &Fault{Type: ExcUnhandledReagentMode, Line: 0},
	},
	{
		name: "rmap reports that reagent recipes are not modelled", op: ic10.OpRmap,
		devices:           []Device{pump(101, 0)},
		source:            "rmap r0 d0 1",
		wantUnimplemented: true,
	},
	{
		name: "a network suffix on a pin reads the connected device", op: ic10.OpL,
		devices:       []Device{networked(pump(101, 5), 1, pump(201, 42))},
		source:        "l r0 d0:1 Setting",
		wantRegisters: map[ic10.Register]float64{0: 42},
	},
	{
		name: "a network suffix on a reference id reads the connected device", op: ic10.OpL,
		devices:       []Device{networked(pump(101, 5), 1, pump(201, 42))},
		registers:     map[ic10.Register]float64{3: 101},
		source:        "l r0 r3:1 Setting",
		wantRegisters: map[ic10.Register]float64{0: 42},
	},
	{
		name: "a network suffix naming no connection resolves to no device", op: ic10.OpL,
		devices:   []Device{networked(pump(101, 5), 1, pump(201, 42))},
		source:    "l r0 d0:2 Setting",
		wantFault: &Fault{Type: ExcDeviceNotFound, Line: 0},
	},
	{
		// A device with no network reference answers any suffix with nothing,
		// which is what an unconnected pin does in game.
		name: "a network suffix on a plain device resolves to no device", op: ic10.OpL,
		devices:   []Device{pump(101, 5)},
		source:    "l r0 d0:1 Setting",
		wantFault: &Fault{Type: ExcDeviceNotFound, Line: 0},
	},
	{
		name: "a network suffix on a plain device's reference id resolves to no device", op: ic10.OpL,
		devices:   []Device{pump(101, 5)},
		registers: map[ic10.Register]float64{3: 101},
		source:    "l r0 r3:1 Setting",
		wantFault: &Fault{Type: ExcDeviceNotFound, Line: 0},
	},
	{
		// The suffix parse has no error path: what it cannot read is the base
		// network, which means the device on the pin.
		name: "an unparseable network suffix reads the device itself", op: ic10.OpL,
		devices:       []Device{networked(pump(101, 5), 1, pump(201, 42))},
		source:        "l r0 d0:x Setting",
		wantRegisters: map[ic10.Register]float64{0: 5},
	},
	{
		name: "a network suffix on db resolves to no device", op: ic10.OpGet,
		source:    "get r0 db:1 0",
		wantFault: &Fault{Type: ExcDeviceNotFound, Line: 0},
	},
	{
		// The indirect form indexes the register file before checking, so a
		// starting index that cannot exist is a host exception.
		name: "an indirect pin whose starting index cannot exist is an unknown error", op: ic10.OpL,
		devices:   []Device{pump(101, 5)},
		source:    "l r0 drr99 Setting",
		wantFault: &Fault{Type: ExcUnknown, Line: 0},
	},
	{
		// The resolved index is out of range, the device operand carries it with
		// exceptions off, and the housing faults on the array read instead.
		name: "an indirect pin resolving out of range is an unknown error", op: ic10.OpL,
		devices:   []Device{pump(101, 5)},
		registers: map[ic10.Register]float64{3: -1},
		source:    "l r0 dr3 Setting",
		wantFault: &Fault{Type: ExcUnknown, Line: 0},
	},
	{
		// alias is the one caller that resolves a device index with exceptions
		// on, so it is where the out of device bounds fault surfaces.
		name: "aliasing an indirect pin resolving out of range is out of device bounds", op: ic10.OpAlias,
		devices:   []Device{pump(101, 5)},
		registers: map[ic10.Register]float64{3: 99},
		source:    "alias p dr3",
		wantFault: &Fault{Type: ExcOutOfDeviceBounds, Line: 0},
	},
	{
		name: "an alias naming a register reads the device that register's id names", op: ic10.OpL,
		devices:       []Device{pump(101, 5)},
		registers:     map[ic10.Register]float64{3: 101},
		source:        "alias p r3\nl r0 p Setting",
		wantRegisters: map[ic10.Register]float64{0: 5},
	},
	{
		name: "an alias naming a register holding no id resolves to no device", op: ic10.OpL,
		devices:   []Device{pump(101, 5)},
		source:    "alias p r3\nl r0 p Setting",
		wantFault: &Fault{Type: ExcDeviceNotFound, Line: 1},
	},
	{
		name: "an unknown alias in a device position is an incorrect variable", op: ic10.OpL,
		devices:   []Device{pump(101, 5)},
		source:    "l r0 nosuch Setting",
		wantFault: &Fault{Type: ExcIncorrectVariable, Line: 0},
	},
	{
		// The enum operand form truncates a register, where the integer and
		// line number forms round it to even. LogicType 12 is Setting and 13 is
		// not a property this device answers, so the two are distinguishable.
		name: "a logic type held in a register truncates", op: ic10.OpL,
		devices:       []Device{pump(101, 5)},
		registers:     map[ic10.Register]float64{1: 12.7},
		source:        "l r0 d0 r1",
		wantRegisters: map[ic10.Register]float64{0: 5},
	},
	{
		name: "a defined logic type truncates", op: ic10.OpL,
		devices:       []Device{pump(101, 5)},
		source:        "define lt 12.7\nl r0 d0 lt",
		wantRegisters: map[ic10.Register]float64{0: 5},
	},
	{
		// The same 0.7 in a slot index, which is the integer operand form: the
		// register arm rounds it to slot 1 and the define arm truncates it to
		// slot 0.
		name: "a slot index held in a register rounds to even", op: ic10.OpLs,
		devices:       []Device{withSlot(pump(101, 5), 1, "Occupied", 1, false)},
		registers:     map[ic10.Register]float64{1: 0.7},
		source:        "ls r0 d0 r1 Occupied",
		wantRegisters: map[ic10.Register]float64{0: 1},
	},
	{
		name: "a defined slot index truncates", op: ic10.OpLs,
		devices:   []Device{withSlot(pump(101, 5), 1, "Occupied", 1, false)},
		source:    "define s 0.7\nls r0 d0 s Occupied",
		wantFault: &Fault{Type: ExcIncorrectLogicType, Line: 1},
	},
	{
		name: "lb takes the minimum across a batch", op: ic10.OpLb,
		devices:       []Device{pump(101, 3), pump(102, 9)},
		source:        "lb r0 1234 Setting Minimum",
		wantRegisters: map[ic10.Register]float64{0: 3},
	},
	{
		// The minimum comparison is negated so that a NaN reading replaces the
		// running value rather than being skipped.
		name: "a NaN reading wins the minimum", op: ic10.OpLb,
		devices:       []Device{pump(101, math.NaN())},
		source:        "lb r0 1234 Setting Minimum",
		wantRegisters: map[ic10.Register]float64{0: math.NaN()},
	},
	{
		// An undefined mode falls off the game's switch, leaving the accumulator
		// at zero. It is not an error and it is not Average.
		name: "an undefined batch mode answers zero", op: ic10.OpLb,
		devices:       []Device{pump(101, 3), pump(102, 9)},
		source:        "lb r0 1234 Setting 256",
		wantRegisters: map[ic10.Register]float64{0: 0},
	},
}

func withSlot(d *fakeDevice, slot int, name string, value float64, canWrite bool) *fakeDevice {
	return d.withSlot(slot, name, value, canWrite)
}

func withName(d *fakeDevice, nameHash int) *fakeDevice {
	d.nameHash = nameHash
	return d
}

func withMemory(d *fakeDevice, size int) *fakeDevice {
	return d.withMemory(size)
}

// networked puts one device behind a network reference on another, which is
// what a `d0:1` operand walks.
func networked(d *fakeDevice, index int, connected Device) *networkedDevice {
	return &networkedDevice{fakeDevice: d, connected: map[int]Device{index: connected}}
}
