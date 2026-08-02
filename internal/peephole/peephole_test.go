package peephole

import (
	"slices"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/mir"
	"github.com/greg2010/ic11c/internal/source"
)

// pos is the location every built instruction carries. Nothing here depends on
// its value; it exists so that mir.NewInstr produces something a diagnostic
// could name.
var pos = source.Position{File: "t.mc", Line: 1, Column: 1}

func instr(t *testing.T, op ic10.Opcode, args ...mir.Operand) *mir.Instr {
	t.Helper()
	built, err := mir.NewInstr(op, pos, args...)
	if err != nil {
		t.Fatalf("NewInstr(%v): %v", op, err)
	}
	return built
}

func phys(r int) mir.PhysReg { return mir.PhysReg{Reg: ic10.Register(r)} }

// rendered lists every instruction the program still holds, in emission order.
func rendered(prog *mir.Program) []string {
	var lines []string
	if prog == nil {
		return nil
	}
	for _, fn := range prog.Funcs {
		for _, block := range fn.Blocks {
			for _, in := range block.Instrs {
				lines = append(lines, in.String())
			}
		}
	}
	return lines
}

func TestRunDropsIdentityMoves(t *testing.T) {
	tests := []struct {
		name  string
		build func(t *testing.T) []*mir.Instr
		want  []string
	}{
		{
			name: "a move of a register onto itself",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, ic10.OpMove, phys(0), phys(0)),
					instr(t, ic10.OpAdd, phys(1), phys(0), mir.Imm{Value: 1}),
				}
			},
			want: []string{"add r1 r0 1"},
		},
		{
			name: "several in a row",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, ic10.OpMove, phys(2), phys(2)),
					instr(t, ic10.OpMove, phys(3), phys(3)),
					instr(t, ic10.OpMove, phys(4), phys(4)),
				}
			},
			want: nil,
		},
		{
			name: "a move between two registers is kept",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{instr(t, ic10.OpMove, phys(0), phys(1))}
			},
			want: []string{"move r0 r1"},
		},
		{
			name: "a move of a literal is kept",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{instr(t, ic10.OpMove, phys(0), mir.Imm{Value: 0})}
			},
			want: []string{"move r0 0"},
		},
		{
			name: "a move of one virtual register onto itself is kept",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				v := mir.VirtReg{ID: 3}
				return []*mir.Instr{instr(t, ic10.OpMove, v, v)}
			},
			want: []string{"move vr3 vr3"},
		},
		{
			name: "another opcode with equal operands is kept",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{instr(t, ic10.OpAdd, phys(0), phys(0), phys(0))}
			},
			want: []string{"add r0 r0 r0"},
		},
		{
			name: "the stack pointer is not special",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{instr(t, ic10.OpMove, mir.PhysReg{Reg: ic10.RegSP}, mir.PhysReg{Reg: ic10.RegSP})}
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn := mir.NewFunc("main", pos)
			block := fn.NewBlock("main.entry", pos)
			block.Append(tt.build(t)...)
			prog := &mir.Program{Funcs: []*mir.Func{fn}}

			Run(prog)

			got := rendered(prog)
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Errorf("Run left\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
			}
		})
	}
}

// TestRunKeepsAnEmptiedBlock holds the label a branch resolves through. Removing
// the block would leave the branch naming nothing; keeping it lets the label
// resolve to the first line after it.
func TestRunKeepsAnEmptiedBlock(t *testing.T) {
	fn := mir.NewFunc("main", pos)
	head := fn.NewBlock("main.entry", pos)
	middle := fn.NewBlock("main.middle", pos)
	tail := fn.NewBlock("main.tail", pos)

	head.Append(instr(t, ic10.OpJ, mir.Label{Name: "main.middle"}))
	middle.Append(instr(t, ic10.OpMove, phys(0), phys(0)))
	tail.Append(instr(t, ic10.OpAdd, phys(1), phys(0), mir.Imm{Value: 1}))

	Run(&mir.Program{Funcs: []*mir.Func{fn}})

	if len(fn.Blocks) != 3 {
		t.Fatalf("the function holds %d blocks, want 3", len(fn.Blocks))
	}
	if len(middle.Instrs) != 0 {
		t.Errorf("the identity move survived: %v", rendered(&mir.Program{Funcs: []*mir.Func{fn}}))
	}
	if middle.Label != "main.middle" {
		t.Errorf("the emptied block is labelled %q, want main.middle", middle.Label)
	}
}

