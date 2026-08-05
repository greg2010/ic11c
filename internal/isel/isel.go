// Package isel selects IC10 machine instructions for an LLVM module,
// producing internal/mir for register allocation. A pointer is a slot
// index at run time. Branch targets are absolute: internal/mir refuses to
// construct the relative forms a later line-count change would corrupt.
package isel

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strconv"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/isa"
	"github.com/greg2010/ic11c/internal/llvmir"
	"github.com/greg2010/ic11c/internal/mir"
	"github.com/greg2010/ic11c/internal/sema"
	"github.com/greg2010/ic11c/internal/source"
	"tinygo.org/x/go-llvm"
)

// Options configures selection.
type Options struct {
	// File names the source. LLVM debug locations carry a line and a column but
	// no file, so it is restored here.
	File string
	// Lines restores the byte offset a debug location does not carry. It may be
	// nil, which leaves every reconstructed position at offset zero; ordering is
	// unaffected either way, since positions compare by line and column first.
	Lines *source.LineMap
	// InlineSites names the function spliced in at each call site, keyed by the
	// call's line and column. It is irgen.Result.InlineSites, and is what lets
	// the size report say which call an inlined body's bytes belong to. A site
	// missing from it is still attributed, by position alone.
	InlineSites map[source.LineCol]string
}

// Result is the selected program and the memory it committed.
type Result struct {
	Program *mir.Program
	// DataSlots counts the slots globals, arrays, and address-taken locals
	// occupy from slot 0 upward, an array taking one per element. It is where
	// spill slots may start: regalloc.Config.SpillSlotBase.
	DataSlots int
	// CallingConvention reports whether any real call was selected. sp and ra
	// are ordinary registers holding whatever a value was put in them until one
	// is, which is what regalloc.Config.Reserved has to say.
	CallingConvention bool
	// Recursive names every function that can reach itself through a call.
	// Each activation of one shares the data region slots the last used, so a
	// spill slot or an address-taken local in it holds the wrong activation's
	// value on return.
	Recursive map[string]bool
}

// Select lowers every defined function in m to machine IR. A construct
// outside the selected subset is reported as a [source.DiagnosticList]
// rather than lowered into something that faults once per tick on the
// chip. Funcs[0] is the entry: execution starts at line 0.
func Select(ctx context.Context, m llvm.Module, opts Options) (*Result, error) {
	if m.IsNil() {
		return nil, errors.New("isel: nil module")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("isel: %w", err)
	}

	defined, err := definedFunctions(m)
	if err != nil {
		return nil, err
	}

	s := &selector{
		ctx:           ctx,
		mod:           m,
		pos:           llvmir.Positions{File: opts.File, Lines: opts.Lines},
		inlineSites:   opts.InlineSites,
		widthReported: make(map[string]bool),
		recursive:     recursiveFunctions(defined),
		programEnd:    sema.EntryFunction + ".exit",
		unplaced:      planPlacement(m),
		unranged:      planInt32Range(m),
	}
	// Before the globals are placed, since placing one can report against it:
	// a fact about the module as a whole has no line of its own, and the entry
	// is where a reader looking for the program goes.
	s.entryPos = s.pos.Func(defined[0])
	s.assignGlobalSlots()

	prog := &mir.Program{}
	for i, fn := range defined {
		s.isEntry = i == 0
		s.isLast = i == len(defined)-1
		selected, err := s.function(fn)
		if err != nil {
			return nil, err
		}
		if selected != nil {
			prog.Funcs = append(prog.Funcs, selected)
		}
	}
	s.checkDataRegion()
	if err := s.diags.Err(); err != nil {
		return nil, err
	}
	// Every function that produced no diagnostic was selected, and
	// definedFunctions refuses a module holding none, so Funcs[0] is the entry.
	if err := s.prologue(prog.Funcs[0]); err != nil {
		return nil, err
	}
	if err := prog.Validate(); err != nil {
		return nil, fmt.Errorf("isel: selected program is malformed: %w", err)
	}
	return &Result{
		Program:           prog,
		DataSlots:         s.nextSlot,
		CallingConvention: s.callsSelected,
		// Cloned so that a caller holding the result cannot reach the selector's
		// own map, which is still the answer every later query here reads.
		Recursive: maps.Clone(s.recursive),
	}, nil
}

