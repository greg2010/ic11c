package main

import (
	"fmt"
	"strings"
)

const thingPath = "Assets/Scripts/Objects/Thing.cs"

// thingLifts are the Thing members the device shim carries as written.
//
// Thing itself is a MonoBehaviour three classes above Device and cannot be
// lifted, but these two declarations reach nothing outside the unit.
var thingLifts = []memberLift{
	{container: "Thing", signature: "public static string[] DefaultModeStrings",
		note: "the two modes a prefab that names none of its own still publishes"},
	{container: "Thing", signature: "public virtual bool Powered"},
}

// occupantLifts are the Thing members a slot read reaches through the
// thing in the slot rather than through the device the slot belongs to.
// They land on the class the device shim derives from, since Thing sits
// above both Device and DynamicThing, so a device and an occupant share this code.
var occupantLifts = []memberLift{
	{container: "Thing", signature: "public virtual int TotalSlots"},
	{container: "Thing", signature: "public virtual Slot GetSlot(int slotIndex)",
		note: "unguarded; Device overrides it with the range check the slot accessors rely on"},
	{container: "Thing", signature: "public int GetFreeSlotCount()",
		note: "the whole of what the FreeSlots arm answers"},
}

// occupantEdits is the Unity lifetime comparison, for the reason deviceEdits
// says.
var occupantEdits = []textEdit{
	{old: "(Object)(object)slot.Occupant == (Object)null", new: "slot.Occupant == null", count: 1},
}

// sliceOccupant renders the class a slot's occupant is reached as.
func sliceOccupant(s *slicing) (string, error) {
	src, err := s.read(thingPath)
	if err != nil {
		return "", err
	}
	thing, err := src.topLevelType("Thing")
	if err != nil {
		return "", err
	}
	body := src.top().scopeOf(thing)
	made := make(map[string]int, len(occupantEdits))
	parts := []string{occupantState}
	for _, want := range occupantLifts {
		m, err := body.member(want.signature)
		if err != nil {
			return "", fmt.Errorf("%s: Thing.%w", src.rel, err)
		}
		text, err := applyEdits(s.strip(body.lift(m, want.note)), occupantEdits, made,
			src.rel+": Thing."+want.signature)
		if err != nil {
			return "", err
		}
		parts = append(parts, indent(text))
	}
	if err := checkEditCounts(occupantEdits, made, src.rel+": Thing"); err != nil {
		return "", err
	}
	return occupantHeader + strings.Join(parts, "\n\n") + "\n}", nil
}

const occupantHeader = `// The thing in a slot, which is the same object as the device on a pin. The
// game puts a DynamicThing in a slot and a Device on a pin, different
// branches under Thing; every slot-reachable accessor is already on the
// device shim, so this unit has one thing class and the device shim is it.
public class DynamicThing : IReferencable
{
`

// occupantState is what the world and the prefab carried.
const occupantState = `	// Injected: state assigned by a registry, a prefab and the damage model.
	// ReferenceId comes from the world's object registry, which this unit does
	// not have; DisplayName is the batch sort's key, set directly here — two
	// things can share one name in game, and since List.Sort is not stable, a run whose answer depends on order must give its devices distinct names.
	public long ReferenceId { get; set; }

	public string DisplayName { get; set; } = string.Empty;

	public Slot.Class SlotType;

	public SortingClass SortingClass;

	public IndestructableDamageState DamageState = new IndestructableDamageState();

	public List<Slot> Slots = new List<Slot>();`

// thingAccessor is one of the state properties Device.GetLogicValue and
// Device.SetLogicValue read. Each is written as a branch for an object with
// no base animator plus a frame-cached animator branch; since a harness
// device is never a scene object, only the no-animator constant is reachable.
type thingAccessor struct {
	// signature is the property as Thing declares it.
	signature string
	// headless is the whole condition of the branch an object with no animator
	// takes, including the animator term. Requiring it verbatim is what stops
	// the slice when the property is rewritten.
	headless string
	// guard is what is left of that condition once the animator term is gone.
	guard string
	// fallback is what the animator branch computes when there is no animator.
	fallback string
	// pre is the condition of a statement that runs ahead of the branch, lifted
	// with its block. Empty when there is none.
	pre string
}

