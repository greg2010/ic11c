package mir

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/source"
)

func pos(line int) source.Position {
	return source.Position{File: "t.mc", Offset: line, Line: line, Column: 1}
}

func mustInstr(t *testing.T, op ic10.Opcode, args ...Operand) *Instr {
	t.Helper()
	instr, err := NewInstr(op, pos(1), args...)
	if err != nil {
		t.Fatalf("NewInstr(%v): %v", op, err)
	}
	return instr
}

func reg(n uint8) PhysReg { return PhysReg{Reg: ic10.Register(n)} }

// TestNewInstrRejectsUnemittable is the reason construction validates at all:
// an instruction the chip cannot assemble, or one that silently miscompiles,
// must not exist in the IR to be caught later.
func TestNewInstrRejectsUnemittable(t *testing.T) {
	tests := []struct {
		name        string
		op          ic10.Opcode
		args        []Operand
		wantMention string
	}{
		{name: "brapz arity defect", op: ic10.OpBrapz, args: []Operand{reg(0), reg(1), Imm{}}, wantMention: "uncompilable"},
		{name: "brnaz arity defect", op: ic10.OpBrnaz, args: []Operand{reg(0), reg(1), Imm{}}, wantMention: "uncompilable"},
		{name: "bapzal arity defect", op: ic10.OpBapzal, args: []Operand{reg(0), reg(1), Imm{}}, wantMention: "uncompilable"},
		{name: "bnazal arity defect", op: ic10.OpBnazal, args: []Operand{reg(0), reg(1), Imm{}}, wantMention: "uncompilable"},
		{name: "sla duplicates sll", op: ic10.OpSla, args: []Operand{reg(0), reg(1), Imm{Value: 2}}, wantMention: "sll"},
		{name: "relative branch", op: ic10.OpBreq, args: []Operand{reg(0), reg(1), Imm{}}, wantMention: "line offset"},
		{name: "relative jump", op: ic10.OpJr, args: []Operand{Imm{}}, wantMention: "line offset"},
		{name: "hcf", op: ic10.OpHcf, wantMention: "destroys the chip"},
		{name: "alias directive", op: ic10.OpAlias, wantMention: "execution slot"},
		{name: "define directive", op: ic10.OpDefine, wantMention: "execution slot"},
		{name: "label directive", op: ic10.OpLabel, wantMention: "computes nothing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instr, err := NewInstr(tt.op, pos(1), tt.args...)
			if err == nil {
				t.Fatalf("NewInstr(%v) built %s, want a rejection", tt.op, instr)
			}
			if !errors.Is(err, ErrUnemittable) {
				t.Errorf("NewInstr(%v) error = %v, want ErrUnemittable", tt.op, err)
			}
			if !strings.Contains(err.Error(), tt.wantMention) {
				t.Errorf("NewInstr(%v) error = %q, want it to mention %q", tt.op, err, tt.wantMention)
			}
		})
	}
}

// TestNewInstrRejectsEveryUnemittableOpcode catches an opcode ic10 adds to the
// unemittable table that this package would otherwise let through.
func TestNewInstrRejectsEveryUnemittableOpcode(t *testing.T) {
	for _, instruction := range ic10.Instructions {
		if _, bad := ic10.Unemittable(instruction.Opcode); !bad {
			continue
		}
		args := make([]Operand, len(instruction.Operands))
		for i := range args {
			args[i] = Imm{}
		}
		if _, err := NewInstr(instruction.Opcode, pos(1), args...); !errors.Is(err, ErrUnemittable) {
			t.Errorf("NewInstr(%s) error = %v, want ErrUnemittable", instruction.Mnemonic, err)
		}
	}
}

