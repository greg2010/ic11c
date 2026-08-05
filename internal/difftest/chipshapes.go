package difftest

import (
	"strconv"

	"github.com/greg2010/ic11c/internal/chip"
)

// The shapes here reach the instruction families the compiler backend never
// selects: the transcendentals, the bitfield and rotate group, the approximate
// comparisons, the relative branches and the device forms.

// fieldBits is the significand ext and ins address, and what bounds a legal
// offset and width pair. It is restated here rather than derived, since
// nothing the extraction reaches records it; the fault recipes at the end of
// this file hold it to the chip instead.
const fieldBits = 53

// Mnemonic groups these emitters draw from, each sharing an operand shape so
// that one emitter covers the group.
var (
	chipUnaryOps             = []string{"round", "sgn"}
	transcendentalUnaryOps   = []string{"sin", "cos", "tan", "asin", "acos", "atan", "log", "exp"}
	transcendentalBinaryOps  = []string{"atan2", "pow"}
	extremeOps               = []string{"max", "min"}
	bitfieldOps              = []string{"ext", "ins"}
	rotateOps                = []string{"rol", "ror"}
	approximateOps           = []string{"sap", "sna"}
	approximateZeroOps       = []string{"sapz", "snaz"}
	approximateBranchOps     = []string{"bap", "bna"}
	approximateBranchZeroOps = []string{"bapz", "bnaz"}
	relativeBranch1Ops       = []string{"brltz", "brgtz", "brlez", "brgez", "breqz", "brnez", "brnan"}
	relativeBranch2Ops       = []string{"brlt", "brgt", "brle", "brge", "breq", "brne"}
	relativeBranch3Ops       = []string{"brap", "brna"}
	relativeDeviceBranchOps  = []string{"brdse", "brdns"}
)

// tolerancePool is the third operand of the approximate comparisons, kept
// apart from literalPool because that pool is dominated by values too large
// to make a tolerance test ever fail. The negative entry is a defined case,
// not a mistake: the tolerance is floored at a constant before use.
var tolerancePool = []string{"0", "0.0000001", "0.001", "0.1", "1", "-1"}

// tolerance draws the operand that decides an approximate comparison, half
// from tolerancePool and half from anywhere a value can come from, which is
// what puts a NaN there — one of the two ways sap and sna diverge from being
// complements.
func (g *generator) tolerance() string {
	if g.rng.IntN(2) == 0 {
		return pick(g.rng, tolerancePool)
	}
	return g.value()
}

func (g *generator) emitChipUnary() {
	source := g.value()
	g.emit(pick(g.rng, chipUnaryOps), g.destination(false), source)
}

// emitTranscendental is the group whose answers come from a platform math
// library rather than from the chip, so a program naming one is held only to
// assembling, running and reaching its own end — not to the digits that come
// back.
func (g *generator) emitTranscendental() {
	if g.rng.IntN(3) == 0 {
		a, b := g.value(), g.value()
		g.emit(pick(g.rng, transcendentalBinaryOps), g.destination(false), a, b)
		return
	}
	source := g.value()
	g.emit(pick(g.rng, transcendentalUnaryOps), g.destination(false), source)
}

// emitExtreme reaches the signed zero tie-break: initialPool carries a
// negative zero and literalPool a positive one.
func (g *generator) emitExtreme() {
	a, b := g.value(), g.value()
	g.emit(pick(g.rng, extremeOps), g.destination(false), a, b)
}

// emitInterpolate draws clamp's bounds independently rather than in order, so
// a low bound above the high one — the case that separates a min-of-max from
// Math.Clamp — stays reachable.
func (g *generator) emitInterpolate() {
	if g.rng.IntN(2) == 0 {
		a, b, t := g.value(), g.value(), g.value()
		g.emit("lerp", g.destination(false), a, b, t)
		return
	}
	value, low, high := g.value(), g.value(), g.value()
	g.emit("clamp", g.destination(false), value, low, high)
}

// emitBitfield keeps the offset and width inside the payload, where the
// instruction computes rather than faults; the bounds themselves are the
// fault recipes' business. The result is always inside the payload, so the
// destination stays a safe bitwise operand.
func (g *generator) emitBitfield() {
	source := g.boundedValue()
	offset := g.rng.IntN(fieldBits)
	width := 1 + g.rng.IntN(fieldBits-offset)
	g.emit(pick(g.rng, bitfieldOps), g.destination(true), source,
		strconv.Itoa(offset), strconv.Itoa(width))
}

// emitRotate shares shiftDistancePool. A rotate reduces modulo 54 instead of
// masking to six bits, so the same pool reaches a different set of
// boundaries.
func (g *generator) emitRotate() {
	value, distance := g.boundedValue(), pick(g.rng, shiftDistancePool)
	g.emit(pick(g.rng, rotateOps), g.destination(true), value, distance)
}

