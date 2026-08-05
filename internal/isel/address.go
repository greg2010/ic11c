package isel

import (
	"math"
	"math/bits"
	"slices"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/isa"
	"github.com/greg2010/ic11c/internal/llvmir"
	"github.com/greg2010/ic11c/internal/mir"
	"github.com/greg2010/ic11c/internal/source"
	"tinygo.org/x/go-llvm"
)

// addressTerm is one runtime value an address expression contributes and
// how many slots it's worth: value>>shift scaled by scale (in slots, not
// bytes; negative for a subtracted term). shift is the byte-stride part
// the scale couldn't carry — see [slotAlignment].
type addressTerm struct {
	value llvm.Value
	scale int
	shift int
}

// gepPlan is one getelementptr resolved into the arithmetic the machine
// performs on a slot index: the object the address starts from, the slots the
// constant indices step over, and the runtime indices.
type gepPlan struct {
	base  llvm.Value
	fixed int
	terms []addressTerm
}

// gepPlanOf reads the offset a getelementptr states, in the slot space the
// machine addresses in — the byte stride is divided out here so it reaches
// no emitted instruction. Reports false for an offset that doesn't land on
// whole slots; visited instructions come back once per read.
func (s *selector) gepPlanOf(gep llvm.Value) (gepPlan, []llvm.Value, bool) {
	var bytes slotPlan
	var visited []llvm.Value
	elem := gep.GEPSourceElementType()
	for i := 1; i < gep.OperandsCount(); i++ {
		stride, ok := byteSizeOf(elem)
		if !ok {
			return gepPlan{}, nil, false
		}
		index := gep.Operand(i)
		switch {
		case !index.IsAConstantInt().IsNil():
			step, ok := scaleBy(int(index.SExtValue()), stride)
			if !ok {
				return gepPlan{}, nil, false
			}
			bytes.fixed += step
		case stride%ic10.SlotBytes == 0:
			bytes.terms = append(bytes.terms, addressTerm{value: index, scale: stride})
		default:
			var sub slotPlan
			var seen []llvm.Value
			if !s.walkByteOffset(index, stride, &sub, &seen, 0) {
				return gepPlan{}, nil, false
			}
			bytes.fixed += sub.fixed
			bytes.terms = append(bytes.terms, sub.terms...)
			visited = append(visited, seen...)
		}
		if i+1 == gep.OperandsCount() {
			break
		}
		// Every index after the first steps within the type the one before it
		// selected. Only arrays are indexed into here; MicroC has no aggregate
		// whose members sit at differing offsets.
		if elem.TypeKind() != llvm.ArrayTypeKind {
			return gepPlan{}, nil, false
		}
		elem = elem.ElementType()
	}
	slots, ok := bytes.inSlots()
	if !ok {
		return gepPlan{}, nil, false
	}
	return gepPlan{base: gep.Operand(0), fixed: slots.fixed, terms: slots.terms}, visited, true
}

// inSlots divides a plan stated in bytes down into slot space, reporting
// false for a constant offset or a term that isn't a whole number of slots.
// Terms are rewritten in place, so the caller must own them. A zero-stride
// term is dropped — the one place that happens, so [slotScale] and below
// always see a stride with a sign.
func (p slotPlan) inSlots() (slotPlan, bool) {
	if p.fixed%ic10.SlotBytes != 0 {
		return slotPlan{}, false
	}
	kept := p.terms[:0]
	for _, term := range p.terms {
		if term.scale == 0 {
			continue
		}
		scale, shift, ok := slotScale(term)
		if !ok {
			return slotPlan{}, false
		}
		term.scale, term.shift = scale, shift
		kept = append(kept, term)
	}
	clear(p.terms[len(kept):])
	p.terms = kept
	p.fixed /= ic10.SlotBytes
	return p, true
}

// dividedTerms reports whether any term of a resolved plan still divides its
// value at run time, which is the byte stride reaching the emitted program
// rather than being taken out of it.
func (p slotPlan) dividedTerms() bool {
	return slices.ContainsFunc(p.terms, func(term addressTerm) bool { return term.shift != 0 })
}

