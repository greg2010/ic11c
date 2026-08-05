package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// updateGolden rewrites the expected output of TestRenderTablesGolden. Review
// the resulting diff: it is the only check on what the tables actually say.
var updateGolden = flag.Bool("update", false, "rewrite the testdata golden files")

func TestRenderTablesGolden(t *testing.T) {
	tests := []struct {
		name string
		// fixture and golden name files under testdata.
		fixture string
		golden  string
	}{
		{
			name:    "finite constants need no math import",
			fixture: "minimal.json",
			golden:  "minimal_tables.go.golden",
		},
		{
			name:    "every operand kind, deprecated members, and non-finite constants",
			fixture: "full.json",
			golden:  "full_tables.go.golden",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isa := readFixture(t, tt.fixture)
			got, err := renderTables(isa)
			if err != nil {
				t.Fatalf("renderTables: %v", err)
			}
			assertParses(t, got)
			compareGolden(t, filepath.Join("testdata", tt.golden), got)
		})
	}
}

func TestRenderDevicesGolden(t *testing.T) {
	got, err := renderDevices(readDevicesFixture(t, "devices.json"), readFixture(t, "devices_isa.json"))
	if err != nil {
		t.Fatalf("renderDevices: %v", err)
	}
	assertParses(t, got)
	compareGolden(t, filepath.Join("testdata", "devices.go.golden"), got)
}

func TestRenderDevicesErrors(t *testing.T) {
	tests := []struct {
		name    string
		devices *Devices
		wantErr string
	}{
		{
			name:    "no prefabs",
			devices: &Devices{},
			wantErr: "prefab table is empty",
		},
		{
			name:    "unnamed prefab",
			devices: &Devices{Prefabs: []Prefab{{Hash: 1}}},
			wantErr: "unnamed entry",
		},
		{
			name: "property outside the ISA enumeration",
			devices: &Devices{
				Prefabs: []Prefab{{Name: "A", Logic: []LogicAccess{
					{Name: "Nonexistent", Access: accessRead},
				}}},
			},
			wantErr: "not a member of the ISA enumeration",
		},
		{
			name:    "slot listed out of order",
			devices: &Devices{Prefabs: []Prefab{{Name: "A", Slots: []PrefabSlot{{Index: 1, Class: "Helmet"}}}}},
			wantErr: "the generated table indexes slots by position",
		},
		{
			name:    "mode listed out of order",
			devices: &Devices{Prefabs: []Prefab{{Name: "A", Modes: []Mode{{Value: 1, Name: "Second"}}}}},
			wantErr: "the generated table indexes modes by position",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := renderDevices(tt.devices, readFixture(t, "devices_isa.json"))
			checkErr(t, "renderDevices", err, tt.wantErr)
		})
	}
}

func TestRenderTablesHeaderAndImports(t *testing.T) {
	tests := []struct {
		name        string
		fixture     string
		wantImport  bool
		wantSnippet []string
	}{
		{
			name:        "minimal",
			fixture:     "minimal.json",
			wantImport:  false,
			wantSnippet: []string{`const ManifestID = "1000"`, `const GameVersion = "0.0.1.2"`, `Opcode: OpMove`, `Opcode: OpYield`},
		},
		{
			name:        "full",
			fixture:     "full.json",
			wantImport:  true,
			wantSnippet: []string{`{Name: "nan", Value: math.Float64frombits(0xfff8000000000000)}`, `{Name: "pinf", Value: math.Inf(1)}`, `{Name: "ninf", Value: math.Inf(-1)}`, `Deprecated: true`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := renderTables(readFixture(t, tt.fixture))
			if err != nil {
				t.Fatalf("renderTables: %v", err)
			}
			text := string(got)
			if !strings.HasPrefix(text, generatedHeader+"\n") {
				t.Errorf("output does not start with %q; linters key off that exact line", generatedHeader)
			}
			if gotImport := strings.Contains(text, `"math"`); gotImport != tt.wantImport {
				t.Errorf("math import present = %v, want %v", gotImport, tt.wantImport)
			}
			for _, snippet := range tt.wantSnippet {
				if !strings.Contains(text, snippet) {
					t.Errorf("output does not contain %q", snippet)
				}
			}
		})
	}
}

