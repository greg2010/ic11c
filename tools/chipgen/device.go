package main

import (
	"fmt"
	"strings"
)

const (
	devicePath    = "Assets/Scripts/Objects/Pipes/Device.cs"
	slotPath      = "Assets/Scripts/Objects/Slot.cs"
	batteryPath   = "Assets/Scripts/Objects/Items/BatteryCell.cs"
	smallGridPath = "Assets/Scripts/Objects/SmallGrid.cs"
	structurePath = "Assets/Scripts/Objects/Structure.cs"
)

// deviceChain is the class chain Device reaches Thing through. The emitted
// device derives from the occupant shim, collapsing the two classes between
// them — sound only because neither declares a member lifted out of Thing.
var deviceChain = []classLink{
	{devicePath, "Device", "SmallGrid"},
	{smallGridPath, "SmallGrid", "Structure"},
	{structurePath, "Structure", "Thing"},
}

// deviceThingMembers are the Thing members the emitted unit answers with
// Thing's own text, each written with the type an override would have to
// repeat.
var deviceThingMembers = []string{
	"GetSlot(",
	"GetFreeSlotCount(",
	"int TotalSlots",
	"int ColorState",
	"int Activate",
	"bool OnOff",
	"int PoweredValue",
	"bool IsOpen",
	"int Mode",
	"int Error",
	"bool IsLocked",
	"bool Powered",
	"string[] DefaultModeStrings",
}

// deviceLifts are the Device members a conformance run compares against: four
// batch reducers, four permission predicates, and the two logic accessors —
// together the whole device side of the instruction set that is not world simulation.
var deviceLifts = []memberLift{
	{signature: "public static double BatchRead(LogicBatchMethod method, LogicType logicType, int deviceHash, List<ILogicable> devices)"},
	{signature: "public static double BatchRead(LogicBatchMethod method, LogicType logicType, int deviceHash, int nameHash, List<ILogicable> devices)"},
	{signature: "public static double BatchRead(LogicBatchMethod method, LogicSlotType logicType, int slotIndex, int deviceHash, List<ILogicable> devices)"},
	{signature: "public static double BatchRead(LogicBatchMethod method, LogicSlotType logicType, int slotIndex, int deviceHash, int nameHash, List<ILogicable> devices)"},
	{signature: "public float UsedPower"},
	{signature: "protected virtual bool IsOperable"},
	{signature: "public CableNetwork PowerCableNetwork"},
	{signature: "public virtual float GetUsedPower(CableNetwork cableNetwork)",
		note: "what RequiredPower answers: -1 for a device on no power cable, 0 for one that is off"},
	{signature: "public override Slot GetSlot(int slotIndex)",
		note: "the guard the two slot accessors below read a slot through"},
	{signature: "public virtual bool CanLogicRead(LogicType logicType)"},
	{signature: "public virtual bool CanLogicWrite(LogicType logicType)"},
	{signature: "public virtual double GetLogicValue(LogicType logicType)"},
	{signature: "public virtual void SetLogicValue(LogicType logicType, double value)",
		note: "the clamp runs ahead of the cast, and the cast is the machine's"},
	{signature: "public virtual bool CanLogicWrite(LogicSlotType logicSlotType, int slotId)"},
	{signature: "public virtual bool CanLogicRead(LogicSlotType logicSlotType, int slotId)"},
	{signature: "public virtual double GetLogicValue(LogicSlotType logicSlotType, int slotId)"},
	{signature: "public virtual void SetLogicValue(LogicSlotType logicSlotType, int slotId, double value)",
		note: "no clamp: the value is cast onto the occupant's interactable as it stands, where\n" +
			"the LogicType arms above narrow theirs first. This is the one write on this class\n" +
			"that can leave a state its own read reports back outside the range it accepts"},
}

// deviceEdits rewrites base-qualified references the shim now declares
// directly, Unity lifetime checks into plain null tests, and unwraps
// ToDouble on the atmosphere's already-double readings. virtual stays: the
// housing shim overrides seven of these members.
var deviceEdits = []textEdit{
	{old: "base.IsStructureCompleted", new: "IsStructureCompleted"},
	{old: "base.ReferenceId", new: "ReferenceId", count: 1},
	{old: "base.InternalAtmosphere", new: "InternalAtmosphere", count: 4},
	{old: "base.Interact", new: "Interact", count: 6},
	{old: "(Object)(object)PowerCable == (Object)null", new: "PowerCable == null", count: 2},
	{old: "Object.op_Implicit((Object)(object)slot.Occupant)", new: "(slot.Occupant != null)", count: 14},
	{old: ".ToDouble()", new: "", count: 6},
}

