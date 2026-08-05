package isel

import (
	"slices"

	"github.com/greg2010/ic11c/internal/isa"
	"github.com/greg2010/ic11c/internal/llvmir"
	"github.com/greg2010/ic11c/internal/mir"
	"github.com/greg2010/ic11c/internal/source"
	"tinygo.org/x/go-llvm"
)

// planEdges records each block's successors and decides which edges need a
// block of their own. A phi's copies belong on the edge into its block;
// they can be appended to a predecessor only when that edge is its sole
// exit, otherwise the edge is split into a block of its own.
func (s *selector) planEdges() {
	for i, bb := range s.order {
		info := &blockInfo{
			llvmBlock: bb,
			index:     i,
			split:     make(map[llvm.BasicBlock]bool),
			edges:     make(map[llvm.BasicBlock]*mir.Block),
			copies:    make(map[llvm.BasicBlock][]*mir.Instr),
			pos:       s.blockPosition(bb),
		}
		term := bb.LastInstruction()
		if !term.IsNil() {
			for i := range term.SuccessorsCount() {
				if succ := term.Successor(i); !slices.Contains(info.succs, succ) {
					info.succs = append(info.succs, succ)
				}
			}
		}
		s.blocks[bb] = info
	}

	for _, bb := range s.order {
		phis := phiNodes(bb)
		if len(phis) == 0 {
			continue
		}
		for _, phi := range phis {
			for i := range phi.IncomingCount() {
				pred := s.blocks[phi.IncomingBlock(i)]
				if pred == nil {
					continue
				}
				if len(pred.succs) > 1 {
					pred.split[bb] = true
				}
			}
		}
	}
}

func phiNodes(bb llvm.BasicBlock) []llvm.Value {
	var phis []llvm.Value
	for in := range llvmir.BlockInstrs(bb) {
		if in.InstructionOpcode() != llvm.PHI {
			break
		}
		phis = append(phis, in)
	}
	return phis
}

// phiPosition locates a phi node for diagnostics. A phi the optimizer built
// (mem2reg, loop rotation) carries no debug location of its own, so this
// falls back to the first located instruction in the block it heads — where
// control from every incoming branch converges.
func (s *selector) phiPosition(phi llvm.Value) source.Position {
	if pos, located := s.pos.Instr(phi); located {
		return pos
	}
	return s.blockPosition(phi.InstructionParent())
}

// blockPosition is the statement a block stands for, which is the first
// instruction in it that still carries a location. A block the optimizer built
// out of nothing the source wrote has none, and falls back to the function the
// way every other diagnostic does.
func (s *selector) blockPosition(bb llvm.BasicBlock) source.Position {
	for in := range llvmir.BlockInstrs(bb) {
		if pos, located := s.pos.Instr(in); located {
			return pos
		}
	}
	return s.enclosingPos()
}

// lowerPhis turns each block's phis into parallel copies on its incoming
// edges, sequenced so no copy clobbers a value another still needs. A
// predecessor a phi names more than once (two switch arms reaching the same
// block) still contributes only one copy, since it is reached once at run time.
func (s *selector) lowerPhis() {
	for _, bb := range s.order {
		phis := phiNodes(bb)
		if len(phis) == 0 {
			continue
		}
		byPred := make(map[llvm.BasicBlock][]copyMove)
		for _, phi := range phis {
			dst, ok := s.vregs[phi]
			if !ok {
				s.errorf(s.position(phi), "the phi node was given no register")
				continue
			}
			pos, inline := s.phiPosition(phi), s.inlineChain(phi)
			seen := make(map[llvm.BasicBlock]bool, phi.IncomingCount())
			for i := range phi.IncomingCount() {
				pred := phi.IncomingBlock(i)
				if seen[pred] {
					continue
				}
				seen[pred] = true
				src, err := s.operand(phi.IncomingValue(i))
				if err != nil {
					s.errorf(pos, "this value reaches here from more than one branch, and what one of them supplies is not something the backend can name: %v", err)
					continue
				}
				byPred[pred] = append(byPred[pred], copyMove{dst: dst, src: src, pos: pos, inline: inline})
			}
		}
		// Layout order, not map order: breaking a copy cycle takes a fresh
		// virtual register, and map order would assign register numbers that
		// differ between runs, which register allocation uses to order
		// equal-length live intervals.
		for _, pred := range s.order {
			moves, wanted := byPred[pred]
			if !wanted {
				continue
			}
			info := s.blocks[pred]
			if info == nil {
				continue
			}
			info.copies[bb] = s.copyInstrs(s.sequence(moves))
		}
	}
}

