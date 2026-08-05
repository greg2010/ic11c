package main

import (
	"strings"
	"testing"
)

// The two logic accessors are lifted over injected state, and the edits redirect
// them onto it. An edit matching nothing would leave the arms reaching for a base
// the shim does not have, or -- where the shim declares a member of the same name
// -- compile and answer through something nobody looked at.
func TestCheckEditCounts(t *testing.T) {
	edits := []textEdit{
		{old: "base.IsStructureCompleted", new: "IsStructureCompleted"},
		{old: "base.InternalAtmosphere", new: "InternalAtmosphere", count: 4},
	}

	tests := []struct {
		name    string
		made    map[string]int
		wantErr string
	}{
		{name: "the counts as they stand", made: map[string]int{"base.IsStructureCompleted": 9, "base.InternalAtmosphere": 4}},
		{
			name:    "an arm that stopped reading the atmosphere",
			made:    map[string]int{"base.IsStructureCompleted": 9, "base.InternalAtmosphere": 3},
			wantErr: "found 3",
		},
		{
			name:    "an arm that grew one",
			made:    map[string]int{"base.IsStructureCompleted": 9, "base.InternalAtmosphere": 5},
			wantErr: "found 5",
		},
		{
			name:    "a member that no longer reaches it at all",
			made:    map[string]int{"base.IsStructureCompleted": 9},
			wantErr: "found 0",
		},
		{
			name:    "an uncounted edit that matched nothing",
			made:    map[string]int{"base.InternalAtmosphere": 4},
			wantErr: "edited nowhere in the slice",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := checkEditCounts(edits, test.made, "Device.cs: Device")
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("checkEditCounts: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("checkEditCounts error = %v, want one containing %q", err, test.wantErr)
			}
		})
	}
}