// definedFunctions lists the module's definitions with the entry first, since
// emission order is execution order and the chip starts at line 0.
func definedFunctions(m llvm.Module) ([]llvm.Value, error) {
	var entry llvm.Value
	var rest []llvm.Value
	for fn := m.FirstFunction(); !fn.IsNil(); fn = llvm.NextFunction(fn) {
		if fn.IsDeclaration() {
			continue
		}
		if fn.Name() == sema.EntryFunction {
			entry = fn
			continue
		}
		rest = append(rest, fn)
	}
	if entry.IsNil() {
		return nil, fmt.Errorf("isel: the module defines no %s to start at line 0", sema.EntryFunction)
	}
	return append([]llvm.Value{entry}, rest...), nil
}

// selector carries the state shared across every function of one module.
type selector struct {
	ctx         context.Context
	mod         llvm.Module
	pos         llvmir.Positions
	inlineSites map[source.LineCol]string

	// slots maps each object in the data region to the slot it starts at, and
	// each pointer whose value is fixed at compile time to the slot it names.
	// Globals are placed first and keep their slots for the whole program;
	// allocas are placed per function.
	slots    map[llvm.Value]int
	nextSlot int
	// extents gives the slots each placed object holds, keyed by the object
	// itself. Membership is what separates an object from the pointers slots
	// also holds, and the count is the length a literal index is held against.
	extents map[llvm.Value]int
	// origins maps a pointer whose slot is fixed at compile time to the object
	// its address was computed from. A pointer whose walk reached no placed
	// object is absent, and nothing but the array as a whole bounds it.
	origins map[llvm.Value]llvm.Value

	diags         source.DiagnosticList
	widthReported map[string]bool

	// unplaced holds the values whose register may fall outside the machine's
	// conversion to a signed 64-bit integer, which is what decides whether an
	// optimizer-formed bitwise instruction may be selected at all.
	unplaced map[llvm.Value]bool
	// unranged holds the values whose register may fall outside the signed
	// 32-bit range, deciding whether a narrowed operand may be filled. It is
	// not unplaced: the machine's bitwise conversion places a NaN by
	// answering zero, at ±2^63 rather than this position's 2^31.
	unranged map[llvm.Value]bool

	// recursive names the functions that can reach themselves through a call.
	recursive map[string]bool
	// callsSelected records that some function's body ended up holding a jal.
	// It is what Result.CallingConvention reports; the module's function
	// count is not a substitute, since the optimizer can leave a defined
	// function whose every call it inlined or proved dead.
	callsSelected bool
	// programEnd labels the block one past the last instruction. A return from
	// the entry function jumps there, which takes the program counter out of
	// the program and ends the run.
	programEnd string
	// entryPos is where the entry function was declared — what a diagnostic
	// about the module as a whole is reported against, since such a fact has
	// no one line that caused it and the entry is where a reader looking for
	// the program would go.
	entryPos source.Position

	fn *mir.Func
	// isEntry and isLast place the function being selected in the program:
	// the entry ends the run where every other function returns to its caller,
	// and the last function carries the block the entry ends at.
	isEntry bool
	isLast  bool
	vregs   map[llvm.Value]mir.VirtReg
	aliases map[llvm.Value]llvm.Value
	fused   map[llvm.Value]bool
	// swapped holds the comparisons whose sole reader is a select that absorbs
	// their negation by trading its arms.
	swapped map[llvm.Value]bool
	// consumed holds the instructions another instruction's pattern absorbed,
	// which produce no value and no line of their own.
	consumed map[llvm.Value]bool
	// absorbed counts the reads an address plan made of each instruction it
	// took apart; absorbers names the instructions those plans stand in for.
	// [selector.markAbsorbed] settles the two into consumed once every plan
	// exists, since a value one plan absorbed can still be read elsewhere.
	absorbed  map[llvm.Value]int
	absorbers map[llvm.Value]bool
	// geps holds the slot arithmetic of every getelementptr that needs any,
	// which is one whose offset is not fixed at compile time.
	geps map[llvm.Value]gepPlan
	// diffs holds the slot arithmetic of every pointer difference whose stride
	// this stage divided out.
	diffs map[llvm.Value]slotPlan
	// covers memoises [selector.coverOf], which reads a value through the values
	// it is built from and would otherwise walk a shared subexpression once per
	// path that reaches it.
	covers map[llvm.Value]cover
	blocks map[llvm.BasicBlock]*blockInfo
	order  []llvm.BasicBlock
	endLbl string
}

