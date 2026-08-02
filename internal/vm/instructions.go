package vm

import (
	"math"

	"github.com/greg2010/ic11c/internal/ic10"
)

// Branch encodings, named so the table below reads as the game's own switch.
var (
	absolute = branchForm{}
	relative = branchForm{relative: true}
	linked   = branchForm{link: true}
)

// instructions is the game's compile switch, one row per ScriptCommand member.
//
// tokens is the count _LineOfCode compares against, counting the mnemonic, and
// class names the C# operation the builder is transliterated from. Four rows
// have a tokens value their builder disagrees with; that is not a mistake here,
// it is the defect that makes those four impossible to assemble.
var instructions = map[ic10.Opcode]instructionSpec{
	ic10.OpL:    {class: "_L_Operation", tokens: 4, build: buildLoadLogic},
	ic10.OpLd:   {class: "_LD_Operation", tokens: 4, build: buildLoadLogicByID},
	ic10.OpS:    {class: "_S_Operation", tokens: 4, build: buildStoreLogic},
	ic10.OpSd:   {class: "_SD_Operation", tokens: 4, build: buildStoreLogicByID},
	ic10.OpLs:   {class: "_LS_Operation", tokens: 5, build: buildLoadSlot},
	ic10.OpSs:   {class: "_SS_Operation", tokens: 5, build: buildStoreSlot},
	ic10.OpLr:   {class: "_LR_Operation", tokens: 5, build: buildReagentLoad},
	ic10.OpRmap: {class: "_RMAP_Operation", tokens: 4, build: buildReagentMap},

	ic10.OpLb:   {class: "_LB_Operation", tokens: 5, build: buildBatchLoad(false, false)},
	ic10.OpLbn:  {class: "_LBN_Operation", tokens: 6, build: buildBatchLoad(true, false)},
	ic10.OpLbs:  {class: "_LBS_Operation", tokens: 6, build: buildBatchLoad(false, true)},
	ic10.OpLbns: {class: "_LBNS_Operation", tokens: 7, build: buildBatchLoad(true, true)},
	ic10.OpSb:   {class: "_SB_Operation", tokens: 4, build: buildBatchStore(false, false)},
	ic10.OpSbn:  {class: "_SBN_Operation", tokens: 5, build: buildBatchStore(true, false)},
	ic10.OpSbs:  {class: "_SBS_Operation", tokens: 5, build: buildBatchStore(false, true)},

	ic10.OpSdse: {class: "_SDSE_Operation", tokens: 3, build: deviceSet(true)},
	ic10.OpSdns: {class: "_SDNS_Operation", tokens: 3, build: deviceSet(false)},

	ic10.OpAlias:  {class: "_ALIAS_Operation", tokens: 3, build: buildAlias},
	ic10.OpLabel:  {class: "_LABEL_Operation", tokens: 3, build: buildLabel},
	ic10.OpDefine: {class: "_DEFINE_Operation", tokens: 3, build: buildDefine},

	ic10.OpMove: {class: "_MOVE_Operation", tokens: 3, build: storeValue(unary(func(a float64) float64 { return a }), at(2))},
	ic10.OpAdd:  {class: "_ADD_Operation", tokens: 4, build: storeValue(binary(func(a, b float64) float64 { return a + b }), at(2), at(3))},
	ic10.OpSub:  {class: "_SUB_Operation", tokens: 4, build: storeValue(binary(func(a, b float64) float64 { return a - b }), at(2), at(3))},
	ic10.OpMul:  {class: "_MUL_Operation", tokens: 4, build: storeValue(binary(func(a, b float64) float64 { return a * b }), at(2), at(3))},
	ic10.OpDiv:  {class: "_DIV_Operation", tokens: 4, build: storeValue(binary(func(a, b float64) float64 { return a / b }), at(2), at(3))},
	ic10.OpMod:  {class: "_MOD_Operation", tokens: 4, build: storeValue(binary(mod), at(2), at(3))},

	ic10.OpSqrt:  {class: "_SQRT_Operation", tokens: 3, build: storeValue(unary(math.Sqrt), at(2))},
	ic10.OpRound: {class: "_ROUND_Operation", tokens: 3, build: storeValue(unary(math.RoundToEven), at(2))},
	ic10.OpTrunc: {class: "_TRUNC_Operation", tokens: 3, build: storeValue(unary(math.Trunc), at(2))},
	ic10.OpCeil:  {class: "_CEIL_Operation", tokens: 3, build: storeValue(unary(math.Ceil), at(2))},
	ic10.OpFloor: {class: "_FLOOR_Operation", tokens: 3, build: storeValue(unary(math.Floor), at(2))},
	ic10.OpAbs:   {class: "_ABS_Operation", tokens: 3, build: storeValue(unary(math.Abs), at(2))},
	ic10.OpLog:   {class: "_LOG_Operation", tokens: 3, build: storeValue(unary(math.Log), at(2))},
	ic10.OpExp:   {class: "_EXP_Operation", tokens: 3, build: storeValue(unary(math.Exp), at(2))},
	ic10.OpSgn:   {class: "_SGN_Operation", tokens: 3, build: storeValue(unary(sign), at(2))},
	ic10.OpSin:   {class: "_SIN_Operation", tokens: 3, build: storeValue(unary(math.Sin), at(2))},
	ic10.OpAsin:  {class: "_ASIN_Operation", tokens: 3, build: storeValue(unary(math.Asin), at(2))},
	ic10.OpCos:   {class: "_COS_Operation", tokens: 3, build: storeValue(unary(math.Cos), at(2))},
	ic10.OpAcos:  {class: "_ACOS_Operation", tokens: 3, build: storeValue(unary(math.Acos), at(2))},
	ic10.OpTan:   {class: "_TAN_Operation", tokens: 3, build: storeValue(unary(math.Tan), at(2))},
	ic10.OpAtan:  {class: "_ATAN_Operation", tokens: 3, build: storeValue(unary(math.Atan), at(2))},
	ic10.OpAtan2: {class: "_ATAN2_Operation", tokens: 4, build: storeValue(binary(math.Atan2), at(2), at(3))},
	ic10.OpMax:   {class: "_MAX_Operation", tokens: 4, build: storeValue(binary(math.Max), at(2), at(3))},
	ic10.OpMin:   {class: "_MIN_Operation", tokens: 4, build: storeValue(binary(math.Min), at(2), at(3))},
	ic10.OpPow:   {class: "_POW_Operation", tokens: 4, build: storeValue(binary(math.Pow), at(2), at(3))},
	ic10.OpLerp:  {class: "_LERP_Operation", tokens: 5, build: storeValue(lerp, at(2), at(3), at(4))},
	ic10.OpClamp: {class: "_CLAMP_Operation", tokens: 5, build: storeValue(func(a, b, c float64) float64 { return math.Min(math.Max(a, b), c) }, at(2), at(3), at(4))},
	ic10.OpSelect: {class: "_SELECT_Operation", tokens: 5, build: storeValue(func(a, b, c float64) float64 {
		if a != 0 {
			return b
		}
		return c
	}, at(2), at(3), at(4))},

	ic10.OpSlt:   {class: "_SLT_Operation", tokens: 4, build: storeTest(lessThan, at(2), at(3))},
	ic10.OpSgt:   {class: "_SGT_Operation", tokens: 4, build: storeTest(greaterThan, at(2), at(3))},
	ic10.OpSle:   {class: "_SLE_Operation", tokens: 4, build: storeTest(lessOrEqual, at(2), at(3))},
	ic10.OpSge:   {class: "_SGE_Operation", tokens: 4, build: storeTest(greaterOrEqual, at(2), at(3))},
	ic10.OpSeq:   {class: "_SEQ_Operation", tokens: 4, build: storeTest(equal, at(2), at(3))},
	ic10.OpSne:   {class: "_SNE_Operation", tokens: 4, build: storeTest(notEqual, at(2), at(3))},
	ic10.OpSap:   {class: "_SAP_Operation", tokens: 5, build: storeTest(approximate, at(2), at(3), at(4))},
	ic10.OpSna:   {class: "_SNA_Operation", tokens: 5, build: storeTest(notApproximate, at(2), at(3), at(4))},
	ic10.OpSltz:  {class: "_SLTZ_Operation", tokens: 3, build: storeTest(lessThan, at(2), zero)},
	ic10.OpSgtz:  {class: "_SGTZ_Operation", tokens: 3, build: storeTest(greaterThan, at(2), zero)},
	ic10.OpSlez:  {class: "_SLEZ_Operation", tokens: 3, build: storeTest(lessOrEqual, at(2), zero)},
	ic10.OpSgez:  {class: "_SGEZ_Operation", tokens: 3, build: storeTest(greaterOrEqual, at(2), zero)},
	ic10.OpSeqz:  {class: "_SEQZ_Operation", tokens: 3, build: storeTest(equal, at(2), zero)},
	ic10.OpSnez:  {class: "_SNEZ_Operation", tokens: 3, build: storeTest(notEqual, at(2), zero)},
	ic10.OpSapz:  {class: "_SAPZ_Operation", tokens: 4, build: storeTest(approximate, at(2), zero, at(3))},
	ic10.OpSnaz:  {class: "_SNAZ_Operation", tokens: 4, build: storeTest(notApproximate, at(2), zero, at(3))},
	ic10.OpSnan:  {class: "_SNAN_Operation", tokens: 3, build: storeValue(unary(isNaNValue), at(2))},
	ic10.OpSnanz: {class: "_SNANZ_Operation", tokens: 3, build: storeValue(unary(isNotNaNValue), at(2))},

	ic10.OpAnd: {class: "_AND_Operation", tokens: 4, build: storeLong(func(a, b int64) int64 { return a & b }, at(2), at(3))},
	ic10.OpOr:  {class: "_OR_Operation", tokens: 4, build: storeLong(func(a, b int64) int64 { return a | b }, at(2), at(3))},
	ic10.OpXor: {class: "_XOR_Operation", tokens: 4, build: storeLong(func(a, b int64) int64 { return a ^ b }, at(2), at(3))},
	ic10.OpNor: {class: "_NOR_Operation", tokens: 4, build: storeLong(func(a, b int64) int64 { return ^(a | b) }, at(2), at(3))},
	ic10.OpNot: {class: "_NOT_Operation", tokens: 3, build: storeLong(func(a, _ int64) int64 { return ^a }, at(2))},

	ic10.OpSrl: {class: "_SRL_Operation", tokens: 4, build: shift(false, func(v int64, d int) int64 { return v >> shiftCount(d) })},
	ic10.OpSra: {class: "_SRA_Operation", tokens: 4, build: shift(true, func(v int64, d int) int64 { return v >> shiftCount(d) })},
	ic10.OpSll: {class: "_SLA_SLL_Operation", tokens: 4, build: shift(true, func(v int64, d int) int64 { return v << shiftCount(d) })},
	ic10.OpSla: {class: "_SLA_SLL_Operation", tokens: 4, build: shift(true, func(v int64, d int) int64 { return v << shiftCount(d) })},
	ic10.OpRol: {class: "_ROL_Operation", tokens: 4, build: shift(false, func(v int64, d int) int64 { return rotate(v, d, true) })},
	ic10.OpRor: {class: "_ROR_Operation", tokens: 4, build: shift(false, func(v int64, d int) int64 { return rotate(v, d, false) })},
	ic10.OpExt: {class: "_EXT_Operation", tokens: 5, build: buildExt},
	ic10.OpIns: {class: "_INS_Operation", tokens: 5, build: buildIns},

	ic10.OpJ:   {class: "_J_Operation", tokens: 2, build: jump(absolute)},
	ic10.OpJr:  {class: "_JR_Operation", tokens: 2, build: jump(relative)},
	ic10.OpJal: {class: "_JAL_Operation", tokens: 2, build: jump(linked)},

	ic10.OpBlt:   {class: "_BLT_Operation", tokens: 4, build: compare(absolute, lessThan, at(3), at(1), at(2))},
	ic10.OpBgt:   {class: "_BGT_Operation", tokens: 4, build: compare(absolute, greaterThan, at(3), at(1), at(2))},
	ic10.OpBle:   {class: "_BLE_Operation", tokens: 4, build: compare(absolute, lessOrEqual, at(3), at(1), at(2))},
	ic10.OpBge:   {class: "_BGE_Operation", tokens: 4, build: compare(absolute, greaterOrEqual, at(3), at(1), at(2))},
	ic10.OpBeq:   {class: "_BEQ_Operation", tokens: 4, build: compare(absolute, equal, at(3), at(1), at(2))},
	ic10.OpBne:   {class: "_BNE_Operation", tokens: 4, build: compare(absolute, notEqual, at(3), at(1), at(2))},
	ic10.OpBap:   {class: "_BAP_Operation", tokens: 5, build: compare(absolute, approximate, at(4), at(1), at(2), at(3))},
	ic10.OpBna:   {class: "_BNA_Operation", tokens: 5, build: compare(absolute, notApproximate, at(4), at(1), at(2), at(3))},
	ic10.OpBltz:  {class: "_BLTZ_Operation", tokens: 3, build: compare(absolute, lessThan, at(2), at(1), zero)},
	ic10.OpBgtz:  {class: "_BGTZ_Operation", tokens: 3, build: compare(absolute, greaterThan, at(2), at(1), zero)},
	ic10.OpBlez:  {class: "_BLEZ_Operation", tokens: 3, build: compare(absolute, lessOrEqual, at(2), at(1), zero)},
	ic10.OpBgez:  {class: "_BGEZ_Operation", tokens: 3, build: compare(absolute, greaterOrEqual, at(2), at(1), zero)},
	ic10.OpBeqz:  {class: "_BEQZ_Operation", tokens: 3, build: compare(absolute, equal, at(2), at(1), zero)},
	ic10.OpBnez:  {class: "_BNEZ_Operation", tokens: 3, build: compare(absolute, notEqual, at(2), at(1), zero)},
	ic10.OpBapz:  {class: "_BAPZ_Operation", tokens: 4, build: compare(absolute, approximate, at(3), at(1), zero, at(2))},
	ic10.OpBnaz:  {class: "_BNAZ_Operation", tokens: 4, build: compare(absolute, notApproximate, at(3), at(1), zero, at(2))},
	ic10.OpBnan:  {class: "_BNAN_Operation", tokens: 3, build: compare(absolute, isNaN, at(2), at(1))},
	ic10.OpBdse:  {class: "_BDSE_Operation", tokens: 3, build: deviceBranchBuilder(absolute, true)},
	ic10.OpBdns:  {class: "_BDNS_Operation", tokens: 3, build: deviceBranchBuilder(absolute, false)},
	ic10.OpBdnvl: {class: "_BDNVL_Operation", tokens: 4, build: logicBranchBuilder(false)},
	ic10.OpBdnvs: {class: "_BDNVS_Operation", tokens: 4, build: logicBranchBuilder(true)},

	ic10.OpBrlt:  {class: "_BRLT_Operation", tokens: 4, build: compare(relative, lessThan, at(3), at(1), at(2))},
	ic10.OpBrgt:  {class: "_BRGT_Operation", tokens: 4, build: compare(relative, greaterThan, at(3), at(1), at(2))},
	ic10.OpBrle:  {class: "_BRLE_Operation", tokens: 4, build: compare(relative, lessOrEqual, at(3), at(1), at(2))},
	ic10.OpBrge:  {class: "_BRGE_Operation", tokens: 4, build: compare(relative, greaterOrEqual, at(3), at(1), at(2))},
	ic10.OpBreq:  {class: "_BREQ_Operation", tokens: 4, build: compare(relative, equal, at(3), at(1), at(2))},
	ic10.OpBrne:  {class: "_BRNE_Operation", tokens: 4, build: compare(relative, notEqual, at(3), at(1), at(2))},
	ic10.OpBrap:  {class: "_BRAP_Operation", tokens: 5, build: compare(relative, approximate, at(4), at(1), at(2), at(3))},
	ic10.OpBrna:  {class: "_BRNA_Operation", tokens: 5, build: compare(relative, notApproximate, at(4), at(1), at(2), at(3))},
	ic10.OpBrltz: {class: "_BRLTZ_Operation", tokens: 3, build: compare(relative, lessThan, at(2), at(1), zero)},
	ic10.OpBrgtz: {class: "_BRGTZ_Operation", tokens: 3, build: compare(relative, greaterThan, at(2), at(1), zero)},
	ic10.OpBrlez: {class: "_BRLEZ_Operation", tokens: 3, build: compare(relative, lessOrEqual, at(2), at(1), zero)},
	ic10.OpBrgez: {class: "_BRGEZ_Operation", tokens: 3, build: compare(relative, greaterOrEqual, at(2), at(1), zero)},
	ic10.OpBreqz: {class: "_BREQZ_Operation", tokens: 3, build: compare(relative, equal, at(2), at(1), zero)},
	ic10.OpBrnez: {class: "_BRNEZ_Operation", tokens: 3, build: compare(relative, notEqual, at(2), at(1), zero)},
	ic10.OpBrnan: {class: "_BRNAN_Operation", tokens: 3, build: compare(relative, isNaN, at(2), at(1))},
	ic10.OpBrdse: {class: "_BRDSE_Operation", tokens: 3, build: deviceBranchBuilder(relative, true)},
	ic10.OpBrdns: {class: "_BRDNS_Operation", tokens: 3, build: deviceBranchBuilder(relative, false)},

	// Uncompilable. The token count is checked against 3 and the target is then
	// read from position 3, so three operands raise an arity error and two run
	// off the end of the line. No operand count assembles.
	ic10.OpBrapz: {class: "_BRAPZ_Operation", tokens: 3, build: compare(relative, approximate, at(3), at(1), zero, at(2))},
	ic10.OpBrnaz: {class: "_BRNAZ_Operation", tokens: 3, build: compare(relative, notApproximate, at(3), at(1), zero, at(2))},

	ic10.OpBltal:  {class: "_BLTAL_Operation", tokens: 4, build: compare(linked, lessThan, at(3), at(1), at(2))},
	ic10.OpBgtal:  {class: "_BGTAL_Operation", tokens: 4, build: compare(linked, greaterThan, at(3), at(1), at(2))},
	ic10.OpBleal:  {class: "_BLEAL_Operation", tokens: 4, build: compare(linked, lessOrEqual, at(3), at(1), at(2))},
	ic10.OpBgeal:  {class: "_BGEAL_Operation", tokens: 4, build: compare(linked, greaterOrEqual, at(3), at(1), at(2))},
	ic10.OpBeqal:  {class: "_BEQAL_Operation", tokens: 4, build: compare(linked, equal, at(3), at(1), at(2))},
	ic10.OpBneal:  {class: "_BNEAL_Operation", tokens: 4, build: compare(linked, notEqual, at(3), at(1), at(2))},
	ic10.OpBapal:  {class: "_BAPAL_Operation", tokens: 5, build: compare(linked, approximate, at(4), at(1), at(2), at(3))},
	ic10.OpBnaal:  {class: "_BNAAL_Operation", tokens: 5, build: compare(linked, notApproximate, at(4), at(1), at(2), at(3))},
	ic10.OpBltzal: {class: "_BLTZAL_Operation", tokens: 3, build: compare(linked, lessThan, at(2), at(1), zero)},
	ic10.OpBgtzal: {class: "_BGTZAL_Operation", tokens: 3, build: compare(linked, greaterThan, at(2), at(1), zero)},
	ic10.OpBlezal: {class: "_BLEZAL_Operation", tokens: 3, build: compare(linked, lessOrEqual, at(2), at(1), zero)},
	ic10.OpBgezal: {class: "_BGEZAL_Operation", tokens: 3, build: compare(linked, greaterOrEqual, at(2), at(1), zero)},
	ic10.OpBeqzal: {class: "_BEQZAL_Operation", tokens: 3, build: compare(linked, equal, at(2), at(1), zero)},
	ic10.OpBnezal: {class: "_BNEZAL_Operation", tokens: 3, build: compare(linked, notEqual, at(2), at(1), zero)},
	ic10.OpBdseal: {class: "_BDSEAL_Operation", tokens: 3, build: deviceBranchBuilder(linked, true)},
	ic10.OpBdnsal: {class: "_BDNSAL_Operation", tokens: 3, build: deviceBranchBuilder(linked, false)},

	// Uncompilable, for the same reason as brapz and brnaz.
	ic10.OpBapzal: {class: "_BAPZAL_Operation", tokens: 3, build: compare(linked, approximate, at(3), at(1), zero, at(2))},
	ic10.OpBnazal: {class: "_BNAZAL_Operation", tokens: 3, build: compare(linked, notApproximate, at(3), at(1), zero, at(2))},

	ic10.OpPeek: {class: "_PEEK_Operation", tokens: 2, build: buildPeek},
	ic10.OpPop:  {class: "_POP_Operation", tokens: 2, build: buildPop},
	ic10.OpPush: {class: "_PUSH_Operation", tokens: 2, build: buildPush},
	ic10.OpPoke: {class: "_POKE_Operation", tokens: 3, build: buildPoke},
	ic10.OpGet:  {class: "_GET_Operation", tokens: 4, build: buildGet},
	ic10.OpGetd: {class: "_GETD_Operation", tokens: 4, build: buildGetByID},
	ic10.OpPut:  {class: "_PUT_Operation", tokens: 4, build: buildPut},
	ic10.OpPutd: {class: "_PUTD_Operation", tokens: 4, build: buildPutByID},
	ic10.OpClr:  {class: "_CLR_Operation", tokens: 2, build: buildClear},
	ic10.OpClrd: {class: "_CLRD_Operation", tokens: 2, build: buildClearByID},

	ic10.OpRand:  {class: "_RAND_Operation", tokens: 2, build: buildRand},
	ic10.OpYield: {class: "_YIELD_Operation", tokens: 1, build: buildYield},
	ic10.OpSleep: {class: "_SLEEP_Operation", tokens: 2, build: buildSleep},
	ic10.OpHcf:   {class: "_HCF_Operation", tokens: 1, build: buildHcf},
}

