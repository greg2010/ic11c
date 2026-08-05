package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ISA is the canonical extracted description of the IC10 instruction set and
// its operand enums. It is the on-disk contract between the extract and
// generate stages, written from a decompiled game assembly and read back to
// emit Go. Every slice is emitted in game declaration order, so two
// extractions of the same build produce identical bytes.
type ISA struct {
	// Manifest is the gid of the Steam depot manifest the assembly was fetched
	// from -- the exact bytes a re-extraction has to reach to land on this
	// table again. A build id names a build across a whole depot and does not
	// pin any one of them.
	Manifest string `json:"manifest"`
	// Version is the four-part file version the game stamps into the
	// assembly's PE resource.
	Version      string        `json:"version"`
	Instructions []Instruction `json:"instructions"`
	LogicTypes   []EnumMember  `json:"logic_types"`
	SlotTypes    []EnumMember  `json:"logic_slot_types"`
	BatchModes   []EnumMember  `json:"logic_batch_methods"`
	ReagentModes []EnumMember  `json:"logic_reagent_modes"`
	Constants    []Constant    `json:"constants"`
	BasicEnums   []BasicEnum   `json:"basic_enums"`
}

// BasicEnum is one BasicEnum entry of ProgrammableChip.InternalEnums: a game
// enum whose members an operand may name wherever it accepts an enum at all.
type BasicEnum struct {
	// Prefix is the type name a member has to be qualified with. It is empty
	// for the one entry the game registers without one, whose members match
	// bare in every value position.
	Prefix string `json:"prefix"`
	// Type is the C# enum the members were read from, which is not always the
	// prefix the game exposes them under.
	Type    string       `json:"type"`
	Members []EnumMember `json:"members"`
}

// Instruction is one ScriptCommand member together with the operand signature
// recovered from ProgrammableChip.GetCommandExample.
type Instruction struct {
	Mnemonic   string    `json:"mnemonic"`
	Opcode     int       `json:"opcode"`
	Deprecated bool      `json:"deprecated"`
	Example    string    `json:"example"`
	Operands   []Operand `json:"operands"`
	// Implicit lists the registers the instruction reaches without an operand
	// naming them, ordered by register name. It is empty for the instructions
	// that reach none, which is all but the stack forms and the linking jumps.
	Implicit []ImplicitUse `json:"implicit,omitempty"`
}

// ImplicitUse is one register an instruction reaches without an operand
// naming it: the stack pointer the stack forms move, and the return address
// the linking jumps leave behind. Register is the machine's own name, which a
// caller resolves against its own register file rather than a table index.
type ImplicitUse struct {
	Register  string    `json:"register"`
	Direction Direction `json:"direction"`
}

// Operand is one positional operand. Name is the variable name the game's help
// text gives it and is empty for the unnamed forms. Kinds lists the accepted
// alternatives in source order; it always holds at least one entry.
type Operand struct {
	Name       string     `json:"name,omitempty"`
	Kinds      []string   `json:"kinds"`
	Direction  Direction  `json:"direction"`
	Conversion Conversion `json:"conversion"`
}

// Direction is what an instruction does to the register an operand names. It is
// read from the operation class the chip's parser builds, not inferred from the
// operand's position, so a build that moves a write is carried rather than
// missed.
type Direction string

// The operand directions. DirectionReadWrite is a use as well as a
// definition: the register an instruction folds a new value into. No
// extraction produces DirectionUnknown -- an undetermined operand stops
// extraction -- but a hand-edited table can still carry it, so it refuses.
const (
	DirectionRead      Direction = "read"
	DirectionWrite     Direction = "write"
	DirectionReadWrite Direction = "readwrite"
	DirectionUnknown   Direction = "unknown"
)

// Conversion is what the chip does to the value an operand's register holds
// on the way into the instruction. It is per operand rather than per
// instruction -- a shift reduces its value and bounds its distance
// differently -- and is read from the call the operation class makes on that
// operand's variable, not from the operand's kind or position.
type Conversion string

// The operand conversions, each named for the reader the chip calls and each
// with a bound and an outcome of its own. ConversionUnknown says nothing and
// no extraction produces it, for the same reason as DirectionUnknown.
const (
	// ConversionNone is an operand no conversion below governs, not the absence
	// of every reduction the machine performs: it covers the plain double
	// GetVariableValue hands back (ProgrammableChip.cs:1708), an operand read
	// as a register or device, and one narrowed by its own variable class --
	// untracked here, since that narrowing belongs to the variable class
	// rather than to any call the operation makes.
	ConversionNone Conversion = "none"
	// ConversionInt is GetVariableInt (ibid.:1694), which faults ShiftUnderflow
	// below -2^31 and ShiftOverflow above 2^31-1 and plain-casts what's left. A
	// NaN is not one of them: both guards are ordered comparisons, false for
	// NaN, so it reaches the same bare cast an in-range number does.
	ConversionInt Conversion = "int"
	// ConversionNarrowedInt is a cast to int the operation's own Execute body
	// applies unchecked to the double it read (ibid.:5417), so unlike
	// ConversionInt it never stops the chip. It truncates toward zero; the
	// game runs Mono on x86-64, where conv.i4 lowers to a bare cvttsd2si with
	// no range check, so a NaN, an infinity, or an out-of-range value all
	// answer -2147483648 -- not the saturating result .NET Core/ARM64 gives.
	ConversionNarrowedInt Conversion = "narrowed_int"
	// ConversionSignedLong is GetVariableLong with signed left at its true
	// default (ibid.:1680), faulting below -2^63 and above 2^63. It reduces
	// through DoubleToLong (ibid.:6790) -- (long)(d % 2^53), the identity
	// inside ±2^53 -- silently, using the same NaN-permissive guards as
	// ConversionInt.
	ConversionSignedLong Conversion = "signed_long"
	// ConversionUnsignedLong is the same reader called with signed: false,
	// which masks the residue to 54 bits rather than keeping its sign.
	ConversionUnsignedLong Conversion = "unsigned_long"
	// ConversionUnknown is an operand whose conversion the extraction could not
	// read out of the game source.
	ConversionUnknown Conversion = "unknown"
)