// slotScale divides one term's byte stride into slot space: the factors of
// two in the stride move to the value's side, and what's left of the
// division must be guaranteed by [slotAlignment] or the term is refused —
// otherwise the address would land inside a slot.
func slotScale(term addressTerm) (scale, shift int, ok bool) {
	magnitude := term.scale
	if magnitude < 0 {
		magnitude = -magnitude
	}
	divisor := ic10.SlotBytes
	for divisor > 1 && magnitude%2 == 0 {
		magnitude /= 2
		divisor /= 2
	}
	shift = bits.TrailingZeros(uint(divisor))
	if shift > slotAlignment(term.value) {
		return 0, 0, false
	}
	if term.scale < 0 {
		magnitude = -magnitude
	}
	return magnitude, shift, true
}

// slotAlignment reports the power of two a value is known to be a multiple
// of (its guaranteed low zero bits). A mask over a constant is the one
// shape here whose divisibility is in the value, not the stride: the
// optimizer restates a bounded index as a mask over the byte offset.
func slotAlignment(v llvm.Value) int {
	if v.IsAInstruction().IsNil() || v.InstructionOpcode() != llvm.And {
		return 0
	}
	zeros := 0
	for i := range 2 {
		mask := v.Operand(i)
		if mask.IsAConstantInt().IsNil() {
			continue
		}
		zeros = max(zeros, bits.TrailingZeros64(uint64(mask.SExtValue())))
	}
	return zeros
}

// scaleBy multiplies one address factor by another, reporting false for a
// product an int can't hold, and for math.MinInt specifically: it negates
// to itself, so a term meant to be subtracted would emit as an addition.
// Refusing it here lets [slotScale], [selector.scaledTerm], and
// [selector.walkByteOffset] all read scale's magnitude without checking.
func scaleBy(scale, factor int) (int, bool) {
	product := scale * factor
	if scale != 0 && product/scale != factor {
		return 0, false
	}
	if product == math.MinInt {
		return 0, false
	}
	return product, true
}

// slotBytesShift is the shift the optimizer canonicalises a division by
// ic10.SlotBytes into. Both spellings reach this stage: InstCombine rewrites a
// signed division by a power of two into an arithmetic shift once it has proved
// the division exact, and leaves it alone otherwise.
var slotBytesShift = int64(bits.TrailingZeros(ic10.SlotBytes))

// maxScaleShift is the first shift distance [selector.walkByteOffset]
// declines: the scale 1<<distance it would form stops fitting a signed int.
// Distances at or past it wrap toward zero or toward math.MinInt (which
// [scaleBy] separately refuses), so this catches the wrap before either
// happens.
const maxScaleShift = 62

// maxOffsetDepth bounds every walk over an address expression — the byte
// offset decomposition, the constant slot resolution, and the alias chain —
// so a malformed use chain can't recurse without end, and so the three
// walks agree on which chains a program may have.
const maxOffsetDepth = 32

// slotPlan is an address expression resolved into slot space: a constant offset
// in slots and the runtime values that contribute to it. It differs from a
// gepPlan in having no object to start from, since a difference of two
// addresses has cancelled one out.
type slotPlan struct {
	fixed int
	terms []addressTerm
}

// planPointerDiffs resolves the distance between two pointers into slot
// space, so the element stride reaches no emitted instruction: LLVM states
// the difference in bytes, so this divides term by term — the same
// division [selector.gepPlanOf] does for a subscript. An unresolved
// expression falls back to ordinary arithmetic, kept correct by
// [selector.lower]'s ptrtoint scale.
func (s *selector) planPointerDiffs() {
	for _, bb := range s.order {
		for in := range llvmir.BlockInstrs(bb) {
			// The right operand that makes the instruction a division by
			// ic10.SlotBytes. A shift states it as a distance, so the two arms
			// hold different numbers for the same division.
			bySlotBytes := int64(0)
			// Default is the rule: an instruction that is not one of the two
			// division spellings absorbs no stride.
			//exhaustive:ignore
			switch in.InstructionOpcode() {
			case llvm.SDiv:
				bySlotBytes = ic10.SlotBytes
			case llvm.AShr:
				bySlotBytes = slotBytesShift
			default:
				continue
			}
			if !isImmediate(in.Operand(1), bySlotBytes) {
				continue
			}
			plan, visited, ok := s.planByteOffset(in.Operand(0))
			if !ok {
				continue
			}
			// The instruction is the division already, over the whole offset and
			// in one line. A plan whose terms carry part of the stride pays for
			// it once per term, so it is not a rewrite of the division but a
			// multiplication of it, and the shift is left to select as itself.
			if plan.dividedTerms() {
				continue
			}
			s.absorb(in, visited)
			if len(plan.terms) == 1 && plan.terms[0].scale == 1 && plan.fixed == 0 {
				s.aliases[in] = plan.terms[0].value
				continue
			}
			s.diffs[in] = plan
		}
	}
}

