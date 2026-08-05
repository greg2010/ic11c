package isel

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"strconv"
	"strings"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/isa"
	"github.com/greg2010/ic11c/internal/llvmir"
	"github.com/greg2010/ic11c/internal/mir"
	"github.com/greg2010/ic11c/internal/source"
	"tinygo.org/x/go-llvm"
)

// binaryOps are the LLVM operations one machine instruction covers exactly.
// sla is absent: it's byte-identical to sll despite the help text. Only
// division differs from its float counterpart downstream: fdiv is a full
// quotient where an integer one still needs truncating.
var binaryOps = map[llvm.Opcode]ic10.Opcode{
	llvm.Add:  isa.OpAdd,
	llvm.Sub:  isa.OpSub,
	llvm.Mul:  isa.OpMul,
	llvm.And:  isa.OpAnd,
	llvm.Or:   isa.OpOr,
	llvm.Shl:  isa.OpSll,
	llvm.AShr: isa.OpSra,
	llvm.LShr: isa.OpSrl,
	llvm.FAdd: isa.OpAdd,
	llvm.FSub: isa.OpSub,
	llvm.FMul: isa.OpMul,
	llvm.FDiv: isa.OpDiv,
}

// predicateArithmetic gives the machine operation a [binaryOps] entry means
// when its type is one bit wide: LLVM's i1 add/sub are both mod-2 (xor),
// where the machine's add would answer 2 for true+true — a truth value no
// reader can use. The optimizer already canonicalises an i1 add into xor.
var predicateArithmetic = map[llvm.Opcode]ic10.Opcode{
	llvm.Add: isa.OpXor,
	llvm.Sub: isa.OpXor,
}

// setOps materialise a comparison as 0 or 1.
var setOps = map[llvm.IntPredicate]ic10.Opcode{
	llvm.IntEQ:  isa.OpSeq,
	llvm.IntNE:  isa.OpSne,
	llvm.IntSLT: isa.OpSlt,
	llvm.IntSLE: isa.OpSle,
	llvm.IntSGT: isa.OpSgt,
	llvm.IntSGE: isa.OpSge,
}

// setZeroOps are the same comparisons against zero, which cost one operand
// fewer to render.
var setZeroOps = map[llvm.IntPredicate]ic10.Opcode{
	llvm.IntEQ:  isa.OpSeqz,
	llvm.IntNE:  isa.OpSnez,
	llvm.IntSLT: isa.OpSltz,
	llvm.IntSLE: isa.OpSlez,
	llvm.IntSGT: isa.OpSgtz,
	llvm.IntSGE: isa.OpSgez,
}

// branchOps fuse a comparison and a branch into one instruction. Every
// predicate has a form, which is why a comparison feeding a branch never has to
// be materialised.
var branchOps = map[llvm.IntPredicate]ic10.Opcode{
	llvm.IntEQ:  isa.OpBeq,
	llvm.IntNE:  isa.OpBne,
	llvm.IntSLT: isa.OpBlt,
	llvm.IntSLE: isa.OpBle,
	llvm.IntSGT: isa.OpBgt,
	llvm.IntSGE: isa.OpBge,
}

// branchZeroOps are the fused forms against zero.
var branchZeroOps = map[llvm.IntPredicate]ic10.Opcode{
	llvm.IntEQ:  isa.OpBeqz,
	llvm.IntNE:  isa.OpBnez,
	llvm.IntSLT: isa.OpBltz,
	llvm.IntSLE: isa.OpBlez,
	llvm.IntSGT: isa.OpBgtz,
	llvm.IntSGE: isa.OpBgez,
}

// sameSignPredicates give the signed predicate an unsigned one means under the
// icmp samesign flag.
var sameSignPredicates = map[llvm.IntPredicate]llvm.IntPredicate{
	llvm.IntULT: llvm.IntSLT,
	llvm.IntULE: llvm.IntSLE,
	llvm.IntUGT: llvm.IntSGT,
	llvm.IntUGE: llvm.IntSGE,
}

// swappedPredicates give the predicate that holds when the operands trade
// places, so a comparison written with the constant first still reaches the
// cheaper against-zero form and the unsigned rewrites below.
var swappedPredicates = map[llvm.IntPredicate]llvm.IntPredicate{
	llvm.IntEQ:  llvm.IntEQ,
	llvm.IntNE:  llvm.IntNE,
	llvm.IntSLT: llvm.IntSGT,
	llvm.IntSGT: llvm.IntSLT,
	llvm.IntSLE: llvm.IntSGE,
	llvm.IntSGE: llvm.IntSLE,
	llvm.IntULT: llvm.IntUGT,
	llvm.IntUGT: llvm.IntULT,
	llvm.IntULE: llvm.IntUGE,
	llvm.IntUGE: llvm.IntULE,
}