// TestRenderUndeterminedOperand covers the two spellings extraction refuses to
// write. A hand-edited table can still hold them, and rendering them as the
// constants that say so makes the compiler refuse the instruction rather than
// allocate around a guess.
func TestRenderUndeterminedOperand(t *testing.T) {
	got, err := renderOperands(Instruction{
		Mnemonic: "swap",
		Operands: []Operand{{Name: "a", Kinds: []string{kindRegister}, Direction: DirectionUnknown, Conversion: ConversionUnknown}},
	})
	if err != nil {
		t.Fatalf("renderOperands: %v", err)
	}
	want := `[]Operand{{Name: "a", Kinds: []OperandKind{OperandRegister}, Direction: DirectionUnknown, Conversion: ConversionUnknown}}`
	if got != want {
		t.Errorf("renderOperands = %s, want %s", got, want)
	}
}

// TestRenderUndecidedCircuitHolder covers the prefab whose class extends a base
// the extraction could not place. An ordinary entry would state that the thing
// holds no chip, which is the reading the extraction refused to make.
func TestRenderUndecidedCircuitHolder(t *testing.T) {
	devices := &Devices{Prefabs: []Prefab{{Name: "StructureMount", CircuitHolderUnknown: true}}}
	got, err := renderDevices(devices, readFixture(t, "devices_isa.json"))
	if err != nil {
		t.Fatalf("renderDevices: %v", err)
	}
	want := `{Name: "StructureMount", Hash: 0, CircuitHolderUnknown: true},`
	if !strings.Contains(string(got), want) {
		t.Errorf("rendered table does not contain %s", want)
	}
}

