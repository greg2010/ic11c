package peephole

import (
	"slices"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/isa"
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
					instr(t, isa.OpMove, phys(0), phys(0)),
					instr(t, isa.OpAdd, phys(1), phys(0), mir.Imm{Value: 1}),
				}
			},
			want: []string{"add r1 r0 1"},
		},
		{
			name: "several in a row",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, isa.OpMove, phys(2), phys(2)),
					instr(t, isa.OpMove, phys(3), phys(3)),
					instr(t, isa.OpMove, phys(4), phys(4)),
				}
			},
			want: nil,
		},
		{
			name: "a move between two registers is kept",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{instr(t, isa.OpMove, phys(0), phys(1))}
			},
			want: []string{"move r0 r1"},
		},
		{
			name: "a move of a literal is kept",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{instr(t, isa.OpMove, phys(0), mir.Imm{Value: 0})}
			},
			want: []string{"move r0 0"},
		},
		{
			name: "a move of one virtual register onto itself is kept",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				v := mir.VirtReg{ID: 3}
				return []*mir.Instr{instr(t, isa.OpMove, v, v)}
			},
			want: []string{"move vr3 vr3"},
		},
		{
			name: "another opcode with equal operands is kept",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{instr(t, isa.OpAdd, phys(0), phys(0), phys(0))}
			},
			want: []string{"add r0 r0 r0"},
		},
		{
			name: "the stack pointer is not special",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{instr(t, isa.OpMove, mir.PhysReg{Reg: ic10.RegSP}, mir.PhysReg{Reg: ic10.RegSP})}
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

	head.Append(instr(t, isa.OpJ, mir.Label{Name: "main.middle"}))
	middle.Append(instr(t, isa.OpMove, phys(0), phys(0)))
	tail.Append(instr(t, isa.OpAdd, phys(1), phys(0), mir.Imm{Value: 1}))

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

// TestRunDropsAJumpAnEmptiedBlockMadeAFallthrough covers the jump selection
// could not drop. It asked the same question before allocation, when the block
// between the jump and its target still held the copies a phi became; emptying
// that block here is what leaves the jump going where control already goes.
func TestRunDropsAJumpAnEmptiedBlockMadeAFallthrough(t *testing.T) {
	tests := []struct {
		name  string
		build func(t *testing.T, fn *mir.Func)
		want  []string
	}{
		{
			name: "a jump past a block the identity fold emptied",
			build: func(t *testing.T, fn *mir.Func) {
				t.Helper()
				head := fn.NewBlock("main.entry", pos)
				middle := fn.NewBlock("main.middle", pos)
				tail := fn.NewBlock("main.tail", pos)
				head.Append(
					instr(t, isa.OpAdd, phys(0), phys(0), mir.Imm{Value: 1}),
					instr(t, isa.OpJ, mir.Label{Name: "main.tail"}),
				)
				middle.Append(instr(t, isa.OpMove, phys(1), phys(1)))
				tail.Append(instr(t, isa.OpAdd, phys(0), phys(0), mir.Imm{Value: 2}))
			},
			want: []string{"add r0 r0 1", "add r0 r0 2"},
		},
		{
			// The middle block is only empty once its own jump has gone, so the
			// head's jump reaches the tail by fallthrough on a last-to-first
			// walk and not on a first-to-last one.
			name: "a jump past a block this empties by dropping its jump",
			build: func(t *testing.T, fn *mir.Func) {
				t.Helper()
				head := fn.NewBlock("main.entry", pos)
				middle := fn.NewBlock("main.middle", pos)
				tail := fn.NewBlock("main.tail", pos)
				head.Append(instr(t, isa.OpJ, mir.Label{Name: "main.tail"}))
				middle.Append(instr(t, isa.OpJ, mir.Label{Name: "main.tail"}))
				tail.Append(instr(t, isa.OpAdd, phys(0), phys(0), mir.Imm{Value: 2}))
			},
			want: []string{"add r0 r0 2"},
		},
		{
			name: "a jump past a block that still holds an instruction is kept",
			build: func(t *testing.T, fn *mir.Func) {
				t.Helper()
				head := fn.NewBlock("main.entry", pos)
				middle := fn.NewBlock("main.middle", pos)
				tail := fn.NewBlock("main.tail", pos)
				head.Append(instr(t, isa.OpJ, mir.Label{Name: "main.tail"}))
				middle.Append(instr(t, isa.OpMove, phys(1), phys(2)))
				tail.Append(instr(t, isa.OpAdd, phys(0), phys(0), mir.Imm{Value: 2}))
			},
			want: []string{"j main.tail", "move r1 r2", "add r0 r0 2"},
		},
		{
			name: "a jump backwards is kept",
			build: func(t *testing.T, fn *mir.Func) {
				t.Helper()
				head := fn.NewBlock("main.entry", pos)
				latch := fn.NewBlock("main.latch", pos)
				head.Append(instr(t, isa.OpAdd, phys(0), phys(0), mir.Imm{Value: 1}))
				latch.Append(instr(t, isa.OpJ, mir.Label{Name: "main.entry"}))
			},
			want: []string{"add r0 r0 1", "j main.entry"},
		},
		{
			name: "a return through the link register is not a jump to a label",
			build: func(t *testing.T, fn *mir.Func) {
				t.Helper()
				head := fn.NewBlock("main.entry", pos)
				tail := fn.NewBlock("main.tail", pos)
				head.Append(instr(t, isa.OpJ, mir.PhysReg{Reg: ic10.RegRA}))
				tail.Append(instr(t, isa.OpAdd, phys(0), phys(0), mir.Imm{Value: 2}))
			},
			want: []string{"j ra", "add r0 r0 2"},
		},
		{
			// A call names its target where a jump does, and dropping one would
			// turn a call into a fallthrough that never comes back.
			name: "a call onto the following block is kept",
			build: func(t *testing.T, fn *mir.Func) {
				t.Helper()
				head := fn.NewBlock("main.entry", pos)
				tail := fn.NewBlock("main.tail", pos)
				head.Append(instr(t, isa.OpJal, mir.Label{Name: "main.tail"}))
				tail.Append(instr(t, isa.OpAdd, phys(0), phys(0), mir.Imm{Value: 2}))
			},
			want: []string{"jal main.tail", "add r0 r0 2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn := mir.NewFunc("main", pos)
			tt.build(t, fn)
			prog := &mir.Program{Funcs: []*mir.Func{fn}}

			Run(prog)

			got := rendered(prog)
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Errorf("Run left\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
			}
			if err := prog.Validate(); err != nil {
				t.Errorf("the rewritten program does not validate: %v", err)
			}
		})
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

// TestRunFoldsARetestedSet covers both in-place re-tests: seqz, which
// leaves the complement docs/target.md records as exact, and snez,
// which leaves nothing. It holds the fold off the ordered comparisons
// (see [complements]) and off any definition answering something other than 0 or 1.
func TestRunFoldsARetestedSet(t *testing.T) {
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
					instr(t, isa.OpSnan, phys(1), phys(0)),
					instr(t, isa.OpSeqz, phys(1), phys(1)),
				}
			},
			want: []string{"snanz r1 r0"},
		},
		{
			name: "the NaN test the other way round",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, isa.OpSnanz, phys(1), phys(0)),
					instr(t, isa.OpSeqz, phys(1), phys(1)),
				}
			},
			want: []string{"snan r1 r0"},
		},
		{
			name: "equality",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, isa.OpSeq, phys(2), phys(0), phys(1)),
					instr(t, isa.OpSeqz, phys(2), phys(2)),
				}
			},
			want: []string{"sne r2 r0 r1"},
		},
		{
			name: "inequality",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, isa.OpSne, phys(2), phys(0), phys(1)),
					instr(t, isa.OpSeqz, phys(2), phys(2)),
				}
			},
			want: []string{"seq r2 r0 r1"},
		},
		{
			name: "a test against zero, which is the double negation",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, isa.OpSeqz, phys(1), phys(0)),
					instr(t, isa.OpSeqz, phys(1), phys(1)),
				}
			},
			want: []string{"snez r1 r0"},
		},
		{
			// sna is not sap's negation, so the seqz stands. The snez retest asks
			// only for a result that is already 0 or 1, which sap answers, so that
			// one still goes.
			name: "an approximate comparison keeps its seqz",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, isa.OpSap, phys(3), phys(0), phys(1), phys(2)),
					instr(t, isa.OpSeqz, phys(3), phys(3)),
				}
			},
			want: []string{"sap r3 r0 r1 r2", "seqz r3 r3"},
		},
		{
			name: "an approximate comparison against zero drops its snez",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, isa.OpSnaz, phys(2), phys(0), phys(1)),
					instr(t, isa.OpSnez, phys(2), phys(2)),
				}
			},
			want: []string{"snaz r2 r0 r1"},
		},
		{
			name: "the device test",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, isa.OpSdse, phys(1), devicePin(t)),
					instr(t, isa.OpSeqz, phys(1), phys(1)),
				}
			},
			want: []string{"sdns r1 d0"},
		},
		{
			name: "an ordered comparison is not a complement and is kept",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, isa.OpSlt, phys(2), phys(0), phys(1)),
					instr(t, isa.OpSeqz, phys(2), phys(2)),
				}
			},
			want: []string{"slt r2 r0 r1", "seqz r2 r2"},
		},
		{
			name: "an ordered comparison against zero is kept",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, isa.OpSgez, phys(1), phys(0)),
					instr(t, isa.OpSeqz, phys(1), phys(1)),
				}
			},
			want: []string{"sgez r1 r0", "seqz r1 r1"},
		},
		{
			name: "a negation into a second register needs liveness and is kept",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, isa.OpSnan, phys(1), phys(0)),
					instr(t, isa.OpSeqz, phys(2), phys(1)),
				}
			},
			want: []string{"snan r1 r0", "seqz r2 r1"},
		},
		{
			name: "a negation of some other register is kept",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, isa.OpSnan, phys(1), phys(0)),
					instr(t, isa.OpSeqz, phys(3), phys(3)),
				}
			},
			want: []string{"snan r1 r0", "seqz r3 r3"},
		},
		{
			name: "an instruction in between is kept and blocks the fold",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, isa.OpSnan, phys(1), phys(0)),
					instr(t, isa.OpAdd, phys(4), phys(1), mir.Imm{Value: 1}),
					instr(t, isa.OpSeqz, phys(1), phys(1)),
				}
			},
			want: []string{"snan r1 r0", "add r4 r1 1", "seqz r1 r1"},
		},
		{
			// The allocator spills by poking the defining register into a data
			// region slot. The poke is the last kept instruction when the
			// re-test arrives, so the fold does not fire across it.
			name: "a spill store between the two blocks the fold",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, isa.OpSnan, phys(1), phys(0)),
					instr(t, isa.OpPoke, mir.Imm{Value: 5}, phys(1)),
					instr(t, isa.OpSeqz, phys(1), phys(1)),
				}
			},
			want: []string{"snan r1 r0", "poke 5 r1", "seqz r1 r1"},
		},
		{
			// A reload writes a scratch register and reads neither operand, so
			// the fold would still be sound across it. Adjacency is enforced
			// rather than reasoned about, and this is the cost of that.
			name: "a reload between the two blocks the fold",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, isa.OpSnan, phys(1), phys(0)),
					instr(t, isa.OpGet, phys(2), mir.NewDeviceBase(), mir.Imm{Value: 5}),
					instr(t, isa.OpSeqz, phys(1), phys(1)),
				}
			},
			want: []string{"snan r1 r0", "get r2 db 5", "seqz r1 r1"},
		},
		{
			name: "an identity move between the two does not block the fold",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, isa.OpSnan, phys(1), phys(0)),
					instr(t, isa.OpMove, phys(1), phys(1)),
					instr(t, isa.OpSeqz, phys(1), phys(1)),
				}
			},
			want: []string{"snanz r1 r0"},
		},
		{
			name: "a chain folds one pair at a time",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, isa.OpSnan, phys(1), phys(0)),
					instr(t, isa.OpSeqz, phys(1), phys(1)),
					instr(t, isa.OpSeqz, phys(1), phys(1)),
				}
			},
			want: []string{"snan r1 r0"},
		},
		{
			name: "a non-comparison producing something other than 0 or 1 is kept",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, isa.OpAdd, phys(1), phys(0), mir.Imm{Value: 2}),
					instr(t, isa.OpSeqz, phys(1), phys(1)),
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
					instr(t, isa.OpSnan, v, mir.VirtReg{ID: 0}),
					instr(t, isa.OpSeqz, v, v),
				}
			},
			want: []string{"snan vr1 vr0", "seqz vr1 vr1"},
		},
		{
			// snez of a value that is already 0 or 1 is the value, so the
			// definition stands alone. A source condition reading an intrinsic
			// that answers a truth value writes this, and the optimizer cannot
			// fold it: an opaque declaration states no range for its result.
			name: "asking a truth value for its truth again leaves the definition alone",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, isa.OpSnan, phys(1), phys(0)),
					instr(t, isa.OpSnez, phys(1), phys(1)),
				}
			},
			want: []string{"snan r1 r0"},
		},
		{
			name: "the same over the device test",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, isa.OpSdse, phys(1), devicePin(t)),
					instr(t, isa.OpSnez, phys(1), phys(1)),
				}
			},
			want: []string{"sdse r1 d0"},
		},
		{
			name: "a re-test of something that is not a truth value is kept",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, isa.OpAdd, phys(1), phys(0), mir.Imm{Value: 2}),
					instr(t, isa.OpSnez, phys(1), phys(1)),
				}
			},
			want: []string{"add r1 r0 2", "snez r1 r1"},
		},
		{
			// The ordered comparisons answer 0 or 1 for every pair of operands,
			// a NaN included. What they lack is a negation, and this arm needs
			// none: it removes the test and leaves the definition standing.
			name: "an ordered comparison re-tested with snez is the comparison",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, isa.OpSlt, phys(2), phys(0), phys(1)),
					instr(t, isa.OpSnez, phys(2), phys(2)),
				}
			},
			want: []string{"slt r2 r0 r1"},
		},
		{
			name: "an ordered comparison against zero re-tested with snez",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, isa.OpSgez, phys(1), phys(0)),
					instr(t, isa.OpSnez, phys(1), phys(1)),
				}
			},
			want: []string{"sgez r1 r0"},
		},
		{
			name: "a re-test into a second register needs liveness and is kept",
			build: func(t *testing.T) []*mir.Instr {
				t.Helper()
				return []*mir.Instr{
					instr(t, isa.OpSnan, phys(1), phys(0)),
					instr(t, isa.OpSnez, phys(2), phys(1)),
				}
			},
			want: []string{"snan r1 r0", "snez r2 r1"},
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

	head.Append(instr(t, isa.OpSnan, phys(1), phys(0)))
	tail.Append(instr(t, isa.OpSeqz, phys(1), phys(1)))

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

// TestRewrittenOperandsComeFromTheInstructionTable names the operand
// each instruction this pass rewrites assigns its result to, and
// holds [writtenAndRead] to resolving the other operand as the read:
// a table moving a write to operand 1 would fold `seqz rDst rDef` into the wrong register.
func TestRewrittenOperandsComeFromTheInstructionTable(t *testing.T) {
	tests := []struct {
		name string
		op   ic10.Opcode
		want int
	}{
		{name: "move", op: isa.OpMove, want: 0},
		{name: "seqz", op: isa.OpSeqz, want: 0},
		{name: "snez", op: isa.OpSnez, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, known := tt.op.Instruction()
			if !known {
				t.Fatalf("%v is not in the instruction table", tt.op)
			}
			at, err := info.WriteIndex()
			if err != nil {
				t.Fatalf("WriteIndex(%v): %v", tt.op, err)
			}
			if at != tt.want {
				t.Errorf("%v writes operand %d, want operand %d", tt.op, at, tt.want)
			}

			// Two different registers, so that a read taken from the write's own
			// position is a different answer rather than the same one.
			built := instr(t, tt.op, phys(1), phys(2))
			written, read, ok := writtenAndRead(built)
			if !ok {
				t.Fatalf("writtenAndRead(%s) declined an instruction over two physical registers", built)
			}
			if wantWritten := built.Args[at]; written != wantWritten {
				t.Errorf("writtenAndRead(%s) wrote %s, want operand %d, %s", built, written, at, wantWritten)
			}
			if wantRead := built.Args[1-at]; read != wantRead {
				t.Errorf("writtenAndRead(%s) read %s, want operand %d, %s", built, read, 1-at, wantRead)
			}
		})
	}
}

