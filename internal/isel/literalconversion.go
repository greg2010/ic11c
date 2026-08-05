package isel

import (
	"fmt"
	"math"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/mir"
	"github.com/greg2010/ic11c/internal/source"
)

// operandModulus is the modulus GetVariableLong reduces a value operand by.
// DoubleToLong is (long)(d % 2^53): the identity strictly inside ±2^53, and
// a residue — not zero — at every magnitude above it, which is why the
// bound is on magnitude rather than on a specific value.
const operandModulus = 1 << 53

// distanceBits is how many bits of a shift distance the machine's shift keeps.
// Everything outside 0 through maxDistance is therefore some other distance
// rather than a refusal.
const distanceBits = 6

// maxDistance is the largest distance the machine shifts by as written:
// srl, sra, sll and sla mask a distance to its low six bits (sla is the
// same class as sll despite its help text). rol, ror, ext, and ins each
// want a different range and are not selected here today.
const maxDistance = 1<<distanceBits - 1

// intConversionMin and intConversionMax bound what GetVariableInt carries. It
// compares strictly against each end, so both are carried and the first value
// past either one stops the chip.
const (
	intConversionMin = -1 << 31
	intConversionMax = 1<<31 - 1
)

// unconverted builds a machine instruction whose operands the chip reads
// as the numbers they name, bypassing [selector.checkOperandConversion].
// Used only for instructions this package builds outside pattern selection
// (data-region zeroing, split-edge jumps, phi copies, the calling convention).
func unconverted(op ic10.Opcode, pos source.Position, args ...mir.Operand) (*mir.Instr, error) {
	instruction, known := op.Instruction()
	if !known {
		return nil, fmt.Errorf("%s is not in the machine's instruction table, so which conversion each of its operands is read through is not known", op)
	}
	if convertsAnOperand(instruction) {
		return nil, fmt.Errorf("the machine converts an operand of %s, which is not something this may build without the conversion check every selected instruction goes through", op)
	}
	return mir.NewInstr(op, pos, args...)
}

// checkOperandConversion refuses a machine instruction over a literal
// operand its conversion doesn't hand the instruction as written. Only a
// literal is read here: a run-time operand reaches its conversion through
// the machine's own arithmetic, which needs no refusal.
func (s *selector) checkOperandConversion(pos source.Position, op ic10.Opcode, args []mir.Operand) bool {
	instruction, known := op.Instruction()
	if !known {
		s.errorf(pos, "the %s selected here is not in the machine's instruction table, so which conversion each of its operands is read through is not known; this is a defect in the compiler, not in the program", op)
		return false
	}
	if !convertsAnOperand(instruction) {
		return true
	}
	if len(args) != len(instruction.Operands) {
		s.errorf(pos, "the %s selected here has %d operands where the machine's own table gives it %d, so which conversion each one is read through is not known; this is a defect in the compiler, not in the program", op, len(args), len(instruction.Operands))
		return false
	}
	for i, arg := range args {
		imm, literal := arg.(mir.Imm)
		if !literal {
			continue
		}
		switch instruction.Operands[i].Conversion {
		case ic10.ConversionSignedLong, ic10.ConversionUnsignedLong:
			if math.Abs(imm.Value) >= operandModulus {
				s.refuseReducedValue(pos, op, imm)
				return false
			}
		case ic10.ConversionInt:
			if imm.Value != math.Trunc(imm.Value) || imm.Value < 0 || imm.Value > maxDistance {
				s.refuseDistance(pos, op, imm)
				return false
			}
		case ic10.ConversionNarrowedInt:
			if imm.Value != math.Trunc(imm.Value) || imm.Value < math.MinInt32 || imm.Value > math.MaxInt32 {
				s.refuseNarrowedValue(pos, op, imm)
				return false
			}
		case ic10.ConversionNone:
		case ic10.ConversionUnknown:
			fallthrough
		default:
			// A spelling with no arm above says as little to this stage as the
			// one that says nothing, and passing it would place a literal
			// against a conversion nothing here has read.
			s.errorf(pos, "%s of the %s selected here has no conversion in the machine's table, so nothing here can say whether the chip reads %s as the number it names; this is a defect in the compiler, not in the program", operandLabel(instruction, i), op, literalText(imm))
			return false
		}
	}
	return true
}

