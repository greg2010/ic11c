// Package ic10 describes the Stationeers IC10 target: register file,
// instruction set, and operand enums, sourced from internal/isa plus machine
// facts the extraction cannot state (slot width, sp/ra). Tables are
// package-level slices — do not mutate them.
package ic10

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/greg2010/ic11c/internal/isa"
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
// Deprecated instructions still assemble and run; the flag exists so the
// compiler can prefer the current spelling. See [Unemittable] for the
// stronger condition that the backend must never select an instruction.
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
	// Implicit lists the registers the instruction reaches without an operand
	// naming them, ordered by register. Nil except for the stack forms and
	// the linking jumps.
	Implicit []ImplicitUse
}

// ImplicitUse is one register an instruction reaches without an operand
// naming it, and what it does to it. jal's DirectionWrite always replaces
// ra; every other linking form assigns ra only where it branches, so it
// carries DirectionReadWrite instead: the prior value survives otherwise.
type ImplicitUse struct {
	Register  Register
	Direction Direction
}

// WritesImplicitly reports whether the instruction assigns reg without any
// operand naming it, which covers both replacing the register and folding a new
// value into what it held.
func (i Instruction) WritesImplicitly(reg Register) bool {
	return slices.ContainsFunc(i.Implicit, func(use ImplicitUse) bool {
		return use.Register == reg && (use.Direction == DirectionWrite || use.Direction == DirectionReadWrite)
	})
}

// LinksReturn reports whether op leaves a return address in ra, marking it
// as a call: what is live across it must be saved. See [Instruction.Implicit]
// for the write/read-write split this collapses.
func LinksReturn(op Opcode) bool {
	return linkingOps[op]
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
	// Conversion is what the chip does to the operand's value on the way into
	// the instruction, extracted per operand: one operand of an instruction
	// may convert while another does not, or convert differently.
	Conversion Conversion
}

// Accepts reports whether the operand admits a value of the given kind.
func (o Operand) Accepts(kind OperandKind) bool {
	return slices.Contains(o.Kinds, kind)
}

// Direction is what an instruction does to the register an operand names.
// Zero value is DirectionUnknown, so an operand built outside the generated
// table claims nothing. Treating an unknown direction as read or write loses
// a value silently — refuse it instead.
type Direction = isa.Direction

const (
	// DirectionUnknown is an operand whose direction the extraction could not
	// read out of the game source. It is never a licence to assume either
	// answer.
	DirectionUnknown = isa.DirectionUnknown
	// DirectionRead is an operand the instruction only consumes.
	DirectionRead = isa.DirectionRead
	// DirectionWrite is an operand whose register the instruction assigns
	// without reading what was there.
	DirectionWrite = isa.DirectionWrite
	// DirectionReadWrite is an operand whose register the instruction reads and
	// then assigns, so the value live in it before the instruction is one of the
	// instruction's inputs.
	DirectionReadWrite = isa.DirectionReadWrite
)

// Conversion is what the chip does to an operand's value on the way into
// the instruction, per operand rather than per instruction: the bound
// differs by conversion, so one operand can stop the chip while another
// operand of the same instruction silently narrows.
//
// Zero value is ConversionUnknown, so an operand built outside the
// generated table claims nothing; treating it as unconverted skips the
// bound that would have refused the operand.
type Conversion = isa.Conversion

const (
	// ConversionUnknown is an operand whose conversion the extraction could
	// not read out of the game source. It is never a licence to assume the
	// operand is unconverted.
	ConversionUnknown = isa.ConversionUnknown
	// ConversionNone is an operand no conversion below governs — not the same
	// as no reduction: poke's address, ls's slotIndex, lb's deviceHash, j's
	// target, and the id operand of sd, ld and getd all reach the instruction
	// already rounded or truncated by their own variable class rather than by
	// a conversion here.
	ConversionNone = isa.ConversionNone
	// ConversionInt is an operand read through GetVariableInt: it raises
	// ShiftUnderflow below -2^31 and ShiftOverflow above 2^31-1, otherwise
	// casts. Both bounds are ordered comparisons, false for NaN, so a NaN
	// passes through and casts to -2^31 like any other in-range value.
	ConversionInt = isa.ConversionInt
	// ConversionNarrowedInt is an operand the operation casts to int
	// directly, with no range check ahead of it, so unlike ConversionInt it
	// never stops the chip. Mono's cvttsd2si truncates toward zero and
	// answers -2^31 for a NaN, either infinity, or an out-of-range magnitude
	// alike.
	ConversionNarrowedInt = isa.ConversionNarrowedInt
	// ConversionSignedLong is an operand read through GetVariableLong, sign
	// kept: it stops the chip outside ±2^63, otherwise reduces modulo 2^53
	// silently. The bound is the same ordered comparisons as ConversionInt,
	// so NaN falls through and reduces to -2^63 rather than stopping the
	// chip.
	ConversionSignedLong = isa.ConversionSignedLong
	// ConversionUnsignedLong is the same reduction with the residue masked to
	// 54 bits rather than signed.
	ConversionUnsignedLong = isa.ConversionUnsignedLong
)

