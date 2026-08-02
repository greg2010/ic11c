package vm

import (
	"sort"

	"github.com/greg2010/ic11c/internal/ic10"
)

// Support records what this interpreter provides for one mnemonic, so that a
// gap is visible rather than assumed away.
//
// Implemented and ParseImpossible are derived from the instruction table rather
// than restated, which is what stops the record drifting from the code. Unit,
// Fuzz and FuzzExcluded are the test dimensions, and each is held to what the
// tests and the differential generators actually do: TestUnitCoverageMatchesTests
// for the first, TestFuzzCoverageMatchesTheGenerators for the other two.
type Support struct {
	Mnemonic string
	Opcode   ic10.Opcode
	// Class is the C# operation the Go implementation derives from, the anchor
	// for diffing this package against a future game build.
	Class string
	// Implemented reports that a builder exists. Every mnemonic has one,
	// including the four that cannot be assembled, because the fuzzer generates
	// them and the failure has to be the right failure.
	Implemented bool
	// ParseImpossible reports that no operand count assembles, because the
	// game's own token count check disagrees with its operand read.
	ParseImpossible bool
	// Unit reports that a per-instruction test exercises the mnemonic.
	Unit bool
	// Fuzz reports that the differential corpus in internal/difftest reaches the
	// mnemonic.
	Fuzz bool
	// FuzzExcluded reports that the differential generators never emit the
	// mnemonic on purpose. It is a different gap from one they could reach and
	// happen not to, and internal/difftest.Excluded gives the reason for each.
	FuzzExcluded bool
}

// parseImpossible is the set whose token count check and operand read disagree.
// There is no operand count that assembles any of them.
var parseImpossible = map[ic10.Opcode]bool{
	ic10.OpBrapz:  true,
	ic10.OpBrnaz:  true,
	ic10.OpBapzal: true,
	ic10.OpBnazal: true,
}

// unitTested lists the mnemonics a per-instruction test covers. It is a
// declaration, not an observation: TestUnitCoverageMatchesTests compares it
// against the opcodes the test corpus actually runs and fails on either
// direction of drift, so adding a mnemonic here without a test is caught.
var unitTested = map[ic10.Opcode]bool{
	ic10.OpL:      true,
	ic10.OpS:      true,
	ic10.OpLs:     true,
	ic10.OpLr:     true,
	ic10.OpSb:     true,
	ic10.OpLb:     true,
	ic10.OpAlias:  true,
	ic10.OpMove:   true,
	ic10.OpAdd:    true,
	ic10.OpSub:    true,
	ic10.OpSdse:   true,
	ic10.OpSdns:   true,
	ic10.OpSlt:    true,
	ic10.OpSgt:    true,
	ic10.OpSle:    true,
	ic10.OpSge:    true,
	ic10.OpSeq:    true,
	ic10.OpSne:    true,
	ic10.OpSap:    true,
	ic10.OpSna:    true,
	ic10.OpAnd:    true,
	ic10.OpOr:     true,
	ic10.OpXor:    true,
	ic10.OpNor:    true,
	ic10.OpMul:    true,
	ic10.OpDiv:    true,
	ic10.OpMod:    true,
	ic10.OpJ:      true,
	ic10.OpBltz:   true,
	ic10.OpBgez:   true,
	ic10.OpBlez:   true,
	ic10.OpBgtz:   true,
	ic10.OpBdse:   true,
	ic10.OpBdns:   true,
	ic10.OpBeq:    true,
	ic10.OpBne:    true,
	ic10.OpBap:    true,
	ic10.OpBna:    true,
	ic10.OpJal:    true,
	ic10.OpBrdse:  true,
	ic10.OpBrdns:  true,
	ic10.OpBltzal: true,
	ic10.OpBgezal: true,
	ic10.OpBlezal: true,
	ic10.OpBgtzal: true,
	ic10.OpBeqal:  true,
	ic10.OpBneal:  true,
	ic10.OpJr:     true,
	ic10.OpBdseal: true,
	ic10.OpBdnsal: true,
	ic10.OpBrltz:  true,
	ic10.OpBrgez:  true,
	ic10.OpBrlez:  true,
	ic10.OpBrgtz:  true,
	ic10.OpBreq:   true,
	ic10.OpBrne:   true,
	ic10.OpBrap:   true,
	ic10.OpBrna:   true,
	ic10.OpSqrt:   true,
	ic10.OpRound:  true,
	ic10.OpTrunc:  true,
	ic10.OpCeil:   true,
	ic10.OpFloor:  true,
	ic10.OpMax:    true,
	ic10.OpMin:    true,
	ic10.OpAbs:    true,
	ic10.OpLog:    true,
	ic10.OpExp:    true,
	ic10.OpRand:   true,
	ic10.OpYield:  true,
	ic10.OpLabel:  true,
	ic10.OpPeek:   true,
	ic10.OpPush:   true,
	ic10.OpPop:    true,
	ic10.OpHcf:    true,
	ic10.OpSelect: true,
	ic10.OpBlt:    true,
	ic10.OpBgt:    true,
	ic10.OpBle:    true,
	ic10.OpBge:    true,
	ic10.OpBrlt:   true,
	ic10.OpBrgt:   true,
	ic10.OpBrle:   true,
	ic10.OpBrge:   true,
	ic10.OpBltal:  true,
	ic10.OpBgtal:  true,
	ic10.OpBleal:  true,
	ic10.OpBgeal:  true,
	ic10.OpBapal:  true,
	ic10.OpBnaal:  true,
	ic10.OpBeqz:   true,
	ic10.OpBnez:   true,
	ic10.OpBapz:   true,
	ic10.OpBnaz:   true,
	ic10.OpBreqz:  true,
	ic10.OpBrnez:  true,
	ic10.OpBrapz:  true,
	ic10.OpBrnaz:  true,
	ic10.OpBeqzal: true,
	ic10.OpBnezal: true,
	ic10.OpBapzal: true,
	ic10.OpBnazal: true,
	ic10.OpSltz:   true,
	ic10.OpSgtz:   true,
	ic10.OpSlez:   true,
	ic10.OpSgez:   true,
	ic10.OpSeqz:   true,
	ic10.OpSnez:   true,
	ic10.OpSapz:   true,
	ic10.OpSnaz:   true,
	ic10.OpDefine: true,
	ic10.OpSleep:  true,
	ic10.OpSin:    true,
	ic10.OpAsin:   true,
	ic10.OpTan:    true,
	ic10.OpAtan:   true,
	ic10.OpCos:    true,
	ic10.OpAcos:   true,
	ic10.OpAtan2:  true,
	ic10.OpBrnan:  true,
	ic10.OpBnan:   true,
	ic10.OpSnan:   true,
	ic10.OpSnanz:  true,
	ic10.OpLbs:    true,
	ic10.OpLbn:    true,
	ic10.OpSbn:    true,
	ic10.OpLbns:   true,
	ic10.OpSs:     true,
	ic10.OpSbs:    true,
	ic10.OpSrl:    true,
	ic10.OpSra:    true,
	ic10.OpSll:    true,
	ic10.OpSla:    true,
	ic10.OpNot:    true,
	ic10.OpLd:     true,
	ic10.OpSd:     true,
	ic10.OpPoke:   true,
	ic10.OpGetd:   true,
	ic10.OpPutd:   true,
	ic10.OpGet:    true,
	ic10.OpPut:    true,
	ic10.OpClr:    true,
	ic10.OpClrd:   true,
	ic10.OpRmap:   true,
	ic10.OpBdnvl:  true,
	ic10.OpBdnvs:  true,
	ic10.OpPow:    true,
	ic10.OpExt:    true,
	ic10.OpIns:    true,
	ic10.OpLerp:   true,
	ic10.OpSgn:    true,
	ic10.OpClamp:  true,
	ic10.OpRol:    true,
	ic10.OpRor:    true,
}