// blockInfo is one LLVM block's machine form while it is being built. body
// and term stay apart until assembly, since a phi's copies land after the
// block's computation and before its branch, and a block can end in
// several instructions (a fused compare-and-branch, then a jump).
type blockInfo struct {
	llvmBlock llvm.BasicBlock
	block     *mir.Block
	body      []*mir.Instr
	term      []*mir.Instr
	pos       source.Position
	// index is the block's position in the function, which is what names one
	// the optimizer left without a name.
	index int
	// split names the successors whose edge needs a block of its own, and edges
	// maps each of those to the block that carries its copies.
	split map[llvm.BasicBlock]bool
	edges map[llvm.BasicBlock]*mir.Block
	// copies holds the sequenced phi transfers per successor.
	copies map[llvm.BasicBlock][]*mir.Instr
	succs  []llvm.BasicBlock
}

// assignGlobalSlots places every module global in the data region, slot 0
// upward; the boundary the stack pointer must stay above starts past
// everything placed here. An array takes one slot per element — nothing
// packs, since every element is one double, the whole of one slot.
func (s *selector) assignGlobalSlots() {
	s.slots = make(map[llvm.Value]int)
	s.extents = make(map[llvm.Value]int)
	s.origins = make(map[llvm.Value]llvm.Value)
	for g := s.mod.FirstGlobal(); !g.IsNil(); g = llvm.NextGlobal(g) {
		size, ok := slotsOf(g.GlobalValueType())
		if !ok {
			s.errorf(s.entryPos, "the global '%s' does not lay out in whole memory slots; 'long long', 'double', 'bool', a pointer, and an array of them are what the data region holds", g.Name())
			continue
		}
		s.place(g, size)
	}
}

// slotsOf reports how many memory slots an object of type t occupies. It
// reports false for a type the data region has no layout for: an integer that
// is not the machine's one value width, and anything that is neither a scalar
// nor an array of them.
func slotsOf(t llvm.Type) (int, bool) {
	// Default is the rule: a type the data region has no layout for is refused,
	// and the caller names it.
	//exhaustive:ignore
	switch t.TypeKind() {
	case llvm.IntegerTypeKind:
		return 1, t.IntTypeWidth() == ic10.SlotBits
	case llvm.DoubleTypeKind:
		return 1, true
	case llvm.PointerTypeKind:
		return 1, true
	case llvm.ArrayTypeKind:
		elem, ok := slotsOf(t.ElementType())
		return elem * t.ArrayLength(), ok
	default:
		return 0, false
	}
}

