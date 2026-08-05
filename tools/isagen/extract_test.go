package main

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// fixtureManifest stands in for the depot manifest gid an extraction stamps in.
const fixtureManifest = "2546537964923579038"

// validISA returns tables that satisfy every assertion in validate, so a test
// can perturb one field at a time.
func validISA() *ISA {
	isa := &ISA{Manifest: fixtureManifest, Version: fixtureVersion}
	for i := range wantInstructions {
		isa.Instructions = append(isa.Instructions, Instruction{Mnemonic: "i" + strconv.Itoa(i), Opcode: i})
	}
	for i, mnemonic := range slices.Sorted(maps.Keys(wantExamples)) {
		isa.Instructions[i].Mnemonic = mnemonic
		isa.Instructions[i].Example = wantExamples[mnemonic]
	}
	// One instruction accepting every operand kind is all the reachability check
	// asks for, and the last so its kinds cannot collide with a manifest
	// instruction's implied destination shape.
	for _, kind := range slices.Sorted(maps.Values(helpTokenKinds)) {
		everyKind := &isa.Instructions[len(isa.Instructions)-1]
		everyKind.Operands = append(everyKind.Operands, Operand{Kinds: []string{kind}, Direction: DirectionRead, Conversion: ConversionNone})
	}
	// The same instruction carries every bound, on operands it reads: a conversion
	// on one it only assigns is what the second reachability check refuses.
	everyKind := isa.Instructions[len(isa.Instructions)-1].Operands
	for i, conversion := range boundedConversions {
		everyKind[i].Conversion = conversion
	}
	// The two mnemonics whose name says they move the stack pointer, which
	// checkImplicit holds the recovered answer to, and the one that folds.
	for i := range isa.Instructions {
		switch isa.Instructions[i].Mnemonic {
		case "push", "pop":
			isa.Instructions[i].Implicit = []ImplicitUse{{Register: "sp", Direction: DirectionReadWrite}}
		case "peek":
			isa.Instructions[i].Implicit = []ImplicitUse{{Register: "sp", Direction: DirectionRead}}
		case "ins":
			isa.Instructions[i].Operands = []Operand{{
				Kinds:      []string{kindRegister},
				Direction:  DirectionReadWrite,
				Conversion: ConversionUnsignedLong,
			}}
		}
	}
	for i := range wantDeprecatedCommands {
		isa.Instructions[i].Deprecated = true
	}
	for i := range wantLogicTypes {
		isa.LogicTypes = append(isa.LogicTypes, EnumMember{Name: "L", Value: int64(i), Deprecated: i < wantDeprecatedLogic})
	}
	for i := range wantSlotTypes {
		isa.SlotTypes = append(isa.SlotTypes, EnumMember{Name: "S", Value: int64(i)})
	}
	for i := range wantBatchModes {
		isa.BatchModes = append(isa.BatchModes, EnumMember{Name: "B", Value: int64(i)})
	}
	isa.BatchModes[wantBatchModes-1].Name = "Count"
	for i := range wantReagentModes {
		isa.ReagentModes = append(isa.ReagentModes, EnumMember{Name: "R", Value: int64(i)})
	}
	isa.ReagentModes[wantReagentModes-1].Name = "TotalContents"
	for _, name := range wantConstants {
		isa.Constants = append(isa.Constants, Constant{Name: name, Value: "0"})
	}
	for _, want := range wantBasicEnums {
		enum := BasicEnum{Prefix: want.prefix, Type: want.prefix + "Enum"}
		for i := range want.members {
			enum.Members = append(enum.Members, EnumMember{Name: "m" + strconv.Itoa(i), Value: int64(i)})
		}
		isa.BasicEnums = append(isa.BasicEnums, enum)
	}
	return isa
}

