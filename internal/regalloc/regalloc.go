// Package regalloc rewrites a mir.Func from virtual registers to physical ones
// by linear scan over live intervals, spilling to the data region.
package regalloc

import (
	"fmt"
	"maps"
	"slices"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/isa"
	"github.com/greg2010/ic11c/internal/mir"
	"github.com/greg2010/ic11c/internal/source"
)

// Config parameterises allocation. The zero value reserves nothing and holds
// back no scratch register, which is usable only for a function that fits in
// the register file.
type Config struct {
	// Reserved names registers no virtual register may be given. sp and ra
	// belong here whenever the calling convention is in use.
	Reserved []ic10.Register
	// Scratch names registers to reload spilled operands into. Every entry must
	// be within r0 through r15, since a reload can land in a device reference
	// position and only those registers may hold one. It is a ceiling: a
	// function that spills gives up only as many as its widest line reads.
	Scratch []ic10.Register
	// SpillSlotBase is the first data region slot spill slots may occupy. The
	// caller advances it by Result.SpillSlots after each function, since one
	// flat array holds every function's spill slots and none crosses a function
	// boundary to say a slot came free.
	SpillSlotBase int
}

// DefaultScratch is the scratch set a whole program is allocated against: the
// top three general registers. Three is the most register-capable sources any
// selected instruction reads; that no calling-convention register lands among
// them is enforced by a test in internal/isel. Each call returns a fresh slice.
func DefaultScratch() []ic10.Register {
	// Spelled against the general register count rather than as a literal, so
	// that a file of a different width moves the set with it.
	const top = ic10.NumGeneralRegisters - 1
	return []ic10.Register{top - 2, top - 1, top}
}

// Result reports what allocation decided, so a caller can compute the memory
// layout boundary and attribute the size of what was emitted.
type Result struct {
	// SpillSlots counts the data region slots consumed, starting at
	// Config.SpillSlotBase. The boundary sp must stay above is
	// SpillSlotBase+SpillSlots.
	SpillSlots int
	// assigned maps each virtual register that lives in a register to it, and
	// spilled maps each of the rest to its absolute data region slot, offset by
	// Config.SpillSlotBase. The two are disjoint: this allocator does not split,
	// so an interval either keeps a register for its whole lifetime or spills.
	assigned map[mir.VirtReg]ic10.Register
	spilled  map[mir.VirtReg]int
}

// Allocate rewrites fn in place, replacing every virtual register operand with
// a physical one and inserting spill code where the register file did not
// suffice.
//
// It accepts mir.RegFormVirtual or mir.RegFormMixed and leaves
// mir.RegFormPhysical or mir.RegFormEmpty. fn is unchanged when an error is
// returned: the rewrite is planned in full and applied only once every
// instruction is known to be expressible.
func Allocate(fn *mir.Func, cfg Config) (Result, error) {
	if fn == nil {
		return Result{}, fmt.Errorf("regalloc: function is nil")
	}
	if err := cfg.validate(); err != nil {
		return Result{}, fmt.Errorf("regalloc: %s: %w", fn.Name, err)
	}

	nums, err := number(fn)
	if err != nil {
		return Result{}, fmt.Errorf("regalloc: %s: %w", fn.Name, err)
	}
	intervals, constrained, err := buildIntervals(fn, nums)
	if err != nil {
		return Result{}, fmt.Errorf("regalloc: %s: %w", fn.Name, err)
	}

	pinned := preassigned(fn)
	if err := cfg.checkPinned(pinned); err != nil {
		return Result{}, fmt.Errorf("regalloc: %s: %w", fn.Name, err)
	}
	if err := cfg.checkCalls(fn, pinned); err != nil {
		return Result{}, fmt.Errorf("regalloc: %s: %w", fn.Name, err)
	}

	assigned, spilled, scratch, err := cfg.allocateRegisters(fn, intervals, constrained, pinned)
	if err != nil {
		return Result{}, fmt.Errorf("regalloc: %s: %w", fn.Name, err)
	}
	slots, count := assignSlots(spilled, cfg.SpillSlotBase)
	if err := outOfSlots(fn, cfg.SpillSlotBase, count); err != nil {
		return Result{}, err
	}

	res := Result{SpillSlots: count, assigned: assigned, spilled: slots}
	plan, err := planRewrite(fn, res, scratch, callSaves(fn, nums, intervals, assigned))
	if err != nil {
		if _, ok := source.DiagnosticsIn(err); ok {
			return Result{}, err
		}
		return Result{}, fmt.Errorf("regalloc: %s: %w", fn.Name, err)
	}
	plan.apply()
	return res, nil
}

