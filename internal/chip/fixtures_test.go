package chip

import (
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
)

// TestEveryReagentModeIsAccountedFor holds the seeding table to the generated
// one, in both directions: a renamed mode would silently drop out of
// reagentSeeds and SetReagent would refuse a surface a program can still
// reach, and a name this package invents that the tables don't have is the
// same fault the other way.
func TestEveryReagentModeIsAccountedFor(t *testing.T) {
	// TotalContents sums the mixture Contents seeds rather than holding a
	// quantity of its own, so it is the one mode with no seeding verb.
	const summed = "TotalContents"

	for _, mode := range ic10.ReagentModes {
		_, seedable := reagentSeeds[mode.Value]
		switch {
		case mode.Name == summed && seedable:
			t.Errorf("%s is seedable, so a total need not add up to its own parts", summed)
		case mode.Name != summed && !seedable:
			t.Errorf("lr reads %s and no verb seeds it, so a program reading it meets whatever a device happened to hold", mode.Name)
		}
	}
	if got, want := len(reagentSeeds), len(ic10.ReagentModes)-1; got != want {
		t.Errorf("%d modes are seedable and the tables name %d, of which %s is not",
			got, len(ic10.ReagentModes), summed)
	}
	for name := range reagentSeedNames {
		if _, ok := ic10.LookupReagentMode(name); !ok {
			t.Errorf("a verb seeds %q and the tables name no such reagent mode", name)
		}
	}
}
