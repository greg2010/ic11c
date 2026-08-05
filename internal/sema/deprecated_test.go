package sema_test

import (
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/source"
)

// TestDeprecatedMachineNameWarns covers the 23 of 358 logic types the game
// marks retired, and the same flag on the other operand enums.
//
// It is a warning rather than a rejection. The chip resolves a retired member
// exactly like any other and the emitted program is unchanged, so refusing
// would reject a program that works; what the programmer cannot see without
// being told is that the game has moved the property and may stop maintaining
// this one.
func TestDeprecatedMachineNameWarns(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "a deprecated logic type in a store",
			src:  "void main(void) { __ic_store(d0, PlantHealth1, 1); }",
			want: "PlantHealth1",
		},
		{
			name: "a deprecated logic type in a load",
			src:  "void main(void) { __ic_store(d0, Setting, __ic_load(d0, PlantHealth1)); }",
			want: "PlantHealth1",
		},
		{
			name: "a deprecated slot type",
			src:  "void main(void) { __ic_store(d0, Setting, __ic_load_slot(d0, 0, Growth)); }",
			want: "Growth",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, diags := analyze(t, tt.src)
			if diags.HasErrors() {
				t.Fatalf("a deprecated name was rejected rather than reported:\n%s", diags.String())
			}
			if len(diags) != 1 {
				t.Fatalf("want one warning, got %d:\n%s", len(diags), diags.String())
			}
			if diags[0].Severity != source.Warning {
				t.Errorf("the diagnostic is a %v, want a warning: %s", diags[0].Severity, diags[0].Error())
			}
			if !strings.Contains(diags[0].Msg, tt.want) {
				t.Errorf("the warning does not name %q: %s", tt.want, diags[0].Msg)
			}
			if !strings.Contains(diags[0].Msg, "deprecated") {
				t.Errorf("the warning does not say what is wrong: %s", diags[0].Msg)
			}
			if !diags[0].Pos.IsValid() {
				t.Errorf("the warning carries no source position: %s", diags[0].Error())
			}
		})
	}
}

// TestCurrentMachineNameIsSilent is the other half: nothing the game still
// maintains produces a diagnostic.
func TestCurrentMachineNameIsSilent(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{name: "a current logic type", src: "void main(void) { __ic_store(d0, Setting, 1); }"},
		{name: "a current slot type", src: "void main(void) { __ic_store(d0, Setting, __ic_load_slot(d0, 0, Occupied)); }"},
		{name: "a batch mode", src: "void main(void) { __ic_store(d0, Setting, __ic_load_batch(__ic_hash(\"StructureStubSensor\"), Temperature, Average)); }"},
		{name: "a reagent mode", src: "void main(void) { __ic_store(d0, Setting, __ic_load_reagent(d0, Contents, 1)); }"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, diags := analyze(t, tt.src)
			if len(diags) != 0 {
				t.Errorf("a current machine name was reported:\n%s", diags.String())
			}
		})
	}
}
