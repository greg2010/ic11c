package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"strings"
	"testing"
)

// The tasks that regenerate a checked-in artefact from a live measurement.
// Diagnostics name the task rather than the go command, so the build tags a
// regeneration needs stay in one place.
const (
	clangBaselineTask       = "task clang:baseline"
	equivalenceBaselineTask = "task equivalence:baseline"
	refusalWitnessTask      = "task refusal:witnesses"
)

// baseline is a checked-in record of what each program was measured to
// write, from which the floors under a comparison are derived. A floor
// derived from a live measurement only moves when a regeneration is asked
// for and lands as a diff somebody reads, unlike one hand-written per program.
type baseline[T any] struct {
	// file holds the measurements and task regenerates it.
	file string
	task string
	// update is the flag a regeneration is asked for with. While it is set the
	// run is measuring the corpus rather than being held to it.
	update *bool
	// excluded reports a program that is not compared and so needs no floor.
	excluded func(name string) bool
	// missing names what one recorded entry leaves without a floor, qualified
	// by the program, and returns nothing for an entry that covers it. A
	// partial entry is a hole rather than a floor of zero, as is a
	// production too small to derive a floor from.
	missing func(name string, entry T) []string
}

// load reads the measured production every floor is derived from. A
// regeneration is about to overwrite the file, so it does not need one that
// reads: an absent or unreadable baseline is how the first one gets written.
func (b baseline[T]) load(t *testing.T) map[string]T {
	t.Helper()
	if *b.update {
		return nil
	}
	text, err := os.ReadFile(b.file)
	if err != nil {
		t.Fatalf("reading %s: %v; measure the corpus with %s", b.file, err, b.task)
	}
	var recorded map[string]T
	if err := json.Unmarshal(text, &recorded); err != nil {
		t.Fatalf("parsing %s: %v", b.file, err)
	}
	return recorded
}

// covers holds the baseline to the corpus in both directions: a program it
// does not name would be compared with no floor under it, and a name the
// corpus no longer has is bookkeeping left behind. A regeneration only gets
// the stale-name check.
func (b baseline[T]) covers(t *testing.T, names []string, recorded map[string]T) {
	t.Helper()
	for name := range recorded {
		if !slices.Contains(names, name) {
			t.Errorf("%s records %s, which is no longer compared; regenerate with %s", b.file, name, b.task)
		}
	}
	if *b.update {
		return
	}
	for _, name := range names {
		if b.excluded(name) {
			continue
		}
		entry, listed := recorded[name]
		if !listed {
			t.Errorf("%s has no entry in %s, so it would be compared with no floor under it; measure it with %s",
				name, b.file, b.task)
			continue
		}
		for _, hole := range b.missing(name, entry) {
			t.Errorf("%s records no production worth a floor for %s: either it is new and wants measuring with %s, or it does not build and run, which its own subtest reports",
				b.file, hole, b.task)
		}
	}
}

// record writes what a regeneration measured. A program that could not be
// measured is left out rather than guessed at; the gap then fails every
// ordinary run afterwards, in covers.
func (b baseline[T]) record(t *testing.T, names []string, measured map[string]T) {
	t.Helper()
	if holes := b.unmeasured(names, measured); len(holes) > 0 {
		t.Logf("%d entries were not measured and are recorded nowhere; each one fails until it builds and runs:\n\t%s",
			len(holes), strings.Join(holes, "\n\t"))
	}
	if len(measured) == 0 {
		t.Fatalf("%s was left alone: nothing could be measured", b.file)
	}
	text, err := json.MarshalIndent(measured, "", "  ")
	if err != nil {
		t.Fatalf("encoding the baseline: %v", err)
	}
	if err := os.WriteFile(b.file, append(text, '\n'), 0o644); err != nil {
		t.Fatalf("writing %s: %v", b.file, err)
	}
	t.Logf("rewrote %s from %d programs; the floors move with it, so read the diff", b.file, len(measured))
}

