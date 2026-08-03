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

		// One false operand settles a conjunction whatever the other says,
		// which is what makes the completed-device guard fall away rather than
		// drag the unknowable game state into every answer.
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

// deviceState is the eleven flags Device switches on, with every one off except
// those named.
func deviceState(t *testing.T, on ...string) map[string]bool {
	t.Helper()
	state := map[string]bool{
		"HasErrorState": false, "HasPowerState": false, "HasActivateState": false,
		"HasLockState": false, "HasOnOffState": false, "HasModeState": false,
		"HasOpenState": false, "HasImportState": false, "HasExportState": false,
		"HasColorState": false, "HasAccessState": false,
	}
	for _, flag := range on {
		if _, declared := state[flag]; !declared {
			t.Fatalf("no serialized flag named %s", flag)
		}
		state[flag] = true
	}
	return state
}

// fixtureSurface builds the evaluator over an index of the decompiled stand-in,
// with the Slot.Class enum that stand-in declares.
func fixtureSurface(t *testing.T, index *typeIndex, isa *ISA) *logicSurface {
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

// fixtureDevice builds a device out of the hand-written decompiled stand-in,
// with every state flag off except the ones named.
func fixtureDevice(t *testing.T, class string, slotClasses []string, usedPower float64, on ...string) *device {
	t.Helper()
	return &device{
		class:       fixtureType(t, class),
		state:       deviceState(t, on...),
		usedPower:   usedPower,
		slotClasses: slotClasses,
	}
}

// TestLogicSurface states, one question at a time, what the evaluator makes of
// each shape the game writes a logic surface in.
func TestLogicSurface(t *testing.T) {
	const (
		device  = "Assets.Scripts.Objects.Pipes.Device"
		sensor  = "Assets.Scripts.Objects.Pipes.Sensor"
		housing = "Assets.Scripts.Objects.Electrical.Housing"
		mirror  = "Assets.Scripts.Objects.Electrical.Mirror"
		panel   = "Objects.Structures.Panel"
		thing   = "Assets.Scripts.Objects.Thing"
	)

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
			name:      "state flag decides the property",
			class:     device,
			flags:     []string{"HasPowerState"},
			usedPower: 10,
			selector:  "Power",
			wantRead:  triYes,
		},
		{
			name:     "a device drawing nothing exposes no power",
			class:    device,
			flags:    []string{"HasPowerState"},
			selector: "Power",
			wantRead: triNo,
		},
		{
			name:      "state flag off closes the property",
			class:     device,
			usedPower: 10,
			selector:  "Power",
		},
		{
			name:      "the switch expression form",
			class:     device,
			flags:     []string{"HasOpenState"},
			selector:  "Open",
			wantWrite: triYes,
		},
		{
			name:     "a class constant the base consults",
			class:    device,
			selector: "Open",
			wantRead: triNo,
		},
		{
			name:     "an override of that constant",
			class:    sensor,
			selector: "Open",
			wantRead: triYes,
		},
		{
			// The compiler folds adjacent cases into one unsigned range test,
			// and a class reaches most of what it exposes only through that.
			name:      "a folded case group",
			class:     panel,
			selector:  "Open",
			wantRead:  triYes,
			wantWrite: triYes,
		},
		{
			name:      "the same group's other member",
			class:     panel,
			flags:     []string{"HasModeState"},
			selector:  "Mode",
			wantRead:  triYes,
			wantWrite: triYes,
		},
		{
			name:      "a selector below the folded group does not wrap into it",
			class:     panel,
			usedPower: 10,
			selector:  "Power",
			wantRead:  triNo,
			wantWrite: triNo,
		},
		{
			// The game has classes whose CanLogicWrite delegates to
			// base.CanLogicRead. Following the question's own direction there
			// would report a property the game refuses, or the reverse.
			name:      "a write delegating to the read implementation",
			class:     panel,
			flags:     []string{"HasModeState"},
			selector:  "Mode",
			wantRead:  triYes,
			wantWrite: triYes,
		},
		{
			name:      "a surface decided by live state",
			class:     mirror,
			flags:     []string{"HasPowerState"},
			usedPower: 10,
			selector:  "Power",
			wantRead:  triMaybe,
		},
		{
			name:     "a class with no logic surface at all",
			class:    thing,
			selector: "Power",
		},
		{
			name:     "a property no body names",
			class:    housing,
			selector: "Open",
			wantRead: triNo,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dev := fixtureDevice(t, tt.class, nil, tt.usedPower, tt.flags...)
			surface := fixtureSurface(t, fixtureTypes(t), fixtureISA(t))
			values := enumValues(fixtureISA(t).LogicTypes)
			for _, direction := range []struct {
				kind surfaceKind
				want tri
			}{
				{kind: kindReadLogic, want: tt.wantRead},
				{kind: kindWriteLogic, want: tt.wantWrite},
			} {
				got, err := surface.can(query{
					device:        dev,
					kind:          direction.kind,
					selector:      tt.selector,
					selectorValue: values[tt.selector],
					slotIndex:     noSlot,
				})
				if err != nil {
					t.Fatalf("can(%d): %v", direction.kind, err)
				}
				if got != direction.want {
					t.Errorf("can(%d, %s) = %d, want %d", direction.kind, tt.selector, got, direction.want)
				}
			}
		})
	}
}

