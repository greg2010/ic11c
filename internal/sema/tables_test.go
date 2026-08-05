package sema_test

import (
	"context"
	"hash/crc32"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/sema"
	"github.com/greg2010/ic11c/internal/source"
	"github.com/greg2010/ic11c/internal/tsparse"
)

// testTables holds the handful of machine names the tests write. The encodings
// are arbitrary: analysis only has to resolve a name to one and record it.
type testTables struct{}

// Unlike the prefab stubs below, these names are the game's own, and most of
// the shape each was chosen for is a fact about the shipped build: PlantHealth1
// is retired there, and Lock and Minimum each sit in two families under
// different values, which is what makes the prefixed spellings reachable here. A
// stub whose families shared no name would resolve every bare one and never
// exercise the rule that keeps the language a C subset. Growth's retirement is
// the one invention, for the reason inventedDeprecations gives.
//
// [TestStubMachineNamesAreShipped] and [TestStubSharedNamesAreSharedInTheGame]
// hold every one of those claims to the shipped tables, so a build that moved
// one fails there rather than leaving these tests exercising a shape the game no
// longer has. The encodings stay arbitrary: analysis only has to resolve a name
// to one.
var (
	testLogicTypes = map[string]sema.Member{
		"On": {Value: 0}, "Pressure": {Value: 5}, "Temperature": {Value: 6},
		"Setting": {Value: 12}, "Open": {Value: 20}, "PlantHealth1": {Value: 44, Deprecated: true},
		"Mode": {Value: 3}, "Lock": {Value: 10}, "Minimum": {Value: 277},
	}
	testSlotTypes = map[string]sema.Member{
		"Occupied": {Value: 0}, "OccupantHash": {Value: 1}, "Growth": {Value: 12, Deprecated: true},
		"Lock": {Value: 23},
	}
	testBatchModes = map[string]sema.Member{
		"Average": {Value: 0}, "Sum": {Value: 1}, "Minimum": {Value: 2}, "Maximum": {Value: 3}, "Count": {Value: 4},
	}
	testReagentModes = map[string]sema.Member{
		"Contents": {Value: 0}, "Required": {Value: 1}, "Recipe": {Value: 2}, "TotalContents": {Value: 3},
	}
)

// stubMember names one member of one stub table.
type stubMember struct {
	what string
	name string
}

// inventedDeprecations names each stub member carrying a retired flag the game
// does not, with what the flag is there to exercise. A member here is a decision
// rather than a mistake, and the shape it stands in for is asserted rather than
// assumed: the game retiring it would make the note wrong, and the test says so.
var inventedDeprecations = map[stubMember]string{
	{what: "slot type", name: "Growth"}: "this game build retires no slot type at all, and the warning for a retired one has to fire on something",
}

// TestStubMachineNamesAreShipped holds every machine name the stub tables
// resolve to the shipped tables, under the same deprecation.
//
// These names are not invented, so each one is a claim about the game build: the
// tests that use them are written around which are retired and which are not,
// and a build that moved one would leave the stub asserting a fiction while every
// test using it still passed.
func TestStubMachineNamesAreShipped(t *testing.T) {
	shipped := sema.Shipped{}
	families := []struct {
		what    string
		stub    map[string]sema.Member
		resolve func(string) (sema.Member, bool)
	}{
		{what: "logic type", stub: testLogicTypes, resolve: shipped.LogicType},
		{what: "slot type", stub: testSlotTypes, resolve: shipped.LogicSlotType},
		{what: "batch mode", stub: testBatchModes, resolve: shipped.BatchMode},
		{what: "reagent mode", stub: testReagentModes, resolve: shipped.ReagentMode},
	}
	invented := map[stubMember]bool{}
	for _, family := range families {
		for name, stub := range family.stub {
			member := stubMember{what: family.what, name: name}
			got, ships := family.resolve(name)
			if !ships {
				t.Errorf("the stub %s %s is not one this game build ships, so nothing it exercises describes the game", family.what, name)
				continue
			}
			why, decided := inventedDeprecations[member]
			if !decided {
				if got.Deprecated != stub.Deprecated {
					t.Errorf("the stub %s %s carries deprecated=%t and this game build says %t", family.what, name, stub.Deprecated, got.Deprecated)
				}
				continue
			}
			invented[member] = true
			if !stub.Deprecated {
				t.Errorf("the stub %s %s is recorded as retired for the stub's own reasons (%s) and does not carry the flag", family.what, name, why)
			}
			if got.Deprecated {
				t.Errorf("the stub %s %s is recorded as retired only in the stub (%s) and this game build retires it too, so the note is stale", family.what, name, why)
			}
		}
	}
	for member := range inventedDeprecations {
		if !invented[member] {
			t.Errorf("the %s %s carries a retired flag the game does not, and is no longer in the stub tables", member.what, member.name)
		}
	}
}