// thingAccessors is every state property the two lifted logic accessors reach.
//
// Export, Import and the button states are left out: no LogicType in
// Device.GetLogicValue's switch reads them.
var thingAccessors = []thingAccessor{
	{signature: "public virtual int ColorState", headless: "if (HasColorState && !HasBaseAnimator)", guard: "HasColorState", fallback: "0"},
	{signature: "public virtual int Activate", headless: "if (!HasBaseAnimator && HasActivateState)", guard: "HasActivateState", fallback: "0", pre: "if (ReferenceId == 0L)"},
	{signature: "public virtual bool OnOff", headless: "if (HasOnOffState && !HasBaseAnimator)", guard: "HasOnOffState", fallback: "false"},
	{signature: "public virtual int PoweredValue", headless: "if (HasPowerState && !HasBaseAnimator)", guard: "HasPowerState", fallback: "0"},
	{signature: "public virtual bool IsOpen", headless: "if (HasOpenState && !HasBaseAnimator)", guard: "HasOpenState", fallback: "false"},
	{signature: "public virtual int Mode", headless: "if (HasModeState && !HasBaseAnimator)", guard: "HasModeState", fallback: "0"},
	{signature: "public virtual int Error", headless: "if (HasErrorState && !HasBaseAnimator)", guard: "HasErrorState", fallback: "0"},
	{signature: "public virtual bool IsLocked", headless: "if (HasLockState && !HasBaseAnimator)", guard: "HasLockState", fallback: "false"},
}

// sliceThingAccessors renders the eight state properties, each over the branch
// the game's own getter takes when there is no animator.
func sliceThingAccessors(s *slicing) (string, error) {
	src, err := s.read(thingPath)
	if err != nil {
		return "", err
	}
	thing, err := src.topLevelType("Thing")
	if err != nil {
		return "", err
	}
	body := src.top().scopeOf(thing)

	parts := make([]string, 0, len(thingAccessors))
	for _, want := range thingAccessors {
		m, err := body.member(want.signature)
		if err != nil {
			return "", fmt.Errorf("%s: Thing.%w", src.rel, err)
		}
		text := s.strip(m.text)
		taken, err := blockAfter(text, want.headless)
		if err != nil {
			return "", fmt.Errorf("%s: Thing.%s: %w", src.rel, want.signature, err)
		}
		var preamble string
		if want.pre != "" {
			block, err := blockAfter(text, want.pre)
			if err != nil {
				return "", fmt.Errorf("%s: Thing.%s: %w", src.rel, want.signature, err)
			}
			preamble = fmt.Sprintf("\t\t%s\n\t\t{\n%s\n\t\t}\n", want.pre, block)
		}
		parts = append(parts, indent(fmt.Sprintf(
			"// %s, the branch an object with no base animator takes.\n%s\n{\n\tget\n\t{\n%s\t\tif (%s)\n\t\t{\n%s\n\t\t}\n\t\treturn %s;\n\t}\n}",
			body.span(m), want.signature, preamble, want.guard, taken, want.fallback)))
	}

	lifts, err := liftMembers(s, thingLifts, thingPath)
	if err != nil {
		return "", err
	}
	return strings.Join(append(lifts, parts...), "\n\n"), nil
}

// blockAfter returns the statements of the braced block following the single
// occurrence of head, indented three levels in, which is where the emitted
// property puts them.
func blockAfter(text, head string) (string, error) {
	_, after, ok := strings.Cut(text, head)
	if !ok {
		return "", fmt.Errorf("branch %q: %w", head, errNotFound)
	}
	if strings.Contains(after, head) {
		return "", fmt.Errorf("branch %q appears more than once", head)
	}
	inner, _, err := matchDelim(after, 0, '{', '}')
	if err != nil {
		return "", fmt.Errorf("branch %q: %w", head, err)
	}
	var lines []string
	depth := -1
	for line := range strings.SplitSeq(inner, "\n") {
		if strings.TrimLeft(line, "\t") == "" {
			continue
		}
		if own := len(line) - len(strings.TrimLeft(line, "\t")); depth < 0 || own < depth {
			depth = own
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return "", fmt.Errorf("branch %q has an empty block", head)
	}
	for i, line := range lines {
		lines[i] = "\t\t\t" + line[depth:]
	}
	return strings.Join(lines, "\n"), nil
}
