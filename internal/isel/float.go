package isel

import (
	"github.com/greg2010/ic11c/internal/llvmir"
	"github.com/greg2010/ic11c/internal/mir"
	"tinygo.org/x/go-llvm"
)

// floatOpcodes give the machine comparison each directly selectable fcmp
// predicate is. The machine's own comparison is a double comparison, so the
// mapping is an identity in everything but spelling.
var floatOpcodes = map[llvm.FloatPredicate]llvm.IntPredicate{
	llvm.FloatOEQ: llvm.IntEQ,
	llvm.FloatUNE: llvm.IntNE,
	llvm.FloatOLT: llvm.IntSLT,
	llvm.FloatOLE: llvm.IntSLE,
	llvm.FloatOGT: llvm.IntSGT,
	llvm.FloatOGE: llvm.IntSGE,
}

// negatedFloatPredicates give the predicate each unordered form is the negation
// of. Every entry's target is either directly selectable or is one, which is
// what makes undoing the canonicalisation terminate.
var negatedFloatPredicates = map[llvm.FloatPredicate]llvm.FloatPredicate{
	llvm.FloatULT: llvm.FloatOGE,
	llvm.FloatULE: llvm.FloatOGT,
	llvm.FloatUGT: llvm.FloatOLE,
	llvm.FloatUGE: llvm.FloatOLT,
	llvm.FloatUEQ: llvm.FloatONE,
	llvm.FloatORD: llvm.FloatUNO,
}

// markSwappedSelect records a comparison whose negation the select reading it
// can absorb by swapping its two arms. InstCombine canonicalises fcmp one,
// ole, and oge into their unordered negations wherever the reader can take
// the swap, so this undoes that in select position.
func (s *selector) markSwappedSelect(bb llvm.BasicBlock) {
	for in := range llvmir.BlockInstrs(bb) {
		if in.InstructionOpcode() != llvm.Select {
			continue
		}
		if truthSelect(in) {
			// The select is the condition's own value, so
			// [selector.producesOperand] takes it and nothing is left to trade
			// arms with. Marking the comparison swapped here would leave it
			// answering the complement with no reader to turn it back round.
			continue
		}
		cond := in.Operand(0)
		if cond.IsAInstruction().IsNil() || cond.InstructionOpcode() != llvm.FCmp {
			continue
		}
		if _, unordered := negatedFloatPredicates[cond.FloatPredicate()]; !unordered {
			continue
		}
		use := cond.FirstUse()
		if use.IsNil() || !use.NextUse().IsNil() || use.User() != in {
			continue
		}
		s.swapped[cond] = true
	}
}

// floatComparison resolves an fcmp predicate into the steps the machine has
// instructions for.
func (s *selector) floatComparison(in llvm.Value, lhs, rhs mir.Operand) (comparisonPlan, bool) {
	pred := in.FloatPredicate()
	negate := false
	if inverse, unordered := negatedFloatPredicates[pred]; unordered {
		pred, negate = inverse, true
	}
	if direct, known := floatOpcodes[pred]; known {
		if isZero(lhs) && !isZero(rhs) {
			direct, lhs, rhs = swappedPredicates[direct], rhs, lhs
		}
		return comparisonPlan{steps: []comparisonStep{{pred: direct, lhs: lhs, rhs: rhs}}, negate: negate}, true
	}
	// Default is the rule: a predicate neither table names and neither arm
	// below builds has no machine instruction, and saying so is the answer.
	//exhaustive:ignore
	switch pred {
	case llvm.FloatONE:
		// Ordered and unequal, which no single instruction answers: it is a
		// less-than or a greater-than, both of which a NaN fails.
		steps := []comparisonStep{
			{pred: llvm.IntSLT, lhs: lhs, rhs: rhs},
			{pred: llvm.IntSGT, lhs: lhs, rhs: rhs},
		}
		return comparisonPlan{steps: steps, join: joinOr, negate: negate}, true
	case llvm.FloatUNO:
		// Unordered, which is a NaN in either operand. The machine's NaN test
		// takes one operand, so there is a step apiece.
		if sameOperand(lhs, rhs) {
			return comparisonPlan{steps: []comparisonStep{{lhs: lhs, isNaN: true}}, negate: negate}, true
		}
		steps := []comparisonStep{{lhs: lhs, isNaN: true}, {lhs: rhs, isNaN: true}}
		return comparisonPlan{steps: steps, join: joinOr, negate: negate}, true
	default:
		s.errorf(s.position(in), "%s compares two doubles with a predicate the machine has no instruction for", describe(in))
		return comparisonPlan{}, false
	}
}

// sameOperand reports whether both sides of a comparison are the same
// operand, which is what "x != x" folds to and makes one NaN test enough.
// The == is safe because [mir.Operand]'s set of implementations is closed
// and every one is a comparable struct of scalars.
func sameOperand(lhs, rhs mir.Operand) bool {
	if lhs == nil || rhs == nil {
		return false
	}
	return lhs == rhs
}
