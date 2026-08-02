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
