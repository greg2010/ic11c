package main

import (
	"bytes"
	"flag"
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

func TestRenderBasicEnumsGolden(t *testing.T) {
	got, err := renderBasicEnums(readFixture(t, "full.json"))
	if err != nil {
		t.Fatalf("renderBasicEnums: %v", err)
	}
	assertParses(t, got)
	compareGolden(t, filepath.Join("testdata", "full_basicenums.go.golden"), got)
}

func TestRenderBasicEnumsErrors(t *testing.T) {
	tests := []struct {
		name    string
		isa     *ISA
		wantErr string
	}{
		{
			name:    "no enums",
			isa:     &ISA{},
			wantErr: "basic enum table is empty",
		},
		{
			name:    "enum with no members",
			isa:     &ISA{BasicEnums: []BasicEnum{{Prefix: "Sound", Type: "SoundAlert"}}},
			wantErr: `basic enum "Sound" has no members`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := renderBasicEnums(tt.isa)
			checkErr(t, "renderBasicEnums", err, tt.wantErr)
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
			if gotImport := strings.Contains(text, `import "math"`); gotImport != tt.wantImport {
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

func TestGenerate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested")
	out := outputs{
		tables:  filepath.Join(dir, "tables.gen.go"),
		prelude: filepath.Join(dir, preludeFileName),
		flags:   filepath.Join(dir, "compile_flags.txt"),
		// One directory further down, so that the header the flags file names
		// is one the run had to work out rather than the bare file name.
		fixtureFlags: filepath.Join(dir, "corpus", "compile_flags.txt"),
		basicEnums:   filepath.Join(dir, "basicenums.gen.go"),
	}
	if err := generate(filepath.Join("testdata", "full.json"), out); err != nil {
		t.Fatalf("generate: %v", err)
	}

	isa := readFixture(t, "full.json")
	wantTables, err := renderTables(isa)
	if err != nil {
		t.Fatalf("renderTables: %v", err)
	}
	wantPrelude, err := renderPrelude(isa)
	if err != nil {
		t.Fatalf("renderPrelude: %v", err)
	}
	wantBasicEnums, err := renderBasicEnums(isa)
	if err != nil {
		t.Fatalf("renderBasicEnums: %v", err)
	}

	assertParses(t, readGenerated(t, out.tables))
	assertParses(t, readGenerated(t, out.basicEnums))
	for _, tt := range []struct {
		name string
		path string
		want []byte
	}{
		{name: "tables", path: out.tables, want: wantTables},
		{name: "prelude", path: out.prelude, want: wantPrelude},
		{name: "compile flags", path: out.flags, want: renderCompileFlags(preludeFileName)},
		{name: "fixture compile flags", path: out.fixtureFlags, want: renderCompileFlags("../" + preludeFileName)},
		{name: "basic enums", path: out.basicEnums, want: wantBasicEnums},
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

	tests := []struct {
		name    string
		in      string
		wantErr string
	}{
		{name: "missing file", in: filepath.Join(dir, "absent.json"), wantErr: "read"},
		{name: "malformed json", in: write("bad.json", "{"), wantErr: "decode"},
		{
			name:    "unknown field",
			in:      write("extra.json", `{"manifest":"1","version":"1","surprise":true}`),
			wantErr: `unknown field "surprise"`,
		},
		{
			name:    "empty table",
			in:      write("empty.json", `{"manifest":"1","version":"1"}`),
			wantErr: "instruction table is empty",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := outputs{
				tables:       filepath.Join(dir, "out.go"),
				prelude:      filepath.Join(dir, preludeFileName),
				flags:        filepath.Join(dir, "compile_flags.txt"),
				fixtureFlags: filepath.Join(dir, "corpus", "compile_flags.txt"),
				basicEnums:   filepath.Join(dir, "basicenums.gen.go"),
			}
			checkErr(t, "generate", generate(tt.in, out), tt.wantErr)
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
	path := filepath.Join("testdata", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	var isa ISA
	if err := decodeISA(data, &isa); err != nil {
		t.Fatalf("decode fixture %s: %v", path, err)
	}
	return &isa
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
