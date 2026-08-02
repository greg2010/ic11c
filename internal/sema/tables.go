package sema

import (
	"hash/crc32"
	"strings"
)

// Tables resolves the machine names an intrinsic argument may write. Logic
// types, slot types, batch modes, and reagent modes are ordinary identifiers to
// the grammar and carry meaning only in an intrinsic argument position; their
// spellings and encodings come from tables generated from the game's own
// assembly.
//
// Analysis depends on the interface rather than on the generated package so
// that type checking is not a compile-time dependency of the tables, and so a
// test can supply the handful of names it needs. Every method reports false for
// a name the tables do not hold, which analysis turns into a diagnostic naming
// the category.
//
// Device pins are not here: the language fixes them at db and d0 through d5,
// which is a property of the housing rather than of a generated table.
type Tables interface {
	// LogicType resolves a logic type name such as "Pressure".
	LogicType(name string) (Member, bool)
	// LogicSlotType resolves a slot property name such as "Occupied".
	LogicSlotType(name string) (Member, bool)
	// BatchMode resolves a batch mode name: Average, Sum, Minimum, Maximum, or
	// Count.
	BatchMode(name string) (Member, bool)
	// ReagentMode resolves a reagent mode name: Contents, Required, Recipe, or
	// TotalContents.
	ReagentMode(name string) (Member, bool)
}

// Member is one resolved machine name.
//
// Deprecated is carried because the game marks 23 of the 358 logic types
// retired while still resolving every one of them. Nothing about the emitted
// program changes, so it is the programmer rather than the compiler that has to
// act on it.
type Member struct {
	// Value is the integer the machine encodes the name as.
	Value int
	// Deprecated reports whether the game's own tables list the member as
	// retired.
	Deprecated bool
}

// maxDevicePin is the highest pin a housing has. d6 and above compile on the
// chip and then fault once per tick, so the compiler is the only thing that can
// reject them.
const maxDevicePin = 5

// devicePin resolves a device operand spelling to its pin number, reporting
// -1 for db, which addresses the housing rather than a pin.
func devicePin(name string) (int64, bool) {
	if name == "db" {
		return -1, true
	}
	digits, ok := strings.CutPrefix(name, "d")
	if !ok || len(digits) != 1 || digits[0] < '0' || digits[0] > '9' {
		return 0, false
	}
	n := int64(digits[0] - '0')
	if n > maxDevicePin {
		return 0, false
	}
	return n, true
}

// isDevicePinSpelling reports whether name is one a device position resolves,
// which is what makes it a spelling no declaration may take.
func isDevicePinSpelling(name string) bool {
	_, ok := devicePin(name)
	return ok
}

// hashString is the compile-time value of __ic_hash: the CRC-32 of the
// literal's bytes reinterpreted as a signed 32-bit integer, which is how the
// game derives a prefab hash from a name.
func hashString(s string) int64 {
	return int64(int32(crc32.ChecksumIEEE([]byte(s))))
}