// Builders that need more than a shared shape.

func buildLoadLogic(m *Machine, line int, args []string) (operation, error) {
	store, err := storeOperand(m, line, args[1])
	if err != nil {
		return nil, err
	}
	device, err := newDeviceOperand(m, line, args[2])
	if err != nil {
		return nil, err
	}
	logicType, err := logicTypeOperand(m, line, args[3])
	if err != nil {
		return nil, err
	}
	return &loadLogicOperation{m: m, store: store, device: device, logicType: logicType, line: line}, nil
}

func buildLoadLogicByID(m *Machine, line int, args []string) (operation, error) {
	store, err := storeOperand(m, line, args[1])
	if err != nil {
		return nil, err
	}
	id, err := intOperand(m, line, args[2])
	if err != nil {
		return nil, err
	}
	logicType, err := logicTypeOperand(m, line, args[3])
	if err != nil {
		return nil, err
	}
	return &loadLogicOperation{m: m, store: store, deviceID: id, byID: true, logicType: logicType, line: line}, nil
}

func buildStoreLogic(m *Machine, line int, args []string) (operation, error) {
	device, err := newDeviceOperand(m, line, args[1])
	if err != nil {
		return nil, err
	}
	logicType, err := logicTypeOperand(m, line, args[2])
	if err != nil {
		return nil, err
	}
	value, err := valueOperand(m, line, args[3])
	if err != nil {
		return nil, err
	}
	return &storeLogicOperation{m: m, device: device, logicType: logicType, value: value, line: line, checkNone: true}, nil
}

