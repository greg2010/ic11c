package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTriAlgebra(t *testing.T) {
	tests := []struct {
		name string
		got  tri
		want tri
	}{
		{name: "not no", got: triNo.not(), want: triYes},
		{name: "not yes", got: triYes.not(), want: triNo},
		{name: "not maybe", got: triMaybe.not(), want: triMaybe},

		// One false operand settles a conjunction whatever the other says, which
		// is what makes the completed-device guard fall away.
		{name: "no and maybe", got: and(triNo, triMaybe), want: triNo},
		{name: "yes and maybe", got: and(triYes, triMaybe), want: triMaybe},
		{name: "yes and yes", got: and(triYes, triYes), want: triYes},

		{name: "yes or maybe", got: or(triYes, triMaybe), want: triYes},
		{name: "no or maybe", got: or(triNo, triMaybe), want: triMaybe},
		{name: "no or no", got: or(triNo, triNo), want: triNo},

		{name: "merge agreeing", got: merge(triYes, triYes), want: triYes},
		{name: "merge disagreeing", got: merge(triYes, triNo), want: triMaybe},
		{name: "merge with maybe", got: merge(triNo, triMaybe), want: triMaybe},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %d, want %d", tt.got, tt.want)
			}
		})
	}
}

// serializedStateFlags are the eleven flags the prefab roster carries per thing,
// which the game's logic surface bodies read a device's own state out of.
var serializedStateFlags = []string{
	"HasErrorState", "HasPowerState", "HasActivateState", "HasLockState",
	"HasOnOffState", "HasModeState", "HasOpenState", "HasImportState",
	"HasExportState", "HasColorState", "HasAccessState",
}

// deviceState is those flags with every one off except those named.
func deviceState(t *testing.T, on ...string) map[string]bool {
	t.Helper()
	state := make(map[string]bool, len(serializedStateFlags))
	for _, flag := range serializedStateFlags {
		state[flag] = false
	}
	for _, flag := range on {
		if _, declared := state[flag]; !declared {
			t.Fatalf("no serialized flag named %s", flag)
		}
		state[flag] = true
	}
	return state
}

// shapeSurface builds the evaluator over an index of the shape corpus.
func shapeSurface(t *testing.T, index *typeIndex, isa *ISA) *logicSurface {
	t.Helper()
	slotClasses, err := slotClassMembers(index)
	if err != nil {
		t.Fatalf("slotClassMembers: %v", err)
	}
	surface, err := newLogicSurface(index, isa, slotClasses)
	if err != nil {
		t.Fatalf("newLogicSurface: %v", err)
	}
	return surface
}

// The classes the whole-device questions below are asked of. Each is named
// because the game writes a particular shape in it.
const (
	gameDeviceClass       = typeDevice
	gameThing             = "Assets.Scripts.Objects.Thing"
	gameAtmosDevice       = "Assets.Scripts.Objects.Pipes.Nitrolyzer"
	gameFoldedGroup       = "Assets.Scripts.Objects.Electrical.AreaPowerControl"
	gameDelegatingWrite   = "Assets.Scripts.Objects.Electrical.PassiveSpeaker"
	gameLiveStateSurface  = "Assets.Scripts.Objects.Electrical.LogicMirror"
	gameOverridingWriteOf = "Objects.Rockets.RocketCelestialTracker"
)

