package sema_test

import (
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/sema"
)

// shippedTables is what analysis is handed in production, held through the
// interface it is handed through, so a method that stopped satisfying it would
// not compile here.
var shippedTables sema.Tables = sema.Shipped{}

// The roster entries below are facts about the pinned game build, named here
// rather than looked up so that a re-extraction that moves one fails a test
// instead of quietly changing what these checks mean. Each is chosen for the
// shape it carries and not for the device it is:
//
//   - a gas sensor reads its atmosphere, accepts nothing, holds no chip, has no
//     slot and no mode state;
//   - an umbilical socket takes a Setting both ways;
//   - an emergency space helmet takes a Flush that answers nothing back;
//   - a combustor decides PressureInput from live state, holds a chip, and
//     fills its mode names at run time;
//   - a roll cover has two modes;
//   - a gas tank storage declares one slot.
const (
	readOnly   = "StructureGasSensor"
	readWrite  = "StructureGasUmbilicalFemale"
	writeOnly  = "ItemEmergencySpaceHelmet"
	undecided  = "H2Combustor"
	twoModes   = "CompositeRollCover"
	oneSlot    = "StructureGasTankStorage"
	notShipped = "StructureWallLite"
)

// shippedPrefab resolves a prefab the build has to ship for the tests below to
// mean anything.
func shippedPrefab(t *testing.T, name string) sema.Prefab {
	t.Helper()
	p, ok := shippedTables.PrefabNamed(name)
	if !ok {
		t.Fatalf("this game build ships nothing named %q, and the checks written against it describe nothing", name)
	}
	return p
}

// shippedLogicType resolves a property name to the encoding the refusal queries
// take.
func shippedLogicType(t *testing.T, name string) int {
	t.Helper()
	member, ok := shippedTables.LogicType(name)
	if !ok {
		t.Fatalf("this game build resolves no logic type named %q", name)
	}
	return member.Value
}

func shippedSlotType(t *testing.T, name string) int {
	t.Helper()
	member, ok := shippedTables.LogicSlotType(name)
	if !ok {
		t.Fatalf("this game build resolves no slot property named %q", name)
	}
	return member.Value
}

// TestRefusesLogic covers the direction dispatch and, with it, the rule the
// whole roster rests on: a property the extraction could not decide refuses
// neither direction, and a property the device does not carry at all refuses
// both.
func TestRefusesLogic(t *testing.T) {
	tests := []struct {
		prefab   string
		property string
		dir      sema.Direction
		want     bool
	}{
		{prefab: readOnly, property: "Pressure", dir: sema.Reading, want: false},
		{prefab: readOnly, property: "Pressure", dir: sema.Writing, want: true},
		{prefab: readOnly, property: "Setting", dir: sema.Reading, want: true},
		{prefab: readOnly, property: "Setting", dir: sema.Writing, want: true},
		{prefab: readWrite, property: "Setting", dir: sema.Reading, want: false},
		{prefab: readWrite, property: "Setting", dir: sema.Writing, want: false},
		{prefab: writeOnly, property: "Flush", dir: sema.Reading, want: true},
		{prefab: writeOnly, property: "Flush", dir: sema.Writing, want: false},
		{prefab: undecided, property: "PressureInput", dir: sema.Reading, want: false},
		{prefab: undecided, property: "PressureInput", dir: sema.Writing, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.prefab+" "+tt.property+" "+shippedDirectionName(tt.dir), func(t *testing.T) {
			if got := shippedPrefab(t, tt.prefab).RefusesLogic(shippedLogicType(t, tt.property), tt.dir); got != tt.want {
				t.Errorf("RefusesLogic(%s, %s) = %v, want %v", tt.property, shippedDirectionName(tt.dir), got, tt.want)
			}
		})
	}
}

// TestRefusesSlot covers the same dispatch for a slot's own properties, and the
// guard on the slot index.
//
// A slot the prefab does not declare refuses nothing here. It is out of range
// rather than refused, which analysis reports with a message naming the slot
// count, and answering true would have it report the property instead.
func TestRefusesSlot(t *testing.T) {
	tests := []struct {
		name     string
		slot     int
		property string
		dir      sema.Direction
		want     bool
	}{
		{name: "a property the slot answers", slot: 0, property: "Occupied", dir: sema.Reading, want: false},
		{name: "reading is not writing", slot: 0, property: "Occupied", dir: sema.Writing, want: true},
		{name: "a property the slot takes both ways", slot: 0, property: "Open", dir: sema.Writing, want: false},
		{name: "a property the slot does not carry", slot: 0, property: "Lock", dir: sema.Reading, want: true},
		{name: "one past the last slot", slot: 1, property: "Occupied", dir: sema.Reading, want: false},
		{name: "far past the last slot", slot: 99, property: "Occupied", dir: sema.Reading, want: false},
		{name: "a negative slot", slot: -1, property: "Occupied", dir: sema.Reading, want: false},
	}
	prefab := shippedPrefab(t, oneSlot)
	if prefab.NumSlots() != 1 {
		t.Fatalf("%s declares %d slots, and these cases are written against the one it had", oneSlot, prefab.NumSlots())
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := prefab.RefusesSlot(tt.slot, shippedSlotType(t, tt.property), tt.dir); got != tt.want {
				t.Errorf("RefusesSlot(%d, %s, %s) = %v, want %v", tt.slot, tt.property, shippedDirectionName(tt.dir), got, tt.want)
			}
		})
	}
}

