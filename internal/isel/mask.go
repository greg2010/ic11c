package isel

import (
	"math"
	"math/bits"

	"github.com/greg2010/ic11c/internal/llvmir"
	"tinygo.org/x/go-llvm"
)

// coverLimit is the largest bound the walk carries: the largest magnitude a
// covering mask reads back as itself, since the operand conversion is the
// identity below 2^53 and sends 2^53 to zero. It bounds a sum and a product
// too, since every value inside it is one a double holds exactly.
const coverLimit = 1<<53 - 1

// andIntrinsic is the declaration standing for the machine's and, which is how
// the operator the source wrote reaches this stage. The instruction spelling is
// the optimizer's own.
const andIntrinsic = "__ic_and"

// cover is one memoised answer from [selector.coverOf].
type cover struct {
	bound uint64
	known bool
}

// coverOf reports the bound a value holds within: a whole number in
// [0, coverLimit], or false for a value that can be negative. depth shares
// [maxOffsetDepth] with the other walks over the operand graph, so they
// agree on which use chains a program may reach.
func (s *selector) coverOf(v llvm.Value, depth int) (uint64, bool) {
	if memo, seen := s.covers[v]; seen {
		return memo.bound, memo.known
	}
	if depth > maxOffsetDepth {
		return 0, false
	}
	bound, known := s.computeCover(v, depth)
	if s.unplaced[v] {
		// A value selection cannot place inside the machine's conversion range
		// is one a bitwise instruction over it is refused for, and a bound is
		// what would drop the instruction that established it.
		bound, known = 0, false
	}
	s.covers[v] = cover{bound: bound, known: known}
	return bound, known
}

// boundedOps names every opcode the walk can bound, gating
// [selector.computeCover] so nothing outside it is. A phi is deliberately
// absent, which keeps the walk acyclic: in SSA nothing else carries a value
// back to itself. Subtraction is absent too, since the difference of two
// bounded values isn't bounded below zero.
var boundedOps = map[llvm.Opcode]bool{
	llvm.And:     true,
	llvm.Call:    true,
	llvm.Add:     true,
	llvm.Mul:     true,
	llvm.Select:  true,
	llvm.ZExt:    true,
	llvm.SIToFP:  true,
	llvm.UIToFP:  true,
	llvm.FPToSI:  true,
	opcodeFreeze: true,
}

// computeCover answers for one value, reading the values it is built from.
func (s *selector) computeCover(v llvm.Value, depth int) (uint64, bool) {
	if predicateType(v.Type()) {
		return 1, true
	}
	if value, whole := wholeConstant(v); whole {
		return boundedBy(value)
	}
	if v.IsAInstruction().IsNil() {
		return 0, false
	}
	if !boundedOps[v.InstructionOpcode()] {
		return 0, false
	}
	if s.producesOperand(v) {
		return s.coverOf(v.Operand(0), depth+1)
	}
	// Default is the rule: an opcode the table admits and no arm answers for is
	// a value this walk knows nothing about, which is the answer that keeps the
	// refusal rather than one that removes it on a guess.
	//exhaustive:ignore
	switch v.InstructionOpcode() {
	case llvm.And:
		return s.andCover(v, depth)
	case llvm.Call:
		// Every other call is a declaration saying only that it computes a
		// number.
		if !andCall(v) {
			return 0, false
		}
		return s.andCover(v, depth)
	case llvm.Add:
		return s.sumCover(v, depth)
	case llvm.Mul:
		return s.productCover(v, depth)
	case llvm.Select:
		return s.armCover(v, depth)
	default:
		return 0, false
	}
}

// andCover bounds an and by whichever operand the walk bounds: a bit clear in
// either operand is clear in the result, and an operand the walk bounds is
// non-negative. Both bounded, the smaller is the tighter answer.
func (s *selector) andCover(v llvm.Value, depth int) (uint64, bool) {
	bound, known := uint64(0), false
	for i := range 2 {
		operand, bounded := s.coverOf(v.Operand(i), depth+1)
		if !bounded {
			continue
		}
		if !known || operand < bound {
			bound, known = operand, true
		}
	}
	return bound, known
}

// bothCovers bounds two values, reporting false unless the walk bounds both.
// Every combining rule needs both, since a bound on one operand says nothing
// about a sum, a product, or the arm a select did not take.
func (s *selector) bothCovers(left, right llvm.Value, depth int) (uint64, uint64, bool) {
	leftBound, leftOK := s.coverOf(left, depth+1)
	rightBound, rightOK := s.coverOf(right, depth+1)
	if !leftOK || !rightOK {
		return 0, 0, false
	}
	return leftBound, rightBound, true
}