// truthValuePredicates give the machine predicate a comparison of two
// one-bit values means. LLVM reads an i1 as signed (0 or -1) where the
// machine holds a truth value as 0 or 1, so a signed ordering has to be
// reversed (the same mapping as swapping operands) and an unsigned one just
// needs spelling signed (the samesign rewrite); equality doesn't depend on
// the reading.
var truthValuePredicates = func() map[llvm.IntPredicate]llvm.IntPredicate {
	m := maps.Clone(swappedPredicates)
	maps.Copy(m, sameSignPredicates)
	return m
}()

// negatedPredicates give the predicate that holds exactly when one does not.
// A conjunction in branch position needs it: the branch leaves for the false
// successor as soon as a conjunct fails.
var negatedPredicates = map[llvm.IntPredicate]llvm.IntPredicate{
	llvm.IntEQ:  llvm.IntNE,
	llvm.IntNE:  llvm.IntEQ,
	llvm.IntSLT: llvm.IntSGE,
	llvm.IntSGE: llvm.IntSLT,
	llvm.IntSLE: llvm.IntSGT,
	llvm.IntSGT: llvm.IntSLE,
}

// joinKind says how a comparison plan's steps combine.
type joinKind uint8

const (
	// joinOne is a plan of one step, which is every comparison the machine has
	// a single instruction for.
	joinOne joinKind = iota
	joinAnd
	joinOr
)

// joinOps combine a two-step plan's results. The steps are 0 or 1, so the
// bitwise instructions are the logical ones.
var joinOps = map[joinKind]ic10.Opcode{
	joinAnd: isa.OpAnd,
	joinOr:  isa.OpOr,
}

// unsignedRewrites give the signed test an unsigned comparison against a
// non-negative constant is equivalent to. See unsignedComparison for why.
var unsignedRewrites = map[llvm.IntPredicate]struct {
	join joinKind
	// sign tests the value against zero and bound tests it against the
	// constant.
	sign  llvm.IntPredicate
	bound llvm.IntPredicate
}{
	llvm.IntULT: {joinAnd, llvm.IntSGE, llvm.IntSLT},
	llvm.IntULE: {joinAnd, llvm.IntSGE, llvm.IntSLE},
	llvm.IntUGT: {joinOr, llvm.IntSLT, llvm.IntSGT},
	llvm.IntUGE: {joinOr, llvm.IntSLT, llvm.IntSGE},
}

// comparisonStep is one comparison the machine selects directly, in both the
// materialised and the fused form.
type comparisonStep struct {
	pred llvm.IntPredicate
	lhs  mir.Operand
	rhs  mir.Operand
	// isNaN makes the step the machine's NaN test over lhs alone. It has no
	// predicate in the comparison family, because it takes one operand where
	// every other comparison takes two.
	isNaN bool
}

// comparisonPlan is the test one comparison becomes: one step, or two
// joined by join. negate reports the plan answers the complement of what
// the comparison asked; a branch takes it by swapping successors, a
// materialised value by complementing the result.
type comparisonPlan struct {
	steps  []comparisonStep
	join   joinKind
	negate bool
}

// describe names an LLVM instruction the way the source that produced it
// reads, rather than its own text (SSA registers, types, and flags the
// programmer never wrote). A diagnostic already carries the line; this is
// which construct on it is at fault, in the language it's written in.
func describe(in llvm.Value) string {
	if in.IsAInstruction().IsNil() {
		return "this expression"
	}
	// Default is the rule: an instruction with no construct of its own to name
	// is described as the expression it is part of.
	//exhaustive:ignore
	switch in.InstructionOpcode() {
	case llvm.Call:
		if name := in.CalledValue().Name(); name != "" {
			return "the call to '" + name + "'"
		}
		return "this call"
	case llvm.Load:
		return "this read of a variable"
	case llvm.Store:
		return "this assignment"
	case llvm.Alloca:
		return "this local variable"
	case llvm.GetElementPtr:
		return "this subscript"
	case llvm.ICmp:
		return "this comparison"
	case llvm.Select:
		return "this conditional expression"
	case llvm.PHI:
		return "this value, which reaches here from more than one branch"
	case llvm.Br, llvm.Switch:
		return "this branch"
	case llvm.Ret:
		return "this return"
	case llvm.SDiv, llvm.UDiv:
		return "this division"
	case llvm.SRem, llvm.URem:
		return "this remainder"
	case llvm.Shl, llvm.AShr, llvm.LShr:
		return "this shift"
	case llvm.And, llvm.Or, llvm.Xor:
		return "this bitwise expression"
	default:
		return "this expression"
	}
}