// absorb records that the plan replacing root took apart visited, once per
// read the walk made of each. root becomes an absorber (excluded from
// [selector.keepSharedTerms]'s readers) however little it took apart. A
// term the plan reads adds to the value's use count without adding to the
// reads recorded here, keeping [selector.markAbsorbed] from claiming it.
func (s *selector) absorb(root llvm.Value, visited []llvm.Value) {
	s.absorbers[root] = true
	for _, value := range visited {
		s.absorbed[value]++
	}
}

// markAbsorbed settles which instructions the address plans took apart no
// longer produce a value, after every plan has been made. An instruction is
// worth nothing only when the plans took apart every read of it — one
// shared with an ordinary use still has to produce its value.
func (s *selector) markAbsorbed() {
	for value, taken := range s.absorbed {
		if taken == useCount(value) {
			s.consumed[value] = true
		}
	}
	s.keepSharedTerms()
}

// keepSharedTerms takes back every value the read count over-claimed that
// an emitted instruction still needs: a walk credits every node it passes
// through on the way to a term, even one another plan later absorbs whole.
// The outer loop runs to a fixpoint because consumed only shrinks and
// absorbers is fixed, so the result doesn't depend on map iteration order.
func (s *selector) keepSharedTerms() {
	for changed := true; changed; {
		changed = false
		for value := range s.consumed {
			for use := value.FirstUse(); !use.IsNil(); use = use.NextUse() {
				user := use.User()
				if s.absorbers[user] || s.consumed[user] {
					continue
				}
				delete(s.consumed, value)
				changed = true
				break
			}
		}
	}
}

// planByteOffset resolves a byte-valued expression into slot space, reporting
// false for one that does not divide into whole slots. The instructions it took
// apart are returned once per read, so that a caller can tell which of them
// nothing else still wants.
func (s *selector) planByteOffset(v llvm.Value) (slotPlan, []llvm.Value, bool) {
	var plan slotPlan
	var visited []llvm.Value
	if !s.walkByteOffset(v, 1, &plan, &visited, 0) {
		return slotPlan{}, nil, false
	}
	plan, ok := plan.inSlots()
	if !ok {
		return slotPlan{}, nil, false
	}
	return plan, visited, true
}

