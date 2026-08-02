package difftest

import (
	"github.com/greg2010/ic11c/internal/vm"
)

// unmappedPrefix marks a chip exception with no harness counterpart. It is
// deliberately a name no emulator can produce, so an unmapped fault always
// reads as a mismatch that names our own type rather than silently matching
// something unrelated.
const unmappedPrefix = "ic11c."

// harnessErrorNames maps a chip exception onto the variant path the ic10emu
// harness prints for the same condition.
//
// Only entries backed by an observed harness response are listed. Our taxonomy
// is the game's and the harness's is its own, so several of ours cover more
// ground than one harness variant does: ExcShiftOverflow stands for both
// ShiftOverflowI64 and ShiftOverflowI32, and ExcIncorrectVariable stands for
// UnknownIdentifier and IncorrectOperandType alike. Each such entry names the
// variant the generated corpus can actually reach; a program reaching the other
// one is a mismatch, which is the honest outcome for a one-to-many mapping.
var harnessErrorNames = map[vm.ExceptionType]string{
	vm.ExcStackUnderFlow:          "MemoryError.StackUnderflow",
	vm.ExcStackOverFlow:           "MemoryError.StackOverflow",
	vm.ExcMemoryNotReadable:       "MemoryError.NotReadable",
	vm.ExcMemoryNotWriteable:      "MemoryError.NotWriteable",
	vm.ExcOutOfRegisterBounds:     "RegisterIndexOutOfRange",
	vm.ExcOutOfDeviceBounds:       "DeviceIndexOutOfRange",
	vm.ExcDeviceNotFound:          "DeviceNotSet",
	vm.ExcIncorrectLogicType:      "UnknownLogicType",
	vm.ExcIncorrectLogicSlotType:  "UnknownLogicSlotType",
	vm.ExcIncorrectReagentMode:    "UnknownReagentMode",
	vm.ExcIncorrectVariable:       "UnknownIdentifier",
	vm.ExcShiftOverflow:           "ShiftOverflowI64",
	vm.ExcShiftUnderflow:          "ShiftUnderflowI64",
	vm.ExcJumpTagDuplicate:        "DuplicateLabel",
	vm.ExcExtraDefine:             "DuplicateDefine",
	vm.ExcUnrecognisedInstruction: "ParseError",
}

// HarnessErrorName renders a chip exception as the error type string an oracle
// Result carries, so that our faults and a harness's are comparable rather than
// each needing a divergence entry to excuse the difference in vocabulary.
//
// ExcNone is the empty string, matching a Result with no error. A type with no
// established counterpart comes back prefixed with "ic11c." so it can never
// coincide with a harness name.
func HarnessErrorName(t vm.ExceptionType) string {
	if t == vm.ExcNone {
		return ""
	}
	if name, ok := harnessErrorNames[t]; ok {
		return name
	}
	return unmappedPrefix + t.String()
}