func (g *generator) emitApproximateSelect() {
	if g.rng.IntN(2) == 0 {
		a, tolerance := g.value(), g.tolerance()
		g.emit(pick(g.rng, approximateZeroOps), g.destination(true), a, tolerance)
		return
	}
	a, b, tolerance := g.value(), g.value(), g.tolerance()
	g.emit(pick(g.rng, approximateOps), g.destination(true), a, b, tolerance)
}

func (g *generator) emitApproximateBranch() {
	target := g.newLabel()
	if g.rng.IntN(2) == 0 {
		a, tolerance := g.value(), g.tolerance()
		g.emit(pick(g.rng, approximateBranchZeroOps), a, tolerance, target)
		return
	}
	a, b, tolerance := g.value(), g.value(), g.tolerance()
	g.emit(pick(g.rng, approximateBranchOps), a, b, tolerance, target)
}

// emitRelativeBranch is the one shape whose target operand is a line offset
// rather than a label. The block writes the lines the branch skips itself and
// takes the offset from how many it wrote, which is safe here — unlike in
// compiled output — because no later pass moves these lines; this rests on
// every safeBlocks emitter writing exactly one line, which
// TestSafeBlocksEmitOneLine holds them to. The rejoin clears bounded
// registers, since it is an unmarked join: a skipped block still writes a
// destination as it is emitted, and a bitwise operand drawing that register
// afterward would fault on whatever an earlier write actually left there.
func (g *generator) emitRelativeBranch() {
	skipped := 1 + g.rng.IntN(3)
	offset := strconv.Itoa(skipped + 1)
	switch g.rng.IntN(5) {
	case 0:
		g.emit("jr", offset)
	case 1:
		g.emit(pick(g.rng, relativeBranch1Ops), g.value(), offset)
	case 2:
		g.emit(pick(g.rng, relativeBranch2Ops), g.value(), g.value(), offset)
	case 3:
		g.emit(pick(g.rng, relativeBranch3Ops), g.value(), g.value(), g.tolerance(), offset)
	default:
		g.emit(pick(g.rng, relativeDeviceBranchOps), "d"+strconv.Itoa(g.rng.IntN(6)), offset)
	}
	for range skipped {
		pick(g.rng, safeBlocks)(g)
	}
	g.clearBounded()
}

// emitDeviceLabel is the label form: alias with its operands the other way
// round, binding a name that can only stand for a device pin. It also emits a
// reader in the same block, so the binding is exercised and no forward branch
// can reach the name unbound.
func (g *generator) emitDeviceLabel() {
	name := "p" + strconv.Itoa(g.pinSeq)
	g.pinSeq++
	pin := "d" + strconv.Itoa(g.rng.IntN(6))
	g.emit("label", pin, name)
	if g.rng.IntN(2) == 0 {
		g.emit(pick(g.rng, deviceSetOps), g.destination(true), name)
		return
	}
	g.emit(pick(g.rng, deviceBranchOps), name, g.newLabel())
}