// TestStubSharedNamesAreSharedInTheGame holds the other claim the stub tables
// make about the game: that one bare name reaches two families, which is what
// makes the prefixed spelling the only one that says which was meant.
func TestStubSharedNamesAreSharedInTheGame(t *testing.T) {
	shipped := sema.Shipped{}
	tests := []struct {
		name  string
		what  string
		one   func(string) (sema.Member, bool)
		other func(string) (sema.Member, bool)
	}{
		{name: "Lock", what: "a logic type and a slot type", one: shipped.LogicType, other: shipped.LogicSlotType},
		{name: "Minimum", what: "a logic type and a batch mode", one: shipped.LogicType, other: shipped.BatchMode},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			one, inOne := tt.one(tt.name)
			other, inOther := tt.other(tt.name)
			if !inOne || !inOther {
				t.Fatalf("%s is meant to be both %s in this game build, and is not", tt.name, tt.what)
			}
			if one.Value == other.Value {
				t.Errorf("%s encodes as %d in both families, so the stub's differing values describe a build this is not", tt.name, one.Value)
			}
		})
	}
}

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

// testAccess is the stub's model of what one property allows. It has the state
// the generated roster's does that a naive model would not: a direction the
// game settles at run time, which nothing may read as a denial.
type testAccess uint8

const (
	// testAbsent is the zero value so that a property missing from a surface
	// map is one the device answers nothing for, which is what the generated
	// roster means by omitting it.
	testAbsent testAccess = iota
	testRead
	testWrite
	testReadWrite
	testUndecided
)

func (a testAccess) refuses(dir sema.Direction) bool {
	switch a {
	case testRead:
		return dir == sema.Writing
	case testWrite:
		return dir == sema.Reading
	case testReadWrite, testUndecided:
		return false
	case testAbsent:
	}
	return true
}

// testHolder is the stub's model of whether a thing can hold a programmable
// chip. The zero value is undecided so that an entry saying nothing about it is
// one nothing may conclude from, which is what the generated roster's own
// unknown flag means.
type testHolder uint8

const (
	holderUndecided testHolder = iota
	holdsCircuit
	holdsNoCircuit
)

// testPrefab is one thing the stub roster ships.
type testPrefab struct {
	title string
	// logic and slots hold the surfaces by property name, which the queries
	// translate the encoding back through.
	logic        map[string]testAccess
	slots        []map[string]testAccess
	modes        int
	modesUnknown bool
	holder       testHolder
}

// testPrefabs models the shapes the real roster has that the check has to tell
// apart, and no more of it than that.
//
// Every name here is invented, which is what [TestStubPrefabNamesAreInvented]
// holds them to. The surfaces, slots and mode counts below are chosen for the
// shape they exercise rather than extracted from a game build, so an entry
// under a name the game ships would read as a fact about that thing and nothing
// would be checking it. What the shipped roster says is established against the
// shipped roster, in the tests that run through [analyzeShipped].
var testPrefabs = map[string]testPrefab{
	// Switched, and answering no temperature of its own: the shape the
	// compiler's own corpus got wrong.
	"StructureStubCooler": {title: "Stub Cooler", logic: map[string]testAccess{
		"On": testReadWrite, "Setting": testReadWrite, "Pressure": testRead,
	}},
	// Switched through On and taking no Setting, which is the other shape the
	// corpus got wrong.
	"StructureStubLight": {title: "Stub Light", logic: map[string]testAccess{"On": testReadWrite}},
	// Reads its atmosphere and accepts nothing, and there is nowhere in one to
	// put a chip.
	"StructureStubSensor": {title: "Stub Sensor", holder: holdsNoCircuit, logic: map[string]testAccess{
		"Temperature": testRead, "Pressure": testRead,
	}},
	// A housing a chip runs in, which is the one thing a declaration of db can
	// name without contradicting the roster.
	"StructureStubHousing": {title: "Stub Housing", holder: holdsCircuit,
		logic: map[string]testAccess{"On": testReadWrite, "Setting": testReadWrite}},
	// Answers whatever it is pointed at, so nothing about its surface is
	// decided.
	"StructureStubMirror": {title: "Stub Mirror", logic: map[string]testAccess{
		"Setting": testUndecided, "On": testUndecided, "Temperature": testUndecided,
		"PlantHealth1": testUndecided,
	}},
	// Three modes, so 3 is one past the end.
	"StructureStubConsole": {title: "Stub Console", modes: 3, logic: map[string]testAccess{"Mode": testReadWrite}},
	// Mode state whose names the extraction could not recover, which is not the
	// same as having none.
	"StructureStubPanel": {title: "Stub Panel", modesUnknown: true, logic: map[string]testAccess{"Mode": testReadWrite}},
	// Two slots, neither of which takes a write.
	"StructureStubFurnace": {title: "Stub Furnace", logic: map[string]testAccess{"Temperature": testRead, "Setting": testReadWrite},
		slots: []map[string]testAccess{
			{"Occupied": testRead, "OccupantHash": testRead},
			{"Occupied": testRead, "OccupantHash": testRead},
		}},
	// A slot that takes a write, which 35 of the roster's slot entries do.
	"StructureStubLocker": {title: "Stub Locker", logic: map[string]testAccess{"On": testReadWrite},
		slots: []map[string]testAccess{{"Occupied": testRead, "Lock": testReadWrite}}},
	// Answers nothing at all, which is most of the roster.
	"ItemStubTool": {title: "Stub Tool"},
	// A deprecated property that still resolves and is still answered.
	"StructureStubPlanter": {title: "Stub Planter", logic: map[string]testAccess{"PlantHealth1": testRead}},
	// No English title, which three of the roster's entries ship without. A
	// diagnostic about one names the roster name and nothing beside it.
	"ItemStubUntitled": {logic: map[string]testAccess{"On": testRead}},
}

