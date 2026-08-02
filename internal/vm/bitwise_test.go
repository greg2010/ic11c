package vm

import (
	"github.com/greg2010/ic11c/internal/ic10"
)

var bitwiseCases = []instructionCase{
	{
		name: "and", op: ic10.OpAnd,
		source:        "and r0 6 3",
		wantRegisters: map[ic10.Register]float64{0: 2},
	},
	{
		// The value reduces modulo 2^53 on the way in, so a bit at 2^53 is gone
		// before the operation runs.
		name: "and loses everything above the payload width", op: ic10.OpAnd,
		source:        "and r0 9007199254740992 9007199254740992",
		wantRegisters: map[ic10.Register]float64{0: 0},
	},
	{
		name: "and keeps the last exactly representable integer", op: ic10.OpAnd,
		source:        "and r0 9007199254740991 9007199254740991",
		wantRegisters: map[ic10.Register]float64{0: twoPow53 - 1},
	},
	{
		name: "or", op: ic10.OpOr,
		source:        "or r0 6 3",
		wantRegisters: map[ic10.Register]float64{0: 7},
	},
	{
		name: "xor", op: ic10.OpXor,
		source:        "xor r0 6 3",
		wantRegisters: map[ic10.Register]float64{0: 5},
	},
	{
		name: "nor sign extends from bit 53 on the way back", op: ic10.OpNor,
		source:        "nor r0 6 3",
		wantRegisters: map[ic10.Register]float64{0: -8},
	},
	{
		name: "not", op: ic10.OpNot,
		source:        "not r0 0\nnot r1 1",
		wantRegisters: map[ic10.Register]float64{0: -1, 1: -2},
	},
	{
		name: "srl treats its value as unsigned across 54 bits", op: ic10.OpSrl,
		source:        "srl r0 -1 1",
		wantRegisters: map[ic10.Register]float64{0: twoPow53 - 1},
	},
	{
		name: "sra keeps the sign", op: ic10.OpSra,
		source:        "sra r0 -1 1\nsra r1 8 2",
		wantRegisters: map[ic10.Register]float64{0: -1, 1: 2},
	},
	{
		// sla and sll construct the same class, and it zero fills. The in-game
		// help text claiming sla sign fills is wrong.
		name: "sll and sla agree and both zero fill", op: ic10.OpSll,
		source:        "sll r0 -1 1\nsla r1 -1 1",
		wantRegisters: map[ic10.Register]float64{0: -2, 1: -2},
	},
	{
		name: "sla is byte identical to sll", op: ic10.OpSla,
		source:        "sla r0 3 2\nsll r1 3 2",
		wantRegisters: map[ic10.Register]float64{0: 12, 1: 12},
	},
	{
		// Shifting into bit 53 makes the round trip read the result as negative.
		name: "sll into the sign position corrupts the result", op: ic10.OpSll,
		source:        "sll r0 1 53",
		wantRegisters: map[ic10.Register]float64{0: -twoPow53},
	},
	{
		// The rotate width is 54, one wider than the payload cap on ext and ins,
		// so bit 53 is a value bit here and a sign bit on the way back out.
		name: "ror rotates over 54 bits", op: ic10.OpRor,
		source:        "ror r0 1 1",
		wantRegisters: map[ic10.Register]float64{0: -twoPow53},
	},
	{
		name: "rol by the full width is the identity", op: ic10.OpRol,
		source:        "rol r0 1 54\nrol r1 1 2",
		wantRegisters: map[ic10.Register]float64{0: 1, 1: 4},
	},
	{
		name: "rol reduces a negative distance into range", op: ic10.OpRol,
		source:        "rol r0 4 -1\nror r1 4 1",
		wantRegisters: map[ic10.Register]float64{0: 2, 1: 2},
	},
	{
		name: "ext pulls a field out", op: ic10.OpExt,
		source:        "ext r0 11 1 2",
		wantRegisters: map[ic10.Register]float64{0: 1},
	},
	{
		name: "ext caps its payload at 53 bits", op: ic10.OpExt,
		source:    "ext r0 1 1 53",
		wantFault: &Fault{Type: ExcPayloadOverflow, Line: 0},
	},
	{
		name: "ext rejects a width past the payload cap", op: ic10.OpExt,
		source:    "ext r0 1 0 54",
		wantFault: &Fault{Type: ExcPayloadOverflow, Line: 0},
	},
	{
		name: "ext rejects an offset at the payload cap", op: ic10.OpExt,
		source:    "ext r0 1 53 1",
		wantFault: &Fault{Type: ExcShiftOverflow, Line: 0},
	},
	{
		name: "ext rejects a zero width", op: ic10.OpExt,
		source:    "ext r0 1 0 0",
		wantFault: &Fault{Type: ExcShiftUnderflow, Line: 0},
	},
	{
		name: "ext rejects a negative offset", op: ic10.OpExt,
		source:    "ext r0 1 -1 2",
		wantFault: &Fault{Type: ExcShiftUnderflow, Line: 0},
	},
	{
		name: "ext accepts the full payload width at offset zero", op: ic10.OpExt,
		source:        "ext r0 9007199254740991 0 53",
		wantRegisters: map[ic10.Register]float64{0: twoPow53 - 1},
	},
	{
		name: "ins writes a field into the destination", op: ic10.OpIns,
		source:        "ins r0 1 2 1",
		wantRegisters: map[ic10.Register]float64{0: 4},
	},
	{
		name: "ins leaves the bits outside the field alone", op: ic10.OpIns,
		registers:     map[ic10.Register]float64{0: 0b1111},
		source:        "ins r0 0 1 2",
		wantRegisters: map[ic10.Register]float64{0: 0b1001},
	},
	{
		name: "ins caps its payload at 53 bits", op: ic10.OpIns,
		source:    "ins r0 1 1 53",
		wantFault: &Fault{Type: ExcPayloadOverflow, Line: 0},
	},
}
