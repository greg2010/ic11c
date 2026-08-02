package ic10

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

// TestTableSizes pins the shape of the target machine. A change here means the
// game changed, and the compiler's assumptions have to be re-derived rather
// than the numbers updated.
func TestTableSizes(t *testing.T) {
	tests := []struct {
		name string
		got  int
		want int
	}{
		{name: "instructions", got: len(Instructions), want: 154},
		{name: "deprecated instructions", got: countInstructions(func(i Instruction) bool { return i.Deprecated }), want: 5},
		{name: "logic types", got: len(LogicTypes), want: 358},
		{name: "deprecated logic types", got: countLogicTypes(func(l LogicTypeInfo) bool { return l.Deprecated }), want: 23},
		{name: "logic slot types", got: len(LogicSlotTypes), want: 33},
		{name: "batch modes", got: len(BatchModes), want: 5},
		{name: "reagent modes", got: len(ReagentModes), want: 4},
		{name: "constants", got: len(Constants), want: 9},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("%s = %d, want %d", tt.name, tt.got, tt.want)
			}
		})
	}
}

// TestOperandEnumWidths pins the integer each operand enum is carried in.
// Only LogicType and LogicSlotType narrow; batch method and reagent mode take
// the full int range. A regeneration that narrowed a mode to a byte would fold
// 256, which the game leaves undefined, onto mode 0, which is defined — the
// program then reads the wrong aggregate for good and nothing faults.
func TestOperandEnumWidths(t *testing.T) {
	tests := []struct {
		name string
		got  reflect.Kind
		want reflect.Kind
	}{
		{name: "LogicType", got: reflect.TypeFor[LogicType]().Kind(), want: reflect.Uint16},
		{name: "LogicSlotType", got: reflect.TypeFor[LogicSlotType]().Kind(), want: reflect.Uint8},
		{name: "BatchMode", got: reflect.TypeFor[BatchMode]().Kind(), want: reflect.Int32},
		{name: "ReagentMode", got: reflect.TypeFor[ReagentMode]().Kind(), want: reflect.Int32},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("%s is backed by %s, want %s", tt.name, tt.got, tt.want)
			}
		})
	}
}

func TestDeprecatedInstructions(t *testing.T) {
	want := map[string]bool{"label": true, "getd": true, "putd": true, "ld": true, "sd": true}
	for _, instruction := range Instructions {
		if instruction.Deprecated != want[instruction.Mnemonic] {
			t.Errorf("%s deprecated = %v, want %v", instruction.Mnemonic, instruction.Deprecated, want[instruction.Mnemonic])
		}
	}
}

// TestInstructionsAreIndexedByOpcode is what lets Opcode.Instruction be a
// slice index rather than a search.
func TestInstructionsAreIndexedByOpcode(t *testing.T) {
	for i, instruction := range Instructions {
		if int(instruction.Opcode) != i {
			t.Errorf("Instructions[%d] is %s with opcode %d", i, instruction.Mnemonic, instruction.Opcode)
		}
	}
}

func TestOpcodeInstruction(t *testing.T) {
	tests := []struct {
		name     string
		op       Opcode
		wantOK   bool
		mnemonic string
	}{
		{name: "first", op: 0, wantOK: true, mnemonic: "l"},
		{name: "add", op: OpAdd, wantOK: true, mnemonic: "add"},
		{name: "last", op: Opcode(len(Instructions) - 1), wantOK: true, mnemonic: "ror"},
		{name: "past the end", op: Opcode(len(Instructions)), wantOK: false, mnemonic: "Opcode(154)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instruction, ok := tt.op.Instruction()
			if ok != tt.wantOK {
				t.Fatalf("Opcode(%d).Instruction() ok = %v, want %v", tt.op, ok, tt.wantOK)
			}
			if ok && instruction.Mnemonic != tt.mnemonic {
				t.Errorf("Opcode(%d) resolved to %q, want %q", tt.op, instruction.Mnemonic, tt.mnemonic)
			}
			if got := tt.op.String(); got != tt.mnemonic {
				t.Errorf("Opcode(%d).String() = %q, want %q", tt.op, got, tt.mnemonic)
			}
		})
	}
}

