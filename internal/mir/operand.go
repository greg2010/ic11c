package mir

import (
	"fmt"
	"strconv"

	"github.com/greg2010/ic11c/internal/ic10"
)

// satisfiesValue reports whether kind names a position the machine fills from
// a runtime value, which both a literal and a register can supply.
//
// The chip's help text spells these positions int, num, id, deviceHash,
// nameHash and slotIndex, but resolves all of them by reading a double, so a
// register is legal in every one — which is what makes j ra the documented
// return sequence even though j is written "j int". The enum positions are
// not on this list: those resolve when the line is assembled, so only a
// literal or a member name will do.
func satisfiesValue(kind ic10.OperandKind) bool {
	switch kind {
	case ic10.OperandNumber, ic10.OperandInteger, ic10.OperandRefID,
		ic10.OperandDeviceHash, ic10.OperandNameHash, ic10.OperandSlotIndex:
		return true
	case ic10.OperandRegister, ic10.OperandDevice, ic10.OperandLogicType,
		ic10.OperandLogicSlotType, ic10.OperandBatchMode, ic10.OperandReagentMode,
		ic10.OperandString:
	}
	return false
}

// Operand is one positional argument of an instruction. The set is closed:
// implementations live in this package only, because every form has to be
// renderable by the emitter and rewritable by the register allocator.
type Operand interface {
	// Satisfies reports whether the operand may appear in a position accepting
	// kind. Most forms satisfy several kinds, since the machine draws no
	// distinction between, say, a slot index and any other number.
	Satisfies(kind ic10.OperandKind) bool
	// String renders the operand for diagnostics. It is not the emitted form:
	// the emitter renders logic types numerically and labels as line numbers.
	String() string
	operand()
}

// VirtReg is a virtual register. Register allocation replaces every one of
// these with a PhysReg; an emitter that meets one reports that allocation has
// not run.
type VirtReg struct {
	// ID is unique within the function that created it. Func.NewVirtReg is the
	// only thing that should hand these out.
	ID uint32
}

func (VirtReg) operand() {}
func (VirtReg) Satisfies(kind ic10.OperandKind) bool {
	return kind == ic10.OperandRegister || satisfiesValue(kind)
}
func (v VirtReg) String() string { return "vr" + strconv.FormatUint(uint64(v.ID), 10) }

// PhysReg is a machine register, r0 through r15 plus sp and ra.
type PhysReg struct {
	Reg ic10.Register
}

func (PhysReg) operand() {}
func (PhysReg) Satisfies(kind ic10.OperandKind) bool {
	return kind == ic10.OperandRegister || satisfiesValue(kind)
}
func (p PhysReg) String() string { return p.Reg.String() }

// Imm is a literal value. Every register and memory slot on this machine holds
// an IEEE double, so there is no separate integer immediate: a slot index, a
// prefab hash, and an arithmetic constant are all doubles.
type Imm struct {
	Value float64
}

func (Imm) operand()                             {}
func (Imm) Satisfies(kind ic10.OperandKind) bool { return satisfiesValue(kind) }
func (i Imm) String() string                     { return strconv.FormatFloat(i.Value, 'g', -1, 64) }

// Label names a branch target by the Label of the block it refers to. Targets
// are named rather than pointed at so that a block can branch to one the
// builder has not created yet.
type Label struct {
	Name string
}

func (Label) operand() {}

// Satisfies accepts both the int and the num positions. The chip's help text
// spells a jump target int for j and jal but num for the comparing branches,
// and a label resolves to a line number either way.
func (Label) Satisfies(kind ic10.OperandKind) bool {
	return kind == ic10.OperandInteger || kind == ic10.OperandNumber
}

func (l Label) String() string { return l.Name }

// DeviceKind distinguishes the spellings of a device operand.
type DeviceKind uint8

const (
	// DeviceBase is db, the housing the chip is inserted into.
	DeviceBase DeviceKind = iota
	// DevicePin is d0 through d5.
	DevicePin
)

// Device is a device pin operand.
//
// A register or a literal holding a ReferenceId is not a Device; those are a
// PhysReg or an Imm, which the relevant operand positions also accept.
type Device struct {
	Kind DeviceKind
	// Pin is the pin index, used by DevicePin only.
	Pin uint8
	// Conn is the network connection index of the "d0:1" form, and
	// NoConnection where none was written, which addresses the device itself.
	Conn int64
}

// NoConnection is Device.Conn for an operand naming no network connection.
const NoConnection = -1

// NewDeviceBase builds the db operand, which reaches the housing the chip is
// inserted into and therefore the chip's own memory.
func NewDeviceBase() Device {
	return Device{Kind: DeviceBase, Conn: NoConnection}
}

// NewDevicePin builds d0 through d5, optionally through the network connection
// index conn, and rejects a pin the housing does not have. Pass NoConnection
// for the plain form, which addresses the device on the pin itself.
func NewDevicePin(pin uint8, conn int64) (Device, error) {
	if pin >= ic10.NumDevicePins {
		return Device{}, fmt.Errorf("device pin: d%d is outside d0-d%d", pin, ic10.NumDevicePins-1)
	}
	if conn < NoConnection {
		return Device{}, fmt.Errorf("device pin: d%d has connection index %d, which is negative", pin, conn)
	}
	return Device{Kind: DevicePin, Pin: pin, Conn: conn}, nil
}

func (Device) operand()                             {}
func (Device) Satisfies(kind ic10.OperandKind) bool { return kind == ic10.OperandDevice }

func (d Device) String() string {
	switch d.Kind {
	case DeviceBase:
		return "db"
	case DevicePin:
		text := "d" + strconv.FormatUint(uint64(d.Pin), 10)
		if d.Conn > NoConnection {
			text += ":" + strconv.FormatInt(d.Conn, 10)
		}
		return text
	default:
		return "Device(" + strconv.FormatUint(uint64(d.Kind), 10) + ")"
	}
}

// LogicType selects a device property for the l, s, and batch forms.
type LogicType struct {
	Value ic10.LogicType
}

func (LogicType) operand()                             {}
func (LogicType) Satisfies(kind ic10.OperandKind) bool { return kind == ic10.OperandLogicType }
func (l LogicType) String() string {
	return "LogicType(" + strconv.FormatUint(uint64(l.Value), 10) + ")"
}

// LogicSlotType selects a slot property.
type LogicSlotType struct {
	Value ic10.LogicSlotType
}

func (LogicSlotType) operand()                             {}
func (LogicSlotType) Satisfies(kind ic10.OperandKind) bool { return kind == ic10.OperandLogicSlotType }
func (l LogicSlotType) String() string {
	return "LogicSlotType(" + strconv.FormatUint(uint64(l.Value), 10) + ")"
}

// BatchMode selects the aggregation a batch load applies.
type BatchMode struct {
	Value ic10.BatchMode
}

func (BatchMode) operand()                             {}
func (BatchMode) Satisfies(kind ic10.OperandKind) bool { return kind == ic10.OperandBatchMode }
func (b BatchMode) String() string {
	return "BatchMode(" + strconv.FormatUint(uint64(b.Value), 10) + ")"
}

// ReagentMode selects which reagent quantity lr reads.
type ReagentMode struct {
	Value ic10.ReagentMode
}

func (ReagentMode) operand()                             {}
func (ReagentMode) Satisfies(kind ic10.OperandKind) bool { return kind == ic10.OperandReagentMode }
func (r ReagentMode) String() string {
	return "ReagentMode(" + strconv.FormatUint(uint64(r.Value), 10) + ")"
}