// walkByteOffset accumulates one node of an address expression into plan.
// outer is what the enclosing expression multiplies the node by (a
// subtraction's sign, or a getelementptr index's stride). Scales and the
// constant offset stay in bytes until the caller divides them.
func (s *selector) walkByteOffset(v llvm.Value, outer int, plan *slotPlan, visited *[]llvm.Value, depth int) bool {
	if depth > maxOffsetDepth {
		return false
	}
	if !v.IsAConstantInt().IsNil() {
		offset, ok := scaleBy(outer, int(v.SExtValue()))
		if !ok {
			return false
		}
		plan.fixed += offset
		return true
	}
	// A pointer contributes its slot scaled up to the byte address the rest of
	// the expression is stated in, so a constant address and a computed one
	// reduce the same way. A constant expression is not recorded: it emits
	// nothing to begin with.
	if pointer, ok := ptrToIntOperand(v); ok {
		scale, ok := scaleBy(outer, ic10.SlotBytes)
		if !ok {
			return false
		}
		if slot, fixed := s.constantSlot(pointer); fixed {
			offset, ok := scaleBy(scale, slot)
			if !ok {
				return false
			}
			plan.fixed += offset
		} else {
			plan.terms = append(plan.terms, addressTerm{value: pointer, scale: scale})
		}
		if !v.IsAInstruction().IsNil() {
			*visited = append(*visited, v)
		}
		return true
	}
	if v.IsAInstruction().IsNil() {
		return false
	}
	// Default is the rule: a term this walk cannot resolve leaves the whole
	// expression alone, which selects as ordinary arithmetic.
	//exhaustive:ignore
	switch v.InstructionOpcode() {
	case llvm.Add:
		if !s.walkByteOffset(v.Operand(0), outer, plan, visited, depth+1) ||
			!s.walkByteOffset(v.Operand(1), outer, plan, visited, depth+1) {
			return false
		}
	case llvm.Sub:
		if !s.walkByteOffset(v.Operand(0), outer, plan, visited, depth+1) ||
			!s.walkByteOffset(v.Operand(1), -outer, plan, visited, depth+1) {
			return false
		}
	case llvm.Shl:
		shift := v.Operand(1)
		if shift.IsAConstantInt().IsNil() || shift.SExtValue() < 0 || shift.SExtValue() >= maxScaleShift {
			return false
		}
		scale, ok := scaleBy(outer, 1<<uint(shift.SExtValue()))
		if !ok {
			return false
		}
		plan.terms = append(plan.terms, addressTerm{value: v.Operand(0), scale: scale})
	case llvm.And:
		// The one node the walk takes whole rather than apart. What makes it a
		// term is [slotAlignment], which reads off the mask what the enclosing
		// scale does not carry; a mask that establishes nothing is refused by
		// [slotScale] rather than here, so one place decides what divides.
		plan.terms = append(plan.terms, addressTerm{value: v, scale: outer})
		return true
	case llvm.Mul:
		// Either operand position, because which one holds the constant is a
		// canonicalisation rather than a guarantee, and the wrong answer for
		// the other order is a refused program rather than a smaller one.
		value, factor := v.Operand(0), v.Operand(1)
		if factor.IsAConstantInt().IsNil() {
			value, factor = factor, value
		}
		if factor.IsAConstantInt().IsNil() {
			return false
		}
		scale, ok := scaleBy(outer, int(factor.SExtValue()))
		if !ok {
			return false
		}
		plan.terms = append(plan.terms, addressTerm{value: value, scale: scale})
	default:
		return false
	}
	*visited = append(*visited, v)
	return true
}

// lowerPointerDiff emits the slot arithmetic a pointer difference costs: one
// add or subtract per term past the first, and one more when a constant offset
// is left over.
func (s *selector) lowerPointerDiff(info *blockInfo, in llvm.Value, plan slotPlan) {
	result := s.def(in)
	if result == nil {
		return
	}
	start, steps, ok := s.diffSteps(info, in, plan)
	if !ok {
		return
	}
	s.accumulate(info, in, result, start, steps)
}

// addressStep is one operand applied to the running total of an address
// expression.
type addressStep struct {
	op      ic10.Opcode
	operand mir.Operand
}

// accumulate emits the running total an address expression becomes. Every
// step but the last lands in a temporary, so the value's own register is
// written once, by the instruction that finishes the address. A plan with
// no step is its starting operand, which still has to reach that register.
func (s *selector) accumulate(info *blockInfo, in llvm.Value, result, start mir.Operand, steps []addressStep) {
	if len(steps) == 0 {
		s.emit(info, in, isa.OpMove, result, start)
		return
	}
	acc := start
	for i, step := range steps {
		dst := result
		if i < len(steps)-1 {
			dst = s.fn.NewVirtReg()
		}
		s.emit(info, in, step.op, dst, acc, step.operand)
		acc = dst
	}
}

// diffSteps turns a plan into a starting operand and the sequence applied
// to it. A plan with no added term starts from the constant offset and
// subtracts down (one instruction, where negating a term first would cost
// two); otherwise the constant is added last.
func (s *selector) diffSteps(info *blockInfo, in llvm.Value, plan slotPlan) (mir.Operand, []addressStep, bool) {
	positive := false
	for _, term := range plan.terms {
		positive = positive || term.scale > 0
	}

	var start mir.Operand
	if !positive {
		start = mir.Imm{Value: float64(plan.fixed)}
	}
	var steps []addressStep
	for _, term := range plan.terms {
		operand := s.scaledTerm(info, in, term)
		if operand == nil {
			return nil, nil, false
		}
		if start == nil && term.scale > 0 {
			start = operand
			continue
		}
		op := ic10.Opcode(isa.OpAdd)
		if term.scale < 0 {
			op = isa.OpSub
		}
		steps = append(steps, addressStep{op: op, operand: operand})
	}
	if positive && plan.fixed != 0 {
		steps = append(steps, addressStep{op: isa.OpAdd, operand: mir.Imm{Value: float64(plan.fixed)}})
	}
	return start, steps, true
}