// TestStubPrefabNamesAreInvented holds the stub roster to the property that
// keeps a reader from taking any of it for a fact about the game: none of its
// names is one the game ships.
func TestStubPrefabNamesAreInvented(t *testing.T) {
	for name := range testPrefabs {
		if _, ships := (sema.Shipped{}).PrefabNamed(name); ships {
			t.Errorf("the stub roster names %s, which this game build ships; a stub entry's surfaces are chosen rather than extracted, so one under a shipped name states something about that thing which nothing checks", name)
		}
	}
}

func (testTables) Prefab(hash int32) (sema.Prefab, bool) {
	for name := range testPrefabs {
		if int32(crc32.ChecksumIEEE([]byte(name))) == hash {
			return testTables{}.PrefabNamed(name)
		}
	}
	return nil, false
}

func (testTables) PrefabNamed(name string) (sema.Prefab, bool) {
	entry, ok := testPrefabs[name]
	if !ok {
		return nil, false
	}
	return stubPrefab{name: name, entry: entry}, true
}

type stubPrefab struct {
	name  string
	entry testPrefab
}

func (p stubPrefab) Name() string { return p.name }

func (p stubPrefab) Title() string { return p.entry.title }

func (p stubPrefab) NumSlots() int { return len(p.entry.slots) }

func (p stubPrefab) NumModes() (int, bool) { return p.entry.modes, !p.entry.modesUnknown }

func (p stubPrefab) HoldsCircuit() (holds, known bool) {
	return p.entry.holder == holdsCircuit, p.entry.holder != holderUndecided
}

func (p stubPrefab) RefusesLogic(logicType int, dir sema.Direction) bool {
	return p.entry.logic[nameOf(testLogicTypes, logicType)].refuses(dir)
}

func (p stubPrefab) RefusesSlot(slot, slotType int, dir sema.Direction) bool {
	if slot < 0 || slot >= len(p.entry.slots) {
		return false
	}
	return p.entry.slots[slot][nameOf(testSlotTypes, slotType)].refuses(dir)
}

// nameOf reverses one of the stub's name tables, since the queries receive the
// encoding and the surfaces above are written by name.
func nameOf(table map[string]sema.Member, value int) string {
	for name, member := range table {
		if member.Value == value {
			return name
		}
	}
	return ""
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
	got := soleDiagnostic(t, src)
	if !strings.Contains(got.Msg, want) {
		t.Errorf("message %q does not name the construct %q", got.Msg, want)
	}
}

// expectRejectedWith is expectRejected holding the whole message rather than a
// fragment of it, which is what a test of the wording itself needs: a fragment
// passes for a message that names the problem and then leaves the reader
// nowhere.
func expectRejectedWith(t *testing.T, src, want string) {
	t.Helper()
	got := soleDiagnostic(t, src)
	if got.Msg != want {
		t.Errorf("message %q, want %q", got.Msg, want)
	}
}

// soleDiagnostic analyzes src, requires it to be rejected by exactly one
// diagnostic, and checks that the diagnostic points at the marker.
func soleDiagnostic(t *testing.T, src string) source.Diagnostic {
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
	return got
}

// expectDiagnosticAt checks that one of the diagnostics src produces points at
// the marker and reads exactly want.
//
// It is what a program with a second mistake of its own needs. [soleDiagnostic]
// would reject the extra message rather than the program, and a source written
// to avoid it would no longer be the source that reaches the message under test.
func expectDiagnosticAt(t *testing.T, src, want string) {
	t.Helper()
	pos := markedPos(t, src)
	_, diags := analyze(t, src)
	for _, d := range diags {
		if d.Pos.Line != pos.Line || d.Pos.Column != pos.Column {
			continue
		}
		if d.Msg != want {
			t.Errorf("message %q, want %q", d.Msg, want)
		}
		return
	}
	t.Errorf("no diagnostic at %s:\n%s", pos, diags.String())
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
	file, diags, err := tsparse.Parse("test.c", src)
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
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
