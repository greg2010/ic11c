package emit

import (
	"context"
	"errors"
	"math"
	"strconv"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/mir"
	"github.com/greg2010/ic11c/internal/vm"
)

func lookupLogicType(t *testing.T, name string) ic10.LogicType {
	t.Helper()
	info, ok := ic10.LookupLogicType(name)
	if !ok {
		t.Fatalf("LookupLogicType(%q) found nothing", name)
	}
	return info.Value
}

// TestRenderOperand covers every operand form in both modes. The numeric
// spelling is what ships; the named one is the readable flag.
func TestRenderOperand(t *testing.T) {
	temperature := lookupLogicType(t, "Temperature")
	slotType := ic10.LogicSlotTypes[2]
	pin, err := mir.NewDevicePin(1, mir.NoConnection)
	if err != nil {
		t.Fatalf("NewDevicePin: %v", err)
	}
	connected, err := mir.NewDevicePin(3, 1)
	if err != nil {
		t.Fatalf("NewDevicePin with a connection: %v", err)
	}

	render := renderer{
		lineOf: map[string]int{"main.loop": 5},
		names:  map[string]string{"main.loop": "main_loop"},
	}

	tests := []struct {
		name string
		op   mir.Operand
		// wantDefault is the shipped form, wantReadable the one with labels
		// resolved symbolically, and wantNumeric the one with every machine
		// name written as the integer behind it.
		wantDefault  string
		wantReadable string
		wantNumeric  string
	}{
		{name: "general register", op: mir.PhysReg{Reg: 3}, wantDefault: "r3", wantReadable: "r3", wantNumeric: "r3"},
		{name: "stack pointer", op: mir.PhysReg{Reg: ic10.RegSP}, wantDefault: "sp", wantReadable: "sp", wantNumeric: "sp"},
		{name: "return address", op: mir.PhysReg{Reg: ic10.RegRA}, wantDefault: "ra", wantReadable: "ra", wantNumeric: "ra"},
		{name: "immediate", op: mir.Imm{Value: 42}, wantDefault: "42", wantReadable: "42", wantNumeric: "42"},
		{name: "label", op: mir.Label{Name: "main.loop"}, wantDefault: "5", wantReadable: "main_loop", wantNumeric: "5"},
		{name: "base device", op: mir.NewDeviceBase(), wantDefault: "db", wantReadable: "db", wantNumeric: "db"},
		{name: "device pin", op: pin, wantDefault: "d1", wantReadable: "d1", wantNumeric: "d1"},
		{
			// The network connection form, which reaches a device the pin's own
			// device is connected to rather than the one on the pin.
			name:         "device pin through a network connection",
			op:           connected,
			wantDefault:  "d3:1",
			wantReadable: "d3:1",
			wantNumeric:  "d3:1",
		},
		{
			name:         "a fractional immediate",
			op:           mir.Imm{Value: 293.15},
			wantDefault:  "293.15",
			wantReadable: "293.15",
			wantNumeric:  "293.15",
		},
		{
			// The emitter never produces exponential notation: the chip's own
			// number parser reads none, so a small magnitude expands in full.
			name:         "a small fractional immediate expands rather than taking an exponent",
			op:           mir.Imm{Value: 0.001},
			wantDefault:  "0.001",
			wantReadable: "0.001",
			wantNumeric:  "0.001",
		},
		{
			name:         "logic type",
			op:           mir.LogicType{Value: temperature},
			wantDefault:  "Temperature",
			wantReadable: "Temperature",
			wantNumeric:  strconv.FormatUint(uint64(temperature), 10),
		},
		{
			name:         "logic slot type",
			op:           mir.LogicSlotType{Value: slotType.Value},
			wantDefault:  slotType.Name,
			wantReadable: slotType.Name,
			wantNumeric:  strconv.FormatUint(uint64(slotType.Value), 10),
		},
		{name: "batch mode", op: mir.BatchMode{Value: 1}, wantDefault: "Sum", wantReadable: "Sum", wantNumeric: "1"},
		{
			name:         "reagent mode",
			op:           mir.ReagentMode{Value: 3},
			wantDefault:  "TotalContents",
			wantReadable: "TotalContents",
			wantNumeric:  "3",
		},
		{
			name:         "enum value with no name falls back to the number",
			op:           mir.BatchMode{Value: 200},
			wantDefault:  "200",
			wantReadable: "200",
			wantNumeric:  "200",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			render.readable = false
			got, err := render.operand(tt.op)
			if err != nil {
				t.Fatalf("operand(%s): %v", tt.op, err)
			}
			if got != tt.wantDefault {
				t.Errorf("operand(%s) = %q, want %q", tt.op, got, tt.wantDefault)
			}
			render.readable = true
			got, err = render.operand(tt.op)
			if err != nil {
				t.Fatalf("readable operand(%s): %v", tt.op, err)
			}
			if got != tt.wantReadable {
				t.Errorf("readable operand(%s) = %q, want %q", tt.op, got, tt.wantReadable)
			}
			render.readable, render.numeric = false, true
			got, err = render.operand(tt.op)
			if err != nil {
				t.Fatalf("numeric operand(%s): %v", tt.op, err)
			}
			if got != tt.wantNumeric {
				t.Errorf("numeric operand(%s) = %q, want %q", tt.op, got, tt.wantNumeric)
			}
			render.numeric = false
		})
	}
}