// textEdit is one text change and the number of times it must be made. A
// zero count applies wherever the text appears; any other count is checked
// across the whole slice, since a stopped match means either uncompilable
// output or, worse, output that compiles over text nobody has read.
type textEdit struct {
	old, new string
	count    int
}

// sliceDevice renders the device shim: the game's own permission, reduction,
// routing and clamping over state a harness sets by hand.
func sliceDevice(s *slicing) (string, error) {
	if err := checkChain(s, deviceChain, deviceThingMembers); err != nil {
		return "", err
	}
	src, err := s.read(devicePath)
	if err != nil {
		return "", err
	}
	device, err := src.topLevelType("Device")
	if err != nil {
		return "", err
	}

	body := src.top().scopeOf(device)
	bodies := make([]string, 0, len(deviceLifts))
	made := make(map[string]int, len(deviceEdits))
	for _, want := range deviceLifts {
		m, err := body.member(want.signature)
		if err != nil {
			return "", fmt.Errorf("%s: Device.%w", src.rel, err)
		}
		text, err := applyEdits(s.strip(m.text), deviceEdits, made,
			src.rel+": Device."+want.signature)
		if err != nil {
			return "", err
		}
		bodies = append(bodies, indent(provenance(body.span(m), want.note)+"\n"+dedent(text)))
	}
	if err := checkEditCounts(deviceEdits, made, src.rel+": Device"); err != nil {
		return "", err
	}

	slot, err := sliceSlotClass(s)
	if err != nil {
		return "", err
	}
	occupant, err := sliceOccupant(s)
	if err != nil {
		return "", err
	}
	accessors, err := sliceThingAccessors(s)
	if err != nil {
		return "", err
	}
	battery, err := sliceBatteryCell(s)
	if err != nil {
		return "", err
	}
	return deviceHeader + slot + "\n\n" + occupant + "\n\n" + deviceShim + accessors + "\n\n" +
		strings.Join(bodies, "\n\n") + "\n}\n\n" + stackableShim + "\n\n" + gasFilterShim + "\n\n" +
		reagentUserShim + "\n\n" + battery + "\n", nil
}

// applyEdits makes every edit that matches text, recording what it made so that
// checkEditCounts can hold a whole slice to the counts its edits named.
func applyEdits(text string, edits []textEdit, made map[string]int, what string) (string, error) {
	for _, edit := range edits {
		found := strings.Count(text, edit.old)
		if found == 0 {
			continue
		}
		made[edit.old] += found
		var err error
		if text, err = replaceExactly(text, edit.old, edit.new, found, what); err != nil {
			return "", err
		}
	}
	return text, nil
}

// batteryLifts are the two BatteryCell members the Charge and ChargeRatio arms
// reach, which is the whole of that class this unit needs.
var batteryLifts = []memberLift{
	{signature: "public float PowerMaximum",
		note: "the prefab's capacity, which the ratio divides by"},
	{signature: "public float PowerRatio"},
	{signature: "public static float GetLogicValue(DynamicThing dynamicThing, LogicSlotType logicSlotType)",
		note: "the whole of the two charge arms; a slot holding anything else answers zero"},
}

// batteryEdits is the Unity lifetime comparison, for the reason deviceEdits
// says. It is parenthesised because the arm negates it.
var batteryEdits = []textEdit{
	{old: "Object.op_Implicit((Object)(object)batteryCell)", new: "(batteryCell != null)", count: 1},
}

// sliceBatteryCell renders the battery a Charge or ChargeRatio slot read is
// answered by, over a stored power a run sets.
func sliceBatteryCell(s *slicing) (string, error) {
	src, err := s.read(batteryPath)
	if err != nil {
		return "", err
	}
	battery, err := src.topLevelType("BatteryCell")
	if err != nil {
		return "", err
	}
	body := src.top().scopeOf(battery)
	made := make(map[string]int, len(batteryEdits))
	parts := []string{batteryStored}
	for _, want := range batteryLifts {
		m, err := body.member(want.signature)
		if err != nil {
			return "", fmt.Errorf("%s: BatteryCell.%w", src.rel, err)
		}
		text, err := applyEdits(s.strip(body.lift(m, want.note)), batteryEdits, made,
			src.rel+": BatteryCell."+want.signature)
		if err != nil {
			return "", err
		}
		parts = append(parts, indent(text))
	}
	if err := checkEditCounts(batteryEdits, made, src.rel+": BatteryCell"); err != nil {
		return "", err
	}
	return batteryHeader + strings.Join(parts, "\n\n") + "\n}", nil
}