// refuseReducedValue reports a value operand GetVariableLong reduces, which is
// the silent outcome: the line runs and answers for the residue with nothing on
// the chip reporting the substitution.
func (s *selector) refuseReducedValue(pos source.Position, op ic10.Opcode, imm mir.Imm) {
	s.errorf(pos, "this %s cannot read %s as the number it names; a value operand reaches the instruction through the machine's conversion to a signed 64-bit integer, which reduces modulo 2^53 and hands it %s instead, and the line then runs and answers for that with nothing on the chip reporting the substitution — so keep a bitwise or shift value operand below 2^53 in magnitude, which is the whole of the range a long long survives that conversion in",
		op, literalText(imm), literalText(mir.Imm{Value: reducedValue(imm.Value)}))
}

// refuseNarrowedValue reports a literal at a position the operation's own body
// casts to a signed 32-bit integer, which is the silent outcome with no bound
// behind it: nothing checks the range ahead of the cast, so the line runs
// against whatever the cast made of the literal.
func (s *selector) refuseNarrowedValue(pos source.Position, op ic10.Opcode, imm mir.Imm) {
	s.errorf(pos, "this %s cannot read %s as the number it names; the instruction casts that operand to a signed 32-bit integer with no range check ahead of the cast, so the line runs against a different number and nothing on the chip reports the substitution — the operand has to be a whole number between %d and %d",
		op, literalText(imm), int64(math.MinInt32), int64(math.MaxInt32))
}

// refuseDistance reports a shift distance the machine does not shift by:
// past ±2^31 the conversion faults and the chip stops; inside it, the
// conversion drops any fraction and the shift masks what's left, so it
// shifts by a different distance instead.
func (s *selector) refuseDistance(pos source.Position, op ic10.Opcode, imm mir.Imm) {
	if imm.Value < intConversionMin || imm.Value > intConversionMax {
		s.errorf(pos, "this %s cannot take %s as a shift distance; a distance reaches the instruction through the machine's conversion to a signed 32-bit integer, which faults outside ±2^31 rather than reducing into range, so the chip stops at this line and loses every write left in the tick — a distance has to be 0 through %d, which is the whole of the range the machine shifts by as written",
			op, literalText(imm), maxDistance)
		return
	}
	s.errorf(pos, "this %s cannot take %s as a shift distance; a distance reaches the instruction through the machine's conversion to a signed 32-bit integer, which drops any fraction and does not reduce it into range, and the shift then keeps its low %d bits and shifts by %s instead, with nothing on the chip reporting the substitution — a distance has to be a whole number 0 through %d, which is the whole of the range the machine shifts by as written",
		op, literalText(imm), distanceBits, literalText(mir.Imm{Value: maskedDistance(imm.Value)}), maxDistance)
}

// reducedValue is what GetVariableLong hands an instruction for a value
// operand — the residue rather than zero for any magnitude not a multiple
// of operandModulus. Readers that pass DoubleToLong's signed flag as false
// (srl, the rotates) mask that residue to 54 bits, turning a negative one large.
func reducedValue(value float64) float64 {
	reduced := math.Mod(value, operandModulus)
	if reduced == 0 {
		// Mod takes its sign from the dividend, so a negative multiple leaves a
		// negative zero and no line holds a literal spelled that way.
		reduced = 0
	}
	return reduced
}

// maskedDistance is the distance a shift uses for one the conversion carried
// but did not bring into range. C# takes the low six bits of a long shift's
// count, so -8 shifts by 56 and 64 shifts by 0.
func maskedDistance(value float64) float64 {
	return float64(int64(value) & maxDistance)
}