// unmeasured names everything the run produced no measurement for.
func (b baseline[T]) unmeasured(names []string, measured map[string]T) []string {
	var holes []string
	for _, name := range names {
		if b.excluded(name) {
			continue
		}
		entry, seen := measured[name]
		if !seen {
			holes = append(holes, name+": not measured at all")
			continue
		}
		holes = append(holes, b.missing(name, entry)...)
	}
	return holes
}

// writeFloor is the floor derived from a program's measured production: half
// of it, rounded down to a multiple of ten while that leaves a bound at all.
// Half keeps the floor from tracking production exactly; the rounding is
// skipped when it would round a small measurement away to zero.
func writeFloor(measured int) int {
	half := measured / 2
	switch {
	case half/10*10 > 0:
		return half / 10 * 10
	case half > 0:
		return half
	default:
		return 1
	}
}

// TestWriteFloor holds the derived floor to being a bound at the productions
// where the rounding would otherwise round it away: a floor of one at the
// measurement a regeneration will just accept would be cleared by a program
// that stopped on its first statement.
func TestWriteFloor(t *testing.T) {
	for _, tt := range []struct {
		measured int
		want     int
	}{
		{measured: 0, want: 1},
		{measured: 1, want: 1},
		{measured: 2, want: 1},
		{measured: measurableWrites, want: 5},
		{measured: 19, want: 9},
		{measured: 20, want: 10},
		{measured: 23, want: 10},
		{measured: 200, want: 100},
	} {
		if got := writeFloor(tt.measured); got != tt.want {
			t.Errorf("writeFloor(%d) = %d, want %d", tt.measured, got, tt.want)
		}
	}
}

// requireNamedInCorpus holds a table keyed by program name to naming programs
// the corpus still has. One it no longer has is bookkeeping left behind, and a
// table excusing or driving a program nothing compiles reads as coverage that is
// merely missing.
func requireNamedInCorpus[T any](t *testing.T, table string, entries map[string]T, names []string) {
	t.Helper()
	for name := range entries {
		if !slices.Contains(names, name) {
			t.Errorf("%s names %s, which the corpus does not contain", table, name)
		}
	}
}

// requireReasonsCited holds a table keyed by an exclusion's reason to
// reasons some exclusion still states — the direction the per-exclusion
// check cannot see: a reason left after the last citing exclusion is gone
// reads as a condition somebody is still watching.
func requireReasonsCited(t *testing.T, table string, keyed map[string]string, exclusions map[string]string) {
	t.Helper()
	for reason := range keyed {
		cited := false
		for _, stated := range exclusions {
			cited = cited || stated == reason
		}
		if !cited {
			t.Errorf("%s carries what to look for when a program is excluded because %q, and nothing is excluded for that reason any more", table, reason)
		}
	}
}

// requireTestsDeclared holds a table whose values name tests to tests this
// package still declares — the direction a check on the caller cannot see: a
// deleted test names nothing and fails nowhere, so it goes on being counted
// as coverage by everything derived from the table's size.
func requireTestsDeclared(t *testing.T, table string, keyed map[string]string) {
	t.Helper()
	declared := declaredTests(t)
	for key, test := range keyed {
		if !declared[test] {
			t.Errorf("%s gives %s to %s, which this package no longer declares", table, key, test)
		}
	}
}

// declaredTests is every top level test function this package's own test
// files declare, which is the set a t.Name() outside a subtest can come
// from. The files are read rather than the running binary asked: a test is
// reachable through the testing package only while it is running.
func declaredTests(t *testing.T) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	fset := token.NewFileSet()
	tests := make(map[string]bool)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Test") {
				continue
			}
			tests[fn.Name.Name] = true
		}
	}
	if len(tests) == 0 {
		t.Fatal("no test functions were read out of this package's source, so the read that finds them is what is wrong")
	}
	return tests
}
