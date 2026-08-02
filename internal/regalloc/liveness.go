package regalloc

import (
	"fmt"
	"slices"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/mir"
)

// numbering assigns every instruction an index in layout order and records the
// half open index range each block occupies.
type numbering struct {
	first []int // per block, the index of its first instruction
	last  []int // per block, one past the index of its last
}

func number(fn *mir.Func) (numbering, error) {
	n := numbering{first: make([]int, len(fn.Blocks)), last: make([]int, len(fn.Blocks))}
	next := 0
	for i, block := range fn.Blocks {
		if block == nil {
			return numbering{}, fmt.Errorf("block %d is nil", i)
		}
		n.first[i] = next
		next += len(block.Instrs)
		n.last[i] = next
	}
	return n, nil
}

// defIndex reports the operand position an instruction writes, or -1 when it
// writes none.
//
// The target's operand table is the only description of direction available:
// mir carries none. A destination is spelled as a bare, unnamed r? in the
// game's own help text, and it is always the first operand. Every other
// register position is named — a, b, address, value, device — and is read. The
// store forms put an unnamed r? last, which is why the position matters and not
// just the shape.
//
// The two instructions whose bare r? sits elsewhere, alias and label, are
// assembler directives mir refuses to construct.
func defIndex(instr *mir.Instr) (int, error) {
	info, ok := instr.Op.Instruction()
	if !ok {
		return 0, fmt.Errorf("opcode %v is not in the instruction table", instr.Op)
	}
	if len(info.Operands) == 0 {
		return -1, nil
	}
	first := info.Operands[0]
	if first.Name != "" || len(first.Kinds) != 1 || first.Kinds[0] != ic10.OperandRegister {
		return -1, nil
	}
	return 0, nil
}

// deviceConstrained reports whether an operand position may hold a device
// reference.
//
// A register in one of these positions holds a ReferenceId, and that is the one
// range restriction the machine imposes on a register: it resolves within r0
// through r15 only, so a virtual register reaching such a position cannot be
// given sp or ra even when the calling convention leaves them free. Indirect
// referencing is not the reason — an rr form is bounded by the whole 18 entry
// register array and reaches sp and ra quite happily.
//
// No pattern in internal/isel puts a register in one of these positions yet:
// every device operand it builds is a d0 through d5 pin or db. The check is
// what keeps the first one that does from being handed a register the chip
// cannot resolve.
func deviceConstrained(instr *mir.Instr, index int) bool {
	info, ok := instr.Op.Instruction()
	if !ok || index >= len(info.Operands) {
		return false
	}
	for _, kind := range info.Operands[index].Kinds {
		if kind == ic10.OperandDevice || kind == ic10.OperandRefID {
			return true
		}
	}
	return false
}

// liveness is the classic backward set problem, iterated to a fixed point over
// the successor edges mir records.
//
// A fixed point rather than a single pass over a block ordering is what makes a
// back edge come out exact. A value defined in a loop body and read at the top
// of the next iteration is live out of the latch, so it needs no separate loop
// extension, and a value live in two blocks that layout order separates keeps
// the hole between them instead of being widened to cover it.
type liveness struct {
	in  []map[mir.VirtReg]bool
	out []map[mir.VirtReg]bool
}

