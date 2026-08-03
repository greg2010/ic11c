package main

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestSkipLiteralAndMatchDelim(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		want    string
		wantErr bool
	}{
		{name: "plain block", src: `x { a; b; } y`, want: ` a; b; `},
		{name: "nested block", src: `{ { inner } outer }`, want: ` { inner } outer `},
		{name: "brace in string", src: `{ s = "{"; }`, want: ` s = "{"; `},
		{name: "brace in interpolated string", src: `{ s = $"a{b}c"; }`, want: ` s = $"a{b}c"; `},
		{name: "brace in verbatim string", src: `{ s = @"a""}b"; }`, want: ` s = @"a""}b"; `},
		{name: "brace in char literal", src: `{ c = '}'; }`, want: ` c = '}'; `},
		{name: "escaped quote in string", src: `{ s = "\"}"; }`, want: ` s = "\"}"; `},
		{name: "brace in line comment", src: "{ // }\n }", want: " // }\n "},
		{name: "brace in block comment", src: `{ /* } */ }`, want: ` /* } */ `},
		{name: "no opening brace", src: `no block here`, wantErr: true},
		{name: "unterminated block", src: `{ a;`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _, err := matchDelim(tt.src, 0, '{', '}')
			if tt.wantErr {
				if err == nil {
					t.Fatalf("matchDelim(%q) = %q, want an error", tt.src, got)
				}
				if !errors.Is(err, errNotFound) {
					t.Errorf("matchDelim(%q) error = %v, want it to wrap errNotFound", tt.src, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("matchDelim(%q): %v", tt.src, err)
			}
			if got != tt.want {
				t.Errorf("matchDelim(%q) = %q, want %q", tt.src, got, tt.want)
			}
		})
	}
}

func TestSplitTop(t *testing.T) {
	tests := []struct {
		name string
		src  string
		sep  byte
		want []string
	}{
		{name: "flat", src: `a, b, c`, sep: ',', want: []string{`a`, ` b`, ` c`}},
		{name: "nested parens", src: `a, f(b, c), d`, sep: ',', want: []string{`a`, ` f(b, c)`, ` d`}},
		{name: "comma in string", src: `"a, b", c`, sep: ',', want: []string{`"a, b"`, ` c`}},
		{name: "trailing separator", src: `a, b,`, sep: ',', want: []string{`a`, ` b`}},
		{name: "plus separator", src: `A + B + C`, sep: '+', want: []string{`A `, ` B `, ` C`}},
		{name: "empty", src: ``, sep: ',', want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := splitTop(tt.src, tt.sep); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("splitTop(%q, %q) = %#v, want %#v", tt.src, string(tt.sep), got, tt.want)
			}
		})
	}
}

