package ic10

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
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

// cCompilers are the drivers the gate runs. Every one of them found on PATH is
// run, because the claim the gate makes is about C rather than about a driver,
// and the two disagree about which extensions are C. clang is named first
// because it is what compile_flags.txt targets and what CI installs alongside
// the libLLVM the compiler links, so its diagnostics are the ones a reader sees
// first.
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
// naming a logic type the game retired warns and is still valid MicroC, and so
// does one carrying the prefab attribute, which C requires a toolchain that
// does not know the namespace to ignore; both are the header working. What the
// names the corpus writes resolve to is not read out of a driver's diagnostics
// either: [TestPreludeEnumeratorsResolveAsMicroCDoes] asserts every one of them
// through the driver itself, so a collision between two operand families fails
// there rather than turning up as text here.
func TestParserFixturesCompileAsC(t *testing.T) {
	args := gateArgs(t)

	paths, err := filepath.Glob(filepath.Join(fixtureDir, "*.c"))
	if err != nil {
		t.Fatalf("globbing %s: %v", fixtureDir, err)
	}
	if len(paths) == 0 {
		t.Fatalf("no programs found in %s", fixtureDir)
	}

	for _, compiler := range findCCompilers(t) {
		t.Run(filepath.Base(compiler), func(t *testing.T) {
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
		})
	}
}