const batteryHeader = `// Narrowed from ` + batteryPath + ` to the charge a slot read answers. It
// derives from the device shim for the reason the classes above it do: this
// unit has one thing class, and a battery in a slot is one with stored power.
public class BatteryCell : Device
{
`

// batteryStored is the one reading the lifted members below divide and switch
// on.
const batteryStored = `	// Not lifted: the game's PowerStored clamps into capacity, refuses a NaN,
	// and raises an event on the main thread. A run sets this directly, so it
	// can also be put outside the capacity, which the game's setter would not allow.
	public float PowerStored;`

// stackableShim is the item an occupant quantity read is answered by.
const stackableShim = `// Narrowed from Stackable.cs to the two readings the Quantity and
// MaxQuantity arms take, derived from the device shim like the battery
// below. A plain device is not an IQuantity, which is what keeps the 1.0
// fallback reachable — the game's own answer for something that does not stack.
public class Stackable : Device, IQuantity
{
	public float GetQuantity { get; set; }

	public float GetMaxQuantity { get; set; }
}`

// reagentUserShim stands in for the three game classes that answer
// IRequireReagent (SimpleFabricatorBase, Fabricator, ReagentReader): two of
// lr's four modes and all of rmap cast to the interface and fault on a
// missing one, so one shim class makes both halves of those tests reachable.
const reagentUserShim = `public class ReagentUser : Device, IRequireReagent
{
	// The two tables lr's Required/Recipe modes read, and rmap's prefab mapping.
	// Recipe is a struct, so answering one by property returns a copy a write
	// would silently drop; these are fields, and the interface is implemented
	// explicitly so every reachable spelling is the table itself.
	public Recipe RequiredReagents;

	public Recipe CurrentRecipe;

	// Not lifted: the game walks every ingot prefab for one whose mixture holds
	// the reagent. Which prefabs exist is asset data this tree does not carry,
	// so the mapping is set directly; an unmapped reagent answers 0, as the game's walk does.
	public Dictionary<int, int> ReagentPrefabs = new Dictionary<int, int>();

	Recipe IRequireReagent.RequiredReagents
	{
		get { return RequiredReagents; }
	}

	Recipe IRequireReagent.CurrentRecipe
	{
		get { return CurrentRecipe; }
	}

	int IRequireReagent.GetPrefabHashFromReagentHash(int reagentHash)
	{
		int prefabHash;
		return ReagentPrefabs.TryGetValue(reagentHash, out prefabHash) ? prefabHash : 0;
	}

	// Not lifted and not settable: the interface names it so the class must
	// answer something, and no body here reads it — a verb would set state nothing observes.
	bool IRequireReagent.IsReagentUser
	{
		get { return true; }
	}
}`

// gasFilterShim is the filter a FilterType slot read is answered by.
const gasFilterShim = `// Narrowed from GasFilter.cs to the gas it filters, derived from the device
// shim like the battery below. What a filter does to an atmosphere is
// simulation; the FilterType arm reads only this field.
public class GasFilter : Device
{
	public Chemistry.GasType FilterType;
}`

// checkEditCounts holds every edit to the count it named, over the whole
// slice rather than one body. A stopped match is what a game update looks
// like here: either output that no longer compiles, or — worse — output
// that does, over text nobody has re-read.
func checkEditCounts(edits []textEdit, made map[string]int, what string) error {
	for _, edit := range edits {
		if edit.count == 0 {
			if made[edit.old] == 0 {
				return fmt.Errorf("%s: %.60q is edited nowhere in the slice", what, edit.old)
			}
			continue
		}
		if made[edit.old] != edit.count {
			return fmt.Errorf("%s: expected %d occurrence(s) of %.60q across the slice, found %d",
				what, edit.count, edit.old, made[edit.old])
		}
	}
	return nil
}

// slotLifts are the members of Slot the emitted class takes as written: the
// classification every permission body switches on, and the occupant test the
// two slot-count arms of the value path ask through.
var slotLifts = []memberLift{
	{signature: "Class"},
	{signature: "public bool Contains<T>(out T occupant) where T : IReferencable",
		note: "what the TotalSlots and FreeSlots arms reach the occupant with"},
}

// slotEdits redirects the backing field onto the one this class declares. The
// game's Occupant is a read-only view onto it and the setter is inventory
// plumbing, so here the field is the property.
var slotEdits = []textEdit{
	{old: "_dynamicThing", new: "Occupant", count: 1},
}