// EnumMember is one member of an operand enum. Value is the integer the game
// resolves the member name to, which is what generated code emits in place of
// the name.
type EnumMember struct {
	Name       string `json:"name"`
	Value      int64  `json:"value"`
	Deprecated bool   `json:"deprecated"`
}

// Constant is one of the numeric literals the chip's assembler accepts in
// place of a number. Value is a string rather than a JSON number because three
// of the nine are NaN or infinite and JSON cannot carry those. It always
// round-trips to the same bits.
type Constant struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// nanPrefix introduces the NaN spelling, which carries the payload rather than
// dropping it. .NET's double.NaN is 0xfff8000000000000 and Go's math.NaN is
// 0x7ff8000000000001; the two are not interchangeable, because `mod` propagates
// an operand's own pattern and a differential test compares the bits.
const nanPrefix = "NaN:"

// Float decodes the stored value. It fails rather than defaulting so a
// hand-edited or truncated table cannot silently change a folded constant.
func (c Constant) Float() (float64, error) {
	if payload, ok := strings.CutPrefix(c.Value, nanPrefix); ok {
		bits, err := strconv.ParseUint(payload, 0, 64)
		if err != nil {
			return 0, fmt.Errorf("constant %q: parse NaN payload %q: %w", c.Name, payload, err)
		}
		v := math.Float64frombits(bits)
		if !math.IsNaN(v) {
			return 0, fmt.Errorf("constant %q: payload %q is not a NaN", c.Name, payload)
		}
		return v, nil
	}
	v, err := strconv.ParseFloat(c.Value, 64)
	if err != nil {
		return 0, fmt.Errorf("constant %q: parse value %q: %w", c.Name, c.Value, err)
	}
	return v, nil
}

// formatFloat renders v in the shortest form that parses back to the same bits.
func formatFloat(v float64) string {
	switch {
	case math.IsNaN(v):
		return nanPrefix + "0x" + strconv.FormatUint(math.Float64bits(v), 16)
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	default:
		return strconv.FormatFloat(v, 'g', -1, 64)
	}
}

// Operand kind identifiers. These are the JSON spelling of the HelpString
// tokens the game's help text is built from; goOperandKinds maps each to the
// constant the generated package declares it as.
const (
	kindRegister      = "register"
	kindNumber        = "number"
	kindInteger       = "integer"
	kindDevice        = "device"
	kindRefID         = "refid"
	kindLogicType     = "logictype"
	kindLogicSlotType = "logicslottype"
	kindBatchMode     = "batchmode"
	kindReagentMode   = "reagentmode"
	kindDeviceHash    = "devicehash"
	kindNameHash      = "namehash"
	kindSlotIndex     = "slotindex"
	kindString        = "string"
)

// helpTokenKinds maps the literal token a HelpString renders to the operand
// kind it denotes. Extraction fails on any token absent here, so a game update
// that introduces a new operand class is reported rather than dropped.
var helpTokenKinds = map[string]string{
	"r?":            kindRegister,
	"num":           kindNumber,
	"int":           kindInteger,
	"d?":            kindDevice,
	"id":            kindRefID,
	"logicType":     kindLogicType,
	"logicSlotType": kindLogicSlotType,
	"batchMode":     kindBatchMode,
	"reagentMode":   kindReagentMode,
	"deviceHash":    kindDeviceHash,
	"nameHash":      kindNameHash,
	"slotIndex":     kindSlotIndex,
	"str":           kindString,
}

// kindTokens inverts helpTokenKinds so the generate stage can rebuild an
// operand's rendered spelling from the JSON alone.
var kindTokens = func() map[string]string {
	m := make(map[string]string, len(helpTokenKinds))
	for token, kind := range helpTokenKinds {
		m[kind] = token
	}
	return m
}()

// The extracted spelling of each vocabulary member against the constant the
// generated package declares it as. Each is derived from the vocabulary that
// renders the declarations, so a member the render stage cannot resolve is one
// the generated package does not declare.
var (
	goDirections      = directions.goNames()
	goConversions     = conversions.goNames()
	goOperandKinds    = operandKinds.goNames()
	goAccessConstants = accesses.goNames()
)

// goRegisters maps each register an instruction can reach without naming it
// to its index in the game's register file, which is the number the rendered
// tables carry. Nothing here shares a declaration with the compiler;
// TestMachineLimitsMatchTheGameSource and
// TestImplicitReachesAreTheWholeOfWhatTheTableRecords are what close the loop.
var goRegisters = map[string]string{
	"sp": "16",
	"ra": "17",
}
