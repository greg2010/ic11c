package regalloc

import (
	"fmt"
	"slices"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/isa"
	"github.com/greg2010/ic11c/internal/mir"
	"github.com/greg2010/ic11c/internal/source"
)

// argWrite is a deferred operand replacement. Rewriting is planned before any
// of it is applied so that a function is never left half virtual and half
// physical, a form well suited to neither allocation nor emission.
type argWrite struct {
	instr   *mir.Instr
	index   int
	operand mir.Operand
}

type plan struct {
	fn     *mir.Func
	blocks [][]*mir.Instr
	writes []argWrite
}

func (p *plan) apply() {
	for _, w := range p.writes {
		w.instr.Args[w.index] = w.operand
	}
	for i, instrs := range p.blocks {
		p.fn.Blocks[i].Instrs = instrs
	}
}

func planRewrite(fn *mir.Func, res Result, scratch []ic10.Register, saves map[*mir.Instr][]ic10.Register) (*plan, error) {
	p := &plan{fn: fn, blocks: make([][]*mir.Instr, len(fn.Blocks))}
	for b, block := range fn.Blocks {
		instrs := make([]*mir.Instr, 0, len(block.Instrs))
		for _, instr := range block.Instrs {
			reloads, stores, err := p.planInstr(instr, res, scratch)
			if err != nil {
				return nil, locate(block, instr, err)
			}
			pushes, pops, err := p.planCallSaves(instr, saves[instr])
			if err != nil {
				return nil, locate(block, instr, err)
			}
			instrs = append(instrs, pushes...)
			instrs = append(instrs, reloads...)
			instrs = append(instrs, instr)
			instrs = append(instrs, stores...)
			instrs = append(instrs, pops...)
		}
		p.blocks[b] = instrs
	}
	return p, nil
}

// locate names the block and the instruction a planning failure came from. A
// diagnostic passes through untouched: it already carries the line the
// programmer acts on, and wrapping it would bury that behind a block label the
// source doesn't have.
func locate(block *mir.Block, instr *mir.Instr, err error) error {
	if _, ok := source.DiagnosticsIn(err); ok {
		return err
	}
	return fmt.Errorf("block %s: %s: %w", block.Label, instr, err)
}

// shortfall reports an instruction the scratch set cannot serve, as a
// diagnostic against the line rather than an internal error, the same way
// [outOfSlots] is: an expression that wants more scratch registers than are
// held back is the source's problem, not the compiler's.
func shortfall(instr *mir.Instr, format string, args ...any) error {
	var diags source.DiagnosticList
	diags.Addf(instr.Pos, format, args...)
	return diags.Err()
}

// around builds one instruction of the spill or call-frame code that surrounds
// at, carrying at's source position and inline chain — the size report
// attributes bytes by that chain, so spill code inside an inlined body must be
// charged to the call that spliced the body in, not the enclosing function.
func around(at *mir.Instr, op ic10.Opcode, args ...mir.Operand) (*mir.Instr, error) {
	instr, err := mir.NewInstr(op, at.Pos, args...)
	if err != nil {
		return nil, err
	}
	instr.Inline = at.Inline
	return instr, nil
}

// callSaves lists, per call, the registers holding a value the call must not
// destroy. Every register is caller saved, so a value the caller still wants
// afterward goes to the frame push/pop builds around the call rather than to
// an ordinary spill. The set is read from the live intervals, not the
// assignment, so a value the call is the last reader of costs nothing.
func callSaves(fn *mir.Func, nums numbering, intervals []*interval, assigned map[mir.VirtReg]ic10.Register) map[*mir.Instr][]ic10.Register {
	saves := make(map[*mir.Instr][]ic10.Register)
	for b, block := range fn.Blocks {
		for i, instr := range block.Instrs {
			if !ic10.LinksReturn(instr.Op) {
				continue
			}
			point := nums.first[b] + i
			var regs []ic10.Register
			for _, iv := range intervals {
				reg, held := assigned[iv.vreg]
				if !held || !iv.covers(defPoint(point)) {
					continue
				}
				regs = append(regs, reg)
			}
			if len(regs) == 0 {
				continue
			}
			slices.Sort(regs)
			saves[instr] = regs
		}
	}
	return saves
}

// planCallSaves builds the frame one call needs around it. The pops must
// mirror the pushes exactly: pop lowers sp and bounds-checks after, so an
// unmatched pop silently restores a register from a slot holding a global and
// only faults with StackUnderFlow once sp walks below slot zero — long after
// the damage.
func (p *plan) planCallSaves(instr *mir.Instr, regs []ic10.Register) (pushes, pops []*mir.Instr, err error) {
	for _, reg := range regs {
		save, err := around(instr, isa.OpPush, mir.PhysReg{Reg: reg})
		if err != nil {
			return nil, nil, fmt.Errorf("saving %s across the call: %w", reg, err)
		}
		pushes = append(pushes, save)
	}
	for i := len(regs) - 1; i >= 0; i-- {
		restore, err := around(instr, isa.OpPop, mir.PhysReg{Reg: regs[i]})
		if err != nil {
			return nil, nil, fmt.Errorf("restoring %s across the call: %w", regs[i], err)
		}
		pops = append(pops, restore)
	}
	return pushes, pops, nil
}

