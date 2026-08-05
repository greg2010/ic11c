package mir

import (
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/isa"
	"github.com/greg2010/ic11c/internal/source"
)

// negativeZero is the second member of the class, and the one no decimal
// spelling reaches: the chip's parser answers +0.0 for every one of them.
var negativeZero = Imm{Value: math.Copysign(0, -1)}

// rendered lists a block as the opcode and every operand of each instruction.
func rendered(t *testing.T, block *Block) []string {
	t.Helper()
	lines := make([]string, 0, len(block.Instrs))
	for _, instr := range block.Instrs {
		lines = append(lines, instr.String())
	}
	return lines
}

// TestMaterialiseUnreadable holds the two shapes a literal with no spelling
// takes to what the chip can run: a move becomes the arithmetic itself, and
// every other position gets it ahead of them into a register.
func TestMaterialiseUnreadable(t *testing.T) {
	nan := Imm{Value: math.NaN()}
	tests := []struct {
		name string
		// build takes the function so a case can draw virtual registers from the
		// same counter the pass draws its temporaries from.
		build func(t *testing.T, fn *Func) []*Instr
		want  []string
	}{
		{
			name: "a move of a NaN becomes the division and costs no line",
			build: func(t *testing.T, _ *Func) []*Instr {
				return []*Instr{mustInstr(t, isa.OpMove, reg(0), nan)}
			},
			want: []string{"div r0 0 0"},
		},
		{
			name: "a move of a negative zero becomes the multiplication and costs no line",
			build: func(t *testing.T, _ *Func) []*Instr {
				return []*Instr{mustInstr(t, isa.OpMove, reg(0), negativeZero)}
			},
			want: []string{"mul r0 0 -1"},
		},
		{
			name: "a move into a virtual register becomes the division too",
			build: func(t *testing.T, fn *Func) []*Instr {
				return []*Instr{mustInstr(t, isa.OpMove, fn.NewVirtReg(), nan)}
			},
			want: []string{"div vr0 0 0"},
		},
		{
			name: "a NaN read by anything else is divided into a fresh register first",
			build: func(t *testing.T, fn *Func) []*Instr {
				return []*Instr{mustInstr(t, isa.OpMax, fn.NewVirtReg(), fn.NewVirtReg(), nan)}
			},
			want: []string{"div vr2 0 0", "max vr0 vr1 vr2"},
		},
		{
			name: "a negative zero read by anything else is multiplied into a fresh register first",
			build: func(t *testing.T, fn *Func) []*Instr {
				return []*Instr{mustInstr(t, isa.OpMax, fn.NewVirtReg(), fn.NewVirtReg(), negativeZero)}
			},
			want: []string{"mul vr2 0 -1", "max vr0 vr1 vr2"},
		},
		{
			name: "two NaN operands of one instruction share one division",
			build: func(t *testing.T, fn *Func) []*Instr {
				return []*Instr{mustInstr(t, isa.OpMax, fn.NewVirtReg(), nan, nan)}
			},
			want: []string{"div vr1 0 0", "max vr0 vr1 vr1"},
		},
		{
			// The two remedies are different instructions, so sharing a register
			// between them would put one value where the other belongs.
			name: "a NaN and a negative zero in one instruction get a register each",
			build: func(t *testing.T, fn *Func) []*Instr {
				return []*Instr{mustInstr(t, isa.OpMax, fn.NewVirtReg(), nan, negativeZero)}
			},
			want: []string{"div vr1 0 0", "mul vr2 0 -1", "max vr0 vr1 vr2"},
		},
		{
			name: "a NaN written to a memory slot is divided in first",
			build: func(t *testing.T, _ *Func) []*Instr {
				return []*Instr{mustInstr(t, isa.OpPoke, Imm{Value: 4}, nan)}
			},
			want: []string{"div vr0 0 0", "poke 4 vr0"},
		},
		{
			name: "a negative zero written to a memory slot is multiplied in first",
			build: func(t *testing.T, _ *Func) []*Instr {
				return []*Instr{mustInstr(t, isa.OpPoke, Imm{Value: 4}, negativeZero)}
			},
			want: []string{"mul vr0 0 -1", "poke 4 vr0"},
		},
		{
			name: "each unreadable literal in a block gets its own arithmetic",
			build: func(t *testing.T, _ *Func) []*Instr {
				return []*Instr{
					mustInstr(t, isa.OpMove, reg(0), nan),
					mustInstr(t, isa.OpMove, reg(1), Imm{Value: 3}),
					mustInstr(t, isa.OpMove, reg(2), negativeZero),
				}
			},
			want: []string{"div r0 0 0", "move r1 3", "mul r2 0 -1"},
		},
		{
			// A positive zero is not the same value and needs nothing: the parser
			// reads "0" back as the zero the program meant.
			name: "a positive zero is left alone",
			build: func(t *testing.T, _ *Func) []*Instr {
				return []*Instr{mustInstr(t, isa.OpMove, reg(0), Imm{Value: 0})}
			},
			want: []string{"move r0 0"},
		},
		{
			// +Inf is Instr.String's diagnostic spelling. The emitter writes
			// pinf and ninf, which is what makes the infinities the case a NaN
			// is unlike: the chip has a literal for these and none for a NaN.
			name: "an infinity is left alone, the chip having a literal for it",
			build: func(t *testing.T, _ *Func) []*Instr {
				return []*Instr{
					mustInstr(t, isa.OpMove, reg(0), Imm{Value: math.Inf(1)}),
					mustInstr(t, isa.OpMove, reg(1), Imm{Value: math.Inf(-1)}),
				}
			},
			want: []string{"move r0 +Inf", "move r1 -Inf"},
		},
		{
			// The smallest subnormal is a value the chip does have a spelling
			// for: the parser's constant table carries it as epsilon, and the
			// emitter picks that name over the 326 character expansion.
			name: "the smallest subnormal is left alone",
			build: func(t *testing.T, _ *Func) []*Instr {
				return []*Instr{mustInstr(t, isa.OpMove, reg(0), Imm{Value: math.SmallestNonzeroFloat64})}
			},
			want: []string{"move r0 5e-324"},
		},
		{
			name: "a block holding nothing unreadable is untouched",
			build: func(t *testing.T, _ *Func) []*Instr {
				return []*Instr{
					mustInstr(t, isa.OpMove, reg(0), Imm{Value: 1}),
					mustInstr(t, isa.OpAdd, reg(0), reg(0), Imm{Value: 2}),
				}
			},
			want: []string{"move r0 1", "add r0 r0 2"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn := NewFunc("f", pos(1))
			block := fn.NewBlock("f.entry", pos(1))
			block.Append(tt.build(t, fn)...)
			prog := &Program{Funcs: []*Func{fn}}

			if err := MaterialiseUnreadable(prog); err != nil {
				t.Fatalf("MaterialiseUnreadable: %v", err)
			}
			if got := rendered(t, block); !slices.Equal(got, tt.want) {
				t.Errorf("MaterialiseUnreadable left\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
			}
			if err := prog.Validate(); err != nil {
				t.Errorf("the rewritten program does not validate: %v", err)
			}
		})
	}
}

// TestMaterialiseUnreadableLeavesNothingBehind runs the pass over every value
// the unreadable class names and the ones nearest it. The comparison is on bits
// rather than the value: a negative zero compares equal to a positive one, so a
// check written as == would pass on the miscompiled output.
func TestMaterialiseUnreadableLeavesNothingBehind(t *testing.T) {
	values := []float64{
		math.NaN(),
		math.Copysign(0, -1),
		0,
		math.Inf(1),
		math.Inf(-1),
		math.SmallestNonzeroFloat64,
		-math.SmallestNonzeroFloat64,
		math.MaxFloat64,
		-math.MaxFloat64,
		1,
		-1,
	}
	for _, value := range values {
		t.Run(Imm{Value: value}.String(), func(t *testing.T) {
			fn := NewFunc("f", pos(1))
			block := fn.NewBlock("f.entry", pos(1))
			block.Append(
				mustInstr(t, isa.OpMove, reg(0), Imm{Value: value}),
				mustInstr(t, isa.OpPoke, Imm{Value: 1}, Imm{Value: value}),
			)
			prog := &Program{Funcs: []*Func{fn}}

			if err := MaterialiseUnreadable(prog); err != nil {
				t.Fatalf("MaterialiseUnreadable: %v", err)
			}
			for _, instr := range block.Instrs {
				for i, arg := range instr.Args {
					imm, ok := arg.(Imm)
					if !ok {
						continue
					}
					if _, unreadable := ic10.Unreadable(imm.Value); unreadable {
						t.Errorf("%s still names at operand %d a value the chip cannot read (%016x)",
							instr, i, math.Float64bits(imm.Value))
					}
				}
			}
		})
	}
}

// TestMaterialiseUnreadableCarriesAttribution keeps the arithmetic charged to
// the construct that wanted the value: byte accounting reports per inline site,
// so an instruction attributed to nothing lands against the wrong one.
func TestMaterialiseUnreadableCarriesAttribution(t *testing.T) {
	for _, value := range []float64{math.NaN(), math.Copysign(0, -1)} {
		t.Run(Imm{Value: value}.String(), func(t *testing.T) {
			fn := NewFunc("f", pos(1))
			block := fn.NewBlock("f.entry", pos(1))
			move, err := NewInstr(isa.OpMove, pos(7), reg(0), Imm{Value: value})
			if err != nil {
				t.Fatalf("NewInstr: %v", err)
			}
			sites := []source.InlineSite{{Callee: "g", Pos: pos(3)}}
			move.Inline = sites
			block.Append(move)

			if err := MaterialiseUnreadable(&Program{Funcs: []*Func{fn}}); err != nil {
				t.Fatalf("MaterialiseUnreadable: %v", err)
			}
			computed := block.Instrs[0]
			if computed.Pos != pos(7) {
				t.Errorf("the arithmetic is at %v, want the position of the move it replaced, %v", computed.Pos, pos(7))
			}
			if !slices.Equal(computed.Inline, sites) {
				t.Errorf("the arithmetic carries inline chain %v, want %v", computed.Inline, sites)
			}
		})
	}
}

// TestMaterialiseUnreadableAcceptsANilProgram covers the one hole this pass
// answers by doing nothing, as the peephole does: nothing there to rewrite.
func TestMaterialiseUnreadableAcceptsANilProgram(t *testing.T) {
	if err := MaterialiseUnreadable(nil); err != nil {
		t.Fatalf("MaterialiseUnreadable: %v", err)
	}
}

// TestMaterialiseUnreadableRejectsANilInsideTheProgram holds the pass to
// reporting what it cannot rewrite instead of dereferencing it.
// [Program.Validate] names all three, but selection validates and this is what
// rebuilds an instruction list afterwards.
func TestMaterialiseUnreadableRejectsANilInsideTheProgram(t *testing.T) {
	tests := []struct {
		name        string
		build       func(t *testing.T) *Program
		wantMention string
	}{
		{
			name:        "nil function",
			build:       func(*testing.T) *Program { return &Program{Funcs: []*Func{nil}} },
			wantMention: "function 0 is nil",
		},
		{
			name: "nil block",
			build: func(*testing.T) *Program {
				fn := NewFunc("f", pos(1))
				fn.Blocks = append(fn.Blocks, nil)
				return &Program{Funcs: []*Func{fn}}
			},
			wantMention: "block 0 is nil",
		},
		{
			name: "nil instruction",
			build: func(t *testing.T) *Program {
				t.Helper()
				fn := NewFunc("f", pos(1))
				block := fn.NewBlock("f.entry", pos(1))
				block.Append(mustInstr(t, isa.OpMove, reg(0), Imm{Value: 1}), nil)
				return &Program{Funcs: []*Func{fn}}
			},
			wantMention: "instruction 1 is nil",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := MaterialiseUnreadable(tt.build(t))
			if err == nil {
				t.Fatalf("MaterialiseUnreadable accepted a program holding a nil, want a rejection")
			}
			if !strings.Contains(err.Error(), tt.wantMention) {
				t.Errorf("error = %q, want it to mention %q", err, tt.wantMention)
			}
		})
	}
}

