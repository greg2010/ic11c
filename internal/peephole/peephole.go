// Package peephole rewrites an allocated program where a pair of adjacent
// instructions computes what one instruction computes.
//
// It runs after register allocation because that is the first point either case
// it covers is visible: both ask whether two operands are the same storage, and
// a phi becomes copies over virtual registers whose physical registers are the
// allocator's decision, not selection's. It runs before emission because
// dropping an instruction moves every line after it and branch targets are
// absolute line numbers.
//
// The cost model is the one the rest of the backend uses. An instruction is one
// line against a 128 line budget and one execution slot against a 128
// instruction tick, and a program of a dozen lines can spend a tenth of them on
// work already done.
//
// # Liveness
//
// The pass has no liveness information: it sees one block's instruction list
// and neither the interference graph the allocator built nor a live-out set per
// block. Every rewrite here is therefore restricted to a shape that needs none.
// An instruction reading and writing one register kills that register's earlier
// value whatever happens later in the program, so a rewrite that folds the
// earlier definition into it is sound without knowing whether the register is
// live out. The general form — a complement written to a second register, where
// the first may still be read — is what liveness would be needed for and is not
// taken.
package peephole

import (
	"slices"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/mir"
)

// complements pairs each set instruction that answers exactly 0 or 1 with the
// instruction answering its exact complement.
//
// docs/target.md records which pairs qualify. The ordered comparisons do not:
// `slt` and `sge` are both false when either operand is NaN, so neither is the
// other's negation and folding one into the other would answer 1 where the
// machine answers 0. Equality, the approximate forms, the device test, and the
// NaN test are total, and their `z` forms substitute a literal zero operand
// without changing the predicate.
//
// Both members of a pair take the same operands in the same positions, which is
// what makes rewriting the opcode alone produce a valid instruction rather than
// one the emitter has to reject.
var complements = map[ic10.Opcode]ic10.Opcode{
	ic10.OpSeq:   ic10.OpSne,
	ic10.OpSne:   ic10.OpSeq,
	ic10.OpSeqz:  ic10.OpSnez,
	ic10.OpSnez:  ic10.OpSeqz,
	ic10.OpSap:   ic10.OpSna,
	ic10.OpSna:   ic10.OpSap,
	ic10.OpSapz:  ic10.OpSnaz,
	ic10.OpSnaz:  ic10.OpSapz,
	ic10.OpSdse:  ic10.OpSdns,
	ic10.OpSdns:  ic10.OpSdse,
	ic10.OpSnan:  ic10.OpSnanz,
	ic10.OpSnanz: ic10.OpSnan,
}

// Run rewrites prog in place.
//
// Identity moves go first so that one sitting between a comparison and the
// negation of its result does not hide the fold behind it.
//
// Blocks are kept even when emptied. A label resolves to the first line at or
// after the block that carries it, so a branch into a block this emptied still
// reaches the code that followed it, and removing the block would instead
// require rewriting every branch that named it.
//
// prog must hold no nil function or block, which mir.Program.Validate is what
// reports. A nil program is a program with nothing to rewrite.
func Run(prog *mir.Program) {
	if prog == nil {
		return
	}
	for _, fn := range prog.Funcs {
		for _, block := range fn.Blocks {
			block.Instrs = slices.DeleteFunc(block.Instrs, isIdentityMove)
			block.Instrs = foldNegations(block.Instrs)
		}
	}
}

// isIdentityMove reports whether an instruction copies a register onto itself.
//
// It asks about physical registers only. Two virtual registers naming the same
// value are the normal output of phi lowering and are not the same storage
// until allocation says so.
func isIdentityMove(instr *mir.Instr) bool {
	if instr == nil || instr.Op != ic10.OpMove || len(instr.Args) != 2 {
		return false
	}
	dst, ok := instr.Args[0].(mir.PhysReg)
	if !ok {
		return false
	}
	src, ok := instr.Args[1].(mir.PhysReg)
	return ok && src.Reg == dst.Reg
}

// foldNegations turns a set instruction whose result the next instruction
// immediately negates in place into the instruction answering the complement,
// so that `snan r1 r0; seqz r1 r1` becomes `snanz r1 r0`.
//
// Adjacency within one block is what makes this local. A branch target resolves
// to the start of a block, so no control flow can arrive between two
// instructions of the same block and observe the value the fold removes.
func foldNegations(instrs []*mir.Instr) []*mir.Instr {
	kept := instrs[:0]
	for _, instr := range instrs {
		if len(kept) > 0 {
			if def := kept[len(kept)-1]; negatesInPlace(def, instr) {
				def.Op = complements[def.Op]
				continue
			}
		}
		kept = append(kept, instr)
	}
	clear(instrs[len(kept):])
	return kept
}

// negatesInPlace reports whether test turns def's result round without reading
// it anywhere else.
//
// It holds for `seqz d d` over a def writing d, and for that shape alone. A
// `seqz` is `d == 0`, which is the complement exactly when the value tested is
// already 0 or 1, so def has to be one of the set instructions that answers
// nothing else. Requiring test's two operands to be def's destination is what
// removes the liveness question: test overwrites d, so d's old value has no
// reader after it, and nothing sits between the two to have read it before.
func negatesInPlace(def, test *mir.Instr) bool {
	if def == nil || test == nil {
		return false
	}
	if _, negatable := complements[def.Op]; !negatable {
		return false
	}
	if test.Op != ic10.OpSeqz || len(test.Args) != 2 || len(def.Args) == 0 {
		return false
	}
	defined, ok := def.Args[0].(mir.PhysReg)
	if !ok {
		return false
	}
	dst, ok := test.Args[0].(mir.PhysReg)
	if !ok || dst.Reg != defined.Reg {
		return false
	}
	src, ok := test.Args[1].(mir.PhysReg)
	return ok && src.Reg == defined.Reg
}