// scaledTerm gives the slot count one term contributes: a divide when part
// of the byte stride is in the value, a multiply when the stride is more
// than one slot. It divides rather than shifts because the value is a
// double, not an integer register — a divide is exact where a shift would
// first need to reinterpret the bits.
func (s *selector) scaledTerm(info *blockInfo, in llvm.Value, term addressTerm) mir.Operand {
	operand, err := s.operand(term.value)
	if err != nil {
		s.errorf(s.position(in), "a term of %s: %v", describe(in), err)
		return nil
	}
	if term.shift > 0 {
		divided := s.fn.NewVirtReg()
		s.emit(info, in, isa.OpDiv, divided, operand, mir.Imm{Value: float64(int(1) << term.shift)})
		operand = divided
	}
	scale := term.scale
	if scale < 0 {
		scale = -scale
	}
	if scale == 1 {
		return operand
	}
	scaled := s.fn.NewVirtReg()
	s.emit(info, in, isa.OpMul, scaled, operand, mir.Imm{Value: float64(scale)})
	return scaled
}

// ptrToIntOperand reads the pointer behind a cast to an integer, in both the
// instruction and the constant expression spelling.
func ptrToIntOperand(v llvm.Value) (llvm.Value, bool) {
	switch {
	case !v.IsAInstruction().IsNil() && v.InstructionOpcode() == llvm.PtrToInt:
		return v.Operand(0), true
	case !v.IsAConstantExpr().IsNil() && v.Opcode() == llvm.PtrToInt:
		return v.Operand(0), true
	default:
		return llvm.Value{}, false
	}
}

// useCount counts the operand positions naming v across the function.
func useCount(v llvm.Value) int {
	n := 0
	for use := v.FirstUse(); !use.IsNil(); use = use.NextUse() {
		n++
	}
	return n
}

func isImmediate(v llvm.Value, want int64) bool {
	return !v.IsAConstantInt().IsNil() && v.SExtValue() == want
}

// address resolves the pointer of a memory access to the slot operand the
// machine addresses with. A pointer tracing to no object has already been
// reported by internal/pointers, which runs before this stage; what's left
// to fail here is a pointer computation this stage has no pattern for.
func (s *selector) address(at llvm.Value, ptr llvm.Value) mir.Operand {
	operand, resolved := s.slotOperand(at, ptr)
	if !resolved || !s.inMemoryArray(at, operand) {
		return nil
	}
	return operand
}

func (s *selector) slotOperand(at llvm.Value, ptr llvm.Value) (mir.Operand, bool) {
	operand, err := s.operand(ptr)
	if err != nil {
		s.errorf(s.position(at), "the address %s reads does not resolve to a memory slot: %v", describe(at), err)
		return nil, false
	}
	return operand, true
}

// inMemoryArray checks a slot index fixed at compile time against the
// memory array, and passes anything computed at run time. A literal is
// checked because the chip doesn't: get answers an out-of-range address
// with the unknown error and poke with a stack overflow, once per tick.
func (s *selector) inMemoryArray(at llvm.Value, operand mir.Operand) bool {
	imm, literal := operand.(mir.Imm)
	if !literal || (imm.Value >= 0 && imm.Value < ic10.NumMemorySlots) {
		return true
	}
	s.errorf(s.position(at), "%s is at memory slot %s, and the chip has slots 0 through %d; the address is outside the memory array itself, so shorten the index, or shorten an array or drop a global to lay the objects out lower",
		describe(at), literalText(imm), ic10.NumMemorySlots-1)
	return false
}

// inObject checks the address of a compile-time-fixed access against the
// length of the object it was computed from — the machine and the array
// can't be asked this at run time, since the data region lays objects end
// to end: the slot past a short global belongs to the next one, and the
// slot past the last object is where push writes the return address.
func (s *selector) inObject(at llvm.Value, ptr llvm.Value, operand mir.Operand) bool {
	imm, literal := operand.(mir.Imm)
	if !literal {
		return true
	}
	origin, named := s.originOf(ptr)
	if !named {
		return true
	}
	element := imm.Value - float64(s.slots[origin])
	length := s.extents[origin]
	if element >= 0 && element < float64(length) {
		return true
	}
	s.errorf(s.position(at), "%s reaches element %s of %s, which holds %s; the slot it names was laid out for another object, so index within the length the object was declared with",
		describe(at), literalText(mir.Imm{Value: element}), objectName(origin), source.Plural(length, "element"))
	return false
}

