package difftest

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/greg2010/ic11c/internal/chip"
	"github.com/greg2010/ic11c/internal/ic10"
)

// How many programs a corpus draws.
const (
	// DefaultPrograms keeps a corpus inside the ordinary suite's budget.
	DefaultPrograms = 1000
	// ShortPrograms is what -short draws.
	ShortPrograms = 50
)

// Generator kinds.
const (
	// KindValue is the generator that emits terminating, fault-free programs,
	// held to reaching their own end.
	KindValue = "value"
	// KindFault is the generator that provokes a fault, held to raising the
	// exception the recipe names on the line the recipe stands on.
	KindFault = "fault"
)

// Program is one generated program and everything needed to replay it. Source
// and Initial travel together because the source is written to start from
// those registers; a run given one without the other is a run of a program
// nobody generated.
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
	Initial chip.State
	// Mnemonics are the mnemonics the source contains, in sorted order. It is
	// what the source names, not what a run executes: a branch that skips a
	// line still counts it, because the chip does not report which
	// instructions retired.
	Mnemonics []string
}

func (p Program) String() string {
	if p.Recipe != "" {
		return fmt.Sprintf("%s program, recipe %q, seed %d", p.Kind, p.Recipe, p.Seed)
	}
	return fmt.Sprintf("%s program, seed %d", p.Kind, p.Seed)
}

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
// set this corpus reached, and what it did not, separating the mnemonics
// [excluded] keeps out on purpose from the rest.
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
