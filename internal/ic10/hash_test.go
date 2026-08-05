package ic10_test

import (
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
)

// TestHashNameMatchesWhatTheGameStamps holds HashName to values a running
// game actually stamps: if the two disagree, a batch instruction selecting
// on a seeded device's hash matches nothing, and the fixture that compares
// against it passes vacuously on an empty run.
func TestHashNameMatchesWhatTheGameStamps(t *testing.T) {
	tests := []struct {
		name string
		want int
	}{
		{name: "StructureFurnace", want: 1947944864},
		{name: "StructureWallLight", want: -1860064656},
		{name: "north", want: -601214782},
		// A reagent is hashed by its short type name, which is what lr's
		// operand carries and what a seed names it by.
		{name: "Copper", want: -1172078909},
		{name: "Iron", want: -666742878},
		// The empty name is the zero a device left without one carries, and it
		// has to be the hash of nothing rather than a sentinel.
		{name: "", want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ic10.HashName(tt.name); got != tt.want {
				t.Errorf("HashName(%q) is %d, want %d", tt.name, got, tt.want)
			}
		})
	}
}

// TestHashNameReachesBothSignsOfTheThirtyTwoBitRange covers the reinterpretation
// the hash ends in. It is a CRC-32 read as a signed integer, so about half of
// all names hash negative, and a build that read it as unsigned would agree with
// this one on every name whose top bit is clear.
func TestHashNameReachesBothSignsOfTheThirtyTwoBitRange(t *testing.T) {
	if got := ic10.HashName("north"); got >= 0 {
		t.Errorf("HashName(%q) is %d, and the game stamps a negative there", "north", got)
	}
	if got := ic10.HashName("StructureFurnace"); got <= 0 {
		t.Errorf("HashName(%q) is %d, and the game stamps a positive there", "StructureFurnace", got)
	}
}
