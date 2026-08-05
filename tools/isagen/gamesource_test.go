package main

import (
	"bytes"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// gameFixture is the decompiled tree and everything derived from it, built once
// for the package: indexing is cheap, but the parsed types, evaluated bodies and
// recovered tables the tests accumulate through it are not.
type gameFixture struct {
	tree        *sourceTree
	index       *typeIndex
	isa         *ISA
	slotClasses []EnumMember
	surface     *logicSurface
	err         error
}

var theGame *gameFixture

// game returns the shared fixture, failing where no decompiled tree is there.
func game(t *testing.T) *gameFixture {
	t.Helper()
	if theGame == nil {
		theGame = newGameFixture()
	}
	if theGame.err != nil {
		t.Fatalf("%v", theGame.err)
	}
	return theGame
}

func newGameFixture() *gameFixture {
	g := &gameFixture{}
	g.tree, g.err = openGameSource()
	if g.err != nil {
		return g
	}
	g.index = newTypeIndex(g.tree)
	if g.isa, g.err = extractISA(g.tree); g.err != nil {
		return g
	}
	if g.slotClasses, g.err = slotClassMembers(g.index); g.err != nil {
		return g
	}
	g.surface, g.err = newLogicSurface(g.index, g.isa, g.slotClasses)
	return g
}

func gameTree(t *testing.T) *sourceTree { return game(t).tree }

func gameIndex(t *testing.T) *typeIndex { return game(t).index }

// gameClass looks a class up, failing where the game declares nothing so named.
func gameClass(t *testing.T, qualified string) *csharpType {
	t.Helper()
	found, err := gameIndex(t).lookup(qualified)
	if err != nil {
		t.Fatalf("lookup %s: %v", qualified, err)
	}
	if found == nil {
		t.Fatalf("the game declares no %s", qualified)
	}
	return found
}

// gameDevice builds a device out of a class the game declares, with every
// serialized state flag off except the ones named. The flags and the draw come
// from the roster rather than the source, so a test states them.
func gameDevice(t *testing.T, qualified string, slotClasses []string, usedPower float64, on ...string) *device {
	t.Helper()
	return &device{
		class:       gameClass(t, qualified),
		state:       deviceState(t, on...),
		usedPower:   usedPower,
		slotClasses: slotClasses,
	}
}

// checkedInISA is the ISA table this program last wrote out of the game.
func checkedInISA(t *testing.T) *ISA {
	t.Helper()
	isa, err := readJSON[ISA](filepath.Join(moduleRoot, defaultJSONPath))
	if err != nil {
		t.Fatalf("read the checked-in ISA tables: %v", err)
	}
	return isa
}

// checkedInDevices is the device table this program last wrote out of the game.
func checkedInDevices(t *testing.T) *Devices {
	t.Helper()
	d, err := readJSON[Devices](filepath.Join(moduleRoot, defaultDevicesJSONPath))
	if err != nil {
		t.Fatalf("read the checked-in device tables: %v", err)
	}
	return d
}

// TestTheGameStillYieldsTheCheckedInISA runs the whole ISA recovery over the
// decompiled game and holds the result to the table in the tree.
func TestTheGameStillYieldsTheCheckedInISA(t *testing.T) {
	want := checkedInISA(t)
	got := *game(t).isa
	// Manifest and version come from the assembly's PE resource and from the
	// depot, not from the source, so extraction over a tree carries neither.
	got.Manifest, got.Version = want.Manifest, want.Version

	// validate's floors are hand-written counts of the game's own enumerations,
	// so running it here catches one the game has grown past.
	if err := validate(&got); err != nil {
		t.Errorf("the tables recovered from the game source: %v", err)
	}

	gotJSON, err := encodeJSON(&got)
	if err != nil {
		t.Fatalf("encode the recovered tables: %v", err)
	}
	wantJSON, err := encodeJSON(want)
	if err != nil {
		t.Fatalf("encode the checked-in tables: %v", err)
	}
	if bytes.Equal(gotJSON, wantJSON) {
		return
	}
	t.Errorf("the tables recovered from the game source are not %s; %s",
		defaultJSONPath, firstDifference(gotJSON, wantJSON))
}

// TestTheGameStillYieldsTheCheckedInReagents covers the half of the device
// tables the source settles alone; the prefab half is a join against a roster
// that is not in the tree. Reagent order is ReagentId, so a build that inserted
// one renumbers every reagent a compiled program names.
func TestTheGameStillYieldsTheCheckedInReagents(t *testing.T) {
	src, err := gameTree(t).qualified(typeReagent)
	if err != nil {
		t.Fatalf("read %s: %v", typeReagent, err)
	}
	got, err := extractReagents(src)
	if err != nil {
		t.Fatalf("extractReagents: %v", err)
	}
	want := checkedInDevices(t).Reagents
	if slices.Equal(got, want) {
		return
	}
	for i := range min(len(got), len(want)) {
		if got[i] != want[i] {
			t.Fatalf("the game source makes ReagentId %d %+v, and %s carries %+v",
				i, got[i], defaultDevicesJSONPath, want[i])
		}
	}
	t.Errorf("the game source declares %d reagents and %s carries %d",
		len(got), defaultDevicesJSONPath, len(want))
}

// TestTheGameDeclaresEverySlotClassTheCheckedInTablesName covers the one device
// enumeration read from the source rather than carried by the roster. The game's
// bodies grant a slot a property by comparing its class against a member name,
// so a renamed member silently stops every such body matching.
func TestTheGameDeclaresEverySlotClassTheCheckedInTablesName(t *testing.T) {
	declared := make(map[string]bool, len(game(t).slotClasses))
	for _, member := range game(t).slotClasses {
		declared[member.Name] = true
	}

	var missing []string
	for _, prefab := range checkedInDevices(t).Prefabs {
		for _, slot := range prefab.Slots {
			if !declared[slot.Class] && !slices.Contains(missing, slot.Class) {
				missing = append(missing, slot.Class)
			}
		}
	}
	if len(missing) != 0 {
		t.Errorf("%s names slot classes %s, which %s.Class no longer declares",
			defaultDevicesJSONPath, strings.Join(missing, ", "), typeSlot)
	}
}

// TestTheGameDeclaresEverySurfaceSignature holds surfaceSignatures to the class
// declaring them. A pattern that matches nothing sends the evaluator to a base
// that declares nothing either, which answers no -- reporting the whole roster
// as refusing every property.
func TestTheGameDeclaresEverySurfaceSignature(t *testing.T) {
	class := gameClass(t, typeDevice)
	for kind, pattern := range surfaceSignatures {
		t.Run(kind.String(), func(t *testing.T) {
			decl, _, ok := findMember(class, pattern)
			if !ok {
				t.Fatalf("%s declares nothing matching %s", typeDevice, pattern)
			}
			body, err := memberBody(decl)
			if err != nil {
				t.Fatalf("memberBody: %v", err)
			}
			if len(splitExprList(body)) == 0 {
				t.Errorf("%s.%s has an empty body", typeDevice, decl.name)
			}
		})
	}
}

// TestTheGameDeclaresEveryFingerprintedType covers gameTypes, which names the
// declarations the hand-written Go here transliterates. A renamed type drops out
// of the fingerprint silently, leaving its Go counterpart covered by nothing.
func TestTheGameDeclaresEveryFingerprintedType(t *testing.T) {
	tree := gameTree(t)
	for _, gt := range gameTypes {
		t.Run(gt.name, func(t *testing.T) {
			if _, err := tree.qualified(gt.name); err != nil {
				t.Errorf("the digest fingerprints %s, and the game source: %v", gt.name, err)
			}
		})
	}
}

// Floors for the sweep below, well under what the shipped build answers. They
// catch bodies that went out of the evaluator's reach, leaving the roster
// undecided; a surface that collapsed to a decided no is
// [TestTheGameDeclaresEverySurfaceSignature]'s failure instead.
const (
	minSurfaceClasses  = 90
	minSurfaceDecided  = 60000
	maxSurfaceQuestion = 8000
)

// TestTheEvaluatorReadsEveryLogicSurfaceTheGameDeclares asks every class
// declaring a logic surface about every property the ISA tables carry. Every
// state flag is on and the draw nonzero, which is no prefab in the roster: what
// is asserted is that the bodies read, not what a device exposes.
func TestTheEvaluatorReadsEveryLogicSurfaceTheGameDeclares(t *testing.T) {
	g := game(t)
	values := enumValues(g.isa.LogicTypes)
	state := make(map[string]bool, len(serializedStateFlags))
	for _, flag := range serializedStateFlags {
		state[flag] = true
	}

	classes, decided, undecided := 0, 0, 0
	for _, qualified := range surfaceClasses(t, g.tree) {
		class, err := g.index.lookup(qualified)
		if err != nil {
			t.Fatalf("lookup %s: %v", qualified, err)
		}
		// An interface declaration has no body to read.
		if class == nil || !class.IsClass {
			continue
		}
		classes++
		dev := &device{class: class, state: state, usedPower: 1, slotClasses: []string{"None"}}
		for _, kind := range []surfaceKind{kindReadLogic, kindWriteLogic} {
			for _, member := range g.isa.LogicTypes {
				got, err := g.surface.can(query{
					device:        dev,
					kind:          kind,
					selector:      member.Name,
					selectorValue: values[member.Name],
					slotIndex:     noSlot,
				})
				if err != nil {
					t.Errorf("%s, asked %s about %s: %v", qualified, kind, member.Name, err)
					continue
				}
				if got == triMaybe {
					undecided++
				} else {
					decided++
				}
			}
		}
	}

	if classes < minSurfaceClasses {
		t.Errorf("the sweep reached %d classes declaring a logic surface, want at least %d", classes, minSurfaceClasses)
	}
	if decided < minSurfaceDecided {
		t.Errorf("the evaluator decided %d of the game's own answers, want at least %d", decided, minSurfaceDecided)
	}
	if undecided > maxSurfaceQuestion {
		t.Errorf("the evaluator left %d of the game's own answers undecided, want at most %d", undecided, maxSurfaceQuestion)
	}
}

// TestTheGameNamesEveryRosterStateFlag holds serializedStateFlags to the class
// declaring them. The roster comes from serialized files that are not in the
// tree, so nothing else here can tell a flag the game renamed from one it never
// had, and a flag missing from a device's state decides nothing either way.
func TestTheGameNamesEveryRosterStateFlag(t *testing.T) {
	declared := everySerializedState(gameClass(t, gameThing))
	for _, flag := range serializedStateFlags {
		if !declared[flag] {
			t.Errorf("the roster carries %s and %s no longer declares it", flag, gameThing)
		}
	}
}

// TestTheResolverReadsEveryModeStringsTheGameDeclares floors how many classes
// resolve rather than requiring all of them: a class that fills its modes in at
// runtime reads fine and resolves to nothing.
func TestTheResolverReadsEveryModeStringsTheGameDeclares(t *testing.T) {
	const minResolvedModes = 15

	resolver := newModeResolver(gameIndex(t))
	declaring, resolved := 0, 0
	for _, qualified := range classesDeclaring(t, gameTree(t), "string[] ModeStrings") {
		class, err := gameIndex(t).lookup(qualified)
		if err != nil {
			t.Fatalf("lookup %s: %v", qualified, err)
		}
		if class == nil || !class.IsClass {
			continue
		}
		declaring++
		if _, ok, err := resolver.modes(class); err != nil {
			t.Errorf("%v", err)
		} else if ok {
			resolved++
		}
	}
	if resolved < minResolvedModes {
		t.Errorf("the resolver read the modes of %d of the %d classes the game declares them on, want at least %d",
			resolved, declaring, minResolvedModes)
	}
}

// surfaceClasses names every type declaring one of the logic surface methods.
func surfaceClasses(t *testing.T, tree *sourceTree) []string {
	t.Helper()
	return classesDeclaring(t, tree, "bool CanLogic")
}

// classesDeclaring names every type in the tree whose source holds the given
// text. The scan is textual and over the whole tree rather than over a list, so
// a class the game adds is swept without anything here being told about it.
func classesDeclaring(t *testing.T, tree *sourceTree, declaration string) []string {
	t.Helper()
	var found []string
	for _, name := range tree.types() {
		src, err := tree.qualified(name)
		if err != nil {
			t.Fatalf("scan the decompiled source for %q: %v", declaration, err)
		}
		if strings.Contains(src, declaration) {
			found = append(found, name)
		}
	}
	return found
}

// firstDifference describes where two encodings part; they are too wide to read.
func firstDifference(got, want []byte) string {
	const context = 160
	for i := range min(len(got), len(want)) {
		if got[i] != want[i] {
			return fmt.Sprintf("they part at byte %d:\n got: %s\nwant: %s",
				i, got[i:min(len(got), i+context)], want[i:min(len(want), i+context)])
		}
	}
	return fmt.Sprintf("one is a prefix of the other: got %d bytes, want %d", len(got), len(want))
}