func (s *selector) lowerBlocks() {
	for _, bb := range s.order {
		if err := s.ctx.Err(); err != nil {
			s.errorf(source.Position{File: s.pos.File}, "selection was cancelled: %v", err)
			return
		}
		info := s.blocks[bb]
		for in := range llvmir.BlockInstrs(bb) {
			s.lower(info, in)
		}
	}
}

// predicateWidth is the width of an i1. A comparison produces one, and a
// branch, a select, and the zero extension selection treats as an alias consume
// one; none of them ever occupies a register, because the machine's set
// instructions already produce 0 or 1.
const predicateWidth = 1

// machineWidth reports whether t is the machine's own value width, which is
// what makes a whole-register operation stand in for the operation the type
// asks for. An i1 is not: it is a truth value the machine holds as 0 or 1 in a
// register the rest of which is not part of the value.
func machineWidth(t llvm.Type) bool {
	return t.TypeKind() == llvm.IntegerTypeKind && t.IntTypeWidth() == ic10.SlotBits
}

// predicateType reports whether t is the one-bit integer a comparison
// produces — the case every whole-register pattern has to be held away
// from. The machine holds the value as 0 or 1, with the rest of the
// register unused.
func predicateType(t llvm.Type) bool {
	return t.TypeKind() == llvm.IntegerTypeKind && t.IntTypeWidth() == predicateWidth
}

// representable reports whether the machine can hold a value of type t.
//
// Only integers are constrained. A pointer is a slot index, and a label, a
// function, and void carry no value for a register to hold.
func representable(t llvm.Type) bool {
	if t.TypeKind() != llvm.IntegerTypeKind {
		return true
	}
	width := t.IntTypeWidth()
	return width == ic10.SlotBits || width == predicateWidth
}

// checkWidths refuses an instruction computing on an integer the machine
// has no representation for, before any pattern gets to select one. The
// optimizer produces these — a loop's induction variable can close-form
// into a multiply and shift over i65 — silently computing a wrong number.
func (s *selector) checkWidths(in llvm.Value) bool {
	if !s.widthOK(in, in.Type()) {
		return false
	}
	if in.InstructionOpcode() == llvm.Alloca {
		// An alloca's one operand is its element count, which the data region
		// layout reads at compile time and no register ever holds. LLVM types
		// it i32 whatever the module declares native.
		return true
	}
	for i := range in.OperandsCount() {
		operand := in.Operand(i)
		if operand.IsNil() {
			continue
		}
		if !s.widthOK(in, operand.Type()) {
			return false
		}
	}
	return true
}

func (s *selector) widthOK(in llvm.Value, t llvm.Type) bool {
	if representable(t) {
		return true
	}
	pos := s.position(in)
	// The wide chain is several instructions carrying the line of the one
	// expression they came from, so the same width at the same line is one
	// defect and is named once.
	key := fmt.Sprintf("%s %d", pos, t.IntTypeWidth())
	if s.widthReported[key] {
		return false
	}
	s.widthReported[key] = true
	s.errorf(pos, "%s computes on a %d bit integer; every register and memory slot holds one whole double, so the machine has no representation for anything but %d bits — the width comes from the range the optimizer proved for the expression, so bounding the values it accumulates is what brings it back within an int",
		describe(in), t.IntTypeWidth(), ic10.SlotBits)
	return false
}

