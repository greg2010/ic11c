// Package ic10 describes the Stationeers IC10 target: its register file, its
// instruction set, and the operand enums instructions are parameterised by.
//
// The tables in tables.gen.go are extracted from the game assembly by
// cmd/isagen and are authoritative over any published documentation. The types
// here are hand written and are what the tables populate. Behavioural defects
// that no extraction can express live in quirks.go.
//
// Nothing in this package is safe to mutate. The tables are package level
// slices for cheap indexed access, not a defensive copy per caller.
package ic10

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Opcode identifies an instruction. Its value is the ScriptCommand ordinal in
// the game assembly and doubles as the index into Instructions.
type Opcode uint16

// Instruction returns the entry describing op, and reports false for an opcode
// outside the table.
func (op Opcode) Instruction() (Instruction, bool) {
	if int(op) >= len(Instructions) {
		return Instruction{}, false
	}
	return Instructions[op], true
}

func (op Opcode) String() string {
	instruction, ok := op.Instruction()
	if !ok {
		return "Opcode(" + strconv.FormatUint(uint64(op), 10) + ")"
	}
	return instruction.Mnemonic
}

// Instruction is one mnemonic the chip's assembler accepts.
//
// Deprecated instructions still assemble and run; the flag mirrors the game's
// own list and exists so the compiler can prefer the current spelling.
// Unemittable reports the stronger condition, that the backend must never
// select the instruction at all.
type Instruction struct {
	Mnemonic   string
	Opcode     Opcode
	Deprecated bool
	// Example is the game's own help text with its colour markup stripped,
	// for instance `add r? a(r?|num) b(r?|num)`.
	Example string
	// Operands lists the positional operands in order, and is nil for the
	// operandless forms. Its length is the arity the chip's assembler
	// enforces, which is one of the few things it does check at compile time.
	Operands []Operand
}

// Operand describes one positional operand.
type Operand struct {
	// Name is the variable name the game's help text gives the operand, such
	// as "address" or "a". It is empty for the unnamed forms.
	Name string
	// Kinds lists the accepted alternatives in the order the help text shows
	// them. It always holds at least one entry.
	Kinds []OperandKind
	// Direction is what the instruction does to the register the operand
	// names. It is extracted from the game's own operation classes rather
	// than inferred from the operand's position.
	Direction Direction
}

// Accepts reports whether the operand admits a value of the given kind.
func (o Operand) Accepts(kind OperandKind) bool {
	return slices.Contains(o.Kinds, kind)
}

// Direction is what an instruction does to the register an operand names.
//
// The zero value is DirectionUnknown so that an operand built anywhere other
// than the generated table claims nothing. Register allocation reading a
// direction wrongly produces working-looking code that has lost a value, with
// no diagnostic anywhere; refusing an operand that says nothing is the only
// failure mode that reports itself.
type Direction uint8

const (
	// DirectionUnknown is an operand whose direction the extraction could not
	// read out of the game source. It is never a licence to assume either
	// answer.
	DirectionUnknown Direction = iota
	// DirectionRead is an operand the instruction only consumes.
	DirectionRead
	// DirectionWrite is an operand whose register the instruction assigns
	// without reading what was there.
	DirectionWrite
	// DirectionReadWrite is an operand whose register the instruction reads and
	// then assigns, so the value live in it before the instruction is one of the
	// instruction's inputs.
	DirectionReadWrite
)

var directionNames = [...]string{
	DirectionUnknown:   "unknown",
	DirectionRead:      "read",
	DirectionWrite:     "write",
	DirectionReadWrite: "readwrite",
}

func (d Direction) String() string {
	if int(d) >= len(directionNames) {
		return "Direction(" + strconv.FormatUint(uint64(d), 10) + ")"
	}
	return directionNames[d]
}