// sliceSlotClass lifts the slot classification the slot-permission bodies
// switch on, over the occupant a run puts in the slot. Serialization
// attributes are dropped: they only repeat the member's own name and would
// pull System.Xml behind a unit that needs nothing but the base library.
func sliceSlotClass(s *slicing) (string, error) {
	src, err := s.read(slotPath)
	if err != nil {
		return "", err
	}
	slot, err := src.topLevelType("Slot")
	if err != nil {
		return "", err
	}
	body := src.top().scopeOf(slot)
	made := make(map[string]int, len(slotEdits))
	parts := make([]string, 0, len(slotLifts)+2)
	for _, want := range slotLifts {
		m, err := body.member(want.signature)
		if err != nil {
			return "", fmt.Errorf("%s: Slot.%w", src.rel, err)
		}
		text, err := applyEdits(s.strip(body.lift(m, want.note)), slotEdits, made,
			src.rel+": Slot."+want.signature)
		if err != nil {
			return "", err
		}
		parts = append(parts, indent(text))
	}
	if err := checkEditCounts(slotEdits, made, src.rel+": Slot"); err != nil {
		return "", err
	}
	parts = append(parts, "\tpublic Class Type;", slotOccupantField)
	return slotHeader + strings.Join(parts, "\n\n") + "\n}", nil
}

const slotHeader = `// A slot, reduced to the classification the permission bodies read and the
// occupant every slot read reaches through.
public class Slot
{
`

// slotOccupantField is the occupant, typed as the one thing class this unit has.
const slotOccupantField = `	// Injected: what is in the slot. The game holds a DynamicThing behind a
	// read-only Occupant; the setter is inventory plumbing that moves an object
	// between slots. A run puts an occupant here directly.
	public Device Occupant;`

const deviceHeader = `using System;
using System.Collections.Generic;

`

// deviceShim is the state the lifted bodies read, declared as plain fields.
// In the game each is answered by the prefab, the power network, the
// atmosphere or the interaction system; here a run sets them directly, which
// is what makes every combination a permission body and routing switch reach drivable.
const deviceShim = `public class Device : DynamicThing, ILogicable, ISlotWriteable, IConnected
{
	public bool IsStructureCompleted = true;

	public bool HasColorState, HasActivateState, HasPowerState, HasOpenState;

	public bool HasModeState, HasErrorState, HasLockState, HasOnOffState;

	public bool HasReadableAtmosphere, HasReadableReagentMixture;

	public bool HasAnySlots;

	public int PrefabHash, NameHash;

	// Cable networks reachable from this device, resolved through a network-
	// suffix code. In the game each is found by walking the cable; here they are
	// set directly, and an unset network is one nothing is plugged into.
	public Dictionary<int, ILogicable> Networks = new Dictionary<int, ILogicable>();

	public ReagentMixture ReadableReagentMixture = new ReagentMixture();

	// Injected: the world the lifted logic accessors read through. Which
	// interactable a read or write may reach is gated by the Has*State flags
	// above, the game's own gate. The atmosphere is null until a run supplies
	// one, which is what a device with no internal volume answers zero for.
	public bool IsBroken;

	public Atmosphere InternalAtmosphere;

	public Cable PowerCable;

	public Interactable InteractColor = new Interactable();

	public Interactable InteractActivate = new Interactable();

	public Interactable InteractOnOff = new Interactable();

	public Interactable InteractPowered = new Interactable();

	public Interactable InteractError = new Interactable();

	public Interactable InteractMode = new Interactable();

	public Interactable InteractOpen = new Interactable();

	public Interactable InteractLock = new Interactable();

	// Not lifted: every device prefab overrides DefaultModeStrings with its own
	// mode dial's names. Which strings a prefab names is prefab data; the Mode
	// write below reads only the length.
	public string[] Modes = DefaultModeStrings;

	public virtual string[] ModeStrings
	{
		get { return Modes; }
	}

	// Not lifted: the game reaches this through Thing.GasRatio, dividing a named
	// gas's moles by the mixture's total and answering -1 for an unnamed type.
	// A run sets the ratio it wants each type to answer directly; an unset type
	// reads zero rather than the game's -1.
	public Dictionary<LogicType, double> GasRatios = new Dictionary<LogicType, double>();

	public virtual double GasRatio(LogicType logicType)
	{
		double ratio;
		return GasRatios.TryGetValue(logicType, out ratio) ? ratio : 0.0;
	}

	bool ILogicable.HasAnySlots
	{
		get { return HasAnySlots; }
	}

	public int GetPrefabHash()
	{
		return PrefabHash;
	}

	public int GetNameHash()
	{
		return NameHash;
	}

	public ILogicable GetNetwork(int networkIndex)
	{
		ILogicable found;
		return Networks.TryGetValue(networkIndex, out found) ? found : null;
	}

`
