package difftest

import (
	"context"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/vm"
)

// corpusSample is how many programs the generator-only tests draw. It is large
// enough for every weighted shape to appear and small enough to stay in the
// ordinary suite's budget.
const corpusSample = 2000

// sample generates one program of each kind for every seed in a fixed range.
func sample(tb testing.TB, n int) []Program {
	tb.Helper()
	programs := make([]Program, 0, 2*n)
	for seed := range uint64(n) {
		programs = append(programs, ValueProgram(seed), FaultProgram(seed))
	}
	return programs
}

func TestGenerationIsReproducible(t *testing.T) {
	tests := []struct {
		name     string
		generate func(uint64) Program
	}{
		{name: "value", generate: ValueProgram},
		{name: "fault", generate: FaultProgram},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, seed := range []uint64{0, 1, 42, 1 << 40, ^uint64(0)} {
				first, second := tt.generate(seed), tt.generate(seed)
				if first.Source != second.Source {
					t.Errorf("seed %d: source differs between runs\nfirst:\n%s\nsecond:\n%s",
						seed, first.Source, second.Source)
				}
				if first.Initial != second.Initial {
					t.Errorf("seed %d: initial state differs between runs", seed)
				}
				if first.Recipe != second.Recipe {
					t.Errorf("seed %d: recipe = %q then %q", seed, first.Recipe, second.Recipe)
				}
			}
		})
	}
}

func TestDistinctSeedsGiveDistinctPrograms(t *testing.T) {
	seen := make(map[string]uint64)
	for _, p := range sample(t, 200) {
		key := p.Kind + "\x00" + p.Source
		if other, ok := seen[key]; ok && other != p.Seed {
			t.Errorf("seeds %d and %d produce the same %s program", other, p.Seed, p.Kind)
		}
		seen[key] = p.Seed
	}
}

func TestGeneratedProgramsLoad(t *testing.T) {
	ctx := context.Background()
	for _, p := range sample(t, corpusSample/2) {
		m := vm.NewMachine()
		if err := m.Load(ctx, p.Source); err != nil {
			t.Fatalf("%s does not assemble: %v\n%s", p, err, p.Source)
		}
	}
}

// TestValueProgramsRunToTheEnd is what holds the value generator to its claim.
// A program that faults is still comparable, so nothing else would notice that
// the generator had started producing them, and the corpus would quietly stop
// testing final machine state.
func TestValueProgramsRunToTheEnd(t *testing.T) {
	ctx := context.Background()
	for seed := range uint64(corpusSample) {
		p := ValueProgram(seed)
		got, err := Run(ctx, p.Source, p.Initial, 0)
		if err != nil {
			t.Fatalf("%s: %v\n%s", p, err, p.Source)
		}
		if got.Status != StatusEnded {
			t.Fatalf("%s: status = %q (%s on line %d), want %q\n%s",
				p, got.Status, got.ErrorType, got.ErrorLine, StatusEnded, p.Source)
		}
	}
}

// TestSourceHasNoTrailingNewline pins the one representation choice the two
// harnesses disagree about. ic10emu drops a trailing newline and the npm
// package keeps it as a final empty line, so a program carrying one is not
// comparable across both.
func TestSourceHasNoTrailingNewline(t *testing.T) {
	for _, p := range sample(t, 200) {
		if strings.HasSuffix(p.Source, "\n") {
			t.Fatalf("%s ends in a newline", p)
		}
		if p.Source == "" {
			t.Fatalf("%s is empty", p)
		}
	}
}

// TestExcludedMnemonicsAreNeverEmitted is the exclusion policy's only
// enforcement: nothing stops an emitter naming an excluded mnemonic except
// this.
func TestExcludedMnemonicsAreNeverEmitted(t *testing.T) {
	for _, p := range sample(t, corpusSample/2) {
		for _, mnemonic := range p.Mnemonics {
			if reason, ok := Excluded(mnemonic); ok {
				t.Fatalf("%s emits excluded mnemonic %q (%s)\n%s", p, mnemonic, reason, p.Source)
			}
		}
		for line := range strings.SplitSeq(p.Source, "\n") {
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			if _, ok := Excluded(fields[0]); ok {
				t.Fatalf("%s has a line starting with excluded mnemonic %q\n%s", p, fields[0], p.Source)
			}
		}
	}
}

// TestExclusionsPartitionTheInstructionSet holds the coverage claim to account:
// every mnemonic is either something a generator emits or something excluded
// with a reason, and nothing is both.
func TestExclusionsPartitionTheInstructionSet(t *testing.T) {
	generated := make(map[string]bool)
	for _, mnemonic := range GeneratedMnemonics() {
		generated[mnemonic] = true
	}
	for _, instruction := range ic10.Instructions {
		reason, isExcluded := Excluded(instruction.Mnemonic)
		switch {
		case isExcluded && generated[instruction.Mnemonic]:
			t.Errorf("%s is both generated and excluded", instruction.Mnemonic)
		case isExcluded && reason == "":
			t.Errorf("%s is excluded with no reason", instruction.Mnemonic)
		case !isExcluded && !generated[instruction.Mnemonic]:
			t.Errorf("%s is neither generated nor excluded", instruction.Mnemonic)
		}
	}
	for mnemonic := range excluded {
		if _, ok := ic10.LookupInstruction(mnemonic); !ok {
			t.Errorf("excluded mnemonic %q is not in the instruction set", mnemonic)
		}
	}
}

// TestCorpusReachesTheGeneratedMnemonics keeps the declared set honest in both
// directions: a mnemonic listed but no longer emitted is as much a defect as
// one emitted but not listed.
func TestCorpusReachesTheGeneratedMnemonics(t *testing.T) {
	coverage := make(Coverage)
	for _, p := range sample(t, corpusSample/2) {
		coverage.Add(p)
	}
	declared := make(map[string]bool)
	for _, mnemonic := range GeneratedMnemonics() {
		declared[mnemonic] = true
	}
	for _, mnemonic := range coverage.Reached() {
		if !declared[mnemonic] {
			t.Errorf("corpus reaches %q, which GeneratedMnemonics does not list", mnemonic)
		}
	}
	for mnemonic := range declared {
		if coverage[mnemonic] == 0 {
			t.Errorf("GeneratedMnemonics lists %q, which a %d program corpus never reaches",
				mnemonic, corpusSample)
		}
	}
	t.Log(coverage.Report())
}

// TestGeneratedNamesAreNotReserved guards the one silent failure mode in a
// generated name: a label or define colliding with a name the chip already
// resolves is not rejected, it is shadowed, and every line referring to it then
// faults forever.
func TestGeneratedNamesAreNotReserved(t *testing.T) {
	for _, p := range sample(t, 200) {
		for line := range strings.SplitSeq(p.Source, "\n") {
			fields := strings.Fields(line)
			var name string
			switch {
			case len(fields) == 1 && strings.HasSuffix(fields[0], ":"):
				name = strings.TrimSuffix(fields[0], ":")
			case len(fields) == 3 && (fields[0] == "define" || fields[0] == "alias"):
				name = fields[1]
			default:
				continue
			}
			if ic10.IsReservedWord(name) {
				t.Errorf("%s declares reserved name %q", p, name)
			}
		}
	}
}