func (s *selector) lower(info *blockInfo, in llvm.Value) {
	if !s.checkWidths(in) {
		return
	}
	if _, aliased := s.aliases[in]; aliased {
		// The value is one this stage already produced under another name: a
		// zero extension of an i1, a freeze, an address resolved to a fixed
		// slot, or a pointer difference reduced to one of its own terms. The
		// operand resolver follows the alias.
		return
	}
	if s.consumed[in] {
		// Absorbed into another instruction's pattern, which is the only reader
		// it had.
		return
	}
	if plan, difference := s.diffs[in]; difference {
		s.lowerPointerDiff(info, in, plan)
		return
	}
	op := in.InstructionOpcode()
	// Default is the rule: an operation with no pattern here has no machine
	// instruction, and a diagnostic is what an unselectable one owes rather
	// than a line that faults on the chip.
	//exhaustive:ignore
	switch op {
	case llvm.Alloca, llvm.PHI:
		// An alloca is a slot assignment, and a phi is copies on its incoming
		// edges. Neither is an instruction here.
	case llvm.Load:
		s.lowerLoad(info, in)
	case llvm.Store:
		s.lowerStore(info, in)
	case llvm.GetElementPtr:
		s.lowerGetElementPtr(info, in)
	case llvm.Add, llvm.Sub, llvm.Mul, llvm.And, llvm.Or, llvm.Shl, llvm.AShr, llvm.LShr,
		llvm.FAdd, llvm.FSub, llvm.FMul, llvm.FDiv:
		s.lowerBinary(info, in, op)
	case opcodeFNeg:
		// The machine has no negate. Multiplying by -1 is one instruction and
		// carries the sign of a zero across, which subtracting from zero does
		// not.
		s.emit(info, in, isa.OpMul, s.def(in), s.arg(in, 0), mir.Imm{Value: -1})
	case llvm.SExt, llvm.SIToFP:
		// A truth value read as signed: 0 or -1, where the machine holds it as 0
		// or 1 (a sitofp of the machine's own width is an alias instead). This
		// subtracts from zero, not multiplies by -1: 0 * -1 is -0.0 where LLVM's
		// false sign-extends to 0, and a device write reads the bits.
		s.emit(info, in, isa.OpSub, s.def(in), mir.Imm{Value: 0}, s.arg(in, 0))
	case llvm.Trunc:
		// Narrowing to a truth value keeps the low bit, which is the machine's
		// and against 1. Only narrowing to one bit reaches here. The bit is not
		// the value's own truth: an even value can be false or true, so a test
		// against zero would answer wrong for half the inputs.
		if s.checkConversionRange(in, isa.OpAnd, in.Operand(0)) {
			s.emit(info, in, isa.OpAnd, s.def(in), s.arg(in, 0), mir.Imm{Value: 1})
		}
	case llvm.PtrToInt:
		// A register holds a pointer as a slot index; the integer LLVM computed
		// with is the byte address the data layout gives that slot, so the
		// scale belongs here. [selector.planPointerDiffs] cancels it against
		// the element stride wherever the whole expression divides back down.
		s.emit(info, in, isa.OpMul, s.def(in), s.arg(in, 0), mir.Imm{Value: ic10.SlotBytes})
	case llvm.Xor:
		s.lowerXor(info, in)
	case llvm.SDiv, llvm.UDiv:
		s.lowerDiv(info, in)
	case llvm.SRem, llvm.URem:
		s.lowerRem(info, in)
	case llvm.ICmp, llvm.FCmp:
		s.lowerICmp(info, in)
	case llvm.Select:
		s.lowerSelect(info, in)
	case llvm.Call:
		s.lowerCall(info, in)
	case llvm.Br:
		s.lowerBr(info, in)
	case llvm.Switch:
		s.lowerSwitch(info, in)
	case llvm.Ret:
		s.lowerRet(info, in)
	case llvm.Unreachable:
		s.term(info, in, isa.OpJ, mir.Label{Name: s.endLbl})
	default:
		s.errorf(s.position(in), "%s uses an operation the backend has no machine instruction for; every register holds one whole double and the instruction set is fixed, so write the operation out of the arithmetic, the comparisons, and the __ic_ intrinsics the machine does have", describe(in))
	}
}

// lowerBinary selects the one machine instruction a binary operation is.
// The table is asked rather than indexed: a missing entry reads back as
// opcode zero (a real instruction), so an operation added to the dispatch
// switch but not the table would silently emit a wrong line.
func (s *selector) lowerBinary(info *blockInfo, in llvm.Value, op llvm.Opcode) {
	machineOp, known := binaryOps[op]
	if !known {
		s.errorf(s.position(in), "%s uses an operation the backend dispatches as arithmetic and has no machine instruction for; this is a defect in the compiler, not in the program", describe(in))
		return
	}
	if wrapped, wraps := predicateArithmetic[op]; wraps && predicateType(in.Type()) {
		machineOp = wrapped
	}
	if !s.checkConversionRange(in, machineOp, in.Operand(0), in.Operand(1)) {
		return
	}
	s.emit(info, in, machineOp, s.def(in), s.arg(in, 0), s.arg(in, 1))
}

// newInstr builds the machine instruction one pattern selected, reporting
// an operand the chip wouldn't read as written: one no position accepts
// (faulting every tick), or a literal whose conversion changes it. Nothing
// else in this package reaches mir.NewInstr directly; unpatterned
// instructions go through [unconverted] instead.
func (s *selector) newInstr(at llvm.Value, op ic10.Opcode, args []mir.Operand) *mir.Instr {
	pos := s.position(at)
	for _, arg := range args {
		if arg == nil {
			return nil
		}
	}
	if !s.checkOperandConversion(pos, op, args) {
		return nil
	}
	built, err := mir.NewInstr(op, pos, args...)
	if err != nil {
		s.errorf(pos, "the instruction selected for %s does not fit the machine: %v", describe(at), err)
		return nil
	}
	built.Inline = s.inlineChain(at)
	return built
}

// emit appends one instruction to the block's body.
func (s *selector) emit(info *blockInfo, at llvm.Value, op ic10.Opcode, args ...mir.Operand) {
	if built := s.newInstr(at, op, args); built != nil {
		info.body = append(info.body, built)
	}
}