// TestWrittenAndReadDeclinesWhatItCannotPlace covers the operands the pair
// accessor answers nothing for, each of which leaves the instruction standing.
func TestWrittenAndReadDeclinesWhatItCannotPlace(t *testing.T) {
	tests := []struct {
		name  string
		build func(t *testing.T) *mir.Instr
	}{
		{
			name: "an instruction of some other arity",
			build: func(t *testing.T) *mir.Instr {
				t.Helper()
				return instr(t, isa.OpAdd, phys(1), phys(2), phys(3))
			},
		},
		{
			name: "a write to something other than a register",
			build: func(t *testing.T) *mir.Instr {
				t.Helper()
				return instr(t, isa.OpPoke, mir.Imm{Value: 5}, phys(1))
			},
		},
		{
			name: "a read of something other than a register",
			build: func(t *testing.T) *mir.Instr {
				t.Helper()
				return instr(t, isa.OpMove, phys(1), mir.Imm{Value: 5})
			},
		},
		{
			name: "a virtual register, which is not storage until allocation says so",
			build: func(t *testing.T) *mir.Instr {
				t.Helper()
				return instr(t, isa.OpMove, mir.VirtReg{ID: 1}, mir.VirtReg{ID: 1})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			built := tt.build(t)
			if _, _, ok := writtenAndRead(built); ok {
				t.Errorf("writtenAndRead(%s) answered a pair", built)
			}
		})
	}
}