// TestLogicSurfaceOverTheGameSource states, one question at a time, what the
// evaluator makes of each shape the game writes a logic surface in. Every
// expectation is a reading of a real class's body, so a build that rewrote one
// moves the answer and one that removed the class fails the lookup.
func TestLogicSurfaceOverTheGameSource(t *testing.T) {
	tests := []struct {
		name      string
		class     string
		flags     []string
		usedPower float64
		selector  string
		wantRead  tri
		wantWrite tri
	}{
		{
			name:      "a state flag decides the property",
			class:     gameDeviceClass,
			flags:     []string{"HasPowerState"},
			usedPower: 10,
			selector:  "Power",
			wantRead:  triYes,
		},
		{
			name:      "the same flag off closes it",
			class:     gameDeviceClass,
			usedPower: 10,
			selector:  "Power",
		},
		{
			// The one property the base gates on the serialized draw, not a flag.
			name:      "a device drawing nothing exposes no required power",
			class:     gameDeviceClass,
			flags:     []string{"HasPowerState"},
			selector:  "RequiredPower",
			wantRead:  triNo,
			wantWrite: triNo,
		},
		{
			name:      "a device that draws does",
			class:     gameDeviceClass,
			flags:     []string{"HasPowerState"},
			usedPower: 10,
			selector:  "RequiredPower",
			wantRead:  triYes,
		},
		{
			// The base's write side is a switch expression where its read side is
			// a statement switch, so this reads one flag out of both forms.
			name:      "the switch expression form",
			class:     gameDeviceClass,
			flags:     []string{"HasOpenState"},
			usedPower: 10,
			selector:  "Open",
			wantRead:  triYes,
			wantWrite: triYes,
		},
		{
			// The atmosphere properties are a case group answering one class
			// constant, which is how a device says whether it has an atmosphere.
			name:      "a class constant the base consults",
			class:     gameDeviceClass,
			usedPower: 10,
			selector:  "Pressure",
			wantRead:  triNo,
		},
		{
			name:      "an override of that constant",
			class:     gameAtmosDevice,
			usedPower: 10,
			selector:  "Pressure",
			wantRead:  triYes,
		},
		{
			// The compiler folds adjacent members into one unsigned range test,
			// and a class reaches most of what it exposes only through that.
			name:      "a folded case group",
			class:     gameFoldedGroup,
			usedPower: 10,
			selector:  "Maximum",
			wantRead:  triYes,
		},
		{
			name:      "the far end of the same group",
			class:     gameFoldedGroup,
			usedPower: 10,
			selector:  "PowerActual",
			wantRead:  triYes,
		},
		{
			// The member after the group. Read as wrapping, the subtraction
			// would carry every selector above the group into it.
			name:      "a selector past the folded group does not wrap into it",
			class:     gameFoldedGroup,
			usedPower: 10,
			selector:  "Quantity",
			wantRead:  triNo,
		},
		{
			// The base's write side refuses Power and its read side grants it,
			// so following the question's own direction here would answer no.
			name:      "a write delegating to the read implementation",
			class:     gameDelegatingWrite,
			flags:     []string{"HasPowerState"},
			usedPower: 10,
			selector:  "Power",
			wantRead:  triYes,
			wantWrite: triYes,
		},
		{
			// A body answering from another device's surface, a fact about the
			// running game rather than about the class.
			name:      "a surface decided by live state",
			class:     gameLiveStateSurface,
			flags:     []string{"HasPowerState"},
			usedPower: 10,
			selector:  "Power",
			wantRead:  triMaybe,
			wantWrite: triMaybe,
		},
		{
			name:      "an override granting a property the base refuses",
			class:     gameOverridingWriteOf,
			usedPower: 10,
			selector:  "Index",
			wantRead:  triYes,
			wantWrite: triYes,
		},
		{
			name:      "a class with no logic surface at all",
			class:     gameThing,
			flags:     []string{"HasPowerState"},
			usedPower: 10,
			selector:  "Power",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := game(t)
			dev := gameDevice(t, tt.class, nil, tt.usedPower, tt.flags...)
			values := enumValues(g.isa.LogicTypes)
			if _, named := values[tt.selector]; !named {
				t.Fatalf("the game's LogicType no longer declares %s", tt.selector)
			}
			for _, direction := range []struct {
				kind surfaceKind
				want tri
			}{
				{kind: kindReadLogic, want: tt.wantRead},
				{kind: kindWriteLogic, want: tt.wantWrite},
			} {
				got, err := g.surface.can(query{
					device:        dev,
					kind:          direction.kind,
					selector:      tt.selector,
					selectorValue: values[tt.selector],
					slotIndex:     noSlot,
				})
				if err != nil {
					t.Fatalf("can(%s): %v", direction.kind, err)
				}
				if got != direction.want {
					t.Errorf("can(%s, %s) = %d, want %d", direction.kind, tt.selector, got, direction.want)
				}
			}
		})
	}
}

// probeHeader opens every probe below with the imports that put Device and
// LogicType in scope, and with the namespace the file's path declares.
const probeHeader = "using Assets.Scripts.Objects.Motherboards;\nusing Assets.Scripts.Objects.Pipes;\n\nnamespace Objects.Structures;\n\n"