// term appends one instruction to the block's terminator sequence, which
// assembly places after the copies a phi in a successor asked for.
func (s *selector) term(info *blockInfo, at llvm.Value, op ic10.Opcode, args ...mir.Operand) {
	if built := s.newInstr(at, op, args); built != nil {
		info.term = append(info.term, built)
	}
}

// def is the register an instruction's result lands in.
func (s *selector) def(in llvm.Value) mir.Operand {
	reg, ok := s.vregs[in]
	if !ok {
		s.errorf(s.position(in), "the result of %s was given no register", describe(in))
		return nil
	}
	return reg
}

// arg resolves operand i of an instruction, reporting an unusable one and
// returning nil so that emit drops the instruction rather than building it from
// a placeholder.
func (s *selector) arg(in llvm.Value, i int) mir.Operand {
	operand, err := s.operand(in.Operand(i))
	if err != nil {
		s.errorf(s.position(in), "operand %d of %s: %v", i+1, describe(in), err)
		return nil
	}
	return operand
}

// undefImm is what an undef or a poison operand becomes. Zero is one of the
// values the operand is allowed to be, and the machine has no representation
// for "any value at all".
var undefImm = mir.Imm{Value: 0}

// operand turns an LLVM value into a machine operand: a literal for a
// constant, the assigned register otherwise, or a slot index for a
// compile-time-fixed pointer (it resolves like a number, since at run time
// it is one). An undef or poison operand becomes zero — analysis already
// rejects reading an unassigned local, so nothing valid depends on it.
func (s *selector) operand(v llvm.Value) (mir.Operand, error) {
	for range maxOffsetDepth + 1 {
		if !v.IsAConstantInt().IsNil() {
			return exactImm(v)
		}
		if !v.IsAConstantFP().IsNil() {
			return constantFP(v)
		}
		if v.IsUndef() {
			return undefImm, nil
		}
		if slot, fixed := s.constantSlot(v); fixed {
			return mir.Imm{Value: float64(slot)}, nil
		}
		// The constant expression the optimizer folds a global's address into.
		// It is a byte address for the same reason the instruction spelling is,
		// and nothing lowers a constant expression, so the scale is applied
		// where the operand is read.
		if !v.IsAConstantExpr().IsNil() {
			if pointer, cast := ptrToIntOperand(v); cast {
				if slot, fixed := s.constantSlot(pointer); fixed {
					return mir.Imm{Value: float64(slot * ic10.SlotBytes)}, nil
				}
			}
		}
		if src, aliased := s.aliases[v]; aliased {
			v = src
			continue
		}
		if reg, ok := s.vregs[v]; ok {
			return reg, nil
		}
		return nil, errors.New("the value is neither a constant nor one the backend produced")
	}
	return nil, errors.New("the chain of values this one stands for is longer than the backend follows, or refers back to itself")
}

// immOf reads a constant as the double the machine holds.
//
// A one-bit constant is read unsigned: an i1 true sign-extends to -1, and the
// value a comparison produces is 1.
func immOf(v llvm.Value) mir.Imm {
	if predicateType(v.Type()) {
		return mir.Imm{Value: float64(v.ZExtValue())}
	}
	return mir.Imm{Value: float64(v.SExtValue())}
}

// twoPow63 is one past the largest int64. A double holds it exactly, which is
// why a constant at or above it is one no int64 round trip can confirm.
const twoPow63 = 1 << 63

// exactImm reads a constant, refusing one the machine cannot hold. Every
// register and slot is one IEEE double, so a constant is representable
// exactly when it survives the round trip through one — the optimizer can
// fold to the full i64 width, silently rounding a value the emitted
// literal would name differently. The bound is not a magnitude.
func exactImm(v llvm.Value) (mir.Imm, error) {
	imm := immOf(v)
	if predicateType(v.Type()) {
		return imm, nil
	}
	value := v.SExtValue()
	// The conversion back is defined only inside the int64 range, so the range
	// is tested first. Its lower end is a power of two and lands exactly on
	// MinInt64; its upper end does not, which is why the test there is strict.
	if imm.Value >= math.MinInt64 && imm.Value < twoPow63 && int64(imm.Value) == value {
		return imm, nil
	}
	return mir.Imm{}, fmt.Errorf("the constant %d is not one an IEEE double holds exactly, and every register and memory slot is one; the nearest value the chip would read is %s, so keep integer constants within 2^53",
		value, literalText(imm))
}

// literalText spells a literal the way an operand position takes it.
// mir.Imm.String renders with %g, which states a large value in exponent
// notation — a diagnostic that quotes one would name a number no emitted
// line could have held.
func literalText(imm mir.Imm) string {
	return strconv.FormatFloat(imm.Value, 'f', -1, 64)
}