// probeHeader opens every stand-in below with the imports that put Device and
// LogicType in scope, and with the namespace the file's path declares.
const probeHeader = "using Assets.Scripts.Objects.Motherboards;\nusing Assets.Scripts.Objects.Pipes;\n\nnamespace Objects.Structures;\n\n"

// TestLogicSurfaceMisses states what the evaluator makes of a body it cannot
// read all of.
//
// Each of these is a shape whose miss used to collapse into a decided no. That
// is the one answer the table must never carry: a decided denial makes the
// compiler reject a program the game runs, which is the mistake the extraction
// exists to prevent. An undecided answer costs a check; a wrong one costs a
// build.
//
// A declaration this program cannot read at all is an error rather than an
// undecided answer. There is no body to be undecided about: the alternative is
// to walk on to the base and answer with an implementation the class replaced.
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
			// decompiler writes that as a disjunction. Reading it is what keeps
			// the arm from being missed in the first place.
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
			// A relational pattern is outside the vocabulary. The discard arm
			// below is reached only by not matching it, which an unread label
			// does not say.
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
			// The arm leaves the switch, so the game runs what follows the
			// switch and not what follows the if.
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
			// The folded form reaches its group by arithmetic, so the bound is
			// a number rather than a name. A member the ISA tables do not carry
			// has no number, and reading the miss as zero decides every
			// selector against a bound the body never wrote.
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
			// The same shape with a bound the tables do carry, which is the
			// reading the folded group exists to support.
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
			// The base list names a class the tree declares under a namespace
			// this resolution does not reach, so the whole of what Device says
			// went unread.
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
			// C# requires the base of a base call to declare the method it
			// names, so a base that resolved to nothing was not placed rather
			// than absent.
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
			// signature the methods are found by does not end at. Read as a class
			// declaring nothing, the walk goes on to Device and answers Open with
			// Device's body, which denies what this class grants.
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
			// The same hazard written the other way the game reaches these
			// methods: the declaration names the interface it satisfies.
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
			// Reading that as a declaration gone unread would stop the extraction
			// on most of the roster.
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
			// The statement split closes an if at the brace ending its block, so
			// an else arrives as a statement of its own. Run there it runs on the
			// path the if took, which is the one path the game skips it on: the
			// bound below would be read as 100 where the game leaves it at 0, and
			// the device would be reported as refusing a property it answers.
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
			// A bound the body does write and this program cannot represent. Left
			// unknown it takes the comparison with it, and the property it decides
			// silently becomes one the game answers from live state.
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
			// The same, written as the offset of the contiguous group the game
			// tests a selector against.
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
			got, err := fixtureSurface(t, index, isa).can(query{
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
// decides from what the slot accepts.
//
// The Slot.Class members are the one enumeration in these bodies the ISA tables
// do not carry, so a reference to one is held to the enum the assembly declares.
// A name absent from that enum names no slot in the roster, and reading the miss
// as a non-match denies the property on every slot the body grants it to.
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
			// The shape Device itself is written in. One unreadable name leaves
			// the other free to decide the disjunction where it matches.
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
			got, err := fixtureSurface(t, index, isa).can(query{
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

// TestLogicSurfaceUnreadableBase covers a base class whose file is there and
// will not open. Reading that as a class the assembly does not declare strips
// every property the base contributes from every prefab below it, which is a
// silent wrong answer where the read failure is a loud one.
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
	_, err = fixtureSurface(t, index, isa).can(query{
		device:        &device{class: class, state: deviceState(t, "HasPowerState"), usedPower: 10},
		kind:          kindReadLogic,
		selector:      "Power",
		selectorValue: enumValues(isa.LogicTypes)["Power"],
		slotIndex:     noSlot,
	})
	checkErr(t, "can", err, "read type Assets.Scripts.Objects.Pipes.Device")
}

// TestLogicSurfaceSlots covers the half of the surface that depends on what a
// slot accepts rather than on the device's own flags.
func TestLogicSurfaceSlots(t *testing.T) {
	tests := []struct {
		name      string
		class     string
		slots     []string
		slot      int
		selector  string
		wantRead  tri
		wantWrite tri
	}{
		{
			name:     "readable on any slot",
			class:    "Assets.Scripts.Objects.Pipes.Device",
			slots:    []string{"Suit"},
			selector: "Occupied",
			wantRead: triYes,
		},
		{
			name:      "the slot class decides both directions",
			class:     "Assets.Scripts.Objects.Pipes.Device",
			slots:     []string{"Helmet"},
			selector:  "Quantity",
			wantRead:  triYes,
			wantWrite: triYes,
		},
		{
			name:     "a slot class the write side excludes",
			class:    "Assets.Scripts.Objects.Pipes.Device",
			slots:    []string{"Suit"},
			selector: "Quantity",
			wantRead: triYes,
		},
		{
			name:      "an override reaching past the slot class",
			class:     "Assets.Scripts.Objects.Electrical.Housing",
			slots:     []string{"None"},
			selector:  "Quantity",
			wantRead:  triYes,
			wantWrite: triNo,
		},
		{
			name:      "the second slot of a device",
			class:     "Assets.Scripts.Objects.Pipes.Device",
			slots:     []string{"None", "Helmet"},
			slot:      1,
			selector:  "Quantity",
			wantRead:  triYes,
			wantWrite: triYes,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dev := fixtureDevice(t, tt.class, tt.slots, 10)
			surface := fixtureSurface(t, fixtureTypes(t), fixtureISA(t))
			values := enumValues(fixtureISA(t).SlotTypes)
			for _, direction := range []struct {
				kind surfaceKind
				want tri
			}{
				{kind: kindReadSlot, want: tt.wantRead},
				{kind: kindWriteSlot, want: tt.wantWrite},
			} {
				got, err := surface.can(query{
					device:        dev,
					kind:          direction.kind,
					selector:      tt.selector,
					selectorValue: values[tt.selector],
					slotIndex:     tt.slot,
					slotClass:     tt.slots[tt.slot],
				})
				if err != nil {
					t.Fatalf("can(%d): %v", direction.kind, err)
				}
				if got != direction.want {
					t.Errorf("can(%d, %s) = %d, want %d", direction.kind, tt.selector, got, direction.want)
				}
			}
		})
	}
}

// TestConstantBool covers the walk that answers a class-level property, and in
// particular that a class declaring it in a form this program does not model
// stops the walk rather than reporting the literal it inherits.
func TestConstantBool(t *testing.T) {
	tests := []struct {
		class string
		name  string
		want  tri
	}{
		{class: "Assets.Scripts.Objects.Thing", name: "HasReadableAtmosphere", want: triNo},
		{class: "Assets.Scripts.Objects.Pipes.Device", name: "HasReadableAtmosphere", want: triNo},
		{class: "Assets.Scripts.Objects.Pipes.Sensor", name: "HasReadableAtmosphere", want: triYes},
		{class: "Assets.Scripts.Objects.Thing", name: "HasReadableReagentMixture", want: triMaybe},
	}
	for _, tt := range tests {
		t.Run(tt.class+"."+tt.name, func(t *testing.T) {
			surface := fixtureSurface(t, fixtureTypes(t), fixtureISA(t))
			got, err := surface.constantBool(fixtureType(t, tt.class), tt.name)
			if err != nil {
				t.Fatalf("constantBool: %v", err)
			}
			if got != tt.want {
				t.Errorf("constantBool(%s, %s) = %d, want %d", tt.class, tt.name, got, tt.want)
			}
		})
	}
}

// TestMentions covers the cost shortcut and the one case that disables it: a
// class reaching selectors by arithmetic names none of them.
func TestMentions(t *testing.T) {
	tests := []struct {
		name       string
		class      string
		wantNamed  []string
		wantNoneOf []string
		wantAll    bool
	}{
		{
			name:       "names the properties its bodies switch on",
			class:      "Assets.Scripts.Objects.Pipes.Device",
			wantNamed:  []string{"Power", "Open", "Mode"},
			wantNoneOf: []string{"Setting"},
		},
		{
			name:    "a class testing the selector by arithmetic names none",
			class:   "Objects.Structures.Panel",
			wantAll: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			surface := fixtureSurface(t, fixtureTypes(t), fixtureISA(t))
			named, err := surface.mentions(fixtureType(t, tt.class), kindReadLogic)
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
