package vm

import (
	"math"

	"github.com/greg2010/ic11c/internal/ic10"
)

var comparisonCases = []instructionCase{
	{
		name: "slt", op: ic10.OpSlt,
		source:        "slt r0 1 2\nslt r1 2 1",
		wantRegisters: map[ic10.Register]float64{0: 1, 1: 0},
	},
	{
		name: "sgt", op: ic10.OpSgt,
		source:        "sgt r0 2 1\nsgt r1 1 2",
		wantRegisters: map[ic10.Register]float64{0: 1, 1: 0},
	},
	{
		name: "sle", op: ic10.OpSle,
		source:        "sle r0 2 2\nsle r1 3 2",
		wantRegisters: map[ic10.Register]float64{0: 1, 1: 0},
	},
	{
		name: "sge", op: ic10.OpSge,
		source:        "sge r0 2 2\nsge r1 1 2",
		wantRegisters: map[ic10.Register]float64{0: 1, 1: 0},
	},
	{
		name: "seq", op: ic10.OpSeq,
		source:        "seq r0 2 2\nseq r1 1 2",
		wantRegisters: map[ic10.Register]float64{0: 1, 1: 0},
	},
	{
		name: "sne", op: ic10.OpSne,
		source:        "sne r0 1 2\nsne r1 2 2",
		wantRegisters: map[ic10.Register]float64{0: 1, 1: 0},
	},
	{
		// Every comparison is a plain double comparison, so NaN is neither less
		// than nor equal to anything, including itself.
		name: "comparisons against NaN are all false", op: ic10.OpSeq,
		registers:     map[ic10.Register]float64{5: math.NaN()},
		source:        "seq r0 r5 r5\nslt r1 r5 1\nsge r2 r5 1",
		wantRegisters: map[ic10.Register]float64{0: 0, 1: 0, 2: 0},
	},
	{
		name: "sap within a relative tolerance", op: ic10.OpSap,
		source:        "sap r0 1000 1000.5 0.001\nsap r1 1000 1002 0.001",
		wantRegisters: map[ic10.Register]float64{0: 1, 1: 0},
	},
	{
		name: "sna is the complement of sap", op: ic10.OpSna,
		source:        "sna r0 1000 1000.5 0.001\nsna r1 1000 1002 0.001",
		wantRegisters: map[ic10.Register]float64{0: 0, 1: 1},
	},
	{
		name: "sapz compares against zero", op: ic10.OpSapz,
		source:        "sapz r0 0 0.001\nsapz r1 5 0.001",
		wantRegisters: map[ic10.Register]float64{0: 1, 1: 0},
	},
	{
		name: "snaz compares against zero", op: ic10.OpSnaz,
		source:        "snaz r0 0 0.001\nsnaz r1 5 0.001",
		wantRegisters: map[ic10.Register]float64{0: 0, 1: 1},
	},
	{
		name: "sltz", op: ic10.OpSltz,
		source:        "sltz r0 -1\nsltz r1 0",
		wantRegisters: map[ic10.Register]float64{0: 1, 1: 0},
	},
	{
		name: "sgtz", op: ic10.OpSgtz,
		source:        "sgtz r0 1\nsgtz r1 0",
		wantRegisters: map[ic10.Register]float64{0: 1, 1: 0},
	},
	{
		name: "slez", op: ic10.OpSlez,
		source:        "slez r0 0\nslez r1 1",
		wantRegisters: map[ic10.Register]float64{0: 1, 1: 0},
	},
	{
		name: "sgez", op: ic10.OpSgez,
		source:        "sgez r0 0\nsgez r1 -1",
		wantRegisters: map[ic10.Register]float64{0: 1, 1: 0},
	},
	{
		name: "seqz", op: ic10.OpSeqz,
		source:        "seqz r0 0\nseqz r1 1",
		wantRegisters: map[ic10.Register]float64{0: 1, 1: 0},
	},
	{
		name: "snez", op: ic10.OpSnez,
		source:        "snez r0 1\nsnez r1 0",
		wantRegisters: map[ic10.Register]float64{0: 1, 1: 0},
	},
	{
		name: "snan", op: ic10.OpSnan,
		registers:     map[ic10.Register]float64{5: math.NaN()},
		source:        "snan r0 r5\nsnan r1 1",
		wantRegisters: map[ic10.Register]float64{0: 1, 1: 0},
	},
	{
		name: "snanz is the complement of snan", op: ic10.OpSnanz,
		registers:     map[ic10.Register]float64{5: math.NaN()},
		source:        "snanz r0 r5\nsnanz r1 1",
		wantRegisters: map[ic10.Register]float64{0: 0, 1: 1},
	},
	{
		// select tests its condition against zero, so any non-zero value
		// including NaN selects the first branch.
		name: "select takes the first branch for a NaN condition", op: ic10.OpSelect,
		registers:     map[ic10.Register]float64{5: math.NaN()},
		source:        "select r0 r5 2 3\nselect r1 0 2 3\nselect r2 1 2 3",
		wantRegisters: map[ic10.Register]float64{0: 2, 1: 3, 2: 2},
	},
}