// constantFP reads a floating-point constant, which needs no round-trip
// check: the only float type the front end emits is a double and every
// register holds one, so what the optimizer folded is what the chip reads.
func constantFP(v llvm.Value) (mir.Imm, error) {
	value, inexact := v.DoubleValue()
	if inexact {
		return mir.Imm{}, errors.New("the constant is a floating-point value wider than the double every register holds")
	}
	return mir.Imm{Value: value}, nil
}

// lowerXor folds an exclusive or against all ones into the machine's
// one-operand not, at the machine's value width only: an i1 all-ones is
// true, and the machine's not complements the whole double (false becomes
// -1, true becomes -2), so this fold does not apply to a one-bit value.
func (s *selector) lowerXor(info *blockInfo, in llvm.Value) {
	if !s.checkConversionRange(in, isa.OpXor, in.Operand(0), in.Operand(1)) {
		return
	}
	if machineWidth(in.Type()) {
		for i := range 2 {
			c := in.Operand(i)
			if c.IsAConstantInt().IsNil() || c.SExtValue() != -1 {
				continue
			}
			s.emit(info, in, isa.OpNot, s.def(in), s.arg(in, 1-i))
			return
		}
	}
	s.emit(info, in, isa.OpXor, s.def(in), s.arg(in, 0), s.arg(in, 1))
}

// lowerDiv synthesizes C's truncating division. The machine's div doesn't
// truncate (7/2 is 3.5 in a register), so the quotient goes through trunc
// (toward zero, not round's banker's rounding). A truncated-to-zero
// quotient can come out -0.0 (e.g. 0/-3); adding +0.0 restores the sign C
// states, since a device write reads the bits.
func (s *selector) lowerDiv(info *blockInfo, in llvm.Value) {
	quotient := s.fn.NewVirtReg()
	truncated := s.fn.NewVirtReg()
	s.emit(info, in, isa.OpDiv, quotient, s.arg(in, 0), s.arg(in, 1))
	s.emit(info, in, isa.OpTrunc, truncated, quotient)
	s.emit(info, in, isa.OpAdd, s.def(in), truncated, mir.Imm{})
}

// lowerRem synthesizes C's remainder. The machine's mod is not a floor
// modulus: it adds the divisor back once when the truncated remainder came
// out negative, correct only for a positive divisor (mod -7 3 is 2, mod -7
// -3 is -4, both where C gives -1). The identity a - trunc(a/b)*b gives C's
// answer for every divisor with no correction branch or zero-sign fix-up.
func (s *selector) lowerRem(info *blockInfo, in llvm.Value) {
	quotient := s.fn.NewVirtReg()
	truncated := s.fn.NewVirtReg()
	product := s.fn.NewVirtReg()
	dividend := s.arg(in, 0)
	divisor := s.arg(in, 1)
	s.emit(info, in, isa.OpDiv, quotient, dividend, divisor)
	s.emit(info, in, isa.OpTrunc, truncated, quotient)
	s.emit(info, in, isa.OpMul, product, truncated, divisor)
	s.emit(info, in, isa.OpSub, s.def(in), dividend, product)
}

// lowerICmp materialises a comparison as 0 or 1. A comparison whose only reader
// is its own block's branch never reaches here: it was folded into the branch.
func (s *selector) lowerICmp(info *blockInfo, in llvm.Value) {
	if s.fused[in] {
		return
	}
	plan, ok := s.comparison(in)
	if !ok {
		return
	}
	dst := s.def(in)
	if plan.negate && s.swapped[in] {
		// The select reading this takes the negation by trading its arms, so
		// the comparison answers what the machine has an instruction for.
		s.materialise(info, in, plan, dst)
		return
	}
	if plan.negate {
		// The plan answers the complement, so it lands in a scratch register
		// and seqz turns the truth value round.
		answer := s.fn.NewVirtReg()
		s.materialise(info, in, plan, answer)
		s.emit(info, in, isa.OpSeqz, dst, answer)
		return
	}
	s.materialise(info, in, plan, dst)
}

// materialise puts a plan's answer in dst as 0 or 1.
func (s *selector) materialise(info *blockInfo, in llvm.Value, plan comparisonPlan, dst mir.Operand) {
	if plan.join == joinOne {
		s.setStep(info, in, plan.steps[0], dst)
		return
	}
	left, right := s.fn.NewVirtReg(), s.fn.NewVirtReg()
	s.setStep(info, in, plan.steps[0], left)
	s.setStep(info, in, plan.steps[1], right)
	s.emit(info, in, joinOps[plan.join], dst, left, right)
}