func TestValidate(t *testing.T) {
	if err := validate(validISA()); err != nil {
		t.Fatalf("validate rejected a well formed table: %v", err)
	}

	tests := []struct {
		name    string
		perturb func(*ISA)
		wantErr string
	}{
		{
			name:    "short instruction table",
			perturb: func(i *ISA) { i.Instructions = i.Instructions[:1] },
			wantErr: "instructions: got 1, want 154",
		},
		{
			name:    "wrong deprecated instruction count",
			perturb: func(i *ISA) { i.Instructions[0].Deprecated = false },
			wantErr: "deprecated instructions: got 4, want 5",
		},
		{
			name:    "short logic type table",
			perturb: func(i *ISA) { i.LogicTypes = i.LogicTypes[:2] },
			wantErr: "logic types: got 2, want 358",
		},
		{
			name:    "wrong deprecated logic type count",
			perturb: func(i *ISA) { i.LogicTypes[0].Deprecated = false },
			wantErr: "deprecated logic types: got 22, want 23",
		},
		{
			name:    "short slot type table",
			perturb: func(i *ISA) { i.SlotTypes = nil },
			wantErr: "logic slot types: got 0, want 33",
		},
		{
			name:    "batch mode Count moved",
			perturb: func(i *ISA) { i.BatchModes[wantBatchModes-1].Value = 9 },
			wantErr: "LogicBatchMethod.Count: got 9, want 4",
		},
		{
			name:    "batch mode Count removed",
			perturb: func(i *ISA) { i.BatchModes[wantBatchModes-1].Name = "Total" },
			wantErr: "LogicBatchMethod.Count: missing",
		},
		{
			name:    "reagent mode TotalContents moved",
			perturb: func(i *ISA) { i.ReagentModes[wantReagentModes-1].Value = 0 },
			wantErr: "LogicReagentMode.TotalContents: got 0, want 3",
		},
		{
			name:    "constants renamed",
			perturb: func(i *ISA) { i.Constants[0].Name = "notanumber" },
			wantErr: "constants: got [notanumber",
		},
		{
			name:    "constants reordered",
			perturb: func(i *ISA) { i.Constants[0], i.Constants[1] = i.Constants[1], i.Constants[0] },
			wantErr: "constants: got [pinf nan",
		},
		{
			name:    "unparsable constant value",
			perturb: func(i *ISA) { i.Constants[3].Value = "three" },
			wantErr: `constant "pi": parse value "three"`,
		},
		{
			name:    "manifest instruction renamed",
			perturb: func(i *ISA) { i.Instructions[0].Mnemonic = "addd" },
			wantErr: "instruction add: missing",
		},
		{
			name:    "manifest instruction resigned",
			perturb: func(i *ISA) { i.Instructions[0].Example = "add r? a(r?|num)" },
			wantErr: `instruction add: got "add r? a(r?|num)", want "add r? a(r?|num) b(r?|num)"`,
		},
		{
			name:    "operand kind unreachable",
			perturb: func(i *ISA) { i.Instructions[len(i.Instructions)-1].Operands = nil },
			wantErr: "operand kind number: no instruction accepts one",
		},
		{
			name: "operand direction undetermined",
			perturb: func(i *ISA) {
				i.Instructions[len(i.Instructions)-1].Operands[0].Direction = DirectionUnknown
			},
			wantErr: `operand 0: direction "unknown" says nothing`,
		},
		{
			name: "a write the operand list does not put first",
			perturb: func(i *ISA) {
				i.Instructions[len(i.Instructions)-1].Operands[1].Direction = DirectionWrite
			},
			wantErr: "the operation writes operand 1, but the operand list puts a destination at -1",
		},
		{
			name: "a destination the operation does not write",
			perturb: func(i *ISA) {
				last := &i.Instructions[len(i.Instructions)-1]
				last.Operands = []Operand{{Kinds: []string{kindRegister}, Direction: DirectionRead, Conversion: ConversionNone}}
			},
			wantErr: "the operation writes operand -1, but the operand list puts a destination at 0",
		},
		{
			name: "two written operands",
			perturb: func(i *ISA) {
				last := &i.Instructions[len(i.Instructions)-1]
				last.Operands = []Operand{
					{Kinds: []string{kindRegister}, Direction: DirectionWrite, Conversion: ConversionNone},
					{Kinds: []string{kindRegister}, Direction: DirectionWrite, Conversion: ConversionNone},
				}
			},
			wantErr: "operands 0 and 1 are both written",
		},
		{
			name: "operand conversion undetermined",
			perturb: func(i *ISA) {
				i.Instructions[len(i.Instructions)-1].Operands[3].Conversion = ConversionUnknown
			},
			wantErr: `operand 3: conversion "unknown" says nothing`,
		},
		{
			name: "a conversion no operand is read through",
			perturb: func(i *ISA) {
				i.Instructions[len(i.Instructions)-1].Operands[0].Conversion = ConversionNone
			},
			wantErr: "conversion int: no operand is read through one",
		},
		{
			name: "a converted operand the operation only assigns",
			perturb: func(i *ISA) {
				last := &i.Instructions[len(i.Instructions)-1]
				last.Operands[0].Direction = DirectionWrite
			},
			wantErr: "the operation converts it through int and assigns its register without reading it",
		},
		{
			name: "a folded destination read as one the operation only assigns",
			perturb: func(i *ISA) {
				index := slices.IndexFunc(i.Instructions, func(in Instruction) bool { return in.Mnemonic == "ins" })
				i.Instructions[index].Operands[0].Direction = DirectionWrite
				i.Instructions[index].Operands[0].Conversion = ConversionNone
			},
			wantErr: "instruction ins: the operation reads the destination it assigns = false, but the mnemonic says true",
		},
		{
			name: "a destination folded where the mnemonic says it is replaced",
			perturb: func(i *ISA) {
				index := slices.IndexFunc(i.Instructions, func(in Instruction) bool { return in.Mnemonic == "add" })
				i.Instructions[index].Operands = []Operand{{Kinds: []string{kindRegister}, Direction: DirectionReadWrite, Conversion: ConversionNone}}
			},
			wantErr: "instruction add: the operation reads the destination it assigns = true, but the mnemonic says false",
		},
		{
			name: "an implicit write the mnemonic does not claim",
			perturb: func(i *ISA) {
				i.Instructions[0].Implicit = []ImplicitUse{{Register: "ra", Direction: DirectionWrite}}
			},
			wantErr: `the operation does "write" to ra, but the mnemonic says ""`,
		},
		{
			name: "an implicit write the operation lost",
			perturb: func(i *ISA) {
				index := slices.IndexFunc(i.Instructions, func(in Instruction) bool { return in.Mnemonic == "push" })
				i.Instructions[index].Implicit = nil
			},
			wantErr: `instruction push: the operation does "" to sp, but the mnemonic says "readwrite"`,
		},
		{
			name: "an implicit use of a register no instruction leaves unnamed",
			perturb: func(i *ISA) {
				i.Instructions[0].Implicit = []ImplicitUse{{Register: "r3", Direction: DirectionRead}}
			},
			wantErr: `reaches "r3", which is not a register an instruction can leave unnamed`,
		},
		{
			name: "an implicit use whose direction says nothing",
			perturb: func(i *ISA) {
				index := slices.IndexFunc(i.Instructions, func(in Instruction) bool { return in.Mnemonic == "push" })
				i.Instructions[index].Implicit[0].Direction = DirectionUnknown
			},
			wantErr: `what it does to sp is "unknown", which says nothing`,
		},
		{
			name:    "basic enum table short",
			perturb: func(i *ISA) { i.BasicEnums = i.BasicEnums[:len(i.BasicEnums)-1] },
			wantErr: "basic enums: got 25, want 26",
		},
		{
			name:    "basic enums reordered",
			perturb: func(i *ISA) { i.BasicEnums[0], i.BasicEnums[1] = i.BasicEnums[1], i.BasicEnums[0] },
			wantErr: `basic enum 0: got prefix "TransmitterMode", want "Sound"`,
		},
		{
			name:    "basic enum truncated",
			perturb: func(i *ISA) { i.BasicEnums[0].Members = i.BasicEnums[0].Members[:1] },
			wantErr: `basic enum "Sound": got 1 members, want 46`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isa := validISA()
			tt.perturb(isa)
			err := validate(isa)
			if err == nil {
				t.Fatalf("validate accepted a perturbed table, want an error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("validate error = %q, want it to contain %q", err.Error(), tt.wantErr)
			}
			if !strings.Contains(err.Error(), fixtureManifest) {
				t.Errorf("validate error = %q, want it to name the manifest it checked", err.Error())
			}
		})
	}
}