// TestMaterialiseUnreadableReportsAMoveCarryingOneOperand covers the other half
// of that window. Instr.Args is public, so a move whose operand count disagrees
// with its opcode reaches this pass, and reading the second operand before the
// arity refusal ends the compiler on an index rather than naming the move.
func TestMaterialiseUnreadableReportsAMoveCarryingOneOperand(t *testing.T) {
	fn := NewFunc("main", pos(1))
	fn.NewBlock("main.entry", pos(1)).Append(&Instr{Op: isa.OpMove, Args: []Operand{Imm{Value: math.NaN()}}, Pos: pos(1)})

	err := MaterialiseUnreadable(&Program{Funcs: []*Func{fn}})
	if err == nil {
		t.Fatal("MaterialiseUnreadable accepted a move carrying one operand")
	}
	if want := "takes 2"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to mention %q", err, want)
	}
}

// TestMaterialiseUnreadableRefusesToWriteToALiteral covers the window
// [movesUnreadable] reads operand 0 for. A move built as a struct literal can
// carry an unreadable value in the position the arithmetic would write to, and
// taking the shortcut there would ask the chip to store into a number.
func TestMaterialiseUnreadableRefusesToWriteToALiteral(t *testing.T) {
	fn := NewFunc("main", pos(1))
	nan := Imm{Value: math.NaN()}
	fn.NewBlock("main.entry", pos(1)).Append(&Instr{Op: isa.OpMove, Args: []Operand{nan, nan}, Pos: pos(1)})

	err := MaterialiseUnreadable(&Program{Funcs: []*Func{fn}})
	if err == nil {
		t.Fatal("MaterialiseUnreadable accepted a move writing to a literal")
	}
	if want := "materialising the literal"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to mention %q", err, want)
	}
}