// WriteIndex reports the operand position the instruction assigns a register
// through, or -1 when it assigns none.
//
// It fails rather than answering where one index cannot carry the whole truth:
// an operand of unknown direction, more than one written operand, or an operand
// the instruction reads before assigning. A caller treats the index as a
// definition with no accompanying use, so answering with a read-write position
// would let allocation believe a live value is dead. Supporting one means
// reading Operands directly and modelling the use as well.
func (i Instruction) WriteIndex() (int, error) {
	written := -1
	for position, operand := range i.Operands {
		switch operand.Direction {
		case DirectionRead:
			continue
		case DirectionWrite:
			if written >= 0 {
				return 0, fmt.Errorf("%s writes operands %d and %d", i.Mnemonic, written, position)
			}
			written = position
			continue
		case DirectionReadWrite:
			return 0, fmt.Errorf("%s reads and writes operand %d, which one write index cannot express", i.Mnemonic, position)
		case DirectionUnknown:
		}
		// Undetermined and anything outside the four both mean the table says
		// nothing usable, and refusing is the only answer that reports itself.
		return 0, fmt.Errorf("%s operand %d has direction %s", i.Mnemonic, position, operand.Direction)
	}
	return written, nil
}

// OperandKind is a class of value an operand position accepts.
type OperandKind uint8

// The operand kinds, one per token the game's help text is built from.
const (
	// OperandRegister is r0 through r15, sp, ra, or an indirect rr form.
	OperandRegister OperandKind = iota
	// OperandNumber is any literal, named constant, or defined name.
	OperandNumber
	// OperandInteger is a jump target: a line number, label, or defined name.
	OperandInteger
	// OperandDevice is db or d0 through d5, optionally with a network
	// connection suffix, or an indirect dr form.
	OperandDevice
	// OperandRefID is a register or literal holding a device ReferenceId.
	OperandRefID
	// OperandLogicType is a LogicTypes member, by name or by value.
	OperandLogicType
	// OperandLogicSlotType is a LogicSlotTypes member, by name or by value.
	OperandLogicSlotType
	// OperandBatchMode is a BatchModes member, by name or by value.
	OperandBatchMode
	// OperandReagentMode is a ReagentModes member, by name or by value.
	OperandReagentMode
	// OperandDeviceHash is a prefab hash selecting a batch of devices.
	OperandDeviceHash
	// OperandNameHash is a name hash narrowing a device batch.
	OperandNameHash
	// OperandSlotIndex is a slot number on a device.
	OperandSlotIndex
	// OperandString is a bare word, used only by the preprocessor forms.
	OperandString
)

// operandKindTokens is indexed by OperandKind and holds the spelling the
// game's help text uses.
var operandKindTokens = [...]string{
	OperandRegister:      "r?",
	OperandNumber:        "num",
	OperandInteger:       "int",
	OperandDevice:        "d?",
	OperandRefID:         "id",
	OperandLogicType:     "logicType",
	OperandLogicSlotType: "logicSlotType",
	OperandBatchMode:     "batchMode",
	OperandReagentMode:   "reagentMode",
	OperandDeviceHash:    "deviceHash",
	OperandNameHash:      "nameHash",
	OperandSlotIndex:     "slotIndex",
	OperandString:        "str",
}

func (k OperandKind) String() string {
	if int(k) >= len(operandKindTokens) {
		return "OperandKind(" + strconv.FormatUint(uint64(k), 10) + ")"
	}
	return operandKindTokens[k]
}

// LogicType is a device property selector. Generated code emits the numeric
// value rather than the name: they resolve identically and the number is far
// cheaper against the 4096 byte program budget.
type LogicType uint16

// LogicSlotType is a slot property selector.
type LogicSlotType uint8

// BatchMode is an aggregation mode for the batch load forms.
//
// The game backs the mode with a plain int, so this is int32 rather than the
// byte the five defined members would fit in. A mode the game does not define
// stays undefined; narrowing would fold 256 onto Average and leave the program
// reading the wrong aggregate with no diagnostic.
//
// A batch read that matches no device does not report an error. It returns NaN
// for Average, zero for Sum and Minimum, and negative infinity for Maximum, so
// testing the result against zero is wrong; test for NaN.
type BatchMode int32

// ReagentMode selects which reagent quantity lr reads.
//
// Backed by a plain int in the game, for the reason given on [BatchMode].
// Narrowing would fold an undefined mode onto Contents.
type ReagentMode int32