// byteSizeOf gives the size the data layout assigns t, the unit a
// getelementptr states its offsets in. It is not slotsOf scaled up: the
// optimizer restates an offset over an i8 element type wherever it can, and
// an i8 is exactly the stride slotsOf refuses to lay an object out in.
func byteSizeOf(t llvm.Type) (int, bool) {
	// Default is the rule: a type the data layout assigns no size this stage
	// can use is refused.
	//exhaustive:ignore
	switch t.TypeKind() {
	case llvm.IntegerTypeKind:
		width := t.IntTypeWidth()
		if width%8 != 0 {
			return 0, false
		}
		return width / 8, true
	case llvm.DoubleTypeKind, llvm.PointerTypeKind:
		return ic10.SlotBytes, true
	case llvm.ArrayTypeKind:
		elem, ok := byteSizeOf(t.ElementType())
		return elem * t.ArrayLength(), ok
	default:
		return 0, false
	}
}

func (s *selector) errorf(pos source.Position, format string, args ...any) {
	s.diags.Addf(pos, format, args...)
}

// position recovers the source location an instruction was generated from.
// A missing location means the optimizer formed it rather than moved it
// (hoisted a loop invariant, rewrote a comparison as a mask, built a phi
// no source wrote a merge for), so this falls back to the enclosing function.
func (s *selector) position(in llvm.Value) source.Position {
	if pos, located := s.pos.Instr(in); located {
		return pos
	}
	return s.enclosingPos()
}

// enclosingPos is the narrowest place a diagnostic about an instruction with no
// location of its own can name: the function being selected, or the entry point
// before selection has entered one.
func (s *selector) enclosingPos() source.Position {
	if s.fn != nil && s.fn.Pos.IsValid() {
		return s.fn.Pos
	}
	return s.entryPos
}

// inlineChain is the sequence of calls an instruction's code was spliced
// through, innermost first. Both the location's inlinedAt chain and
// [selector.inlineSites] are needed: metadata has no callee name, and the
// front end doesn't know which instructions survived.
func (s *selector) inlineChain(in llvm.Value) []source.InlineSite {
	loc := in.InstructionDebugLoc()
	if loc.IsNil() {
		return nil
	}
	var chain []source.InlineSite
	for site := loc.LocationInlinedAt(); !site.IsNil(); site = site.LocationInlinedAt() {
		pos := s.pos.Location(site)
		chain = append(chain, source.InlineSite{Pos: pos, Callee: s.inlineSites[pos.LineCol()]})
	}
	return chain
}

func (s *selector) function(fnValue llvm.Value) (*mir.Func, error) {
	if err := s.ctx.Err(); err != nil {
		return nil, fmt.Errorf("isel: %w", err)
	}
	name := fnValue.Name()
	s.fn = mir.NewFunc(name, s.pos.Func(fnValue))
	s.vregs = make(map[llvm.Value]mir.VirtReg)
	s.aliases = make(map[llvm.Value]llvm.Value)
	s.fused = make(map[llvm.Value]bool)
	s.swapped = make(map[llvm.Value]bool)
	s.consumed = make(map[llvm.Value]bool)
	s.absorbed = make(map[llvm.Value]int)
	s.absorbers = make(map[llvm.Value]bool)
	s.geps = make(map[llvm.Value]gepPlan)
	s.diffs = make(map[llvm.Value]slotPlan)
	s.covers = make(map[llvm.Value]cover)
	s.blocks = make(map[llvm.BasicBlock]*blockInfo)
	s.order = nil
	s.endLbl = s.programEnd
	if !s.isEntry {
		s.endLbl = name + ".exit"
	}

	for bb := fnValue.FirstBasicBlock(); !bb.IsNil(); bb = llvm.NextBasicBlock(bb) {
		s.order = append(s.order, bb)
	}
	if !s.assignParams(fnValue) {
		return nil, nil
	}
	s.assignLocalSlots(name)
	s.planAddresses()
	s.planPointerDiffs()
	s.planIdentityMasks()
	s.markAbsorbed()
	s.assignValues()
	s.planEdges()
	s.createBlocks(name)
	s.lowerParams(fnValue)
	s.lowerBlocks()
	s.lowerPhis()
	if err := s.diags.Err(); err != nil {
		return nil, nil
	}
	s.assemble()
	s.callsSelected = s.callsSelected || s.callsOut()
	s.frame()
	return s.fn, nil
}

