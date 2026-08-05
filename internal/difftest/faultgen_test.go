package difftest

import (
	"context"
	"testing"

	"github.com/greg2010/ic11c/internal/chip"
	"github.com/greg2010/ic11c/internal/chiptest"
)

// TestRecipesRaiseTheFaultTheyName is what keeps the fault generator honest.
// A recipe that stops faulting still assembles and runs, so nothing else
// would notice that it had stopped testing the error model.
func TestRecipesRaiseTheFaultTheyName(t *testing.T) {
	for _, recipe := range faultRecipes {
		t.Run(recipe.name, func(t *testing.T) {
			ctx, harness := chiptest.Harness(t)
			runRecipe(ctx, t, harness, recipe)
		})
	}
}

func runRecipe(ctx context.Context, t *testing.T, harness *chip.Harness, recipe faultRecipe) {
	t.Helper()
	for seed := range uint64(64) {
		g := newGenerator(seed)
		start, end := buildFaultProgram(g, recipe)
		p := g.program(seed, KindFault, recipe.name)

		got, err := Run(ctx, harness, p)
		if err != nil {
			t.Fatalf("seed %d: Run: %v\n%s", seed, err, p.Source)
		}
		if got.Stop != chip.StopFaulted {
			t.Fatalf("seed %d: stopped %q (compile %v), want %q\n%s",
				seed, got.Stop, got.CompileError, chip.StopFaulted, p.Source)
		}
		if got.Fault.Type != recipe.want {
			t.Errorf("seed %d: fault = %v, want %v\n%s", seed, got.Fault.Type, recipe.want, p.Source)
		}
		if line := got.Fault.Line; line < start || line >= end {
			t.Errorf("seed %d: faulted on line %d, outside the recipe's lines %d..%d\n%s",
				seed, line, start, end-1, p.Source)
		}
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
	if want := len(faultRecipes); len(seen) != want {
		t.Errorf("saw %d distinct recipes, want %d", len(seen), want)
	}
}