// findCCompilers returns every C driver on PATH to run the gate with.
//
// Every one is run rather than the first found. The gate's claim is that the
// corpus is C, and the drivers disagree about what that is -- which is the
// reason -pedantic-errors is passed at all. Asking whichever one happens to be
// installed first would make the claim a claim about that driver, and would go
// on reading as a pass on a machine carrying both.
//
// No compiler at all fails the test rather than skipping it. A skip is
// invisible, since `go test` discards a passing package's output, so a run that
// checked nothing would read exactly like one that checked everything — which
// is the failure mode this gate exists to rule out.
func findCCompilers(t *testing.T) []string {
	t.Helper()
	var found []string
	for _, name := range cCompilers {
		if path, err := exec.LookPath(name); err == nil {
			found = append(found, path)
		}
	}
	if len(found) == 0 {
		t.Fatalf("none of %s is on PATH, and the conformance gate cannot run without one; install clang, which is what %s targets, or gcc",
			strings.Join(cCompilers, ", "), CompileFlagsFileName)
	}
	return found
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

// enumeratorPattern matches one declared enumerator in the generated header and
// the value it is declared with. A name a later family gives up appears as a
// comment instead and does not match, which is what makes this a check on the
// declarations rather than on the text.
var enumeratorPattern = regexp.MustCompile(`(?m)^ {4}([A-Za-z_][A-Za-z0-9_]*)(?: \[\[deprecated\("[^"]*"\)\]\])? = (-?\d+),$`)

// preludeMember is an operand name paired with the value something resolves it
// to: the header for a declared enumerator, an operand table for a family.
type preludeMember struct {
	name  string
	value int64
}

// preludeFamily is one operand family, which is also one position. An intrinsic
// parameter typed with the family's typedef is where a name standing in that
// position resolves against the family's table.
type preludeFamily struct {
	position string
	typedef  string
	prefix   string
	members  []preludeMember
}

// preludeFamilies lists the four families in the order the header claims names
// in, read out of the generated tables.
//
// Nothing about the name set is written down here. A name the game adds, moves
// or drops reaches both checks below through the table it lands in, which is
// what keeps them from going stale against a re-extracted build.
func preludeFamilies() []preludeFamily {
	return []preludeFamily{
		{position: "logic type", typedef: "ic10_logic", prefix: LogicTypePrefix, members: preludeMembers(LogicTypes, func(t LogicTypeInfo) (string, int64) { return t.Name, int64(t.Value) })},
		{position: "slot type", typedef: "ic10_slot", prefix: SlotTypePrefix, members: preludeMembers(LogicSlotTypes, func(t LogicSlotTypeInfo) (string, int64) { return t.Name, int64(t.Value) })},
		{position: "batch mode", typedef: "ic10_batch", prefix: BatchModePrefix, members: preludeMembers(BatchModes, func(m BatchModeInfo) (string, int64) { return m.Name, int64(m.Value) })},
		{position: "reagent mode", typedef: "ic10_reagent", prefix: ReagentModePrefix, members: preludeMembers(ReagentModes, func(m ReagentModeInfo) (string, int64) { return m.Name, int64(m.Value) })},
	}
}

// preludeSpelling is the name a position resolves one of its members by: the
// bare one where this family is the first to carry it, and the prefixed one
// everywhere else. It is the resolution rule internal/sema implements, written
// against the tables rather than against the header, so the two are compared
// rather than one read out of the other.
func preludeSpelling(families []preludeFamily, family preludeFamily, name string) string {
	for _, earlier := range families {
		if earlier.typedef == family.typedef {
			return name
		}
		for _, member := range earlier.members {
			if member.name == name {
				return family.prefix + name
			}
		}
	}
	return name
}

func preludeMembers[T any](entries []T, split func(T) (string, int64)) []preludeMember {
	out := make([]preludeMember, len(entries))
	for i, entry := range entries {
		out[i].name, out[i].value = split(entry)
	}
	return out
}

// declaredEnumerators reads the enumerators the checked-in header declares, in
// the order it declares them.
//
// A duplicate name is fatal rather than reported: C rejects a scope declaring
// one twice, so a header holding one describes nothing, and every check built
// on the parse would be comparing against a value no driver agrees with.
func declaredEnumerators(t *testing.T) []preludeMember {
	t.Helper()
	var declared []preludeMember
	seen := make(map[string]bool)
	for _, match := range enumeratorPattern.FindAllStringSubmatch(Prelude, -1) {
		value, err := strconv.ParseInt(match[2], 10, 64)
		if err != nil {
			t.Fatalf("%s declares %s with the value %q: %v", PreludeFileName, match[1], match[2], err)
		}
		if seen[match[1]] {
			t.Fatalf("%s declares %s twice", PreludeFileName, match[1])
		}
		seen[match[1]] = true
		declared = append(declared, preludeMember{name: match[1], value: value})
	}
	if len(declared) == 0 {
		t.Fatalf("%s declares no enumerators", PreludeFileName)
	}
	return declared
}

// TestPreludeDeclaresEveryOperandName checks the checked-in header against the
// checked-in tables. Regeneration is what keeps the two in step; this is what
// notices a hand edit to either, which no amount of regeneration would.
//
// It is a check on which spellings exist and on nothing else. What they mean is
// [TestPreludeEnumeratorsResolveAsMicroCDoes].
func TestPreludeDeclaresEveryOperandName(t *testing.T) {
	declared := make(map[string]bool)
	for _, enumerator := range declaredEnumerators(t) {
		declared[enumerator.name] = true
	}

	families := preludeFamilies()
	spelled := make(map[string]bool)
	for _, family := range families {
		t.Run(family.position, func(t *testing.T) {
			for _, member := range family.members {
				spelling := preludeSpelling(families, family, member.name)
				spelled[spelling] = true
				if !declared[spelling] {
					t.Errorf("%s %s is in the tables and %s declares no enumerator %s", family.position, member.name, PreludeFileName, spelling)
				}
			}
		})
	}
	for name := range declared {
		if !spelled[name] {
			t.Errorf("%s declares %s, which is no operand table's spelling of anything", PreludeFileName, name)
		}
	}
}

// TestPreludeEnumeratorsResolveAsMicroCDoes checks what the header means, which
// is a different claim from the one every other check here makes.
//
// C has one enumerator namespace per scope, so a language resolving an operand
// name against the position it stands in could not be a subset of it. MicroC
// resolves in that one namespace instead: the header gives each shared name to
// the first family carrying it and spells the later families' members with a
// prefix, and the compiler resolves exactly those spellings. This is what holds
// the compiler's rule to the header a native build reads -- a position whose
// spelling of a member C numbered differently is one where an editor and the
// compiler would disagree about what a program says with nothing reporting it.
//
// The comparison is per position rather than per name, because a name means one
// thing to C and the positions are where the two languages could part company.
func TestPreludeEnumeratorsResolveAsMicroCDoes(t *testing.T) {
	declared := declaredEnumerators(t)
	for _, compiler := range findCCompilers(t) {
		t.Run(filepath.Base(compiler), func(t *testing.T) {
			confirmDeclaredValues(t, compiler, declared)
		})
	}

	inC := make(map[string]int64, len(declared))
	for _, enumerator := range declared {
		inC[enumerator.name] = enumerator.value
	}

	families := preludeFamilies()
	for _, family := range families {
		t.Run(family.position, func(t *testing.T) {
			for _, member := range family.members {
				spelling := preludeSpelling(families, family, member.name)
				value, ok := inC[spelling]
				if !ok {
					t.Errorf("the %s position spells %s %s, and %s declares no such enumerator, so what C means by it cannot be compared",
						family.position, member.name, spelling, PreludeFileName)
					continue
				}
				if value != member.value {
					t.Errorf("%s in the %s position (%s): C resolves it to %d, MicroC resolves it to %d",
						spelling, family.position, family.typedef, value, member.value)
				}
			}
		})
	}
}

// confirmDeclaredValues compiles a translation unit asserting each declared
// value against a C driver's own resolution of the name.
//
// It is what makes the check above one on C rather than one on a regexp. A
// driver resolving a name to something the header's text does not read as --
// through a macro, a later declaration, a value the parse misread -- would
// otherwise be compared as the text, and the position check would be reporting
// on a language nothing implements.
func confirmDeclaredValues(t *testing.T, compiler string, declared []preludeMember) {
	t.Helper()
	var unit strings.Builder
	for _, enumerator := range declared {
		fmt.Fprintf(&unit, "static_assert(%s == %d, %q);\n", enumerator.name, enumerator.value, enumerator.name)
	}

	path := filepath.Join(t.TempDir(), "prelude_values.c")
	if err := os.WriteFile(path, []byte(unit.String()), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}

	out, err := exec.CommandContext(t.Context(), compiler, append(gateArgs(t), path)...).CombinedOutput()
	if _, rejected := errors.AsType[*exec.ExitError](err); rejected {
		t.Fatalf("%s does not resolve the enumerators %s declares to the values it declares them as:\n%s",
			compiler, PreludeFileName, out)
	}
	if err != nil {
		t.Fatalf("running %s: %v", compiler, err)
	}
}