func TestRegisterFile(t *testing.T) {
	tests := []struct {
		name string
		reg  Register
		text string
	}{
		{name: "first general purpose", reg: 0, text: "r0"},
		{name: "last general purpose", reg: 15, text: "r15"},
		{name: "stack pointer is r16", reg: RegSP, text: "sp"},
		{name: "return address is r17", reg: RegRA, text: "ra"},
		{name: "past the file", reg: 18, text: "Register(18)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.reg.String(); got != tt.text {
				t.Errorf("Register(%d).String() = %q, want %q", tt.reg, got, tt.text)
			}
		})
	}

	if RegSP != 16 {
		t.Errorf("RegSP = %d, want 16", RegSP)
	}
	if RegRA != 17 {
		t.Errorf("RegRA = %d, want 17", RegRA)
	}
	if NumRegisters != 18 {
		t.Errorf("NumRegisters = %d, want 18", NumRegisters)
	}
	if NumGeneralRegisters != 16 {
		t.Errorf("NumGeneralRegisters = %d, want 16: sp and ra are not among them", NumGeneralRegisters)
	}
	if NumMemorySlots != 512 {
		t.Errorf("NumMemorySlots = %d, want 512: the data region, the spill slots and the call frames are all laid out against this one number", NumMemorySlots)
	}
}

func TestParseRegister(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   Register
		wantOK bool
	}{
		{name: "r0", input: "r0", want: 0, wantOK: true},
		{name: "r15", input: "r15", want: 15, wantOK: true},
		{name: "sp alias", input: "sp", want: RegSP, wantOK: true},
		{name: "ra alias", input: "ra", want: RegRA, wantOK: true},
		{name: "r16 names sp", input: "r16", want: RegSP, wantOK: true},
		{name: "r17 names ra", input: "r17", want: RegRA, wantOK: true},
		{name: "past the file", input: "r18"},
		{name: "indirect form", input: "rr0"},
		{name: "bare r", input: "r"},
		{name: "device", input: "d0"},
		{name: "empty", input: ""},
		{name: "negative", input: "r-1"},
		// The digits go through the same integer parse the chip uses, which
		// admits a sign and leading zeros. Accepted, not intended.
		{name: "leading zero", input: "r05", want: 5, wantOK: true},
		{name: "explicit sign", input: "r+5", want: 5, wantOK: true},
		{name: "trailing sign", input: "r5+"},
		{name: "surrounding space", input: "r 5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseRegister(tt.input)
			if ok != tt.wantOK {
				t.Fatalf("ParseRegister(%q) ok = %v, want %v", tt.input, ok, tt.wantOK)
			}
			if ok && got != tt.want {
				t.Errorf("ParseRegister(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

// TestConstantValues pins the values that cannot be recomputed. deg2rad and
// rad2deg are float precision literals widened to double, so folding pi/180 or
// 180/pi at double precision gives a different answer than the game does.
func TestConstantValues(t *testing.T) {
	tests := []struct {
		name string
		want float64
	}{
		{name: "pi", want: math.Pi},
		{name: "tau", want: 6.283185307179586},
		{name: "deg2rad", want: 0.01745329238474369},
		{name: "rad2deg", want: 57.295780181884766},
		{name: "epsilon", want: math.SmallestNonzeroFloat64},
		{name: "rgas", want: 8.31446261815324},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			constant, ok := LookupConstant(tt.name)
			if !ok {
				t.Fatalf("LookupConstant(%q) found nothing", tt.name)
			}
			if math.Float64bits(constant.Value) != math.Float64bits(tt.want) {
				t.Errorf("%s = %v (bits %#x), want %v (bits %#x)",
					tt.name, constant.Value, math.Float64bits(constant.Value), tt.want, math.Float64bits(tt.want))
			}
		})
	}
}

// TestWidenedFloatConstantsDifferFromDoublePrecision is the reason the values
// above are extracted rather than derived.
func TestWidenedFloatConstantsDifferFromDoublePrecision(t *testing.T) {
	tests := []struct {
		name   string
		folded float64
	}{
		{name: "deg2rad", folded: math.Pi / 180},
		{name: "rad2deg", folded: 180 / math.Pi},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			constant, ok := LookupConstant(tt.name)
			if !ok {
				t.Fatalf("LookupConstant(%q) found nothing", tt.name)
			}
			if constant.Value == tt.folded {
				t.Errorf("%s equals the double precision fold %v; the extracted value should be the widened float literal", tt.name, tt.folded)
			}
		})
	}
}

