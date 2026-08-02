package difftest

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/oracle"
)

// Generator kinds.
const (
	// KindValue is the generator that emits terminating, fault-free programs,
	// compared on final machine state.
	KindValue = "value"
	// KindFault is the generator that provokes a fault, compared on error type
	// and faulting line.
	KindFault = "fault"
)

// Program is one generated program and everything needed to replay it.
//
// Both implementations must be given the same Source and Initial or the
// comparison means nothing.
type Program struct {
	// Seed regenerates this program exactly, through ValueProgram or
	// FaultProgram according to Kind.
	Seed uint64
	// Kind is KindValue or KindFault.
	Kind string
	// Recipe names the fault construct, and is empty for a value program.
	Recipe string
	// Source is IC10 assembly with no trailing newline.
	Source string
	// Initial is the machine state the program starts from.
	Initial oracle.State
	// Mnemonics are the mnemonics the source contains, in sorted order. It is
	// what the source names, not what a run executes: a branch that skips a
	// line still counts it, because the interpreter does not report which
	// instructions retired.
	Mnemonics []string
}

func (p Program) String() string {
	if p.Recipe != "" {
		return fmt.Sprintf("%s program, recipe %q, seed %d", p.Kind, p.Recipe, p.Seed)
	}
	return fmt.Sprintf("%s program, seed %d", p.Kind, p.Seed)
}

// generatedMnemonics is every mnemonic a generator emits.
//
// It is a declaration, not an observation: TestCorpusReachesTheGeneratedMnemonics
// compares it against what a corpus actually produces and fails on either
// direction of drift, so a mnemonic listed here without an emitter is caught.
var generatedMnemonics = []string{
	"abs", "add", "alias", "and", "bdns", "bdnsal", "bdse", "bdseal", "beq",
	"beqal", "beqz", "beqzal", "bge", "bgeal", "bgez", "bgezal", "bgt",
	"bgtal", "bgtz", "bgtzal", "ble", "bleal", "blez", "blezal", "blt",
	"bltal", "bltz", "bltzal", "bnan", "bne", "bneal", "bnez", "bnezal",
	"ceil", "clr", "define", "div", "floor", "get", "j", "jal", "l", "mod",
	"move", "mul", "nor", "not", "or", "peek", "poke", "pop", "push", "put",
	"s", "sdns", "sdse", "select", "seq", "seqz", "sge", "sgez", "sgt",
	"sgtz", "sle", "slez", "sll", "slt", "sltz", "snan", "snanz", "sne",
	"snez", "sqrt", "sra", "srl", "sub", "trunc", "xor", "yield",
}

// GeneratedMnemonics lists the mnemonics the generators emit, in sorted order.
// Everything else in the instruction set is excluded, with a reason.
func GeneratedMnemonics() []string { return slices.Clone(generatedMnemonics) }

// Coverage counts how many generated programs named each mnemonic.
type Coverage map[string]int

// Add folds one program's mnemonics into the tally.
func (c Coverage) Add(p Program) {
	for _, mnemonic := range p.Mnemonics {
		c[mnemonic]++
	}
}

// Reached lists the mnemonics the corpus named, in sorted order.
func (c Coverage) Reached() []string { return slices.Sorted(maps.Keys(c)) }

// Missed lists the mnemonics the corpus did not name, in sorted order.
func (c Coverage) Missed() []string {
	var out []string
	for _, instruction := range ic10.Instructions {
		if c[instruction.Mnemonic] == 0 {
			out = append(out, instruction.Mnemonic)
		}
	}
	slices.Sort(out)
	return out
}

// Report renders the tally as a coverage summary: how much of the instruction
// set this corpus reached, and what it did not, separating mnemonics excluded
// on purpose from the rest.
//
// The last list is empty only for a tally over every generator. One corpus
// alone leaves the other's mnemonics in it, which is the honest reading: it is
// what that corpus did not exercise.
func (c Coverage) Report() string {
	reached, missed := c.Reached(), c.Missed()
	var excludedMisses, unreached []string
	for _, mnemonic := range missed {
		if _, ok := excluded[mnemonic]; ok {
			excludedMisses = append(excludedMisses, mnemonic)
			continue
		}
		unreached = append(unreached, mnemonic)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "fuzz coverage: %d of %d mnemonics reached", len(reached), len(ic10.Instructions))
	fmt.Fprintf(&b, "\n  reached: %s", strings.Join(reached, " "))
	fmt.Fprintf(&b, "\n  excluded by policy (%d): %s", len(excludedMisses), strings.Join(excludedMisses, " "))
	if len(unreached) > 0 {
		fmt.Fprintf(&b, "\n  not reached by this corpus (%d): %s", len(unreached), strings.Join(unreached, " "))
	}
	return b.String()
}