// TestMaterialiseUnreadableValidatesWhatItLeaves covers the window this pass
// opens. Validate does not hold successor edges against a [RegFormEmpty]
// function, so minting a register turns one virtual and hands an edge it never
// checked to register allocation, which reads Succs and nothing else.
func TestMaterialiseUnreadableValidatesWhatItLeaves(t *testing.T) {
	tests := []struct {
		name        string
		listTheEdge bool
		wantMention string
	}{
		{name: "the branch edge is listed", listTheEdge: true},
		{name: "the branch edge is missing", wantMention: "does not list it as a successor"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn := NewFunc("main", pos(1))
			entry := fn.NewBlock("main.entry", pos(1))
			loop := fn.NewBlock("main.loop", pos(2))
			entry.Append(mustInstr(t, isa.OpPoke, Imm{Value: 4}, Imm{Value: math.NaN()}))
			entry.Append(mustInstr(t, isa.OpJ, Label{Name: "main.loop"}))
			loop.Append(mustInstr(t, isa.OpYield))
			if tt.listTheEdge {
				entry.AddSucc(loop)
			}
			prog := &Program{Funcs: []*Func{fn}}

			if got := fn.RegForm(); got != RegFormEmpty {
				t.Fatalf("RegForm before the pass = %v, want empty: the case rests on the edge check being off", got)
			}
			if err := prog.Validate(); err != nil {
				t.Fatalf("the program does not validate before the pass, so the case proves nothing: %v", err)
			}

			err := MaterialiseUnreadable(prog)
			if got := fn.RegForm(); got != RegFormVirtual {
				t.Fatalf("RegForm after the pass = %v, want virtual", got)
			}
			if tt.wantMention == "" {
				if err != nil {
					t.Fatalf("MaterialiseUnreadable: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("MaterialiseUnreadable accepted a function it left with an unlisted edge")
			}
			if !strings.Contains(err.Error(), tt.wantMention) {
				t.Errorf("error = %q, want it to mention %q", err, tt.wantMention)
			}
		})
	}
}

// TestSameRemedyDoesNotConflateTheZeros covers the trap this pass closes, as it
// appears in the pass itself. ic10.UnreadableValue is a comparable struct, but
// == on a float field reads +0.0 and -0.0 as one value, so two remedies merged
// by it would share a register while standing for different numbers.
func TestSameRemedyDoesNotConflateTheZeros(t *testing.T) {
	positive := ic10.UnreadableValue{Op: isa.OpMul, Left: 0, Right: -1}
	negative := ic10.UnreadableValue{Op: isa.OpMul, Left: math.Copysign(0, -1), Right: -1}

	if positive != negative {
		t.Fatal("== already tells the two zeros apart, so this test is asking about nothing")
	}
	if sameRemedy(positive, negative) {
		t.Error("sameRemedy merged a remedy computing +0 with one computing -0")
	}
	if !sameRemedy(positive, positive) {
		t.Error("sameRemedy answered false for a remedy and itself")
	}
	if sameRemedy(positive, ic10.UnreadableValue{Op: isa.OpDiv, Left: 0, Right: 0}) {
		t.Error("sameRemedy merged two different instructions")
	}
}