// fuzzCovered lists the mnemonics the differential corpus in internal/difftest
// reaches. It mirrors difftest.GeneratedMnemonics rather than restating it from
// memory, and TestFuzzCoverageMatchesTheGenerators compares the two in both
// directions.
var fuzzCovered = map[ic10.Opcode]bool{
	ic10.OpAbs:    true,
	ic10.OpAdd:    true,
	ic10.OpAlias:  true,
	ic10.OpAnd:    true,
	ic10.OpBdns:   true,
	ic10.OpBdnsal: true,
	ic10.OpBdse:   true,
	ic10.OpBdseal: true,
	ic10.OpBeq:    true,
	ic10.OpBeqal:  true,
	ic10.OpBeqz:   true,
	ic10.OpBeqzal: true,
	ic10.OpBge:    true,
	ic10.OpBgeal:  true,
	ic10.OpBgez:   true,
	ic10.OpBgezal: true,
	ic10.OpBgt:    true,
	ic10.OpBgtal:  true,
	ic10.OpBgtz:   true,
	ic10.OpBgtzal: true,
	ic10.OpBle:    true,
	ic10.OpBleal:  true,
	ic10.OpBlez:   true,
	ic10.OpBlezal: true,
	ic10.OpBlt:    true,
	ic10.OpBltal:  true,
	ic10.OpBltz:   true,
	ic10.OpBltzal: true,
	ic10.OpBnan:   true,
	ic10.OpBne:    true,
	ic10.OpBneal:  true,
	ic10.OpBnez:   true,
	ic10.OpBnezal: true,
	ic10.OpCeil:   true,
	ic10.OpClr:    true,
	ic10.OpDefine: true,
	ic10.OpDiv:    true,
	ic10.OpFloor:  true,
	ic10.OpGet:    true,
	ic10.OpJ:      true,
	ic10.OpJal:    true,
	ic10.OpL:      true,
	ic10.OpMod:    true,
	ic10.OpMove:   true,
	ic10.OpMul:    true,
	ic10.OpNor:    true,
	ic10.OpNot:    true,
	ic10.OpOr:     true,
	ic10.OpPeek:   true,
	ic10.OpPoke:   true,
	ic10.OpPop:    true,
	ic10.OpPush:   true,
	ic10.OpPut:    true,
	ic10.OpS:      true,
	ic10.OpSdns:   true,
	ic10.OpSdse:   true,
	ic10.OpSelect: true,
	ic10.OpSeq:    true,
	ic10.OpSeqz:   true,
	ic10.OpSge:    true,
	ic10.OpSgez:   true,
	ic10.OpSgt:    true,
	ic10.OpSgtz:   true,
	ic10.OpSle:    true,
	ic10.OpSlez:   true,
	ic10.OpSll:    true,
	ic10.OpSlt:    true,
	ic10.OpSltz:   true,
	ic10.OpSnan:   true,
	ic10.OpSnanz:  true,
	ic10.OpSne:    true,
	ic10.OpSnez:   true,
	ic10.OpSqrt:   true,
	ic10.OpSra:    true,
	ic10.OpSrl:    true,
	ic10.OpSub:    true,
	ic10.OpTrunc:  true,
	ic10.OpXor:    true,
	ic10.OpYield:  true,
}