// copyMove is one transfer a phi asks for on one edge. Every move on an edge
// happens at once, so the order they are emitted in has to be worked out rather
// than taken from the phi list.
type copyMove struct {
	dst mir.VirtReg
	src mir.Operand
	pos source.Position
	// inline is the call site chain pos was reached through, carried so the
	// copies a phi becomes are charged to the same inline site as the value.
	inline []source.InlineSite
}

func (s *selector) copyInstrs(moves []copyMove) []*mir.Instr {
	instrs := make([]*mir.Instr, 0, len(moves))
	for _, move := range moves {
		instr, err := unconverted(isa.OpMove, move.pos, move.dst, move.src)
		if err != nil {
			s.errorf(move.pos, "the copy a value reaching this statement from two branches becomes: %v", err)
			continue
		}
		instr.Inline = move.inline
		instrs = append(instrs, instr)
	}
	return instrs
}

// sequence orders simultaneous copies into a sequence with the same effect.
// Emitting them in phi order is wrong when one copy's destination is
// another's source — two phis swapping a pair of registers would both end up
// holding the same value — so a destination is written only once nothing
// still needs its old contents, and a cycle is broken by moving one member
// into a fresh register first. A copy from a literal reads nothing, so it
// cannot be in a cycle and is emitted last.
func (s *selector) sequence(moves []copyMove) []copyMove {
	var regs, imms []copyMove
	for _, move := range moves {
		if _, ok := move.src.(mir.VirtReg); ok {
			regs = append(regs, move)
			continue
		}
		imms = append(imms, move)
	}
	return append(s.sequenceRegisters(regs), imms...)
}

func (s *selector) sequenceRegisters(moves []copyMove) []copyMove {
	// loc tracks, for each value, the register currently holding it. pred maps
	// each destination to the value it is waiting for.
	loc := make(map[mir.VirtReg]mir.VirtReg, len(moves))
	pred := make(map[mir.VirtReg]mir.VirtReg, len(moves))
	pos := make(map[mir.VirtReg]source.Position, len(moves))
	inline := make(map[mir.VirtReg][]source.InlineSite, len(moves))
	for _, move := range moves {
		src, ok := move.src.(mir.VirtReg)
		if !ok {
			continue
		}
		loc[src] = src
		pred[move.dst] = src
		pos[move.dst] = move.pos
		inline[move.dst] = move.inline
	}

	todo := make([]mir.VirtReg, 0, len(moves))
	var ready []mir.VirtReg
	for _, move := range moves {
		todo = append(todo, move.dst)
		if _, needed := loc[move.dst]; !needed {
			ready = append(ready, move.dst)
		}
	}

	out := make([]copyMove, 0, len(moves)+1)
	emit := func(dst, src mir.VirtReg) {
		out = append(out, copyMove{dst: dst, src: src, pos: pos[dst], inline: inline[dst]})
	}

	for len(todo) > 0 {
		for len(ready) > 0 {
			b := ready[len(ready)-1]
			ready = ready[:len(ready)-1]
			a := pred[b]
			c := loc[a]
			emit(b, c)
			loc[a] = b
			if a == c {
				if _, waiting := pred[a]; waiting {
					ready = append(ready, a)
				}
			}
		}
		b := todo[len(todo)-1]
		todo = todo[:len(todo)-1]
		if loc[pred[b]] == b {
			// Already written by the loop above.
			continue
		}
		temp := s.fn.NewVirtReg()
		out = append(out, copyMove{dst: temp, src: b, pos: pos[b], inline: inline[b]})
		loc[b] = temp
		pos[temp] = pos[b]
		inline[temp] = inline[b]
		ready = append(ready, b)
	}
	return out
}