// outOfSlots reports a function whose spill slots would reach past the memory
// array, as a diagnostic against the function rather than as a failure of this
// package: the source declared more live at once than the register file and
// the array below base have room for, which the programmer can act on.
func outOfSlots(fn *mir.Func, base, count int) error {
	if base+count <= ic10.NumMemorySlots {
		return nil
	}
	var diags source.DiagnosticList
	diags.Addf(fn.Pos, "'%s' holds more values at once than the register file has room for, and the %s it would spill into reach past the %d slot memory array, of which %s already hold globals, arrays, address-taken locals and what other functions spilled; shorten an array, drop a global, or split the expression so that fewer values are live at once",
		fn.Name, source.Plural(count, "slot"), ic10.NumMemorySlots, source.Plural(base, "slot"))
	return diags.Err()
}

// SetStackBase prepends to the entry point the instruction that puts sp above
// the data region, which shares one 512-slot array with the call stack and
// leaves nothing between them. Chip state survives reflashing, so sp holds
// whatever the last program left there — nothing may push before this runs.
func SetStackBase(entry *mir.Func, base int) error {
	if entry == nil || len(entry.Blocks) == 0 {
		return fmt.Errorf("regalloc: the entry function has no block to set sp in")
	}
	if base < 0 || base > ic10.NumMemorySlots {
		return fmt.Errorf("regalloc: a stack base of %d is outside the %d slot memory array", base, ic10.NumMemorySlots)
	}
	if base == ic10.NumMemorySlots {
		var diags source.DiagnosticList
		diags.Addf(entry.Pos, "the globals, arrays, address-taken locals and spilled values fill all %d memory slots, leaving no slot above them for the call stack to start at; shorten an array, drop a global, or split an expression so that fewer values are live at once", ic10.NumMemorySlots)
		return diags.Err()
	}
	set, err := mir.NewInstr(isa.OpMove, entry.Pos, mir.PhysReg{Reg: ic10.RegSP}, mir.Imm{Value: float64(base)})
	if err != nil {
		return fmt.Errorf("regalloc: setting sp to %d: %w", base, err)
	}
	block := entry.Blocks[0]
	block.Instrs = append([]*mir.Instr{set}, block.Instrs...)
	return nil
}

func (c Config) validate() error {
	seen := make(map[ic10.Register]string, len(c.Reserved)+len(c.Scratch))
	for _, r := range c.Reserved {
		if r >= ic10.NumRegisters {
			return fmt.Errorf("reserved register %d is outside the register file of %d", r, ic10.NumRegisters)
		}
		seen[r] = "reserved"
	}
	for _, r := range c.Scratch {
		if r >= ic10.NumGeneralRegisters {
			return fmt.Errorf("scratch register %s is outside r0-r15, which is the only range a device reference may be held in", r)
		}
		if role, dup := seen[r]; dup {
			return fmt.Errorf("scratch register %s is already %s", r, role)
		}
		seen[r] = "scratch"
	}
	if c.SpillSlotBase < 0 || c.SpillSlotBase > ic10.NumMemorySlots {
		return fmt.Errorf("spill slot base %d is outside the %d slot data region", c.SpillSlotBase, ic10.NumMemorySlots)
	}
	return nil
}

// checkPinned refuses a scratch register the input already names: reloading a
// spilled operand into one would silently overwrite a value this package
// cannot see is live. internal/isel enforces by test that [DefaultScratch]
// never collides with a calling convention's pinned registers.
func (c Config) checkPinned(pinned map[ic10.Register]bool) error {
	for _, r := range c.Scratch {
		if pinned[r] {
			return fmt.Errorf("scratch register %s is already named by an instruction in this function, and a reload into it would overwrite a value with no live range here; hold scratch registers apart from the ones the calling convention passes arguments and results in", r)
		}
	}
	return nil
}

// checkCalls refuses a configuration that leaves sp or ra allocatable in a
// function that makes a call: a link opcode writes ra and the frame this
// package wraps a call in moves sp, and no operand of either names the
// register, so nothing else keeps allocation off them.
func (c Config) checkCalls(fn *mir.Func, pinned map[ic10.Register]bool) error {
	free := c.allocatable(pinned)
	for _, reg := range clobberedByCalls(fn) {
		if slices.Contains(free, reg) {
			return fmt.Errorf("this function makes a call and %s is left allocatable, so a value would be placed in the register the call writes or the frame moves; reserve sp and ra whenever the calling convention is in use", reg)
		}
	}
	return nil
}

