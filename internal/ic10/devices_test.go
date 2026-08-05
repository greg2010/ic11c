package ic10

import (
	"testing"

	"github.com/greg2010/ic11c/internal/isa"
)

// TestPrefabTable checks the shape of the generated table itself, so a
// re-extraction that lost half the roster or dropped the hashes is caught here
// rather than by whatever reads it.
func TestPrefabTable(t *testing.T) {
	if len(prefabs) == 0 {
		t.Fatal("prefab table is empty")
	}
	seen := make(map[int32]string, len(prefabs))
	for _, prefab := range prefabs {
		if prefab.Name == "" {
			t.Fatalf("prefab with hash %d is unnamed", prefab.Hash)
		}
		if previous, taken := seen[prefab.Hash]; taken {
			t.Errorf("prefabs %s and %s both hash to %d", previous, prefab.Name, prefab.Hash)
		}
		seen[prefab.Hash] = prefab.Name
		for i, slot := range prefab.Slots {
			if slot.Class == "" {
				t.Errorf("%s slot %d names no class", prefab.Name, i)
			}
		}
		if prefab.ModesUnknown && len(prefab.Modes) > 0 {
			t.Errorf("%s has modes both unresolved and listed", prefab.Name)
		}
	}
}

// TestPrefabPropertiesAreDeclared holds every property the prefab table names
// to the machine tables, which is the one relationship between the two files
// that neither can state on its own.
func TestPrefabPropertiesAreDeclared(t *testing.T) {
	logicTypes := make(map[LogicType]bool, len(LogicTypes))
	for _, info := range LogicTypes {
		logicTypes[info.Value] = true
	}
	slotTypes := make(map[LogicSlotType]bool, len(LogicSlotTypes))
	for _, info := range LogicSlotTypes {
		slotTypes[info.Value] = true
	}
	for _, prefab := range prefabs {
		for _, entry := range prefab.logic {
			if !logicTypes[LogicType(entry.LogicType)] {
				t.Errorf("%s names logic type %d, which LogicTypes does not declare", prefab.Name, entry.LogicType)
			}
			if entry.Allows == isa.AccessNone {
				t.Errorf("%s lists logic type %d with no access", prefab.Name, entry.LogicType)
			}
		}
		for i, slot := range prefab.Slots {
			for _, entry := range slot.types {
				if !slotTypes[LogicSlotType(entry.SlotType)] {
					t.Errorf("%s slot %d names slot type %d, which LogicSlotTypes does not declare", prefab.Name, i, entry.SlotType)
				}
			}
		}
	}
}

func TestLookupPrefab(t *testing.T) {
	tests := []struct {
		name  string
		found bool
	}{
		{name: "StructureCircuitHousing", found: true},
		{name: "StructureWallLight", found: true},
		// Case-sensitive, like every other name a program hashes.
		{name: "structurecircuithousing"},
		{name: "StructureNotAThing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prefab, ok := LookupPrefab(tt.name)
			if ok != tt.found {
				t.Fatalf("LookupPrefab(%q) found = %v, want %v", tt.name, ok, tt.found)
			}
			if !ok {
				return
			}
			if prefab.Name != tt.name {
				t.Errorf("LookupPrefab(%q) returned %q", tt.name, prefab.Name)
			}
			if byHash, ok := LookupPrefabHash(prefab.Hash); !ok || byHash.Name != prefab.Name {
				t.Errorf("LookupPrefabHash(%d) = (%q, %v), want %q", prefab.Hash, byHash.Name, ok, prefab.Name)
			}
		})
	}
}

// TestLookupPrefabHashRejectsAnUnusedHash covers what makes an arbitrary
// __ic_hash argument checkable at all: a number naming nothing in this build.
func TestLookupPrefabHashRejectsAnUnusedHash(t *testing.T) {
	used := make(map[int32]bool, len(prefabs))
	for _, prefab := range prefabs {
		used[prefab.Hash] = true
	}
	var unused int32 = 1
	for used[unused] {
		unused++
	}
	if prefab, ok := LookupPrefabHash(unused); ok {
		t.Errorf("LookupPrefabHash(%d) returned %s, want nothing", unused, prefab.Name)
	}
}

// TestKnownSurfaces pins a handful of answers the compiler's own test corpus
// got wrong, so a re-extraction that stopped distinguishing them is caught.
func TestKnownSurfaces(t *testing.T) {
	tests := []struct {
		prefab    string
		logicType string
		want      isa.Access
	}{
		// The housing a chip runs in reads and writes its own Setting.
		{prefab: "StructureCircuitHousing", logicType: "Setting", want: isa.AccessReadWrite},
		// Power is a reading, not a switch; On is the switch.
		{prefab: "StructureSatelliteDish", logicType: "Power", want: isa.AccessRead},
		{prefab: "StructureSatelliteDish", logicType: "Setting", want: isa.AccessReadWrite},
		// A wall cooler exposes no temperature, and a batch read of one over a
		// bank of them returns NaN rather than an error.
		{prefab: "StructureWallCooler", logicType: "Temperature", want: isa.AccessNone},
		// A wall light has no Setting to write.
		{prefab: "StructureWallLight", logicType: "Setting", want: isa.AccessNone},
		{prefab: "StructureWallLight", logicType: "On", want: isa.AccessReadWrite},
		// A gas sensor reads its atmosphere and accepts nothing.
		{prefab: "StructureGasSensor", logicType: "Pressure", want: isa.AccessRead},
		{prefab: "StructureGasSensor", logicType: "Setting", want: isa.AccessNone},
	}
	for _, tt := range tests {
		t.Run(tt.prefab+"."+tt.logicType, func(t *testing.T) {
			prefab, ok := LookupPrefab(tt.prefab)
			if !ok {
				t.Fatalf("this build ships no %s", tt.prefab)
			}
			info, ok := LookupLogicType(tt.logicType)
			if !ok {
				t.Fatalf("this build has no %s logic type", tt.logicType)
			}
			if got := prefab.accessFor(info.Value); got != tt.want {
				t.Errorf("%s.%s = %s, want %s", tt.prefab, tt.logicType, got, tt.want)
			}
		})
	}
}

