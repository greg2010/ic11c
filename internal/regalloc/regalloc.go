// Package regalloc rewrites a mir.Func from virtual registers to physical ones
// by linear scan over live intervals, spilling to the data region.
//
// The cost model is instructions. A program may hold 128 lines and 4096 bytes,
// so a line would have to average 32 bytes for the byte cap to be reached
// first, and a real instruction is far shorter than that: the line count is
// what binds. Every choice here counts emitted instructions
// first and their width second. That is why a spill is a poke and a get db
// rather than a push and a pop, and why the spill heuristic weighs an
// interval's touch count against the span it would free. Preferring low
// numbered registers over high ones is the one width-directed choice, and it
// is free when the instruction count is equal.
//
// sp and ra are ordinary registers with no hardware protection. Whether they
// are allocatable depends on whether the calling convention is in use, which is
// a decision made elsewhere, so the reservation set arrives in Config rather
// than being fixed here.
package regalloc

import (
	"fmt"
	"maps"
	"slices"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/mir"
)

// Config parameterises allocation. The zero value reserves nothing and holds
// back no scratch register, which is usable only for a function that fits in
// the register file.
type Config struct {
	// Reserved names registers no virtual register may be given. sp and ra
	// belong here when the calling convention is in use and nowhere when it is
	// not. Registers already named by a physical operand in the input are
	// reserved in addition to these.
	Reserved []ic10.Register
	// Scratch names registers to reload spilled operands into. Every entry must
	// be within r0 through r15: a reload can land in a device reference
	// position, and only those registers may hold one.
	//
	// They are held back from allocation only for a function that spills, since
	// a function whose values all fit in registers reloads nothing. Allocate
	// therefore hands out the whole file first and reserves these only if that
	// was not enough.
	//
	// One scratch register serves an instruction that reads at most one spilled
	// operand. Allocate reports the shortfall by instruction when more are
	// needed, rather than emitting code that silently reuses one. Nothing is
	// spent on address computation: a spill slot is a compile time constant, so
	// poke and get db take it as a literal.
	Scratch []ic10.Register
	// SpillSlotBase is the first data region slot spill slots may occupy. Slots
	// below it hold globals, arrays and address taken locals.
	SpillSlotBase int
}

// DefaultScratch is the scratch set a whole program is allocated against.
//
// Three, because three register-capable sources is the widest an instruction
// selection emits reads, and an instruction needs one scratch register per
// distinct spilled source it reads. select, clamp and lerp read three and write
// a result, lbns reads three and writes one, and sbn and sbs read three and
// write nothing at all. The store forms are the shape with no slack: a spilled
// destination costs no fourth register, because planInstr lets it take back one
// a source already borrowed, and an instruction with no destination is given
// none of that. Both shapes still fit in three.
//
// Fewer is reported as a shortfall by Allocate rather than worked around, which
// is what makes the number a size choice rather than a correctness one. It does
// not grow with how much a function spills: the requirement is per instruction.
//
// They are the highest numbered general registers, which is where allocation
// reaches last within a name width: r10 through r15 all render in three
// characters, so which three of those six are held back costs nothing.
//
// Each call returns a fresh slice, so a caller may extend or reorder it.
func DefaultScratch() []ic10.Register {
	// Spelled against the general register count rather than as literals, so
	// that a file of a different width moves the set with it and leaves both
	// the argument above and checkPinned's disjointness argument true.
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
	// spilled maps each of the rest to its absolute data region slot, already
	// offset by Config.SpillSlotBase. The two are disjoint: an interval either
	// keeps a register for its whole lifetime or lives in memory, since this
	// allocator does not split.
	//
	// They are the rewriter's working state rather than an answer for a caller,
	// which is why they leave the package only as the code that was emitted.
	assigned map[mir.VirtReg]ic10.Register
	spilled  map[mir.VirtReg]int
}

// Allocate rewrites fn in place, replacing every virtual register operand with
// a physical one and inserting spill code where the register file did not
// suffice.
//
// It accepts a function in mir.RegFormVirtual or mir.RegFormMixed form and
// leaves it in mir.RegFormPhysical or mir.RegFormEmpty form. A physical
// register already named in the input is reserved for its original purpose over
// the whole function, since the input carries no live range for it.
//
// fn is unchanged when an error is returned: the rewrite is planned in full and
// applied only once every instruction is known to be expressible.
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

	assigned, spilled, err := cfg.allocateRegisters(intervals, constrained, pinned)
	if err != nil {
		return Result{}, fmt.Errorf("regalloc: %s: %w", fn.Name, err)
	}
	slots, count, err := assignSlots(spilled, cfg.SpillSlotBase)
	if err != nil {
		return Result{}, fmt.Errorf("regalloc: %s: %w", fn.Name, err)
	}

	res := Result{SpillSlots: count, assigned: assigned, spilled: slots}
	plan, err := planRewrite(fn, res, cfg.Scratch, callSaves(fn, intervals, assigned))
	if err != nil {
		return Result{}, fmt.Errorf("regalloc: %s: %w", fn.Name, err)
	}
	plan.apply()
	return res, nil
}