// TestValidateAsksReachabilityOfTheBoundsOnly is the reason ConversionNone is not
// one of the spellings an operand has to be found carrying: it is the answer
// every unreduced operand already gets, so a walk that stopped resolving readers
// would satisfy it everywhere.
func TestValidateAsksReachabilityOfTheBoundsOnly(t *testing.T) {
	isa := validISA()
	operands := isa.Instructions[len(isa.Instructions)-1].Operands
	for i := range operands {
		operands[i].Conversion = boundedConversions[i%len(boundedConversions)]
	}
	unbounded := slices.ContainsFunc(isa.Instructions, func(in Instruction) bool {
		return slices.ContainsFunc(in.Operands, func(o Operand) bool { return o.Conversion == ConversionNone })
	})
	if unbounded {
		t.Fatal("the table still carries an operand no conversion bounds, so the check is not the one under test")
	}
	if err := validate(isa); err != nil {
		t.Errorf("validate rejected a table whose every operand carries a bound: %v", err)
	}
}

// TestValidateReportsEveryMismatch confirms a game update can be assessed in one
// pass rather than one failed run per changed table.
func TestValidateReportsEveryMismatch(t *testing.T) {
	isa := validISA()
	isa.SlotTypes = nil
	isa.BatchModes = nil
	err := validate(isa)
	if err == nil {
		t.Fatal("validate accepted a table with two mismatches")
	}
	for _, want := range []string{"logic slot types", "logic batch methods", "LogicBatchMethod.Count: missing"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("validate error = %q, want it to mention %q", err.Error(), want)
		}
	}
}