// prologue prepends the zeroing the data region needs before anything reads
// it: chip state survives power loss, removal, and reflashing, so nothing
// starts at zero, and clr db zeroes all 512 slots in one instruction. It's
// emitted only for a program that puts anything in the data region at all.
// sp is not set here — regalloc.SetStackBase does that once spill slots are
// known.
func (s *selector) prologue(entryFn *mir.Func) error {
	if s.nextSlot == 0 || len(entryFn.Blocks) == 0 {
		return nil
	}
	entry := entryFn.Blocks[0]
	zeroing, err := unconverted(isa.OpClr, entryFn.Pos, mir.NewDeviceBase())
	if err != nil {
		return fmt.Errorf("isel: the data region zeroing prologue: %w", err)
	}
	entry.Instrs = append([]*mir.Instr{zeroing}, entry.Instrs...)
	return nil
}

// assignLocalSlots gives every alloca its data region slots. An alloca
// survives optimization only when its address was taken or its object is
// an array. A non-constant count is unreachable in a valid program: sema
// already refuses a non-constant bound.
func (s *selector) assignLocalSlots(name string) {
	for _, bb := range s.order {
		for in := range llvmir.BlockInstrs(bb) {
			if in.InstructionOpcode() != llvm.Alloca {
				continue
			}
			count := in.Operand(0)
			if count.IsAConstantInt().IsNil() {
				s.errorf(s.position(in), "this local has a length that is not known at compile time, and the data region is laid out at compile time; give the array a constant bound")
				continue
			}
			size, ok := slotsOf(in.AllocatedType())
			if !ok {
				s.errorf(s.position(in), "this local does not lay out in whole memory slots; 'long long', 'double', 'bool', a pointer, and an array of them are what the data region holds")
				continue
			}
			if s.recursive[name] {
				s.errorf(s.position(in), "'%s' can reach itself through a call, and this local needs a data region slot; the slot is one address for every activation, so the inner call would overwrite the outer one's value — pass the value as a parameter and return it, or rewrite the recursion as a loop", name)
				continue
			}
			s.place(in, size*int(count.ZExtValue()))
		}
	}
}

// place gives an object the next slots in the data region and records how many
// it holds, which is the length [selector.inObject] bounds an index against.
func (s *selector) place(object llvm.Value, size int) {
	s.slots[object] = s.nextSlot
	s.extents[object] = size
	s.nextSlot += size
}

// originOf names the object a pointer's address was computed from, for a
// pointer whose slot is fixed at compile time. It follows the alias chain
// for the same reason [selector.operand] does: an instruction handing its
// operand back unchanged is the value the literal came from.
func (s *selector) originOf(ptr llvm.Value) (llvm.Value, bool) {
	for range maxOffsetDepth + 1 {
		if _, placed := s.extents[ptr]; placed {
			return ptr, true
		}
		if origin, named := s.origins[ptr]; named {
			return origin, true
		}
		src, aliased := s.aliases[ptr]
		if !aliased {
			return llvm.Value{}, false
		}
		ptr = src
	}
	return llvm.Value{}, false
}

// checkDataRegion holds the data region's slot count against the array it
// shares with the call frames. The one case the count alone can't decide is
// filling it exactly: sp lands at the far side, so a program that calls
// faults on its first push where one that doesn't never touches a frame.
func (s *selector) checkDataRegion() {
	pos := s.entryPos
	switch {
	case s.nextSlot > ic10.NumMemorySlots:
		s.errorf(pos, "globals, arrays, and address-taken locals need %d of the %d memory slots; shorten an array or drop a global", s.nextSlot, ic10.NumMemorySlots)
	case s.nextSlot == ic10.NumMemorySlots && s.callsSelected:
		// Register allocation reports the same shape once it knows the spill
		// count, and its advice includes splitting an expression because a spill
		// is a value it placed. Nothing here is, so splitting one changes
		// nothing this count measures.
		s.errorf(pos, "globals, arrays, and address-taken locals fill all %d memory slots, and the program makes a call, whose return address needs a slot above them; shorten an array or drop a global", ic10.NumMemorySlots)
	}
}

