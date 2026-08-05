package chip

import (
	"errors"
	"fmt"

	"github.com/greg2010/ic11c/internal/source"
)

// ErrUnavailable reports that the chip could not be started or could not be
// spoken to. It is deliberately distinct from every chip verdict: a caller that
// gets one has measured nothing, and reporting it as a clean run is the failure
// this separation exists to make impossible.
var ErrUnavailable = errors.New("chip harness unavailable")

// ExceptionType is the chip's own error taxonomy: one constant per member of
// ProgrammableChipException.ICExceptionType. The name is the contract, not the
// ordinal: every comparison uses these constants, so renumbering the game enum
// is harmless but renaming or adding a member fails to resolve here.
type ExceptionType uint8

// The exception types, in game enum order. [buildExceptionTypes] walks them, so
// they must stay contiguous with [ExcAliasNotFound] last.
const (
	ExcNone ExceptionType = iota
	// ExcUnknown is what the chip reports for any .NET exception that is not
	// one of its own. Several reachable operand mistakes land here rather than
	// in a specific type, which is why it is not a catch-all to be ignored.
	ExcUnknown
	ExcIncorrectVariable
	ExcIncorrectVariableType
	ExcIncorrectLogicType
	ExcIncorrectLogicSlotType
	ExcIncorrectArgumentCount
	ExcDeviceNotSet
	ExcJumpTagDuplicate
	ExcUnrecognisedInstruction
	ExcIndexOutOfRange
	ExcOutOfRegisterBounds
	ExcOutOfDeviceBounds
	ExcIncorrectReagentMode
	ExcUnhandledReagentMode
	ExcIncorrectReagentDevice
	ExcChipCatchingFire
	ExcStackOverFlow
	ExcStackUnderFlow
	ExcExtraDefine
	ExcDeviceListNull
	ExcIncorrectReagentType
	ExcDeviceNotSlotWriteable
	ExcShiftOverflow
	ExcShiftUnderflow
	ExcDeviceNotFound
	ExcMemoryNotReadable
	ExcMemoryNotWriteable
	ExcInvalidStringLength
	ExcInvalidStringNull
	ExcInvalidStringNonASCII
	ExcInvalidPreprocessHash
	ExcInvalidProcessBinary
	ExcInvalidPreprocessHex
	ExcLogicTypeIsNone
	ExcPayloadOverflow
	ExcPayloadUnderflow
	ExcInvalidInteger
	ExcAliasNotFound
)

var exceptionNames = [...]string{
	ExcNone:                    "None",
	ExcUnknown:                 "Unknown",
	ExcIncorrectVariable:       "IncorrectVariable",
	ExcIncorrectVariableType:   "IncorrectVariableType",
	ExcIncorrectLogicType:      "IncorrectLogicType",
	ExcIncorrectLogicSlotType:  "IncorrectLogicSlotType",
	ExcIncorrectArgumentCount:  "IncorrectArgumentCount",
	ExcDeviceNotSet:            "DeviceNotSet",
	ExcJumpTagDuplicate:        "JumpTagDuplicate",
	ExcUnrecognisedInstruction: "UnrecognisedInstruction",
	ExcIndexOutOfRange:         "IndexOutOfRange",
	ExcOutOfRegisterBounds:     "OutOfRegisterBounds",
	ExcOutOfDeviceBounds:       "OutOfDeviceBounds",
	ExcIncorrectReagentMode:    "IncorrectReagentMode",
	ExcUnhandledReagentMode:    "UnhandledReagentMode",
	ExcIncorrectReagentDevice:  "IncorrectReagentDevice",
	ExcChipCatchingFire:        "ChipCatchingFire",
	ExcStackOverFlow:           "StackOverFlow",
	ExcStackUnderFlow:          "StackUnderFlow",
	ExcExtraDefine:             "ExtraDefine",
	ExcDeviceListNull:          "DeviceListNull",
	ExcIncorrectReagentType:    "IncorrectReagentType",
	ExcDeviceNotSlotWriteable:  "DeviceNotSlotWriteable",
	ExcShiftOverflow:           "ShiftOverflow",
	ExcShiftUnderflow:          "ShiftUnderflow",
	ExcDeviceNotFound:          "DeviceNotFound",
	ExcMemoryNotReadable:       "MemoryNotReadable",
	ExcMemoryNotWriteable:      "MemoryNotWriteable",
	ExcInvalidStringLength:     "InvalidStringLength",
	ExcInvalidStringNull:       "InvalidStringNull",
	ExcInvalidStringNonASCII:   "InvalidStringNonAscii",
	ExcInvalidPreprocessHash:   "InvalidPreprocessHash",
	ExcInvalidProcessBinary:    "InvalidProcessBinary",
	ExcInvalidPreprocessHex:    "InvalidPreprocessHex",
	ExcLogicTypeIsNone:         "LogicTypeIsNone",
	ExcPayloadOverflow:         "PayloadOverflow",
	ExcPayloadUnderflow:        "PayloadUnderflow",
	ExcInvalidInteger:          "InvalidInteger",
	ExcAliasNotFound:           "AliasNotFound",
}

// String is the name the game's enum carries, which is also what the harness
// prints in a state block.
func (t ExceptionType) String() string {
	return source.EnumName(exceptionNames[:], int(t), "ExceptionType")
}

// exceptionTypes maps the name the harness prints back onto the taxonomy,
// derived from [ExceptionType.String] so a new game member reaches both
// directions. A name with no entry is an error, not an unknown bucket — it
// means the enum moved and that has to be read, not silently ignored.
var exceptionTypes = buildExceptionTypes()

func buildExceptionTypes() map[string]ExceptionType {
	out := make(map[string]ExceptionType, int(ExcAliasNotFound)+1)
	for t := ExcNone; t <= ExcAliasNotFound; t++ {
		out[t.String()] = t
	}
	return out
}

func parseExceptionType(name string) (ExceptionType, error) {
	t, ok := exceptionTypes[name]
	if !ok {
		return 0, fmt.Errorf("no chip exception is named %q", name)
	}
	return t, nil
}

// Fault is an error the chip recorded.
//
// Line is the zero-based source line, and is meaningful only when Type is not
// [ExcNone].
type Fault struct {
	Type ExceptionType
	Line int
}

func (f Fault) String() string {
	if f.Type == ExcNone {
		return "none"
	}
	return fmt.Sprintf("%s at line %d", f.Type, f.Line)
}
