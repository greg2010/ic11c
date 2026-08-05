package isel

import (
	"tinygo.org/x/go-llvm"

	"github.com/greg2010/ic11c/internal/ic10"
)

// checkConversionRange refuses a machine instruction over an operand
// selection cannot place, at a position the chip reads through a
// conversion. It is asked only where a pattern selects a bitwise or shift
// instruction the optimizer formed out of an operation that couldn't itself fault.
func (s *selector) checkConversionRange(in llvm.Value, op ic10.Opcode, operands ...llvm.Value) bool {
	return s.checkOperands(in, op, operands, s.unplaced, func(instruction ic10.Instruction, position int) bool {
		switch conversion := instruction.Operands[position].Conversion; conversion {
		case ic10.ConversionNone:
			return true
		case ic10.ConversionInt, ic10.ConversionSignedLong, ic10.ConversionUnsignedLong,
			ic10.ConversionNarrowedInt, ic10.ConversionUnknown:
			fallthrough
		default:
			// A spelling with no bound of its own reaches the same refusal,
			// which names none and says so. Refusing with nothing to read is
			// the one outcome this may not have.
			s.refuseUnplacedOperand(in, instruction, position, conversion)
		}
		return false
	})
}

// checkNarrowedOperands refuses a machine instruction over an operand
// selection cannot show lies inside the signed 32-bit range, at a position
// the chip rounds to an integer. It reads [planInt32Range]'s set, not
// [planPlacement]'s, whose ±2^63 bound is wider than this position's 2^31.
func (s *selector) checkNarrowedOperands(in llvm.Value, op ic10.Opcode, operands ...llvm.Value) bool {
	return s.checkOperands(in, op, operands, s.unranged, func(instruction ic10.Instruction, position int) bool {
		if !narrowsOperand(instruction.Operands[position]) {
			return true
		}
		s.refuseNarrowedOperand(in, instruction, position)
		return false
	})
}

// checkOperands lines the values a pattern hands an instruction up with the
// positions the machine reads them from, and asks accept about each position
// holding a value named by held. A pattern may leave a trailing operand to a
// literal, which [selector.checkOperandConversion] checks instead.
func (s *selector) checkOperands(in llvm.Value, op ic10.Opcode, operands []llvm.Value, held map[llvm.Value]bool, accept func(ic10.Instruction, int) bool) bool {
	instruction, known := op.Instruction()
	positions, directed := readPositions(instruction)
	if !known || !directed {
		s.errorf(s.position(in), "the machine's own table does not say which operands of the %s selected here it reads, so what each value goes through is not known; this is a defect in the compiler, not in the program", op)
		return false
	}
	if len(operands) > len(positions) {
		s.errorf(s.position(in), "the %s selected here was handed %d operands where the machine reads %d, so what each one goes through is not known; this is a defect in the compiler, not in the program", op, len(operands), len(positions))
		return false
	}
	for i, operand := range operands {
		if operand == blankOperand || !held[operand] {
			continue
		}
		if !accept(instruction, positions[i]) {
			return false
		}
	}
	return true
}

// blankOperand fills a position holding no LLVM value, so operands either
// side of it still line up with the machine positions they are checked
// against. It is skipped by name rather than by absence from the range maps,
// since those answer false for anything unrecorded — which would let a
// blank pass by accident.
var blankOperand llvm.Value

// refuseUnplacedOperand reports an operand selection cannot place, naming
// the range that position's conversion carries a value through. The int
// bound is 2^32 nearer than the long one, so a value held to whichever the
// other operand carries can still fault the chip.
func (s *selector) refuseUnplacedOperand(in llvm.Value, instruction ic10.Instruction, position int, conversion ic10.Conversion) {
	width, bound, known := conversionBound(conversion)
	if !known {
		s.errorf(s.position(in), "%s of the %s selected here is read through %s, which this stage has no bound for; this is a defect in the compiler, not in the program", operandLabel(instruction, position), instruction.Mnemonic, conversion)
		return
	}
	s.errorf(s.position(in), "the optimizer folded the test on this line into a bitwise operation over a value that can hold an infinity; %s of the %s it selected reaches the instruction through the machine's conversion to a signed %d-bit integer, which stops the chip outside %s rather than reducing into range, and an infinity is outside it where the comparison the fold replaced refuses nothing, so the backend cannot carry the rewrite out — bounding the value before the test is what leaves the fold with an operand it can use",
		operandLabel(instruction, position), instruction.Mnemonic, width, bound)
}

// refuseNarrowedOperand reports an operand selection cannot show lies
// inside the signed 32-bit range, at a position the chip converts the
// register to an integer of that width. Which integer it lands on is
// deliberately omitted: that value is measured on one build of a runtime
// the game forks ([minInt32]) and would mislead readers.
func (s *selector) refuseNarrowedOperand(in llvm.Value, instruction ic10.Instruction, position int) {
	outcome := "which neither stops the chip nor carries the value into range"
	if instruction.Operands[position].Name == stackIndexName {
		outcome = "which carries the value into no range and stops the chip on any value it cannot represent, poke raising StackUnderFlow and get the unknown error"
	}
	s.errorf(s.position(in), "%s of the %s selected here can be handed a value outside -2147483648 through 2147483647; the chip resolves that position by converting the register to a signed 32-bit integer, %s, so the line runs against some integer the program never computed and nothing on the chip reports it. A batch read matching no device answers a NaN, as does zero divided by zero, and a device reading is whatever the world holds; masking the value makes that worse rather than better, since a bitwise result over a NaN comes back as zero, which is the batch of every device whose prefab is unset, and over an infinity the mask stops the chip outright. Bound the value where the operand is computed instead, as in `%s<a value of your own>` — the comparison is false for a NaN and for both infinities and holds only inside the range this position carries, so the arm it selects there is the one you wrote",
		operandLabel(instruction, position), instruction.Mnemonic, outcome, boundedCastAdvice)
}

// boundedCastAdvice is the spelling [selector.refuseNarrowedOperand] names,
// up to the value the programmer chooses for the arm the test fails on.
// [boundedSelect] holds these to the same bounds rather than trusting this
// string alone.
const boundedCastAdvice = "(v > -2147483648.0 && v < 2147483648.0) ? (long long)v : "
