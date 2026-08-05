package sema

import (
	"slices"

	"github.com/greg2010/ic11c/internal/source"
)

// preludeTypeNames are the type names the generated C header introduces,
// less dev, which is a MicroC keyword the parser refuses first. They are
// reserved because a MicroC program is read as C through that header, where
// redeclaring one of these redefines a typedef and is a hard error.
var preludeTypeNames = []string{"ic10_logic", "ic10_slot", "ic10_batch", "ic10_reagent"}

func isPreludeTypeName(name string) bool { return slices.Contains(preludeTypeNames, name) }

// checkReserved rejects a declaration of a name the language keeps for
// itself: the intrinsic prefix, device pin spellings, machine names an
// intrinsic argument carries, the machine's constants, and the C prelude's
// type names — all of which resolve without consulting scope.
func (c *checker) checkReserved(pos source.Position, name string) {
	switch {
	case isReservedName(name):
		c.errorf(pos, "a name beginning '%s' is reserved for intrinsics", reservedPrefix)
	case isDevicePinSpelling(name):
		c.errorf(pos, "'%s' names a device pin; the spelling is reserved, since a device position resolves it without consulting scope", name)
	case isMachineConstant(name):
		c.errorf(pos, "'%s' is one of the machine's own constants, predeclared as a constexpr double; the spelling is reserved", name)
	case isPreludeTypeName(name):
		c.errorf(pos, "'%s' is the name the C prelude gives one of the operand types; the spelling is reserved, since a C editor reading this program as C would see a redefinition", name)
	default:
		if kind, taken := c.machineName(name); taken {
			c.errorf(pos, "'%s' is a %s; the spelling is reserved, since an intrinsic argument resolves a machine name without consulting scope", name, kind)
		}
	}
}

// machineName reports which family of machine names holds name, and false
// where none does. It runs the same resolution an intrinsic argument does,
// so what a declaration may not spell is exactly what some operand position
// resolves.
func (c *checker) machineName(name string) (OperandKind, bool) {
	for _, kind := range namedOperandKinds {
		switch spelling, _, _ := c.resolveOperandName(kind, name); spelling {
		case spellingBare, spellingPrefixed:
			return kind, true
		case spellingShadowed:
			// The earlier family that owns the bare spelling reports it, and
			// this position resolves nothing under that name.
		case spellingUnknown, spellingOverPrefixed:
		}
	}
	return OperandValue, false
}