// TestLogicSurfaceMisses states what the evaluator makes of a body it cannot
// read all of. Each shape's miss once collapsed into a decided no, which is the
// one answer the table must never carry: a denial makes the compiler reject a
// program the game runs. An unreadable declaration is an error, not an undecided.
func TestLogicSurfaceMisses(t *testing.T) {
	const probe = "Objects.Structures.Probe"

	tests := []struct {
		name     string
		source   string
		flags    []string
		selector string
		want     tri
		wantErr  string
	}{
		{
			// The compiler folds adjacent members into one pattern, and the
			// decompiler writes that as a disjunction.
			name: "a disjunction of members reaches its arm",
			source: probeHeader + `public class Probe : Device
{
	public override bool CanLogicRead(LogicType logicType)
	{
		return logicType switch
		{
			LogicType.Open or LogicType.Mode => true,
			_ => false,
		};
	}
}
`,
			selector: "Mode",
			want:     triYes,
		},
		{
			name: "a member outside the disjunction still reaches the discard arm",
			source: probeHeader + `public class Probe : Device
{
	public override bool CanLogicRead(LogicType logicType)
	{
		return logicType switch
		{
			LogicType.Open or LogicType.Mode => true,
			_ => false,
		};
	}
}
`,
			selector: "Power",
			want:     triNo,
		},
		{
			// A relational pattern is outside the vocabulary, and the discard arm
			// below is reached only by not matching it.
			name: "a label outside the vocabulary does not fall to the discard arm",
			source: probeHeader + `public class Probe : Device
{
	public override bool CanLogicRead(LogicType logicType)
	{
		return logicType switch
		{
			>= LogicType.Power => true,
			_ => false,
		};
	}
}
`,
			selector: "Open",
			want:     triMaybe,
		},
		{
			name: "an unreadable case label does not fall to the default arm",
			source: probeHeader + `public class Probe : Device
{
	public override bool CanLogicRead(LogicType logicType)
	{
		switch (logicType)
		{
		case >= LogicType.Power:
			return true;
		default:
			return false;
		}
	}
}
`,
			selector: "Open",
			want:     triMaybe,
		},
		{
			// The game runs what follows the switch, not what follows the if.
			name: "a break inside an if leaves the switch",
			source: probeHeader + `public class Probe : Device
{
	public override bool CanLogicRead(LogicType logicType)
	{
		switch (logicType)
		{
		case LogicType.Open:
			if (!HasOpenState)
			{
				break;
			}
			return true;
		}
		return false;
	}
}
`,
			selector: "Open",
			want:     triNo,
		},
		{
			name: "the same body with the flag the break turns on",
			source: probeHeader + `public class Probe : Device
{
	public override bool CanLogicRead(LogicType logicType)
	{
		switch (logicType)
		{
		case LogicType.Open:
			if (!HasOpenState)
			{
				break;
			}
			return true;
		}
		return false;
	}
}
`,
			flags:    []string{"HasOpenState"},
			selector: "Open",
			want:     triYes,
		},
		{
			// The folded form reaches its group by arithmetic, so the bound is a
			// number rather than a name. A member the ISA tables do not carry has
			// no number, and reading the miss as zero invents a bound.
			name: "a folded group bounded by a member the ISA tables do not carry",
			source: probeHeader + `public class Probe : Device
{
	public override bool CanLogicRead(LogicType logicType)
	{
		if (logicType - 1 <= LogicType.Setting)
		{
			return true;
		}
		return false;
	}
}
`,
			selector: "Open",
			want:     triMaybe,
		},
		{
			// The reading the folded group exists to support.
			name: "a folded group bounded by a member they do",
			source: probeHeader + `public class Probe : Device
{
	public override bool CanLogicRead(LogicType logicType)
	{
		if (logicType - 1 <= LogicType.Power)
		{
			return true;
		}
		return false;
	}
}
`,
			selector: "Open",
			want:     triYes,
		},
		{
			name: "a base the tree declares and this program cannot place",
			source: `namespace Objects.Structures;

public class Probe : Housing
{
}
`,
			flags:    []string{"HasPowerState"},
			selector: "Power",
			want:     triMaybe,
		},
		{
			// The same base, named through the alias C# resolves it by.
			name: "a base written through a using alias",
			source: `using Housing = Assets.Scripts.Objects.Electrical.Housing;

namespace Objects.Structures;

public class Probe : Housing
{
}
`,
			flags:    []string{"HasPowerState"},
			selector: "Power",
			want:     triYes,
		},
		{
			// C# requires the base of a base call to declare the method it names,
			// so a base that resolved to nothing was not placed rather than absent.
			name: "a base call whose base did not resolve",
			source: probeHeader + `public class Probe : MonoBehaviour
{
	public bool CanLogicRead(LogicType logicType)
	{
		return base.CanLogicRead(logicType);
	}
}
`,
			selector: "Power",
			want:     triMaybe,
		},
		{
			// The decompiler writes a short body as an expression, which the
			// signature the methods are found by does not end at.
			name: "an expression-bodied override",
			source: probeHeader + `public class Probe : Device
{
	public override bool CanLogicRead(LogicType logicType) => true;
}
`,
			selector: "Open",
			wantErr:  "in a form this program does not read",
		},
		{
			// The same hazard, with the declaration naming the interface instead.
			name: "an explicit interface implementation",
			source: probeHeader + `public class Probe : Device, ILogicable
{
	bool ILogicable.CanLogicRead(LogicType logicType)
	{
		return true;
	}
}
`,
			selector: "Open",
			wantErr:  "in a form this program does not read",
		},
		{
			// The whole-device form and the slot form are separate methods, and
			// almost every class in the game declares one and inherits the other.
			name: "the slot form alone is not a whole-device declaration gone unread",
			source: probeHeader + `public class Probe : Device
{
	public override bool CanLogicRead(LogicSlotType logicSlotType, int slotId)
	{
		return true;
	}
}
`,
			selector: "Open",
			want:     triNo,
		},
		{
			// The statement split closes an if at the brace ending its block, so an
			// else arrives as a statement of its own -- and running it there runs
			// it on the one path the game skips it on.
			name: "an else branch",
			source: probeHeader + `public class Probe : Device
{
	public override bool CanLogicRead(LogicType logicType)
	{
		double limit = 0.0;
		if (logicType == LogicType.Power)
		{
			int ignored = 1;
		}
		else limit = 100.0;
		return UsedPower > limit;
	}
}
`,
			selector: "Power",
			wantErr:  "else branch after an if",
		},
		{
			// A bound the body does write and this cannot represent. Left unknown
			// it takes the comparison with it.
			name: "a numeric bound too large to read",
			source: probeHeader + `public class Probe : Device
{
	public override bool CanLogicRead(LogicType logicType)
	{
		return UsedPower > ` + strings.Repeat("9", 400) + `.0;
	}
}
`,
			selector: "Power",
			wantErr:  "numeric literal",
		},
		{
			// The same, as the offset of the group a selector is tested against.
			name: "a selector offset too large to read",
			source: probeHeader + `public class Probe : Device
{
	public override bool CanLogicRead(LogicType logicType)
	{
		return logicType - 99999999999999999999 <= LogicType.Power;
	}
}
`,
			selector: "Power",
			wantErr:  "offset subtracted from logicType",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			index := perturbedTypes(t, map[string]string{"Objects/Structures/Probe.cs": tt.source})
			class, err := index.lookup(probe)
			if err != nil {
				t.Fatalf("lookup %s: %v", probe, err)
			}
			if class == nil {
				t.Fatalf("lookup %s: not found", probe)
			}
			dev := &device{class: class, state: deviceState(t, tt.flags...), usedPower: 10}

			isa := fixtureISA(t)
			got, err := shapeSurface(t, index, isa).can(query{
				device:        dev,
				kind:          kindReadLogic,
				selector:      tt.selector,
				selectorValue: enumValues(isa.LogicTypes)[tt.selector],
				slotIndex:     noSlot,
			})
			if tt.wantErr != "" {
				checkErr(t, "can", err, tt.wantErr)
				return
			}
			if err != nil {
				t.Fatalf("can: %v", err)
			}
			if got != tt.want {
				t.Errorf("can(%s) = %d, want %d", tt.selector, got, tt.want)
			}
		})
	}
}