// planAddresses resolves every getelementptr in the function ahead of
// lowering. One whose offset is fixed at compile time names a slot and
// needs no instruction; one stepping by a runtime index gets the adds
// planned here, and a register from [selector.assignValues].
func (s *selector) planAddresses() {
	for _, bb := range s.order {
		for in := range llvmir.BlockInstrs(bb) {
			if in.InstructionOpcode() != llvm.GetElementPtr {
				continue
			}
			if _, fixed := s.constantSlot(in); fixed {
				continue
			}
			plan, visited, ok := s.gepPlanOf(in)
			if !ok {
				// Reported when the instruction is lowered, where the position
				// and the surrounding diagnostics belong.
				continue
			}
			s.absorb(in, visited)
			if len(plan.terms) == 0 && plan.fixed == 0 {
				s.aliases[in] = plan.base
				continue
			}
			// An index into the object at slot 0 is already the slot it names.
			// Adding a base of zero is not arithmetic, and the add it would
			// cost lands inside whatever loop the subscript sits in.
			if base, fixed := s.constantSlot(plan.base); fixed && base == 0 &&
				plan.fixed == 0 && len(plan.terms) == 1 && plan.terms[0].scale == 1 &&
				plan.terms[0].shift == 0 {
				s.aliases[in] = plan.terms[0].value
				continue
			}
			s.geps[in] = plan
		}
	}
}

// constantSlot resolves a pointer whose value is fixed at compile time to
// the slot it names, memoizing so a chain of address arithmetic costs one
// walk. "Constant" is a property of the offset, not the spelling: a
// pointer cast the walk resolves this way is recorded as absorbed too.
func (s *selector) constantSlot(ptr llvm.Value) (int, bool) {
	return s.constantSlotAt(ptr, 0)
}

func (s *selector) constantSlotAt(ptr llvm.Value, depth int) (int, bool) {
	if depth > maxOffsetDepth {
		return 0, false
	}
	if slot, ok := s.slots[ptr]; ok {
		return slot, true
	}
	switch {
	case !ptr.IsAInstruction().IsNil() && ptr.InstructionOpcode() == llvm.GetElementPtr:
	case !ptr.IsAConstantExpr().IsNil() && ptr.Opcode() == llvm.GetElementPtr:
	default:
		return 0, false
	}
	plan, visited, ok := s.gepPlanOf(ptr)
	if !ok || len(plan.terms) != 0 {
		return 0, false
	}
	base, ok := s.constantSlotAt(plan.base, depth+1)
	if !ok {
		return 0, false
	}
	slot := base + plan.fixed
	s.slots[ptr] = slot
	if origin, named := s.originOf(plan.base); named {
		s.origins[ptr] = origin
	}
	// After the base resolves, not before: a walk that took an instruction apart
	// and then failed on the base is one whose caller plans the address again,
	// and the second walk would credit the same read twice.
	s.absorb(ptr, visited)
	return slot, true
}

// assignValues hands out one virtual register per value-producing
// instruction, so a phi or back edge can name a register defined later in
// layout order. Two kinds get none: one [selector.producesOperand] answers
// for, and a comparison folded into its block's own branch.
func (s *selector) assignValues() {
	for _, bb := range s.order {
		s.markFusedCompare(bb)
		s.markSwappedSelect(bb)
	}
	for _, bb := range s.order {
		for in := range llvmir.BlockInstrs(bb) {
			if _, placed := s.slots[in]; placed {
				continue
			}
			if _, aliased := s.aliases[in]; aliased {
				continue
			}
			if s.producesOperand(in) {
				s.aliases[in] = in.Operand(0)
				continue
			}
			if in.Type().TypeKind() == llvm.VoidTypeKind || in.InstructionOpcode() == llvm.Alloca || s.fused[in] || s.consumed[in] {
				continue
			}
			s.vregs[in] = s.fn.NewVirtReg()
		}
	}
}