// TestValidateReportsBothHalvesOfABasicEnum covers the one table held to two
// things at once. Reporting only a rename leaves the membership change to be
// discovered by the next run against a container that takes minutes to build.
func TestValidateReportsBothHalvesOfABasicEnum(t *testing.T) {
	isa := validISA()
	renamed := wantBasicEnums[0].prefix + "Renamed"
	isa.BasicEnums[0].Prefix = renamed
	isa.BasicEnums[0].Members = isa.BasicEnums[0].Members[:wantBasicEnums[0].members-1]

	err := validate(isa)
	if err == nil {
		t.Fatal("validate accepted a basic enum whose prefix and size both moved")
	}
	for _, want := range []string{
		"got prefix \"" + renamed + "\"",
		"basic enum \"" + wantBasicEnums[0].prefix + "\": got " + strconv.Itoa(wantBasicEnums[0].members-1) + " members",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("validate error = %q, want it to contain %q", err.Error(), want)
		}
	}
}

func TestWriteJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "isa.json")
	isa := readFixture(t, "minimal.json")
	if err := writeJSON(isa, path); err != nil {
		t.Fatalf("writeJSON: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if !strings.HasSuffix(string(data), "}\n") {
		t.Error("written JSON does not end with a newline")
	}
	if strings.Contains(string(data), `<`) {
		t.Error("written JSON escapes HTML characters; operand spellings must survive verbatim")
	}

	var round ISA
	if err := json.Unmarshal(data, &round); err != nil {
		t.Fatalf("re-decode written file: %v", err)
	}
	second := filepath.Join(t.TempDir(), "isa.json")
	if err := writeJSON(&round, second); err != nil {
		t.Fatalf("writeJSON on the re-decoded value: %v", err)
	}
	again, err := os.ReadFile(second)
	if err != nil {
		t.Fatalf("read second file: %v", err)
	}
	if string(again) != string(data) {
		t.Error("writing the same tables twice produced different bytes")
	}
}