// TestLogicSurfaceSlotClasses covers the half of a slot's surface the game
// decides from what the slot accepts. Slot.Class is the one enumeration in these
// bodies the ISA tables do not carry, so a name absent from the assembly's enum
// names no slot, and reading the miss as a non-match denies the property.
func TestLogicSurfaceSlotClasses(t *testing.T) {
	const probe = "Objects.Structures.Probe"

	tests := []struct {
		name      string
		test      string
		slotClass string
		want      tri
	}{
		{
			name:      "the class the body names",
			test:      "type == Slot.Class.Helmet",
			slotClass: "Helmet",
			want:      triYes,
		},
		{
			name:      "a class the body excludes",
			test:      "type == Slot.Class.Suit",
			slotClass: "Helmet",
			want:      triNo,
		},
		{
			name:      "a class the enum does not declare",
			test:      "type == Slot.Class.Vest",
			slotClass: "Helmet",
			want:      triMaybe,
		},
		{
			// The shape Device itself is written in.
			name:      "a disjunction whose other name matches",
			test:      "type == Slot.Class.Vest || type == Slot.Class.Suit",
			slotClass: "Suit",
			want:      triYes,
		},
		{
			name:      "a disjunction whose other name does not",
			test:      "type == Slot.Class.Vest || type == Slot.Class.Suit",
			slotClass: "Helmet",
			want:      triMaybe,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := probeHeader + `public class Probe : Device
{
	public override bool CanLogicRead(LogicSlotType logicSlotType, int slotId)
	{
		Slot slot = GetSlot(slotId);
		Slot.Class type = slot.Type;
		return ` + tt.test + `;
	}
}
`
			index := perturbedTypes(t, map[string]string{"Objects/Structures/Probe.cs": source})
			class, err := index.lookup(probe)
			if err != nil || class == nil {
				t.Fatalf("lookup %s: %v", probe, err)
			}

			isa := fixtureISA(t)
			got, err := shapeSurface(t, index, isa).can(query{
				device: &device{
					class:       class,
					state:       deviceState(t),
					usedPower:   10,
					slotClasses: []string{tt.slotClass},
				},
				kind:          kindReadSlot,
				selector:      "Quantity",
				selectorValue: enumValues(isa.SlotTypes)["Quantity"],
				slotClass:     tt.slotClass,
			})
			if err != nil {
				t.Fatalf("can: %v", err)
			}
			if got != tt.want {
				t.Errorf("can(read Quantity on a %s slot) = %d, want %d", tt.slotClass, got, tt.want)
			}
		})
	}
}