// TestRunAcceptsNothing keeps the pass usable from a pipeline that has not
// built a program. A nil function or block is not covered: mir.Program.Validate
// reports one, and the pass reading past it silently is how a program that never
// held a block would reach emission looking finished.
func TestRunAcceptsNothing(t *testing.T) {
	tests := []struct {
		name string
		prog *mir.Program
	}{
		{name: "no program"},
		{name: "no functions", prog: &mir.Program{}},
		{name: "a function with no block", prog: &mir.Program{Funcs: []*mir.Func{{Name: "main"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			Run(tt.prog)
			if got := rendered(tt.prog); len(got) != 0 {
				t.Errorf("Run left %v, want nothing", got)
			}
		})
	}
}

// devicePin builds the d0 operand the device tests take.
func devicePin(t *testing.T) mir.Device {
	t.Helper()
	dev, err := mir.NewDevicePin(0, mir.NoConnection)
	if err != nil {
		t.Fatalf("NewDevicePin: %v", err)
	}
	return dev
}

// TestRunFoldsANegatedSetIntoItsComplement covers the pairs docs/target.md
// records as exact complements, and holds the pass off the ordered comparisons,
// which are not: both members answer 0 for a NaN operand, so replacing one with
// the other answers 1 where the machine answers 0.
func TestRunFoldsANegatedSetIntoItsComplement(t *testing.T) {
	tests := []struct {
		name  string
		build func(t *testing.T) []*mir.Instr
		want  []string
	}{
		{
			name: "the NaN test",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, ic10.OpSnan, phys(1), phys(0)),
					instr(t, ic10.OpSeqz, phys(1), phys(1)),
				}
			},
			want: []string{"snanz r1 r0"},
		},
		{
			name: "the NaN test the other way round",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, ic10.OpSnanz, phys(1), phys(0)),
					instr(t, ic10.OpSeqz, phys(1), phys(1)),
				}
			},
			want: []string{"snan r1 r0"},
		},
		{
			name: "equality",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, ic10.OpSeq, phys(2), phys(0), phys(1)),
					instr(t, ic10.OpSeqz, phys(2), phys(2)),
				}
			},
			want: []string{"sne r2 r0 r1"},
		},
		{
			name: "inequality",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, ic10.OpSne, phys(2), phys(0), phys(1)),
					instr(t, ic10.OpSeqz, phys(2), phys(2)),
				}
			},
			want: []string{"seq r2 r0 r1"},
		},
		{
			name: "a test against zero, which is the double negation",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, ic10.OpSeqz, phys(1), phys(0)),
					instr(t, ic10.OpSeqz, phys(1), phys(1)),
				}
			},
			want: []string{"snez r1 r0"},
		},
		{
			name: "the approximate forms",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, ic10.OpSap, phys(3), phys(0), phys(1), phys(2)),
					instr(t, ic10.OpSeqz, phys(3), phys(3)),
				}
			},
			want: []string{"sna r3 r0 r1 r2"},
		},
		{
			name: "the approximate form against zero",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, ic10.OpSnaz, phys(2), phys(0), phys(1)),
					instr(t, ic10.OpSeqz, phys(2), phys(2)),
				}
			},
			want: []string{"sapz r2 r0 r1"},
		},
		{
			name: "the device test",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, ic10.OpSdse, phys(1), devicePin(t)),
					instr(t, ic10.OpSeqz, phys(1), phys(1)),
				}
			},
			want: []string{"sdns r1 d0"},
		},
		{
			name: "an ordered comparison is not a complement and is kept",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, ic10.OpSlt, phys(2), phys(0), phys(1)),
					instr(t, ic10.OpSeqz, phys(2), phys(2)),
				}
			},
			want: []string{"slt r2 r0 r1", "seqz r2 r2"},
		},
		{
			name: "an ordered comparison against zero is kept",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, ic10.OpSgez, phys(1), phys(0)),
					instr(t, ic10.OpSeqz, phys(1), phys(1)),
				}
			},
			want: []string{"sgez r1 r0", "seqz r1 r1"},
		},
		{
			name: "a negation into a second register needs liveness and is kept",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, ic10.OpSnan, phys(1), phys(0)),
					instr(t, ic10.OpSeqz, phys(2), phys(1)),
				}
			},
			want: []string{"snan r1 r0", "seqz r2 r1"},
		},
		{
			name: "a negation of some other register is kept",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, ic10.OpSnan, phys(1), phys(0)),
					instr(t, ic10.OpSeqz, phys(3), phys(3)),
				}
			},
			want: []string{"snan r1 r0", "seqz r3 r3"},
		},
		{
			name: "an instruction in between is kept and blocks the fold",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, ic10.OpSnan, phys(1), phys(0)),
					instr(t, ic10.OpAdd, phys(4), phys(1), mir.Imm{Value: 1}),
					instr(t, ic10.OpSeqz, phys(1), phys(1)),
				}
			},
			want: []string{"snan r1 r0", "add r4 r1 1", "seqz r1 r1"},
		},
		{
			name: "an identity move between the two does not block the fold",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, ic10.OpSnan, phys(1), phys(0)),
					instr(t, ic10.OpMove, phys(1), phys(1)),
					instr(t, ic10.OpSeqz, phys(1), phys(1)),
				}
			},
			want: []string{"snanz r1 r0"},
		},
		{
			name: "a chain folds one pair at a time",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, ic10.OpSnan, phys(1), phys(0)),
					instr(t, ic10.OpSeqz, phys(1), phys(1)),
					instr(t, ic10.OpSeqz, phys(1), phys(1)),
				}
			},
			want: []string{"snan r1 r0"},
		},
		{
			name: "a non-comparison producing something other than 0 or 1 is kept",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, ic10.OpAdd, phys(1), phys(0), mir.Imm{Value: 2}),
					instr(t, ic10.OpSeqz, phys(1), phys(1)),
				}
			},
			want: []string{"add r1 r0 2", "seqz r1 r1"},
		},
		{
			name: "virtual registers are not folded",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				v := mir.VirtReg{ID: 1}
				return []*mir.Instr{
					instr(t, ic10.OpSnan, v, mir.VirtReg{ID: 0}),
					instr(t, ic10.OpSeqz, v, v),
				}
			},
			want: []string{"snan vr1 vr0", "seqz vr1 vr1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn := mir.NewFunc("main", pos)
			block := fn.NewBlock("main.entry", pos)
			block.Append(tt.build(t)...)
			prog := &mir.Program{Funcs: []*mir.Func{fn}}

			Run(prog)

			got := rendered(prog)
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Errorf("Run left\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
			}
		})
	}
}