func buildStoreLogicByID(m *Machine, line int, args []string) (operation, error) {
	id, err := intOperand(m, line, args[1])
	if err != nil {
		return nil, err
	}
	logicType, err := logicTypeOperand(m, line, args[2])
	if err != nil {
		return nil, err
	}
	value, err := valueOperand(m, line, args[3])
	if err != nil {
		return nil, err
	}
	return &storeLogicOperation{m: m, deviceID: id, byID: true, logicType: logicType, value: value, line: line}, nil
}

func buildLoadSlot(m *Machine, line int, args []string) (operation, error) {
	store, err := storeOperand(m, line, args[1])
	if err != nil {
		return nil, err
	}
	device, err := newDeviceOperand(m, line, args[2])
	if err != nil {
		return nil, err
	}
	slot, err := intOperand(m, line, args[3])
	if err != nil {
		return nil, err
	}
	slotType, err := slotTypeOperand(m, line, args[4])
	if err != nil {
		return nil, err
	}
	return &loadSlotOperation{m: m, store: store, device: device, slot: slot, slotType: slotType, line: line}, nil
}

func buildStoreSlot(m *Machine, line int, args []string) (operation, error) {
	device, err := newDeviceOperand(m, line, args[1])
	if err != nil {
		return nil, err
	}
	slot, err := intOperand(m, line, args[2])
	if err != nil {
		return nil, err
	}
	slotType, err := slotTypeOperand(m, line, args[3])
	if err != nil {
		return nil, err
	}
	value, err := valueOperand(m, line, args[4])
	if err != nil {
		return nil, err
	}
	return &storeSlotOperation{m: m, device: device, slot: slot, slotType: slotType, value: value, line: line}, nil
}