func TestNewInstrValidation(t *testing.T) {
	logicType := LogicType{Value: 6}
	tests := []struct {
		name    string
		op      ic10.Opcode
		args    []Operand
		wantErr error
	}{
		{name: "add accepts registers", op: ic10.OpAdd, args: []Operand{reg(0), reg(1), reg(2)}},
		{name: "add accepts an immediate", op: ic10.OpAdd, args: []Operand{reg(0), reg(1), Imm{Value: 3}}},
		{name: "add accepts a virtual register", op: ic10.OpAdd, args: []Operand{VirtReg{ID: 0}, VirtReg{ID: 1}, Imm{}}},
		{name: "yield takes nothing", op: ic10.OpYield},
		{name: "l takes a device and a logic type", op: ic10.OpL, args: []Operand{reg(0), NewDeviceBase(), logicType}},
		{name: "j takes a label", op: ic10.OpJ, args: []Operand{Label{Name: "loop"}}},
		{name: "j takes a line number", op: ic10.OpJ, args: []Operand{Imm{Value: 12}}},
		{name: "j takes a register, which is how a call returns", op: ic10.OpJ, args: []Operand{PhysReg{Reg: ic10.RegRA}}},
		{name: "a slot index may be computed", op: ic10.OpLs, args: []Operand{reg(0), NewDeviceBase(), reg(1), LogicSlotType{Value: 2}}},
		{name: "clr takes a device", op: ic10.OpClr, args: []Operand{NewDeviceBase()}},

		{name: "add with too few operands", op: ic10.OpAdd, args: []Operand{reg(0), reg(1)}, wantErr: ErrArity},
		{name: "add with too many operands", op: ic10.OpAdd, args: []Operand{reg(0), reg(1), reg(2), reg(3)}, wantErr: ErrArity},
		{name: "yield with an operand", op: ic10.OpYield, args: []Operand{reg(0)}, wantErr: ErrArity},
		{name: "add with a device operand", op: ic10.OpAdd, args: []Operand{reg(0), reg(1), NewDeviceBase()}, wantErr: ErrOperandKind},
		{name: "l with a number for a logic type", op: ic10.OpL, args: []Operand{reg(0), NewDeviceBase(), Imm{Value: 6}}, wantErr: ErrOperandKind},
		{name: "l with a logic type for a register", op: ic10.OpL, args: []Operand{logicType, NewDeviceBase(), logicType}, wantErr: ErrOperandKind},
		{name: "clr with a register", op: ic10.OpClr, args: []Operand{reg(0)}, wantErr: ErrOperandKind},
		{name: "a label in a device position", op: ic10.OpClr, args: []Operand{Label{Name: "loop"}}, wantErr: ErrOperandKind},
		{name: "a label is a line number, so move accepts one", op: ic10.OpMove, args: []Operand{reg(0), Label{Name: "loop"}}},
		{name: "opcode past the table", op: ic10.Opcode(len(ic10.Instructions)), wantErr: ErrUnknownOpcode},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instr, err := NewInstr(tt.op, pos(7), tt.args...)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("NewInstr error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewInstr: %v", err)
			}
			if instr.Op != tt.op {
				t.Errorf("Op = %v, want %v", instr.Op, tt.op)
			}
			if instr.Pos != pos(7) {
				t.Errorf("Pos = %v, want %v", instr.Pos, pos(7))
			}
			if len(instr.Args) != len(tt.args) {
				t.Errorf("len(Args) = %d, want %d", len(instr.Args), len(tt.args))
			}
		})
	}
}

func TestNewInstrRejectsNilOperand(t *testing.T) {
	if _, err := NewInstr(ic10.OpMove, pos(1), reg(0), nil); err == nil {
		t.Fatal("NewInstr accepted a nil operand")
	}
}

// TestNewInstrCopiesArgs keeps a caller's scratch slice from aliasing the
// instruction, which register allocation would then rewrite through.
func TestNewInstrCopiesArgs(t *testing.T) {
	args := []Operand{reg(0), Imm{Value: 1}}
	instr := mustInstr(t, ic10.OpMove, args...)
	args[0] = reg(9)
	if got := instr.Args[0]; got != Operand(reg(0)) {
		t.Errorf("Args[0] = %s, want r0", got)
	}
}