func TestNonFiniteConstants(t *testing.T) {
	tests := []struct {
		name  string
		check func(float64) bool
		want  string
	}{
		{name: "nan", check: math.IsNaN, want: "NaN"},
		{name: "pinf", check: func(v float64) bool { return math.IsInf(v, 1) }, want: "positive infinity"},
		{name: "ninf", check: func(v float64) bool { return math.IsInf(v, -1) }, want: "negative infinity"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			constant, ok := LookupConstant(tt.name)
			if !ok {
				t.Fatalf("LookupConstant(%q) found nothing", tt.name)
			}
			if !tt.check(constant.Value) {
				t.Errorf("%s = %v, want %s", tt.name, constant.Value, tt.want)
			}
		})
	}
}

func TestLookups(t *testing.T) {
	t.Run("instruction", func(t *testing.T) {
		got, ok := LookupInstruction("lbns")
		if !ok {
			t.Fatal("LookupInstruction(\"lbns\") found nothing")
		}
		if len(got.Operands) != 6 {
			t.Errorf("lbns arity = %d, want 6", len(got.Operands))
		}
		if got.Example != "lbns r? deviceHash nameHash slotIndex logicSlotType batchMode" {
			t.Errorf("lbns example = %q", got.Example)
		}
		if !got.Operands[0].Accepts(OperandRegister) {
			t.Error("lbns operand 0 does not accept a register")
		}
		if got.Operands[0].Accepts(OperandDevice) {
			t.Error("lbns operand 0 accepts a device, want register only")
		}
		if _, ok := LookupInstruction("LBNS"); ok {
			t.Error("LookupInstruction matched a mnemonic case-insensitively")
		}
		if _, ok := LookupInstruction("nosuchinstruction"); ok {
			t.Error("LookupInstruction found a mnemonic that does not exist")
		}
	})

	t.Run("logic type", func(t *testing.T) {
		got, ok := LookupLogicType("Temperature")
		if !ok {
			t.Fatal("LookupLogicType(\"Temperature\") found nothing")
		}
		if got.Value != 6 {
			t.Errorf("Temperature = %d, want 6", got.Value)
		}
		deprecated, ok := LookupLogicType("ImportQuantity")
		if !ok {
			t.Fatal("LookupLogicType(\"ImportQuantity\") found nothing")
		}
		if !deprecated.Deprecated {
			t.Error("ImportQuantity is not marked deprecated")
		}
		if _, ok := LookupLogicType("temperature"); ok {
			t.Error("LookupLogicType matched case-insensitively; the chip resolves these case-sensitively")
		}
	})

	t.Run("logic slot type", func(t *testing.T) {
		got, ok := LookupLogicSlotType("Occupied")
		if !ok {
			t.Fatal("LookupLogicSlotType(\"Occupied\") found nothing")
		}
		if got.Value != 1 {
			t.Errorf("Occupied = %d, want 1", got.Value)
		}
	})

	t.Run("batch mode", func(t *testing.T) {
		got, ok := LookupBatchMode("Count")
		if !ok {
			t.Fatal("LookupBatchMode(\"Count\") found nothing")
		}
		if got.Value != 4 {
			t.Errorf("Count = %d, want 4", got.Value)
		}
	})

	t.Run("reagent mode", func(t *testing.T) {
		got, ok := LookupReagentMode("TotalContents")
		if !ok {
			t.Fatal("LookupReagentMode(\"TotalContents\") found nothing")
		}
		if got.Value != 3 {
			t.Errorf("TotalContents = %d, want 3", got.Value)
		}
	})

	t.Run("constant is case-insensitive", func(t *testing.T) {
		if _, ok := LookupConstant("PI"); !ok {
			t.Error("LookupConstant(\"PI\") found nothing; the chip compares constant names case-insensitively")
		}
	})
}

