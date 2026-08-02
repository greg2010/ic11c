package ic10

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// fixturePreludePath is the -include argument the corpus's own argument file
// carries: the one checked-in header, reached from the directory that file sits
// in. Naming it rather than copying the header beside the corpus is what leaves
// nothing there to drift.
const fixturePreludePath = "../../ic10/" + PreludeFileName

// fixtureDir holds the MicroC programs the parser tests exercise. They are
// whole programs written the way the language is meant to be written, which is
// what makes them the corpus the subset claim is worth checking against.
const fixtureDir = "../parser/testdata"

// cCompilers are the drivers the gate runs, in order of preference. clang is
// first because it is what compile_flags.txt targets and what CI installs
// alongside the libLLVM the compiler links.
//
// cc is deliberately absent. It is a symlink to one of these wherever it
// exists, and the name does not say which, so running it would leave the
// extension set under test unknown.
var cCompilers = []string{"clang", "gcc"}

// gateFlags are what compile_flags.txt cannot carry, because an editor wants
// the opposite of each. Deprecation warnings are the point of the attributes
// the header emits and are noise here, since a retired logic type still
// resolves on the chip.
//
// -pedantic-errors is what gives the gate its teeth: without it clang accepts
// several GNU extensions in silence that gcc rejects outright, so a program
// could pass on one machine and fail on the other.
var gateFlags = []string{
	"-fsyntax-only",
	"-pedantic-errors",
	"-Wno-deprecated-declarations",
}

// TestParserFixturesCompileAsC checks that every program the parser accepts is
// also a C program. That is the whole of the subset claim, and it is what lets
// an editor read a MicroC source file as C.
//
// It keys on the compiler's exit status rather than on silence. A program
// naming a logic type the game retired warns and is still valid MicroC, so
// diagnostics are reported and only a rejection fails.
func TestParserFixturesCompileAsC(t *testing.T) {
	compiler := findCCompiler(t)
	args := gateArgs(t)

	paths, err := filepath.Glob(filepath.Join(fixtureDir, "*.c"))
	if err != nil {
		t.Fatalf("globbing %s: %v", fixtureDir, err)
	}
	if len(paths) == 0 {
		t.Fatalf("no programs found in %s", fixtureDir)
	}

	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			out, err := exec.CommandContext(t.Context(), compiler, append(slices.Clone(args), path)...).CombinedOutput()
			_, rejected := errors.AsType[*exec.ExitError](err)
			switch {
			case rejected:
				t.Errorf("%s rejected a program MicroC accepts:\n%s", compiler, out)
			case err != nil:
				t.Fatalf("running %s: %v", compiler, err)
			case len(out) > 0:
				t.Logf("%s accepted %s with diagnostics:\n%s", compiler, filepath.Base(path), out)
			}
		})
	}
}

// findCCompiler returns the C driver to run the gate with.
//
// An absent compiler fails the test rather than skipping it. A skip is
// invisible, since `go test` discards a passing package's output, so a run that
// checked nothing would read exactly like one that checked everything — which
// is the failure mode this gate exists to rule out.
func findCCompiler(t *testing.T) string {
	t.Helper()
	for _, name := range cCompilers {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	t.Fatalf("none of %s is on PATH, and the conformance gate cannot run without one; install clang, which is what %s targets, or gcc",
		strings.Join(cCompilers, ", "), CompileFlagsFileName)
	return ""
}

// gateArgs renders the argv the gate runs. The base is CompileFlags itself, so
// what an editor is configured with is what gets exercised, with the flags
// file's relative header path resolved against this directory.
func gateArgs(t *testing.T) []string {
	t.Helper()
	header, err := filepath.Abs(PreludeFileName)
	if err != nil {
		t.Fatalf("resolving %s: %v", PreludeFileName, err)
	}

	args := slices.Clone(gateFlags)
	included := 0
	for line := range strings.Lines(CompileFlags) {
		arg := strings.TrimSpace(line)
		if arg == "" {
			continue
		}
		if arg == PreludeFileName {
			args = append(args, header)
			included++
			continue
		}
		args = append(args, arg)
	}
	if included != 1 {
		t.Fatalf("%s names %s %d times, want once; the two are generated together and neither works without the other",
			CompileFlagsFileName, PreludeFileName, included)
	}
	return args
}

// TestCompileFlagsIncluding checks the substitution against the one thing it
// has to preserve: everything before the -include argument, verbatim.
func TestCompileFlagsIncluding(t *testing.T) {
	tests := []struct {
		name   string
		header string
	}{
		{name: "beside the flags file", header: PreludeFileName},
		{name: "under the generated directory", header: PreludeDirName + "/" + PreludeFileName},
		{name: "elsewhere in the tree", header: fixturePreludePath},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CompileFlagsIncluding(tt.header)
			if err != nil {
				t.Fatalf("CompileFlagsIncluding(%q): %v", tt.header, err)
			}
			lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
			if len(lines) < 2 || lines[len(lines)-2] != "-include" || lines[len(lines)-1] != tt.header {
				t.Fatalf("CompileFlagsIncluding(%q) does not end with -include and the header on separate lines: %q", tt.header, lines)
			}
			wantPrefix := strings.Split(strings.TrimSuffix(CompileFlags, "\n"), "\n")
			if !slices.Equal(lines[:len(lines)-1], wantPrefix[:len(wantPrefix)-1]) {
				t.Errorf("CompileFlagsIncluding(%q) changed an argument other than the header:\n%s", tt.header, got)
			}
		})
	}
}

