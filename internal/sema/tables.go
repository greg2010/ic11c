package sema

import "hash/crc32"

// Tables resolves the machine names an intrinsic argument may write: logic
// types, slot types, batch modes, and reagent modes, generated from the
// game's own assembly ([Shipped] is the production implementation). Device
// pins are not here; see device.go.
type Tables interface {
	// LogicType resolves a logic type name such as "Pressure".
	LogicType(name string) (Member, bool)
	// LogicSlotType resolves a slot property name such as "Occupied".
	LogicSlotType(name string) (Member, bool)
	// BatchMode resolves a batch mode name: Average, Sum, Minimum, Maximum, or
	// Count.
	BatchMode(name string) (Member, bool)
	// ReagentMode resolves a reagent mode name: Contents, Required, Recipe, or
	// TotalContents.
	ReagentMode(name string) (Member, bool)

	// Prefab resolves what a batch instruction's prefab hash names. A false
	// answer means this game build ships nothing under that number, which is
	// the strongest statement available about a hash: the batch forms report
	// nothing for one that matches no device, they simply reach none.
	Prefab(hash int32) (Prefab, bool)
	// PrefabNamed resolves what a __ic_hash argument names, case-sensitively,
	// as the hash is.
	PrefabNamed(name string) (Prefab, bool)
}

// Direction is which way a batch instruction touches a device property: the
// load forms read and the store forms write.
type Direction uint8

const (
	Reading Direction = iota
	Writing
)

// Prefab is what one game build says a completed device of one kind answers
// for: the thing a batch instruction's prefab hash names.
type Prefab interface {
	// Name is the prefab's own name, which is what a program hashes.
	Name() string
	// Title is the English name the game shows, empty for the few things the
	// game ships no title for. It is a reading aid for a diagnostic: a roster
	// name is not what the device is called in the game.
	Title() string
	// NumSlots is how many slots the device declares, which bounds the slot
	// index a batch slot operand may carry.
	NumSlots() int
	// HoldsCircuit reports whether the thing can hold a programmable chip,
	// which is what makes it reachable as db, and known whether the extraction
	// decided that at all. A false known is not a denial, and nothing may read
	// it as one.
	HoldsCircuit() (holds, known bool)
	// NumModes is how many settings the device's Mode property selects between,
	// and known reports whether the extraction recovered them at all. A device
	// that fills its mode names at run time answers false, and nothing may
	// conclude a mode number is out of range for one.
	NumModes() (count int, known bool)
	// RefusesLogic reports whether a completed device is known to answer
	// nothing for logicType in dir. logicType is the encoding [Member.Value]
	// carries.
	RefusesLogic(logicType int, dir Direction) bool
	// RefusesSlot is [Prefab.RefusesLogic] for one slot's own properties. slot
	// is below NumSlots.
	RefusesSlot(slot, slotType int, dir Direction) bool
}

// Member is one resolved machine name. Deprecated is carried because the
// game marks some logic types retired while still resolving every one of
// them; nothing about the emitted program changes, so it is the programmer,
// not the compiler, that has to act on it.
type Member struct {
	// Value is the integer the machine encodes the name as.
	Value int
	// Deprecated reports whether the game's own tables list the member as
	// retired.
	Deprecated bool
}

// hashString is the compile-time value of __ic_hash: the CRC-32 of the
// literal's bytes reinterpreted as a signed 32-bit integer, which is how the
// game derives a prefab hash from a name.
func hashString(s string) int64 {
	return int64(int32(crc32.ChecksumIEEE([]byte(s))))
}
