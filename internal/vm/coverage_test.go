package vm

import (
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
)

// TestEveryScriptCommandIsAccountedFor is the gate that keeps gaps visible. A
// mnemonic with no builder and no explicit mark fails here rather than
// disappearing into a silent no-op at run time.
func TestEveryScriptCommandIsAccountedFor(t *testing.T) {
	for _, record := range Coverage() {
		if !record.Implemented {
			t.Errorf("%s (opcode %d): no implementation and not marked unassemblable", record.Mnemonic, record.Opcode)
			continue
		}
		if record.Class == "" {
			t.Errorf("%s: implemented without naming the C# operation class it derives from", record.Mnemonic)
		}
	}
	if got, want := len(Coverage()), len(ic10.Instructions); got != want {
		t.Errorf("coverage records = %d, instruction table = %d", got, want)
	}
}

// TestTokenCountsMatchTheOperandTables cross-checks the arity this package
// enforces against the arity extracted from the game's help text. The four
// unassemblable forms are where the two legitimately disagree, and the test
// requires exactly those four to disagree.
func TestTokenCountsMatchTheOperandTables(t *testing.T) {
	for _, instruction := range ic10.Instructions {
		spec, ok := instructions[instruction.Opcode]
		if !ok {
			t.Fatalf("%s: no instruction spec", instruction.Mnemonic)
		}
		want := len(instruction.Operands) + 1
		mismatch := spec.tokens != want
		if mismatch != parseImpossible[instruction.Opcode] {
			t.Errorf("%s: tokens = %d, operand table implies %d, unassemblable = %v",
				instruction.Mnemonic, spec.tokens, want, parseImpossible[instruction.Opcode])
		}
	}
}

// TestParseImpossibleIsASubsetOfUnemittable checks that everything this package
// cannot assemble is also something the backend is told never to select. The
// reverse does not hold: sla, the relative branches, hcf and sleep all assemble
// and run here and are still off the table for the compiler.
func TestParseImpossibleIsASubsetOfUnemittable(t *testing.T) {
	for op := range parseImpossible {
		if _, ok := ic10.Unemittable(op); !ok {
			t.Errorf("%s cannot be assembled but is not marked unemittable in internal/ic10", op)
		}
	}
}

// TestUnitCoverageMatchesTests holds the declared unit column to what the
// per-instruction corpus actually exercises, in both directions, so the record
// cannot drift from the tests.
func TestUnitCoverageMatchesTests(t *testing.T) {
	exercised := make(map[ic10.Opcode]bool)
	for _, c := range instructionCases {
		exercised[c.op] = true
	}
	for op := range exercised {
		if !unitTested[op] {
			t.Errorf("%s is exercised by a test but not recorded in unitTested", op)
		}
	}
	for op := range unitTested {
		if !exercised[op] {
			t.Errorf("%s is recorded in unitTested but no test exercises it", op)
		}
	}
}

// TestCoverageSummary reports the per-mnemonic totals. It asserts the two
// invariants the other tests do not cover, that no mnemonic is both implemented
// and unassemblable by accident and that none is both reached by the
// differential corpus and excluded from it, and prints the rest so a run shows
// the gap.
func TestCoverageSummary(t *testing.T) {
	var implemented, impossible, untested, fuzzed, fuzzExcludedCount, unfuzzed int
	for _, record := range Coverage() {
		if record.Implemented {
			implemented++
		}
		if record.ParseImpossible {
			impossible++
		}
		if !record.Unit {
			untested++
		}
		if record.Fuzz && record.FuzzExcluded {
			t.Errorf("%s is both reached by the differential corpus and excluded from it", record.Mnemonic)
		}
		switch {
		case record.Fuzz:
			fuzzed++
		case record.FuzzExcluded:
			fuzzExcludedCount++
		default:
			unfuzzed++
		}
	}
	t.Logf("mnemonics %d, implemented %d, unassemblable %d, without a unit test %d, "+
		"reached by the differential corpus %d, excluded from it %d, neither %d",
		len(Coverage()), implemented, impossible, untested, fuzzed, fuzzExcludedCount, unfuzzed)
	if impossible != 4 {
		t.Errorf("unassemblable mnemonics = %d, want the 4 whose token check and operand read disagree", impossible)
	}
}

// TestEnumOrdinalsMatchTables keeps the ordinals this package switches on in
// step with the generated tables, since a switch cannot be written over a table
// lookup.
func TestEnumOrdinalsMatchTables(t *testing.T) {
	batch := map[string]int{
		"Average": batchAverage,
		"Sum":     batchSum,
		"Minimum": batchMinimum,
		"Maximum": batchMaximum,
		"Count":   batchCount,
	}
	for name, want := range batch {
		info, ok := ic10.LookupBatchMode(name)
		if !ok {
			t.Errorf("batch mode %s is missing from the tables", name)
			continue
		}
		if int(info.Value) != want {
			t.Errorf("batch mode %s = %d in the tables, %d here", name, info.Value, want)
		}
	}
	reagent := map[string]int{
		"Contents":      reagentContents,
		"Required":      reagentRequired,
		"Recipe":        reagentRecipe,
		"TotalContents": reagentTotalContents,
	}
	for name, want := range reagent {
		info, ok := ic10.LookupReagentMode(name)
		if !ok {
			t.Errorf("reagent mode %s is missing from the tables", name)
			continue
		}
		if int(info.Value) != want {
			t.Errorf("reagent mode %s = %d in the tables, %d here", name, info.Value, want)
		}
	}
	if info, ok := ic10.LookupLogicType("None"); !ok || info.Value != logicTypeNone {
		t.Errorf("LogicType None = %v (found %v), want %d", info.Value, ok, logicTypeNone)
	}
	if _, ok := ic10.LookupLogicType("LineNumber"); !ok {
		t.Error("LogicType LineNumber is missing from the tables; the chip's own db read depends on it")
	}
}