// LogicTypeInfo is one LogicTypes entry. Deprecated members still resolve and
// still work.
type LogicTypeInfo struct {
	Name       string
	Value      LogicType
	Deprecated bool
}

// LogicSlotTypeInfo is one LogicSlotTypes entry.
type LogicSlotTypeInfo struct {
	Name       string
	Value      LogicSlotType
	Deprecated bool
}

// BatchModeInfo is one BatchModes entry.
type BatchModeInfo struct {
	Name       string
	Value      BatchMode
	Deprecated bool
}

// ReagentModeInfo is one ReagentModes entry.
type ReagentModeInfo struct {
	Name       string
	Value      ReagentMode
	Deprecated bool
}

// PrefabInfo is one Prefabs entry: a thing the game ships, described by
// everything a chip can reach about it.
//
// The logic and slot surfaces describe a *completed* device. The game refuses
// every property of a structure still under construction, so a table that
// reported that state would say no to everything.
type PrefabInfo struct {
	// Name is the prefab's own name, which the game hashes. Hash is the number
	// a batch instruction operand carries; the chip's assembler resolves no
	// prefab name of its own.
	Name string
	Hash int32
	// Title is the English name the game shows, for diagnostics that a person
	// reads. It is empty for the few prefabs the game ships no title for.
	Title string
	// CircuitHolder reports whether the thing can hold a programmable chip,
	// which is what makes it reachable as db rather than only through a pin.
	CircuitHolder bool
	// CircuitHolderUnknown says whether the thing holds a chip could not be
	// recovered, which is not the same as its holding none. Nothing may
	// conclude the thing is unreachable as db while it is set.
	CircuitHolderUnknown bool
	// Slots lists the declared slots, indexed by the slot number an ls or ss
	// operand carries.
	Slots []SlotInfo
	// Modes names the settings the Mode property selects between, indexed by
	// the number a program writes. It is empty for a thing with no mode state,
	// and also when ModesUnknown is set.
	Modes []string
	// ModesUnknown says the thing has mode state whose names could not be
	// recovered, which is not the same as having none. Nothing may conclude a
	// mode number is out of range while it is set.
	ModesUnknown bool

	// logic lists the properties l, s and the batch forms reach, in LogicType
	// order. A property absent from it is one the device answers nothing for.
	//
	// It is unexported so that [PrefabInfo.RefusesRead] and
	// [PrefabInfo.RefusesWrite] are the only ways to ask about a property. See
	// [access].
	logic []logicAccess
}

// RefusesRead reports whether a completed device of this prefab is known to
// answer nothing when a program reads logicType, and RefusesWrite whether it is
// known to accept no write of one.
//
// These two are the only questions the table answers about a property, and both
// are phrased as refusals rather than as permissions on purpose. 3500 of the
// 13426 extracted entries record that the game settles the direction from live
// state, and a permission query would have to report every one of those as
// denied. They refuse nothing here, so a program touching one produces no
// diagnostic at all.
//
// A true answer describes a completed device, which is the only state the table
// models. It says nothing about a structure still under construction, which
// refuses everything.
func (p PrefabInfo) RefusesRead(logicType LogicType) bool {
	return p.accessFor(logicType).refusesRead()
}

// RefusesWrite is [PrefabInfo.RefusesRead] for the other direction.
func (p PrefabInfo) RefusesWrite(logicType LogicType) bool {
	return p.accessFor(logicType).refusesWrite()
}

func (p PrefabInfo) accessFor(logicType LogicType) access {
	for _, entry := range p.logic {
		if entry.logicType == logicType {
			return entry.allows
		}
	}
	return accessNone
}

// SlotInfo is one slot a prefab declares.
type SlotInfo struct {
	// Class is the Slot.Class member naming what the slot accepts, such as
	// "GasCanister". Half the slot properties are readable purely by virtue of
	// it. It is the name of what an ls Class read returns as a number.
	Class string

	// types lists the slot properties reachable on this slot, in LogicSlotType
	// order. Unexported for the reason PrefabInfo.logic is.
	types []slotAccess
}

