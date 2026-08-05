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

	"github.com/greg2010/ic11c/internal/corpus"
)

// fixturePreludePath is the -include argument the corpus's own argument
// file carries: the checked-in header, reached from the directory that
// file sits in. Named rather than copied beside the corpus so nothing
// there can drift.
const fixturePreludePath = "../../ic10/generated/" + PreludeFileName

// cCompilers are the drivers the gate runs, every one found on PATH: the
// gate's claim is about C, not about one driver, and drivers disagree
// about the boundary. clang is listed first since it is what
// compile_flags.txt targets. cc is deliberately absent — a symlink to one
// of these, with no way to tell which.
var cCompilers = []string{"clang", "gcc"}

// gateFlags are what compile_flags.txt cannot carry, since an editor wants
// the opposite of each: deprecation warnings are the point of the header's
// attributes and are noise here. -pedantic-errors gives the gate its
// teeth — without it clang silently accepts GNU extensions gcc rejects.
var gateFlags = []string{
	"-fsyntax-only",
	"-pedantic-errors",
	"-Wno-deprecated-declarations",
}

// TestCorpusCompilesAsC checks that every program the compiler accepts is
// also a C program — the whole of the subset claim, and what lets an
// editor read a MicroC source file as C. It keys on exit status, not
// silence: a warning (a retired logic type, the prefab attribute) is still
// valid MicroC. See [TestPreludeEnumeratorsResolveAsMicroCDoes] for what
// the names resolve to.
func TestCorpusCompilesAsC(t *testing.T) {
	args := gateArgs(t)
	paths := corpusPaths(t)

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

// corpusDir is where the corpus is checked in, which a C driver needs because
// it reads a file rather than a source.
func corpusDir(tb testing.TB) string {
	tb.Helper()
	dir, err := corpus.Dir()
	if err != nil {
		tb.Fatalf("%v", err)
	}
	return dir
}

// corpusPaths returns the on-disk path of every program in the corpus.
//
// The count is held to what the build captured. A glob that stopped matching
// would otherwise leave a gate over no programs at all, which passes.
func corpusPaths(tb testing.TB) []string {
	tb.Helper()
	dir := corpusDir(tb)
	paths, err := filepath.Glob(filepath.Join(dir, "*.c"))
	if err != nil {
		tb.Fatalf("globbing %s: %v", dir, err)
	}
	programs, err := corpus.Programs()
	if err != nil {
		tb.Fatalf("loading the corpus: %v", err)
	}
	if len(paths) != len(programs) {
		tb.Fatalf("%s holds %d programs and the build captured %d", dir, len(paths), len(programs))
	}
	return paths
}

// findCCompilers returns every C driver on PATH to run the gate with,
// since the drivers disagree about what "is C" means — the reason
// -pedantic-errors is passed at all. No compiler found is a Fatalf, not a
// skip: `go test` discards a passing package's output, so a silent skip
// would read exactly like a full pass.
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

// gateArgs renders the argv the gate runs: CompileFlags itself, so what an
// editor is configured with is what gets exercised, with the header path
// resolved against the corpus directory where the argument file sits.
func gateArgs(t *testing.T) []string {
	t.Helper()
	header := filepath.Join(corpusDir(t), fixturePreludePath)
	if _, err := os.Stat(header); err != nil {
		t.Fatalf("%s names %s and it does not resolve: %v", CompileFlagsFileName, fixturePreludePath, err)
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

// TestCompileFlagsIncludingRejectsAnUnrecognizedForm covers the guard on
// the embedded file's shape: the substitution is a suffix replacement, so
// a flags file not ending in the bare header name must be reported, not
// have a second header path appended. Drives the unexported form, since
// the embedded file cannot be given these shapes.
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

// TestFixtureCompileFlagsReachThePrelude checks the fixture's argument
// file against CompileFlagsIncluding(fixturePreludePath): the two are
// generated together and differ only in the header path, so this catches a
// hand edit to either. [TestCorpusCompilesAsC] is what checks the flags
// themselves work.
func TestFixtureCompileFlagsReachThePrelude(t *testing.T) {
	fixtureDir := corpusDir(t)
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

// preludeFamilies lists the four families in the order the header claims
// names in, read out of the generated tables — not written down here — so
// a name the game adds, moves, or drops reaches both checks below without
// going stale against a re-extracted build.
func preludeFamilies() []preludeFamily {
	return []preludeFamily{
		{position: "logic type", typedef: "ic10_logic", prefix: LogicTypePrefix, members: preludeMembers(LogicTypes, func(t LogicTypeInfo) (string, int64) { return t.Name, int64(t.Value) })},
		{position: "slot type", typedef: "ic10_slot", prefix: SlotTypePrefix, members: preludeMembers(LogicSlotTypes, func(t LogicSlotTypeInfo) (string, int64) { return t.Name, int64(t.Value) })},
		{position: "batch mode", typedef: "ic10_batch", prefix: BatchModePrefix, members: preludeMembers(BatchModes, func(m BatchModeInfo) (string, int64) { return m.Name, int64(m.Value) })},
		{position: "reagent mode", typedef: "ic10_reagent", prefix: ReagentModePrefix, members: preludeMembers(ReagentModes, func(m ReagentModeInfo) (string, int64) { return m.Name, int64(m.Value) })},
	}
}

// preludeSpelling is the name a position resolves one of its members by:
// the bare spelling where this family first carries it, prefixed
// everywhere else — written against the tables rather than the header, so
// the two are compared rather than one read out of the other.
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

// declaredEnumerators reads the enumerators the checked-in header
// declares, in order. A duplicate name is fatal, not reported: C rejects a
// scope declaring one twice, so a header holding one describes nothing a
// driver agrees with.
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

// TestPreludeDeclaresEveryOperandName checks the checked-in header against
// the checked-in tables, catching a hand edit to either that regeneration
// would otherwise paper over. Checks which spellings exist only; what they
// mean is [TestPreludeEnumeratorsResolveAsMicroCDoes].
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

// TestPreludeEnumeratorsResolveAsMicroCDoes checks what the header means,
// against a C driver rather than the regexp: C has one enumerator
// namespace per scope, so MicroC resolves an operand name the same way,
// giving each shared name to the first family and prefixing the rest. The
// comparison is per position, not per name, since a name means one thing
// to C and position is where the two languages could part company.
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

// confirmDeclaredValues compiles a translation unit asserting each
// declared value against a C driver's own resolution of the name, so the
// check is against C rather than against the regexp that read the header.
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