// deviceFaultRecipes are the deliberate faults raised by the bitfield group
// and by the instructions that address a device. The sweep puts nothing on d0
// through d5, so the device ones reach the narrowing rather than an answer,
// and which operand is read first — differing per instruction — decides
// which fault wins.
var deviceFaultRecipes = []faultRecipe{
	{
		name: "bitfield-offset-past-the-payload",
		want: chip.ExcShiftOverflow,
		emit: func(g *generator) {
			g.emit(pick(g.rng, bitfieldOps), g.destination(true), g.boundedValue(),
				strconv.Itoa(fieldBits+g.rng.IntN(100)), "1")
		},
	},
	{
		name: "bitfield-empty-width",
		want: chip.ExcShiftUnderflow,
		emit: func(g *generator) {
			g.emit(pick(g.rng, bitfieldOps), g.destination(true), g.boundedValue(),
				strconv.Itoa(g.rng.IntN(fieldBits)), strconv.Itoa(-g.rng.IntN(100)))
		},
	},
	{
		name: "bitfield-field-past-the-payload",
		want: chip.ExcPayloadOverflow,
		emit: func(g *generator) {
			offset := 1 + g.rng.IntN(fieldBits-1)
			g.emit(pick(g.rng, bitfieldOps), g.destination(true), g.boundedValue(),
				strconv.Itoa(offset), strconv.Itoa(fieldBits-offset+1+g.rng.IntN(10)))
		},
	},
	{
		name: "infinite-rotate-operand",
		want: chip.ExcShiftOverflow,
		emit: func(g *generator) {
			g.emit("div", "r"+strconv.Itoa(scratchRegister), "1", "0")
			g.emit(pick(g.rng, rotateOps), g.destination(true), "r"+strconv.Itoa(scratchRegister), "1")
		},
	},
	{
		name: "batch-form-on-a-housing-with-no-data-cable",
		want: chip.ExcDeviceListNull,
		emit: (*generator).emitBatch,
	},
	{
		name: "slot-read-from-an-unset-pin",
		want: chip.ExcDeviceNotFound,
		emit: func(g *generator) {
			g.emit("ls", g.destination(false), g.pin(), g.slotIndex(), pick(g.rng, logicSlotTypePool))
		},
	},
	{
		name: "slot-write-to-an-unset-pin",
		want: chip.ExcDeviceNotFound,
		emit: func(g *generator) {
			g.emit("ss", g.pin(), g.slotIndex(), pick(g.rng, logicSlotTypePool), g.value())
		},
	},
	{
		name: "reagent-read-from-an-unset-pin",
		want: chip.ExcDeviceNotFound,
		emit: func(g *generator) {
			g.emit("lr", g.destination(false), g.pin(), pick(g.rng, reagentModePool), g.value())
		},
	},
	{
		name: "reagent-map-on-an-unset-pin",
		want: chip.ExcDeviceNotFound,
		emit: func(g *generator) {
			g.emit("rmap", g.destination(false), g.pin(), g.value())
		},
	},
	{
		name: "logic-read-branch-on-an-unset-pin",
		want: chip.ExcDeviceNotFound,
		emit: func(g *generator) {
			g.emit("bdnvl", g.pin(), pick(g.rng, logicTypePool), g.newLabel())
		},
	},
	{
		name: "logic-write-branch-on-an-unset-pin",
		want: chip.ExcDeviceNotFound,
		emit: func(g *generator) {
			g.emit("bdnvs", g.pin(), pick(g.rng, logicTypePool), g.newLabel())
		},
	},
	{
		// ld dereferences the reference id without a null check, unlike sd
		// below, so it faults with ExcUnknown rather than ExcDeviceNotFound.
		name: "logic-read-by-a-reference-id-nothing-has",
		want: chip.ExcUnknown,
		emit: func(g *generator) {
			g.emit("ld", g.destination(false), g.referenceID(), pick(g.rng, logicTypePool))
		},
	},
	{
		name: "logic-write-by-a-reference-id-nothing-has",
		want: chip.ExcDeviceNotFound,
		emit: func(g *generator) {
			g.emit("sd", g.referenceID(), pick(g.rng, logicTypePool), g.value())
		},
	},
	{
		name: "memory-read-by-a-reference-id-nothing-has",
		want: chip.ExcDeviceNotFound,
		emit: func(g *generator) {
			g.emit("getd", g.destination(false), g.referenceID(), g.address())
		},
	},
	{
		name: "memory-write-by-a-reference-id-nothing-has",
		want: chip.ExcDeviceNotFound,
		emit: func(g *generator) {
			g.emit("putd", g.referenceID(), g.address(), g.value())
		},
	},
	{
		name: "memory-clear-by-a-reference-id-nothing-has",
		want: chip.ExcDeviceNotFound,
		emit: func(g *generator) {
			g.emit("clrd", g.referenceID())
		},
	},
}

// These pools name members of three of the seven enums tools/chipgen's slice
// lifts, so an operand naming one resolves on the harness as it does in game.
// A member of any other enum does not resolve there at all.
var (
	logicSlotTypePool    = []string{"Occupied", "Quantity", "Damage"}
	reagentModePool      = []string{"Contents", "Required", "Recipe"}
	logicBatchMethodPool = []string{"Average", "Sum", "Minimum", "Maximum", "Count"}
)

// emitBatch is the batch forms, which address a housing's data cable by
// structure hash rather than a pin. All seven fault here rather than
// answering: the sweep runs no cable to the housing, so the chip raises
// DeviceListNull before it has looked at a hash.
func (g *generator) emitBatch() {
	hash, mode := g.hash(), pick(g.rng, logicBatchMethodPool)
	logic, slotLogic := pick(g.rng, logicTypePool), pick(g.rng, logicSlotTypePool)
	name, slot := g.hash(), g.slotIndex()
	switch g.rng.IntN(7) {
	case 0:
		g.emit("lb", g.destination(false), hash, logic, mode)
	case 1:
		g.emit("lbn", g.destination(false), hash, name, logic, mode)
	case 2:
		g.emit("lbs", g.destination(false), hash, slot, slotLogic, mode)
	case 3:
		g.emit("lbns", g.destination(false), hash, name, slot, slotLogic, mode)
	case 4:
		g.emit("sb", hash, logic, g.value())
	case 5:
		g.emit("sbn", hash, name, logic, g.value())
	default:
		g.emit("sbs", hash, slot, slotLogic, g.value())
	}
}

// hash is a structure or name hash. Both are the game's 32 bit string hashes and
// span both signs, and the sweep builds no network for either to match.
func (g *generator) hash() string { return strconv.Itoa(int(int32(g.rng.Uint32()))) }

// pin is a device pin, which the sweep connects nothing to.
func (g *generator) pin() string { return "d" + strconv.Itoa(g.rng.IntN(6)) }

// referenceID is a device reference id, drawn from a range no device has. Zero
// is included because the game short-circuits on it before the lookup, which is
// a different path to the same missing device.
func (g *generator) referenceID() string { return strconv.Itoa(g.rng.IntN(1000)) }

// slotIndex is a slot number. The chip housing has none, so the fault is raised
// for the missing device before the index is used.
func (g *generator) slotIndex() string { return strconv.Itoa(g.rng.IntN(8)) }
