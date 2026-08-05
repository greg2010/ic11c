package source_test

import (
	"testing"

	"github.com/greg2010/ic11c/internal/source"
)

// TestEnumNameAnswersForATableWithAGap exercises the gap a caller's own enum
// tables cannot: every table in the tree is full, so only an artificial table
// with a hole triggers the empty-entry fallback that flags an unnamed
// constant.
func TestEnumNameAnswersForATableWithAGap(t *testing.T) {
	names := []string{0: "first", 1: "", 2: "third"}

	tests := []struct {
		name string
		v    int
		want string
	}{
		{name: "an entry before the gap", v: 0, want: "first"},
		{name: "the gap", v: 1, want: "K(1)"},
		{name: "the entry after the gap", v: 2, want: "third"},
		{name: "one past the end of the table", v: 3, want: "K(3)"},
		{name: "far past the end of the table", v: 200, want: "K(200)"},
		{name: "below the table", v: -1, want: "K(-1)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := source.EnumName(names, tt.v, "K"); got != tt.want {
				t.Errorf("EnumName(%d) = %q, want %q", tt.v, got, tt.want)
			}
		})
	}
}