// SetStackBase prepends to the entry point the instruction that puts sp above
// the data region.
//
// The data region and the call frames share one 512 slot array with nothing
// between them: a frame that reaches a slot a global occupies overwrites it and
// nothing traps, and a poke into a frame corrupts a return address the same
// way. Keeping the two apart is this package's job, and base is the far side of
// the spill slots it just handed out.
//
// base is the first slot a frame may take, so it has to leave at least one.
// push writes at sp and advances afterwards, which makes a base of 511 a stack
// of exactly one value and a base of 512 a stack with nowhere to put the first.
// How much headroom is enough beyond that is not decidable here: frame depth is
// data-dependent for a recursive program, and the size report states what is
// left rather than pretending a number is safe.
//
// Chip state survives power loss, chip removal, and reflashing, so sp holds
// whatever the last program to run left in it. Nothing may push before this.
func SetStackBase(entry *mir.Func, base int) error {
	if entry == nil || len(entry.Blocks) == 0 {
		return fmt.Errorf("regalloc: the entry function has no block to set sp in")
	}
	if base < 0 || base >= ic10.NumMemorySlots {
		return fmt.Errorf("regalloc: a stack base of %d leaves no slot of the %d for a call frame", base, ic10.NumMemorySlots)
	}
	set, err := mir.NewInstr(ic10.OpMove, entry.Pos, mir.PhysReg{Reg: ic10.RegSP}, mir.Imm{Value: float64(base)})
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
	if c.SpillSlotBase < 0 || c.SpillSlotBase >= ic10.NumMemorySlots {
		return fmt.Errorf("spill slot base %d is outside the %d slot data region", c.SpillSlotBase, ic10.NumMemorySlots)
	}
	return nil
}

// checkPinned refuses a scratch register the input already names.
//
// A physical register in the input carries no live range here, which is why
// preassigned withholds it for the whole function. Reloading a spilled operand
// into one would write over a value nothing in this package can see is live,
// and the write is silent: the reload assembles, runs, and leaves a different
// number where a call argument or a result was.
//
// Config.validate cannot answer this. Which registers the input pins is a
// property of the function, so the two sets only meet once one is in hand. The
// caller is what has to keep them apart, and today does: the convention in
// internal/isel passes arguments in r0 upward and returns in r0, and
// DefaultScratch takes the top of the file.
func (c Config) checkPinned(pinned map[ic10.Register]bool) error {
	for _, r := range c.Scratch {
		if pinned[r] {
			return fmt.Errorf("scratch register %s is already named by an instruction in this function, and a reload into it would overwrite a value with no live range here; hold scratch registers apart from the ones the calling convention passes arguments and results in", r)
		}
	}
	return nil
}

// allocateRegisters places every interval, holding the scratch set back only if
// the whole register file was not enough.
//
// Scratch is spent on reloading a spilled operand, so a function that spills
// nothing has no use for it, and reserving it up front would start spilling
// len(Scratch) values early.
//
// Two passes are all it takes. The second cannot in turn need more scratch than
// it reserved, because the requirement is per instruction — one register per
// distinct spilled source that instruction reads — and does not grow with how
// much the function spilled. An instruction reading more spilled sources than
// there are scratch registers is reported by planInstr, which is the same answer
// a single pass would have given.
func (c Config) allocateRegisters(intervals []*interval, constrained map[mir.VirtReg]bool, pinned map[ic10.Register]bool) (map[mir.VirtReg]ic10.Register, []*interval, error) {
	assigned, spilled, err := scan(intervals, constrained, c.allocatable(pinned), c)
	if err != nil || len(spilled) == 0 {
		return assigned, spilled, err
	}
	held := make(map[ic10.Register]bool, len(pinned)+len(c.Scratch))
	maps.Copy(held, pinned)
	for _, r := range c.Scratch {
		held[r] = true
	}
	return scan(intervals, constrained, c.allocatable(held), c)
}

// allocatable orders the registers a virtual register may be given by the bytes
// they cost to render, so r0 through r9 and the two character sp and ra are
// handed out before r10 through r15. On a 4096 byte budget a register name
// appearing on a hundred lines is a hundred bytes.
//
// blocked names what allocation may not reach beyond Config.Reserved: the
// registers the input already pins, and the scratch set once a first pass has
// shown the function spills.
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
