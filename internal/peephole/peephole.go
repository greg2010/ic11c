// Package peephole rewrites an allocated program where a pair of
// adjacent instructions computes what one instruction computes. It
// runs after register allocation, the first point operands are known
// to be the same physical storage, and before emission, since line numbers matter.
package peephole

import (
	"slices"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/isa"
	"github.com/greg2010/ic11c/internal/mir"
)

// complements pairs each set instruction that answers exactly 0 or 1
// with the instruction answering its exact complement; docs/target.md
// records which pairs qualify. Ordered and approximate comparisons are
// excluded: both answer 0 for a NaN operand, so neither negates the other.
var complements = map[ic10.Opcode]ic10.Opcode{
	isa.OpSeq:   isa.OpSne,
	isa.OpSne:   isa.OpSeq,
	isa.OpSeqz:  isa.OpSnez,
	isa.OpSnez:  isa.OpSeqz,
	isa.OpSdse:  isa.OpSdns,
	isa.OpSdns:  isa.OpSdse,
	isa.OpSnan:  isa.OpSnanz,
	isa.OpSnanz: isa.OpSnan,
}

// Run rewrites prog in place: identity moves and re-test folds first,
// per block, then the two control-flow rewrites, per function, since
// those read block layout the earlier rewrites change. prog must hold
// no nil function, block, or instruction, which [mir.Program.Validate] reports.
func Run(prog *mir.Program) {
	if prog == nil {
		return
	}
	for _, fn := range prog.Funcs {
		for _, block := range fn.Blocks {
			block.Instrs = slices.DeleteFunc(block.Instrs, isIdentityMove)
			block.Instrs = foldRetests(block.Instrs)
		}
		mir.DropFallthroughJumps(fn)
		invertBranchesOverFallthrough(fn)
	}
}

// isIdentityMove reports whether an instruction copies a register onto
// itself. It asks about physical registers only: two virtual registers
// naming the same value are the normal output of phi lowering and are
// not the same storage until allocation says so.
func isIdentityMove(instr *mir.Instr) bool {
	if instr.Op != isa.OpMove {
		return false
	}
	dst, src, ok := writtenAndRead(instr)
	return ok && src.Reg == dst.Reg
}

// retests are the instructions that ask a value already 0 or 1 for its
// truth again, mapped to what asking a second time is worth: `seqz`
// answers the complement, so the definition becomes the complement and
// the test goes; `snez` answers the value itself, so only the test goes.
var retests = map[ic10.Opcode]bool{
	isa.OpSeqz: true,
	isa.OpSnez: false,
}

// uncomplementedSets are the set instructions that answer a truth
// value and have no complement on the machine (see [complements]). The
// `snez` retest inverts nothing, so these belong to the half of the
// fold needing a truth value, not a negation.
var uncomplementedSets = map[ic10.Opcode]bool{
	isa.OpSlt:  true,
	isa.OpSltz: true,
	isa.OpSle:  true,
	isa.OpSlez: true,
	isa.OpSgt:  true,
	isa.OpSgtz: true,
	isa.OpSge:  true,
	isa.OpSgez: true,
	isa.OpSap:  true,
	isa.OpSna:  true,
	isa.OpSapz: true,
	isa.OpSnaz: true,
}

// foldRetests removes the second of a pair of instructions where a
// set instruction's result is immediately re-tested in place, so
// `snan r1 r0; seqz r1 r1` becomes `snanz r1 r0`. Adjacency within one
// block makes this local: a branch target resolves to the start of a block.
func foldRetests(instrs []*mir.Instr) []*mir.Instr {
	kept := instrs[:0]
	for _, instr := range instrs {
		if len(kept) > 0 {
			if def := kept[len(kept)-1]; retestsInPlace(def, instr) {
				if retests[instr.Op] {
					def.Op = complements[def.Op]
				}
				continue
			}
		}
		kept = append(kept, instr)
	}
	clear(instrs[len(kept):])
	return kept
}