// opcodeFreeze is LLVMFreeze and opcodeFNeg is LLVMFNeg. The bindings define no
// constant for either, so the enum values are spelled numerically;
// TestFreezeOpcode and TestFNegOpcode pin them against instructions the linked
// libLLVM built.
const (
	opcodeFNeg   = llvm.Opcode(66)
	opcodeFreeze = llvm.Opcode(68)
)

// producesOperand reports whether an instruction hands its operand back
// unchanged, needing no register of its own.
func (s *selector) producesOperand(in llvm.Value) bool {
	// Anything not named below isn't free.
	//exhaustive:ignore
	switch in.InstructionOpcode() {
	case llvm.ZExt:
		// A truth value (0 or 1) widened unsigned is free: the register
		// already holds it.
		return predicateType(in.Operand(0).Type())
	case llvm.SIToFP:
		// Signed widening of a truth value needs a real instruction: sign
		// extension means 0 or -1, not the mul-by -1 the machine spells (0
		// * -1 is -0.0). At the machine's own i64 width this is free
		// instead, since the register already holds the exact double.
		return !predicateType(in.Operand(0).Type())
	case llvm.Select:
		return truthSelect(in)
	case opcodeFreeze, llvm.UIToFP, llvm.FPToSI:
		// Unconditionally free: the register holds the same double
		// throughout.
		return true
	default:
		// ptrtoint is deliberately not free: the register holds a slot
		// index where LLVM computed a byte address, so [selector.lower]
		// emits the scale rather than aliasing.
		return false
	}
}

// truthSelect reports whether an instruction chooses between one and zero
// on a truth value — the value it already holds. InstCombine forms it by
// widening a comparison to a MicroC value (uitofp of an i1), canonicalising
// into this select; the machine's set instructions already produce 0 or 1.
func truthSelect(in llvm.Value) bool {
	if !predicateType(in.Operand(0).Type()) {
		return false
	}
	return isConstantNumber(in.Operand(1), 1) && isConstantNumber(in.Operand(2), 0)
}

// isConstantNumber reports whether v is the integer or floating-point constant
// want.
func isConstantNumber(v llvm.Value, want float64) bool {
	if !v.IsAConstantInt().IsNil() {
		return float64(v.SExtValue()) == want
	}
	if v.IsAConstantFP().IsNil() {
		return false
	}
	value, inexact := v.DoubleValue()
	return !inexact && value == want
}

// markFusedCompare records a comparison the block's own conditional branch is
// its only reader. Fusing saves an instruction and the register it would have
// landed in, and the machine has a branch form for every predicate.
func (s *selector) markFusedCompare(bb llvm.BasicBlock) {
	term := bb.LastInstruction()
	if term.IsNil() || term.InstructionOpcode() != llvm.Br || term.OperandsCount() != 3 {
		return
	}
	cond := term.Operand(0)
	if cond.IsAInstruction().IsNil() {
		return
	}
	if cond.InstructionOpcode() != llvm.ICmp && cond.InstructionOpcode() != llvm.FCmp {
		return
	}
	if cond.InstructionParent() != bb {
		return
	}
	use := cond.FirstUse()
	if use.IsNil() || !use.NextUse().IsNil() {
		return
	}
	if use.User() != term {
		return
	}
	s.fused[cond] = true
}