func buildReagentLoad(m *Machine, line int, args []string) (operation, error) {
	store, err := storeOperand(m, line, args[1])
	if err != nil {
		return nil, err
	}
	device, err := newDeviceOperand(m, line, args[2])
	if err != nil {
		return nil, err
	}
	mode, err := reagentModeOperand(m, line, args[3])
	if err != nil {
		return nil, err
	}
	reagent, err := intOperand(m, line, args[4])
	if err != nil {
		return nil, err
	}
	return &reagentLoadOperation{m: m, store: store, device: device, mode: mode, reagent: reagent, line: line, mnemonic: args[0]}, nil
}

func buildReagentMap(m *Machine, line int, args []string) (operation, error) {
	store, err := storeOperand(m, line, args[1])
	if err != nil {
		return nil, err
	}
	device, err := newDeviceIndexVariable(m, line, args[2], maskDeviceIndex)
	if err != nil {
		return nil, err
	}
	value, err := valueOperand(m, line, args[3])
	if err != nil {
		return nil, err
	}
	return &reagentMapOperation{m: m, store: store, device: *device, value: value, line: line}, nil
}

// buildBatchLoad covers lb, lbn, lbs and lbns, whose operand order differs only
// in which of the name hash and slot index positions are present.
func buildBatchLoad(byName, bySlot bool) buildFunc {
	return func(m *Machine, line int, args []string) (operation, error) {
		op := &batchLoadOperation{m: m, byName: byName, bySlot: bySlot, line: line}
		var err error
		if op.store, err = storeOperand(m, line, args[1]); err != nil {
			return nil, err
		}
		next := 2
		if op.prefabHash, err = intOperand(m, line, args[next]); err != nil {
			return nil, err
		}
		next++
		if byName {
			if op.nameHash, err = intOperand(m, line, args[next]); err != nil {
				return nil, err
			}
			next++
		}
		if bySlot {
			if op.slot, err = intOperand(m, line, args[next]); err != nil {
				return nil, err
			}
			next++
			if op.slotType, err = slotTypeOperand(m, line, args[next]); err != nil {
				return nil, err
			}
		} else if op.logicType, err = logicTypeOperand(m, line, args[next]); err != nil {
			return nil, err
		}
		next++
		if op.batchMode, err = batchModeOperand(m, line, args[next]); err != nil {
			return nil, err
		}
		return op, nil
	}
}