func TestInstrString(t *testing.T) {
	instr := mustInstr(t, ic10.OpAdd, reg(0), VirtReg{ID: 3}, Imm{Value: 2})
	if got, want := instr.String(), "add r0 vr3 2"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestRegForm(t *testing.T) {
	tests := []struct {
		name  string
		build func(t *testing.T, block *Block)
		want  RegForm
	}{
		{
			name:  "no registers",
			build: func(t *testing.T, b *Block) { t.Helper(); b.Append(mustInstr(t, ic10.OpYield)) },
			want:  RegFormEmpty,
		},
		{
			name: "virtual only",
			build: func(t *testing.T, b *Block) {
				t.Helper()
				b.Append(mustInstr(t, ic10.OpAdd, VirtReg{ID: 0}, VirtReg{ID: 1}, Imm{Value: 1}))
			},
			want: RegFormVirtual,
		},
		{
			name: "physical only",
			build: func(t *testing.T, b *Block) {
				t.Helper()
				b.Append(mustInstr(t, ic10.OpAdd, reg(0), reg(1), Imm{Value: 1}))
			},
			want: RegFormPhysical,
		},
		{
			name: "mixed",
			build: func(t *testing.T, b *Block) {
				t.Helper()
				b.Append(mustInstr(t, ic10.OpAdd, reg(0), reg(1), Imm{Value: 1}))
				b.Append(mustInstr(t, ic10.OpMove, VirtReg{ID: 0}, Imm{Value: 2}))
			},
			want: RegFormMixed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn := NewFunc("f", pos(1))
			block := fn.NewBlock("f.entry", pos(1))
			tt.build(t, block)
			if got := fn.RegForm(); got != tt.want {
				t.Errorf("RegForm() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRegFormString(t *testing.T) {
	tests := []struct {
		form RegForm
		want string
	}{
		{form: RegFormEmpty, want: "empty"},
		{form: RegFormVirtual, want: "virtual"},
		{form: RegFormPhysical, want: "physical"},
		{form: RegFormMixed, want: "mixed"},
		{form: RegForm(9), want: "RegForm(9)"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.form.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewVirtRegIsUnique(t *testing.T) {
	fn := NewFunc("f", pos(1))
	seen := make(map[uint32]bool)
	for range 8 {
		v := fn.NewVirtReg()
		if seen[v.ID] {
			t.Fatalf("NewVirtReg handed out %s twice", v)
		}
		seen[v.ID] = true
	}
}

func TestAllInstrsWalksLayoutOrder(t *testing.T) {
	fn := NewFunc("f", pos(1))
	first := fn.NewBlock("f.entry", pos(1))
	second := fn.NewBlock("f.loop", pos(2))
	first.Append(mustInstr(t, ic10.OpMove, reg(0), Imm{Value: 1}))
	first.Append(mustInstr(t, ic10.OpJ, Label{Name: "f.loop"}))
	second.Append(mustInstr(t, ic10.OpYield))

	var got []string
	for block, instr := range fn.AllInstrs() {
		got = append(got, block.Label+"/"+instr.Mnemonic())
	}
	want := []string{"f.entry/move", "f.entry/j", "f.loop/yield"}
	if !slices.Equal(got, want) {
		t.Errorf("AllInstrs() = %v, want %v", got, want)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name        string
		build       func(t *testing.T) *Program
		wantMention string
	}{
		{
			name: "well formed",
			build: func(t *testing.T) *Program {
				t.Helper()
				fn := NewFunc("main", pos(1))
				entry := fn.NewBlock("main.entry", pos(1))
				loop := fn.NewBlock("main.loop", pos(2))
				entry.Append(mustInstr(t, ic10.OpJ, Label{Name: "main.loop"}))
				entry.AddSucc(loop)
				loop.Append(mustInstr(t, ic10.OpYield))
				return &Program{Funcs: []*Func{fn}}
			},
		},
		{
			name:        "no functions",
			build:       func(*testing.T) *Program { return &Program{} },
			wantMention: "no functions",
		},
		{
			name: "unnamed function",
			build: func(t *testing.T) *Program {
				t.Helper()
				fn := NewFunc("", pos(1))
				fn.NewBlock("entry", pos(1)).Append(mustInstr(t, ic10.OpYield))
				return &Program{Funcs: []*Func{fn}}
			},
			wantMention: "has no name",
		},
		{
			name: "function with no blocks",
			build: func(*testing.T) *Program {
				return &Program{Funcs: []*Func{NewFunc("main", pos(1))}}
			},
			wantMention: "no blocks",
		},
		{
			name: "unlabelled block",
			build: func(t *testing.T) *Program {
				t.Helper()
				fn := NewFunc("main", pos(1))
				fn.NewBlock("", pos(1)).Append(mustInstr(t, ic10.OpYield))
				return &Program{Funcs: []*Func{fn}}
			},
			wantMention: "no label",
		},
		{
			name: "duplicate label across functions",
			build: func(t *testing.T) *Program {
				t.Helper()
				first := NewFunc("main", pos(1))
				first.NewBlock("entry", pos(1)).Append(mustInstr(t, ic10.OpYield))
				second := NewFunc("helper", pos(2))
				second.NewBlock("entry", pos(2)).Append(mustInstr(t, ic10.OpYield))
				return &Program{Funcs: []*Func{first, second}}
			},
			wantMention: "already defined",
		},
		{
			name: "successor from another function",
			build: func(t *testing.T) *Program {
				t.Helper()
				first := NewFunc("main", pos(1))
				entry := first.NewBlock("main.entry", pos(1))
				entry.Append(mustInstr(t, ic10.OpYield))
				second := NewFunc("helper", pos(2))
				foreign := second.NewBlock("helper.entry", pos(2))
				foreign.Append(mustInstr(t, ic10.OpYield))
				entry.AddSucc(foreign)
				return &Program{Funcs: []*Func{first, second}}
			},
			wantMention: "not a block of function",
		},
		{
			name: "nil instruction",
			build: func(*testing.T) *Program {
				fn := NewFunc("main", pos(1))
				fn.NewBlock("main.entry", pos(1)).Append(nil)
				return &Program{Funcs: []*Func{fn}}
			},
			wantMention: "nil instruction",
		},
		{
			name: "nil operand",
			build: func(*testing.T) *Program {
				fn := NewFunc("main", pos(1))
				fn.NewBlock("main.entry", pos(1)).Append(&Instr{Op: ic10.OpMove, Args: []Operand{nil, nil}, Pos: pos(1)})
				return &Program{Funcs: []*Func{fn}}
			},
			wantMention: "nil operand",
		},
		{
			name: "opcode outside the table",
			build: func(*testing.T) *Program {
				fn := NewFunc("main", pos(1))
				fn.NewBlock("main.entry", pos(1)).Append(&Instr{Op: ic10.Opcode(len(ic10.Instructions)), Pos: pos(1)})
				return &Program{Funcs: []*Func{fn}}
			},
			wantMention: "not in the instruction table",
		},
		{
			name: "too few operands",
			build: func(*testing.T) *Program {
				fn := NewFunc("main", pos(1))
				fn.NewBlock("main.entry", pos(1)).Append(&Instr{Op: ic10.OpAdd, Args: []Operand{reg(0), reg(1)}, Pos: pos(1)})
				return &Program{Funcs: []*Func{fn}}
			},
			wantMention: "takes 3",
		},
		{
			name: "too many operands",
			build: func(*testing.T) *Program {
				fn := NewFunc("main", pos(1))
				fn.NewBlock("main.entry", pos(1)).Append(&Instr{Op: ic10.OpMove, Args: []Operand{reg(0), reg(1), reg(2)}, Pos: pos(1)})
				return &Program{Funcs: []*Func{fn}}
			},
			wantMention: "takes 2",
		},
		{
			name: "operand kind the position does not accept",
			build: func(*testing.T) *Program {
				fn := NewFunc("main", pos(1))
				fn.NewBlock("main.entry", pos(1)).Append(&Instr{Op: ic10.OpAdd, Args: []Operand{reg(0), reg(1), NewDeviceBase()}, Pos: pos(1)})
				return &Program{Funcs: []*Func{fn}}
			},
			wantMention: "operand 2",
		},
		{
			name: "a register rewritten into a device",
			build: func(t *testing.T) *Program {
				t.Helper()
				fn := NewFunc("main", pos(1))
				instr := mustInstr(t, ic10.OpAdd, reg(0), reg(1), reg(2))
				// The window Args is public for: register allocation rewrites
				// operands in place, and construction cannot see what it wrote.
				instr.Args[0] = NewDeviceBase()
				fn.NewBlock("main.entry", pos(1)).Append(instr)
				return &Program{Funcs: []*Func{fn}}
			},
			wantMention: "operand 0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.build(t).Validate()
			if tt.wantMention == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() = nil, want an error mentioning %q", tt.wantMention)
			}
			if !strings.Contains(err.Error(), tt.wantMention) {
				t.Errorf("Validate() = %q, want it to mention %q", err, tt.wantMention)
			}
		})
	}
}

func TestValidateNilProgram(t *testing.T) {
	var prog *Program
	if err := prog.Validate(); err == nil {
		t.Fatal("Validate() on a nil program returned nil")
	}
}

// TestCheckPlacement fixes what "line 0" means. Line 0 is the first instruction
// the emitted sequence holds, wherever it came from, so anything ahead of a
// sleep clears it — including the prologue, which no program is guaranteed.
func TestCheckPlacement(t *testing.T) {
	sleep := func(t *testing.T) *Instr {
		t.Helper()
		return mustInstr(t, ic10.OpSleep, Imm{Value: 1})
	}
	tests := []struct {
		name        string
		build       func(t *testing.T) *Program
		wantMention string
	}{
		{
			name: "sleep with nothing ahead of it",
			build: func(t *testing.T) *Program {
				t.Helper()
				fn := NewFunc("main", pos(1))
				fn.NewBlock("main.entry", pos(1)).Append(sleep(t), mustInstr(t, ic10.OpYield))
				return &Program{Funcs: []*Func{fn}}
			},
			wantMention: "first instruction",
		},
		{
			name: "the zeroing prologue takes line 0",
			build: func(t *testing.T) *Program {
				t.Helper()
				fn := NewFunc("main", pos(1))
				fn.NewBlock("main.entry", pos(1)).Append(mustInstr(t, ic10.OpClr, NewDeviceBase()), sleep(t))
				return &Program{Funcs: []*Func{fn}}
			},
		},
		{
			name: "the stack pointer initialization takes line 0",
			build: func(t *testing.T) *Program {
				t.Helper()
				fn := NewFunc("main", pos(1))
				set := mustInstr(t, ic10.OpMove, PhysReg{Reg: ic10.RegSP}, Imm{Value: 64})
				fn.NewBlock("main.entry", pos(1)).Append(set, sleep(t))
				return &Program{Funcs: []*Func{fn}}
			},
		},
		{
			name: "an empty leading block does not occupy a line",
			build: func(t *testing.T) *Program {
				t.Helper()
				fn := NewFunc("main", pos(1))
				fn.NewBlock("main.entry", pos(1))
				fn.NewBlock("main.body", pos(2)).Append(sleep(t))
				return &Program{Funcs: []*Func{fn}}
			},
			wantMention: "first instruction",
		},
		{
			name: "a function that emitted nothing does not occupy a line",
			build: func(t *testing.T) *Program {
				t.Helper()
				empty := NewFunc("main", pos(1))
				empty.NewBlock("main.entry", pos(1))
				helper := NewFunc("helper", pos(2))
				helper.NewBlock("helper.entry", pos(2)).Append(sleep(t))
				return &Program{Funcs: []*Func{empty, helper}}
			},
			wantMention: "first instruction",
		},
		{
			name: "sleep in a later function is placement independent",
			build: func(t *testing.T) *Program {
				t.Helper()
				entry := NewFunc("main", pos(1))
				entry.NewBlock("main.entry", pos(1)).Append(mustInstr(t, ic10.OpYield))
				helper := NewFunc("helper", pos(2))
				helper.NewBlock("helper.entry", pos(2)).Append(sleep(t))
				return &Program{Funcs: []*Func{entry, helper}}
			},
		},
		{
			name: "a program holding no instructions",
			build: func(t *testing.T) *Program {
				t.Helper()
				fn := NewFunc("main", pos(1))
				fn.NewBlock("main.entry", pos(1))
				return &Program{Funcs: []*Func{fn}}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.build(t).CheckPlacement()
			if tt.wantMention == "" {
				if err != nil {
					t.Fatalf("CheckPlacement() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("CheckPlacement() = nil, want an error mentioning %q", tt.wantMention)
			}
			if !strings.Contains(err.Error(), tt.wantMention) {
				t.Errorf("CheckPlacement() = %q, want it to mention %q", err, tt.wantMention)
			}
			if !strings.Contains(err.Error(), "sleep") {
				t.Errorf("CheckPlacement() = %q, want it to name the instruction", err)
			}
			if !strings.Contains(err.Error(), pos(1).String()) {
				t.Errorf("CheckPlacement() = %q, want it to carry the source position", err)
			}
		})
	}
}

func TestCheckPlacementNilProgram(t *testing.T) {
	var prog *Program
	if err := prog.CheckPlacement(); err != nil {
		t.Fatalf("CheckPlacement() on a nil program = %v, want nil", err)
	}
}