// TestLogicSurfaceUnreadableBase covers a base class whose file is there and will
// not open. Read as a class the assembly does not declare, it would silently
// strip every property the base contributes from every prefab below it.
func TestLogicSurfaceUnreadableBase(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a file whatever its mode")
	}
	const panel = "Objects.Structures.Panel"
	index := perturbedTypes(t, nil)
	class, err := index.lookup(panel)
	if err != nil || class == nil {
		t.Fatalf("lookup %s: %v", panel, err)
	}
	base := filepath.Join(index.tree.root, "Assets", "Scripts", "Objects", "Pipes", "Device.cs")
	if err := os.Chmod(base, 0); err != nil {
		t.Fatalf("chmod fixture: %v", err)
	}

	isa := fixtureISA(t)
	_, err = shapeSurface(t, index, isa).can(query{
		device:        &device{class: class, state: deviceState(t, "HasPowerState"), usedPower: 10},
		kind:          kindReadLogic,
		selector:      "Power",
		selectorValue: enumValues(isa.LogicTypes)["Power"],
		slotIndex:     noSlot,
	})
	checkErr(t, "can", err, "read type Assets.Scripts.Objects.Pipes.Device")
}

// TestLogicSurfaceSlotsOverTheGameSource covers the half of the surface that
// depends on what a slot accepts rather than on the device's own flags. Every
// case is the game's base implementation, where nearly every slot answer in the
// artifact comes from.
func TestLogicSurfaceSlotsOverTheGameSource(t *testing.T) {
	tests := []struct {
		name      string
		slots     []string
		slot      int
		selector  string
		wantRead  tri
		wantWrite tri
	}{
		{
			name:     "readable on any slot",
			slots:    []string{"None"},
			selector: "Occupied",
			wantRead: triYes,
		},
		{
			name:      "the slot class decides both directions",
			slots:     []string{"Helmet"},
			selector:  "Lock",
			wantRead:  triYes,
			wantWrite: triYes,
		},
		{
			name:     "a slot class both directions exclude",
			slots:    []string{"Suit"},
			selector: "Lock",
		},
		{
			name:      "the second slot of a device, on another class of the same group",
			slots:     []string{"None", "Tool"},
			slot:      1,
			selector:  "On",
			wantRead:  triYes,
			wantWrite: triYes,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := game(t)
			dev := gameDevice(t, gameDeviceClass, tt.slots, 10)
			values := enumValues(g.isa.SlotTypes)
			if _, named := values[tt.selector]; !named {
				t.Fatalf("the game's LogicSlotType no longer declares %s", tt.selector)
			}
			for _, direction := range []struct {
				kind surfaceKind
				want tri
			}{
				{kind: kindReadSlot, want: tt.wantRead},
				{kind: kindWriteSlot, want: tt.wantWrite},
			} {
				got, err := g.surface.can(query{
					device:        dev,
					kind:          direction.kind,
					selector:      tt.selector,
					selectorValue: values[tt.selector],
					slotIndex:     tt.slot,
					slotClass:     tt.slots[tt.slot],
				})
				if err != nil {
					t.Fatalf("can(%s): %v", direction.kind, err)
				}
				if got != direction.want {
					t.Errorf("can(%s, %s on a %s slot) = %d, want %d",
						direction.kind, tt.selector, tt.slots[tt.slot], got, direction.want)
				}
			}
		})
	}
}

