package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderPreludeGolden(t *testing.T) {
	got, err := renderPrelude(readFixture(t, "full.json"))
	if err != nil {
		t.Fatalf("renderPrelude: %v", err)
	}
	compareGolden(t, filepath.Join("testdata", "full_prelude.h.golden"), got)
}

func TestRenderPreludeContents(t *testing.T) {
	got, err := renderPrelude(readFixture(t, "full.json"))
	if err != nil {
		t.Fatalf("renderPrelude: %v", err)
	}
	text := string(got)

	tests := []struct {
		name    string
		snippet string
	}{
		{name: "generated marker", snippet: generatedHeader + "\n"},
		{name: "include guard", snippet: "#ifndef " + includeGuard},
		{name: "highest pin", snippet: ", d" + string(rune('0'+maxDevicePin)) + " } dev;"},
		{name: "deprecated attribute follows the name", snippet: `None [[deprecated("the game marks this logic type retired")]] = 0,`},
		{name: "shared name goes to the first family", snippet: "// None = 0 omitted; ic10_logic declares it as 0."},
		{name: "constants are constexpr", snippet: "constexpr double epsilon = 5e-324;"},
		{name: "device argument is typed", snippet: "double    __ic_load(dev d, ic10_logic t);"},
		{name: "string operand is a pointer", snippet: "long long __ic_hash(const char *s);"},
		{name: "operandless form", snippet: "void      __ic_yield(void);"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(text, tt.snippet) {
				t.Errorf("prelude does not contain %q", tt.snippet)
			}
		})
	}
}

// TestRenderPreludeOpensWithTheToolDirectives pins both directives to the top
// of the file and in that order: linters read the generated marker as the first
// line, and clang-format holds its own directive to end of file, so one further
// down would leave everything above it reflowable.
func TestRenderPreludeOpensWithTheToolDirectives(t *testing.T) {
	got, err := renderPrelude(readFixture(t, "full.json"))
	if err != nil {
		t.Fatalf("renderPrelude: %v", err)
	}
	lines := strings.Split(string(got), "\n")

	tests := []struct {
		name string
		line int
		want string
	}{
		{name: "generated marker", line: 0, want: generatedHeader},
		{name: "clang-format off", line: 1, want: clangFormatOff},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if len(lines) <= tt.line {
				t.Fatalf("prelude has %d lines", len(lines))
			}
			if lines[tt.line] != tt.want {
				t.Errorf("line %d is %q, want %q", tt.line+1, lines[tt.line], tt.want)
			}
		})
	}

	if strings.Contains(string(got), "// clang-format on") {
		t.Error("the prelude turns clang-format back on, which leaves what follows it reflowable")
	}
}

func TestRenderPreludeErrors(t *testing.T) {
	full := func() *ISA { return readFixture(t, "full.json") }

	tests := []struct {
		name    string
		isa     func() *ISA
		wantErr string
	}{
		{
			name:    "missing machine constant",
			isa:     func() *ISA { isa := full(); isa.Constants = nil; return isa },
			wantErr: `carries no constant "pi"`,
		},
		{
			name: "machine constant C cannot spell",
			isa: func() *ISA {
				isa := full()
				isa.Constants = append(isa.Constants, Constant{Name: "pi", Value: "+Inf"})
				return isa
			},
			wantErr: `constant "pi": no C literal spells +Inf`,
		},
		{
			name:    "empty family",
			isa:     func() *ISA { isa := full(); isa.ReagentModes = nil; return isa },
			wantErr: "carries no ic10_reagent members",
		},
		{
			name: "family wholly claimed by an earlier one",
			isa: func() *ISA {
				isa := full()
				isa.BatchModes = []EnumMember{{Name: "None", Value: 7}}
				return isa
			},
			wantErr: "every ic10_batch member is declared by an earlier family",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := renderPrelude(tt.isa())
			checkErr(t, "renderPrelude", err, tt.wantErr)
		})
	}
}

func TestCDouble(t *testing.T) {
	tests := []struct {
		name  string
		value float64
		want  string
	}{
		{name: "widened float literal", value: 0.01745329238474369, want: "0.01745329238474369"},
		{name: "smallest subnormal", value: 5e-324, want: "5e-324"},
		{name: "integral value takes a point", value: 8, want: "8.0"},
		{name: "negative integral value takes a point", value: -1, want: "-1.0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := cDouble(tt.value)
			if err != nil {
				t.Fatalf("cDouble(%v): %v", tt.value, err)
			}
			if got != tt.want {
				t.Errorf("cDouble(%v) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

// TestRenderCompileFlagsIsAnArgumentPerLine pins the format a driver reads the
// file in. -include and its argument on one line is the mistake this catches:
// the driver takes the whole line as one argument and reports no such file.
func TestRenderCompileFlagsIsAnArgumentPerLine(t *testing.T) {
	tests := []struct {
		name   string
		header string
	}{
		{name: "beside the header", header: preludeFileName},
		{name: "elsewhere in the tree", header: "../../ic10/" + preludeFileName},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(renderCompileFlags(tt.header))
			if !strings.HasSuffix(got, "\n") {
				t.Errorf("compile flags do not end in a newline: %q", got)
			}
			lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
			for _, line := range lines {
				if strings.ContainsAny(line, " \t") {
					t.Errorf("line %q holds more than one argument", line)
				}
			}
			if len(lines) < 2 || lines[len(lines)-2] != "-include" || lines[len(lines)-1] != tt.header {
				t.Errorf("compile flags do not end with -include and %s on separate lines: %q", tt.header, lines)
			}
		})
	}
}

// TestRenderFlagsForNamesTheHeaderRelativeToTheFlagsFile pins the paths the
// checked-in argument files carry. A driver resolves the -include argument
// against the directory holding the flags file, so a path that reads correctly
// from the module root is the mistake this catches.
func TestRenderFlagsForNamesTheHeaderRelativeToTheFlagsFile(t *testing.T) {
	tests := []struct {
		name  string
		flags string
		want  string
	}{
		{name: "beside the header", flags: defaultFlagsPath, want: preludeFileName},
		{name: "beside the corpus", flags: defaultFixtureFlagsPath, want: "../../ic10/" + preludeFileName},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := renderFlagsFor(tt.flags, defaultPreludePath)
			if err != nil {
				t.Fatalf("renderFlagsFor(%s): %v", tt.flags, err)
			}
			if want := string(renderCompileFlags(tt.want)); string(got) != want {
				t.Errorf("renderFlagsFor(%s) = %q, want %q", tt.flags, got, want)
			}
		})
	}
}

// TestIntrinsicPrototypesAreDistinct guards the one thing the C compiler cannot
// catch about this list: a duplicate name declares the same function twice with
// two signatures, which the header would then fail to compile on.
func TestIntrinsicPrototypesAreDistinct(t *testing.T) {
	seen := make(map[string]bool)
	for _, prototype := range intrinsicPrototypes() {
		if seen[prototype.name] {
			t.Errorf("%s is declared more than once", prototype.name)
		}
		seen[prototype.name] = true
		if !strings.HasPrefix(prototype.name, "__ic_") {
			t.Errorf("%s does not carry the reserved intrinsic prefix", prototype.name)
		}
	}
}
