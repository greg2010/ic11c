package ic10

import "github.com/greg2010/ic11c/internal/isa"

// ManifestID is the Steam depot manifest the tables were extracted from. It
// names the assembly's exact bytes, so a re-extraction that reaches it lands on
// the same tables again, and it is the compiler's whole statement of which game
// build it describes.
const ManifestID = isa.ManifestID

// GameVersion is the assembly file version the tables were extracted from.
const GameVersion = isa.GameVersion

// The machine tables, in the types this package states the machine through.
// Built at initialisation, not lazily: every exported lookup reads one, and
// Go orders these after the internal/isa slices they read. The roster is
// unexported — a prefab's property leaves this package only through the
// four refusal queries; see [PrefabInfo.RefusesRead].
var (
	// Instructions holds every mnemonic the chip's assembler accepts, indexed by
	// Opcode. Deprecated entries still assemble and run.
	Instructions = instructionTable()

	// LogicTypes holds the device properties addressable by l, s, and the batch
	// forms. Generated code emits Value rather than Name; they resolve alike.
	LogicTypes = enumTable(isa.LogicTypes, func(m isa.EnumMember) LogicTypeInfo {
		return LogicTypeInfo{Name: m.Name, Value: LogicType(m.Value), Deprecated: m.Deprecated}
	})
	// LogicSlotTypes holds the slot properties addressable by ls, ss, and the
	// batch slot forms.
	LogicSlotTypes = enumTable(isa.LogicSlotTypes, func(m isa.EnumMember) LogicSlotTypeInfo {
		return LogicSlotTypeInfo{Name: m.Name, Value: LogicSlotType(m.Value), Deprecated: m.Deprecated}
	})
	// BatchModes holds the aggregation modes accepted by the batch load forms.
	BatchModes = enumTable(isa.BatchModes, func(m isa.EnumMember) BatchModeInfo {
		return BatchModeInfo{Name: m.Name, Value: BatchMode(m.Value), Deprecated: m.Deprecated}
	})
	// ReagentModes holds the reagent views accepted by lr.
	ReagentModes = enumTable(isa.ReagentModes, func(m isa.EnumMember) ReagentModeInfo {
		return ReagentModeInfo{Name: m.Name, Value: ReagentMode(m.Value), Deprecated: m.Deprecated}
	})

	// Constants holds the numeric literals the chip's assembler accepts wherever
	// a number is accepted, in game declaration order.
	Constants = constantTable()

	prefabs = prefabTable()
)

// enumTable renders one extracted operand enum in the info type this
// package carries its values as. The value narrows — a member's number
// comes in as an int64 and each family here holds it in the width the game
// backs that family with — and [TestEnumValuesSurviveTheirWidth] holds
// every member to surviving it.
func enumTable[T any](members []isa.EnumMember, convert func(isa.EnumMember) T) []T {
	table := make([]T, len(members))
	for i, member := range members {
		table[i] = convert(member)
	}
	return table
}

// instructionTable renders the extracted instruction table, indexed by
// Opcode.
func instructionTable() []Instruction {
	table := make([]Instruction, len(isa.Instructions))
	for i, extracted := range isa.Instructions {
		table[i] = Instruction{
			Mnemonic:   extracted.Mnemonic,
			Opcode:     Opcode(extracted.Opcode),
			Deprecated: extracted.Deprecated,
			Example:    extracted.Example,
			Operands:   operandList(extracted.Operands),
			Implicit:   implicitList(extracted.Implicit),
		}
	}
	return table
}

// operandList renders one instruction's operands, answering nil for the
// operandless forms so that the arity a caller reads off the length is the
// arity the chip's assembler enforces.
func operandList(extracted []isa.Operand) []Operand {
	if len(extracted) == 0 {
		return nil
	}
	operands := make([]Operand, len(extracted))
	for i, operand := range extracted {
		operands[i] = Operand(operand)
	}
	return operands
}

// implicitList renders the registers an instruction reaches without an operand
// naming them, answering nil for the instructions that reach none.
func implicitList(extracted []isa.ImplicitUse) []ImplicitUse {
	if len(extracted) == 0 {
		return nil
	}
	uses := make([]ImplicitUse, len(extracted))
	for i, use := range extracted {
		uses[i] = ImplicitUse{Register: Register(use.Register), Direction: use.Direction}
	}
	return uses
}

func constantTable() []Constant {
	table := make([]Constant, len(isa.Constants))
	for i, constant := range isa.Constants {
		table[i] = Constant(constant)
	}
	return table
}

// prefabTable renders the extracted roster. Slot and logic property
// surfaces are taken as they stand rather than copied element by element —
// this package reads them through the refusal queries alone.
func prefabTable() []PrefabInfo {
	table := make([]PrefabInfo, len(isa.Prefabs))
	for i, extracted := range isa.Prefabs {
		table[i] = PrefabInfo{
			Name:                 extracted.Name,
			Hash:                 extracted.Hash,
			Title:                extracted.Title,
			CircuitHolder:        extracted.CircuitHolder,
			CircuitHolderUnknown: extracted.CircuitHolderUnknown,
			Slots:                slotList(extracted.Slots),
			Modes:                extracted.Modes,
			ModesUnknown:         extracted.ModesUnknown,
			logic:                extracted.Logic,
		}
	}
	return table
}

func slotList(extracted []isa.Slot) []SlotInfo {
	if len(extracted) == 0 {
		return nil
	}
	slots := make([]SlotInfo, len(extracted))
	for i, slot := range extracted {
		slots[i] = SlotInfo{Class: slot.Class, types: slot.Types}
	}
	return slots
}