// objectName names a placed object the way the source does, since an SSA value
// number would name the module rather than the program the user wrote. The
// optimizer strips the name of a local it rewrote, which leaves nothing to
// quote and the surrounding position to locate it.
func objectName(object llvm.Value) string {
	if name := object.Name(); name != "" {
		return "'" + name + "'"
	}
	return "an unnamed variable"
}

// lowerLoad and lowerStore reach memory through get/poke, whose address
// operand the chip narrows without any bound this stage can state for a
// register. On Mono amd64, that unchecked cast answers -2^31 for a NaN,
// either infinity, or any out-of-range magnitude: below the array, so poke
// raises StackUnderFlow and get answers the unknown error; past the far
// end, poke raises StackOverFlow instead.
func (s *selector) lowerLoad(info *blockInfo, in llvm.Value) {
	s.emit(info, in, isa.OpGet, s.def(in), mir.NewDeviceBase(), s.accessAddress(in, in.Operand(0)))
}

func (s *selector) lowerStore(info *blockInfo, in llvm.Value) {
	s.emit(info, in, isa.OpPoke, s.accessAddress(in, in.Operand(1)), s.arg(in, 0))
}

// accessAddress resolves the pointer an access reaches memory through — the
// one position where both bounds apply, since the address is the whole of
// what the instruction reads or writes. The narrower bound (the object) is
// asked first, so a failing address is reported against the object named
// rather than the array every object shares.
func (s *selector) accessAddress(at llvm.Value, ptr llvm.Value) mir.Operand {
	operand, resolved := s.slotOperand(at, ptr)
	if !resolved || !s.inObject(at, ptr, operand) || !s.inMemoryArray(at, operand) {
		return nil
	}
	return operand
}

// lowerGetElementPtr emits the slot arithmetic a subscript at a runtime
// index costs. A subtracted term comes from [selector.walkByteOffset]
// taking a subtraction apart — an index over the element type never
// produces one. A scale other than one needs a multiply: no MicroC array is
// multi-dimensional, but the optimizer can fold a subscript into a constant
// expression over a wider element type.
func (s *selector) lowerGetElementPtr(info *blockInfo, in llvm.Value) {
	if _, resolved := s.slots[in]; resolved {
		return
	}
	plan, planned := s.geps[in]
	if !planned {
		s.errorf(s.position(in), "the address %s computes does not land on whole memory slots; every element of an array is one slot and nothing packs, so index the array by elements rather than by a byte offset", describe(in))
		return
	}

	base := s.address(in, plan.base)
	result := s.def(in)
	if base == nil || result == nil {
		return
	}
	fixed := plan.fixed
	if imm, literal := base.(mir.Imm); literal && fixed != 0 && len(plan.terms) > 0 {
		// A literal base and a literal offset are one literal, so the constant
		// indices cost nothing beyond the add the runtime ones already need.
		// The sum is an addressing literal the base alone was not, so it is
		// checked again: only the base went through s.address.
		folded := mir.Imm{Value: imm.Value + float64(fixed)}
		if !s.inMemoryArray(in, folded) {
			return
		}
		base, fixed = folded, 0
	}

	steps := make([]addressStep, 0, len(plan.terms)+1)
	for _, term := range plan.terms {
		scaled := s.scaledTerm(info, in, term)
		if scaled == nil {
			return
		}
		op := ic10.Opcode(isa.OpAdd)
		if term.scale < 0 {
			op = isa.OpSub
		}
		steps = append(steps, addressStep{op: op, operand: scaled})
	}
	if fixed != 0 {
		steps = append(steps, addressStep{op: isa.OpAdd, operand: mir.Imm{Value: float64(fixed)}})
	}
	// An index into the object at slot 0 already names that slot, so the base
	// contributes nothing and the add it would cost lands inside whatever loop
	// the subscript sits in. A first step that subtracts can't drop the base
	// this way: zero minus the term is the term negated, not what starting
	// from it computes.
	if imm, literal := base.(mir.Imm); literal && imm.Value == 0 && len(steps) > 0 && steps[0].op == isa.OpAdd {
		base, steps = steps[0].operand, steps[1:]
	}
	s.accumulate(info, in, result, base, steps)
}