func TestRenderOperandErrors(t *testing.T) {
	render := renderer{lineOf: map[string]int{"known": 1}, names: map[string]string{"known": "known"}}
	tests := []struct {
		name string
		op   mir.Operand
		want error
	}{
		{name: "virtual register", op: mir.VirtReg{ID: 4}, want: ErrVirtualRegister},
		{name: "label naming no block", op: mir.Label{Name: "nowhere"}, want: ErrUnresolvedLabel},
		// The chip's operand pattern for a device admits d0 through d9 and a
		// housing has six pins, so d6 assembles and then faults once per tick.
		{name: "device pin the housing does not have", op: mir.Device{Kind: mir.DevicePin, Pin: 6}, want: ErrUnspellableOperand},
		{name: "device kind with no spelling", op: mir.Device{Kind: mir.DeviceKind(9)}, want: ErrUnspellableOperand},
		{name: "register outside the file", op: mir.PhysReg{Reg: ic10.NumRegisters}, want: ErrUnspellableOperand},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := render.operand(tt.op)
			if !errors.Is(err, tt.want) {
				t.Fatalf("operand(%s) = %q, %v, want %v", tt.op, got, err, tt.want)
			}
		})
	}
}

// TestFormatImm pins the literal spellings and loads each one back through the
// interpreter. Exponent notation is never produced; a named constant wins only
// when it is shorter than the decimal expansion, which is the whole reason to
// prefer it.
func TestFormatImm(t *testing.T) {
	tests := []struct {
		name    string
		value   float64
		want    string
		wantErr error
	}{
		{name: "zero", value: 0, want: "0"},
		{name: "negative zero", value: math.Copysign(0, -1), want: "-0"},
		{name: "integer", value: 42, want: "42"},
		{name: "negative integer", value: -7, want: "-7"},
		{name: "fraction", value: 1.5, want: "1.5"},
		{name: "prefab hash", value: -1252983604, want: "-1252983604"},
		{name: "large integer stays decimal", value: 1e21, want: "1000000000000000000000"},
		{name: "small fraction stays decimal", value: 0.001, want: "0.001"},
		{name: "not a number is unspellable", value: math.NaN(), wantErr: ErrUnmaterialisedNaN},
		{name: "positive infinity", value: math.Inf(1), want: "pinf"},
		{name: "negative infinity", value: math.Inf(-1), want: "ninf"},
		{name: "pi", value: math.Pi, want: "pi"},
		{name: "tau", value: 2 * math.Pi, want: "tau"},
		{name: "gas constant", value: 8.31446261815324, want: "rgas"},
		{name: "smallest subnormal", value: 5e-324, want: "epsilon"},
		{name: "degrees to radians as the game stores it", value: 0.01745329238474369, want: "deg2rad"},
		{name: "degrees to radians at full precision is not the game's", value: math.Pi / 180, want: "0.017453292519943295"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := formatImm(tt.value)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("formatImm(%v) = %q, %v, want %v", tt.value, got, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("formatImm(%v): %v", tt.value, err)
			}
			if got != tt.want {
				t.Fatalf("formatImm(%v) = %q, want %q", tt.value, got, tt.want)
			}
			assertLoadsAs(t, got, tt.value)
		})
	}
}