// TestExtractISAOverTheShapeCorpus runs the whole recovery over the shape corpus,
// which is small enough that the expected tables can be stated outright. Those
// tables are not the game's; whether the recovery still yields the game's own is
// [TestTheGameStillYieldsTheCheckedInISA].
func TestExtractISAOverTheShapeCorpus(t *testing.T) {
	isa := extractFixture(t)

	want := []Instruction{
		{Mnemonic: "move", Opcode: 0, Example: "move r? a(r?|num)", Operands: []Operand{
			{Kinds: []string{kindRegister}, Direction: DirectionWrite, Conversion: ConversionNone},
			{Name: "a", Kinds: []string{kindRegister, kindNumber}, Direction: DirectionRead, Conversion: ConversionNone},
		}},
		{Mnemonic: "l", Opcode: 1, Example: "l r? logicType", Operands: []Operand{
			{Kinds: []string{kindRegister}, Direction: DirectionWrite, Conversion: ConversionNone},
			{Kinds: []string{kindLogicType}, Direction: DirectionRead, Conversion: ConversionNone},
		}},
		{Mnemonic: "swap", Opcode: 2, Example: "swap a(r?|num) r?", Operands: []Operand{
			{Name: "a", Kinds: []string{kindRegister, kindNumber}, Direction: DirectionRead, Conversion: ConversionSignedLong},
			{Kinds: []string{kindRegister}, Direction: DirectionWrite, Conversion: ConversionNone},
		}},
		{Mnemonic: "hcf", Opcode: 3, Deprecated: true, Example: "hcf"},
	}
	if !slices.EqualFunc(isa.Instructions, want, sameInstruction) {
		t.Errorf("instructions = %+v, want %+v", isa.Instructions, want)
	}

	for _, tt := range []struct {
		name string
		got  []EnumMember
		want []EnumMember
	}{
		{
			name: "logic types",
			got:  isa.LogicTypes,
			want: []EnumMember{{Name: "Power"}, {Name: "Open", Value: 1}, {Name: "Mode", Value: 2, Deprecated: true}},
		},
		{
			name: "logic slot types",
			got:  isa.SlotTypes,
			want: []EnumMember{{Name: "Occupied"}, {Name: "Quantity", Value: 1}},
		},
		{
			name: "reagent modes",
			got:  isa.ReagentModes,
			want: []EnumMember{{Name: "Contents"}, {Name: "Required", Value: 1}, {Name: "Recipe", Value: 2}, {Name: "TotalContents", Value: 3}},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if !slices.Equal(tt.got, tt.want) {
				t.Errorf("got %+v, want %+v", tt.got, tt.want)
			}
		})
	}

	wantConstants := []Constant{{Name: "nan", Value: "NaN:0xfff8000000000000"}, {Name: "pi", Value: "3.141592653589793"}}
	if !slices.Equal(isa.Constants, wantConstants) {
		t.Errorf("constants = %+v, want %+v", isa.Constants, wantConstants)
	}
}

// TestExtractBasicEnums covers what the operand tables do not: a prefix differing
// from the C# type, a nested type, an entry registered without a prefix, and the
// LogicType entry the operand tables already carry.
func TestExtractBasicEnums(t *testing.T) {
	want := []BasicEnum{
		{Prefix: "Color", Type: "ColorType", Members: []EnumMember{{Name: "Blue"}, {Name: "Gray", Value: 1}}},
		{Prefix: "SlotClass", Type: "Slot.Class", Members: []EnumMember{{Name: "None"}, {Name: "Helmet", Value: 1}, {Name: "Suit", Value: 2}}},
		{Prefix: "", Type: "ConditionOperation", Members: []EnumMember{{Name: "Equals"}, {Name: "Greater", Value: 1}}},
	}
	if got := extractFixture(t).BasicEnums; !slices.EqualFunc(got, want, sameBasicEnum) {
		t.Errorf("basic enums = %+v, want %+v", got, want)
	}
}

// extractFixture recovers the tables from the shape corpus.
func extractFixture(t *testing.T) *ISA {
	t.Helper()
	tree, err := newSourceTree(filepath.Join("testdata", shapeCorpus))
	if err != nil {
		t.Fatalf("newSourceTree: %v", err)
	}
	isa, err := extractISA(tree)
	if err != nil {
		t.Fatalf("extractISA: %v", err)
	}
	return isa
}

func sameInstruction(a, b Instruction) bool {
	return a.Mnemonic == b.Mnemonic && a.Opcode == b.Opcode && a.Deprecated == b.Deprecated &&
		a.Example == b.Example && slices.EqualFunc(a.Operands, b.Operands, sameOperand) &&
		slices.Equal(a.Implicit, b.Implicit)
}

func sameOperand(a, b Operand) bool {
	return a.Name == b.Name && slices.Equal(a.Kinds, b.Kinds) &&
		a.Direction == b.Direction && a.Conversion == b.Conversion
}

// TestExtractedDirectionBeatsTheOperandShape is the reason direction is extracted
// at all. positionalWrite -- the destination is the unnamed, register-only
// operand in first position -- answers that swap writes nothing, which would
// leave allocation believing an overwritten register still holds a live value.
func TestExtractedDirectionBeatsTheOperandShape(t *testing.T) {
	isa := extractFixture(t)
	i := slices.IndexFunc(isa.Instructions, func(instruction Instruction) bool { return instruction.Mnemonic == "swap" })
	if i < 0 {
		t.Fatal("the fixture declares no swap, which is what carries a write outside first position")
	}
	swap := isa.Instructions[i]

	if got := positionalWrite(swap); got != -1 {
		t.Errorf("positionalWrite(swap) = %d, want -1; the shape of %q says nothing is written", got, swap.Example)
	}
	want := []Direction{DirectionRead, DirectionWrite}
	got := make([]Direction, len(swap.Operands))
	for j, operand := range swap.Operands {
		got[j] = operand.Direction
	}
	if !slices.Equal(got, want) {
		t.Errorf("swap directions = %v, want %v", got, want)
	}

	if problems := checkDirections([]Instruction{swap}); len(problems) == 0 {
		t.Error("checkDirections accepted a write the operand list does not put first; a build introducing one must stop extraction")
	}
}