// retestsInPlace reports whether test asks def's result for its truth
// again without reading it anywhere else: `seqz d d` or `snez d d`
// over a def writing d. Requiring both of test's operands to be def's
// destination removes the liveness question: nothing reads d's old value between the two.
func retestsInPlace(def, test *mir.Instr) bool {
	negate, retest := retests[test.Op]
	if !retest {
		return false
	}
	// The two retests ask different things of the definition. `seqz` leaves the
	// complement standing in its place, so def has to have one; `snez` leaves def
	// itself, so a result that is already 0 or 1 is the whole requirement, and the
	// two tables together are exactly the instructions that answer one.
	_, complemented := complements[def.Op]
	if negate && !complemented {
		return false
	}
	if !negate && !complemented && !uncomplementedSets[def.Op] {
		return false
	}
	defined, ok := writtenReg(def)
	if !ok {
		return false
	}
	dst, src, ok := writtenAndRead(test)
	return ok && dst.Reg == defined.Reg && src.Reg == defined.Reg
}

// writtenAndRead is the register a two-operand instruction assigns
// and the register it reads, and false for one that is not two
// operands over two physical registers. The read is the operand the
// write is not, which keeps the two apart under a table that moves the write.
func writtenAndRead(instr *mir.Instr) (written, read mir.PhysReg, ok bool) {
	at, placed := writeIndex(instr)
	if !placed || len(instr.Args) != 2 {
		return mir.PhysReg{}, mir.PhysReg{}, false
	}
	written, writes := instr.Args[at].(mir.PhysReg)
	read, reads := instr.Args[1-at].(mir.PhysReg)
	return written, read, writes && reads
}

// writtenReg is the physical register an instruction assigns, and false for one
// that assigns none, writes something other than a physical register, or is not
// in the target's operand table at all.
func writtenReg(instr *mir.Instr) (mir.PhysReg, bool) {
	at, placed := writeIndex(instr)
	if !placed {
		return mir.PhysReg{}, false
	}
	reg, ok := instr.Args[at].(mir.PhysReg)
	return reg, ok
}

// writeIndex is the operand position an instruction assigns, and
// false for one that assigns none or is not in the target's operand
// table. A table that cannot state where an instruction writes is a
// build defect: allocation asks the same question and has already run.
func writeIndex(instr *mir.Instr) (int, bool) {
	info, known := instr.Op.Instruction()
	if !known {
		return 0, false
	}
	at, err := info.WriteIndex()
	if err != nil || at < 0 {
		return 0, false
	}
	return at, true
}

// branchComplements pairs each conditional branch with the branch
// taken in exactly the cases it is not, for the same reason
// [complements] pairs set instructions. bnan has no negation on the
// machine at all. Both members share operand positions, with the target last.
var branchComplements = map[ic10.Opcode]ic10.Opcode{
	isa.OpBeq:  isa.OpBne,
	isa.OpBne:  isa.OpBeq,
	isa.OpBeqz: isa.OpBnez,
	isa.OpBnez: isa.OpBeqz,
	isa.OpBdse: isa.OpBdns,
	isa.OpBdns: isa.OpBdse,
}

// invertBranchesOverFallthrough removes the jump from a block ending
// in a conditional branch to its own fallthrough followed by a jump
// elsewhere: `bnez r0 L; j M` with L as fallthrough becomes `beqz r0
// M`. [mir.Block.Succs] is left stale.
func invertBranchesOverFallthrough(fn *mir.Func) {
	for i, block := range fn.Blocks {
		if len(block.Instrs) < 2 {
			continue
		}
		jump := block.Instrs[len(block.Instrs)-1]
		branch := block.Instrs[len(block.Instrs)-2]
		if jump.Op != isa.OpJ {
			continue
		}
		fallen, ok := jump.Args[0].(mir.Label)
		if !ok {
			continue
		}
		complement, invertible := branchComplements[branch.Op]
		if !invertible {
			continue
		}
		taken, ok := branch.Args[len(branch.Args)-1].(mir.Label)
		if !ok || !mir.FallsThroughTo(fn, i, taken.Name) {
			continue
		}
		branch.Op = complement
		branch.Args[len(branch.Args)-1] = fallen
		clear(block.Instrs[len(block.Instrs)-1:])
		block.Instrs = block.Instrs[:len(block.Instrs)-1]
	}
}