// TestConstantBoolOverTheGameSource covers the walk that answers a class-level
// property, over the two properties the game decides a whole case group from.
func TestConstantBoolOverTheGameSource(t *testing.T) {
	tests := []struct {
		class string
		name  string
		want  tri
	}{
		{class: gameThing, name: "HasReadableAtmosphere", want: triNo},
		{class: gameDeviceClass, name: "HasReadableAtmosphere", want: triNo},
		{class: gameAtmosDevice, name: "HasReadableAtmosphere", want: triYes},
		{class: gameThing, name: "HasReadableReagentMixture", want: triNo},
		{class: "Assets.Scripts.Objects.Pipes.Centrifuge", name: "HasReadableReagentMixture", want: triYes},
	}
	for _, tt := range tests {
		t.Run(tt.class+"."+tt.name, func(t *testing.T) {
			got, err := game(t).surface.constantBool(gameClass(t, tt.class), tt.name)
			if err != nil {
				t.Fatalf("constantBool: %v", err)
			}
			if got != tt.want {
				t.Errorf("constantBool(%s, %s) = %d, want %d", tt.class, tt.name, got, tt.want)
			}
		})
	}
}

// TestConstantBoolStopsAtAnUnmodelledDeclaration covers the walk meeting a class
// that answers in a form this program does not model; reporting the literal it
// inherits would put an answer the class replaced into every prefab below it. It
// uses the shape corpus, since no class the game ships is written that way.
func TestConstantBoolStopsAtAnUnmodelledDeclaration(t *testing.T) {
	got, err := shapeSurface(t, shapeTypes(t), fixtureISA(t)).
		constantBool(shapeType(t, "Assets.Scripts.Objects.Thing"), "HasReadableReagentMixture")
	if err != nil {
		t.Fatalf("constantBool: %v", err)
	}
	if got != triMaybe {
		t.Errorf("constantBool = %d, want %d", got, triMaybe)
	}
}

// TestMentionsOverTheGameSource covers the cost shortcut and the one case that
// disables it: a class reaching selectors by arithmetic names none of them.
func TestMentionsOverTheGameSource(t *testing.T) {
	tests := []struct {
		name       string
		class      string
		wantNamed  []string
		wantNoneOf []string
		wantAll    bool
	}{
		{
			name:       "names the properties its bodies switch on",
			class:      gameDeviceClass,
			wantNamed:  []string{"Power", "Open", "Mode"},
			wantNoneOf: []string{"Setting"},
		},
		{
			name:    "a class testing the selector by arithmetic names none",
			class:   gameFoldedGroup,
			wantAll: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			named, err := game(t).surface.mentions(gameClass(t, tt.class), kindReadLogic)
			if err != nil {
				t.Fatalf("mentions: %v", err)
			}
			if tt.wantAll {
				if named != nil {
					t.Errorf("mentions = %v, want the shortcut disabled", named)
				}
				return
			}
			for _, name := range tt.wantNamed {
				if !named[name] {
					t.Errorf("mentions does not name %s", name)
				}
			}
			for _, name := range tt.wantNoneOf {
				if named[name] {
					t.Errorf("mentions names %s, which no body mentions", name)
				}
			}
		})
	}
}

func TestEveryEnumValueIsNamed(t *testing.T) {
	for f := flowFell; f <= flowBroke; f++ {
		if s := f.String(); strings.Contains(s, "flow(") {
			t.Errorf("flow %d has no name", f)
		}
	}
	if s := (flowBroke + 1).String(); !strings.Contains(s, "flow(") {
		t.Errorf("flow(%d) is named %q, so the loop above stops short of the last flow", flowBroke+1, s)
	}
	for k := kindReadLogic; k <= kindWriteSlot; k++ {
		if s := k.String(); strings.Contains(s, "surfaceKind(") {
			t.Errorf("surface kind %d has no name", k)
		}
	}
	if s := (kindWriteSlot + 1).String(); !strings.Contains(s, "surfaceKind(") {
		t.Errorf("surfaceKind(%d) is named %q, so the loop above stops short of the last kind", kindWriteSlot+1, s)
	}
}