// buildBatchStore covers sb, sbn and sbs. The value is always the last operand,
// and the slot form takes its slot index before its slot type.
func buildBatchStore(byName, bySlot bool) buildFunc {
	return func(m *Machine, line int, args []string) (operation, error) {
		op := &batchStoreOperation{m: m, byName: byName, bySlot: bySlot, line: line}
		var err error
		next := 1
		if op.prefabHash, err = intOperand(m, line, args[next]); err != nil {
			return nil, err
		}
		next++
		if byName {
			if op.nameHash, err = intOperand(m, line, args[next]); err != nil {
				return nil, err
			}
			next++
		}
		if bySlot {
			if op.slot, err = intOperand(m, line, args[next]); err != nil {
				return nil, err
			}
			next++
			if op.slotType, err = slotTypeOperand(m, line, args[next]); err != nil {
				return nil, err
			}
		} else if op.logicType, err = logicTypeOperand(m, line, args[next]); err != nil {
			return nil, err
		}
		next++
		if op.value, err = valueOperand(m, line, args[next]); err != nil {
			return nil, err
		}
		return op, nil
	}
}

func buildAlias(m *Machine, line int, args []string) (operation, error) {
	return newAliasOperation(m, line, args[1], args[2])
}

// buildLabel is _LABEL_Operation, which is _ALIAS_Operation with its operands
// the other way round. It is deprecated and still works.
func buildLabel(m *Machine, line int, args []string) (operation, error) {
	return newAliasOperation(m, line, args[2], args[1])
}