// createBlocks builds machine blocks in layout order: each LLVM block, then
// blocks for its critical outgoing edges, and finally the blocks a return
// leaves through — layout order is emission order, which is what makes the
// jump to the following block droppable.
func (s *selector) createBlocks(fnName string) {
	for _, bb := range s.order {
		info := s.blocks[bb]
		info.block = s.fn.NewBlock(blockLabel(fnName, bb, info.index), info.pos)
		for _, succ := range info.succs {
			if !info.split[succ] {
				continue
			}
			label := info.block.Label + ".to." + blockName(succ, s.blocks[succ].index)
			info.edges[succ] = s.fn.NewBlock(label, info.pos)
		}
	}
	if !s.isEntry {
		s.fn.NewBlock(s.endLbl, s.fn.Pos)
	}
	if s.isLast {
		s.fn.NewBlock(s.programEnd, s.fn.Pos)
	}
}

func blockLabel(fnName string, bb llvm.BasicBlock, index int) string {
	return fnName + "." + blockName(bb, index)
}

// blockName names one block within its function. A block the optimizer
// produced carries no name, and LLVM identifies it by position instead, so
// the position has to reach the label — two nameless blocks would
// otherwise share a label and send a branch into the wrong one.
func blockName(bb llvm.BasicBlock, index int) string {
	if name := bb.AsValue().Name(); name != "" {
		return name
	}
	return "bb" + strconv.Itoa(index)
}

// targetLabel names the block a branch from one LLVM block to another
// should reach: the successor itself, or the edge block carrying that
// edge's copies. Returns nil for a successor no machine block was built
// for — failing compilation rather than misdirecting control flow.
func (s *selector) targetLabel(from, to llvm.BasicBlock) mir.Operand {
	origin, known := s.blocks[from]
	if known {
		if edge := origin.edges[to]; edge != nil {
			return mir.Label{Name: edge.Label}
		}
	}
	if info, ok := s.blocks[to]; ok && info.block != nil {
		return mir.Label{Name: info.block.Label}
	}
	pos := s.entryPos
	if known {
		pos = origin.pos
	}
	s.errorf(pos, "control leaves this statement for a place the backend laid out no code at; this is a defect in the compiler, not in the program")
	return nil
}

// assemble joins each block's body, its outgoing phi copies, and its
// terminator, wires up the successor edges liveness needs, and drops the jumps
// control would reach their targets without.
func (s *selector) assemble() {
	for _, bb := range s.order {
		info := s.blocks[bb]
		instrs := info.body
		for _, succ := range info.succs {
			if info.edges[succ] != nil {
				continue
			}
			instrs = append(instrs, info.copies[succ]...)
		}
		info.block.Instrs = append(instrs, info.term...)

		for _, succ := range info.succs {
			target := s.blocks[succ].block
			if edge := info.edges[succ]; edge != nil {
				copies := info.copies[succ]
				edge.Instrs = append(edge.Instrs, copies...)
				jump, err := unconverted(isa.OpJ, info.pos, mir.Label{Name: target.Label})
				if err != nil {
					s.errorf(info.pos, "the jump onto the edge into %s: %v", target.Label, err)
					continue
				}
				jump.Inline = edgeInline(copies)
				edge.Instrs = append(edge.Instrs, jump)
				edge.AddSucc(target)
				info.block.AddSucc(edge)
				continue
			}
			info.block.AddSucc(target)
		}

		// A block with no LLVM successor is a return or an unreachable, both
		// lowering to a jump to the exit block, which appears in no LLVM
		// successor list. The lookup is nil for the entry function, whose exit
		// is the program-end block on the last function, outside Succs.
		if len(info.succs) == 0 {
			if exit := s.blockByLabel(s.endLbl); exit != nil {
				info.block.AddSucc(exit)
			}
		}
	}
	mir.DropFallthroughJumps(s.fn)
}

// edgeInline is the call site chain an edge block's jump belongs to: the
// one its copies carry. The block exists for those copies, so the jump
// belongs to the same inlined call — charging it to the enclosing function
// instead would split one edge block across two size-report units.
func edgeInline(copies []*mir.Instr) []source.InlineSite {
	if len(copies) == 0 {
		return nil
	}
	return copies[0].Inline
}