// TestRunDoesNotFoldAcrossABlockBoundary holds the fold to one block. A branch
// target resolves to the start of a block, so a definition ending one block and
// a negation starting the next are not adjacent for any purpose: control can
// arrive at the negation without having run the definition.
func TestRunDoesNotFoldAcrossABlockBoundary(t *testing.T) {
	fn := mir.NewFunc("main", pos)
	head := fn.NewBlock("main.entry", pos)
	tail := fn.NewBlock("main.tail", pos)

	head.Append(instr(t, ic10.OpSnan, phys(1), phys(0)))
	tail.Append(instr(t, ic10.OpSeqz, phys(1), phys(1)))

	Run(&mir.Program{Funcs: []*mir.Func{fn}})

	want := []string{"snan r1 r0", "seqz r1 r1"}
	if got := rendered(&mir.Program{Funcs: []*mir.Func{fn}}); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("Run left\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestComplementsAreSubstitutable checks the invariant the fold rests on: it
// rewrites an opcode and leaves the operand list alone, which is only a valid
// instruction when both members of a pair take the same operands in the same
// positions.
func TestComplementsAreSubstitutable(t *testing.T) {
	for op, complement := range complements {
		if complements[complement] != op {
			t.Errorf("%v maps to %v, which maps back to %v", op, complement, complements[complement])
		}
		from, ok := op.Instruction()
		if !ok {
			t.Errorf("%v is not in the instruction table", op)
			continue
		}
		to, ok := complement.Instruction()
		if !ok {
			t.Errorf("%v is not in the instruction table", complement)
			continue
		}
		if len(from.Operands) != len(to.Operands) {
			t.Errorf("%v takes %d operands and %v takes %d", op, len(from.Operands), complement, len(to.Operands))
			continue
		}
		for i := range from.Operands {
			if !slices.Equal(from.Operands[i].Kinds, to.Operands[i].Kinds) {
				t.Errorf("%v operand %d accepts %v where %v accepts %v",
					op, i, from.Operands[i].Kinds, complement, to.Operands[i].Kinds)
			}
		}
	}
}
