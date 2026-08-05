package mir

import (
	"fmt"
	"math"
	"slices"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/isa"
)

// MaterialiseUnreadable replaces every literal operand naming a value the
// chip's operand parser cannot reproduce with the arithmetic that computes it.
// [ic10.Unreadable] holds the class and the remedy; a NaN operand raises
// IncorrectVariable every tick, and a negative zero silently loads +0.0, which
// the machine then treats as a different value everywhere it looks at sign.
//
// It runs on selection's output and before register allocation, since minting
// a register for a value outside a move turns the function virtual, which
// requires re-running [Program.Validate] to hold its successor edges before
// allocation reads them.
func MaterialiseUnreadable(p *Program) error {
	if p == nil {
		return nil
	}
	for i, fn := range p.Funcs {
		if fn == nil {
			return fmt.Errorf("function %d is nil", i)
		}
		for j, block := range fn.Blocks {
			if block == nil {
				return fmt.Errorf("function %s: block %d is nil", fn.Name, j)
			}
			if err := materialiseBlock(fn, block); err != nil {
				return fmt.Errorf("function %s, block %s: %w", fn.Name, block.Label, err)
			}
		}
	}
	if err := p.Validate(); err != nil {
		return fmt.Errorf("the program is malformed once its unreadable literals are materialised: %w", err)
	}
	return checkMaterialised(p)
}

func materialiseBlock(fn *Func, block *Block) error {
	for i, instr := range block.Instrs {
		if instr == nil {
			return fmt.Errorf("instruction %d is nil", i)
		}
	}
	if !slices.ContainsFunc(block.Instrs, holdsUnreadable) {
		return nil
	}
	rewritten := make([]*Instr, 0, len(block.Instrs))
	for _, instr := range block.Instrs {
		if dst, remedy, ok := movesUnreadable(instr); ok {
			computed, err := compute(instr, dst, remedy)
			if err != nil {
				return err
			}
			rewritten = append(rewritten, computed)
			continue
		}
		for _, remedy := range remedies(instr) {
			temp := fn.NewVirtReg()
			computed, err := compute(instr, temp, remedy)
			if err != nil {
				return err
			}
			replaceUnreadable(instr, remedy, temp)
			rewritten = append(rewritten, computed)
		}
		rewritten = append(rewritten, instr)
	}
	block.Instrs = rewritten
	return nil
}

func holdsUnreadable(instr *Instr) bool {
	return slices.ContainsFunc(instr.Args, func(arg Operand) bool {
		_, unreadable := unreadableImm(arg)
		return unreadable
	})
}

// unreadableImm reports the remedy an operand needs, and false for an operand
// that is not a literal or is one the chip reads back as itself.
func unreadableImm(arg Operand) (ic10.UnreadableValue, bool) {
	imm, ok := arg.(Imm)
	if !ok {
		return ic10.UnreadableValue{}, false
	}
	return ic10.Unreadable(imm.Value)
}

// remedies lists the distinct arithmetic instr's literal operands need, in the
// order those operands appear. Operands sharing a remedy appear once, which is
// what puts two NaN operands of one instruction behind one division.
func remedies(instr *Instr) []ic10.UnreadableValue {
	var needed []ic10.UnreadableValue
	for _, arg := range instr.Args {
		remedy, unreadable := unreadableImm(arg)
		if unreadable && !slices.ContainsFunc(needed, func(seen ic10.UnreadableValue) bool { return sameRemedy(seen, remedy) }) {
			needed = append(needed, remedy)
		}
	}
	return needed
}

// sameRemedy reports whether two remedies are the same instruction, which
// decides whether two operands share a register. Bits are compared rather
// than ==, since == reads +0 and -0 as equal — the exact conflation this pass
// exists to close. The reason text is not compared.
func sameRemedy(a, b ic10.UnreadableValue) bool {
	return a.Op == b.Op &&
		math.Float64bits(a.Left) == math.Float64bits(b.Left) &&
		math.Float64bits(a.Right) == math.Float64bits(b.Right)
}

// movesUnreadable reports the destination and the remedy when instr does
// nothing but copy an unreadable value into a register, letting the arithmetic
// take its place rather than precede it.
//
// Operand positions are read literally rather than off the table, unlike the
// rest of the backend. That is safe here because being wrong only misses the
// shortening: the general path still mints a register and computes the value,
// one line longer.
func movesUnreadable(instr *Instr) (Operand, ic10.UnreadableValue, bool) {
	var none ic10.UnreadableValue
	if instr.Op != isa.OpMove || len(instr.Args) != 2 {
		return nil, none, false
	}
	remedy, unreadable := unreadableImm(instr.Args[1])
	if !unreadable {
		return nil, none, false
	}
	return instr.Args[0], remedy, true
}

// compute builds the instruction answering remedy into dst, carrying the
// position and inline chain of the instruction that wanted the value so that
// diagnostics and byte accounting both charge the construct that asked for it.
func compute(at *Instr, dst Operand, remedy ic10.UnreadableValue) (*Instr, error) {
	computed, err := NewInstr(remedy.Op, at.Pos, dst, Imm{Value: remedy.Left}, Imm{Value: remedy.Right})
	if err != nil {
		return nil, fmt.Errorf("materialising the literal %s reads: %w", at, err)
	}
	computed.Inline = at.Inline
	return computed, nil
}

// replaceUnreadable puts the register holding the computed value in every
// position a literal needing that same remedy held. The kinds still check out
// unasked: a register satisfies every position a literal does, and the register
// positions besides.
func replaceUnreadable(instr *Instr, remedy ic10.UnreadableValue, temp VirtReg) {
	for i, arg := range instr.Args {
		if found, unreadable := unreadableImm(arg); unreadable && sameRemedy(found, remedy) {
			instr.Args[i] = temp
		}
	}
}

// checkMaterialised sweeps the whole program for a literal the chip's parser
// still cannot read back. It is not the walk above restated: a remedy is
// itself two literals, and one from the same unreadable class would be built
// by the walk and never revisited by it.
func checkMaterialised(p *Program) error {
	for _, fn := range p.Funcs {
		for _, block := range fn.Blocks {
			for _, instr := range block.Instrs {
				for i, arg := range instr.Args {
					remedy, unreadable := unreadableImm(arg)
					if unreadable {
						return fmt.Errorf("function %s, block %s: %s still names at operand %d a value the chip cannot read: %s",
							fn.Name, block.Label, instr, i, remedy.Reason)
					}
				}
			}
		}
	}
	return nil
}