// fuzzExcluded lists the mnemonics the differential generators never emit on
// purpose. It mirrors difftest.ExcludedMnemonics; the reason each one is out
// stays there, because a second copy of the prose is what would drift.
var fuzzExcluded = map[ic10.Opcode]bool{
	ic10.OpAcos:   true,
	ic10.OpAsin:   true,
	ic10.OpAtan:   true,
	ic10.OpAtan2:  true,
	ic10.OpBap:    true,
	ic10.OpBapal:  true,
	ic10.OpBapz:   true,
	ic10.OpBapzal: true,
	ic10.OpBdnvl:  true,
	ic10.OpBdnvs:  true,
	ic10.OpBna:    true,
	ic10.OpBnaal:  true,
	ic10.OpBnaz:   true,
	ic10.OpBnazal: true,
	ic10.OpBrap:   true,
	ic10.OpBrapz:  true,
	ic10.OpBrdns:  true,
	ic10.OpBrdse:  true,
	ic10.OpBreq:   true,
	ic10.OpBreqz:  true,
	ic10.OpBrge:   true,
	ic10.OpBrgez:  true,
	ic10.OpBrgt:   true,
	ic10.OpBrgtz:  true,
	ic10.OpBrle:   true,
	ic10.OpBrlez:  true,
	ic10.OpBrlt:   true,
	ic10.OpBrltz:  true,
	ic10.OpBrna:   true,
	ic10.OpBrnan:  true,
	ic10.OpBrnaz:  true,
	ic10.OpBrne:   true,
	ic10.OpBrnez:  true,
	ic10.OpClamp:  true,
	ic10.OpClrd:   true,
	ic10.OpCos:    true,
	ic10.OpExp:    true,
	ic10.OpExt:    true,
	ic10.OpGetd:   true,
	ic10.OpHcf:    true,
	ic10.OpIns:    true,
	ic10.OpJr:     true,
	ic10.OpLabel:  true,
	ic10.OpLb:     true,
	ic10.OpLbn:    true,
	ic10.OpLbns:   true,
	ic10.OpLbs:    true,
	ic10.OpLd:     true,
	ic10.OpLerp:   true,
	ic10.OpLog:    true,
	ic10.OpLr:     true,
	ic10.OpLs:     true,
	ic10.OpMax:    true,
	ic10.OpMin:    true,
	ic10.OpPow:    true,
	ic10.OpPutd:   true,
	ic10.OpRand:   true,
	ic10.OpRmap:   true,
	ic10.OpRol:    true,
	ic10.OpRor:    true,
	ic10.OpRound:  true,
	ic10.OpSap:    true,
	ic10.OpSapz:   true,
	ic10.OpSb:     true,
	ic10.OpSbn:    true,
	ic10.OpSbs:    true,
	ic10.OpSd:     true,
	ic10.OpSgn:    true,
	ic10.OpSin:    true,
	ic10.OpSla:    true,
	ic10.OpSleep:  true,
	ic10.OpSna:    true,
	ic10.OpSnaz:   true,
	ic10.OpSs:     true,
	ic10.OpTan:    true,
}

// SupportFor reports what is provided for one opcode, and false for an opcode
// outside the instruction set.
func SupportFor(op ic10.Opcode) (Support, bool) {
	instruction, ok := op.Instruction()
	if !ok {
		return Support{}, false
	}
	spec, implemented := instructions[op]
	return Support{
		Mnemonic:        instruction.Mnemonic,
		Opcode:          op,
		Class:           spec.class,
		Implemented:     implemented,
		ParseImpossible: parseImpossible[op],
		Unit:            unitTested[op],
		Fuzz:            fuzzCovered[op],
		FuzzExcluded:    fuzzExcluded[op],
	}, true
}

// Coverage reports the support record for every mnemonic in the instruction
// set, in mnemonic order, so a caller can print or assert over the whole table.
func Coverage() []Support {
	records := make([]Support, 0, len(ic10.Instructions))
	for _, instruction := range ic10.Instructions {
		record, ok := SupportFor(instruction.Opcode)
		if !ok {
			continue
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Mnemonic < records[j].Mnemonic })
	return records
}