// RefusesRead and RefusesWrite are [PrefabInfo.RefusesRead] for one slot's own
// properties, and are phrased as refusals for the same reason.
func (s SlotInfo) RefusesRead(slotType LogicSlotType) bool {
	return s.accessFor(slotType).refusesRead()
}

// RefusesWrite is [SlotInfo.RefusesRead] for the other direction.
func (s SlotInfo) RefusesWrite(slotType LogicSlotType) bool {
	return s.accessFor(slotType).refusesWrite()
}

func (s SlotInfo) accessFor(slotType LogicSlotType) access {
	for _, entry := range s.types {
		if entry.slotType == slotType {
			return entry.allows
		}
	}
	return accessNone
}

// logicAccess pairs a device property with the directions the game allows.
type logicAccess struct {
	logicType LogicType
	allows    access
}

// slotAccess pairs a slot property with the directions the game allows.
type slotAccess struct {
	slotType LogicSlotType
	allows   access
}

// access is the pair of directions the game allows on a property.
//
// The game names no such thing: it answers CanLogicRead and CanLogicWrite
// separately and calls the pair nothing. This is that pair and no more.
//
// It does not leave this package, and neither does any field holding one. A
// consumer that could hold one could write `a == accessReadWrite`, which reads
// accessUnknown as a denial and would reject working programs from the very
// data that exists to stop that. What leaves the package is the four refusal
// queries, which cannot express that mistake.
type access uint8

const (
	// accessNone is a property the device does not answer for. It is what a
	// lookup returns for a property absent from a prefab's table.
	accessNone access = iota
	accessRead
	accessWrite
	accessReadWrite
	// accessUnknown is a property whose direction the game decides from live
	// state -- what a logic transmitter is currently pointed at, whether a pipe
	// connection is made -- which no reading of the game's code settles.
	accessUnknown
)

// refusesRead reports whether the pair rules out a read, and refusesWrite
// whether it rules out a write.
//
// Both name every case that answers true rather than negating the cases that
// answer false. accessUnknown falls past them, and so does an access a later
// extraction introduces before these two learn about it, so a pair nothing
// decided refuses nothing.
func (a access) refusesRead() bool { return a == accessNone || a == accessWrite }

func (a access) refusesWrite() bool { return a == accessNone || a == accessRead }

func (a access) String() string {
	switch a {
	case accessNone:
		return "none"
	case accessRead:
		return "read"
	case accessWrite:
		return "write"
	case accessReadWrite:
		return "readwrite"
	case accessUnknown:
		return "unknown"
	default:
		return "access(" + strconv.FormatUint(uint64(a), 10) + ")"
	}
}

// Constant is a numeric literal the chip's assembler accepts wherever a number
// is accepted.
//
// Values come from the game and are not recomputed. deg2rad and rad2deg are
// float precision literals widened to double, so folding them at full double
// precision diverges from the game, and epsilon is the smallest positive
// subnormal rather than a comparison tolerance.
type Constant struct {
	Name  string
	Value float64
}

// Register file. All 18 registers hold doubles and none is protected by
// hardware; sp and ra are ordinary registers that instructions happen to use.
const (
	// NumRegisters counts the whole file, r0 through r15 plus sp and ra. It is
	// the length of ProgrammableChip._Registers.
	NumRegisters = 18
	// NumGeneralRegisters counts r0 through r15, and bounds the one range
	// restriction the machine puts on a register: a register holding a device
	// ReferenceId resolves within them only.
	//
	// Indirect referencing is not bounded by it. An rr form indexes the whole
	// register array, so an index of 16 or 17 reaches sp or ra, and only one
	// outside 0 through 17 faults.
	NumGeneralRegisters = 16
	// RegSP is the stack pointer used by push, pop, and peek. Keeping it
	// clear of the data region is the register allocator's job, since both
	// share one 512 slot array.
	RegSP Register = 16
	// RegRA is where jal writes the return address.
	RegRA Register = 17
)

// NumMemorySlots is the length of ProgrammableChip._Stack, the chip's one
// double array. push, pop, peek, poke, get and put all address it, and so does
// a get or put through db, so the data region a compiler lays out and the call
// frames a program pushes share it with no hardware boundary between them.
const NumMemorySlots = 512

