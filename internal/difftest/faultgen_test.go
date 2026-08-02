package difftest

import (
	"context"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/oracle"
)

// TestRecipesRaiseTheFaultTheyName is what keeps the fault generator honest. A
// recipe that stops faulting still assembles, still runs, and still agrees with
// the harness, so nothing else would notice that it had stopped testing the
// error model.
func TestRecipesRaiseTheFaultTheyName(t *testing.T) {
	ctx := context.Background()
	for _, recipe := range faultRecipes {
		t.Run(recipe.name, func(t *testing.T) {
			for seed := range uint64(64) {
				g := newGenerator(seed)
				start, end := buildFaultProgram(g, recipe)
				p := g.program(seed, KindFault, recipe.name)

				got, err := Run(ctx, p.Source, p.Initial, 0)
				if err != nil {
					t.Fatalf("seed %d: Run: %v\n%s", seed, err, p.Source)
				}
				if got.Status != StatusError {
					t.Fatalf("seed %d: status = %q, want %q\n%s", seed, got.Status, StatusError, p.Source)
				}
				if want := HarnessErrorName(recipe.want); got.ErrorType != want {
					t.Errorf("seed %d: error type = %q, want %q\n%s", seed, got.ErrorType, want, p.Source)
				}
				if line := int(got.ErrorLine); line < start || line >= end {
					t.Errorf("seed %d: faulted on line %d, outside the recipe's lines %d..%d\n%s",
						seed, line, start, end-1, p.Source)
				}
			}
		})
	}
}

// TestFaultProgramsCoverEveryRecipe guards against a recipe becoming
// unreachable through the seeded choice.
func TestFaultProgramsCoverEveryRecipe(t *testing.T) {
	seen := make(map[string]int)
	for seed := range uint64(corpusSample) {
		seen[FaultProgram(seed).Recipe]++
	}
	for _, name := range faultRecipeNames() {
		if seen[name] == 0 {
			t.Errorf("recipe %q never chosen across %d seeds", name, corpusSample)
		}
	}
	if len(seen) != len(faultRecipes) {
		t.Errorf("saw %d distinct recipes, want %d", len(seen), len(faultRecipes))
	}
}

// TestFaultRecipeOperandsResolve covers the names a recipe hard-codes. A logic
// type the tables do not carry is a compile error rather than the run time
// fault the recipe is built to raise.
func TestFaultRecipeOperandsResolve(t *testing.T) {
	for _, name := range logicTypePool {
		if _, ok := ic10.LookupLogicType(name); !ok {
			t.Errorf("logic type %q is not in the generated tables", name)
		}
	}
}

// TestUnregisteredDivergencesAreDescribed keeps the record of what the
// generators deliberately do not produce from decaying into a list of names.
func TestUnregisteredDivergencesAreDescribed(t *testing.T) {
	if len(unregisteredDivergences) == 0 {
		t.Fatalf("no divergences recorded")
	}
	for _, d := range unregisteredDivergences {
		if d.Harness == "" || d.Summary == "" || d.Source == "" || d.Ours == "" || d.Theirs == "" {
			t.Errorf("divergence %q is incompletely described: %+v", d.Summary, d)
		}
		if d.Harness != oracle.IC10Emu && d.Harness != oracle.NPM {
			t.Errorf("divergence %q names an unknown harness %q", d.Summary, d.Harness)
		}
	}
}