// WriteIndex reports the operand position the instruction assigns through,
// or -1 when no such operand exists — including when the write is implicit
// (jal, the al family, every stack form): read [Instruction.Implicit] for
// those. It errors on an unknown direction, more than one written operand,
// or a read-write operand, since none of those fit a single index.
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
		// DirectionUnknown and anything outside the four cases mean the table
		// says nothing usable; refuse rather than guess.
		return 0, fmt.Errorf("%s operand %d has direction %s", i.Mnemonic, position, operand.Direction)
	}
	return written, nil
}

// OperandKind is a class of value an operand position accepts.
type OperandKind = isa.OperandKind

// The operand kinds, one per token the game's help text is built from.
const (
	// OperandRegister is r0 through r15, sp, ra, or an indirect rr form.
	OperandRegister = isa.OperandRegister
	// OperandNumber is any literal, named constant, or defined name.
	OperandNumber = isa.OperandNumber
	// OperandInteger is a jump target: a line number, label, or defined name.
	OperandInteger = isa.OperandInteger
	// OperandDevice is db or d0 through d5, optionally with a network
	// connection suffix, or an indirect dr form.
	OperandDevice = isa.OperandDevice
	// OperandRefID is a register or literal holding a device ReferenceId.
	OperandRefID = isa.OperandRefID
	// OperandLogicType is a LogicTypes member, by name or by value.
	OperandLogicType = isa.OperandLogicType
	// OperandLogicSlotType is a LogicSlotTypes member, by name or by value.
	OperandLogicSlotType = isa.OperandLogicSlotType
	// OperandBatchMode is a BatchModes member, by name or by value.
	OperandBatchMode = isa.OperandBatchMode
	// OperandReagentMode is a ReagentModes member, by name or by value.
	OperandReagentMode = isa.OperandReagentMode
	// OperandDeviceHash is a prefab hash selecting a batch of devices.
	OperandDeviceHash = isa.OperandDeviceHash
	// OperandNameHash is a name hash narrowing a device batch.
	OperandNameHash = isa.OperandNameHash
	// OperandSlotIndex is a slot number on a device.
	OperandSlotIndex = isa.OperandSlotIndex
	// OperandString is a bare word, used only by the preprocessor forms.
	OperandString = isa.OperandString
)

// LogicType is a device property selector. Generated code emits the numeric
// value rather than the name: they resolve identically and the number is far
// cheaper against the 4096 byte program budget.
type LogicType uint16

// LogicSlotType is a slot property selector.
type LogicSlotType uint8

// BatchMode is an aggregation mode for the batch load forms. Backed by a
// plain int in the game, so int32 rather than the byte the five defined
// members would fit — narrowing would silently fold an undefined mode onto
// Average.
//
// A batch read matching no device reports no error: it returns NaN for
// Average, zero for Sum and Minimum, and negative infinity for Maximum.
// Test the result for NaN, not against zero.
type BatchMode int32

// ReagentMode selects which reagent quantity lr reads. Backed by a plain
// int in the game, for the reason given on [BatchMode]; narrowing would
// fold an undefined mode onto Contents.
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

// PrefabInfo is one entry of the game's prefab roster: a thing the game
// ships, described by everything a chip can reach about it. The logic and
// slot surfaces describe a completed device only — the game refuses every
// property of a structure still under construction.
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
	// order; absence means the device answers nothing. Unexported so
	// [PrefabInfo.RefusesRead] and [PrefabInfo.RefusesWrite] are the only way
	// to ask about a property.
	logic []isa.LogicAccess
}

// RefusesRead reports whether a completed device of this prefab is known to
// answer nothing when a program reads logicType; RefusesWrite is the same
// for a write. Both are phrased as refusals, not permissions: many
// properties are settled from live state and refuse nothing here.
func (p PrefabInfo) RefusesRead(logicType LogicType) bool {
	return refusesRead(p.accessFor(logicType))
}

// RefusesWrite is [PrefabInfo.RefusesRead] for the other direction.
func (p PrefabInfo) RefusesWrite(logicType LogicType) bool {
	return refusesWrite(p.accessFor(logicType))
}

func (p PrefabInfo) accessFor(logicType LogicType) isa.Access {
	for _, entry := range p.logic {
		if LogicType(entry.LogicType) == logicType {
			return entry.Allows
		}
	}
	return isa.AccessNone
}

// SlotInfo is one slot a prefab declares.
type SlotInfo struct {
	// Class is the Slot.Class member naming what the slot accepts, such as
	// "GasCanister" — what an ls Class read returns as a number.
	Class string

	// types lists the slot properties reachable on this slot, in LogicSlotType
	// order. Unexported for the reason PrefabInfo.logic is.
	types []isa.SlotAccess
}

// RefusesRead and RefusesWrite are [PrefabInfo.RefusesRead] for one slot's own
// properties, and are phrased as refusals for the same reason.
func (s SlotInfo) RefusesRead(slotType LogicSlotType) bool {
	return refusesRead(s.accessFor(slotType))
}