// NumDevicePins is how many device pins a housing has, d0 through d5, and is
// the length of CircuitHousing.Devices. The housing itself is reached by db,
// which is not a pin.
//
// d6 through d9 assemble and then index a six element array, faulting once per
// tick with no error naming the pin, so nothing downstream catches a pin past
// this.
const NumDevicePins = 6

// NumParsedDevicePins is how many `d`-prefixed pin names the chip's assembler
// resolves. Its pin pattern accepts a single digit, so d0 through d9 all parse
// as device operands even though only the first NumDevicePins are sockets.
const NumParsedDevicePins = 10

const (
	// SlotBits is the width of everything the machine holds. A register and a
	// memory slot are both one IEEE double, and there is nothing narrower: an
	// integer of any other width is a type the machine cannot represent.
	SlotBits = 64
	// SlotBytes is that width in the byte-addressed model LLVM states offsets
	// in. The chip addresses slots and has no byte addressing of its own, so
	// this is the stride a byte offset divides by to become a slot index.
	SlotBytes = SlotBits / 8
)

// Register is an index into the register file.
type Register uint8

func (r Register) String() string {
	switch {
	case r == RegSP:
		return "sp"
	case r == RegRA:
		return "ra"
	case r < NumGeneralRegisters:
		return "r" + strconv.FormatUint(uint64(r), 10)
	default:
		return "Register(" + strconv.FormatUint(uint64(r), 10) + ")"
	}
}

// ParseRegister resolves a register name, accepting the sp and ra aliases
// alongside r0 through r17. It reports false for anything else, including the
// indirect rr forms, which name a register only at runtime.
//
// What follows the r goes through the same integer parse the chip uses, which
// admits a leading sign and leading zeros: `r05` and `r+5` both name r5. Those
// spellings are accepted rather than intended, and nothing should emit them.
func ParseRegister(name string) (Register, bool) {
	switch name {
	case "sp":
		return RegSP, true
	case "ra":
		return RegRA, true
	}
	digits, ok := strings.CutPrefix(name, "r")
	if !ok || digits == "" {
		return 0, false
	}
	index, err := strconv.Atoi(digits)
	if err != nil || index < 0 || index >= NumRegisters {
		return 0, false
	}
	return Register(index), true
}

// Indexes over the generated tables. Go initialises these after the tables
// they depend on, so they are ready before any exported lookup can run.
var (
	instructionsByMnemonic = indexBy(Instructions, func(i Instruction) string { return i.Mnemonic })
	logicTypesByName       = indexBy(LogicTypes, func(t LogicTypeInfo) string { return t.Name })
	slotTypesByName        = indexBy(LogicSlotTypes, func(t LogicSlotTypeInfo) string { return t.Name })
	batchModesByName       = indexBy(BatchModes, func(m BatchModeInfo) string { return m.Name })
	reagentModesByName     = indexBy(ReagentModes, func(m ReagentModeInfo) string { return m.Name })
	constantsByName        = indexBy(Constants, func(c Constant) string { return strings.ToLower(c.Name) })
	prefabsByName          = indexBy(Prefabs, func(p PrefabInfo) string { return p.Name })
	prefabsByHash          = indexPrefabsByHash()
	reservedWords          = buildReservedWords()
)

func indexBy[T any](entries []T, key func(T) string) map[string]T {
	m := make(map[string]T, len(entries))
	for _, entry := range entries {
		m[key(entry)] = entry
	}
	return m
}

// LookupInstruction resolves a mnemonic. Mnemonics are lower case and match
// case-sensitively, as the chip's assembler requires.
func LookupInstruction(mnemonic string) (Instruction, bool) {
	instruction, ok := instructionsByMnemonic[mnemonic]
	return instruction, ok
}

// LookupLogicType resolves a device property name. The chip matches these
// case-sensitively, so a name differing only in case is not the same property.
func LookupLogicType(name string) (LogicTypeInfo, bool) {
	info, ok := logicTypesByName[name]
	return info, ok
}