// clobberedByCalls lists the registers a call in fn writes without naming one
// in an operand, plus those the frame this package wraps it in writes the same
// way. The set is read from [ic10.Instruction.Implicit] rather than spelled
// here, so it moves with the instruction table.
func clobberedByCalls(fn *mir.Func) []ic10.Register {
	var regs []ic10.Register
	reached := func(op ic10.Opcode) {
		info, known := op.Instruction()
		if !known {
			return
		}
		for _, use := range info.Implicit {
			if info.WritesImplicitly(use.Register) && !slices.Contains(regs, use.Register) {
				regs = append(regs, use.Register)
			}
		}
	}
	calls := false
	for _, instr := range fn.AllInstrs() {
		if ic10.LinksReturn(instr.Op) {
			calls = true
			reached(instr.Op)
		}
	}
	if !calls {
		return nil
	}
	reached(isa.OpPush)
	reached(isa.OpPop)
	slices.Sort(regs)
	return regs
}

// allocateRegisters places every interval and returns the prefix of
// Config.Scratch it held back to do it. It re-scans with more scratch reserved
// whenever a round's spill set demands more than was held back, since a
// narrower allocatable set can produce a different, larger spill set. Reserved
// only grows, which bounds the rounds by len(Config.Scratch).
func (c Config) allocateRegisters(fn *mir.Func, intervals []*interval, constrained map[mir.VirtReg]bool, pinned map[ic10.Register]bool) (map[mir.VirtReg]ic10.Register, []*interval, []ic10.Register, error) {
	assigned, spilled, err := scan(intervals, constrained, c.allocatable(pinned), c)
	if err != nil {
		return nil, nil, nil, err
	}
	reserved := 0
	for len(spilled) > 0 {
		wanted, err := scratchDemand(fn, inMemory(spilled))
		if err != nil {
			return nil, nil, nil, err
		}
		wanted = min(wanted, len(c.Scratch))
		if wanted <= reserved {
			break
		}
		reserved = wanted
		blocked := make(map[ic10.Register]bool, len(pinned)+reserved)
		maps.Copy(blocked, pinned)
		for _, r := range c.Scratch[:reserved] {
			blocked[r] = true
		}
		assigned, spilled, err = scan(intervals, constrained, c.allocatable(blocked), c)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	return assigned, spilled, c.Scratch[:reserved], nil
}

// inMemory reports membership of the spilled intervals by virtual register.
func inMemory(spilled []*interval) func(mir.VirtReg) bool {
	set := make(map[mir.VirtReg]bool, len(spilled))
	for _, iv := range spilled {
		set[iv.vreg] = true
	}
	return func(v mir.VirtReg) bool { return set[v] }
}

// allocatable orders the registers a virtual register may be given by the
// bytes their name costs to render, so r0 through r9 and the two-character sp
// and ra are handed out before r10 through r15. blocked adds to
// Config.Reserved for this call.
func (c Config) allocatable(blocked map[ic10.Register]bool) []ic10.Register {
	unavailable := make(map[ic10.Register]bool, len(c.Reserved)+len(blocked))
	for _, r := range c.Reserved {
		unavailable[r] = true
	}
	for r := range blocked {
		unavailable[r] = true
	}
	regs := make([]ic10.Register, 0, ic10.NumRegisters)
	for r := range ic10.Register(ic10.NumRegisters) {
		if !unavailable[r] {
			regs = append(regs, r)
		}
	}
	slices.SortFunc(regs, func(a, b ic10.Register) int {
		if n := len(a.String()) - len(b.String()); n != 0 {
			return n
		}
		return int(a) - int(b)
	})
	return regs
}

// preassigned collects every physical register the input already names. Their
// live ranges are unknown here, so they are withheld from allocation for the
// whole function.
func preassigned(fn *mir.Func) map[ic10.Register]bool {
	used := make(map[ic10.Register]bool)
	for _, instr := range fn.AllInstrs() {
		for _, arg := range instr.Args {
			if reg, physical := arg.(mir.PhysReg); physical {
				used[reg.Reg] = true
			}
		}
	}
	return used
}
