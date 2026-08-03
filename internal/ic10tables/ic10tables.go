// Package ic10tables backs sema.Tables with the machine tables generated from
// the game's own assembly.
//
// It exists so that semantic analysis depends on an interface rather than on
// the generated package: type checking is not a compile-time dependency of the
// tables, and a test can supply the handful of names it needs. This is the
// production binding of that interface, and the only place the two are joined.
package ic10tables

import (
	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/sema"
)

// Tables resolves the machine names an intrinsic argument may write. The zero
// value is ready to use: the generated tables are package level and immutable,
// so there is nothing to configure and nothing to copy.
type Tables struct{}

// LogicType resolves a device property name such as "Pressure". The chip
// matches these case-sensitively, so a name differing only in case is a
// different property and does not resolve.
func (Tables) LogicType(name string) (sema.Member, bool) {
	info, ok := ic10.LookupLogicType(name)
	return sema.Member{Value: int(info.Value), Deprecated: info.Deprecated}, ok
}

// LogicSlotType resolves a slot property name such as "Occupied".
func (Tables) LogicSlotType(name string) (sema.Member, bool) {
	info, ok := ic10.LookupLogicSlotType(name)
	return sema.Member{Value: int(info.Value), Deprecated: info.Deprecated}, ok
}

// BatchMode resolves a batch aggregation mode name.
func (Tables) BatchMode(name string) (sema.Member, bool) {
	info, ok := ic10.LookupBatchMode(name)
	return sema.Member{Value: int(info.Value), Deprecated: info.Deprecated}, ok
}

// ReagentMode resolves a reagent mode name.
func (Tables) ReagentMode(name string) (sema.Member, bool) {
	info, ok := ic10.LookupReagentMode(name)
	return sema.Member{Value: int(info.Value), Deprecated: info.Deprecated}, ok
}

// Prefab resolves what a batch instruction's prefab hash names.
func (Tables) Prefab(hash int32) (sema.Prefab, bool) {
	info, ok := ic10.LookupPrefabHash(hash)
	if !ok {
		return nil, false
	}
	return prefab{info}, true
}

// PrefabNamed resolves what a __ic_hash argument names.
func (Tables) PrefabNamed(name string) (sema.Prefab, bool) {
	info, ok := ic10.LookupPrefab(name)
	if !ok {
		return nil, false
	}
	return prefab{info}, true
}

// prefab answers sema.Prefab from one roster entry.
//
// It carries no access values across the boundary and cannot: internal/ic10
// keeps the pair of directions a property allows to itself, and answers only
// the two refusal questions. Analysis therefore has nothing to compare, and no
// way to read an undecided property as a denied one.
type prefab struct{ info ic10.PrefabInfo }

func (p prefab) Name() string { return p.info.Name }

func (p prefab) Title() string { return p.info.Title }

func (p prefab) NumSlots() int { return len(p.info.Slots) }

func (p prefab) HoldsCircuit() (holds, known bool) {
	return p.info.CircuitHolder, !p.info.CircuitHolderUnknown
}

func (p prefab) NumModes() (int, bool) {
	return len(p.info.Modes), !p.info.ModesUnknown
}

func (p prefab) RefusesLogic(logicType int, dir sema.Direction) bool {
	if dir == sema.Writing {
		return p.info.RefusesWrite(ic10.LogicType(logicType))
	}
	return p.info.RefusesRead(ic10.LogicType(logicType))
}

func (p prefab) RefusesSlot(slot, slotType int, dir sema.Direction) bool {
	if slot < 0 || slot >= len(p.info.Slots) {
		// A slot the prefab does not declare is out of range rather than
		// refused, and analysis reports that before asking this.
		return false
	}
	if dir == sema.Writing {
		return p.info.Slots[slot].RefusesWrite(ic10.LogicSlotType(slotType))
	}
	return p.info.Slots[slot].RefusesRead(ic10.LogicSlotType(slotType))
}