func TestIsReservedWord(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "logic type", input: "Temperature", want: true},
		{name: "logic type in another case", input: "temperature", want: true},
		{name: "slot type", input: "OccupantHash", want: true},
		{name: "batch mode", input: "Average", want: true},
		{name: "reagent mode", input: "Recipe", want: true},
		{name: "constant", input: "pi", want: true},
		{name: "mnemonic", input: "add", want: true},
		{name: "register", input: "r0", want: true},
		{name: "stack pointer", input: "sp", want: true},
		{name: "return address", input: "ra", want: true},
		{name: "device pin", input: "d0", want: true},
		{name: "the last real pin", input: "d5", want: true},
		// d6 through d9 assemble and fault at runtime, so a label named d6
		// shadows something the assembler already resolves.
		{name: "a pin past the housing still parses", input: "d6", want: true},
		{name: "the last pin the assembler parses", input: "d9", want: true},
		{name: "past every pin the assembler parses", input: "d10", want: false},
		{name: "base unit", input: "db", want: true},
		// Register.String renders these as sp and ra, but both spellings resolve.
		{name: "the stack pointer by number", input: "r16", want: true},
		{name: "the return address by number", input: "r17", want: true},
		{name: "past the register file", input: "r18", want: false},
		{name: "mangled name", input: "_L3_body", want: false},
		{name: "ordinary word", input: "counter", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsReservedWord(tt.input); got != tt.want {
				t.Errorf("IsReservedWord(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestOperandKindString(t *testing.T) {
	tests := []struct {
		kind OperandKind
		want string
	}{
		{kind: OperandRegister, want: "r?"},
		{kind: OperandNumber, want: "num"},
		{kind: OperandInteger, want: "int"},
		{kind: OperandDevice, want: "d?"},
		{kind: OperandRefID, want: "id"},
		{kind: OperandLogicType, want: "logicType"},
		{kind: OperandLogicSlotType, want: "logicSlotType"},
		{kind: OperandBatchMode, want: "batchMode"},
		{kind: OperandReagentMode, want: "reagentMode"},
		{kind: OperandDeviceHash, want: "deviceHash"},
		{kind: OperandNameHash, want: "nameHash"},
		{kind: OperandSlotIndex, want: "slotIndex"},
		{kind: OperandString, want: "str"},
		{kind: OperandString + 1, want: "OperandKind(13)"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.kind.String(); got != tt.want {
				t.Errorf("OperandKind(%d).String() = %q, want %q", tt.kind, got, tt.want)
			}
		})
	}
}

// TestExamplesMatchOperands keeps the human readable signature and the machine
// readable operand list from drifting apart.
func TestExamplesMatchOperands(t *testing.T) {
	for _, instruction := range Instructions {
		var want strings.Builder
		want.WriteString(instruction.Mnemonic)
		for _, operand := range instruction.Operands {
			rendered := ""
			for i, kind := range operand.Kinds {
				if i > 0 {
					rendered += "|"
				}
				rendered += kind.String()
			}
			if operand.Name != "" {
				rendered = operand.Name + "(" + rendered + ")"
			}
			want.WriteString(" " + rendered)
		}
		if instruction.Example != want.String() {
			t.Errorf("%s example = %q, but its operands render as %q", instruction.Mnemonic, instruction.Example, want.String())
		}
	}
}

func TestBuildStamp(t *testing.T) {
	if ManifestID == "" {
		t.Error("ManifestID is empty")
	}
	if GameVersion == "" {
		t.Error("GameVersion is empty")
	}
}

func countInstructions(pred func(Instruction) bool) int {
	n := 0
	for _, i := range Instructions {
		if pred(i) {
			n++
		}
	}
	return n
}

func countLogicTypes(pred func(LogicTypeInfo) bool) int {
	n := 0
	for _, l := range LogicTypes {
		if pred(l) {
			n++
		}
	}
	return n
}
