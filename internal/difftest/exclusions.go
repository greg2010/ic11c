package difftest

import (
	"slices"

	"github.com/greg2010/ic11c/internal/ic10"
)

// Exclusion reasons, one per class of mnemonic a generator keeps out, named
// rather than retyped per mnemonic so the wording cannot drift between them.
const (
	// reasonUnseededRandom is rand.
	reasonUnseededRandom = "draws from an unseeded generator in the game, so what a program answers is not a function of the program"
	// reasonNotSliced is hcf, the one operation class the slice leaves behind.
	reasonNotSliced = "tools/chipgen drops the parse arm that builds the operation, so the harness cannot compile the mnemonic at all"
	// reasonHarnessClock is sleep, which counts down against a clock the sweep
	// never arms.
	reasonHarnessClock = "counts down against a clock the sweep does not arm, so how far a program containing one runs depends on state no generator sets"
	// reasonNoOperandCount is the four forms whose token count check and
	// operand read disagree, so the program never runs and the rest of it is
	// wasted.
	reasonNoOperandCount = "no operand count assembles in the game build, so a program naming one is judged on its compile error and on nothing it does"
)

// excluded is what the generators never emit, and why. Everything the
// instruction set defines is either here or emitted by a generator;
// TestExclusionsPartitionTheInstructionSet holds both halves to account.
var excluded = map[string]string{
	"rand": reasonUnseededRandom,

	"hcf":   reasonNotSliced,
	"sleep": reasonHarnessClock,

	"brapz": reasonNoOperandCount, "brnaz": reasonNoOperandCount,
	"bapzal": reasonNoOperandCount, "bnazal": reasonNoOperandCount,
}

// Excluded reports whether the generators keep a mnemonic out, and the reason
// they do.
func Excluded(mnemonic string) (reason string, ok bool) {
	reason, ok = excluded[mnemonic]
	return reason, ok
}

// GeneratedMnemonics lists the mnemonics the generators emit, in sorted
// order. It is the complement of [excluded] rather than a second declaration
// beside it, so the two cannot drift apart.
func GeneratedMnemonics() []string {
	var out []string
	for _, instruction := range ic10.Instructions {
		if _, ok := excluded[instruction.Mnemonic]; !ok {
			out = append(out, instruction.Mnemonic)
		}
	}
	slices.Sort(out)
	return out
}
