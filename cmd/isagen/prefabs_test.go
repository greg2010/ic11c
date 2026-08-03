package main

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// extractFixturePrefabs runs the join over the hand-written stand-in for the
// decompiled game and its prefab roster.
func extractFixturePrefabs(t *testing.T) []Prefab {
	t.Helper()
	tree, err := newSourceTree(filepath.Join("testdata", "decompiled"))
	if err != nil {
		t.Fatalf("newSourceTree: %v", err)
	}
	assets, err := readJSON[assetPrefabs](filepath.Join("testdata", "prefabs.json"))
	if err != nil {
		t.Fatalf("read prefab roster: %v", err)
	}
	titles, err := readThingNames(filepath.Join("testdata", "names.xml"))
	if err != nil {
		t.Fatalf("readThingNames: %v", err)
	}
	prefabs, err := extractPrefabs(tree, fixtureISA(t), assets, titles)
	if err != nil {
		t.Fatalf("extractPrefabs: %v", err)
	}
	return prefabs
}

func findPrefab(t *testing.T, prefabs []Prefab, name string) Prefab {
	t.Helper()
	for _, prefab := range prefabs {
		if prefab.Name == name {
			return prefab
		}
	}
	t.Fatalf("no prefab named %s", name)
	return Prefab{}
}

// TestExtractPrefabs states what the join makes of each prefab in the fixture
// roster. The fixture golden pins the bytes; this pins the meaning, one shape
// of the game's own code at a time.
func TestExtractPrefabs(t *testing.T) {
	tests := []struct {
		name          string
		prefab        string
		wantTitle     string
		wantHolder    bool
		wantLogic     []LogicAccess
		wantSlots     []PrefabSlot
		wantModes     []Mode
		wantModesOpen bool
	}{
		{
			name:      "a class overriding the atmosphere constant",
			prefab:    "StructureSensor",
			wantTitle: "Gas Sensor",
			wantLogic: []LogicAccess{
				{Name: "Power", Access: accessRead},
				{Name: "Open", Access: accessRead},
			},
		},
		{
			name:       "a chip holder with slots and enum-named modes",
			prefab:     "StructureHousing",
			wantTitle:  "IC Housing",
			wantHolder: true,
			wantLogic: []LogicAccess{
				{Name: "Power", Access: accessRead},
				{Name: "Mode", Access: accessReadWrite},
			},
			wantSlots: []PrefabSlot{
				{Index: 0, Class: "Helmet", Types: []LogicAccess{
					{Name: "Occupied", Access: accessRead},
					{Name: "Quantity", Access: accessReadWrite},
				}},
				{Index: 1, Class: "Suit", Types: []LogicAccess{
					{Name: "Occupied", Access: accessRead},
					{Name: "Quantity", Access: accessRead},
				}},
			},
			wantModes: []Mode{{Value: 0, Name: "Number"}, {Value: 1, Name: "String"}},
		},
		{
			name:   "a folded case group, and modes filled in at runtime",
			prefab: "StructurePanel",
			wantLogic: []LogicAccess{
				{Name: "Open", Access: accessReadWrite},
				{Name: "Mode", Access: accessReadWrite},
			},
			wantModesOpen: true,
		},
		{
			name:   "modes reached through an enum collection",
			prefab: "StructureLouvre",
			wantLogic: []LogicAccess{
				{Name: "Power", Access: accessRead},
				{Name: "Open", Access: accessWrite},
				{Name: "Mode", Access: accessReadWrite},
			},
			wantModes: []Mode{{Value: 0, Name: "Retracted"}, {Value: 1, Name: "HalfOpen"}},
		},
		{
			name:   "a surface the game decides from live state",
			prefab: "StructureMirror",
			wantLogic: []LogicAccess{
				{Name: "Power", Access: accessUnknown},
				{Name: "Open", Access: accessUnknown},
				{Name: "Mode", Access: accessUnknown},
			},
		},
		{
			name:   "a thing with no logic surface at all",
			prefab: "ItemWrench",
		},
	}
	prefabs := extractFixturePrefabs(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findPrefab(t, prefabs, tt.prefab)
			if got.Hash != stringToHash(tt.prefab) {
				t.Errorf("hash = %d, want %d", got.Hash, stringToHash(tt.prefab))
			}
			if got.Title != tt.wantTitle {
				t.Errorf("title = %q, want %q", got.Title, tt.wantTitle)
			}
			if got.CircuitHolder != tt.wantHolder {
				t.Errorf("circuit holder = %v, want %v", got.CircuitHolder, tt.wantHolder)
			}
			if got.CircuitHolderUnknown {
				t.Errorf("circuit holder is marked unresolved, and every class in the fixture tree is placeable")
			}
			if !slices.Equal(got.Logic, tt.wantLogic) {
				t.Errorf("logic = %+v, want %+v", got.Logic, tt.wantLogic)
			}
			if !slices.EqualFunc(got.Slots, tt.wantSlots, sameSlot) {
				t.Errorf("slots = %+v, want %+v", got.Slots, tt.wantSlots)
			}
			if !slices.Equal(got.Modes, tt.wantModes) {
				t.Errorf("modes = %+v, want %+v", got.Modes, tt.wantModes)
			}
			if got.ModesUnknown != tt.wantModesOpen {
				t.Errorf("modes unresolved = %v, want %v", got.ModesUnknown, tt.wantModesOpen)
			}
		})
	}
}

