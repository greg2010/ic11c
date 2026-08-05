package sema

import "github.com/greg2010/ic11c/internal/ic10"

// Shipped answers [Tables] from the machine tables extracted from the
// game's own assembly, and is what analysis is handed in production. The
// zero value is ready to use: the tables are package level and immutable.
type Shipped struct{}

// LogicType resolves a device property name such as "Pressure". The chip
// matches these case-sensitively, so a name differing only in case is a
// different property and does not resolve.
func (Shipped) LogicType(name string) (Member, bool) {
	info, ok := ic10.LookupLogicType(name)
	return Member{Value: int(info.Value), Deprecated: info.Deprecated}, ok
}

// LogicSlotType resolves a slot property name such as "Occupied".
func (Shipped) LogicSlotType(name string) (Member, bool) {
	info, ok := ic10.LookupLogicSlotType(name)
	return Member{Value: int(info.Value), Deprecated: info.Deprecated}, ok
}

// BatchMode resolves a batch aggregation mode name.
func (Shipped) BatchMode(name string) (Member, bool) {
	info, ok := ic10.LookupBatchMode(name)
	return Member{Value: int(info.Value), Deprecated: info.Deprecated}, ok
}

// ReagentMode resolves a reagent mode name.
func (Shipped) ReagentMode(name string) (Member, bool) {
	info, ok := ic10.LookupReagentMode(name)
	return Member{Value: int(info.Value), Deprecated: info.Deprecated}, ok
}

// Prefab resolves what a batch instruction's prefab hash names.
func (Shipped) Prefab(hash int32) (Prefab, bool) {
	info, ok := ic10.LookupPrefabHash(hash)
	if !ok {
		return nil, false
	}
	return shippedPrefab{info}, true
}

// PrefabNamed resolves what a __ic_hash argument names.
func (Shipped) PrefabNamed(name string) (Prefab, bool) {
	info, ok := ic10.LookupPrefab(name)
	if !ok {
		return nil, false
	}
	return shippedPrefab{info}, true
}

// shippedPrefab answers [Prefab] from one roster entry. It carries no access
// values across the boundary and cannot: internal/ic10 answers only the four
// refusal questions about a property, so analysis has no way to read an
// undecided property as a denied one.
type shippedPrefab struct{ info ic10.PrefabInfo }

func (p shippedPrefab) Name() string { return p.info.Name }

func (p shippedPrefab) Title() string { return p.info.Title }

func (p shippedPrefab) NumSlots() int { return len(p.info.Slots) }

func (p shippedPrefab) HoldsCircuit() (holds, known bool) {
	return p.info.CircuitHolder, !p.info.CircuitHolderUnknown
}

func (p shippedPrefab) NumModes() (int, bool) {
	return len(p.info.Modes), !p.info.ModesUnknown
}

func (p shippedPrefab) RefusesLogic(logicType int, dir Direction) bool {
	if dir == Writing {
		return p.info.RefusesWrite(ic10.LogicType(logicType))
	}
	return p.info.RefusesRead(ic10.LogicType(logicType))
}

func (p shippedPrefab) RefusesSlot(slot, slotType int, dir Direction) bool {
	if slot < 0 || slot >= len(p.info.Slots) {
		// A slot the prefab does not declare is out of range rather than
		// refused, and analysis reports that before asking this.
		return false
	}
	if dir == Writing {
		return p.info.Slots[slot].RefusesWrite(ic10.LogicSlotType(slotType))
	}
	return p.info.Slots[slot].RefusesRead(ic10.LogicSlotType(slotType))
}