// spilledReads lists the distinct spilled virtual registers instr reads, in
// the order its operands name them. Its length is how many scratch registers
// the instruction needs, since every reload must survive until all of them are
// read. The same walk backs both [scratchDemand] and [planInstr], so the
// scratch held back cannot disagree with what gets spent.
func spilledReads(instr *mir.Instr, def int, spilled func(mir.VirtReg) bool) []mir.VirtReg {
	var reads []mir.VirtReg
	for j, arg := range instr.Args {
		v, ok := arg.(mir.VirtReg)
		if !ok || j == def || !spilled(v) || slices.Contains(reads, v) {
			continue
		}
		reads = append(reads, v)
	}
	return reads
}

// writesSpilled reports whether instr's result goes to memory, which needs one
// register to stage the write through.
func writesSpilled(instr *mir.Instr, def int, spilled func(mir.VirtReg) bool) bool {
	if def < 0 || def >= len(instr.Args) {
		return false
	}
	v, isVirtual := instr.Args[def].(mir.VirtReg)
	return isVirtual && spilled(v)
}

// scratchDemand is the most scratch registers any single instruction of fn
// will need once the given values are in memory. The requirement is per
// instruction, not per function, which is what lets allocation hold back what
// one line wants rather than the whole configured set.
func scratchDemand(fn *mir.Func, spilled func(mir.VirtReg) bool) (int, error) {
	most := 0
	for block, instr := range fn.AllInstrs() {
		def, err := defIndex(instr)
		if err != nil {
			return 0, fmt.Errorf("block %s: %w", block.Label, err)
		}
		need := len(spilledReads(instr, def, spilled))
		if writesSpilled(instr, def, spilled) {
			need = max(need, 1)
		}
		most = max(most, need)
	}
	return most, nil
}

// planInstr records the operand replacements one instruction needs and returns
// the spill code that must surround it. Sources are handled before the
// destination so a spilled destination can reuse a scratch register a source
// already borrowed: the machine reads every operand before it writes the
// result, so the reload is dead by the time the result lands.
func (p *plan) planInstr(instr *mir.Instr, res Result, scratch []ic10.Register) (reloads, stores []*mir.Instr, err error) {
	def, err := defIndex(instr)
	if err != nil {
		return nil, nil, err
	}

	spilled := func(v mir.VirtReg) bool { _, inMemory := res.spilled[v]; return inMemory }
	reads := spilledReads(instr, def, spilled)
	if len(reads) > len(scratch) {
		return nil, nil, shortfall(instr, "this line reads %s and the configuration holds back %s to reload them into, so one reload would overwrite another before the instruction read it; an instruction needs one scratch register per distinct spilled operand it reads",
			source.Plural(len(reads), "distinct spilled operand"), source.Plural(len(scratch), "scratch register"))
	}
	borrowed := make(map[mir.VirtReg]ic10.Register, len(reads))
	for i, v := range reads {
		reg := scratch[i]
		borrowed[v] = reg
		reload, err := around(instr, isa.OpGet, mir.PhysReg{Reg: reg}, mir.NewDeviceBase(), mir.Imm{Value: float64(res.spilled[v])})
		if err != nil {
			return nil, nil, fmt.Errorf("reloading %s from slot %d: %w", v, res.spilled[v], err)
		}
		reloads = append(reloads, reload)
	}

	for j, arg := range instr.Args {
		v, ok := arg.(mir.VirtReg)
		if !ok || j == def {
			continue
		}
		reg, placed := res.assigned[v]
		if !placed {
			if reg, placed = borrowed[v]; !placed {
				return nil, nil, fmt.Errorf("%s was neither assigned a register nor a spill slot", v)
			}
		}
		p.writes = append(p.writes, argWrite{instr: instr, index: j, operand: mir.PhysReg{Reg: reg}})
	}

	if def < 0 || def >= len(instr.Args) {
		return reloads, nil, nil
	}
	v, ok := instr.Args[def].(mir.VirtReg)
	if !ok {
		return reloads, nil, nil
	}
	if reg, ok := res.assigned[v]; ok {
		p.writes = append(p.writes, argWrite{instr: instr, index: def, operand: mir.PhysReg{Reg: reg}})
		return reloads, nil, nil
	}
	slot, ok := res.spilled[v]
	if !ok {
		return nil, nil, fmt.Errorf("%s was neither assigned a register nor a spill slot", v)
	}
	reg, held := borrowed[v]
	if !held {
		// scratch holds at least one register here: writing a spilled value
		// makes scratchDemand answer one or more, allocation reserves that many,
		// and scan refuses to spill anything at all without a register to stage
		// it through.
		reg = scratch[0]
	}
	p.writes = append(p.writes, argWrite{instr: instr, index: def, operand: mir.PhysReg{Reg: reg}})
	store, err := around(instr, isa.OpPoke, mir.Imm{Value: float64(slot)}, mir.PhysReg{Reg: reg})
	if err != nil {
		return nil, nil, fmt.Errorf("spilling %s to slot %d: %w", v, slot, err)
	}
	return reloads, []*mir.Instr{store}, nil
}