func TestRenderTablesErrors(t *testing.T) {
	tests := []struct {
		name    string
		isa     *ISA
		wantErr string
	}{
		{
			name:    "no instructions",
			isa:     &ISA{},
			wantErr: "instruction table is empty",
		},
		{
			name: "opcodes are not dense",
			isa: &ISA{Instructions: []Instruction{
				{Mnemonic: "move", Opcode: 0},
				{Mnemonic: "yield", Opcode: 7},
			}},
			wantErr: "opcodes must be dense and ordered",
		},
		{
			name: "unknown operand kind",
			isa: &ISA{Instructions: []Instruction{
				{Mnemonic: "move", Opcode: 0, Operands: []Operand{{Kinds: []string{"nonesuch"}}}},
			}},
			wantErr: `unknown kind "nonesuch"`,
		},
		{
			name: "operand without kinds",
			isa: &ISA{Instructions: []Instruction{
				{Mnemonic: "move", Opcode: 0, Operands: []Operand{{Name: "a"}}},
			}},
			wantErr: "has no kinds",
		},
		{
			name: "operand with no direction",
			isa: &ISA{Instructions: []Instruction{
				{Mnemonic: "move", Opcode: 0, Operands: []Operand{{Kinds: []string{kindRegister}}}},
			}},
			wantErr: `unknown direction ""`,
		},
		{
			name: "unparsable constant",
			isa: &ISA{
				Instructions: []Instruction{{Mnemonic: "move", Opcode: 0}},
				Constants:    []Constant{{Name: "pi", Value: "three"}},
			},
			wantErr: `constant "pi": parse value "three"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := renderTables(tt.isa)
			checkErr(t, "renderTables", err, tt.wantErr)
		})
	}
}

// TestGenerate runs the whole stage over the checked-in tables, since generation
// holds its inputs to the shape extraction wrote them in and no readable fixture
// satisfies that. It settles that each renderer's bytes reach the path meant for
// them, not what a renderer produces -- check:codegen holds those.
func TestGenerate(t *testing.T) {
	isaPath := filepath.Join(moduleRoot, defaultJSONPath)
	devicesPath := filepath.Join(moduleRoot, defaultDevicesJSONPath)

	dir := filepath.Join(t.TempDir(), "nested")
	out := outputs{
		tables:  filepath.Join(dir, "tables.gen.go"),
		devices: filepath.Join(dir, "devices.gen.go"),
		prelude: filepath.Join(dir, preludeFileName),
		flags:   filepath.Join(dir, "compile_flags.txt"),
		// One directory further down, so that the header the flags file names
		// is one the run had to work out rather than the bare file name.
		fixtureFlags: filepath.Join(dir, "corpus", "compile_flags.txt"),
	}
	if err := generate(isaPath, devicesPath, out); err != nil {
		t.Fatalf("generate: %v", err)
	}

	isa, err := readJSON[ISA](isaPath)
	if err != nil {
		t.Fatalf("read checked-in ISA: %v", err)
	}
	extracted, err := readJSON[Devices](devicesPath)
	if err != nil {
		t.Fatalf("read checked-in device tables: %v", err)
	}
	wantTables, err := renderTables(isa)
	if err != nil {
		t.Fatalf("renderTables: %v", err)
	}
	wantDevices, err := renderDevices(extracted, isa)
	if err != nil {
		t.Fatalf("renderDevices: %v", err)
	}
	wantPrelude, err := renderPrelude(isa)
	if err != nil {
		t.Fatalf("renderPrelude: %v", err)
	}
	assertParses(t, readGenerated(t, out.tables))
	assertParses(t, readGenerated(t, out.devices))
	for _, tt := range []struct {
		name string
		path string
		want []byte
	}{
		{name: "tables", path: out.tables, want: wantTables},
		{name: "device tables", path: out.devices, want: wantDevices},
		{name: "prelude", path: out.prelude, want: wantPrelude},
		{name: "compile flags", path: out.flags, want: renderCompileFlags(preludeFileName)},
		{name: "fixture compile flags", path: out.fixtureFlags, want: renderCompileFlags("../" + preludeFileName)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := readGenerated(t, tt.path); !bytes.Equal(got, tt.want) {
				t.Errorf("generate wrote something other than the renderer produced:\n%s", got)
			}
		})
	}
}

func readGenerated(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read generated file: %v", err)
	}
	return data
}

func TestGenerateInputErrors(t *testing.T) {
	dir := t.TempDir()
	write := func(name, contents string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		return path
	}

	// The two inputs are read and cross-checked before anything is rendered,
	// so every case names both.
	const oneBuild = `{"manifest":"1","version":"1"`
	goodDevices := write("devices.json", oneBuild+`,"reagents":[{"name":"Flour","hash":1}]}`)

	checkedInISA := filepath.Join(moduleRoot, defaultJSONPath)
	isa, err := readJSON[ISA](checkedInISA)
	if err != nil {
		t.Fatalf("read checked-in ISA: %v", err)
	}
	shortDevices := write("shortdevices.json",
		fmt.Sprintf(`{"manifest":%q,"version":%q,"reagents":[{"name":"Flour","hash":-811006991}]}`, isa.Manifest, isa.Version))

	tests := []struct {
		name    string
		in      string
		devices string
		wantErr string
	}{
		{name: "missing ISA", in: filepath.Join(dir, "absent.json"), devices: goodDevices, wantErr: "read"},
		{name: "malformed ISA", in: write("bad.json", "{"), devices: goodDevices, wantErr: "decode"},
		{
			name:    "unknown ISA field",
			in:      write("extra.json", `{"manifest":"1","version":"1","surprise":true}`),
			devices: goodDevices,
			wantErr: `unknown field "surprise"`,
		},
		{
			name:    "missing device tables",
			in:      write("empty.json", oneBuild+`}`),
			devices: filepath.Join(dir, "absent.json"),
			wantErr: "read",
		},
		{
			name:    "unknown device field",
			in:      write("empty2.json", oneBuild+`}`),
			devices: write("extradevices.json", oneBuild+`,"surprise":true}`),
			wantErr: `unknown field "surprise"`,
		},
		{
			name:    "tables from two builds",
			in:      write("empty3.json", oneBuild+`}`),
			devices: write("otherbuild.json", `{"manifest":"2","version":"1","reagents":[{"name":"Flour","hash":1}]}`),
			wantErr: "different game builds",
		},
		{
			name:    "ISA table of the wrong shape",
			in:      write("empty4.json", oneBuild+`}`),
			devices: goodDevices,
			wantErr: "instructions: got 0, want",
		},
		{
			// The ISA half is whole and the device half is not, which is the
			// half check:codegen would otherwise render straight through.
			name:    "device tables of the wrong shape",
			in:      checkedInISA,
			devices: shortDevices,
			wantErr: "reagents: got 1, want",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := outputs{
				tables:       filepath.Join(dir, "out.go"),
				devices:      filepath.Join(dir, "devices.gen.go"),
				prelude:      filepath.Join(dir, preludeFileName),
				flags:        filepath.Join(dir, "compile_flags.txt"),
				fixtureFlags: filepath.Join(dir, "corpus", "compile_flags.txt"),
			}
			checkErr(t, "generate", generate(tt.in, tt.devices, out), tt.wantErr)
		})
	}
}

func TestOpcodeConst(t *testing.T) {
	tests := []struct{ mnemonic, want string }{
		{mnemonic: "l", want: "OpL"},
		{mnemonic: "add", want: "OpAdd"},
		{mnemonic: "atan2", want: "OpAtan2"},
		{mnemonic: "lbns", want: "OpLbns"},
	}
	for _, tt := range tests {
		t.Run(tt.mnemonic, func(t *testing.T) {
			if got := opcodeConst(tt.mnemonic); got != tt.want {
				t.Errorf("opcodeConst(%q) = %q, want %q", tt.mnemonic, got, tt.want)
			}
		})
	}
}

func TestGoFloat(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "nan", value: "NaN:0xfff8000000000000", want: "math.Float64frombits(0xfff8000000000000)"},
		{name: "positive infinity", value: "+Inf", want: "math.Inf(1)"},
		{name: "negative infinity", value: "-Inf", want: "math.Inf(-1)"},
		{name: "smallest subnormal", value: "5e-324", want: "5e-324"},
		{name: "widened float literal", value: "0.01745329238474369", want: "0.01745329238474369"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := Constant{Name: tt.name, Value: tt.value}.Float()
			if err != nil {
				t.Fatalf("Float(): %v", err)
			}
			if got := goFloat(v); got != tt.want {
				t.Errorf("goFloat(%v) = %q, want %q", v, got, tt.want)
			}
		})
	}
}

func readFixture(t *testing.T, name string) *ISA {
	t.Helper()
	isa, err := readJSON[ISA](filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return isa
}

func readDevicesFixture(t *testing.T, name string) *Devices {
	t.Helper()
	d, err := readJSON[Devices](filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return d
}

// assertParses confirms the generated text is syntactically valid Go, which
// catches a malformed literal before it reaches the ic10 package.
func assertParses(t *testing.T, src []byte) {
	t.Helper()
	if _, err := parser.ParseFile(token.NewFileSet(), "tables.gen.go", src, parser.SkipObjectResolution); err != nil {
		t.Fatalf("generated source does not parse: %v", err)
	}
}

func compareGolden(t *testing.T, path string, got []byte) {
	t.Helper()
	if *updateGolden {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("update golden %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (rerun with -update to create it)", path, err)
	}
	if bytes.Equal(got, want) {
		return
	}
	t.Errorf("generated output differs from %s; rerun with -update after reviewing.\ngot:\n%s\nwant:\n%s", path, got, want)
}
