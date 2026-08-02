package vm_test

import (
	"slices"
	"testing"

	"github.com/greg2010/ic11c/internal/difftest"
	"github.com/greg2010/ic11c/internal/vm"
)

// TestFuzzCoverageMatchesTheGenerators holds the two fuzz columns of the
// coverage record to what internal/difftest actually generates and excludes,
// failing on either direction of drift the way TestUnitCoverageMatchesTests
// does for the unit column.
//
// It is in the external test package because internal/difftest imports
// internal/vm, so an in-package test file could not name it.
func TestFuzzCoverageMatchesTheGenerators(t *testing.T) {
	var reached, excluded []string
	for _, record := range vm.Coverage() {
		if record.Fuzz {
			reached = append(reached, record.Mnemonic)
		}
		if record.FuzzExcluded {
			excluded = append(excluded, record.Mnemonic)
		}
	}

	tests := []struct {
		column   string
		recorded []string
		want     []string
	}{
		{column: "Fuzz", recorded: reached, want: difftest.GeneratedMnemonics()},
		{column: "FuzzExcluded", recorded: excluded, want: difftest.ExcludedMnemonics()},
	}

	for _, tt := range tests {
		t.Run(tt.column, func(t *testing.T) {
			slices.Sort(tt.recorded)
			slices.Sort(tt.want)
			for _, mnemonic := range tt.recorded {
				if !slices.Contains(tt.want, mnemonic) {
					t.Errorf("%s records %q, which internal/difftest does not", tt.column, mnemonic)
				}
			}
			for _, mnemonic := range tt.want {
				if !slices.Contains(tt.recorded, mnemonic) {
					t.Errorf("internal/difftest has %q, which %s does not record", mnemonic, tt.column)
				}
			}
		})
	}
}