// buildDefine registers the value while compiling, which is why a define is
// visible to lines above it and an alias is not.
func buildDefine(m *Machine, line int, args []string) (operation, error) {
	value, err := newDoubleValueVariable(m, line, args[2], maskDefineValue)
	if err != nil {
		return nil, err
	}
	if _, exists := m.defines[args[1]]; exists {
		return nil, newFault(ExcExtraDefine, line)
	}
	m.defines[args[1]] = value.literal()
	return defineOperation{}, nil
}

func buildExt(m *Machine, line int, args []string) (operation, error) {
	store, err := storeOperand(m, line, args[1])
	if err != nil {
		return nil, err
	}
	source, err := valueOperand(m, line, args[2])
	if err != nil {
		return nil, err
	}
	offset, err := valueOperand(m, line, args[3])
	if err != nil {
		return nil, err
	}
	width, err := valueOperand(m, line, args[4])
	if err != nil {
		return nil, err
	}
	return &extOp{m: m, store: store, source: source, offset: offset, width: width, line: line}, nil
}

func buildIns(m *Machine, line int, args []string) (operation, error) {
	store, err := storeOperand(m, line, args[1])
	if err != nil {
		return nil, err
	}
	source, err := valueOperand(m, line, args[2])
	if err != nil {
		return nil, err
	}
	offset, err := valueOperand(m, line, args[3])
	if err != nil {
		return nil, err
	}
	width, err := valueOperand(m, line, args[4])
	if err != nil {
		return nil, err
	}
	return &insOp{m: m, store: store, source: source, offset: offset, width: width, line: line}, nil
}

