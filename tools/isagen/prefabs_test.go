package main

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fixtureRoster is a prefab roster carrying the same eleven serialized flags per
// prefab that tools/prefabreader writes, over the shape corpus's classes.
func fixtureRoster(t *testing.T) *assetPrefabs {
	t.Helper()
	assets, err := readJSON[assetPrefabs](filepath.Join("testdata", "prefabs.json"))
	if err != nil {
		t.Fatalf("read prefab roster: %v", err)
	}
	return assets
}

// rosterPrefab carries the serialized flags every entry tools/prefabreader writes
// carries, so a test perturbing one field perturbs nothing else.
func rosterPrefab(t *testing.T, name, script string) assetPrefab {
	t.Helper()
	roster := fixtureRoster(t)
	if len(roster.Prefabs) == 0 {
		t.Fatalf("the roster fixture names no prefabs")
	}
	return assetPrefab{
		Name:   name,
		Hash:   stringToHash(name),
		Script: script,
		State:  roster.Prefabs[0].State,
	}
}

// extractFixturePrefabs runs the join over the shape corpus and that roster.
func extractFixturePrefabs(t *testing.T) []Prefab {
	t.Helper()
	tree, err := newSourceTree(filepath.Join("testdata", shapeCorpus))
	if err != nil {
		t.Fatalf("newSourceTree: %v", err)
	}
	assets := fixtureRoster(t)
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
// roster. The golden pins the bytes; this pins the meaning.
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

// TestExtractPrefabsRosterErrors covers a roster naming something the decompiled
// tree does not declare -- a game update that moved a class.
func TestExtractPrefabsRosterErrors(t *testing.T) {
	tree, err := newSourceTree(filepath.Join("testdata", shapeCorpus))
	if err != nil {
		t.Fatalf("newSourceTree: %v", err)
	}
	tests := []struct {
		name    string
		script  string
		slots   []assetSlot
		wantErr string
	}{
		{
			name:    "class absent from the tree",
			script:  "Assets.Scripts.Objects.Absent",
			wantErr: "class Assets.Scripts.Objects.Absent: not found",
		},
		{
			name:    "slot class outside the enum",
			script:  typeDevice,
			slots:   []assetSlot{{Class: 999}},
			wantErr: "Slot.Class has no member 999",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prefab := rosterPrefab(t, "StructureOdd", tt.script)
			prefab.Slots = tt.slots
			assets := assetPrefabs{Prefabs: []assetPrefab{prefab}}
			_, err := extractPrefabs(tree, fixtureISA(t), &assets, nil)
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
			// The commonplace C# alias; the bodies compare against the other name.
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

// TestExtractPrefabsRejectsAliasedSlotClass covers the alias reached the way the
// extraction reaches it. The roster's ordinal 2 takes the name the enum declares
// last, Device compares against the name it was written with, and the property
// drops out of the artifact altogether.
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
	prefab := rosterPrefab(t, "StructureSuited", typeDevice)
	prefab.UsedPower = new(10.0)
	prefab.Slots = []assetSlot{{Class: 2}}
	assets := &assetPrefabs{Prefabs: []assetPrefab{prefab}}
	_, err := extractPrefabs(index.tree, fixtureISA(t), assets, nil)
	checkErr(t, "extractPrefabs", err, "Suit and Vest are both 2")
}

// deviceNamingAFlag is the fixture's Device with two places a serialized flag can
// be named: an arm of CanLogicWrite, and a method that is none of the four bodies.
const deviceNamingAFlag = `using Assets.Scripts.Objects.Motherboards;

namespace Assets.Scripts.Objects.Pipes;

public class Device : Thing
{
	public float UsedPower = 10f;

	public virtual bool CanLogicRead(LogicType logicType)
	{
		switch (logicType)
		{
		case LogicType.Power:
			return HasPowerState;
		default:
			return false;
		}
	}

	public virtual bool CanLogicWrite(LogicType logicType)
	{
		return logicType switch
		{
			LogicType.Open => HasOpenState,
%s			_ => false,
		};
	}

	public virtual bool CanLogicRead(LogicSlotType logicSlotType, int slotId)
	{
		return logicSlotType == LogicSlotType.Occupied;
	}

	public virtual bool CanLogicWrite(LogicSlotType logicSlotType, int slotId)
	{
		return logicSlotType == LogicSlotType.Quantity;
	}
%s}
`

// TestExtractPrefabsRejectsUnmodelledStateRead covers a logic surface body that
// starts reading a serialized flag the roster leaves out. Nothing else reports
// it: the layout check passes, and the evaluator's neither-yes-nor-no reaches the
// artifact as an undecided property, which a consumer reads as no diagnostic.
func TestExtractPrefabsRejectsUnmodelledStateRead(t *testing.T) {
	tests := []struct {
		name    string
		arm     string
		method  string
		wantErr string
	}{
		{
			name:    "read by a logic surface body",
			arm:     "\t\t\tLogicType.Mode => HasButton1State,\n",
			wantErr: "CanLogicWrite(LogicType) reads HasButton1State",
		},
		{
			// Why the flags are read out of the four bodies rather than the whole
			// class source: the shipped Thing names all five unmodelled flags in
			// its animator code, and names HasContentsState, which is not a
			// serialized field at all.
			name:   "named outside the four bodies",
			method: "\n\tpublic void OnServerTick()\n\t{\n\t\tif (HasButton1State)\n\t\t{\n\t\t\tSetAnimation();\n\t\t}\n\t}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := fmt.Sprintf(deviceNamingAFlag, tt.arm, tt.method)
			index := perturbedTypes(t, map[string]string{
				"Assets/Scripts/Objects/Pipes/Device.cs": source,
			})
			_, err := extractPrefabs(index.tree, fixtureISA(t), fixtureRoster(t), nil)
			checkErr(t, "extractPrefabs", err, tt.wantErr)
		})
	}
}

// TestExtractPrefabsCircuitHolder covers the two ways a base list naming the
// ICircuitHolder implementation resolves. The tree declares Housing under
// Assets.Scripts.Objects.Electrical, so a base list written in Objects.Structures
// reaches it only through the alias.
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
			assets := &assetPrefabs{Prefabs: []assetPrefab{
				rosterPrefab(t, "StructureMount", "Objects.Structures.Mount"),
			}}
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

// TestExtractPrefabsRejectsTitlesThatMatchNothing covers a localization file read
// successfully that named nothing on the roster: nothing else reports it, and the
// artifact comes out plausible with every title gone. The floor is one match
// rather than all, since the shipped file names far fewer things than the roster.
func TestExtractPrefabsRejectsTitlesThatMatchNothing(t *testing.T) {
	tree, err := newSourceTree(filepath.Join("testdata", shapeCorpus))
	if err != nil {
		t.Fatalf("newSourceTree: %v", err)
	}
	matching, err := readThingNames(filepath.Join("testdata", "names.xml"))
	if err != nil {
		t.Fatalf("readThingNames: %v", err)
	}

	tests := []struct {
		name    string
		titles  map[string]string
		wantErr string
	}{
		{name: "titles naming some of the roster", titles: matching},
		{name: "no localization file at all", titles: nil},
		{
			name:    "titles naming nothing on the roster",
			titles:  map[string]string{"StructureRenamed": "Gas Sensor"},
			wantErr: "the localization file carries 1 titles and none of them keys one of the 6 prefabs",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := extractPrefabs(tree, fixtureISA(t), fixtureRoster(t), tt.titles)
			checkErr(t, "extractPrefabs", err, tt.wantErr)
		})
	}
}

// deviceSurfaceBodies is the fixture's Device with each of the four logic surface
// bodies' answers under the caller's control.
const deviceSurfaceBodies = `namespace Assets.Scripts.Objects.Pipes;

public class Device : Thing
{
	public float UsedPower = 10f;

	public virtual bool CanLogicRead(LogicType logicType)
	{
		return %s;
	}

	public virtual bool CanLogicWrite(LogicType logicType)
	{
		return %s;
	}

	public virtual bool CanLogicRead(LogicSlotType logicSlotType, int slotId)
	{
		return %s;
	}

	public virtual bool CanLogicWrite(LogicSlotType logicSlotType, int slotId)
	{
		return %s;
	}
}
`

// TestExtractPrefabsRejectsABaseSurfaceThatGrantsNothing covers the class nearly
// every logic surface is inherited from declaring one of its four bodies in a
// spelling the matchers no longer reach. The surface-total floors do not see it:
// overriding classes decide as before, and the rest come out with it absent.
func TestExtractPrefabsRejectsABaseSurfaceThatGrantsNothing(t *testing.T) {
	bodies := [4]string{
		"logicType == LogicType.Power && HasPowerState",
		"logicType == LogicType.Open && HasOpenState",
		"logicSlotType == LogicSlotType.Occupied",
		"logicSlotType == LogicSlotType.Quantity",
	}

	const everyBodyRead = -1

	tests := []struct {
		name string
		// outOfReach names the body the case puts out of reach, or none of them.
		outOfReach int
		wantErr    string
	}{
		// Without this case, a template that stopped compiling to a readable body
		// would make every other case refuse for a reason it does not name.
		{name: "every body read", outOfReach: everyBodyRead},
		{name: "the whole-device read", outOfReach: 0, wantErr: typeDevice + ".CanLogicRead(LogicType) grants nothing"},
		{name: "the whole-device write", outOfReach: 1, wantErr: typeDevice + ".CanLogicWrite(LogicType) grants nothing"},
		{name: "the slot read", outOfReach: 2, wantErr: typeDevice + ".CanLogicRead(LogicSlotType, int) grants nothing"},
		{name: "the slot write", outOfReach: 3, wantErr: typeDevice + ".CanLogicWrite(LogicSlotType, int) grants nothing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			answers := bodies
			if tt.outOfReach != everyBodyRead {
				answers[tt.outOfReach] = "false"
			}
			index := perturbedTypes(t, map[string]string{
				"Assets/Scripts/Objects/Pipes/Device.cs": fmt.Sprintf(
					deviceSurfaceBodies, answers[0], answers[1], answers[2], answers[3]),
			})
			_, err := extractPrefabs(index.tree, fixtureISA(t), fixtureRoster(t), nil)
			checkErr(t, "extractPrefabs", err, tt.wantErr)
		})
	}
}

// TestAccessOf covers the reduction the whole artifact turns on: one undecided
// half makes the entry undecided, since reporting the decided half alone would
// read as an assertion about the other.
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
