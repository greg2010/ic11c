package ic10_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
)

// convertedOperand is one operand position and what the chip reads it through.
type convertedOperand struct {
	mnemonic   string
	position   int
	conversion ic10.Conversion
}

// convertedOperands is every operand the chip reads through a conversion:
// a hand-written oracle sourced from the reader each operation calls, not
// derived from the table it checks. The bitwise and shift families carry
// the two long reductions, every distance the int one. rmap's reagent hash
// is the one ConversionNarrowedInt — its own body casts it, rather than a
// reader bounding it first. ins converts its assigned operand too, since
// it folds a field into what the destination already held.
var convertedOperands = []convertedOperand{
	{mnemonic: "and", position: 1, conversion: ic10.ConversionSignedLong},
	{mnemonic: "and", position: 2, conversion: ic10.ConversionSignedLong},
	{mnemonic: "or", position: 1, conversion: ic10.ConversionSignedLong},
	{mnemonic: "or", position: 2, conversion: ic10.ConversionSignedLong},
	{mnemonic: "xor", position: 1, conversion: ic10.ConversionSignedLong},
	{mnemonic: "xor", position: 2, conversion: ic10.ConversionSignedLong},
	{mnemonic: "nor", position: 1, conversion: ic10.ConversionSignedLong},
	{mnemonic: "nor", position: 2, conversion: ic10.ConversionSignedLong},
	{mnemonic: "not", position: 1, conversion: ic10.ConversionSignedLong},
	{mnemonic: "srl", position: 1, conversion: ic10.ConversionUnsignedLong},
	{mnemonic: "srl", position: 2, conversion: ic10.ConversionInt},
	{mnemonic: "sra", position: 1, conversion: ic10.ConversionSignedLong},
	{mnemonic: "sra", position: 2, conversion: ic10.ConversionInt},
	{mnemonic: "sll", position: 1, conversion: ic10.ConversionSignedLong},
	{mnemonic: "sll", position: 2, conversion: ic10.ConversionInt},
	{mnemonic: "sla", position: 1, conversion: ic10.ConversionSignedLong},
	{mnemonic: "sla", position: 2, conversion: ic10.ConversionInt},
	{mnemonic: "rol", position: 1, conversion: ic10.ConversionUnsignedLong},
	{mnemonic: "rol", position: 2, conversion: ic10.ConversionInt},
	{mnemonic: "ror", position: 1, conversion: ic10.ConversionUnsignedLong},
	{mnemonic: "ror", position: 2, conversion: ic10.ConversionInt},
	{mnemonic: "ext", position: 1, conversion: ic10.ConversionUnsignedLong},
	{mnemonic: "ext", position: 2, conversion: ic10.ConversionInt},
	{mnemonic: "ext", position: 3, conversion: ic10.ConversionInt},
	{mnemonic: "ins", position: 0, conversion: ic10.ConversionUnsignedLong},
	{mnemonic: "ins", position: 1, conversion: ic10.ConversionUnsignedLong},
	{mnemonic: "ins", position: 2, conversion: ic10.ConversionInt},
	{mnemonic: "ins", position: 3, conversion: ic10.ConversionInt},
	{mnemonic: "rmap", position: 2, conversion: ic10.ConversionNarrowedInt},
}

// TestConvertedOperandsAreTheWholeOfWhatTheTableConverts holds every named
// operand to what the table says, and holds the table to naming no
// others — so a conversion that silently became none, or one that
// silently appeared, fails either way.
func TestConvertedOperandsAreTheWholeOfWhatTheTableConverts(t *testing.T) {
	for _, want := range convertedOperands {
		instruction, known := ic10.LookupInstruction(want.mnemonic)
		if !known {
			t.Errorf("the machine has no %s", want.mnemonic)
			continue
		}
		if want.position >= len(instruction.Operands) {
			t.Errorf("%s has %d operands, and operand %d is named here", want.mnemonic, len(instruction.Operands), want.position)
			continue
		}
		if got := instruction.Operands[want.position].Conversion; got != want.conversion {
			t.Errorf("%s operand %d is read through %s, want %s", want.mnemonic, want.position, got, want.conversion)
		}
	}

	named := make(map[string]bool, len(convertedOperands))
	for _, want := range convertedOperands {
		named[fmt.Sprintf("%s operand %d", want.mnemonic, want.position)] = true
	}
	var unnamed []string
	for _, instruction := range ic10.Instructions {
		for position, operand := range instruction.Operands {
			key := fmt.Sprintf("%s operand %d", instruction.Mnemonic, position)
			switch {
			case operand.Conversion == ic10.ConversionNone:
				if named[key] {
					t.Errorf("%s is read through none, and is named here as converted", key)
				}
			case !named[key]:
				unnamed = append(unnamed, fmt.Sprintf("%s (%s)", key, operand.Conversion))
			}
		}
	}
	if len(unnamed) > 0 {
		t.Errorf("the table converts operands this does not name: %v", unnamed)
	}
}

// TestEveryExtractedConversionReachesAnOperand holds each conversion the
// vocabulary carries to being reachable by at least one operand — a
// spelling nothing uses is either a reader the game dropped or a reading
// this package stopped doing. ConversionUnknown is excluded: reaching it
// is the failure it exists to report.
func TestEveryExtractedConversionReachesAnOperand(t *testing.T) {
	for _, conversion := range []ic10.Conversion{
		ic10.ConversionNone,
		ic10.ConversionInt,
		ic10.ConversionNarrowedInt,
		ic10.ConversionSignedLong,
		ic10.ConversionUnsignedLong,
	} {
		reached := slices.ContainsFunc(ic10.Instructions, func(instruction ic10.Instruction) bool {
			return slices.ContainsFunc(instruction.Operands, func(operand ic10.Operand) bool {
				return operand.Conversion == conversion
			})
		})
		if !reached {
			t.Errorf("no operand is read through %s", conversion)
		}
	}
}
