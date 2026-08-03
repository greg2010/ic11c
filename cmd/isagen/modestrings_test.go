package main

import (
	"slices"
	"testing"
)

// TestModeStrings covers each indirection the game reaches a device's mode
// names through, and the case that resolves to nothing.
func TestModeStrings(t *testing.T) {
	tests := []struct {
		name   string
		class  string
		want   []string
		wantOK bool
	}{
		{
			name:   "the inherited default",
			class:  "Assets.Scripts.Objects.Pipes.Device",
			want:   []string{"Mode0", "Mode1"},
			wantOK: true,
		},
		{
			name:   "reflection over an enum",
			class:  "Assets.Scripts.Objects.Electrical.Housing",
			want:   []string{"Number", "String"},
			wantOK: true,
		},
		{
			name:   "an enum collection that leaves the names alone",
			class:  "Objects.Structures.Louvre",
			want:   []string{"Retracted", "HalfOpen"},
			wantOK: true,
		},
		{
			name:   "an empty list",
			class:  "Objects.Structures.Vent",
			wantOK: true,
		},
		{
			// Declared here but filled in at runtime, which the extraction
			// cannot follow. Substituting the inherited default would understate
			// how far the device's mode property counts.
			name:  "a field the class fills in elsewhere",
			class: "Objects.Structures.Panel",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := newModeResolver(fixtureTypes(t))
			got, ok, err := resolver.modes(fixtureType(t, tt.class))
			if err != nil {
				t.Fatalf("modes: %v", err)
			}
			if ok != tt.wantOK {
				t.Fatalf("modes resolved = %v, want %v", ok, tt.wantOK)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("modes = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestModesAreStableAcrossPrefabs covers the class whose mode list resolves to
// no names at all, asked for twice as the roster asks it once per prefab of
// that class.
//
// The two answers have to agree. The artifact is a function of its inputs and
// nothing else -- that is what makes the pinned extraction reproducible -- and
// an answer that turned on whether the class had been asked before would make
// the roster order part of the input.
func TestModesAreStableAcrossPrefabs(t *testing.T) {
	resolver := newModeResolver(fixtureTypes(t))
	class := fixtureType(t, "Objects.Structures.Vent")

	first, firstResolved, err := resolver.modes(class)
	if err != nil {
		t.Fatalf("modes: %v", err)
	}
	second, secondResolved, err := resolver.modes(class)
	if err != nil {
		t.Fatalf("modes: %v", err)
	}
	if firstResolved != secondResolved || !slices.Equal(first, second) {
		t.Errorf("modes answered (%q, %v) and then (%q, %v) for one class",
			first, firstResolved, second, secondResolved)
	}
	if !firstResolved {
		t.Errorf("modes reported an empty declared list as unresolved, which is a device with modes this program could not name")
	}
}

// TestModeStringsReportsAnUnreadableEnumDeclaration covers the file declaring a
// nested enum being there and not readable as one type declaration.
//
// The mode names reached through it are unknown, which the artifact already has
// a way to say, but the reason is a change in the layout the whole extraction
// rests on. Reported as an ordinary unresolved list it is one prefab's modes
// going quietly missing; reported as an error it is the one thing a reader has
// to act on.
func TestModeStringsReportsAnUnreadableEnumDeclaration(t *testing.T) {
	const vent = "Objects.Structures.Vent"
	index := perturbedTypes(t, map[string]string{
		"Objects/Structures/Vent.cs": `using Assets.Scripts.Objects.Pipes;

namespace Objects.Structures;

public class Vent : Device
{
	public override string[] ModeStrings => Enum.GetNames(typeof(VentModes.Mode));
}
`,
		"Objects/Structures/VentModes.cs": `namespace Objects.Structures;

public class VentModes
{
	public enum Mode
	{
		Closed,
		Open
	}
}

public class Stray
{
}
`,
	})
	class, err := index.lookup(vent)
	if err != nil || class == nil {
		t.Fatalf("lookup %s: %v", vent, err)
	}
	_, _, err = newModeResolver(index).modes(class)
	checkErr(t, "modes", err, "file for type VentModes declares")
}

// TestToProper covers the game's own rendering of an enum member name into a
// label, which an EnumCollection applies unless its constructor is told not to.
func TestToProper(t *testing.T) {
	tests := []struct{ name, want string }{
		{name: "Idle", want: "Idle"},
		{name: "HalfOpen", want: "Half Open"},
		{name: "RCS", want: "R C S"},
		{name: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := toProper(tt.name); got != tt.want {
				t.Errorf("toProper(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

// TestRendersProper covers the constructor argument that decides how an
// EnumCollection names its members, and in particular that an argument list
// this program does not read leaves the modes unresolved.
//
// The mode names are what a program's mode number is checked against, so a list
// rendered where the game leaves it alone names every setting of that device
// wrong.
func TestRendersProper(t *testing.T) {
	tests := []struct {
		name         string
		args         string
		wantProper   bool
		wantReadable bool
	}{
		{name: "omitted, so the parameter's default stands", args: "", wantProper: true, wantReadable: true},
		{name: "that default written out", args: "toProper: true", wantProper: true, wantReadable: true},
		{name: "suppressed", args: " toProper: false ", wantReadable: true},
		{name: "passed positionally", args: "false"},
		{name: "beside an argument this program does not model", args: "toProper: false, capacity: 4"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proper, readable := rendersProper(tt.args)
			if readable != tt.wantReadable || proper != tt.wantProper {
				t.Errorf("rendersProper(%q) = (%v, %v), want (%v, %v)",
					tt.args, proper, readable, tt.wantProper, tt.wantReadable)
			}
		})
	}
}

func TestEnumMemberNames(t *testing.T) {
	tests := []struct {
		name   string
		src    string
		enum   string
		want   []string
		wantOK bool
	}{
		{
			name:   "dense from zero",
			src:    "public enum M\n{\n\tOff,\n\tOn\n}\n",
			enum:   "M",
			want:   []string{"Off", "On"},
			wantOK: true,
		},
		{
			name:   "declared out of order",
			src:    "public enum M\n{\n\tOn = 1,\n\tOff = 0\n}\n",
			enum:   "M",
			want:   []string{"Off", "On"},
			wantOK: true,
		},
		{
			// A mode number is a position in the name list, so an enum whose
			// values do not run from zero would have the number and the name
			// disagree.
			name: "not starting at zero",
			src:  "public enum M\n{\n\tOff = 1,\n\tOn = 2\n}\n",
			enum: "M",
		},
		{
			name: "not an enum at all",
			src:  "public class M\n{\n}\n",
			enum: "M",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok, err := enumMemberNames(tt.src, tt.enum)
			if err != nil {
				t.Fatalf("enumMemberNames: %v", err)
			}
			if ok != tt.wantOK {
				t.Fatalf("enumMemberNames resolved = %v, want %v", ok, tt.wantOK)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("names = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInitializer(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		want   string
		wantOK bool
	}{
		{
			name:   "expression body",
			text:   "public override string[] ModeStrings => _modeStrings;",
			want:   "_modeStrings",
			wantOK: true,
		},
		{
			name:   "initialized auto-property",
			text:   "public override string[] ModeStrings { get; } = new string[1] { \"Off\" };",
			want:   "new string[1] { \"Off\" }",
			wantOK: true,
		},
		{
			name: "declared with no value",
			text: "private string[] _modeStrings;",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := initializer(tt.text)
			if ok != tt.wantOK {
				t.Fatalf("initializer found = %v, want %v", ok, tt.wantOK)
			}
			if got != tt.want {
				t.Errorf("initializer = %q, want %q", got, tt.want)
			}
		})
	}
}
