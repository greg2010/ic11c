package mir

import (
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
)

func TestOperandString(t *testing.T) {
	pin, err := NewDevicePin(3, NoConnection)
	if err != nil {
		t.Fatalf("NewDevicePin: %v", err)
	}

	tests := []struct {
		name string
		op   Operand
		want string
	}{
		{name: "virtual register", op: VirtReg{ID: 12}, want: "vr12"},
		{name: "general register", op: PhysReg{Reg: 7}, want: "r7"},
		{name: "stack pointer", op: PhysReg{Reg: ic10.RegSP}, want: "sp"},
		{name: "return address", op: PhysReg{Reg: ic10.RegRA}, want: "ra"},
		{name: "immediate", op: Imm{Value: 1.5}, want: "1.5"},
		{name: "label", op: Label{Name: "main.loop"}, want: "main.loop"},
		{name: "base device", op: NewDeviceBase(), want: "db"},
		{name: "device pin", op: pin, want: "d3"},
		{name: "logic type", op: LogicType{Value: 6}, want: "LogicType(6)"},
		{name: "logic slot type", op: LogicSlotType{Value: 2}, want: "LogicSlotType(2)"},
		{name: "batch mode", op: BatchMode{Value: 1}, want: "BatchMode(1)"},
		{name: "reagent mode", op: ReagentMode{Value: 3}, want: "ReagentMode(3)"},
		{name: "device kind outside the set", op: Device{Kind: DeviceKind(9)}, want: "Device(9)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.op.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestOperandSatisfies pins which instruction table positions each operand form
// may occupy. NewInstr is only as good as this mapping.
func TestOperandSatisfies(t *testing.T) {
	every := []ic10.OperandKind{
		ic10.OperandRegister, ic10.OperandNumber, ic10.OperandInteger, ic10.OperandDevice,
		ic10.OperandRefID, ic10.OperandLogicType, ic10.OperandLogicSlotType, ic10.OperandBatchMode,
		ic10.OperandReagentMode, ic10.OperandDeviceHash, ic10.OperandNameHash, ic10.OperandSlotIndex,
		ic10.OperandString,
	}
	valueKinds := []ic10.OperandKind{
		ic10.OperandNumber, ic10.OperandInteger, ic10.OperandRefID,
		ic10.OperandDeviceHash, ic10.OperandNameHash, ic10.OperandSlotIndex,
	}
	registerKinds := append([]ic10.OperandKind{ic10.OperandRegister}, valueKinds...)
	tests := []struct {
		name string
		op   Operand
		want []ic10.OperandKind
	}{
		{name: "virtual register", op: VirtReg{}, want: registerKinds},
		{name: "physical register", op: PhysReg{}, want: registerKinds},
		{name: "immediate", op: Imm{}, want: valueKinds},
		{name: "label", op: Label{}, want: []ic10.OperandKind{ic10.OperandInteger, ic10.OperandNumber}},
		{name: "device", op: NewDeviceBase(), want: []ic10.OperandKind{ic10.OperandDevice}},
		{name: "logic type", op: LogicType{}, want: []ic10.OperandKind{ic10.OperandLogicType}},
		{name: "logic slot type", op: LogicSlotType{}, want: []ic10.OperandKind{ic10.OperandLogicSlotType}},
		{name: "batch mode", op: BatchMode{}, want: []ic10.OperandKind{ic10.OperandBatchMode}},
		{name: "reagent mode", op: ReagentMode{}, want: []ic10.OperandKind{ic10.OperandReagentMode}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allowed := make(map[ic10.OperandKind]bool, len(tt.want))
			for _, kind := range tt.want {
				allowed[kind] = true
			}
			for _, kind := range every {
				if got := tt.op.Satisfies(kind); got != allowed[kind] {
					t.Errorf("Satisfies(%s) = %v, want %v", kind, got, allowed[kind])
				}
			}
		})
	}
}

// TestNoOperandSatisfiesString is what makes alias, define and label
// unrepresentable independently of the pseudo-op table: the bare word operand
// they take has no MIR form.
func TestNoOperandSatisfiesString(t *testing.T) {
	ops := []Operand{
		VirtReg{}, PhysReg{}, Imm{}, Label{},
		NewDeviceBase(), LogicType{}, LogicSlotType{}, BatchMode{}, ReagentMode{},
	}
	for _, op := range ops {
		if op.Satisfies(ic10.OperandString) {
			t.Errorf("%s satisfies OperandString", op)
		}
	}
}

func TestOperandConstructors(t *testing.T) {
	tests := []struct {
		name        string
		build       func() (Operand, error)
		wantMention string
	}{
		{
			name:  "last device pin",
			build: func() (Operand, error) { return NewDevicePin(ic10.NumDevicePins-1, NoConnection) },
		},
		{
			name:        "device pin past the last",
			build:       func() (Operand, error) { return NewDevicePin(ic10.NumDevicePins, NoConnection) },
			wantMention: "outside d0-d5",
		},
		{
			name:  "a pin through a network connection",
			build: func() (Operand, error) { return NewDevicePin(0, 1) },
		},
		{
			name:        "a negative connection index",
			build:       func() (Operand, error) { return NewDevicePin(0, -2) },
			wantMention: "which is negative",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.build()
			if tt.wantMention == "" {
				if err != nil {
					t.Fatalf("build: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("build succeeded, want an error mentioning %q", tt.wantMention)
			}
			if !strings.Contains(err.Error(), tt.wantMention) {
				t.Errorf("error = %q, want it to mention %q", err, tt.wantMention)
			}
		})
	}
}
