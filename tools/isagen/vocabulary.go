package main

import (
	"fmt"
	"strings"
)

// enumeration is one vocabulary the generated package declares for itself: a
// Go type over uint8, one constant per member, and a String rendering the
// spelling the extraction uses. The member order is fixed here rather than
// taken from the data, since the ordinals are what the rendered tables are
// written against, and a reordering would rewrite every table for nothing.
type enumeration struct {
	// typeName is the type the generated package declares, and namesVar the
	// array its String indexes.
	typeName string
	namesVar string
	// doc heads the type declaration, comment markers and all.
	doc string
	// members are in ordinal order; the first is the zero value.
	members []enumMember
}

// enumMember is one member of a vocabulary.
type enumMember struct {
	// goName is the constant the generated package declares.
	goName string
	// value is the spelling the extracted JSON carries the member under, which
	// is what the render stage resolves a table entry through. It is empty for a
	// member no extraction produces.
	value string
	// spelling is what String renders, and defaults to value. It differs only
	// where the JSON spelling is not a name: the operand kinds carry the game's
	// help-text token, and the absent access has no JSON spelling at all.
	spelling string
}

// rendered is the spelling String answers with.
func (m enumMember) rendered() string {
	if m.spelling != "" {
		return m.spelling
	}
	return m.value
}

// goNames maps each member's extracted spelling to the constant the generated
// package declares it as. A member with no extracted spelling is absent, so a
// table entry carrying one is reported rather than rendered.
func (e enumeration) goNames() map[string]string {
	names := make(map[string]string, len(e.members))
	for _, member := range e.members {
		if member.value == "" {
			continue
		}
		names[member.value] = member.goName
	}
	return names
}

// render writes the type, its constants, and its String.
func (e enumeration) render(b *strings.Builder) {
	b.WriteString(e.doc)
	fmt.Fprintf(b, "\ntype %s uint8\n\nconst (\n", e.typeName)
	for i, member := range e.members {
		if i == 0 {
			fmt.Fprintf(b, "\t%s %s = iota\n", member.goName, e.typeName)
			continue
		}
		fmt.Fprintf(b, "\t%s\n", member.goName)
	}
	b.WriteString(")\n\n")

	fmt.Fprintf(b, "var %s = [...]string{", e.namesVar)
	for i, member := range e.members {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(b, "%q", member.rendered())
	}
	b.WriteString("}\n\n")

	fmt.Fprintf(b, `func (v %[1]s) String() string {
	if int(v) >= len(%[2]s) {
		return "%[1]s(" + strconv.FormatUint(uint64(v), 10) + ")"
	}
	return %[2]s[v]
}

`, e.typeName, e.namesVar)
}

// directions is the vocabulary of what an instruction does to the register an
// operand names.
var directions = enumeration{
	typeName: "Direction",
	namesVar: "directionNames",
	doc: `// Direction is what an instruction does to the register an operand names, read
// from the operation class the chip builds rather than from operand position. The
// zero value is DirectionUnknown: an operand built elsewhere states nothing.`,
	members: []enumMember{
		{goName: "DirectionUnknown", value: string(DirectionUnknown)},
		{goName: "DirectionRead", value: string(DirectionRead)},
		{goName: "DirectionWrite", value: string(DirectionWrite)},
		{goName: "DirectionReadWrite", value: string(DirectionReadWrite)},
	},
}

// conversions is the vocabulary of what the chip does to an operand's value on
// the way into the instruction.
var conversions = enumeration{
	typeName: "Conversion",
	namesVar: "conversionNames",
	doc: `// Conversion is the reader the operation calls on an operand, which bounds and
// reduces the double the register holds. It is per operand. The zero value is
// ConversionUnknown: an operand built elsewhere states nothing.`,
	members: []enumMember{
		{goName: "ConversionUnknown", value: string(ConversionUnknown)},
		{goName: "ConversionNone", value: string(ConversionNone)},
		{goName: "ConversionInt", value: string(ConversionInt)},
		{goName: "ConversionNarrowedInt", value: string(ConversionNarrowedInt)},
		{goName: "ConversionSignedLong", value: string(ConversionSignedLong)},
		{goName: "ConversionUnsignedLong", value: string(ConversionUnsignedLong)},
	},
}

// operandKinds is the vocabulary of value classes an operand position accepts.
// Each renders as the token the game's help text is built from.
var operandKinds = enumeration{
	typeName: "OperandKind",
	namesVar: "operandKindTokens",
	doc:      `// OperandKind is a class of value an operand position accepts.`,
	members: []enumMember{
		{goName: "OperandRegister", value: kindRegister, spelling: kindTokens[kindRegister]},
		{goName: "OperandNumber", value: kindNumber, spelling: kindTokens[kindNumber]},
		{goName: "OperandInteger", value: kindInteger, spelling: kindTokens[kindInteger]},
		{goName: "OperandDevice", value: kindDevice, spelling: kindTokens[kindDevice]},
		{goName: "OperandRefID", value: kindRefID, spelling: kindTokens[kindRefID]},
		{goName: "OperandLogicType", value: kindLogicType, spelling: kindTokens[kindLogicType]},
		{goName: "OperandLogicSlotType", value: kindLogicSlotType, spelling: kindTokens[kindLogicSlotType]},
		{goName: "OperandBatchMode", value: kindBatchMode, spelling: kindTokens[kindBatchMode]},
		{goName: "OperandReagentMode", value: kindReagentMode, spelling: kindTokens[kindReagentMode]},
		{goName: "OperandDeviceHash", value: kindDeviceHash, spelling: kindTokens[kindDeviceHash]},
		{goName: "OperandNameHash", value: kindNameHash, spelling: kindTokens[kindNameHash]},
		{goName: "OperandSlotIndex", value: kindSlotIndex, spelling: kindTokens[kindSlotIndex]},
		{goName: "OperandString", value: kindString, spelling: kindTokens[kindString]},
	},
}

// accesses is the vocabulary of the pair of directions the game allows on one
// property. AccessNone is this program's name for a property absent from a
// prefab's table, which the extraction never writes; AccessRead through
// AccessReadWrite are the decided pairs, and AccessUnknown is a property the
// game settles from live state.
var accesses = enumeration{
	typeName: "Access",
	namesVar: "accessNames",
	doc: `// Access is the pair of directions the game allows on one property. AccessUnknown
// is decided from live state, so a caller must ask what a property refuses and
// never what it allows: against AccessReadWrite every unknown reads as a refusal.`,
	members: []enumMember{
		{goName: "AccessNone", value: string(accessNone), spelling: "none"},
		{goName: "AccessRead", value: string(accessRead)},
		{goName: "AccessWrite", value: string(accessWrite)},
		{goName: "AccessReadWrite", value: string(accessReadWrite)},
		{goName: "AccessUnknown", value: string(accessUnknown)},
	},
}
