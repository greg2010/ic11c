package mir

import (
	"slices"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/isa"
)

// TestBranchTargetsCoversTheTable holds branchTargets against the instruction
// set rather than a reading of it, so an opcode the table gains cannot arrive
// looking like straight-line code and silently drop an edge. The expectation is
// spelled out of the machine's own mnemonic naming.
func TestBranchTargetsCoversTheTable(t *testing.T) {
	for _, info := range ic10.Instructions {
		name := info.Mnemonic
		transfers := strings.HasPrefix(name, "j") || strings.HasPrefix(name, "b")
		_, unemittable := ic10.Unemittable(info.Opcode)
		want := transfers && !unemittable && !strings.HasSuffix(name, "al")
		if got := branchTargets[info.Opcode]; got != want {
			t.Errorf("branchTargets[%s] = %v, want %v", name, got, want)
		}
	}
}

func TestBlockExits(t *testing.T) {
	tests := []struct {
		name        string
		instrs      func(t *testing.T) []*Instr
		wantTargets []string
		wantFalls   bool
	}{
		{
			name:      "an empty block falls into the next",
			instrs:    func(*testing.T) []*Instr { return nil },
			wantFalls: true,
		},
		{
			name: "straight line code falls into the next",
			instrs: func(t *testing.T) []*Instr {
				t.Helper()
				return []*Instr{mustInstr(t, isa.OpMove, reg(0), Imm{Value: 1})}
			},
			wantFalls: true,
		},
		{
			name: "an unconditional jump names its target and does not fall",
			instrs: func(t *testing.T) []*Instr {
				t.Helper()
				return []*Instr{mustInstr(t, isa.OpJ, Label{Name: "main.loop"})}
			},
			wantTargets: []string{"main.loop"},
		},
		{
			name: "a jump through a register leaves the function",
			instrs: func(t *testing.T) []*Instr {
				t.Helper()
				return []*Instr{mustInstr(t, isa.OpJ, PhysReg{Reg: ic10.RegRA})}
			},
		},
		{
			name: "a conditional branch names its target and still falls",
			instrs: func(t *testing.T) []*Instr {
				t.Helper()
				return []*Instr{mustInstr(t, isa.OpBeq, reg(0), reg(1), Label{Name: "main.taken"})}
			},
			wantTargets: []string{"main.taken"},
			wantFalls:   true,
		},
		{
			name: "a call names a callee rather than a successor",
			instrs: func(t *testing.T) []*Instr {
				t.Helper()
				return []*Instr{mustInstr(t, isa.OpJal, Label{Name: "helper.entry"})}
			},
			wantFalls: true,
		},
		{
			name: "a branch and a jump name both arms",
			instrs: func(t *testing.T) []*Instr {
				t.Helper()
				return []*Instr{
					mustInstr(t, isa.OpBeq, reg(0), reg(1), Label{Name: "main.taken"}),
					mustInstr(t, isa.OpJ, Label{Name: "main.fallen"}),
				}
			},
			wantTargets: []string{"main.taken", "main.fallen"},
		},
		{
			name:      "a nil instruction names nothing",
			instrs:    func(*testing.T) []*Instr { return []*Instr{nil} },
			wantFalls: true,
		},
		{
			// Control does not reach the move, so the block leaves through the
			// jump alone. Reading the answer off the last instruction instead
			// would demand the fallthrough chain as a successor and report a
			// block that lists the real edge as omitting one.
			name: "a jump followed by unreachable code still does not fall",
			instrs: func(t *testing.T) []*Instr {
				t.Helper()
				return []*Instr{
					mustInstr(t, isa.OpJ, Label{Name: "main.loop"}),
					mustInstr(t, isa.OpMove, reg(0), Imm{Value: 1}),
				}
			},
			wantTargets: []string{"main.loop"},
		},
		{
			name: "a branch after a jump names no target control can reach",
			instrs: func(t *testing.T) []*Instr {
				t.Helper()
				return []*Instr{
					mustInstr(t, isa.OpJ, Label{Name: "main.loop"}),
					mustInstr(t, isa.OpBeq, reg(0), reg(1), Label{Name: "main.dead"}),
				}
			},
			wantTargets: []string{"main.loop"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			block := &Block{Label: "main.entry", Instrs: tt.instrs(t)}
			targets, falls := blockExits(block)
			if !slices.Equal(targets, tt.wantTargets) {
				t.Errorf("blockExits() targets = %v, want %v", targets, tt.wantTargets)
			}
			if falls != tt.wantFalls {
				t.Errorf("blockExits() falls = %v, want %v", falls, tt.wantFalls)
			}
		})
	}
}
