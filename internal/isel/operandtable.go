package isel

import (
	"slices"
	"strconv"

	"github.com/greg2010/ic11c/internal/ic10"
)

// convertsAnOperand reports whether the chip reads any operand through a
// conversion, making the instruction partial where the LLVM op is total.
// ConversionUnknown counts (an unreadable conversion mustn't silently
// disable the bound); ConversionNarrowedInt does not — [narrowsOperand]'s concern.
func convertsAnOperand(instruction ic10.Instruction) bool {
	return slices.ContainsFunc(instruction.Operands, func(operand ic10.Operand) bool {
		return operand.Conversion != ic10.ConversionNone && operand.Conversion != ic10.ConversionNarrowedInt
	})
}

// readPositions returns the operand positions the instruction reads from, in
// order, or false if the table leaves a direction unknown. ins reads its
// destination through its source's conversion, so a read-write position
// counts as an input too.
func readPositions(instruction ic10.Instruction) ([]int, bool) {
	positions := make([]int, 0, len(instruction.Operands))
	for i, operand := range instruction.Operands {
		switch operand.Direction {
		case ic10.DirectionRead, ic10.DirectionReadWrite:
			positions = append(positions, i)
		case ic10.DirectionWrite:
		case ic10.DirectionUnknown:
			return nil, false
		}
	}
	return positions, true
}

// stackIndexName is the variable name the chip's help text gives the
// operand poke, get, put, getd and putd address the shared data/stack
// array through. Its narrowing is the only one that isn't silent: it can
// stop the chip (poke: StackUnderFlow; get: an unknown error).
const stackIndexName = "address"

// narrowsOperand reports whether the chip resolves an operand by
// converting the register to a signed 32-bit integer, which can hand the
// instruction a value the program never computed. Device positions count:
// they all descend from IntValuedVariable.
func narrowsOperand(operand ic10.Operand) bool {
	if operand.Conversion == ic10.ConversionNarrowedInt || operand.Name == stackIndexName {
		return true
	}
	return slices.ContainsFunc(operand.Kinds, narrowingKind)
}

// narrowingKind reports whether an operand kind is read through one of the
// machine's narrowing classes, each converting the register to a signed
// 32-bit integer — though not identically, since IntValuedVariable and
// LineNumberVariable round where EnumValuedVariable truncates.
func narrowingKind(kind ic10.OperandKind) bool {
	// Default is the rule: a kind the extraction gains after this was written
	// answers "narrows", which costs a refusal where a program is fine rather
	// than losing one where it is not.
	//exhaustive:ignore
	switch kind {
	case ic10.OperandRegister, ic10.OperandNumber, ic10.OperandString:
		return false
	default:
		return true
	}
}

// conversionBound is the width and range of the integer an operand's
// conversion produces, spelled for diagnostics. Past either end the chip
// stops (GetVariableInt: ShiftUnderflow/Overflow; GetVariableLong: refuses
// outright). A NaN passes both ordered-comparison guards and reaches the
// cast, landing at zero for long and the smallest int32 for int.
func conversionBound(conversion ic10.Conversion) (width int, bound string, known bool) {
	switch conversion {
	case ic10.ConversionInt:
		return 32, "-2^31 through 2^31-1", true
	case ic10.ConversionSignedLong, ic10.ConversionUnsignedLong:
		return 64, "±2^63", true
	case ic10.ConversionNone, ic10.ConversionNarrowedInt, ic10.ConversionUnknown:
	}
	return 0, "", false
}

// operandLabel names one operand position in the spelling a reader will find
// against the mnemonic in the game. The help text gives a variable name to an
// operand that takes a value and leaves one taking a single kind of thing to
// the kind's own token, so the position number is the last resort.
func operandLabel(instruction ic10.Instruction, position int) string {
	operand := instruction.Operands[position]
	if operand.Name != "" {
		return "the " + operand.Name + " operand"
	}
	if len(operand.Kinds) == 1 {
		return "the " + operand.Kinds[0].String() + " operand"
	}
	return "operand " + strconv.Itoa(position+1)
}
