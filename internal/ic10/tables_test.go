package ic10_test

import (
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/isa"
)

// TestEnumValuesSurviveTheirWidth holds every operand enum member to the
// width this package carries it in. The extraction states a member's
// number as an int64; each family here narrows it to the width the game
// backs that family with. A member the width cannot hold would truncate
// silently and resolve a program's name to a different property.
func TestEnumValuesSurviveTheirWidth(t *testing.T) {
	logicTypes := make([]int64, len(ic10.LogicTypes))
	for i, info := range ic10.LogicTypes {
		logicTypes[i] = int64(info.Value)
	}
	slotTypes := make([]int64, len(ic10.LogicSlotTypes))
	for i, info := range ic10.LogicSlotTypes {
		slotTypes[i] = int64(info.Value)
	}
	batchModes := make([]int64, len(ic10.BatchModes))
	for i, info := range ic10.BatchModes {
		batchModes[i] = int64(info.Value)
	}
	reagentModes := make([]int64, len(ic10.ReagentModes))
	for i, info := range ic10.ReagentModes {
		reagentModes[i] = int64(info.Value)
	}

	tests := []struct {
		family    string
		extracted []isa.EnumMember
		carried   []int64
	}{
		{family: "LogicTypes", extracted: isa.LogicTypes, carried: logicTypes},
		{family: "LogicSlotTypes", extracted: isa.LogicSlotTypes, carried: slotTypes},
		{family: "BatchModes", extracted: isa.BatchModes, carried: batchModes},
		{family: "ReagentModes", extracted: isa.ReagentModes, carried: reagentModes},
	}
	for _, tt := range tests {
		t.Run(tt.family, func(t *testing.T) {
			if len(tt.extracted) == 0 {
				t.Fatalf("nothing was extracted for %s, so this checks nothing", tt.family)
			}
			if len(tt.carried) != len(tt.extracted) {
				t.Fatalf("%s carries %d members and %d were extracted", tt.family, len(tt.carried), len(tt.extracted))
			}
			for i, member := range tt.extracted {
				if tt.carried[i] != member.Value {
					t.Errorf("%s member %s is %d here and %d as extracted", tt.family, member.Name, tt.carried[i], member.Value)
				}
			}
		})
	}
}