func computeLiveness(fn *mir.Func) (liveness, error) {
	index := make(map[*mir.Block]int, len(fn.Blocks))
	for i, block := range fn.Blocks {
		index[block] = i
	}

	live := liveness{in: make([]map[mir.VirtReg]bool, len(fn.Blocks)), out: make([]map[mir.VirtReg]bool, len(fn.Blocks))}
	upward := make([]map[mir.VirtReg]bool, len(fn.Blocks))
	killed := make([]map[mir.VirtReg]bool, len(fn.Blocks))
	for i, block := range fn.Blocks {
		live.in[i] = make(map[mir.VirtReg]bool)
		live.out[i] = make(map[mir.VirtReg]bool)
		upward[i] = make(map[mir.VirtReg]bool)
		killed[i] = make(map[mir.VirtReg]bool)
		for _, instr := range block.Instrs {
			def, err := defIndex(instr)
			if err != nil {
				return liveness{}, fmt.Errorf("block %s: %w", block.Label, err)
			}
			for j, arg := range instr.Args {
				v, ok := arg.(mir.VirtReg)
				if !ok || j == def {
					continue
				}
				if !killed[i][v] {
					upward[i][v] = true
				}
			}
			if def >= 0 && def < len(instr.Args) {
				if v, ok := instr.Args[def].(mir.VirtReg); ok {
					killed[i][v] = true
				}
			}
		}
	}

	for changed := true; changed; {
		changed = false
		for i := len(fn.Blocks) - 1; i >= 0; i-- {
			for _, succ := range fn.Blocks[i].Succs {
				s, ok := index[succ]
				if !ok {
					return liveness{}, fmt.Errorf("block %s has successor %s outside the function", fn.Blocks[i].Label, succ.Label)
				}
				for v := range live.in[s] {
					if !live.out[i][v] {
						live.out[i][v] = true
						changed = true
					}
				}
			}
			for v := range live.out[i] {
				if killed[i][v] || live.in[i][v] {
					continue
				}
				live.in[i][v] = true
				changed = true
			}
			for v := range upward[i] {
				if !live.in[i][v] {
					live.in[i][v] = true
					changed = true
				}
			}
		}
	}
	return live, nil
}

// buildIntervals turns liveness into one interval per virtual register, and
// reports which of them reach a device reference position.
func buildIntervals(fn *mir.Func, nums numbering) ([]*interval, map[mir.VirtReg]bool, error) {
	live, err := computeLiveness(fn)
	if err != nil {
		return nil, nil, err
	}

	byVreg := make(map[mir.VirtReg]*interval)
	constrained := make(map[mir.VirtReg]bool)
	get := func(v mir.VirtReg) *interval {
		iv, ok := byVreg[v]
		if !ok {
			iv = &interval{vreg: v}
			byVreg[v] = iv
		}
		return iv
	}

	// Blocks and their instructions are walked backwards so that every range
	// addition lands at or before the start of the interval's first range,
	// which is what addRange assumes.
	for b := len(fn.Blocks) - 1; b >= 0; b-- {
		block := fn.Blocks[b]
		blockFrom, blockTo := usePoint(nums.first[b]), usePoint(nums.last[b])
		alive := make(map[mir.VirtReg]bool, len(live.out[b]))
		for v := range live.out[b] {
			alive[v] = true
			get(v).addRange(blockFrom, blockTo)
		}
		for i := len(block.Instrs) - 1; i >= 0; i-- {
			instr := block.Instrs[i]
			point := nums.first[b] + i
			def, err := defIndex(instr)
			if err != nil {
				return nil, nil, fmt.Errorf("block %s: %w", block.Label, err)
			}
			if def >= 0 && def < len(instr.Args) {
				if v, ok := instr.Args[def].(mir.VirtReg); ok {
					iv := get(v)
					iv.touches++
					// A definition nothing downstream reads gets a range of its
					// own rather than shortening the next one, so the dead
					// stretch between two definitions does not hold a register.
					if alive[v] {
						iv.setFrom(defPoint(point))
						delete(alive, v)
					} else {
						iv.addRange(defPoint(point), defPoint(point)+1)
					}
					if deviceConstrained(instr, def) {
						constrained[v] = true
					}
				}
			}
			for j, arg := range instr.Args {
				v, ok := arg.(mir.VirtReg)
				if !ok || j == def {
					continue
				}
				iv := get(v)
				iv.touches++
				iv.addRange(blockFrom, defPoint(point))
				alive[v] = true
				if deviceConstrained(instr, j) {
					constrained[v] = true
				}
			}
		}
	}

	intervals := make([]*interval, 0, len(byVreg))
	for _, iv := range byVreg {
		intervals = append(intervals, iv)
	}
	slices.SortFunc(intervals, byStart)
	return intervals, constrained, nil
}
