package vm

import (
	"errors"
	"fmt"
	"strconv"
)

// ExceptionType is the chip's own error taxonomy. Values are the ordinals of
// ProgrammableChipException.ICExceptionType and are what the game serialises
// over the network, so they are stable identifiers a differential test can
// compare against another implementation.
type ExceptionType uint8

// The exception types, in game enum order.
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

func (t ExceptionType) String() string {
	if int(t) >= len(exceptionNames) {
		return "ExceptionType(" + strconv.FormatUint(uint64(t), 10) + ")"
	}
	return exceptionNames[t]
}

// Fault is a chip exception raised at a specific source line. It is the only
// error a caller should need to inspect: everything the chip can go wrong with,
// at compile time or at run time, arrives as one of these.
//
// Line is the zero-based source line, matching the number the chip displays.
type Fault struct {
	Type ExceptionType
	Line int
}

func (f *Fault) Error() string {
	return "line " + strconv.Itoa(f.Line) + ": " + f.Type.String()
}

// Is lets errors.Is match faults by type alone, so a test can write
// errors.Is(err, &vm.Fault{Type: vm.ExcStackUnderFlow, Line: -1}) when the line
// is not the interesting part.
func (f *Fault) Is(target error) bool {
	other, ok := target.(*Fault)
	if !ok {
		return false
	}
	if other.Line >= 0 && other.Line != f.Line {
		return false
	}
	return other.Type == f.Type
}

func newFault(t ExceptionType, line int) error { return &Fault{Type: t, Line: line} }

// errRuntime marks a condition the game hits as a plain .NET exception rather
// than a ProgrammableChipException: an array index out of range, a null
// dictionary key. The chip catches those separately and reports ExcUnknown at
// the line it was executing, so they must stay distinguishable from a Fault all
// the way up to the tick loop.
var errRuntime = errors.New("host exception")

// errStackUnderflow and errStackOverflow are the game's StackUnderflowException
// and StackOverflowException, which derive from SystemException rather than
// from ProgrammableChipException. That inheritance is the whole reason get and
// put report an out of range address differently: put catches them and maps
// them to specific types, while get lets them reach the tick loop's general
// clause and become ExcUnknown.
var (
	errStackUnderflow = fmt.Errorf("memory address below zero: %w", errRuntime)
	errStackOverflow  = fmt.Errorf("memory address past the array: %w", errRuntime)
)

// errNullDevice is the game's NullReferenceException as put's catch clause sees
// it, which that clause maps to ExcDeviceNotFound.
var errNullDevice = fmt.Errorf("null device: %w", errRuntime)

// ErrUnimplemented marks a path this interpreter does not model, currently the
// reagent instructions. It is never converted into a Fault: an oracle that
// reported ExcUnknown here would be indistinguishable from the chip genuinely
// failing, so Tick returns it unchanged and leaves the error state alone.
//
// Callers that sweep the instruction set should test for it with errors.Is and
// treat it as "no answer", not as a mismatch.
var ErrUnimplemented = errors.New("unimplemented")

// asFault converts any error raised while executing or compiling a line into
// the fault the chip would record, mirroring the two catch clauses in
// ProgrammableChip.Execute and ProgrammableChip.SetSourceCode: a chip exception
// keeps its own type and line, anything else becomes ExcUnknown at the line
// that was running.
func asFault(err error, line int) *Fault {
	var fault *Fault
	if errors.As(err, &fault) {
		return fault
	}
	return &Fault{Type: ExcUnknown, Line: line}
}

// hostErrorf builds an error that the chip would surface as ExcUnknown. The
// message is for humans reading a test failure; only the errRuntime marker is
// load bearing.
func hostErrorf(format string, args ...any) error {
	return fmt.Errorf("%s: %w", fmt.Sprintf(format, args...), errRuntime)
}