// TestCompileFlagsIncludingRejectsAnUnrecognizedForm covers the guard on the
// embedded file's shape. The substitution is a suffix replacement, so a flags
// file that stopped ending in the bare header name has to be reported rather
// than have a second header path appended to it.
func TestCompileFlagsIncludingRejectsAnUnrecognizedForm(t *testing.T) {
	tests := []struct {
		name  string
		flags string
	}{
		{name: "empty", flags: ""},
		{name: "no trailing newline", flags: "-include\n" + PreludeFileName},
		{name: "names another header", flags: "-include\nsomething_else.h\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := compileFlagsIncluding(tt.flags, PreludeFileName)
			if err == nil {
				t.Fatalf("compileFlagsIncluding(%q) succeeded, want an error", tt.flags)
			}
			if !strings.Contains(err.Error(), PreludeFileName) {
				t.Errorf("error %q does not name %s", err, PreludeFileName)
			}
		})
	}
}

// TestFixtureCompileFlagsReachThePrelude checks the argument file that
// configures an editor opening a corpus program against the one beside the
// header. The two are generated together and differ in exactly the header path,
// so a hand edit to either is what this notices.
//
// That the flags themselves work is [TestParserFixturesCompileAsC]'s job, which
// runs the corpus through the file this one is equal to.
func TestFixtureCompileFlagsReachThePrelude(t *testing.T) {
	path := filepath.Join(fixtureDir, CompileFlagsFileName)
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	want, err := CompileFlagsIncluding(fixturePreludePath)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if string(got) != want {
		t.Errorf("%s reads\n%s\nand the flags beside the header call for\n%s", path, got, want)
	}

	if _, err := os.Stat(filepath.Join(fixtureDir, fixturePreludePath)); err != nil {
		t.Errorf("%s includes %s, which does not resolve from %s, the directory a C driver resolves it against: %v",
			path, fixturePreludePath, fixtureDir, err)
	}
}

// enumeratorPattern matches one declared enumerator in the generated header. A
// name a later family gives up appears as a comment instead and does not match,
// which is what makes this a check on the declarations rather than on the text.
var enumeratorPattern = regexp.MustCompile(`(?m)^ {4}([A-Za-z_][A-Za-z0-9_]*)(?: \[\[deprecated\("[^"]*"\)\]\])? = -?\d+,$`)

// TestPreludeDeclaresEveryOperandName checks the checked-in header against the
// checked-in tables. Regeneration is what keeps the two in step; this is what
// notices a hand edit to either, which no amount of regeneration would.
func TestPreludeDeclaresEveryOperandName(t *testing.T) {
	declared := make(map[string]bool)
	for _, match := range enumeratorPattern.FindAllStringSubmatch(Prelude, -1) {
		declared[match[1]] = true
	}

	tests := []struct {
		family string
		names  []string
	}{
		{family: "logic type", names: names(LogicTypes, func(t LogicTypeInfo) string { return t.Name })},
		{family: "slot type", names: names(LogicSlotTypes, func(t LogicSlotTypeInfo) string { return t.Name })},
		{family: "batch mode", names: names(BatchModes, func(m BatchModeInfo) string { return m.Name })},
		{family: "reagent mode", names: names(ReagentModes, func(m ReagentModeInfo) string { return m.Name })},
	}

	distinct := make(map[string]bool)
	for _, tt := range tests {
		t.Run(tt.family, func(t *testing.T) {
			for _, name := range tt.names {
				distinct[name] = true
				if !declared[name] {
					t.Errorf("%s %s is in the tables and %s declares no such enumerator", tt.family, name, PreludeFileName)
				}
			}
		})
	}
	for name := range declared {
		if !distinct[name] {
			t.Errorf("%s declares %s, which no operand table carries", PreludeFileName, name)
		}
	}
}

func names[T any](members []T, name func(T) string) []string {
	out := make([]string, len(members))
	for i, member := range members {
		out[i] = name(member)
	}
	return out
}