// sumCover bounds an addition by the sum of its operands' bounds, which no
// value of them exceeds.
func (s *selector) sumCover(v llvm.Value, depth int) (uint64, bool) {
	left, right, bounded := s.bothCovers(v.Operand(0), v.Operand(1), depth)
	// The subtraction rather than the sum is what the limit is tested against,
	// for the reason the product tests a division: the limit is what a mask can
	// cover and not what a uint64 holds.
	if !bounded || left > coverLimit-right {
		return 0, false
	}
	return left + right, true
}

// productCover bounds a multiplication by the product of its operands' bounds,
// which no value of them exceeds.
func (s *selector) productCover(v llvm.Value, depth int) (uint64, bool) {
	left, right, bounded := s.bothCovers(v.Operand(0), v.Operand(1), depth)
	// The division rather than the product is what the limit is tested against:
	// two bounds inside it multiply to a number a uint64 does not hold.
	if !bounded || (right != 0 && left > coverLimit/right) {
		return 0, false
	}
	return left * right, true
}

// armCover bounds a select by the looser of its two arms, which are its second
// and third operands.
func (s *selector) armCover(v llvm.Value, depth int) (uint64, bool) {
	left, right, bounded := s.bothCovers(v.Operand(1), v.Operand(2), depth)
	if !bounded {
		return 0, false
	}
	return max(left, right), true
}

// boundedBy reads a whole number as a bound, refusing a negative one and one
// past what a covering mask reads back unchanged.
func boundedBy(value int64) (uint64, bool) {
	if value < 0 || value > coverLimit {
		return 0, false
	}
	return uint64(value), true
}

// wholeConstant reads a constant as the whole number it stands for. A mask
// the optimizer folded reaches this stage as an i64 or a double; a one-bit
// constant reads as its bit pattern sign extended, so true reads as -1 — the
// mask that clears nothing.
func wholeConstant(v llvm.Value) (int64, bool) {
	if !v.IsAConstantInt().IsNil() {
		return v.SExtValue(), true
	}
	if v.IsAConstantFP().IsNil() {
		return 0, false
	}
	value, inexact := v.DoubleValue()
	if inexact || value != math.Trunc(value) || value < math.MinInt64 || value >= twoPow63 {
		return 0, false
	}
	return int64(value), true
}

// coveredMask reports whether masking a value bounded by bound leaves every
// value it can hold alone. What the mask must keep is the whole run of low
// bits the bound reaches into: a value bounded by 30 holds 29 as readily,
// and 29 sets a bit 30 does not.
func coveredMask(bound uint64, mask int64) bool {
	spread := uint64(1)<<bits.Len64(bound) - 1
	return spread&^uint64(mask) == 0
}

// andCall reports whether a call is the declaration standing for the machine's
// and.
func andCall(v llvm.Value) bool {
	callee := v.CalledValue()
	return !callee.IsNil() && callee.Name() == andIntrinsic
}

// planIdentityMasks records every and whose mask reads its other operand
// back unchanged, so this stage emits nothing for it and its readers
// resolve straight to that operand. This can also fire on an instruction
// the address plans already read as a scaled term; that's harmless since a
// term is read through [selector.operand], which follows the alias either way.
func (s *selector) planIdentityMasks() {
	for _, bb := range s.order {
		for in := range llvmir.BlockInstrs(bb) {
			if value, identity := s.identityMask(in); identity {
				s.aliases[in] = value
			}
		}
	}
}

// identityMask reads an and against a constant mask, answering the operand the
// mask leaves unchanged.
func (s *selector) identityMask(in llvm.Value) (llvm.Value, bool) {
	if in.IsAInstruction().IsNil() {
		return llvm.Value{}, false
	}
	// Default is the rule: an instruction that is not one of the two and
	// spellings masks nothing.
	//exhaustive:ignore
	switch in.InstructionOpcode() {
	case llvm.And:
	case llvm.Call:
		if !andCall(in) {
			return llvm.Value{}, false
		}
	default:
		return llvm.Value{}, false
	}
	for i := range 2 {
		mask, whole := wholeConstant(in.Operand(i))
		if !whole {
			continue
		}
		value := in.Operand(1 - i)
		bound, bounded := s.coverOf(value, 0)
		if !bounded || !coveredMask(bound, mask) {
			continue
		}
		return value, true
	}
	return llvm.Value{}, false
}