// TestNumModes covers the flag that separates a device with no mode state from
// one whose mode names the extraction could not recover. Nothing may conclude a
// mode number is out of range for the second.
func TestNumModes(t *testing.T) {
	tests := []struct {
		prefab    string
		want      int
		wantKnown bool
	}{
		{prefab: twoModes, want: 2, wantKnown: true},
		{prefab: readOnly, want: 0, wantKnown: true},
		{prefab: undecided, want: 0, wantKnown: false},
	}
	for _, tt := range tests {
		t.Run(tt.prefab, func(t *testing.T) {
			count, known := shippedPrefab(t, tt.prefab).NumModes()
			if count != tt.want || known != tt.wantKnown {
				t.Errorf("NumModes = (%d, %v), want (%d, %v)", count, known, tt.want, tt.wantKnown)
			}
		})
	}
}

// TestHoldsCircuit covers what makes a thing reachable as db. A false known is
// not a denial, which is why the two are answered separately.
func TestHoldsCircuit(t *testing.T) {
	tests := []struct {
		prefab    string
		want      bool
		wantKnown bool
	}{
		{prefab: undecided, want: true, wantKnown: true},
		{prefab: readOnly, want: false, wantKnown: true},
	}
	for _, tt := range tests {
		t.Run(tt.prefab, func(t *testing.T) {
			holds, known := shippedPrefab(t, tt.prefab).HoldsCircuit()
			if holds != tt.want || known != tt.wantKnown {
				t.Errorf("HoldsCircuit = (%v, %v), want (%v, %v)", holds, known, tt.want, tt.wantKnown)
			}
		})
	}
}

// TestPrefabLookup covers the two ways a program names a prefab, which have to
// reach the same entry: a batch operand carries the hash, and a __ic_hash
// argument carries the name the game hashes to it.
func TestPrefabLookup(t *testing.T) {
	byName := shippedPrefab(t, readOnly)
	if byName.Name() != readOnly {
		t.Errorf("PrefabNamed(%q).Name = %q", readOnly, byName.Name())
	}
	if byName.Title() == "" {
		t.Errorf("%s carries no title, and a diagnostic naming it would show only the roster name", readOnly)
	}
	if byName.NumSlots() != 0 {
		t.Errorf("%s declares %d slots, and these cases are written against the none it had", readOnly, byName.NumSlots())
	}

	byHash, ok := shippedTables.Prefab(shippedHash(t, readOnly))
	if !ok {
		t.Fatalf("the hash %s resolves by name to does not resolve by number", readOnly)
	}
	if byHash.Name() != byName.Name() {
		t.Errorf("the hash resolves to %s and the name to %s", byHash.Name(), byName.Name())
	}

	if _, ok := shippedTables.PrefabNamed(notShipped); ok {
		t.Errorf("this game build resolves %q, which the misspelling cases elsewhere assume it does not", notShipped)
	}
}

// shippedHash answers the number the roster holds one prefab under. Reading it from
// the table rather than writing it down keeps the case above a check on the two
// lookups reaching one entry rather than on the hash itself.
func shippedHash(t *testing.T, name string) int32 {
	t.Helper()
	info, ok := ic10.LookupPrefab(name)
	if !ok {
		t.Fatalf("this game build ships nothing named %q", name)
	}
	return info.Hash
}

// TestOperandNames covers the four name tables, each of which resolves
// case-sensitively as the chip does and carries the game's own retirement flag.
func TestOperandNames(t *testing.T) {
	tests := []struct {
		name           string
		resolve        func(string) (sema.Member, bool)
		spelling       string
		wantOK         bool
		wantDeprecated bool
	}{
		{name: "a logic type", resolve: shippedTables.LogicType, spelling: "Pressure", wantOK: true},
		{name: "a retired logic type", resolve: shippedTables.LogicType, spelling: "ImportQuantity", wantOK: true, wantDeprecated: true},
		{name: "a logic type in the wrong case", resolve: shippedTables.LogicType, spelling: "pressure"},
		{name: "a slot property", resolve: shippedTables.LogicSlotType, spelling: "Occupied", wantOK: true},
		{name: "no slot property", resolve: shippedTables.LogicSlotType, spelling: "NotASlotProperty"},
		{name: "a batch mode", resolve: shippedTables.BatchMode, spelling: "Average", wantOK: true},
		{name: "no batch mode", resolve: shippedTables.BatchMode, spelling: "Median"},
		{name: "a reagent mode", resolve: shippedTables.ReagentMode, spelling: "Contents", wantOK: true},
		{name: "no reagent mode", resolve: shippedTables.ReagentMode, spelling: "Leftovers"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			member, ok := tt.resolve(tt.spelling)
			if ok != tt.wantOK {
				t.Fatalf("resolving %q reported %v, want %v", tt.spelling, ok, tt.wantOK)
			}
			if ok && member.Deprecated != tt.wantDeprecated {
				t.Errorf("%q is deprecated = %v, want %v", tt.spelling, member.Deprecated, tt.wantDeprecated)
			}
		})
	}
}

func shippedDirectionName(dir sema.Direction) string {
	if dir == sema.Writing {
		return "writing"
	}
	return "reading"
}
