package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ISA is the canonical extracted description of the IC10 instruction set and
// its operand enums. It is the on-disk contract between the extract and
// generate stages: extract writes it from a decompiled game assembly, generate
// reads it and emits Go. Field order here fixes the JSON key order, and every
// slice is emitted in game declaration order, so two extractions of the same
// build produce identical bytes.
type ISA struct {
	// Manifest is the gid of the Steam depot manifest the assembly was fetched
	// from. It names the exact bytes, which is what a re-extraction has to
	// reach to land on this table again; a build id names a build across every
	// depot of an app and does not pin any one of them.
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
}

// Operand is one positional operand. Name is the variable name the game's help
// text gives it and is empty for the unnamed forms. Kinds lists the accepted
// alternatives in source order; it always holds at least one entry.
type Operand struct {
	Name  string   `json:"name,omitempty"`
	Kinds []string `json:"kinds"`
}

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
// matching internal/ic10 constant.
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

// goOperandKinds maps each operand kind to its internal/ic10 constant name.
var goOperandKinds = map[string]string{
	kindRegister:      "OperandRegister",
	kindNumber:        "OperandNumber",
	kindInteger:       "OperandInteger",
	kindDevice:        "OperandDevice",
	kindRefID:         "OperandRefID",
	kindLogicType:     "OperandLogicType",
	kindLogicSlotType: "OperandLogicSlotType",
	kindBatchMode:     "OperandBatchMode",
	kindReagentMode:   "OperandReagentMode",
	kindDeviceHash:    "OperandDeviceHash",
	kindNameHash:      "OperandNameHash",
	kindSlotIndex:     "OperandSlotIndex",
	kindString:        "OperandString",
}