// LookupLogicSlotType resolves a slot property name, case-sensitively.
func LookupLogicSlotType(name string) (LogicSlotTypeInfo, bool) {
	info, ok := slotTypesByName[name]
	return info, ok
}

// LookupBatchMode resolves a batch aggregation mode name, case-sensitively.
func LookupBatchMode(name string) (BatchModeInfo, bool) {
	info, ok := batchModesByName[name]
	return info, ok
}

// LookupReagentMode resolves a reagent mode name, case-sensitively.
func LookupReagentMode(name string) (ReagentModeInfo, bool) {
	info, ok := reagentModesByName[name]
	return info, ok
}

// LookupPrefab resolves a prefab by its own name, case-sensitively. That name
// is what a program hashes; nothing in the chip's assembler resolves it.
func LookupPrefab(name string) (PrefabInfo, bool) {
	prefab, ok := prefabsByName[name]
	return prefab, ok
}

// LookupPrefabHash resolves the prefab a batch instruction operand names.
//
// A false answer means the build ships no such thing, which for a hash a
// program computed or spelled out is the strongest statement available: the
// batch forms report no error for a hash that matches nothing, they just do
// nothing.
func LookupPrefabHash(hash int32) (PrefabInfo, bool) {
	prefab, ok := prefabsByHash[hash]
	return prefab, ok
}

// indexPrefabsByHash indexes the prefab table by the number an instruction
// carries. The game builds its own lookup with Dictionary.Add, so a shipping
// build cannot hold a collision; extraction checks that rather than assuming
// it, and the first entry wins here if one ever appears.
func indexPrefabsByHash() map[int32]PrefabInfo {
	prefabs := make(map[int32]PrefabInfo, len(Prefabs))
	for _, prefab := range Prefabs {
		if _, taken := prefabs[prefab.Hash]; !taken {
			prefabs[prefab.Hash] = prefab
		}
	}
	return prefabs
}

// LookupConstant resolves a named numeric constant. The chip compares constant
// names case-insensitively, unlike every other name in the language, and this
// follows it.
func LookupConstant(name string) (Constant, bool) {
	constant, ok := constantsByName[strings.ToLower(name)]
	return constant, ok
}

// buildReservedWords collects every name the chip's assembler resolves on its
// own, so generated labels and defines can be kept clear of them.
func buildReservedWords() map[string]bool {
	// Two register names per index covers the numeric spelling alongside sp and
	// ra, and the trailing one is db.
	size := len(Instructions) + len(LogicTypes) + len(LogicSlotTypes) + len(BatchModes) +
		len(ReagentModes) + len(Constants) + 2*NumRegisters + NumParsedDevicePins + 1
	words := make(map[string]bool, size)
	add := func(name string) { words[strings.ToLower(name)] = true }
	for _, i := range Instructions {
		add(i.Mnemonic)
	}
	for _, t := range LogicTypes {
		add(t.Name)
	}
	for _, t := range LogicSlotTypes {
		add(t.Name)
	}
	for _, m := range BatchModes {
		add(m.Name)
	}
	for _, m := range ReagentModes {
		add(m.Name)
	}
	for _, c := range Constants {
		add(c.Name)
	}
	for r := range Register(NumRegisters) {
		// Register.String renders 16 and 17 as sp and ra, but the assembler also
		// resolves r16 and r17, so both spellings have to be reserved.
		add(r.String())
		add("r" + strconv.FormatUint(uint64(r), 10))
	}
	for pin := range NumParsedDevicePins {
		add("d" + strconv.Itoa(pin))
	}
	add("db")
	return words
}

// IsReservedWord reports whether the chip's assembler already resolves name to
// something of its own.
//
// A label or define that collides is not rejected. It is shadowed, and every
// instruction referring to it then faults at runtime, once per tick,
// indefinitely. Name mangling must consult this. The comparison is
// case-insensitive, which over-reserves the case-sensitive namespaces on
// purpose: a wrongly rejected candidate name costs nothing, a collision costs
// a silently broken program.
func IsReservedWord(name string) bool { return reservedWords[strings.ToLower(name)] }