// setStep materialises one step of a plan as 0 or 1 in dst.
func (s *selector) setStep(info *blockInfo, at llvm.Value, step comparisonStep, dst mir.Operand) {
	if step.isNaN {
		s.emit(info, at, isa.OpSnan, dst, step.lhs)
		return
	}
	if op, zero := setZeroOps[step.pred]; zero && isZero(step.rhs) {
		s.emit(info, at, op, dst, step.lhs)
		return
	}
	op, known := setOps[step.pred]
	if !known {
		s.errorf(s.position(at), "the machine has no set instruction for the predicate %s asks for", describe(at))
		return
	}
	s.emit(info, at, op, dst, step.lhs, step.rhs)
}

// comparison resolves a comparison into the steps the machine has instructions
// for, putting a zero operand second so that the against-zero forms apply.
func (s *selector) comparison(in llvm.Value) (comparisonPlan, bool) {
	lhs, rhs := s.arg(in, 0), s.arg(in, 1)
	if lhs == nil || rhs == nil {
		return comparisonPlan{}, false
	}
	if in.InstructionOpcode() == llvm.FCmp {
		return s.floatComparison(in, lhs, rhs)
	}
	pred := signedPredicate(in)
	if predicateType(in.Operand(0).Type()) {
		if machine, known := truthValuePredicates[pred]; known {
			pred = machine
		}
	}
	if _, direct := setOps[pred]; !direct {
		return s.unsignedComparison(in, pred, lhs, rhs)
	}
	if isZero(lhs) && !isZero(rhs) {
		pred, lhs, rhs = swappedPredicates[pred], rhs, lhs
	}
	return comparisonPlan{steps: []comparisonStep{{pred: pred, lhs: lhs, rhs: rhs}}}, true
}

// unsignedComparison rewrites an unsigned comparison against a non-negative
// constant into a pair of signed ones. MicroC has no unsigned type, so this
// only ever undoes InstCombine's own fold of "a >= 0 && a < C" into "a <u
// C" — exact under the target's value model, where every value lies within
// ±2^53: a negative a reads as at least 2^64-2^53 unsigned, above every
// non-negative i64 constant.
//
//	a <u  C  is  a >=s 0 && a <s  C
//	a <=u C  is  a >=s 0 && a <=s C
//	a >u  C  is  a <s  0 || a >s  C
//	a >=u C  is  a <s  0 || a >=s C
//
// Comparing two values has no such rewrite: it needs a sign-agreement test
// first, and what it should answer past 2^53 is undecided, so it's refused.
func (s *selector) unsignedComparison(in llvm.Value, pred llvm.IntPredicate, lhs, rhs mir.Operand) (comparisonPlan, bool) {
	if !isNonNegativeImm(rhs) && isNonNegativeImm(lhs) {
		pred, lhs, rhs = swappedPredicates[pred], rhs, lhs
	}
	rewrite, known := unsignedRewrites[pred]
	if !known || !isNonNegativeImm(rhs) {
		s.errorf(s.position(in), "%s became an unsigned test the machine cannot answer; every machine comparison is signed, and an unsigned one is rewritten into signed tests only when it bounds a single value by a non-negative constant, so write the bound as two signed comparisons joined by && rather than relying on the optimizer to fold them", describe(in))
		return comparisonPlan{}, false
	}
	steps := []comparisonStep{
		{pred: rewrite.sign, lhs: lhs, rhs: mir.Imm{Value: 0}},
		{pred: rewrite.bound, lhs: lhs, rhs: rhs},
	}
	return comparisonPlan{steps: steps, join: rewrite.join}, true
}

// isZero reports whether an operand is the literal zero, which is what the
// against-zero comparison and branch forms take. A negative zero counts
// too: those forms test a comparison against zero, which -0 passes.
func isZero(operand mir.Operand) bool {
	imm, ok := operand.(mir.Imm)
	return ok && imm.Value == 0
}

// isNonNegativeImm reports whether an operand is a literal at or above zero,
// which is the bound the unsigned rewrites need.
func isNonNegativeImm(operand mir.Operand) bool {
	imm, ok := operand.(mir.Imm)
	return ok && imm.Value >= 0
}

// signedPredicate reads a comparison's predicate, restoring the signed form
// InstCombine canonicalises into an unsigned one carrying samesign when it
// proves the operands share a sign bit — under that assertion the two
// predicates agree. The flag is read off the printed instruction, matched
// against the mnemonic, since the bindings expose no accessor for it.
func signedPredicate(in llvm.Value) llvm.IntPredicate {
	signed, unsigned := sameSignPredicates[in.IntPredicate()]
	if !unsigned || !strings.Contains(in.String(), "icmp samesign ") {
		return in.IntPredicate()
	}
	return signed
}

