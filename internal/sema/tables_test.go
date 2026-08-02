package sema_test

import (
	"context"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/parser"
	"github.com/greg2010/ic11c/internal/sema"
	"github.com/greg2010/ic11c/internal/source"
)

// testTables holds the handful of machine names the tests write. The encodings
// are arbitrary: analysis only has to resolve a name to one and record it.
type testTables struct{}

// PlantHealth1 and Growth carry the deprecated flag, as the game's own tables
// do, so the deprecation warning has something to fire on.
var (
	testLogicTypes = map[string]sema.Member{
		"On": {Value: 0}, "Pressure": {Value: 5}, "Temperature": {Value: 6},
		"Setting": {Value: 12}, "Open": {Value: 20}, "PlantHealth1": {Value: 44, Deprecated: true},
	}
	testSlotTypes = map[string]sema.Member{
		"Occupied": {Value: 0}, "OccupantHash": {Value: 1}, "Growth": {Value: 12, Deprecated: true},
	}
	testBatchModes = map[string]sema.Member{
		"Average": {Value: 0}, "Sum": {Value: 1}, "Minimum": {Value: 2}, "Maximum": {Value: 3}, "Count": {Value: 4},
	}
	testReagentModes = map[string]sema.Member{
		"Contents": {Value: 0}, "Required": {Value: 1}, "Recipe": {Value: 2}, "TotalContents": {Value: 3},
	}
)

func (testTables) LogicType(name string) (sema.Member, bool) {
	m, ok := testLogicTypes[name]
	return m, ok
}

func (testTables) LogicSlotType(name string) (sema.Member, bool) {
	m, ok := testSlotTypes[name]
	return m, ok
}

func (testTables) BatchMode(name string) (sema.Member, bool) {
	m, ok := testBatchModes[name]
	return m, ok
}

func (testTables) ReagentMode(name string) (sema.Member, bool) {
	m, ok := testReagentModes[name]
	return m, ok
}

// marker is written immediately before the construct a rejecting test expects
// to be named. It is a comment, so it changes no position but its own line and
// column, and the token that follows it starts where the diagnostic must point.
const marker = "/*!*/"

// markedPos reports where the marker in src points: the byte just after it.
func markedPos(t *testing.T, src string) source.Position {
	t.Helper()
	i := strings.Index(src, marker)
	if i < 0 {
		t.Fatalf("test source contains no %s marker", marker)
	}
	end := i + len(marker)
	return source.Position{
		File:   "test.c",
		Offset: end,
		Line:   1 + strings.Count(src[:end], "\n"),
		Column: end - (strings.LastIndex(src[:end], "\n") + 1) + 1,
	}
}

// expectRejected checks that src is rejected by exactly one diagnostic, which
// points at the marker and names want.
func expectRejected(t *testing.T, src, want string) {
	t.Helper()
	pos := markedPos(t, src)
	_, diags := analyze(t, src)
	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want exactly 1:\n%s", len(diags), diags.String())
	}
	got := diags[0]
	if got.Pos.Line != pos.Line || got.Pos.Column != pos.Column {
		t.Errorf("diagnostic at %s, want %s: %s", got.Pos, pos, got.Msg)
	}
	if !strings.Contains(got.Msg, want) {
		t.Errorf("message %q does not name the construct %q", got.Msg, want)
	}
}

// expectAccepted checks that src analyzes with nothing to report, warnings
// included.
func expectAccepted(t *testing.T, src string) {
	t.Helper()
	if _, diags := analyze(t, src); len(diags) != 0 {
		t.Errorf("analysis rejected a valid program:\n%s\n%s", src, diags.String())
	}
}

// analyze parses and checks src, failing the test if it does not parse.
func analyze(t *testing.T, src string) (*sema.Program, source.DiagnosticList) {
	t.Helper()
	file, diags := parser.Parse("test.c", src)
	if len(diags) != 0 {
		t.Fatalf("source did not parse cleanly:\n%s", diags.String())
	}
	prog, semaDiags, err := sema.Analyze(context.Background(), file, testTables{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if prog == nil {
		t.Fatal("Analyze returned no program")
	}
	return prog, semaDiags
}