func sameSlot(a, b PrefabSlot) bool {
	return a.Index == b.Index && a.Class == b.Class && slices.Equal(a.Types, b.Types)
}

// TestExtractPrefabsRosterErrors covers a roster naming something the
// decompiled tree does not declare, which is a game update that moved a class
// out from under the extraction.
func TestExtractPrefabsRosterErrors(t *testing.T) {
	tree, err := newSourceTree(filepath.Join("testdata", "decompiled"))
	if err != nil {
		t.Fatalf("newSourceTree: %v", err)
	}
	tests := []struct {
		name    string
		assets  assetPrefabs
		wantErr string
	}{
		{
			name: "class absent from the tree",
			assets: assetPrefabs{Prefabs: []assetPrefab{
				{Name: "StructureGone", Script: "Assets.Scripts.Objects.Absent"},
			}},
			wantErr: "class Assets.Scripts.Objects.Absent: not found",
		},
		{
			name: "slot numbered out of position",
			assets: assetPrefabs{Prefabs: []assetPrefab{{
				Name:   "StructureOdd",
				Script: "Assets.Scripts.Objects.Pipes.Device",
				Slots:  []assetSlot{{Index: 1, Class: 0}},
			}}},
			wantErr: "slot 1 is listed at position 0",
		},
		{
			name: "slot class outside the enum",
			assets: assetPrefabs{Prefabs: []assetPrefab{{
				Name:   "StructureOdd",
				Script: "Assets.Scripts.Objects.Pipes.Device",
				Slots:  []assetSlot{{Index: 0, Class: 999}},
			}}},
			wantErr: "Slot.Class has no member 999",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := extractPrefabs(tree, fixtureISA(t), &tt.assets, nil)
			checkErr(t, "extractPrefabs", err, tt.wantErr)
		})
	}
}

