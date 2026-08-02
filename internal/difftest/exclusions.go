package difftest

import (
	"maps"
	"slices"

	"github.com/greg2010/ic11c/internal/ic10"
)

// Shared exclusion reasons, one per class of mnemonic the generators keep out.
const (
	reasonTranscendental    = "reaches each implementation's own platform math library, so results agree only to within an ULP; covered by unit tests against the transliterated implementation instead"
	reasonReagent           = "internal/vm does not model the reagent instructions and returns ErrUnimplemented, which is not a chip verdict to compare"
	reasonNoDeviceModel     = "needs a device on a pin, and the oracle wire protocol cannot set up a matching device topology on both sides"
	reasonEmuMissing        = "ic10emu does not implement it: the line fails to parse, is reported as a compile error, and then runs as a nop"
	reasonExcusesEverything = "a divergence entry covers every field for programs containing it, so emitting it would excuse real mismatches instead of finding them"
)

// excluded is the set of mnemonics the generators never emit, and why.
//
// Everything the instruction set defines is either here or emitted by a
// generator, which TestExclusionsPartitionTheInstructionSet holds to account.
// Removing an entry is a claim that the two implementations now agree about the
// instruction, and needs the evidence that established it.
var excluded = buildExclusions()

func buildExclusions() map[string]string {
	out := map[string]string{
		"rand": "draws from an unseeded generator in the game, so no two implementations agree and there is nothing to compare",

		"sin": reasonTranscendental, "cos": reasonTranscendental, "tan": reasonTranscendental,
		"asin": reasonTranscendental, "acos": reasonTranscendental, "atan": reasonTranscendental,
		"atan2": reasonTranscendental, "log": reasonTranscendental, "exp": reasonTranscendental,
		"pow": reasonTranscendental,

		"lr": reasonReagent, "rmap": reasonReagent,

		"ext": reasonEmuMissing, "ins": reasonEmuMissing, "rol": reasonEmuMissing,
		"ror": reasonEmuMissing, "lerp": reasonEmuMissing, "clamp": reasonEmuMissing,
		"sgn": reasonEmuMissing, "bdnvl": reasonEmuMissing, "bdnvs": reasonEmuMissing,

		"getd": reasonExcusesEverything, "putd": reasonExcusesEverything,
		"sleep": reasonExcusesEverything,
		"max":   reasonExcusesEverything, "min": reasonExcusesEverything,

		"lb": reasonNoDeviceModel, "lbn": reasonNoDeviceModel, "lbns": reasonNoDeviceModel,
		"lbs": reasonNoDeviceModel, "sb": reasonNoDeviceModel, "sbn": reasonNoDeviceModel,
		"sbs": reasonNoDeviceModel, "ss": reasonNoDeviceModel, "ls": reasonNoDeviceModel,
		"ld": reasonNoDeviceModel, "sd": reasonNoDeviceModel, "clrd": reasonNoDeviceModel,

		"label": "is alias with its operands the other way round, and the name it binds can only stand for a device pin; every alias the generators emit names a register and every device operand they emit is a literal pin, so the binding would have no reader and the line would compare nothing alias does not",

		"sap": reasonExcusesEverything, "sna": reasonExcusesEverything,
		"sapz": reasonExcusesEverything, "snaz": reasonExcusesEverything,
		"bap": reasonExcusesEverything, "bapal": reasonExcusesEverything,
		"bna": reasonExcusesEverything, "bnaal": reasonExcusesEverything,
		"bapz": reasonExcusesEverything, "bnaz": reasonExcusesEverything,

		"round": reasonExcusesEverything,
	}

	for _, instruction := range ic10.Instructions {
		if reason, ok := ic10.Unemittable(instruction.Opcode); ok {
			out[instruction.Mnemonic] = "unemittable: " + reason
		}
	}
	return out
}

// Excluded reports whether a mnemonic is kept out of every generator, and the
// reason it is.
func Excluded(mnemonic string) (reason string, ok bool) {
	reason, ok = excluded[mnemonic]
	return reason, ok
}

// ExcludedMnemonics lists every excluded mnemonic in sorted order.
func ExcludedMnemonics() []string {
	return slices.Sorted(maps.Keys(excluded))
}
