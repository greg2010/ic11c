package mir

import (
	"slices"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/isa"
	"github.com/greg2010/ic11c/internal/source"
)

// branchTargets are the opcodes whose Label operand names a block control can
// reach from the one holding the instruction. The link forms (jal, b...al) are
// absent since control returns to the next line, and the relative forms (jr,
// br...) are absent since each encodes a line offset that [NewInstr] refuses
// as unemittable.
var branchTargets = map[ic10.Opcode]bool{
	isa.OpJ:     true,
	isa.OpBltz:  true,
	isa.OpBgez:  true,
	isa.OpBlez:  true,
	isa.OpBgtz:  true,
	isa.OpBdse:  true,
	isa.OpBdns:  true,
	isa.OpBeq:   true,
	isa.OpBne:   true,
	isa.OpBap:   true,
	isa.OpBna:   true,
	isa.OpBlt:   true,
	isa.OpBgt:   true,
	isa.OpBle:   true,
	isa.OpBge:   true,
	isa.OpBeqz:  true,
	isa.OpBnez:  true,
	isa.OpBapz:  true,
	isa.OpBnaz:  true,
	isa.OpBnan:  true,
	isa.OpBdnvl: true,
	isa.OpBdnvs: true,
}

// blockExits reports the labels a block branches to, in the order the
// branches appear, and whether control can leave its last instruction into
// whatever is laid out after the block.
//
// j decides both: it names its target where it carries a Label, and leaves
// the function where it carries anything else, since j ra is the return
// sequence. The scan stops at the first j, since control never reaches
// anything laid out after one.
func blockExits(block *Block) (targets []string, falls bool) {
	for _, instr := range block.Instrs {
		if instr == nil || !branchTargets[instr.Op] {
			continue
		}
		if label, named := labelOperand(instr); named {
			targets = append(targets, label)
		}
		if instr.Op == isa.OpJ {
			return targets, false
		}
	}
	return targets, true
}

// labelOperand reads the block a branch names, which is a [Label] and nothing
// else. A branch carrying a line number in that position names no block here
// and reads to [blockExits] the way j ra does: nothing before emission can
// resolve a line number to a function-local name.
func labelOperand(instr *Instr) (string, bool) {
	for _, arg := range instr.Args {
		if label, ok := arg.(Label); ok {
			return label.Name, true
		}
	}
	return "", false
}

// fallthroughChain is where control leaving fn.Blocks[index] without a jump
// can arrive: the blocks laid out after it up to and including the first that
// holds an instruction. A block with no instructions occupies no line, so its
// label and the block past it are the same edge — which is why this is a
// chain rather than one block. It is nil where every block after index is
// empty.
func fallthroughChain(fn *Func, index int) []*Block {
	for i, block := range fn.Blocks[index+1:] {
		if block != nil && len(block.Instrs) > 0 {
			return fn.Blocks[index+1 : index+2+i]
		}
	}
	return nil
}

// DropFallthroughJumps removes each block's trailing unconditional jump where
// control reaches its target without one, saving one line against the 128
// line budget and one execution slot against the 128 instruction tick. Blocks
// are walked last to first, so a jump dropped from one block lets the block
// before it be considered against the same target. [Block.Succs] is left
// alone: the edge the jump named is the edge the fallthrough now takes.
func DropFallthroughJumps(fn *Func) {
	for i := len(fn.Blocks) - 1; i >= 0; i-- {
		block := fn.Blocks[i]
		if len(block.Instrs) == 0 {
			continue
		}
		last := block.Instrs[len(block.Instrs)-1]
		if last.Op != isa.OpJ || len(last.Args) != 1 {
			continue
		}
		label, isLabel := last.Args[0].(Label)
		if !isLabel || !FallsThroughTo(fn, i, label.Name) {
			continue
		}
		clear(block.Instrs[len(block.Instrs)-1:])
		block.Instrs = block.Instrs[:len(block.Instrs)-1]
	}
}

// FallsThroughTo reports whether control leaving fn.Blocks[index] without a
// jump arrives at label anyway. The target need not be the block laid out
// next, only the next one holding an instruction: an empty block occupies no
// line, so its label resolves to the line the fallthrough reaches.
func FallsThroughTo(fn *Func, index int, label string) bool {
	for _, block := range fn.Blocks[index+1:] {
		if block.Label == label {
			return true
		}
		if len(block.Instrs) > 0 {
			return false
		}
	}
	return false
}

// validateSuccs holds a block's Succs against the control flow its
// instructions and layout position describe. Only omissions are reported:
// liveness is the sole reader of Succs, so a missing edge understates what is
// live and lets the allocator hand the register away early, while an extra
// edge only wastes one. checkEdges is false once every register is physical,
// since liveness runs once, over the virtual form.
func validateSuccs(diags *source.DiagnosticList, fn *Func, index int, byLabel map[string]*Block, owned map[*Block]bool, checkEdges bool) {
	block := fn.Blocks[index]

	have := make(map[*Block]bool, len(block.Succs))
	for _, succ := range block.Succs {
		switch {
		case succ == nil:
			diags.Addf(block.Pos, "block %s has a nil successor", block.Label)
		case !owned[succ]:
			diags.Addf(block.Pos, "block %s has successor %s, which is not a block of function %s", block.Label, succ.Label, fn.Name)
		default:
			have[succ] = true
		}
	}

	if !checkEdges {
		return
	}

	targets, falls := blockExits(block)
	for _, name := range targets {
		target := byLabel[name]
		if target == nil || have[target] {
			continue
		}
		diags.Addf(block.Pos, "block %s branches to %s and does not list it as a successor: %s", block.Label, target.Label, succsAreTheGraph)
	}
	if !falls {
		return
	}
	chain := fallthroughChain(fn, index)
	if chain == nil || slices.ContainsFunc(chain, func(b *Block) bool { return have[b] }) {
		return
	}
	diags.Addf(block.Pos, "block %s falls into %s and does not list it as a successor: %s", block.Label, chain[len(chain)-1].Label, succsAreTheGraph)
}

const succsAreTheGraph = "Succs is the whole of what liveness reads, so a missing edge ends a live range early and the allocator can reuse a register the successor still holds"
