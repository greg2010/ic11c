package difftest

import (
	"testing"

	"github.com/greg2010/ic11c/internal/oracle"
)

// TestExcusedExclusionsHaveARegistryEntry mirrors
// oracle.TestEveryDivergenceHasAProbe from the corpus side.
//
// Keeping a mnemonic out because "a divergence entry covers every field" is a
// claim about the registry, and the registry is edited independently. Without
// this, removing an entry that a harness fix made obsolete would leave the
// corpus permanently narrower than it needs to be, and nothing would say so.
func TestExcusedExclusionsHaveARegistryEntry(t *testing.T) {
	checked := 0
	for _, mnemonic := range ExcludedMnemonics() {
		reason, _ := Excluded(mnemonic)
		if reason != reasonExcusesEverything {
			continue
		}
		checked++
		t.Run(mnemonic, func(t *testing.T) {
			var ids []string
			covered := make(map[oracle.Field]bool)
			for _, d := range oracle.Reachable(oracle.IC10Emu, mnemonic) {
				if d.Advisory {
					continue
				}
				ids = append(ids, d.ID)
				for _, field := range d.Fields {
					covered[field] = true
				}
			}
			if len(ids) == 0 {
				t.Fatalf("kept out because an entry excuses every field, but nothing in the "+
					"registry triggers on %q; generate it again or record why it is out", mnemonic)
			}
			for _, field := range oracle.AllFields() {
				if !covered[field] {
					t.Errorf("%v leave %s unexcused, so this no longer has to be kept out", ids, field)
				}
			}
		})
	}
	if checked == 0 {
		t.Errorf("no exclusion cites a divergence entry, so this test asserts nothing")
	}
}