// lowerSelect emits the machine's select, one instruction against a
// branch's branch-plus-jump-plus-target. It reads as "dst = a ? b : c", so
// the condition is the first source operand. A condition
// [markSwappedSelect] absorbed answers the complement, undone by swapping arms.
func (s *selector) lowerSelect(info *blockInfo, in llvm.Value) {
	whenTrue, whenFalse := 1, 2
	if s.swapped[in.Operand(0)] {
		whenTrue, whenFalse = 2, 1
	}
	s.emit(info, in, isa.OpSelect, s.def(in), s.arg(in, 0), s.arg(in, whenTrue), s.arg(in, whenFalse))
}

func (s *selector) lowerBr(info *blockInfo, in llvm.Value) {
	if in.OperandsCount() == 1 {
		s.term(info, in, isa.OpJ, s.targetLabel(info.llvmBlock, in.Successor(0)))
		return
	}
	taken := s.targetLabel(info.llvmBlock, in.Successor(0))
	fallen := s.targetLabel(info.llvmBlock, in.Successor(1))

	cond := in.Operand(0)
	if !cond.IsAConstantInt().IsNil() {
		// A branch on a constant, which "while (true)" writes, is a jump.
		if cond.ZExtValue() != 0 {
			s.term(info, in, isa.OpJ, taken)
			return
		}
		s.term(info, in, isa.OpJ, fallen)
		return
	}

	if s.fused[cond] {
		plan, ok := s.comparison(cond)
		if !ok {
			return
		}
		s.branchPlan(info, in, cond, plan, taken, fallen)
		return
	}

	s.term(info, in, isa.OpBnez, s.arg(in, 0), taken)
	s.term(info, in, isa.OpJ, fallen)
}

// branchPlan emits the branches a fused comparison's steps become. A
// conjunction leaves for the false successor as soon as a step fails, so
// every step but the last branches on its negation; a disjunction sends
// every step to the true successor.
func (s *selector) branchPlan(info *blockInfo, in, cond llvm.Value, plan comparisonPlan, taken, fallen mir.Operand) {
	if plan.negate {
		// The plan answers the complement, and a branch's two successors are
		// exactly what the complement swaps. This is the canonicalisation
		// InstCombine performs on an fcmp in branch position, undone.
		taken, fallen = fallen, taken
	}
	for i, step := range plan.steps {
		target := taken
		if step.isNaN {
			// bnan has no complement to invert into, so a NaN step only ever
			// appears in a disjunction, where no step is negated.
			s.term(info, in, isa.OpBnan, step.lhs, target)
			continue
		}
		if plan.join == joinAnd && i < len(plan.steps)-1 {
			step.pred, target = negatedPredicates[step.pred], fallen
		}
		if op, zero := branchZeroOps[step.pred]; zero && isZero(step.rhs) {
			s.term(info, in, op, step.lhs, target)
			continue
		}
		op, known := branchOps[step.pred]
		if !known {
			s.errorf(s.position(in), "the machine has no compare-and-branch for the predicate %s asks for", describe(cond))
			return
		}
		s.term(info, in, op, step.lhs, step.rhs, target)
	}
	s.term(info, in, isa.OpJ, fallen)
}

// lowerSwitch emits the comparison chain a switch costs. There is no jump
// table: a computed jump would need the target line numbers, which exist only
// after emission.
func (s *selector) lowerSwitch(info *blockInfo, in llvm.Value) {
	tag := s.arg(in, 0)
	for i := 1; i < in.SuccessorsCount(); i++ {
		value := in.GetSwitchCaseValue(i)
		if value.IsAConstantInt().IsNil() {
			s.errorf(s.position(in), "a case label that is not a constant is not selected")
			continue
		}
		imm, err := exactImm(value)
		if err != nil {
			s.errorf(s.position(in), "the case label of this switch: %v", err)
			continue
		}
		target := s.targetLabel(info.llvmBlock, in.Successor(i))
		if isZero(imm) {
			s.term(info, in, isa.OpBeqz, tag, target)
			continue
		}
		s.term(info, in, isa.OpBeq, tag, imm, target)
	}
	s.term(info, in, isa.OpJ, s.targetLabel(info.llvmBlock, in.Successor(0)))
}

// lowerRet leaves the function. From the entry function that means leaving
// the program: the chip resumes where the previous tick left off and
// nothing re-enters, so this jumps to the empty block past the last
// instruction, dropped by emission when nothing follows. Any other
// function reaches the epilogue instead, returning through ra.
func (s *selector) lowerRet(info *blockInfo, in llvm.Value) {
	if in.OperandsCount() != 0 {
		if s.isEntry {
			s.errorf(s.position(in), "the entry point returns a value, and nothing is there to receive it")
			return
		}
		s.emit(info, in, isa.OpMove, mir.PhysReg{Reg: resultRegister}, s.arg(in, 0))
	}
	s.term(info, in, isa.OpJ, mir.Label{Name: s.endLbl})
}