// RefusesWrite is [SlotInfo.RefusesRead] for the other direction.
func (s SlotInfo) RefusesWrite(slotType LogicSlotType) bool {
	return refusesWrite(s.accessFor(slotType))
}

func (s SlotInfo) accessFor(slotType LogicSlotType) isa.Access {
	for _, entry := range s.types {
		if LogicSlotType(entry.SlotType) == slotType {
			return entry.Allows
		}
	}
	return isa.AccessNone
}

// refusesRead reports whether the pair rules out a read, and refusesWrite
// whether it rules out a write. Both enumerate the true cases rather than
// negate the false ones, so an access neither of them yet knows about
// (including [isa.AccessUnknown]) refuses nothing.
func refusesRead(a isa.Access) bool { return a == isa.AccessNone || a == isa.AccessWrite }

func refusesWrite(a isa.Access) bool { return a == isa.AccessNone || a == isa.AccessRead }

// Constant is a numeric literal the chip's assembler accepts wherever a
// number is accepted, taken from the game rather than recomputed. deg2rad
// and rad2deg are float-precision literals widened to double — folding them
// at full double precision diverges from the game. epsilon is the smallest
// positive subnormal, not a comparison tolerance.
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
	// NumGeneralRegisters counts r0 through r15: the one range restriction the
	// machine puts on a register, since a device ReferenceId resolves within
	// them only. Indirect rr forms are not bounded by it — they index the
	// whole register array, so index 16 or 17 reaches sp or ra and only an
	// index outside 0-17 faults.
	NumGeneralRegisters = 16
	// RegSP is the stack pointer used by push, pop, and peek.
	RegSP Register = 16
	// RegRA is where jal writes the return address.
	RegRA Register = 17
)

// NumMemorySlots is the length of ProgrammableChip._Stack: one double array
// shared with no boundary between the data region and the call stack.
// Writing past it via poke/push/put raises StackOverFlow; reading past it
// via get raises Unknown, since _GET_Operation skips the try/catch that
// _PUT_Operation wraps WriteMemory in.
const NumMemorySlots = 512

// NumDevicePins is how many device pins a housing has, d0 through d5 — the
// length of CircuitHousing.Devices. db reaches the housing itself, not a
// pin. d6 through d9 assemble and then fault every tick indexing past the
// array, with no error naming which pin.
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

// ParseRegister resolves a register name: sp, ra, or r0 through r17. It
// reports false for anything else, including the rr indirect forms, which
// name a register only at runtime. Digits after r go through the chip's own
// integer parse, which admits a leading sign and leading zeros: r05 and r+5
// both parse as r5.
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

// Indexes over the tables. Go initialises these after the tables they depend
// on, so they are ready before any exported lookup can run.
var (
	instructionsByMnemonic = indexBy(Instructions, func(i Instruction) string { return i.Mnemonic })
	logicTypesByName       = indexBy(LogicTypes, func(t LogicTypeInfo) string { return t.Name })
	slotTypesByName        = indexBy(LogicSlotTypes, func(t LogicSlotTypeInfo) string { return t.Name })
	batchModesByName       = indexBy(BatchModes, func(m BatchModeInfo) string { return m.Name })
	reagentModesByName     = indexBy(ReagentModes, func(m ReagentModeInfo) string { return m.Name })
	constantsByName        = indexBy(Constants, func(c Constant) string { return strings.ToLower(c.Name) })
	prefabsByName          = indexBy(prefabs, func(p PrefabInfo) string { return p.Name })
	prefabsByHash          = indexPrefabsByHash()
	reservedWords          = buildReservedWords()
	linkingOps             = indexLinkingOps()
)

func indexLinkingOps() map[Opcode]bool {
	ops := make(map[Opcode]bool)
	for _, instruction := range Instructions {
		if instruction.WritesImplicitly(RegRA) {
			ops[instruction.Opcode] = true
		}
	}
	return ops
}

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

// LookupPrefabHash resolves the prefab a batch instruction operand names. A
// false answer means the build ships no such thing — the batch forms
// themselves report no error for a hash that matches nothing.
func LookupPrefabHash(hash int32) (PrefabInfo, bool) {
	prefab, ok := prefabsByHash[hash]
	return prefab, ok
}

// indexPrefabsByHash indexes the prefab table by the number an instruction
// carries. A shipping build holds no hash collision; the first entry wins
// here if one somehow appears.
func indexPrefabsByHash() map[int32]PrefabInfo {
	byHash := make(map[int32]PrefabInfo, len(prefabs))
	for _, prefab := range prefabs {
		if _, taken := byHash[prefab.Hash]; !taken {
			byHash[prefab.Hash] = prefab
		}
	}
	return byHash
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

// IsReservedWord reports whether the chip's assembler already resolves
// name to something of its own. A colliding label or define is not
// rejected: it is shadowed and faults every tick at runtime. The
// comparison is case-insensitive on purpose — over-reserving costs
// nothing, a missed collision breaks a program silently.
func IsReservedWord(name string) bool { return reservedWords[strings.ToLower(name)] }