func TestParseEnum(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		enumName string
		want     []EnumMember
		wantErr  string
	}{
		{
			name:     "implicit values",
			src:      "public enum ScriptCommand\n{\n\tl,\n\ts,\n\tadd\n}\n",
			enumName: "ScriptCommand",
			want:     []EnumMember{{Name: "l"}, {Name: "s", Value: 1}, {Name: "add", Value: 2}},
		},
		{
			name:     "explicit values with a gap",
			src:      "public enum LogicType : ushort\n{\n\tNone = 0,\n\tTargetPadIndex = 158,\n\tSizeX = 160\n}\n",
			enumName: "LogicType",
			want:     []EnumMember{{Name: "None"}, {Name: "TargetPadIndex", Value: 158}, {Name: "SizeX", Value: 160}},
		},
		{
			name:     "implicit value follows an explicit one",
			src:      "public enum E\n{\n\tA = 7,\n\tB\n}\n",
			enumName: "E",
			want:     []EnumMember{{Name: "A", Value: 7}, {Name: "B", Value: 8}},
		},
		{
			name:     "hexadecimal and negative values",
			src:      "public enum E\n{\n\tA = 0x10,\n\tB = -1\n}\n",
			enumName: "E",
			want:     []EnumMember{{Name: "A", Value: 16}, {Name: "B", Value: -1}},
		},
		{
			name:     "skips an enum with a different name",
			src:      "public enum Other\n{\n\tX\n}\npublic enum E : byte\n{\n\tA\n}\n",
			enumName: "E",
			want:     []EnumMember{{Name: "A"}},
		},
		{
			name:     "name is not matched as a prefix",
			src:      "public enum LogicTypeExtra\n{\n\tX\n}\npublic enum LogicType\n{\n\tA\n}\n",
			enumName: "LogicType",
			want:     []EnumMember{{Name: "A"}},
		},
		{
			name:     "unsigned and long value suffixes",
			src:      "public enum E : uint\n{\n\tA = 0x10u,\n\tB = 32U,\n\tC = 64uL\n}\n",
			enumName: "E",
			want:     []EnumMember{{Name: "A", Value: 16}, {Name: "B", Value: 32}, {Name: "C", Value: 64}},
		},
		{
			name:     "members behind attributes",
			src:      "public enum E\n{\n\t[XmlEnum(\"None\")]\n\tNone,\n\t[Obsolete]\n\t[XmlEnum(\"Oxygen, gas\")]\n\tOxygen = 1u\n}\n",
			enumName: "E",
			want:     []EnumMember{{Name: "None"}, {Name: "Oxygen", Value: 1}},
		},
		{
			name:     "nested enum",
			src:      "public class Slot\n{\n\tpublic enum Class : ushort\n\t{\n\t\tNone,\n\t\tHelmet\n\t}\n}\n",
			enumName: "Class",
			want:     []EnumMember{{Name: "None"}, {Name: "Helmet", Value: 1}},
		},
		{
			name:     "missing enum",
			src:      "public enum Other\n{\n\tX\n}\n",
			enumName: "E",
			wantErr:  "enum E",
		},
		{
			name:     "unterminated attribute",
			src:      "public enum E\n{\n\t[XmlEnum(\"None\"\n\tNone\n}\n",
			enumName: "E",
			wantErr:  "attribute on",
		},
		{
			name:     "unrecognized member",
			src:      "public enum E\n{\n\tA = SomethingElse\n}\n",
			enumName: "E",
			wantErr:  "unrecognized member",
		},
		{
			name:     "no members",
			src:      "public enum E\n{\n}\n",
			enumName: "E",
			wantErr:  "no members",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseEnum(tt.src, tt.enumName)
			if !checkErr(t, "parseEnum", err, tt.wantErr) {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseEnum = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestParseListInitializer(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		field    string
		elemType string
		want     []string
		wantErr  string
	}{
		{
			name:     "populated",
			src:      "public static List<ScriptCommand> DeprecatedCommands = new List<ScriptCommand>\n{\n\tScriptCommand.label,\n\tScriptCommand.ld\n};\n",
			field:    "DeprecatedCommands",
			elemType: "ScriptCommand",
			want:     []string{"label", "ld"},
		},
		{
			name:     "empty list has no initializer block",
			src:      "public static List<LogicSlotType> DeprecatedSlotTypes = new List<LogicSlotType>();\n",
			field:    "DeprecatedSlotTypes",
			elemType: "LogicSlotType",
			want:     nil,
		},
		{
			name:     "missing declaration",
			src:      "public static int Unrelated = 1;\n",
			field:    "DeprecatedCommands",
			elemType: "ScriptCommand",
			wantErr:  "initializer for DeprecatedCommands",
		},
		{
			name:     "entry of the wrong type",
			src:      "List<ScriptCommand> DeprecatedCommands = new List<ScriptCommand>\n{\n\tOther.label\n};\n",
			field:    "DeprecatedCommands",
			elemType: "ScriptCommand",
			wantErr:  "unrecognized entry",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseListInitializer(tt.src, tt.field, tt.elemType)
			if !checkErr(t, "parseListInitializer", err, tt.wantErr) {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseListInitializer = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestParseConstructedList(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		field    string
		elemType string
		want     []string
		wantErr  string
	}{
		{
			name:     "populated",
			src:      "AllReagents = new List<Reagent>\n{\n\tnew Flour(0.0),\n\tnew Milk(0.0)\n};\n",
			field:    "AllReagents",
			elemType: "Reagent",
			want:     []string{"Flour", "Milk"},
		},
		{
			name:     "constructor arguments are not entries",
			src:      "AllReagents = new List<Reagent>\n{\n\tnew Flour(new Milk(0.0), 1)\n};\n",
			field:    "AllReagents",
			elemType: "Reagent",
			want:     []string{"Flour"},
		},
		{
			name:     "missing declaration",
			src:      "public static int Unrelated = 1;\n",
			field:    "AllReagents",
			elemType: "Reagent",
			wantErr:  "initializer for AllReagents: not found",
		},
		{
			// The block belonging to whatever follows must not be read as this
			// declaration's.
			name:     "no initializer block",
			src:      "AllReagents = new List<Reagent>();\nvoid F()\n{\n\tnew Flour(0.0);\n}\n",
			field:    "AllReagents",
			elemType: "Reagent",
			wantErr:  `initializer for AllReagents: opening "{": not found`,
		},
		{
			name:     "entry is not a constructor call",
			src:      "AllReagents = new List<Reagent>\n{\n\tReagent.Flour\n};\n",
			field:    "AllReagents",
			elemType: "Reagent",
			wantErr:  `unrecognized entry "Reagent.Flour"`,
		},
		{
			name:     "empty initializer block",
			src:      "AllReagents = new List<Reagent>\n{\n};\n",
			field:    "AllReagents",
			elemType: "Reagent",
			wantErr:  "initializer for AllReagents: no entries",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseConstructedList(tt.src, tt.field, tt.elemType)
			if !checkErr(t, "parseConstructedList", err, tt.wantErr) {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseConstructedList = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestParseConstructorSwitch(t *testing.T) {
	const populated = "return reagentId switch\n{\n\t0 => new Flour(quantity), \n\t1 => new Milk(quantity), \n\t_ => null, \n};\n"

	tests := []struct {
		name    string
		src     string
		subject string
		want    map[int64]string
		wantErr string
	}{
		{
			name:    "populated",
			src:     populated,
			subject: "reagentId",
			want:    map[int64]string{0: "Flour", 1: "Milk"},
		},
		{
			name:    "sparse and out of order labels",
			src:     "return id switch\n{\n\t7 => new B(q), \n\t2 => new A(q), \n\t_ => null, \n};\n",
			subject: "id",
			want:    map[int64]string{2: "A", 7: "B"},
		},
		{
			name:    "missing switch",
			src:     "return null;\n",
			subject: "reagentId",
			wantErr: "switch on reagentId: not found",
		},
		{
			name:    "no block",
			src:     "return reagentId switch;\n",
			subject: "reagentId",
			wantErr: `switch on reagentId: opening "{": not found`,
		},
		{
			name:    "no constructing arm",
			src:     "return reagentId switch\n{\n\t_ => null, \n};\n",
			subject: "reagentId",
			wantErr: "switch on reagentId: no arms",
		},
		{
			name:    "duplicate label",
			src:     "return reagentId switch\n{\n\t0 => new Flour(q), \n\t0 => new Milk(q), \n};\n",
			subject: "reagentId",
			wantErr: "label 0 constructs both Flour and Milk",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseConstructorSwitch(tt.src, tt.subject)
			if !checkErr(t, "parseConstructorSwitch", err, tt.wantErr) {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseConstructorSwitch = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestParseInternalEnums(t *testing.T) {
	const decl = "\t\tInternalEnums = new List<IScriptEnum>\n\t\t{\n"

	tests := []struct {
		name    string
		src     string
		want    []internalEnum
		wantErr string
	}{
		{
			name: "every entry shape",
			src: decl +
				"\t\t\tnew ScriptEnum<LogicType>(InstructionInclude.LogicType, LogicBase.IsDeprecated, LogicBase.GetLogicDescription),\n" +
				"\t\t\tnew BasicEnum<LogicType>(\"LogicType\", LogicBase.IsDeprecated),\n" +
				"\t\t\tnew BasicEnum<SoundAlert>(\"Sound\"),\n" +
				"\t\t\tnew BasicEnum<Slot.Class>(\"SlotClass\"),\n" +
				"\t\t\tnew BasicEnum<ConditionOperation>()\n\t\t};\n",
			want: []internalEnum{
				{typeName: "LogicType", deprecates: true},
				{basic: true, typeName: "LogicType", prefix: "LogicType", deprecates: true},
				{basic: true, typeName: "SoundAlert", prefix: "Sound"},
				{basic: true, typeName: "Slot.Class", prefix: "SlotClass"},
				{basic: true, typeName: "ConditionOperation"},
			},
		},
		{
			name:    "missing initializer",
			src:     "public class ProgrammableChip\n{\n}\n",
			wantErr: "initializer for InternalEnums",
		},
		{
			name:    "unrecognized entry",
			src:     decl + "\t\t\tSomethingElse.Instance\n\t\t};\n",
			wantErr: `unrecognized entry "SomethingElse.Instance"`,
		},
		{
			name:    "prefix is not a string literal",
			src:     decl + "\t\t\tnew BasicEnum<SoundAlert>(SoundPrefix)\n\t\t};\n",
			wantErr: "expected a string literal",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseInternalEnums(tt.src)
			if !checkErr(t, "parseInternalEnums", err, tt.wantErr) {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseInternalEnums = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestParseHelpStrings(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		want    map[string]string
		wantErr string
	}{
		{
			name: "two and three argument forms",
			src: "\t\tREGISTER = new HelpString(\"r?\", \"#0080FFFF\");\n" +
				"\t\tOR = new HelpString(\"or\", \"|\", \"#585858FF\");\n",
			want: map[string]string{"REGISTER": "r?", "OR": "|"},
		},
		{
			name:    "unexpected argument count",
			src:     "\t\tREGISTER = new HelpString(\"r?\");\n",
			wantErr: "unexpected argument count",
		},
		{
			name:    "no symbols",
			src:     "class C { }\n",
			wantErr: "HelpString symbol table",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseHelpStrings(tt.src)
			if !checkErr(t, "parseHelpStrings", err, tt.wantErr) {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseHelpStrings = %#v, want %#v", got, tt.want)
			}
		})
	}
}

// chipFixture mirrors the shape of the decompiled ProgrammableChip: a static
// constructor declaring the HelpString symbols, then the switch that maps each
// command to its operand descriptions.
const chipFixture = `
	static ProgrammableChip()
	{
		DEVICE_INDEX = new HelpString("d?", "green");
		REGISTER = new HelpString("r?", "#0080FFFF");
		NUMBER = new HelpString("num", "#20B2AA");
		REF_ID = new HelpString("id", "#20B2AA");
		OR = new HelpString("or", "|", "#585858FF");
		LOGIC_TYPE = new HelpString("logicType", "orange");
		STRING = new HelpString("str", "white");
	}

	public static string GetCommandExample(ScriptCommand command, string color = "yellow", int spaceCount = -1)
	{
		switch (command)
		{
		case ScriptCommand.l:
			return MakeString(command, color, spaceCount, REGISTER, (DEVICE_INDEX + REGISTER + REF_ID).Var("device"), LOGIC_TYPE);
		case ScriptCommand.add:
		case ScriptCommand.sub:
			return MakeString(command, color, spaceCount, REGISTER, (REGISTER + NUMBER).Var("a"), (REGISTER + NUMBER).Var("b"));
		case ScriptCommand.alias:
			return MakeString(command, color, spaceCount, STRING, REGISTER + DEVICE_INDEX);
		case ScriptCommand.yield:
			return MakeString(command, color, spaceCount);
		default:
			throw new ArgumentOutOfRangeException("command", command, null);
		}
	}
`

func TestParseCommandExamples(t *testing.T) {
	tokens, err := parseHelpStrings(chipFixture)
	if err != nil {
		t.Fatalf("parseHelpStrings: %v", err)
	}
	got, err := parseCommandExamples(chipFixture, tokens)
	if err != nil {
		t.Fatalf("parseCommandExamples: %v", err)
	}

	want := map[string][]Operand{
		"l": {
			{Kinds: []string{kindRegister}},
			{Name: "device", Kinds: []string{kindDevice, kindRegister, kindRefID}},
			{Kinds: []string{kindLogicType}},
		},
		"add": {
			{Kinds: []string{kindRegister}},
			{Name: "a", Kinds: []string{kindRegister, kindNumber}},
			{Name: "b", Kinds: []string{kindRegister, kindNumber}},
		},
		"alias": {
			{Kinds: []string{kindString}},
			{Kinds: []string{kindRegister, kindDevice}},
		},
		"yield": nil,
	}
	want["sub"] = want["add"]

	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseCommandExamples = %#v, want %#v", got, want)
	}
}

func TestParseCommandExamplesErrors(t *testing.T) {
	tokens := map[string]string{"REGISTER": "r?", "MYSTERY": "mystery"}
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name:    "missing method",
			body:    "class C { }",
			wantErr: "GetCommandExample",
		},
		{
			name:    "unknown symbol",
			body:    "public static string GetCommandExample(ScriptCommand c) {\ncase ScriptCommand.add:\nreturn MakeString(command, color, spaceCount, WHAT);\n}",
			wantErr: `unknown HelpString symbol "WHAT"`,
		},
		{
			name:    "symbol with an unmapped token",
			body:    "public static string GetCommandExample(ScriptCommand c) {\ncase ScriptCommand.add:\nreturn MakeString(command, color, spaceCount, MYSTERY);\n}",
			wantErr: `renders unknown token "mystery"`,
		},
		{
			name:    "call without a case label",
			body:    "public static string GetCommandExample(ScriptCommand c) {\nreturn MakeString(command, color, spaceCount, REGISTER);\n}",
			wantErr: "no preceding case label",
		},
		{
			name:    "case label without a call",
			body:    "public static string GetCommandExample(ScriptCommand c) {\ncase ScriptCommand.add:\nbreak;\n}",
			wantErr: "have no MakeString call",
		},
		{
			name:    "duplicate case",
			body:    "public static string GetCommandExample(ScriptCommand c) {\ncase ScriptCommand.add:\nreturn MakeString(command, color, spaceCount, REGISTER);\ncase ScriptCommand.add:\nreturn MakeString(command, color, spaceCount, REGISTER);\n}",
			wantErr: "duplicate case for ScriptCommand.add",
		},
		{
			name:    "too few fixed arguments",
			body:    "public static string GetCommandExample(ScriptCommand c) {\ncase ScriptCommand.add:\nreturn MakeString(command, color);\n}",
			wantErr: "expected at least 3 arguments",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseCommandExamples(tt.body, tokens)
			checkErr(t, "parseCommandExamples", err, tt.wantErr)
		})
	}
}

func TestParseConstants(t *testing.T) {
	src := `
		AllConstants = new Constant[5]
		{
			new Constant("nan", "not, a, number", double.NaN, addValueToDescription: false),
			new Constant("pinf", "positive infinity", double.PositiveInfinity, addValueToDescription: false),
			new Constant("pi", "ratio", Math.PI),
			new Constant("tau", "ratio", Math.PI * 2.0),
			new Constant("deg2rad", "conversion", 0.01745329238474369)
		};
	`
	got, err := parseConstants(src)
	if err != nil {
		t.Fatalf("parseConstants: %v", err)
	}
	want := []Constant{
		{Name: "nan", Value: "NaN:0xfff8000000000000"},
		{Name: "pinf", Value: "+Inf"},
		{Name: "pi", Value: "3.141592653589793"},
		{Name: "tau", Value: "6.283185307179586"},
		{Name: "deg2rad", Value: "0.01745329238474369"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseConstants = %#v, want %#v", got, want)
	}
}

func TestParseConstantsErrors(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string
	}{
		{
			name:    "missing table",
			src:     "class C { }",
			wantErr: "AllConstants",
		},
		{
			name:    "declared length disagrees with the initializer",
			src:     "AllConstants = new Constant[2]\n{\n\tnew Constant(\"pi\", \"d\", Math.PI)\n};",
			wantErr: "declares 2 entries but the initializer has 1",
		},
		{
			name:    "unsupported value expression",
			src:     "AllConstants = new Constant[1]\n{\n\tnew Constant(\"x\", \"d\", SomeOtherThing.Value)\n};",
			wantErr: "unsupported double expression",
		},
		{
			name:    "too few arguments",
			src:     "AllConstants = new Constant[1]\n{\n\tnew Constant(\"x\", \"d\")\n};",
			wantErr: "expected at least 3 arguments",
		},
		{
			name:    "unrecognized entry",
			src:     "AllConstants = new Constant[1]\n{\n\tnull\n};",
			wantErr: "unrecognized entry",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseConstants(tt.src)
			checkErr(t, "parseConstants", err, tt.wantErr)
		})
	}
}

// TestCSharpDoubleFidelity pins the widened float literals and the derived
// constants, which must match the game bit for bit rather than being
// recomputed at full double precision.
func TestCSharpDoubleFidelity(t *testing.T) {
	tests := []struct {
		expr string
		want float64
	}{
		{expr: "Math.PI", want: math.Pi},
		{expr: "Math.PI * 2.0", want: 6.283185307179586},
		{expr: "double.Epsilon", want: math.SmallestNonzeroFloat64},
		{expr: "0.01745329238474369", want: 0.01745329238474369},
		{expr: "57.295780181884766", want: 57.295780181884766},
		{expr: "8.31446261815324", want: 8.31446261815324},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			got, err := csharpDouble(tt.expr)
			if err != nil {
				t.Fatalf("csharpDouble(%q): %v", tt.expr, err)
			}
			if math.Float64bits(got) != math.Float64bits(tt.want) {
				t.Errorf("csharpDouble(%q) = %v (bits %#x), want %v (bits %#x)",
					tt.expr, got, math.Float64bits(got), tt.want, math.Float64bits(tt.want))
			}
		})
	}

	for _, expr := range []string{"double.NaN", "double.PositiveInfinity", "double.NegativeInfinity"} {
		t.Run(expr, func(t *testing.T) {
			got, err := csharpDouble(expr)
			if err != nil {
				t.Fatalf("csharpDouble(%q): %v", expr, err)
			}
			if !math.IsNaN(got) && !math.IsInf(got, 0) {
				t.Errorf("csharpDouble(%q) = %v, want NaN or an infinity", expr, got)
			}
		})
	}
}

// TestFormatFloatRoundTrip confirms the JSON spelling of a value parses back
// to the same bits, which is what keeps the checked-in table exact.
func TestFormatFloatRoundTrip(t *testing.T) {
	values := []float64{
		dotnetNaN, math.NaN(), math.Inf(1), math.Inf(-1), math.Pi, 6.283185307179586,
		0.01745329238474369, 57.295780181884766, math.SmallestNonzeroFloat64, 8.31446261815324, 0,
	}
	for _, want := range values {
		t.Run(formatFloat(want), func(t *testing.T) {
			got, err := Constant{Name: "x", Value: formatFloat(want)}.Float()
			if err != nil {
				t.Fatalf("Float(): %v", err)
			}
			// NaN is compared by bits like everything else: the payload is what
			// this spelling exists to carry.
			if math.Float64bits(got) != math.Float64bits(want) {
				t.Errorf("round trip gave bits %#x, want %#x", math.Float64bits(got), math.Float64bits(want))
			}
		})
	}
}

// TestFloatRejectsABadNaNPayload covers the two ways the NaN spelling can be
// wrong in a hand-edited table.
func TestFloatRejectsABadNaNPayload(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr string
	}{
		{name: "unparseable payload", value: "NaN:0xnope", wantErr: "parse NaN payload"},
		{name: "payload is not a NaN", value: "NaN:0x3ff0000000000000", wantErr: "is not a NaN"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Constant{Name: "nan", Value: tt.value}.Float()
			checkErr(t, "Float", err, tt.wantErr)
		})
	}
}

func TestRenderExample(t *testing.T) {
	tests := []struct {
		name     string
		mnemonic string
		operands []Operand
		want     string
		wantErr  string
	}{
		{name: "no operands", mnemonic: "yield", want: "yield"},
		{
			name:     "named alternatives",
			mnemonic: "add",
			operands: []Operand{
				{Kinds: []string{kindRegister}},
				{Name: "a", Kinds: []string{kindRegister, kindNumber}},
				{Name: "b", Kinds: []string{kindRegister, kindNumber}},
			},
			want: "add r? a(r?|num) b(r?|num)",
		},
		{
			name:     "unnamed alternatives",
			mnemonic: "alias",
			operands: []Operand{{Kinds: []string{kindString}}, {Kinds: []string{kindRegister, kindDevice}}},
			want:     "alias str r?|d?",
		},
		{
			name:     "unknown kind",
			mnemonic: "bogus",
			operands: []Operand{{Kinds: []string{"nonesuch"}}},
			wantErr:  `unknown operand kind "nonesuch"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := renderExample(tt.mnemonic, tt.operands)
			if !checkErr(t, "renderExample", err, tt.wantErr) {
				return
			}
			if got != tt.want {
				t.Errorf("renderExample = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestOperandKindTablesAgree keeps the token, JSON, and Go name tables in step
// with each other; a kind missing from any of them would drop operands
// silently.
func TestOperandKindTablesAgree(t *testing.T) {
	if len(kindTokens) != len(helpTokenKinds) {
		t.Errorf("kindTokens has %d entries, helpTokenKinds has %d: two tokens map to one kind",
			len(kindTokens), len(helpTokenKinds))
	}
	for token, kind := range helpTokenKinds {
		if back := kindTokens[kind]; back != token {
			t.Errorf("kind %q maps back to token %q, want %q", kind, back, token)
		}
		if _, ok := goOperandKinds[kind]; !ok {
			t.Errorf("kind %q (token %q) has no internal/ic10 constant", kind, token)
		}
	}
	if len(goOperandKinds) != len(helpTokenKinds) {
		t.Errorf("goOperandKinds has %d entries, helpTokenKinds has %d", len(goOperandKinds), len(helpTokenKinds))
	}
}

// checkErr asserts the error against a wanted substring and reports whether
// the caller should go on to check the value. An empty wantErr demands success.
func checkErr(t *testing.T, what string, err error, wantErr string) bool {
	t.Helper()
	if wantErr == "" {
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", what, err)
		}
		return true
	}
	if err == nil {
		t.Fatalf("%s: got no error, want one containing %q", what, wantErr)
	}
	if !strings.Contains(err.Error(), wantErr) {
		t.Errorf("%s: error = %q, want it to contain %q", what, err.Error(), wantErr)
	}
	return false
}
