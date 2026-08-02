package regalloc

import (
	"fmt"
	"slices"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/mir"
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
				return nil, fmt.Errorf("block %s: %s: %w", block.Label, instr, err)
			}
			pushes, pops, err := p.planCallSaves(instr, saves[instr])
			if err != nil {
				return nil, fmt.Errorf("block %s: %s: %w", block.Label, instr, err)
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

// around builds one instruction of the spill or call-frame code that surrounds
// at, carrying at's source position and the calls it was reached through.
//
// The inline chain is what the size report attributes bytes by. Code inserted
// around an instruction of an inlined body belongs to the call that spliced the
// body in; without the chain it is charged to the enclosing function instead,
// which is the one unit the report exists to avoid.
func around(at *mir.Instr, op ic10.Opcode, args ...mir.Operand) (*mir.Instr, error) {
	instr, err := mir.NewInstr(op, at.Pos, args...)
	if err != nil {
		return nil, err
	}
	instr.Inline = at.Inline
	return instr, nil
}

// callSaves lists, per call, the registers holding a value the call must not
// destroy.
//
// Every register is caller saved. The callee's allocation is decided
// separately and no clobber set crosses between the two, so a value the caller
// still wants after the call goes to the frame around it. push and pop are one
// instruction each and neither needs an address operand, which is what makes
// this cheaper than the poke and get db pair an ordinary spill costs.
//
// The set comes from the live intervals rather than from the assignment, so a
// value the call is the last reader of costs nothing: it is dead at the point
// the call returns to.
func callSaves(fn *mir.Func, intervals []*interval, assigned map[mir.VirtReg]ic10.Register) map[*mir.Instr][]ic10.Register {
	saves := make(map[*mir.Instr][]ic10.Register)
	index := 0
	for _, instr := range fn.AllInstrs() {
		point := index
		index++
		if instr.Op != ic10.OpJal {
			continue
		}
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
		saves[instr] = slices.Compact(regs)
	}
	return saves
}

// planCallSaves builds the frame one call needs around it.
//
// The pops mirror the pushes, because pop decrements sp before its bounds
// check and does not roll the decrement back: an unbalanced pair walks sp
// downward into the data region one slot per tick with nothing trapping.
//
// The argument registers a call writes are not in the set: allocation withholds
// every physical register the input names for the whole function, so no value
// of the caller is living in one.
func (p *plan) planCallSaves(instr *mir.Instr, regs []ic10.Register) (pushes, pops []*mir.Instr, err error) {
	for _, reg := range regs {
		save, err := around(instr, ic10.OpPush, mir.PhysReg{Reg: reg})
		if err != nil {
			return nil, nil, fmt.Errorf("saving %s across the call: %w", reg, err)
		}
		pushes = append(pushes, save)
	}
	for i := len(regs) - 1; i >= 0; i-- {
		restore, err := around(instr, ic10.OpPop, mir.PhysReg{Reg: regs[i]})
		if err != nil {
			return nil, nil, fmt.Errorf("restoring %s across the call: %w", regs[i], err)
		}
		pops = append(pops, restore)
	}
	return pushes, pops, nil
}

// planInstr records the operand replacements one instruction needs and returns
// the spill code that must surround it.
//
// Sources are handled before the destination so that a spilled destination can
// take a scratch register a source has already borrowed: the machine reads
// every operand before it writes the result, so the reload the scratch holds is
// dead by the time the result lands in it.
func (p *plan) planInstr(instr *mir.Instr, res Result, scratch []ic10.Register) (reloads, stores []*mir.Instr, err error) {
	def, err := defIndex(instr)
	if err != nil {
		return nil, nil, err
	}

	borrowed := make(map[mir.VirtReg]ic10.Register)
	next := 0
	for j, arg := range instr.Args {
		v, ok := arg.(mir.VirtReg)
		if !ok || j == def {
			continue
		}
		if reg, ok := res.assigned[v]; ok {
			p.writes = append(p.writes, argWrite{instr: instr, index: j, operand: mir.PhysReg{Reg: reg}})
			continue
		}
		slot, ok := res.spilled[v]
		if !ok {
			return nil, nil, fmt.Errorf("%s was neither assigned a register nor a spill slot", v)
		}
		reg, held := borrowed[v]
		if !held {
			if next >= len(scratch) {
				return nil, nil, fmt.Errorf("reads %d distinct spilled operands; only %d scratch registers are configured", next+1, len(scratch))
			}
			reg = scratch[next]
			next++
			borrowed[v] = reg
			reload, err := around(instr, ic10.OpGet, mir.PhysReg{Reg: reg}, mir.NewDeviceBase(), mir.Imm{Value: float64(slot)})
			if err != nil {
				return nil, nil, fmt.Errorf("reloading %s from slot %d: %w", v, slot, err)
			}
			reloads = append(reloads, reload)
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
		if len(scratch) == 0 {
			return nil, nil, fmt.Errorf("writes spilled %s but no scratch register is configured", v)
		}
		reg = scratch[0]
	}
	p.writes = append(p.writes, argWrite{instr: instr, index: def, operand: mir.PhysReg{Reg: reg}})
	store, err := around(instr, ic10.OpPoke, mir.Imm{Value: float64(slot)}, mir.PhysReg{Reg: reg})
	if err != nil {
		return nil, nil, fmt.Errorf("spilling %s to slot %d: %w", v, slot, err)
	}
	return reloads, []*mir.Instr{store}, nil
}