func TestSlotAccessFor(t *testing.T) {
	prefab, ok := LookupPrefab("StructureCircuitHousing")
	if !ok {
		t.Fatal("this build ships no StructureCircuitHousing")
	}
	if len(prefab.Slots) == 0 {
		t.Fatal("StructureCircuitHousing declares no slots")
	}
	occupied, ok := LookupLogicSlotType("Occupied")
	if !ok {
		t.Fatal("this build has no Occupied slot type")
	}
	if got := prefab.Slots[0].accessFor(occupied.Value); got != isa.AccessRead {
		t.Errorf("slot 0 Occupied = %s, want %s", got, isa.AccessRead)
	}
	if got := prefab.Slots[0].accessFor(LogicSlotType(200)); got != isa.AccessNone {
		t.Errorf("slot 0 of an undeclared property = %s, want %s", got, isa.AccessNone)
	}
}

// TestAccessRefusals covers the one rule a consumer must not get wrong: an
// undecided pair refuses neither direction. Access(9), a value no
// extraction produces today, is included because it must answer the same
// as isa.AccessUnknown — a new access learned before these two must not
// start rejecting programs on the strength of it.
func TestAccessRefusals(t *testing.T) {
	tests := []struct {
		access       isa.Access
		refusesRead  bool
		refusesWrite bool
		rendered     string
	}{
		{access: isa.AccessNone, refusesRead: true, refusesWrite: true, rendered: "none"},
		{access: isa.AccessRead, refusesWrite: true, rendered: "read"},
		{access: isa.AccessWrite, refusesRead: true, rendered: "write"},
		{access: isa.AccessReadWrite, rendered: "readwrite"},
		{access: isa.AccessUnknown, rendered: "unknown"},
		{access: isa.Access(9), rendered: "Access(9)"},
	}
	for _, tt := range tests {
		t.Run(tt.rendered, func(t *testing.T) {
			if got := refusesRead(tt.access); got != tt.refusesRead {
				t.Errorf("refusesRead = %v, want %v", got, tt.refusesRead)
			}
			if got := refusesWrite(tt.access); got != tt.refusesWrite {
				t.Errorf("refusesWrite = %v, want %v", got, tt.refusesWrite)
			}
			if got := tt.access.String(); got != tt.rendered {
				t.Errorf("String = %q, want %q", got, tt.rendered)
			}
		})
	}
}

// TestRefusesReachesTheTable covers the two exported queries over real entries,
// since they are the whole of what the surfaces answer outside this package.
func TestRefusesReachesTheTable(t *testing.T) {
	cooler, ok := LookupPrefab("StructureWallCooler")
	if !ok {
		t.Fatal("this build ships no StructureWallCooler")
	}
	temperature, ok := LookupLogicType("Temperature")
	if !ok {
		t.Fatal("this build has no Temperature logic type")
	}
	on, ok := LookupLogicType("On")
	if !ok {
		t.Fatal("this build has no On logic type")
	}
	if !cooler.RefusesRead(temperature.Value) {
		t.Error("a wall cooler is reported to answer Temperature, which it does not")
	}
	if cooler.RefusesWrite(on.Value) {
		t.Error("a wall cooler is reported to refuse a write of On, which is its switch")
	}

	// A logic mirror answers whatever it is pointed at, so every one of its
	// properties is undecided and none of them may be refused.
	mirror, ok := LookupPrefab("StructureLogicMirror")
	if !ok {
		t.Fatal("this build ships no StructureLogicMirror")
	}
	for _, entry := range mirror.logic {
		if entry.Allows != isa.AccessUnknown {
			continue
		}
		if mirror.RefusesRead(LogicType(entry.LogicType)) || mirror.RefusesWrite(LogicType(entry.LogicType)) {
			t.Fatalf("logic type %d is undecided on a logic mirror and was refused anyway", entry.LogicType)
		}
	}

	housing, ok := LookupPrefab("StructureCircuitHousing")
	if !ok {
		t.Fatal("this build ships no StructureCircuitHousing")
	}
	occupied, ok := LookupLogicSlotType("Occupied")
	if !ok {
		t.Fatal("this build has no Occupied slot type")
	}
	if housing.Slots[0].RefusesRead(occupied.Value) {
		t.Error("slot 0 of a circuit housing is reported not to answer Occupied, which it reads")
	}
	if !housing.Slots[0].RefusesWrite(occupied.Value) {
		t.Error("slot 0 of a circuit housing is reported to accept a write of Occupied, which it reads only")
	}
}

// TestUndecidedSurfacesAreMarked confirms the artifact says so where it cannot
// answer, rather than answering wrongly. A build in which nothing is marked
// would mean the marking was lost, not that the game became decidable.
func TestUndecidedSurfacesAreMarked(t *testing.T) {
	undecided := 0
	for _, prefab := range prefabs {
		for _, entry := range prefab.logic {
			if entry.Allows == isa.AccessUnknown {
				undecided++
			}
		}
	}
	if undecided == 0 {
		t.Error("no property in the table is marked undecided; the game decides several from live state")
	}
}