// TestSlotClassNames covers the inversion the roster's slot ordinals are read
// through, and the aliased ordinal it has no answer for.
func TestSlotClassNames(t *testing.T) {
	tests := []struct {
		name    string
		members []EnumMember
		want    map[int64]string
		wantErr string
	}{
		{
			name:    "one name per ordinal",
			members: []EnumMember{{Name: "None"}, {Name: "Helmet", Value: 1}},
			want:    map[int64]string{0: "None", 1: "Helmet"},
		},
		{
			// The commonplace C# alias. Whichever name the inversion kept, the
			// bodies compare a slot against the other one.
			name:    "two names on one ordinal",
			members: []EnumMember{{Name: "None"}, {Name: "Default"}},
			wantErr: "None and Default are both 0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := slotClassNames(tt.members)
			if tt.wantErr != "" {
				checkErr(t, "slotClassNames", err, tt.wantErr)
				return
			}
			if err != nil {
				t.Fatalf("slotClassNames: %v", err)
			}
			if !maps.Equal(got, tt.want) {
				t.Errorf("slotClassNames = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestExtractPrefabsRejectsAliasedSlotClass covers the same alias reached the
// way the extraction reaches it, on a slot whose class the game's own body
// grants a property to.
//
// The two halves compose into a denial: the roster's ordinal 2 takes the name
// the enum declares last, Device compares a slot against the name it was
// written with, and the property drops out of the artifact altogether.
func TestExtractPrefabsRejectsAliasedSlotClass(t *testing.T) {
	const aliased = `namespace Assets.Scripts.Objects;

public class Slot
{
	public enum Class : ushort
	{
		None = 0,
		Helmet = 0x1,
		Suit = 2u,
		Vest = 2u
	}
}
`
	index := perturbedTypes(t, map[string]string{"Assets/Scripts/Objects/Slot.cs": aliased})
	assets := &assetPrefabs{Prefabs: []assetPrefab{{
		Name:      "StructureSuited",
		Script:    "Assets.Scripts.Objects.Pipes.Device",
		UsedPower: 10,
		Slots:     []assetSlot{{Index: 0, Class: 2}},
	}}}
	_, err := extractPrefabs(index.tree, fixtureISA(t), assets, nil)
	checkErr(t, "extractPrefabs", err, "Suit and Vest are both 2")
}

// TestExtractPrefabsCircuitHolder covers the two ways a base list naming the
// class that implements ICircuitHolder resolves.
//
// The tree declares Housing under Assets.Scripts.Objects.Electrical, so a base
// list written in Objects.Structures reaches it only through the alias. Without
// one the interfaces above the base went unread, and a prefab reported as
// holding no chip on that basis is one no program can reach as db.
func TestExtractPrefabsCircuitHolder(t *testing.T) {
	const header = "namespace Objects.Structures;\n\npublic class Mount : Housing\n{\n}\n"
	const aliased = "using Housing = Assets.Scripts.Objects.Electrical.Housing;\n\n" + header

	tests := []struct {
		name        string
		source      string
		wantHolder  bool
		wantUnknown bool
	}{
		{name: "a base this program cannot place", source: header, wantUnknown: true},
		{name: "the same base through the alias", source: aliased, wantHolder: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			index := perturbedTypes(t, map[string]string{"Objects/Structures/Mount.cs": tt.source})
			assets := &assetPrefabs{Prefabs: []assetPrefab{{
				Name:   "StructureMount",
				Hash:   stringToHash("StructureMount"),
				Script: "Objects.Structures.Mount",
			}}}
			prefabs, err := extractPrefabs(index.tree, fixtureISA(t), assets, nil)
			if err != nil {
				t.Fatalf("extractPrefabs: %v", err)
			}
			got := findPrefab(t, prefabs, "StructureMount")
			if got.CircuitHolder != tt.wantHolder || got.CircuitHolderUnknown != tt.wantUnknown {
				t.Errorf("circuit holder = %v, unresolved = %v, want %v and %v",
					got.CircuitHolder, got.CircuitHolderUnknown, tt.wantHolder, tt.wantUnknown)
			}
		})
	}
}

func TestReadThingNames(t *testing.T) {
	titles, err := readThingNames(filepath.Join("testdata", "names.xml"))
	if err != nil {
		t.Fatalf("readThingNames: %v", err)
	}
	if got := titles["StructureSensor"]; got != "Gas Sensor" {
		t.Errorf("title = %q, want %q", got, "Gas Sensor")
	}

	// No path is not an error: the titles are a reading aid and every check the
	// artifact supports is on the names and hashes.
	if titles, err := readThingNames(""); err != nil || titles != nil {
		t.Errorf("readThingNames(\"\") = (%v, %v), want (nil, nil)", titles, err)
	}

	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.xml")
	if err := os.WriteFile(empty, []byte("<Language></Language>"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	tests := []struct {
		name    string
		path    string
		wantErr string
	}{
		{name: "absent", path: filepath.Join(dir, "absent.xml"), wantErr: "read thing names"},
		{name: "empty", path: empty, wantErr: "names no things"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := readThingNames(tt.path)
			checkErr(t, "readThingNames", err, tt.wantErr)
		})
	}
}

// TestAccessOf covers the reduction the whole artifact turns on, and in
// particular that one undecided half makes the entry undecided: reporting the
// decided half alone would read as an assertion about the other.
func TestAccessOf(t *testing.T) {
	tests := []struct {
		readable tri
		writable tri
		want     access
	}{
		{readable: triNo, writable: triNo, want: accessNone},
		{readable: triYes, writable: triNo, want: accessRead},
		{readable: triNo, writable: triYes, want: accessWrite},
		{readable: triYes, writable: triYes, want: accessReadWrite},
		{readable: triMaybe, writable: triNo, want: accessUnknown},
		{readable: triYes, writable: triMaybe, want: accessUnknown},
		{readable: triMaybe, writable: triMaybe, want: accessUnknown},
	}
	for _, tt := range tests {
		t.Run(string(tt.want), func(t *testing.T) {
			if got := accessOf(tt.readable, tt.writable); got != tt.want {
				t.Errorf("accessOf(%d, %d) = %q, want %q", tt.readable, tt.writable, got, tt.want)
			}
		})
	}
}

// validPrefab returns one prefab that passes every assertion in
// validatePrefabs, so a test can perturb a single field.
func validPrefab() Prefab {
	return Prefab{
		Name:  "StructureP0",
		Hash:  stringToHash("StructureP0"),
		Logic: []LogicAccess{{Name: "Power", Access: accessRead}},
		Slots: []PrefabSlot{{
			Index: 0,
			Class: "Helmet",
			Types: []LogicAccess{{Name: "Occupied", Access: accessRead}},
		}},
	}
}

func TestValidatePrefabs(t *testing.T) {
	tests := []struct {
		name    string
		perturb func(*Prefab)
		wantErr string
	}{
		{name: "well formed"},
		{
			name:    "unnamed",
			perturb: func(p *Prefab) { p.Name = "" },
			wantErr: "prefab 0: unnamed",
		},
		{
			name:    "hash that is not the name's",
			perturb: func(p *Prefab) { p.Hash = 1 },
			wantErr: "hashes to",
		},
		{
			name:    "property outside the ISA enumeration",
			perturb: func(p *Prefab) { p.Logic[0].Name = "Nonexistent" },
			wantErr: "logic type Nonexistent is not in the ISA tables",
		},
		{
			name:    "slot property outside the ISA enumeration",
			perturb: func(p *Prefab) { p.Slots[0].Types[0].Name = "Nonexistent" },
			wantErr: "slot type Nonexistent is not in the ISA tables",
		},
		{
			name:    "property listed twice",
			perturb: func(p *Prefab) { p.Logic = append(p.Logic, p.Logic[0]) },
			wantErr: "logic type Power listed twice",
		},
		{
			name:    "property with no access",
			perturb: func(p *Prefab) { p.Logic[0].Access = accessNone },
			wantErr: `logic type Power has access ""`,
		},
		{
			name:    "modes both unresolved and listed",
			perturb: func(p *Prefab) { p.ModesUnknown = true; p.Modes = []Mode{{Name: "Mode0"}} },
			wantErr: "marked unresolved and listed",
		},
		{
			name:    "circuit holder both unresolved and settled",
			perturb: func(p *Prefab) { p.CircuitHolder = true; p.CircuitHolderUnknown = true },
			wantErr: "circuit holder is marked unresolved and settled",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prefab := validPrefab()
			if tt.perturb != nil {
				tt.perturb(&prefab)
			}
			problems := validatePrefabs([]Prefab{prefab}, fixtureISA(t))
			if tt.wantErr == "" {
				if len(problems) != 0 {
					t.Fatalf("validatePrefabs rejected a well formed table: %v", problems)
				}
				return
			}
			if !slices.ContainsFunc(problems, func(p string) bool { return strings.Contains(p, tt.wantErr) }) {
				t.Errorf("problems = %v, want one containing %q", problems, tt.wantErr)
			}
		})
	}
}

// TestValidatePrefabsRejectsDuplicates covers the two ways one thing can be
// listed twice, which the game's own lookup cannot represent.
func TestValidatePrefabsRejectsDuplicates(t *testing.T) {
	tests := []struct {
		name    string
		prefabs []Prefab
		wantErr string
	}{
		{
			name:    "same name twice",
			prefabs: []Prefab{validPrefab(), validPrefab()},
			wantErr: "declared twice",
		},
		{
			name: "two names hashing alike",
			prefabs: func() []Prefab {
				other := validPrefab()
				other.Name = "StructureP1"
				other.Hash = stringToHash("StructureP0")
				return []Prefab{validPrefab(), other}
			}(),
			wantErr: "both hash to",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			problems := validatePrefabs(tt.prefabs, fixtureISA(t))
			if !slices.ContainsFunc(problems, func(p string) bool { return strings.Contains(p, tt.wantErr) }) {
				t.Errorf("problems = %v, want one containing %q", problems, tt.wantErr)
			}
		})
	}
}