func buildPeek(m *Machine, line int, args []string) (operation, error) {
	store, err := storeOperand(m, line, args[1])
	if err != nil {
		return nil, err
	}
	return &peekOperation{m: m, store: store, line: line}, nil
}

func buildPop(m *Machine, line int, args []string) (operation, error) {
	store, err := storeOperand(m, line, args[1])
	if err != nil {
		return nil, err
	}
	return &popOperation{m: m, store: store, line: line}, nil
}

func buildPush(m *Machine, line int, args []string) (operation, error) {
	value, err := valueOperand(m, line, args[1])
	if err != nil {
		return nil, err
	}
	return &pushOperation{m: m, value: value, line: line}, nil
}

func buildPoke(m *Machine, line int, args []string) (operation, error) {
	address, err := intOperand(m, line, args[1])
	if err != nil {
		return nil, err
	}
	value, err := valueOperand(m, line, args[2])
	if err != nil {
		return nil, err
	}
	return &pokeOperation{m: m, address: address, value: value, line: line}, nil
}

func buildGet(m *Machine, line int, args []string) (operation, error) {
	store, err := storeOperand(m, line, args[1])
	if err != nil {
		return nil, err
	}
	device, err := newDeviceOperand(m, line, args[2])
	if err != nil {
		return nil, err
	}
	address, err := intOperand(m, line, args[3])
	if err != nil {
		return nil, err
	}
	return &getOperation{m: m, store: store, device: device, address: address, line: line}, nil
}