// TestCheckDirectionsAcceptsAFoldedDestination covers the direction ins carries.
// A destination read before it is assigned is still the one the operand list puts
// first, so the two readings agree and extraction has nothing to report.
func TestCheckDirectionsAcceptsAFoldedDestination(t *testing.T) {
	folded := Instruction{
		Mnemonic: "ins",
		Example:  "ins r? a(r?|num) b(r?|num) c(r?|num)",
		Operands: []Operand{
			{Kinds: []string{kindRegister}, Direction: DirectionReadWrite},
			{Name: "a", Kinds: []string{kindRegister, kindNumber}, Direction: DirectionRead},
		},
	}
	if problems := checkDirections([]Instruction{folded}); len(problems) != 0 {
		t.Errorf("checkDirections reported %v for a destination the instruction folds into", problems)
	}
}

// TestCheckImplicitSeesAConditionalLink covers the half of the implicit reading
// the mnemonic alone settles: whether a linking form replaces ra or leaves what
// it held. Read as an unconditional write, the value ra held across the
// instruction reads as dead.
func TestCheckImplicitSeesAConditionalLink(t *testing.T) {
	tests := []struct {
		name      string
		mnemonic  string
		direction Direction
		wantErr   string
	}{
		{name: "the unconditional link replaces it", mnemonic: "jal", direction: DirectionWrite},
		{name: "a conditional link folds into it", mnemonic: "bgeal", direction: DirectionReadWrite},
		{
			name:      "a conditional link read as replacing it",
			mnemonic:  "bgeal",
			direction: DirectionWrite,
			wantErr:   `the operation does "write" to ra, but the mnemonic says "readwrite"`,
		},
		{
			name:      "the unconditional link read as folding into it",
			mnemonic:  "jal",
			direction: DirectionReadWrite,
			wantErr:   `the operation does "readwrite" to ra, but the mnemonic says "write"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instruction := Instruction{
				Mnemonic: tt.mnemonic,
				Implicit: []ImplicitUse{{Register: "ra", Direction: tt.direction}},
			}
			problems := strings.Join(checkImplicit([]Instruction{instruction}), "\n")
			switch {
			case tt.wantErr == "" && problems != "":
				t.Errorf("checkImplicit reported %q for what the mnemonic says", problems)
			case tt.wantErr != "" && !strings.Contains(problems, tt.wantErr):
				t.Errorf("checkImplicit reported %q, want it to mention %q", problems, tt.wantErr)
			}
		})
	}
}

func sameBasicEnum(a, b BasicEnum) bool {
	return a.Prefix == b.Prefix && a.Type == b.Type && slices.Equal(a.Members, b.Members)
}

func TestExtractInputErrors(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join("testdata", shapeCorpus)
	assembly := filepath.Join(dir, "Assembly-CSharp.dll")
	if err := os.WriteFile(assembly, []byte("not a PE image"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	tests := []struct {
		name     string
		source   string
		assembly string
		manifest string
		wantErr  string
	}{
		{name: "no source", assembly: assembly, manifest: fixtureManifest, wantErr: "--source"},
		{name: "no assembly", source: source, manifest: fixtureManifest, wantErr: "--assembly"},
		{name: "no manifest", source: source, assembly: assembly, wantErr: "--manifest"},
		{
			name:     "absent source",
			source:   filepath.Join(dir, "absent"),
			assembly: assembly,
			manifest: fixtureManifest,
			wantErr:  "index decompiled source",
		},
		{
			name:     "unreadable assembly",
			source:   source,
			assembly: assembly,
			manifest: fixtureManifest,
			wantErr:  "open PE image",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := filepath.Join(dir, "isa.json")
			checkErr(t, "extract", extract(tt.source, tt.assembly, tt.manifest, out), tt.wantErr)
		})
	}
}