// assertLoadsAs fails unless the chip's own operand parser reads text back as
// the value it was formatted from, bit for bit.
//
// The oracle is the interpreter rather than strconv.ParseFloat because the chip
// reads a narrower number syntax than Go does: the game parses an operand with
// NumberStyles.Number, which admits no exponent, no hexadecimal and no spelling
// of an infinity or a NaN. Rounding a literal through Go's parser would accept
// spellings the chip's assembler refuses, which is the one property
// [formatImm]'s contract rests on. Loading it as an instruction operand also
// runs the constant table and the machine's own resolution order, so a named
// constant and a decimal expansion are checked by the same means.
//
// Bits rather than values: -0 and 0 compare equal as doubles, and which of the
// two the formatter was asked for is the whole question for that row.
func assertLoadsAs(t *testing.T, text string, want float64) {
	t.Helper()
	ctx := context.Background()
	src := "move r0 " + text
	m := vm.NewMachine()
	if err := m.Load(ctx, src); err != nil {
		t.Fatalf("Load(%q): %v", src, err)
	}
	executed, err := m.Tick(ctx, 1)
	if err != nil {
		t.Fatalf("running %q: %v", src, err)
	}
	if executed != 1 {
		t.Fatalf("running %q executed %d instructions, want 1", src, executed)
	}
	got := m.Register(0)
	if math.Float64bits(got) != math.Float64bits(want) {
		t.Errorf("%q loads as %v (%#016x), want %v (%#016x)",
			text, got, math.Float64bits(got), want, math.Float64bits(want))
	}
}

func TestRenderInstr(t *testing.T) {
	render := renderer{lineOf: map[string]int{"loop": 9}, names: map[string]string{"loop": "loop"}}
	tests := []struct {
		name string
		op   ic10.Opcode
		args []mir.Operand
		want string
	}{
		{name: "no operands", op: ic10.OpYield, want: "yield"},
		{name: "three operands", op: ic10.OpAdd, args: []mir.Operand{mir.PhysReg{Reg: 0}, mir.PhysReg{Reg: 1}, mir.Imm{Value: 2}}, want: "add r0 r1 2"},
		{name: "branch to a label", op: ic10.OpJ, args: []mir.Operand{mir.Label{Name: "loop"}}, want: "j 9"},
		{name: "return through ra", op: ic10.OpJ, args: []mir.Operand{mir.PhysReg{Reg: ic10.RegRA}}, want: "j ra"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instr, err := mir.NewInstr(tt.op, position(1), tt.args...)
			if err != nil {
				t.Fatalf("NewInstr: %v", err)
			}
			got, err := render.instr(instr)
			if err != nil {
				t.Fatalf("instr: %v", err)
			}
			if got != tt.want {
				t.Errorf("instr = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestRenderRefusesALabelWithNoEmittedName covers the readable form's second
// label table.
//
// A label resolves through two maps and either can be missing. The line table is
// checked and the name table was not, so a label the mangler never saw rendered
// as an empty operand: a line one token short, which the chip's parser reads as
// a different instruction rather than as a failure.
func TestRenderRefusesALabelWithNoEmittedName(t *testing.T) {
	render := renderer{readable: true, lineOf: map[string]int{"known": 1, "unmangled": 2}}
	tests := []struct {
		name  string
		label string
		want  string
	}{
		{name: "a label neither table holds", label: "nowhere"},
		{name: "a label only the line table holds", label: "unmangled"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := render.operand(mir.Label{Name: tt.label})
			if !errors.Is(err, ErrUnresolvedLabel) {
				t.Fatalf("operand(%s) = %q, %v, want %v", tt.label, got, err, ErrUnresolvedLabel)
			}
			if got != tt.want {
				t.Errorf("operand(%s) rendered %q alongside its error, want %q", tt.label, got, tt.want)
			}
		})
	}
}