func buildGetByID(m *Machine, line int, args []string) (operation, error) {
	store, err := storeOperand(m, line, args[1])
	if err != nil {
		return nil, err
	}
	id, err := intOperand(m, line, args[2])
	if err != nil {
		return nil, err
	}
	address, err := intOperand(m, line, args[3])
	if err != nil {
		return nil, err
	}
	return &getOperation{m: m, store: store, deviceID: id, byID: true, address: address, line: line}, nil
}

func buildPut(m *Machine, line int, args []string) (operation, error) {
	value, err := valueOperand(m, line, args[3])
	if err != nil {
		return nil, err
	}
	device, err := newDeviceOperand(m, line, args[1])
	if err != nil {
		return nil, err
	}
	address, err := intOperand(m, line, args[2])
	if err != nil {
		return nil, err
	}
	return &putOperation{m: m, value: value, device: device, address: address, line: line}, nil
}

func buildPutByID(m *Machine, line int, args []string) (operation, error) {
	value, err := valueOperand(m, line, args[3])
	if err != nil {
		return nil, err
	}
	id, err := intOperand(m, line, args[1])
	if err != nil {
		return nil, err
	}
	address, err := intOperand(m, line, args[2])
	if err != nil {
		return nil, err
	}
	return &putOperation{m: m, value: value, deviceID: id, byID: true, address: address, line: line}, nil
}

func buildClear(m *Machine, line int, args []string) (operation, error) {
	device, err := newDeviceIndexVariable(m, line, args[1], maskDeviceIndex)
	if err != nil {
		return nil, err
	}
	return &clearOperation{m: m, device: *device, line: line}, nil
}

func buildClearByID(m *Machine, line int, args []string) (operation, error) {
	id, err := intOperand(m, line, args[1])
	if err != nil {
		return nil, err
	}
	return &clearOperation{m: m, deviceID: id, byID: true, line: line}, nil
}

func buildRand(m *Machine, line int, args []string) (operation, error) {
	store, err := storeOperand(m, line, args[1])
	if err != nil {
		return nil, err
	}
	return &randOperation{m: m, store: store}, nil
}

func buildYield(_ *Machine, _ int, _ []string) (operation, error) {
	return yieldOperation{}, nil
}

func buildHcf(m *Machine, line int, _ []string) (operation, error) {
	return &hcfOperation{m: m, line: line}, nil
}

func buildSleep(m *Machine, line int, args []string) (operation, error) {
	duration, err := valueOperand(m, line, args[1])
	if err != nil {
		return nil, err
	}
	return &sleepOperation{m: m, duration: duration}, nil
}
